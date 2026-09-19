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
	reconcileOnly  bool
}

type taskKind uint8

const (
	taskApply taskKind = iota
	taskReconcile
)

type task struct {
	kind         taskKind
	operation    domain.Operation
	state        journal.OperationState
	requirements []domain.Requirement
	lease        Lease
}

type taskOutcome struct {
	operationID       domain.OperationID
	attempt           uint32
	deferReconcile    bool
	reconciled        bool
	infrastructureErr error
}

type failureStage uint8

const (
	failureBeforeProvider failureStage = iota + 1
	failureAfterProviderStart
	failureAfterDispatch
)

type failureAction uint8

const (
	failureStop failureAction = iota
	failureRetryApply
	failureReconcile
)

type failureDecision struct {
	code      string
	action    failureAction
	cancelled bool
}

type journalExecutionObserver struct {
	mu         sync.Mutex
	runID      domain.RunID
	writer     *journal.Writer
	operation  domain.Operation
	attempt    uint32
	started    bool
	dispatched bool
	responded  bool
}

func (o *journalExecutionObserver) ProviderProcessStarted() error {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.started {
		return fmt.Errorf("provider process start already recorded")
	}
	if _, err := o.writer.Append(journal.Entry{
		RunID: o.runID, Type: journal.EventProviderProcessStarted,
		OperationID: o.operation.ID(), TargetID: o.operation.TargetID(),
		Payload: journal.Payload{Attempt: o.attempt},
	}); err != nil {
		return err
	}
	o.started = true
	return nil
}

func (o *journalExecutionObserver) SideEffectDispatched() error {
	o.mu.Lock()
	defer o.mu.Unlock()
	if !o.started {
		return fmt.Errorf("provider process start must be recorded before dispatch")
	}
	if o.dispatched {
		return fmt.Errorf("side-effect dispatch already recorded")
	}
	if _, err := o.writer.Append(journal.Entry{
		RunID: o.runID, Type: journal.EventSideEffectDispatched,
		OperationID: o.operation.ID(), TargetID: o.operation.TargetID(),
		Payload: journal.Payload{Attempt: o.attempt},
	}); err != nil {
		return err
	}
	o.dispatched = true
	return nil
}

func (o *journalExecutionObserver) ProviderResponseReceived() error {
	o.mu.Lock()
	defer o.mu.Unlock()
	if !o.started {
		return fmt.Errorf("provider process start must be recorded before response")
	}
	if o.responded {
		return fmt.Errorf("provider response already recorded")
	}
	if _, err := o.writer.Append(journal.Entry{
		RunID: o.runID, Type: journal.EventProviderResponseReceived,
		OperationID: o.operation.ID(), TargetID: o.operation.TargetID(),
		Payload: journal.Payload{Attempt: o.attempt},
	}); err != nil {
		return err
	}
	o.responded = true
	return nil
}

func (o *journalExecutionObserver) snapshot() (started, dispatched, responded bool) {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.started, o.dispatched, o.responded
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
		reconcileOnly:  options.ReconcileOnly,
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
	targetRequirements := make(map[domain.TargetID][]domain.Requirement, len(plan.Targets()))
	for _, target := range plan.Targets() {
		targetRequirements[target.ID()] = target.Requirements()
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

		changed, err := expireJournalLeases(runID, writer, state, time.Now().UTC())
		if err != nil {
			return state, err
		}
		if changed {
			continue
		}

		if !state.Cancelled {
			changed, err = journalReadyOperations(runID, writer, state)
			if err != nil {
				return state, err
			}
			if changed {
				continue
			}
			changed, err = recoverUndispatched(runID, writer, state, operations)
			if err != nil {
				return state, err
			}
			if changed {
				continue
			}
			changed, err = cancelBlocked(runID, writer, state, operations, e.maxAttempts)
			if err != nil {
				return state, err
			}
			if changed {
				continue
			}
		}

		candidates := e.candidates(state, operations, reconciled, deferred)
		for i := range candidates {
			candidates[i].requirements = targetRequirements[candidates[i].operation.TargetID()]
		}
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
			if expiry, ok := nextLeaseExpiry(state); ok {
				if err := wait(ctx, time.Until(expiry)); err != nil {
					continue
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
			lease, err := e.leases.Acquire(ctx, LeaseRequest{Key: leaseKey(plan.ID(), candidate.operation.ID()), OperationID: candidate.operation.ID()})
			if err != nil {
				if errors.Is(err, ErrLeaseHeld) {
					leaseBlocked++
					continue
				}
				return state, err
			}
			candidate.lease = lease
			if expired, ok := lease.PreviousExpired(); ok && candidate.state.LeaseActive {
				if err := appendLeaseEvent(runID, writer, candidate.operation, journal.EventLeaseExpired, expired); err != nil {
					_ = lease.Release()
					return state, err
				}
			}
			if err := appendLeaseEvent(runID, writer, candidate.operation, journal.EventLeaseAcquired, lease.State()); err != nil {
				_ = lease.Release()
				return state, err
			}
			if err := prepareTask(runID, writer, candidate); err != nil {
				_ = releaseLease(runID, writer, candidate.operation, lease)
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
		if operationState.LeaseActive {
			continue
		}
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
		if state.Cancelled || e.reconcileOnly {
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
		reason := journal.AttemptReasonInitial
		if current.state.Attempt != 0 {
			reason = journal.AttemptReasonRetry
		}
		if _, err := writer.Append(journal.Entry{
			RunID: runID, Type: journal.EventAttemptStarted, OperationID: current.operation.ID(), TargetID: current.operation.TargetID(),
			Payload: journal.Payload{Attempt: attempt, AttemptReason: reason},
		}); err != nil {
			return err
		}
		return nil
	}
	if _, err := writer.Append(journal.Entry{
		RunID: runID, Type: journal.EventReconcileStarted, OperationID: current.operation.ID(), TargetID: current.operation.TargetID(),
		Payload: journal.Payload{Attempt: attempt, AttemptReason: journal.AttemptReasonReconcile},
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

	leaseCtx, stopLease := context.WithCancel(parent)
	renewDone := make(chan error, 1)
	go func() {
		renewDone <- renewLease(leaseCtx, stopLease, runID, writer, current.operation, current.lease)
	}()
	defer func() {
		stopLease()
		if err := <-renewDone; err != nil && outcome.infrastructureErr == nil {
			outcome.infrastructureErr = err
		}
		if err := releaseLease(runID, writer, current.operation, current.lease); err != nil && outcome.infrastructureErr == nil {
			outcome.infrastructureErr = err
		}
	}()

	ctx, cancel := operationContext(leaseCtx, current.operation.Timeout())
	defer cancel()
	observer := &journalExecutionObserver{
		runID: runID, writer: writer, operation: current.operation, attempt: outcome.attempt,
	}
	request := Request{
		PlanID: planID, RunID: runID, Operation: current.operation, Attempt: outcome.attempt,
		Requirements: append([]domain.Requirement(nil), current.requirements...),
		Observer: observer,
	}
	callCtx := ctx
	var release func()
	if preparer, ok := e.driver.(Preparer); ok {
		operation := DriverApply
		if current.kind == taskReconcile {
			operation = DriverReconcile
		}
		preparation, err := preparer.Prepare(ctx, request, operation)
		if err != nil {
			if current.kind == taskReconcile {
				outcome.infrastructureErr = persistReconcileOutcome(parent, ctx, writer, runID, current.operation, outcome.attempt, current.state.Ambiguous, Result{}, err)
			} else {
				outcome.infrastructureErr = persistPreparationFailure(parent, ctx, writer, runID, current.operation, outcome.attempt, err)
			}
			return outcome
		}
		if preparation.Context != nil {
			callCtx = preparation.Context
		}
		release = preparation.Release
		if err := recordCredentialResolution(runID, writer, current.operation, outcome.attempt, preparation.CredentialRefs); err != nil {
			if release != nil {
				release()
			}
			outcome.infrastructureErr = err
			return outcome
		}
	}
	if release != nil {
		defer release()
	}
	boundaryAware := reportsExecutionBoundaries(e.driver)
	if !boundaryAware {
		if err := observer.ProviderProcessStarted(); err != nil {
			outcome.infrastructureErr = err
			return outcome
		}
		if current.kind == taskApply && current.operation.SideEffecting() {
			if err := observer.SideEffectDispatched(); err != nil {
				outcome.infrastructureErr = err
				return outcome
			}
		}
	}
	if current.kind == taskReconcile {
		request.Previous = &Previous{
			State:         current.state.State,
			ProviderState: current.state.ProviderState,
			Evidence:      append(json.RawMessage(nil), current.state.Evidence...),
			ErrorCode:     current.state.ErrorCode,
			Ambiguous:     current.state.Ambiguous,
		}
		result, err := e.driver.Reconcile(callCtx, request)
		if !boundaryAware && providerResponseReceived(err) {
			if appendErr := observer.ProviderResponseReceived(); appendErr != nil {
				outcome.infrastructureErr = appendErr
				return outcome
			}
		}
		outcome.infrastructureErr = persistReconcileOutcome(parent, callCtx, writer, runID, current.operation, outcome.attempt, current.state.Ambiguous, result, err)
		return outcome
	}

	result, err := e.driver.Apply(callCtx, request)
	if !boundaryAware && providerResponseReceived(err) {
		if appendErr := observer.ProviderResponseReceived(); appendErr != nil {
			outcome.infrastructureErr = appendErr
			return outcome
		}
	}
	started, dispatched, _ := observer.snapshot()
	deferred, persistErr := persistApplyOutcome(parent, callCtx, writer, runID, current.operation, outcome.attempt, started, dispatched, result, err)
	outcome.deferReconcile = deferred
	outcome.infrastructureErr = persistErr
	return outcome
}

func recordCredentialResolution(runID domain.RunID, writer *journal.Writer, operation domain.Operation, attempt uint32, refs []domain.CredentialRef) error {
	for _, ref := range refs {
		if _, err := writer.Append(journal.Entry{
			RunID: runID, Type: journal.EventCredentialResolved, OperationID: operation.ID(), TargetID: operation.TargetID(),
			Payload: journal.Payload{Attempt: attempt, CredentialRef: ref},
		}); err != nil {
			return err
		}
	}
	return nil
}

func persistPreparationFailure(parent, callCtx context.Context, writer *journal.Writer, runID domain.RunID, operation domain.Operation, attempt uint32, callErr error) error {
	decision := decideFailure(failureBeforeProvider, operation.SideEffecting(), parent, callCtx, callErr)
	state := domain.StateFailed
	if decision.cancelled {
		state = domain.StateCancelled
	}
	_, err := writer.Append(resultEntry(runID, operation, attempt, state, "", nil, decision.code, decision.action == failureRetryApply))
	return err
}

func operationContext(parent context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	if timeout > 0 {
		return context.WithTimeout(parent, timeout)
	}
	return context.WithCancel(parent)
}

func persistApplyOutcome(parent, callCtx context.Context, writer *journal.Writer, runID domain.RunID, operation domain.Operation, attempt uint32, providerStarted, dispatched bool, result Result, callErr error) (bool, error) {
	if callErr == nil {
		if err := result.validate(); err != nil {
			if operation.SideEffecting() && dispatched {
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
		if operation.SideEffecting() && !dispatched {
			if _, appendErr := writer.Append(resultEntry(runID, operation, attempt, domain.StateFailed, "", nil, "DRIVER_BOUNDARY_ERROR", true)); appendErr != nil {
				return false, appendErr
			}
			return false, fmt.Errorf("side-effecting operation returned a result before dispatch was recorded")
		}
		if _, err := writer.Append(resultEntry(runID, operation, attempt, result.State, result.ProviderState, result.Evidence, "", false)); err != nil {
			return false, err
		}
		return result.State == domain.StateWaitingExternal, nil
	}

	stage := failureBeforeProvider
	if providerStarted {
		stage = failureAfterProviderStart
	}
	if dispatched {
		stage = failureAfterDispatch
	}
	decision := decideFailure(stage, operation.SideEffecting(), parent, callCtx, callErr)
	if decision.action == failureReconcile {
		_, err := writer.Append(ambiguousEntry(runID, operation, attempt, decision.code))
		return false, err
	}
	state := domain.StateFailed
	if decision.cancelled {
		state = domain.StateCancelled
	}
	_, err := writer.Append(resultEntry(runID, operation, attempt, state, "", nil, decision.code, decision.action == failureRetryApply))
	return false, err
}

func persistReconcileOutcome(parent, callCtx context.Context, writer *journal.Writer, runID domain.RunID, operation domain.Operation, attempt uint32, wasAmbiguous bool, result Result, callErr error) error {
	if callErr == nil {
		if err := result.validate(); err != nil {
			entry := reconcileResultEntry(runID, operation, attempt, domain.StateWaitingExternal, "", nil, "DRIVER_CONTRACT_ERROR", false)
			_, appendErr := writer.Append(entry)
			if appendErr != nil {
				return appendErr
			}
			return err
		}
		entry := reconcileResultEntry(runID, operation, attempt, result.State, result.ProviderState, result.Evidence, "", false)
		_, err := writer.Append(entry)
		return err
	}
	code, retryable, _, _ := classifyCallError(parent, callCtx, callErr)
	if wasAmbiguous && !retryable {
		retryable = true
	}
	entry := reconcileResultEntry(runID, operation, attempt, domain.StateWaitingExternal, "", nil, code, retryable)
	_, err := writer.Append(entry)
	return err
}

func decideFailure(stage failureStage, sideEffecting bool, parent, callCtx context.Context, callErr error) failureDecision {
	code, retryable, ambiguous, cancelled := classifyCallError(parent, callCtx, callErr)
	if cancelled {
		if stage == failureAfterDispatch && sideEffecting {
			return failureDecision{code: code, action: failureReconcile, cancelled: true}
		}
		return failureDecision{code: code, action: failureStop, cancelled: true}
	}

	if stage != failureAfterDispatch {
		if retryable {
			return failureDecision{code: code, action: failureRetryApply}
		}
		var driverErr *DriverError
		if errors.As(callErr, &driverErr) && driverErr.valid() {
			return failureDecision{code: code, action: failureStop}
		}
		return failureDecision{code: code, action: failureRetryApply}
	}

	if sideEffecting && ambiguous {
		return failureDecision{code: code, action: failureReconcile}
	}
	if retryable {
		return failureDecision{code: code, action: failureRetryApply}
	}
	if !sideEffecting {
		var driverErr *DriverError
		if !errors.As(callErr, &driverErr) || !driverErr.valid() {
			return failureDecision{code: code, action: failureRetryApply}
		}
	}
	return failureDecision{code: code, action: failureStop}
}

func reportsExecutionBoundaries(driver Driver) bool {
	reporter, ok := driver.(ExecutionBoundaryReporter)
	return ok && reporter.ReportsExecutionBoundaries()
}

func providerResponseReceived(callErr error) bool {
	if callErr == nil {
		return true
	}
	var driverErr *DriverError
	return errors.As(callErr, &driverErr) && driverErr.valid()
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
	eventType := journal.EventOperationResult
	payloadState := state
	switch state {
	case domain.StateWaitingExternal:
		eventType = journal.EventOperationWaitingExternal
		payloadState = ""
	case domain.StatePublished:
		eventType = journal.EventOperationPublished
		payloadState = ""
	case domain.StateRejected:
		eventType = journal.EventOperationRejected
		payloadState = ""
	case domain.StateFailed:
		eventType = journal.EventOperationFailed
		payloadState = ""
	}
	return journal.Entry{
		RunID: runID, Type: eventType, OperationID: operation.ID(), TargetID: operation.TargetID(),
		Payload: journal.Payload{
			Attempt: attempt, State: payloadState, ProviderState: providerState,
			Evidence: append(json.RawMessage(nil), evidence...), ErrorCode: errorCode, Retryable: retryable,
			ResultCategory: resultCategory(state, retryable),
		},
	}
}

func reconcileResultEntry(runID domain.RunID, operation domain.Operation, attempt uint32, state domain.NormalizedState, providerState string, evidence json.RawMessage, errorCode string, retryable bool) journal.Entry {
	return journal.Entry{
		RunID: runID, Type: journal.EventReconcileResult, OperationID: operation.ID(), TargetID: operation.TargetID(),
		Payload: journal.Payload{
			Attempt: attempt, State: state, ProviderState: providerState,
			Evidence: append(json.RawMessage(nil), evidence...), ErrorCode: errorCode, Retryable: retryable,
			ResultCategory: resultCategory(state, retryable),
		},
	}
}

func ambiguousEntry(runID domain.RunID, operation domain.Operation, attempt uint32, code string) journal.Entry {
	return journal.Entry{
		RunID: runID, Type: journal.EventOutcomeAmbiguous, OperationID: operation.ID(), TargetID: operation.TargetID(),
		Payload: journal.Payload{Attempt: attempt, ErrorCode: code, ResultCategory: journal.ResultCategoryAmbiguous},
	}
}

func resultCategory(state domain.NormalizedState, retryable bool) string {
	switch state {
	case domain.StatePublished:
		return journal.ResultCategoryPublished
	case domain.StateWaitingExternal:
		return journal.ResultCategoryWaitingExternal
	case domain.StateRejected:
		return journal.ResultCategoryRejected
	case domain.StateFailed:
		if retryable {
			return journal.ResultCategoryFailedRetryable
		}
		return journal.ResultCategoryFailedPermanent
	case domain.StateCancelled:
		return journal.ResultCategoryCancelled
	default:
		return ""
	}
}

func journalReadyOperations(runID domain.RunID, writer *journal.Writer, state journal.DerivedState) (bool, error) {
	changed := false
	for _, operation := range state.Operations() {
		if operation.State != domain.StateReady || operation.ReadyJournaled {
			continue
		}
		if _, err := writer.Append(journal.Entry{
			RunID: runID, Type: journal.EventOperationReady, OperationID: operation.ID, TargetID: operation.TargetID,
		}); err != nil {
			return false, err
		}
		changed = true
	}
	return changed, nil
}

func expireJournalLeases(runID domain.RunID, writer *journal.Writer, state journal.DerivedState, now time.Time) (bool, error) {
	changed := false
	for _, operation := range state.Operations() {
		if !operation.LeaseActive || operation.LeaseExpiresAt.IsZero() || operation.LeaseExpiresAt.After(now) {
			continue
		}
		lease := LeaseState{
			ID: operation.LeaseID, Owner: operation.LeaseOwner, OperationID: operation.ID,
			AcquiredAt: operation.LeaseAcquiredAt, ExpiresAt: operation.LeaseExpiresAt,
		}
		if err := appendLeaseStateEvent(runID, writer, operation.ID, operation.TargetID, journal.EventLeaseExpired, lease); err != nil {
			return false, err
		}
		changed = true
	}
	return changed, nil
}

func nextLeaseExpiry(state journal.DerivedState) (time.Time, bool) {
	var next time.Time
	for _, operation := range state.Operations() {
		if !operation.LeaseActive || operation.LeaseExpiresAt.IsZero() {
			continue
		}
		if next.IsZero() || operation.LeaseExpiresAt.Before(next) {
			next = operation.LeaseExpiresAt
		}
	}
	return next, !next.IsZero()
}

func appendLeaseEvent(runID domain.RunID, writer *journal.Writer, operation domain.Operation, eventType journal.EventType, lease LeaseState) error {
	if lease.OperationID != operation.ID() {
		return fmt.Errorf("lease operation %q does not match operation %q", lease.OperationID, operation.ID())
	}
	return appendLeaseStateEvent(runID, writer, operation.ID(), operation.TargetID(), eventType, lease)
}

func appendLeaseStateEvent(runID domain.RunID, writer *journal.Writer, operationID domain.OperationID, targetID domain.TargetID, eventType journal.EventType, lease LeaseState) error {
	_, err := writer.Append(journal.Entry{
		RunID: runID, Type: eventType, OperationID: operationID, TargetID: targetID,
		Payload: journal.Payload{Lease: &journal.LeasePayload{
			ID: lease.ID, Owner: lease.Owner, AcquiredAt: lease.AcquiredAt, ExpiresAt: lease.ExpiresAt,
		}},
	})
	return err
}

func renewLease(ctx context.Context, cancel context.CancelFunc, runID domain.RunID, writer *journal.Writer, operation domain.Operation, lease Lease) error {
	for {
		state := lease.State()
		remaining := time.Until(state.ExpiresAt)
		if remaining <= 0 {
			cancel()
			return ErrLeaseExpired
		}
		delay := remaining / 2
		if delay <= 0 {
			delay = remaining
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			return nil
		case <-timer.C:
		}

		renewed, err := lease.Renew(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			cancel()
			return err
		}
		if err := appendLeaseEvent(runID, writer, operation, journal.EventLeaseRenewed, renewed); err != nil {
			cancel()
			return err
		}
	}
}

func releaseLease(runID domain.RunID, writer *journal.Writer, operation domain.Operation, lease Lease) error {
	state := lease.State()
	if err := lease.Release(); err != nil {
		if errors.Is(err, ErrLeaseExpired) {
			if appendErr := appendLeaseEvent(runID, writer, operation, journal.EventLeaseExpired, state); appendErr != nil {
				return appendErr
			}
		}
		return err
	}
	return appendLeaseEvent(runID, writer, operation, journal.EventLeaseReleased, state)
}

func recoverUndispatched(runID domain.RunID, writer *journal.Writer, state journal.DerivedState, operations map[domain.OperationID]domain.Operation) (bool, error) {
	changed := false
	for _, operationState := range state.Operations() {
		if operationState.State != domain.StateRunning || operationState.ReconcileRequired || operationState.LeaseActive {
			continue
		}
		operation, ok := operations[operationState.ID]
		if !ok {
			continue
		}
		code := "INTERRUPTED_BEFORE_PROVIDER"
		if operationState.ProviderStarted {
			if operation.SideEffecting() {
				code = "INTERRUPTED_BEFORE_DISPATCH"
			} else {
				code = "PROVIDER_INTERRUPTED"
			}
		}
		if _, err := writer.Append(resultEntry(runID, operation, operationState.Attempt, domain.StateFailed, "", nil, code, true)); err != nil {
			return false, err
		}
		changed = true
	}
	return changed, nil
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
		if operation.ReconcileRequired || operation.LeaseActive {
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
