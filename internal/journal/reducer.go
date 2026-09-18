package journal

import (
	"encoding/json"
	"fmt"
	"sort"

	"github.com/aalsanie/distroplane/internal/domain"
)

type OperationState struct {
	ID                domain.OperationID
	TargetID          domain.TargetID
	State             domain.NormalizedState
	Attempt           uint32
	ReconcileRequired bool
	Ambiguous         bool
	Retryable         bool
	ProviderState     string
	Evidence          json.RawMessage
	ErrorCode         string
}

type TargetState struct {
	ID                domain.TargetID
	State             domain.NormalizedState
	ReconcileRequired bool
	Ambiguous         bool
}

type DerivedState struct {
	RunID      domain.RunID
	Started    bool
	Completed  bool
	Cancelled  bool
	operations []OperationState
	targets    []TargetState
}

func (d DerivedState) Operations() []OperationState {
	result := make([]OperationState, len(d.operations))
	for i, state := range d.operations {
		result[i] = cloneOperationState(state)
	}
	return result
}

func (d DerivedState) Targets() []TargetState {
	return append([]TargetState(nil), d.targets...)
}

func (d DerivedState) Operation(id domain.OperationID) (OperationState, bool) {
	i := sort.Search(len(d.operations), func(i int) bool { return string(d.operations[i].ID) >= string(id) })
	if i == len(d.operations) || d.operations[i].ID != id {
		return OperationState{}, false
	}
	return cloneOperationState(d.operations[i]), true
}

func (d DerivedState) Target(id domain.TargetID) (TargetState, bool) {
	i := sort.Search(len(d.targets), func(i int) bool { return string(d.targets[i].ID) >= string(id) })
	if i == len(d.targets) || d.targets[i].ID != id {
		return TargetState{}, false
	}
	return d.targets[i], true
}

type mutableOperation struct {
	OperationState
	dependencies  []domain.OperationID
	sideEffecting bool
	dispatched    bool
	reconciling   bool
}

func Reduce(plan domain.Plan, events []Event) (DerivedState, error) {
	if !plan.ID().Valid() {
		return DerivedState{}, fmt.Errorf("plan is invalid")
	}
	operations := make(map[domain.OperationID]*mutableOperation, len(plan.Operations()))
	for _, operation := range plan.Operations() {
		operations[operation.ID()] = &mutableOperation{
			OperationState: OperationState{ID: operation.ID(), TargetID: operation.TargetID(), State: domain.StatePlanned},
			dependencies:   operation.Dependencies(),
			sideEffecting:  operation.SideEffecting(),
		}
	}
	targets := make(map[domain.TargetID][]domain.OperationID, len(plan.Targets()))
	for _, target := range plan.Targets() {
		targets[target.ID()] = nil
	}
	for _, operation := range plan.Operations() {
		targets[operation.TargetID()] = append(targets[operation.TargetID()], operation.ID())
	}

	var runID domain.RunID
	started := false
	completed := false
	cancelled := false
	var expected uint64 = 1
	for _, event := range events {
		if err := event.validate(); err != nil {
			return DerivedState{}, fmt.Errorf("event %d: %w", expected, err)
		}
		if event.Sequence != expected {
			return DerivedState{}, fmt.Errorf("%w: got %d, want %d", ErrSequence, event.Sequence, expected)
		}
		expected++
		if runID == "" {
			runID = event.RunID
		} else if event.RunID != runID {
			return DerivedState{}, fmt.Errorf("event sequence contains multiple run IDs")
		}
		if !started && event.Type != EventRunStarted {
			return DerivedState{}, fmt.Errorf("first reducer event must be %q", EventRunStarted)
		}
		if completed {
			return DerivedState{}, fmt.Errorf("event %q follows terminal run event", event.Type)
		}
		if cancelled && event.Type != EventReconcileStarted && event.Type != EventReconcileResult {
			return DerivedState{}, fmt.Errorf("event %q is not allowed after run cancellation", event.Type)
		}
		switch event.Type {
		case EventRunStarted:
			if started {
				return DerivedState{}, fmt.Errorf("run already started")
			}
			started = true
			refreshReady(operations)
		case EventRunCompleted:
			if !allQuiescent(operations) {
				return DerivedState{}, fmt.Errorf("run cannot complete with non-terminal operations")
			}
			completed = true
		case EventRunCancelled:
			cancelled = true
			cancelRemaining(operations)
		case EventOperationCancelled:
			operation, err := eventOperation(event, operations)
			if err != nil {
				return DerivedState{}, err
			}
			if operation.State != domain.StatePlanned && operation.State != domain.StateReady {
				return DerivedState{}, fmt.Errorf("operation %q cannot be cancelled from state %q", operation.ID, operation.State)
			}
			operation.State = domain.StateCancelled
			operation.Retryable = false
			operation.ErrorCode = event.Payload.ErrorCode
			refreshReady(operations)
		case EventAttemptStarted:
			operation, err := eventOperation(event, operations)
			if err != nil {
				return DerivedState{}, err
			}
			if operation.State != domain.StateReady && !(operation.State == domain.StateFailed && operation.Retryable) {
				return DerivedState{}, fmt.Errorf("operation %q cannot start attempt from state %q", operation.ID, operation.State)
			}
			if operation.ReconcileRequired {
				return DerivedState{}, fmt.Errorf("operation %q requires reconciliation before another attempt", operation.ID)
			}
			if event.Payload.Attempt != operation.Attempt+1 {
				return DerivedState{}, fmt.Errorf("operation %q attempt is %d, want %d", operation.ID, event.Payload.Attempt, operation.Attempt+1)
			}
			if !dependenciesPublished(operation, operations) {
				return DerivedState{}, fmt.Errorf("operation %q dependencies are not published", operation.ID)
			}
			operation.State = domain.StateRunning
			operation.Attempt = event.Payload.Attempt
			operation.dispatched = false
			operation.reconciling = false
			operation.ReconcileRequired = false
			operation.Ambiguous = false
			operation.Retryable = false
			operation.ProviderState = ""
			operation.Evidence = nil
			operation.ErrorCode = ""
		case EventSideEffectDispatched:
			operation, err := eventOperation(event, operations)
			if err != nil {
				return DerivedState{}, err
			}
			if !operation.sideEffecting {
				return DerivedState{}, fmt.Errorf("operation %q is not side-effecting", operation.ID)
			}
			if operation.State != domain.StateRunning || event.Payload.Attempt != operation.Attempt {
				return DerivedState{}, fmt.Errorf("operation %q has no matching running attempt", operation.ID)
			}
			if operation.dispatched {
				return DerivedState{}, fmt.Errorf("operation %q side effect already dispatched", operation.ID)
			}
			operation.dispatched = true
			operation.ReconcileRequired = true
			operation.Ambiguous = true
		case EventOutcomeAmbiguous:
			operation, err := eventOperation(event, operations)
			if err != nil {
				return DerivedState{}, err
			}
			if !operation.sideEffecting || !operation.dispatched || operation.State != domain.StateRunning || event.Payload.Attempt != operation.Attempt {
				return DerivedState{}, fmt.Errorf("operation %q cannot record an ambiguous outcome", operation.ID)
			}
			operation.ReconcileRequired = true
			operation.Ambiguous = true
			operation.Retryable = false
			operation.ErrorCode = event.Payload.ErrorCode
		case EventOperationResult:
			operation, err := eventOperation(event, operations)
			if err != nil {
				return DerivedState{}, err
			}
			if operation.State != domain.StateRunning || event.Payload.Attempt != operation.Attempt {
				return DerivedState{}, fmt.Errorf("operation %q has no matching running attempt", operation.ID)
			}
			if operation.sideEffecting && !operation.dispatched && event.Payload.State != domain.StateFailed && event.Payload.State != domain.StateCancelled {
				return DerivedState{}, fmt.Errorf("operation %q result recorded before side-effect dispatch", operation.ID)
			}
			applyResult(operation, event.Payload, false)
			refreshReady(operations)
		case EventReconcileStarted:
			operation, err := eventOperation(event, operations)
			if err != nil {
				return DerivedState{}, err
			}
			if !operation.ReconcileRequired || event.Payload.Attempt != operation.Attempt {
				return DerivedState{}, fmt.Errorf("operation %q does not require reconciliation for attempt %d", operation.ID, event.Payload.Attempt)
			}
			if operation.reconciling {
				return DerivedState{}, fmt.Errorf("operation %q reconciliation already started", operation.ID)
			}
			operation.reconciling = true
		case EventReconcileResult:
			operation, err := eventOperation(event, operations)
			if err != nil {
				return DerivedState{}, err
			}
			if !operation.ReconcileRequired || !operation.reconciling || event.Payload.Attempt != operation.Attempt {
				return DerivedState{}, fmt.Errorf("operation %q has no matching reconciliation", operation.ID)
			}
			applyResult(operation, event.Payload, true)
			refreshReady(operations)
		default:
			return DerivedState{}, fmt.Errorf("%w %q", ErrUnknownEventType, event.Type)
		}
	}

	result := DerivedState{RunID: runID, Started: started, Completed: completed, Cancelled: cancelled}
	result.operations = make([]OperationState, 0, len(operations))
	for _, operation := range operations {
		result.operations = append(result.operations, cloneOperationState(operation.OperationState))
	}
	sort.Slice(result.operations, func(i, j int) bool { return string(result.operations[i].ID) < string(result.operations[j].ID) })
	result.targets = deriveTargets(started, targets, operations)
	return result, nil
}

func eventOperation(event Event, operations map[domain.OperationID]*mutableOperation) (*mutableOperation, error) {
	operation, ok := operations[event.OperationID]
	if !ok {
		return nil, fmt.Errorf("event references unknown operation %q", event.OperationID)
	}
	if event.TargetID != operation.TargetID {
		return nil, fmt.Errorf("event target %q does not match operation %q target %q", event.TargetID, event.OperationID, operation.TargetID)
	}
	return operation, nil
}

func applyResult(operation *mutableOperation, payload Payload, preserveAmbiguous bool) {
	wasAmbiguous := operation.Ambiguous
	operation.State = payload.State
	operation.ProviderState = payload.ProviderState
	operation.Evidence = append(json.RawMessage(nil), payload.Evidence...)
	operation.ErrorCode = payload.ErrorCode
	operation.Retryable = payload.Retryable
	operation.dispatched = false
	operation.reconciling = false
	operation.ReconcileRequired = payload.State == domain.StateWaitingExternal
	operation.Ambiguous = preserveAmbiguous && wasAmbiguous && operation.ReconcileRequired
}

func dependenciesPublished(operation *mutableOperation, operations map[domain.OperationID]*mutableOperation) bool {
	for _, dependency := range operation.dependencies {
		candidate := operations[dependency]
		if candidate == nil || candidate.State != domain.StatePublished {
			return false
		}
	}
	return true
}

func refreshReady(operations map[domain.OperationID]*mutableOperation) {
	for _, operation := range operations {
		if operation.State == domain.StatePlanned && dependenciesPublished(operation, operations) {
			operation.State = domain.StateReady
		}
	}
}

func allQuiescent(operations map[domain.OperationID]*mutableOperation) bool {
	for _, operation := range operations {
		if operation.ReconcileRequired {
			return false
		}
		switch operation.State {
		case domain.StatePublished, domain.StateRejected, domain.StateFailed, domain.StateCancelled:
		default:
			return false
		}
	}
	return true
}

func cancelRemaining(operations map[domain.OperationID]*mutableOperation) {
	for _, operation := range operations {
		if operation.ReconcileRequired {
			continue
		}
		switch operation.State {
		case domain.StatePlanned, domain.StateReady, domain.StateRunning, domain.StateFailed:
			operation.State = domain.StateCancelled
			operation.Retryable = false
			operation.ErrorCode = "RUN_CANCELLED"
		}
	}
}

func deriveTargets(started bool, targets map[domain.TargetID][]domain.OperationID, operations map[domain.OperationID]*mutableOperation) []TargetState {
	result := make([]TargetState, 0, len(targets))
	for id, ids := range targets {
		state := TargetState{ID: id, State: domain.StatePlanned}
		if started {
			if len(ids) == 0 {
				state.State = domain.StatePublished
			} else {
				state.State = aggregateTarget(ids, operations)
			}
		}
		for _, operationID := range ids {
			operation := operations[operationID]
			state.ReconcileRequired = state.ReconcileRequired || operation.ReconcileRequired
			state.Ambiguous = state.Ambiguous || operation.Ambiguous
		}
		result = append(result, state)
	}
	sort.Slice(result, func(i, j int) bool { return string(result[i].ID) < string(result[j].ID) })
	return result
}

func aggregateTarget(ids []domain.OperationID, operations map[domain.OperationID]*mutableOperation) domain.NormalizedState {
	allPublished := true
	seen := map[domain.NormalizedState]bool{}
	for _, id := range ids {
		state := operations[id].State
		seen[state] = true
		if state != domain.StatePublished {
			allPublished = false
		}
	}
	if allPublished {
		return domain.StatePublished
	}
	for _, state := range []domain.NormalizedState{
		domain.StateRejected,
		domain.StateFailed,
		domain.StateCancelled,
		domain.StateWaitingExternal,
		domain.StateRunning,
		domain.StateReady,
		domain.StatePlanned,
	} {
		if seen[state] {
			return state
		}
	}
	return domain.StatePlanned
}

func cloneOperationState(state OperationState) OperationState {
	state.Evidence = append(json.RawMessage(nil), state.Evidence...)
	return state
}
