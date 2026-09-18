package executor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/aalsanie/distroplane/internal/domain"
)

var ErrDriverContract = errors.New("driver contract violation")

type Result struct {
	State         domain.NormalizedState
	ProviderState string
	Evidence      json.RawMessage
}

func (r Result) validate() error {
	switch r.State {
	case domain.StateWaitingExternal, domain.StatePublished, domain.StateRejected:
	default:
		return fmt.Errorf("%w: invalid result state %q", ErrDriverContract, r.State)
	}
	if err := validateText("provider state", r.ProviderState, true); err != nil {
		return fmt.Errorf("%w: %v", ErrDriverContract, err)
	}
	if len(r.Evidence) == 0 || !json.Valid(r.Evidence) {
		return fmt.Errorf("%w: evidence must be valid JSON", ErrDriverContract)
	}
	return nil
}

type DriverError struct {
	Code      string
	Message   string
	Retryable bool
	Ambiguous bool
}

func (e *DriverError) Error() string {
	if e == nil {
		return ""
	}
	if e.Message != "" {
		return e.Message
	}
	if e.Code != "" {
		return e.Code
	}
	return "driver error"
}

func (e *DriverError) valid() bool {
	return e != nil && validateText("driver error code", e.Code, false) == nil
}

type Previous struct {
	State         domain.NormalizedState
	ProviderState string
	Evidence      json.RawMessage
	ErrorCode     string
	Ambiguous     bool
}

type Request struct {
	PlanID    domain.PlanID
	RunID     domain.RunID
	Operation domain.Operation
	Attempt   uint32
	Previous  *Previous
}

type Driver interface {
	Apply(context.Context, Request) (Result, error)
	Reconcile(context.Context, Request) (Result, error)
}

type Backoff func(completedAttempt uint32) time.Duration

type Options struct {
	MaxConcurrency int
	MaxAttempts    uint32
	Backoff        Backoff
	Leases         LeaseManager
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
