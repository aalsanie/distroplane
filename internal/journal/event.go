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
	AttemptReasonInitial   = "INITIAL"
	AttemptReasonRetry     = "RETRY"
	AttemptReasonReconcile = "RECONCILE"

	ResultCategoryPublished       = "PUBLISHED"
	ResultCategoryWaitingExternal = "WAITING_EXTERNAL"
	ResultCategoryRejected        = "REJECTED"
	ResultCategoryFailedRetryable = "FAILED_RETRYABLE"
	ResultCategoryFailedPermanent = "FAILED_PERMANENT"
	ResultCategoryCancelled       = "CANCELLED"
	ResultCategoryAmbiguous       = "AMBIGUOUS"
)

const (
	EventRunStarted               EventType = "RUN_STARTED"
	EventRunCompleted             EventType = "RUN_COMPLETED"
	EventRunCancelled             EventType = "RUN_CANCELLED"
	EventOperationReady           EventType = "OPERATION_READY"
	EventLeaseAcquired            EventType = "LEASE_ACQUIRED"
	EventLeaseRenewed             EventType = "LEASE_RENEWED"
	EventLeaseExpired             EventType = "LEASE_EXPIRED"
	EventLeaseReleased            EventType = "LEASE_RELEASED"
	EventAttemptStarted           EventType = "ATTEMPT_STARTED"
	EventCredentialResolved       EventType = "CREDENTIAL_RESOLVED"
	EventProviderProcessStarted   EventType = "PROVIDER_PROCESS_STARTED"
	EventSideEffectDispatched     EventType = "SIDE_EFFECT_DISPATCHED"
	EventProviderResponseReceived EventType = "PROVIDER_RESPONSE_RECEIVED"
	EventOperationWaitingExternal EventType = "OPERATION_WAITING_EXTERNAL"
	EventOperationPublished       EventType = "OPERATION_PUBLISHED"
	EventOperationRejected        EventType = "OPERATION_REJECTED"
	EventOperationFailed          EventType = "OPERATION_FAILED"
	EventOperationResult          EventType = "OPERATION_RESULT"
	EventOperationCancelled       EventType = "OPERATION_CANCELLED"
	EventOutcomeAmbiguous         EventType = "OUTCOME_AMBIGUOUS"
	EventReconcileStarted         EventType = "RECONCILE_STARTED"
	EventReconcileResult          EventType = "RECONCILE_RESULT"
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

type LeasePayload struct {
	ID         string    `json:"id"`
	Owner      string    `json:"owner"`
	AcquiredAt time.Time `json:"acquiredAt"`
	ExpiresAt  time.Time `json:"expiresAt"`
}

type Payload struct {
	Attempt        uint32                 `json:"attempt,omitempty"`
	AttemptReason  string                 `json:"attemptReason,omitempty"`
	ResultCategory string                 `json:"resultCategory,omitempty"`
	CredentialRef  domain.CredentialRef   `json:"credentialRef,omitempty"`
	State          domain.NormalizedState `json:"state,omitempty"`
	ProviderState  string                 `json:"providerState,omitempty"`
	Evidence       json.RawMessage        `json:"evidence,omitempty"`
	ErrorCode      string                 `json:"errorCode,omitempty"`
	Retryable      bool                   `json:"retryable,omitempty"`
	Lease          *LeasePayload          `json:"lease,omitempty"`
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
	case EventRunStarted, EventRunCompleted, EventRunCancelled, EventOperationReady, EventLeaseAcquired, EventLeaseRenewed,
		EventLeaseExpired, EventLeaseReleased, EventAttemptStarted, EventCredentialResolved, EventProviderProcessStarted, EventSideEffectDispatched,
		EventProviderResponseReceived, EventOperationWaitingExternal, EventOperationPublished, EventOperationRejected,
		EventOperationFailed, EventOperationResult, EventOperationCancelled, EventOutcomeAmbiguous,
		EventReconcileStarted, EventReconcileResult:
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
	if e.Type == EventRunStarted || e.Type == EventRunCompleted || e.Type == EventRunCancelled {
		if e.OperationID != "" || e.TargetID != "" || !e.Payload.empty() {
			return fmt.Errorf("run lifecycle event %q must not contain operation, target, or payload data", e.Type)
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
	return p.Attempt == 0 && p.AttemptReason == "" && p.ResultCategory == "" && p.CredentialRef == "" &&
		p.State == "" && p.ProviderState == "" && len(p.Evidence) == 0 && p.ErrorCode == "" && !p.Retryable && p.Lease == nil
}

func (p Payload) validate(eventType EventType) error {
	switch eventType {
	case EventOperationReady:
		if !p.empty() {
			return fmt.Errorf("event %q must not contain payload data", eventType)
		}
	case EventLeaseAcquired, EventLeaseRenewed, EventLeaseExpired, EventLeaseReleased:
		if p.Attempt != 0 || p.AttemptReason != "" || p.ResultCategory != "" || p.CredentialRef != "" || p.State != "" || p.ProviderState != "" || len(p.Evidence) != 0 || p.ErrorCode != "" || p.Retryable {
			return fmt.Errorf("event %q contains unsupported non-lease payload", eventType)
		}
		if p.Lease == nil {
			return fmt.Errorf("event %q requires lease metadata", eventType)
		}
		return p.Lease.validate()
	case EventOperationCancelled:
		if p.Attempt != 0 || p.AttemptReason != "" || p.ResultCategory != "" || p.CredentialRef != "" || p.State != "" || p.ProviderState != "" || len(p.Evidence) != 0 || p.Retryable {
			return fmt.Errorf("operation-cancelled event contains unsupported payload")
		}
	case EventCredentialResolved:
		if p.Attempt == 0 {
			return fmt.Errorf("attempt must be greater than zero")
		}
		if !p.CredentialRef.Valid() {
			return fmt.Errorf("credential reference is invalid")
		}
		if p.AttemptReason != "" || p.ResultCategory != "" || p.State != "" || p.ProviderState != "" || len(p.Evidence) != 0 || p.ErrorCode != "" || p.Retryable {
			return fmt.Errorf("credential-resolved event contains unsupported payload")
		}
	case EventAttemptStarted, EventProviderProcessStarted, EventSideEffectDispatched, EventProviderResponseReceived, EventOutcomeAmbiguous, EventReconcileStarted:
		if p.Attempt == 0 {
			return fmt.Errorf("attempt must be greater than zero")
		}
		if p.CredentialRef != "" || p.State != "" || p.ProviderState != "" || len(p.Evidence) != 0 || p.Retryable {
			return fmt.Errorf("event %q contains unsupported result payload", eventType)
		}
		switch eventType {
		case EventAttemptStarted, EventReconcileStarted:
			if p.ResultCategory != "" {
				return fmt.Errorf("event %q must not contain a result category", eventType)
			}
		case EventOutcomeAmbiguous:
			if p.AttemptReason != "" {
				return fmt.Errorf("event %q must not contain an attempt reason", eventType)
			}
		default:
			if p.AttemptReason != "" || p.ResultCategory != "" {
				return fmt.Errorf("event %q contains unsupported attempt metadata", eventType)
			}
		}
		if eventType != EventOutcomeAmbiguous && p.ErrorCode != "" {
			return fmt.Errorf("event %q must not contain an error code", eventType)
		}
	case EventOperationWaitingExternal, EventOperationPublished, EventOperationRejected, EventOperationFailed:
		if p.AttemptReason != "" {
			return fmt.Errorf("result event must not contain an attempt reason")
		}
		if p.CredentialRef != "" {
			return fmt.Errorf("result event must not contain a credential reference")
		}
		if p.Attempt == 0 {
			return fmt.Errorf("attempt must be greater than zero")
		}
		if p.State != "" {
			return fmt.Errorf("explicit result event %q must not contain a state", eventType)
		}
		state, _ := explicitResultState(eventType)
		if p.Retryable && state != domain.StateFailed && state != domain.StateWaitingExternal {
			return fmt.Errorf("state %q cannot be retryable", state)
		}
	case EventOperationResult, EventReconcileResult:
		if p.AttemptReason != "" {
			return fmt.Errorf("result event must not contain an attempt reason")
		}
		if p.CredentialRef != "" {
			return fmt.Errorf("result event must not contain a credential reference")
		}
		if p.Attempt == 0 {
			return fmt.Errorf("attempt must be greater than zero")
		}
		if !resultState(p.State) {
			return fmt.Errorf("invalid result state %q", p.State)
		}
		if p.Retryable && p.State != domain.StateFailed && p.State != domain.StateWaitingExternal {
			return fmt.Errorf("state %q cannot be retryable", p.State)
		}
	default:
		return fmt.Errorf("%w %q", ErrUnknownEventType, eventType)
	}
	if p.Lease != nil {
		return fmt.Errorf("event %q must not contain lease metadata", eventType)
	}
	if err := validateText("attempt reason", p.AttemptReason, true); err != nil {
		return err
	}
	if p.AttemptReason != "" && !validAttemptReason(p.AttemptReason) {
		return fmt.Errorf("invalid attempt reason %q", p.AttemptReason)
	}
	if err := validateText("result category", p.ResultCategory, true); err != nil {
		return err
	}
	if p.ResultCategory != "" && !validResultCategory(p.ResultCategory) {
		return fmt.Errorf("invalid result category %q", p.ResultCategory)
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

func validAttemptReason(value string) bool {
	switch value {
	case AttemptReasonInitial, AttemptReasonRetry, AttemptReasonReconcile:
		return true
	default:
		return false
	}
}

func validResultCategory(value string) bool {
	switch value {
	case ResultCategoryPublished, ResultCategoryWaitingExternal, ResultCategoryRejected,
		ResultCategoryFailedRetryable, ResultCategoryFailedPermanent, ResultCategoryCancelled,
		ResultCategoryAmbiguous:
		return true
	default:
		return false
	}
}

func (p LeasePayload) validate() error {
	if err := validateText("lease ID", p.ID, false); err != nil {
		return err
	}
	if err := validateText("lease owner", p.Owner, false); err != nil {
		return err
	}
	if p.AcquiredAt.IsZero() {
		return fmt.Errorf("lease acquired time is required")
	}
	if p.ExpiresAt.IsZero() {
		return fmt.Errorf("lease expiry is required")
	}
	if !p.ExpiresAt.After(p.AcquiredAt) {
		return fmt.Errorf("lease expiry must be after acquisition")
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

func explicitResultState(eventType EventType) (domain.NormalizedState, bool) {
	switch eventType {
	case EventOperationWaitingExternal:
		return domain.StateWaitingExternal, true
	case EventOperationPublished:
		return domain.StatePublished, true
	case EventOperationRejected:
		return domain.StateRejected, true
	case EventOperationFailed:
		return domain.StateFailed, true
	default:
		return "", false
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

func cloneEvent(event Event) Event {
	event.Payload.Evidence = append(json.RawMessage(nil), event.Payload.Evidence...)
	if event.Payload.Lease != nil {
		lease := *event.Payload.Lease
		event.Payload.Lease = &lease
	}
	return event
}

func cloneEvents(events []Event) []Event {
	result := make([]Event, len(events))
	for i, event := range events {
		result[i] = cloneEvent(event)
	}
	return result
}
