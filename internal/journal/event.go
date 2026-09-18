package journal

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/aalsanie/distroplane/internal/domain"
)

const (
	SchemaVersion  uint16 = 1
	MaxRecordBytes        = 1 << 20
)

type EventType string

const (
	EventRunStarted           EventType = "RUN_STARTED"
	EventAttemptStarted       EventType = "ATTEMPT_STARTED"
	EventSideEffectDispatched EventType = "SIDE_EFFECT_DISPATCHED"
	EventOperationResult      EventType = "OPERATION_RESULT"
	EventOutcomeAmbiguous     EventType = "OUTCOME_AMBIGUOUS"
	EventReconcileStarted     EventType = "RECONCILE_STARTED"
	EventReconcileResult      EventType = "RECONCILE_RESULT"
)

var (
	ErrCorruptRecord      = errors.New("corrupt journal record")
	ErrSequence           = errors.New("invalid journal sequence")
	ErrUnsupportedVersion = errors.New("unsupported journal schema version")
	ErrUnknownEventType   = errors.New("unknown journal event type")
	ErrWriterLocked       = errors.New("journal writer is locked")
	ErrWriterPoisoned     = errors.New("journal writer is poisoned")
	ErrWriterClosed       = errors.New("journal writer is closed")
)

type Payload struct {
	Attempt       uint32                 `json:"attempt,omitempty"`
	State         domain.NormalizedState `json:"state,omitempty"`
	ProviderState string                 `json:"providerState,omitempty"`
	Evidence      json.RawMessage        `json:"evidence,omitempty"`
	ErrorCode     string                 `json:"errorCode,omitempty"`
}

type Entry struct {
	RunID       domain.RunID       `json:"runId"`
	Type        EventType          `json:"type"`
	OperationID domain.OperationID `json:"operationId,omitempty"`
	TargetID    domain.TargetID    `json:"targetId,omitempty"`
	Payload     Payload            `json:"payload"`
}

type Event struct {
	SchemaVersion uint16             `json:"schemaVersion"`
	Sequence      uint64             `json:"sequence"`
	RunID         domain.RunID       `json:"runId"`
	Type          EventType          `json:"type"`
	OperationID   domain.OperationID `json:"operationId,omitempty"`
	TargetID      domain.TargetID    `json:"targetId,omitempty"`
	ObservedAt    time.Time          `json:"observedAt"`
	Payload       Payload            `json:"payload"`
}

func (t EventType) valid() bool {
	switch t {
	case EventRunStarted, EventAttemptStarted, EventSideEffectDispatched, EventOperationResult, EventOutcomeAmbiguous, EventReconcileStarted, EventReconcileResult:
		return true
	default:
		return false
	}
}

func (e Entry) validate() error {
	if !e.RunID.Valid() {
		return fmt.Errorf("run ID is invalid")
	}
	if !e.Type.valid() {
		return fmt.Errorf("%w %q", ErrUnknownEventType, e.Type)
	}
	if e.Type == EventRunStarted {
		if e.OperationID != "" || e.TargetID != "" || !e.Payload.empty() {
			return fmt.Errorf("run-started event must not contain operation, target, or payload data")
		}
		return nil
	}
	if !e.OperationID.Valid() {
		return fmt.Errorf("operation ID is invalid")
	}
	if !e.TargetID.Valid() {
		return fmt.Errorf("target ID is invalid")
	}
	return e.Payload.validate(e.Type)
}

func (e Event) validate() error {
	if e.SchemaVersion != SchemaVersion {
		return fmt.Errorf("%w %d", ErrUnsupportedVersion, e.SchemaVersion)
	}
	if e.Sequence == 0 {
		return fmt.Errorf("%w: sequence must be greater than zero", ErrSequence)
	}
	if e.ObservedAt.IsZero() {
		return fmt.Errorf("observation timestamp is required")
	}
	return (Entry{RunID: e.RunID, Type: e.Type, OperationID: e.OperationID, TargetID: e.TargetID, Payload: e.Payload}).validate()
}

func (p Payload) empty() bool {
	return p.Attempt == 0 && p.State == "" && p.ProviderState == "" && len(p.Evidence) == 0 && p.ErrorCode == ""
}

func (p Payload) validate(eventType EventType) error {
	switch eventType {
	case EventAttemptStarted, EventSideEffectDispatched, EventOutcomeAmbiguous, EventReconcileStarted:
		if p.Attempt == 0 {
			return fmt.Errorf("attempt must be greater than zero")
		}
		if p.State != "" || p.ProviderState != "" || len(p.Evidence) != 0 {
			return fmt.Errorf("event %q contains unsupported result payload", eventType)
		}
		if eventType != EventOutcomeAmbiguous && p.ErrorCode != "" {
			return fmt.Errorf("event %q must not contain an error code", eventType)
		}
	case EventOperationResult, EventReconcileResult:
		if p.Attempt == 0 {
			return fmt.Errorf("attempt must be greater than zero")
		}
		if !resultState(p.State) {
			return fmt.Errorf("invalid result state %q", p.State)
		}
	default:
		return fmt.Errorf("%w %q", ErrUnknownEventType, eventType)
	}
	if err := validateText("provider state", p.ProviderState, true); err != nil {
		return err
	}
	if err := validateText("error code", p.ErrorCode, true); err != nil {
		return err
	}
	if len(p.Evidence) != 0 {
		if err := validateEvidence(p.Evidence); err != nil {
			return err
		}
	}
	return nil
}

func resultState(state domain.NormalizedState) bool {
	switch state {
	case domain.StateWaitingExternal, domain.StatePublished, domain.StateRejected, domain.StateFailed, domain.StateCancelled:
		return true
	default:
		return false
	}
}

func validateEvidence(raw []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return fmt.Errorf("invalid evidence: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return fmt.Errorf("invalid evidence: trailing JSON data")
	}
	if containsSecretField(value) {
		return fmt.Errorf("evidence contains a secret-bearing field")
	}
	return nil
}

func containsSecretField(value any) bool {
	switch typed := value.(type) {
	case map[string]any:
		for key, child := range typed {
			if secretKey(key) || containsSecretField(child) {
				return true
			}
		}
	case []any:
		for _, child := range typed {
			if containsSecretField(child) {
				return true
			}
		}
	}
	return false
}

func secretKey(key string) bool {
	var b strings.Builder
	for _, r := range strings.ToLower(key) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
		}
	}
	switch b.String() {
	case "authorization", "credential", "credentials", "password", "passwd", "secret", "token", "accesstoken", "refreshtoken", "apikey", "clientsecret", "privatekey":
		return true
	default:
		return false
	}
}

func validateText(name, value string, optional bool) error {
	if value == "" {
		if optional {
			return nil
		}
		return fmt.Errorf("%s must not be empty", name)
	}
	if !utf8.ValidString(value) {
		return fmt.Errorf("%s must be valid UTF-8", name)
	}
	if len(value) > 256 {
		return fmt.Errorf("%s exceeds 256 bytes", name)
	}
	if strings.TrimSpace(value) != value {
		return fmt.Errorf("%s must not have surrounding whitespace", name)
	}
	for _, r := range value {
		if unicode.IsControl(r) {
			return fmt.Errorf("%s must not contain control characters", name)
		}
	}
	return nil
}
