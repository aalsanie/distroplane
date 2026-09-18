package executor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/aalsanie/distroplane/internal/domain"
	"github.com/aalsanie/distroplane/internal/journal"
)

const (
	defaultMaxConcurrency = 4
	defaultMaxAttempts    = 3
)

type Executor struct {
	driver         Driver
	leases         LeaseManager
	maxConcurrency int
	maxAttempts    uint32
	backoff        Backoff
}

type taskKind uint8

const (
	taskApply taskKind = iota
	taskReconcile
)

type task struct {
	kind      taskKind
	operation domain.Operation
	state     journal.OperationState
	lease     Lease
}

type taskOutcome struct {
	operationID       domain.OperationID
	attempt           uint32
	deferReconcile    bool
	reconciled        bool
	infrastructureErr error
}

func New(driver Driver, options Options) (*Executor, error) {
	if driver == nil {
		return nil, fmt.Errorf("driver must not be nil")
	}
	if options.MaxConcurrency < 0 {
		return nil, fmt.Errorf("max concurrency must not be negative")
	}
	if options.MaxAttempts == 0 {
		options.MaxAttempts = defaultMaxAttempts
	}
	if options.MaxConcurrency == 0 {
		options.MaxConcurrency = defaultMaxConcurrency
	}
	if options.Backoff == nil {
		options.Backoff = defaultBackoff
	}
	if options.Leases == nil {
		options.Leases = NewMemoryLeases()
	}
	return &Executor{
		driver:         driver,
		leases:         options.Leases,
		maxConcurrency: options.MaxConcurrency,
		maxAttempts:    options.MaxAttempts,
		backoff:        options.Backoff,
	}, nil
}

func defaultBackoff(completedAttempt uint32) time.Duration {
	if completedAttempt == 0 {
		return 0
	}
	shift := completedAttempt - 1
	if shift > 4 {
		shift = 4
	}
	return 250 * time.Millisecond * time.Duration(1<<shift)
}

func (e *Executor) Execute(ctx context.Context, plan domain.Plan, runID domain.RunID, writer *journal.Writer) (journal.DerivedState, error) {
	if ctx == nil {
		return journal.DerivedState{}, fmt.Errorf("context must not be nil")
	}
	if e == nil || e.driver == nil || e.leases == nil || e.backoff == nil || e.maxConcurrency <= 0 || e.maxAttempts == 0 {
		return journal.DerivedState{}, fmt.Errorf("executor is not initialized")
	}
	if !plan.ID().Valid() {
		return journal.DerivedState{}, fmt.Errorf("plan is invalid")
	}
	if !runID.Valid() {
		return journal.DerivedState{}, fmt.Errorf("run ID is invalid")
	}
	if writer == nil {
		return journal.DerivedState{}, fmt.Errorf("journal writer must not be nil")
	}

	operations := make(map[domain.OperationID]domain.Operation, len(plan.Operations()))
	for _, operation := range plan.Operations() {
		operations[operation.ID()] = operation
	}

	events := writer.Events()
	state, err := journal.Reduce(plan, events)
	if err != nil {
		return journal.DerivedState{}, err
	}
	if len(events) == 0 {
		if _, err := writer.Append(journal.Entry{RunID: runID, Type: journal.EventRunStarted}); err != nil {
			return journal.DerivedState{}, err
		}
		state, err = journal.Reduce(plan, writer.Events())
		if err != nil {
			return journal.DerivedState{}, err
		}
	} else if state.RunID != runID {
		return journal.DerivedState{}, fmt.Errorf("journal belongs to run %q", state.RunID)
	}
	if state.Completed {
		return state, nil
	}

	reconciled := make(map[operationAttempt]struct{})
	deferred := make(map[operationAttempt]struct{})
	for {
		state, err = journal.Reduce(plan, writer.Events())
		if err != nil {
			return journal.DerivedState{}, err
		}
		if state.Completed {
			return state, nil
		}
		if err := ctx.Err(); err != nil && !state.Cancelled {
			if _, appendErr := writer.Append(journal.Entry{RunID: runID, Type: journal.EventRunCancelled}); appendErr != nil {
				return state, appendErr
			}
			state, reduceErr := journal.Reduce(plan, writer.Events())
			if reduceErr != nil {
				return journal.DerivedState{}, reduceErr
			}
			return state, err
		}

		if !state.Cancelled {
			changed, err := cancelBlocked(runID, writer, state, operations, e.maxAttempts)
			if err != nil {
				return state, err
			}
			if changed {
				continue
			}
		}

		candidates := e.candidates(state, operations, reconciled, deferred)
		if len(candidates) == 0 {
			if state.Cancelled {
				return state, nil
			}
			if executorQuiescent(state, e.maxAttempts) {
				if _, err := writer.Append(journal.Entry{RunID: runID, Type: journal.EventRunCompleted}); err != nil {
					return state, err
				}
				continue
			}
			return state, nil
		}

		batch := candidates
		if len(batch) > e.maxConcurrency {
			batch = batch[:e.maxConcurrency]
		}
		prepared := make([]task, 0, len(batch))
		leaseBlocked := 0
		for _, candidate := range batch {
			if candidate.kind == taskApply && candidate.state.State == domain.StateFailed {
				if err := wait(ctx, e.backoff(candidate.state.Attempt)); err != nil {
					break
				}
			}
			lease, err := e.leases.Acquire(ctx, leaseKey(plan.ID(), candidate.operation.ID()))
			if err != nil {
				if errors.Is(err, ErrLeaseHeld) {
					leaseBlocked++
					continue
				}
				return state, err
			}
			candidate.lease = lease
			if err := prepareTask(runID, writer, candidate); err != nil {
				_ = lease.Release()
				return state, err
			}
			prepared = append(prepared, candidate)
		}
		if len(prepared) == 0 {
			if err := ctx.Err(); err != nil {
				continue
			}
			if leaseBlocked != 0 {
				return state, ErrLeaseHeld
			}
			return state, nil
		}

		outcomes := make(chan taskOutcome, len(prepared))
		var wg sync.WaitGroup
		for _, preparedTask := range prepared {
			wg.Add(1)
			go func(current task) {
				defer wg.Done()
				outcomes <- e.runTask(ctx, plan.ID(), runID, writer, current)
			}(preparedTask)
		}
		wg.Wait()
		close(outcomes)
		var firstErr error
		for outcome := range outcomes {
			key := operationAttempt{operationID: outcome.operationID, attempt: outcome.attempt}
			if outcome.deferReconcile {
				deferred[key] = struct{}{}
			}
			if outcome.reconciled {
				reconciled[key] = struct{}{}
			}
			if outcome.infrastructureErr != nil && firstErr == nil {
				firstErr = outcome.infrastructureErr
			}
		}
		if firstErr != nil {
			return state, firstErr
		}
	}
}

type operationAttempt struct {
	operationID domain.OperationID
	attempt     uint32
}

func (e *Executor) candidates(state journal.DerivedState, operations map[domain.OperationID]domain.Operation, reconciled, deferred map[operationAttempt]struct{}) []task {
	result := make([]task, 0)
	for _, operationState := range state.Operations() {
		operation, ok := operations[operationState.ID]
		if !ok {
			continue
		}
		key := operationAttempt{operationID: operationState.ID, attempt: operationState.Attempt}
		if operationState.ReconcileRequired {
			if _, done := reconciled[key]; done {
				continue
			}
			if _, wait := deferred[key]; wait {
				continue
			}
			result = append(result, task{kind: taskReconcile, operation: operation, state: operationState})
			continue
		}
		if state.Cancelled {
			continue
		}
		if operationState.State == domain.StateReady || (operationState.State == domain.StateFailed && operationState.Retryable && operationState.Attempt < e.maxAttempts) {
			result = append(result, task{kind: taskApply, operation: operation, state: operationState})
		}
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].kind != result[j].kind {
			return result[i].kind > result[j].kind
		}
		return string(result[i].operation.ID()) < string(result[j].operation.ID())
	})
	return result
}

func prepareTask(runID domain.RunID, writer *journal.Writer, current task) error {
	attempt := current.state.Attempt
	if current.kind == taskApply {
		attempt++
		if _, err := writer.Append(journal.Entry{
			RunID: runID, Type: journal.EventAttemptStarted, OperationID: current.operation.ID(), TargetID: current.operation.TargetID(),
			Payload: journal.Payload{Attempt: attempt},
		}); err != nil {
			return err
		}
		if current.operation.SideEffecting() {
			if _, err := writer.Append(journal.Entry{
				RunID: runID, Type: journal.EventSideEffectDispatched, OperationID: current.operation.ID(), TargetID: current.operation.TargetID(),
				Payload: journal.Payload{Attempt: attempt},
			}); err != nil {
				return err
			}
		}
		return nil
	}
	if _, err := writer.Append(journal.Entry{
		RunID: runID, Type: journal.EventReconcileStarted, OperationID: current.operation.ID(), TargetID: current.operation.TargetID(),
		Payload: journal.Payload{Attempt: attempt},
	}); err != nil {
		return err
	}
	return nil
}

func (e *Executor) runTask(parent context.Context, planID domain.PlanID, runID domain.RunID, writer *journal.Writer, current task) (outcome taskOutcome) {
	outcome = taskOutcome{operationID: current.operation.ID(), attempt: current.state.Attempt}
	if current.kind == taskApply {
		outcome.attempt++
	} else {
		outcome.reconciled = true
	}
	defer func() {
		if err := current.lease.Release(); err != nil && outcome.infrastructureErr == nil {
			outcome.infrastructureErr = err
		}
	}()

	ctx, cancel := operationContext(parent, current.operation.Timeout())
	defer cancel()
	request := Request{PlanID: planID, RunID: runID, Operation: current.operation, Attempt: outcome.attempt}
	if current.kind == taskReconcile {
		request.Previous = &Previous{
			State:         current.state.State,
			ProviderState: current.state.ProviderState,
			Evidence:      append(json.RawMessage(nil), current.state.Evidence...),
			ErrorCode:     current.state.ErrorCode,
			Ambiguous:     current.state.Ambiguous,
		}
		result, err := e.driver.Reconcile(ctx, request)
		outcome.infrastructureErr = persistReconcileOutcome(parent, ctx, writer, runID, current.operation, outcome.attempt, current.state.Ambiguous, result, err)
		return outcome
	}

	result, err := e.driver.Apply(ctx, request)
	deferred, persistErr := persistApplyOutcome(parent, ctx, writer, runID, current.operation, outcome.attempt, result, err)
	outcome.deferReconcile = deferred
	outcome.infrastructureErr = persistErr
	return outcome
}

func operationContext(parent context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	if timeout > 0 {
		return context.WithTimeout(parent, timeout)
	}
	return context.WithCancel(parent)
}

func persistApplyOutcome(parent, callCtx context.Context, writer *journal.Writer, runID domain.RunID, operation domain.Operation, attempt uint32, result Result, callErr error) (bool, error) {
	if callErr == nil {
		if err := result.validate(); err != nil {
			if operation.SideEffecting() {
				if _, appendErr := writer.Append(ambiguousEntry(runID, operation, attempt, "DRIVER_CONTRACT_ERROR")); appendErr != nil {
					return false, appendErr
				}
				return false, err
			}
			if _, appendErr := writer.Append(resultEntry(runID, operation, attempt, domain.StateFailed, "", nil, "DRIVER_CONTRACT_ERROR", false)); appendErr != nil {
				return false, appendErr
			}
			return false, err
		}
		if _, err := writer.Append(resultEntry(runID, operation, attempt, result.State, result.ProviderState, result.Evidence, "", false)); err != nil {
			return false, err
		}
		return result.State == domain.StateWaitingExternal, nil
	}

	code, retryable, ambiguous, cancelled := classifyCallError(parent, callCtx, callErr)
	if operation.SideEffecting() && ambiguous {
		_, err := writer.Append(ambiguousEntry(runID, operation, attempt, code))
		return false, err
	}
	state := domain.StateFailed
	if cancelled {
		state = domain.StateCancelled
		retryable = false
	}
	_, err := writer.Append(resultEntry(runID, operation, attempt, state, "", nil, code, retryable))
	return false, err
}

func persistReconcileOutcome(parent, callCtx context.Context, writer *journal.Writer, runID domain.RunID, operation domain.Operation, attempt uint32, wasAmbiguous bool, result Result, callErr error) error {
	if callErr == nil {
		if err := result.validate(); err != nil {
			entry := resultEntry(runID, operation, attempt, domain.StateWaitingExternal, "", nil, "DRIVER_CONTRACT_ERROR", false)
			entry.Type = journal.EventReconcileResult
			_, appendErr := writer.Append(entry)
			if appendErr != nil {
				return appendErr
			}
			return err
		}
		entry := resultEntry(runID, operation, attempt, result.State, result.ProviderState, result.Evidence, "", false)
		entry.Type = journal.EventReconcileResult
		_, err := writer.Append(entry)
		return err
	}
	code, retryable, _, _ := classifyCallError(parent, callCtx, callErr)
	if wasAmbiguous && !retryable {
		retryable = true
	}
	entry := resultEntry(runID, operation, attempt, domain.StateWaitingExternal, "", nil, code, retryable)
	entry.Type = journal.EventReconcileResult
	_, err := writer.Append(entry)
	return err
}

func classifyCallError(parent, callCtx context.Context, callErr error) (code string, retryable, ambiguous, cancelled bool) {
	if errors.Is(callErr, context.DeadlineExceeded) || errors.Is(callCtx.Err(), context.DeadlineExceeded) {
		return "TIMEOUT", true, true, false
	}
	if errors.Is(callErr, context.Canceled) || errors.Is(callCtx.Err(), context.Canceled) {
		if parent.Err() != nil {
			return "CANCELLED", false, true, true
		}
		return "CANCELLED", false, true, true
	}
	var driverErr *DriverError
	if errors.As(callErr, &driverErr) && driverErr.valid() {
		return driverErr.Code, driverErr.Retryable, driverErr.Ambiguous, false
	}
	return "DRIVER_ERROR", false, true, false
}

func resultEntry(runID domain.RunID, operation domain.Operation, attempt uint32, state domain.NormalizedState, providerState string, evidence json.RawMessage, errorCode string, retryable bool) journal.Entry {
	return journal.Entry{
		RunID: runID, Type: journal.EventOperationResult, OperationID: operation.ID(), TargetID: operation.TargetID(),
		Payload: journal.Payload{Attempt: attempt, State: state, ProviderState: providerState, Evidence: append(json.RawMessage(nil), evidence...), ErrorCode: errorCode, Retryable: retryable},
	}
}

func ambiguousEntry(runID domain.RunID, operation domain.Operation, attempt uint32, code string) journal.Entry {
	return journal.Entry{
		RunID: runID, Type: journal.EventOutcomeAmbiguous, OperationID: operation.ID(), TargetID: operation.TargetID(),
		Payload: journal.Payload{Attempt: attempt, ErrorCode: code},
	}
}

func cancelBlocked(runID domain.RunID, writer *journal.Writer, state journal.DerivedState, operations map[domain.OperationID]domain.Operation, maxAttempts uint32) (bool, error) {
	states := make(map[domain.OperationID]journal.OperationState, len(state.Operations()))
	for _, operationState := range state.Operations() {
		states[operationState.ID] = operationState
	}
	ids := make([]domain.OperationID, 0, len(operations))
	for id := range operations {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return string(ids[i]) < string(ids[j]) })
	changed := false
	for _, id := range ids {
		operationState := states[id]
		if operationState.State != domain.StatePlanned && operationState.State != domain.StateReady {
			continue
		}
		if !hasTerminalUnpublishedDependency(operations[id], states, maxAttempts) {
			continue
		}
		if _, err := writer.Append(journal.Entry{
			RunID: runID, Type: journal.EventOperationCancelled, OperationID: id, TargetID: operations[id].TargetID(),
			Payload: journal.Payload{ErrorCode: "DEPENDENCY_TERMINAL"},
		}); err != nil {
			return false, err
		}
		changed = true
	}
	return changed, nil
}

func hasTerminalUnpublishedDependency(operation domain.Operation, states map[domain.OperationID]journal.OperationState, maxAttempts uint32) bool {
	for _, dependency := range operation.Dependencies() {
		state, ok := states[dependency]
		if !ok {
			return true
		}
		switch state.State {
		case domain.StateRejected, domain.StateCancelled:
			return true
		case domain.StateFailed:
			if !state.Retryable || state.Attempt >= maxAttempts {
				return true
			}
		}
	}
	return false
}

func executorQuiescent(state journal.DerivedState, maxAttempts uint32) bool {
	for _, operation := range state.Operations() {
		if operation.ReconcileRequired {
			return false
		}
		switch operation.State {
		case domain.StatePublished, domain.StateRejected, domain.StateCancelled:
		case domain.StateFailed:
			if operation.Retryable && operation.Attempt < maxAttempts {
				return false
			}
		default:
			return false
		}
	}
	return true
}

func leaseKey(planID domain.PlanID, operationID domain.OperationID) string {
	return string(planID) + "\x00" + string(operationID)
}

func wait(ctx context.Context, duration time.Duration) error {
	if duration <= 0 {
		return ctx.Err()
	}
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
