package journal

import (
	"fmt"
	"time"

	"github.com/aalsanie/distroplane/internal/domain"
)

// AuthorizeAmbiguousRetry records a one-time, operator-approved retry of an
// ambiguously dispatched npm publication. It never invokes the provider.
// The caller must obtain explicit acknowledgement of duplicate-publication risk.
// Only a successful reconciliation observing the version absent is eligible;
// an absent observation is not a guarantee that the earlier publish cannot arrive.
//
// Validation and durable append occur under the journal writer lock so another
// goroutine cannot change the operation state between authorization and append.
func (w *Writer) AuthorizeAmbiguousRetry(plan domain.Plan, runID domain.RunID, operationID domain.OperationID) (Event, error) {
	if w == nil {
		return Event{}, ErrWriterClosed
	}
	if !operationID.Valid() {
		return Event{}, fmt.Errorf("operation ID is invalid")
	}
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.file == nil || w.closed {
		return Event{}, ErrWriterClosed
	}
	if w.poisoned {
		return Event{}, ErrWriterPoisoned
	}
	if w.runID != runID {
		return Event{}, fmt.Errorf("journal belongs to run %q", w.runID)
	}
	if len(w.events) == 0 {
		return Event{}, fmt.Errorf("retry authorization requires a started journal")
	}
	state, err := Reduce(plan, w.events)
	if err != nil {
		return Event{}, err
	}
	operation, ok := state.Operation(operationID)
	if !ok {
		return Event{}, fmt.Errorf("operation %q does not belong to the plan", operationID)
	}
	entry := Entry{
		RunID: runID, Type: EventRetryAuthorized,
		OperationID: operation.ID, TargetID: operation.TargetID,
		Payload: Payload{Attempt: operation.Attempt},
	}
	// Validate the complete proposed transition before making an append
	// irreversible. In particular, no invalid authorization can poison the journal.
	proposed := Event{
		SchemaVersion: SchemaVersion, Sequence: w.next, RunID: runID,
		Type: entry.Type, OperationID: entry.OperationID, TargetID: entry.TargetID,
		ObservedAt: time.Now().UTC(), Payload: entry.Payload,
	}
	events := append(cloneEvents(w.events), proposed)
	if _, err := Reduce(plan, events); err != nil {
		return Event{}, err
	}
	return w.appendLocked(entry)
}
