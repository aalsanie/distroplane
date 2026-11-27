package journal

import (
	"errors"
	"fmt"

	"github.com/aalsanie/distroplane/internal/domain"
)

var ErrRetryAuthorizationDisabled = errors.New("new ambiguous-publication retry authorization is disabled; inspect the original run and reconcile the destination without republishing")
var ErrLegacyRetryPending = errors.New("unconsumed historical retry authorization; preserve the original plan and journal, inspect the destination and seek manual recovery; do not republish")

type legacyV2Retry struct {
	eligible       bool
	observedAbsent bool
}

func legacyV2RetryFor(operation domain.Operation) legacyV2Retry {
	return legacyV2Retry{eligible: operation.SideEffecting() && operation.Provider().Name() == "npm" && operation.Kind() == "publish"}
}

func (r *legacyV2Retry) observe(payload Payload) {
	r.observedAbsent = payload.State == domain.StateWaitingExternal && payload.ProviderState == "absent" && payload.ErrorCode == ""
}

func replayLegacyV2Authorization(operation *mutableOperation, event Event) error {
	if !operation.legacyRetry.eligible || operation.State != domain.StateWaitingExternal || !operation.ReconcileRequired ||
		!operation.Ambiguous || !operation.legacyRetry.observedAbsent || operation.reconciling ||
		operation.LeaseActive || event.Payload.Attempt != operation.Attempt {
		return fmt.Errorf("operation %q has no eligible historical reconciled absent publication", operation.ID)
	}
	operation.legacyRetry.observedAbsent = false
	operation.legacyRetryPending = true
	operation.State = domain.StateReady
	operation.Retryable = false
	operation.ReconcileRequired = false
	operation.Ambiguous = false
	operation.ProviderState = ""
	operation.Evidence = nil
	operation.ErrorCode = ""
	return nil
}

func (d DerivedState) CheckExecutionAllowed() error {
	if d.Completed {
		return nil
	}
	for _, operation := range d.operations {
		if operation.legacyRetryPending {
			return fmt.Errorf("operation %q: %w", operation.ID, ErrLegacyRetryPending)
		}
	}
	return nil
}

func CheckHistoricalExecution(plan domain.Plan, events []Event) error {
	for _, event := range events {
		if event.Type == EventRetryAuthorized {
			state, err := Reduce(plan, events)
			if err != nil {
				return err
			}
			return state.CheckExecutionAllowed()
		}
	}
	return nil
}
