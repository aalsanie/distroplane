package executor

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/aalsanie/distroplane/internal/domain"
	"github.com/aalsanie/distroplane/internal/journal"
)

type fixedLeaseManager struct {
	lease Lease
	err   error
}

func (m fixedLeaseManager) Acquire(context.Context, LeaseRequest) (Lease, error) {
	return m.lease, m.err
}

type fixedLease struct {
	err error
}

func (l fixedLease) State() LeaseState {
	return LeaseState{
		ID: "fixed-lease", Owner: "fixed-worker", OperationID: "op-a",
		AcquiredAt: time.Unix(1, 0).UTC(), ExpiresAt: time.Unix(4102444800, 0).UTC(),
	}
}

func (l fixedLease) PreviousExpired() (LeaseState, bool) {
	return LeaseState{}, false
}

func (l fixedLease) Renew(context.Context) (LeaseState, error) {
	return l.State(), nil
}

func (l fixedLease) Release() error {
	return l.err
}

func TestDefaultsAndWait(t *testing.T) {
	executor, err := New(&scriptedDriver{}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if executor.maxConcurrency != defaultMaxConcurrency || executor.maxAttempts != defaultMaxAttempts || executor.leases == nil || executor.backoff == nil {
		t.Fatalf("executor=%+v", executor)
	}
	for _, tc := range []struct {
		attempt uint32
		want    time.Duration
	}{
		{0, 0},
		{1, 250 * time.Millisecond},
		{2, 500 * time.Millisecond},
		{6, 4 * time.Second},
	} {
		if got := defaultBackoff(tc.attempt); got != tc.want {
			t.Fatalf("attempt=%d got=%s want=%s", tc.attempt, got, tc.want)
		}
	}
	if err := wait(context.Background(), 0); err != nil {
		t.Fatal(err)
	}
	if err := wait(context.Background(), time.Millisecond); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := wait(ctx, 0); !errors.Is(err, context.Canceled) {
		t.Fatalf("err=%v", err)
	}
	if err := wait(ctx, time.Hour); !errors.Is(err, context.Canceled) {
		t.Fatalf("err=%v", err)
	}
}

func TestClassifyCallError(t *testing.T) {
	parent := context.Background()
	callCtx := context.Background()
	cases := []struct {
		name      string
		parent    context.Context
		callCtx   context.Context
		err       error
		code      string
		retryable bool
		ambiguous bool
		cancelled bool
	}{
		{"deadline", parent, callCtx, context.DeadlineExceeded, "TIMEOUT", true, true, false},
		{"driver", parent, callCtx, &DriverError{Code: "TRANSIENT", Retryable: true}, "TRANSIENT", true, false, false},
		{"driver ambiguous", parent, callCtx, &DriverError{Code: "UNKNOWN", Ambiguous: true}, "UNKNOWN", false, true, false},
		{"invalid driver", parent, callCtx, &DriverError{Code: " bad"}, "DRIVER_ERROR", false, true, false},
		{"generic", parent, callCtx, errors.New("broken"), "DRIVER_ERROR", false, true, false},
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	cases = append(cases,
		struct {
			name      string
			parent    context.Context
			callCtx   context.Context
			err       error
			code      string
			retryable bool
			ambiguous bool
			cancelled bool
		}{"cancelled", cancelled, cancelled, context.Canceled, "CANCELLED", false, true, true},
	)
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			code, retryable, ambiguous, wasCancelled := classifyCallError(tc.parent, tc.callCtx, tc.err)
			if code != tc.code || retryable != tc.retryable || ambiguous != tc.ambiguous || wasCancelled != tc.cancelled {
				t.Fatalf("got=(%q,%v,%v,%v)", code, retryable, ambiguous, wasCancelled)
			}
		})
	}
}

func TestFailureDecisionTable(t *testing.T) {
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	cases := []struct {
		name          string
		stage         failureStage
		sideEffecting bool
		parent        context.Context
		callCtx       context.Context
		err           error
		code          string
		action        failureAction
		cancelled     bool
	}{
		{"pre-provider infrastructure", failureBeforeProvider, true, context.Background(), context.Background(), errors.New("spawn failed"), "DRIVER_ERROR", failureRetryApply, false},
		{"pre-provider transient", failureBeforeProvider, true, context.Background(), context.Background(), &DriverError{Code: "TRANSIENT", Retryable: true}, "TRANSIENT", failureRetryApply, false},
		{"pre-provider permanent", failureBeforeProvider, true, context.Background(), context.Background(), &DriverError{Code: "PERMANENT"}, "PERMANENT", failureStop, false},
		{"non-side process crash", failureAfterProviderStart, false, context.Background(), context.Background(), errors.New("process crashed"), "DRIVER_ERROR", failureRetryApply, false},
		{"side process crash before dispatch", failureAfterProviderStart, true, context.Background(), context.Background(), errors.New("process crashed"), "DRIVER_ERROR", failureRetryApply, false},
		{"side process crash after dispatch", failureAfterDispatch, true, context.Background(), context.Background(), errors.New("process crashed"), "DRIVER_ERROR", failureReconcile, false},
		{"side transient after dispatch", failureAfterDispatch, true, context.Background(), context.Background(), &DriverError{Code: "TRANSIENT", Retryable: true}, "TRANSIENT", failureRetryApply, false},
		{"side permanent after dispatch", failureAfterDispatch, true, context.Background(), context.Background(), &DriverError{Code: "PERMANENT"}, "PERMANENT", failureStop, false},
		{"side ambiguous after dispatch", failureAfterDispatch, true, context.Background(), context.Background(), &DriverError{Code: "AMBIGUOUS", Ambiguous: true}, "AMBIGUOUS", failureReconcile, false},
		{"side timeout after dispatch", failureAfterDispatch, true, context.Background(), context.Background(), context.DeadlineExceeded, "TIMEOUT", failureReconcile, false},
		{"cancel before provider", failureBeforeProvider, true, cancelled, cancelled, context.Canceled, "CANCELLED", failureStop, true},
		{"cancel after start before dispatch", failureAfterProviderStart, true, cancelled, cancelled, context.Canceled, "CANCELLED", failureStop, true},
		{"cancel after dispatch", failureAfterDispatch, true, cancelled, cancelled, context.Canceled, "CANCELLED", failureReconcile, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := decideFailure(tc.stage, tc.sideEffecting, tc.parent, tc.callCtx, tc.err)
			if got.code != tc.code || got.action != tc.action || got.cancelled != tc.cancelled {
				t.Fatalf("decision=%+v want code=%q action=%v cancelled=%v", got, tc.code, tc.action, tc.cancelled)
			}
		})
	}
}

func TestExecuteInfrastructureFailures(t *testing.T) {
	plan := testPlan(t, []operationSpec{{id: "op-a"}})

	t.Run("acquire", func(t *testing.T) {
		want := errors.New("lease acquire")
		executor := newExecutor(t, &scriptedDriver{}, Options{Leases: fixedLeaseManager{err: want}})
		_, err := executor.Execute(context.Background(), plan, runID(), openWriter(t, runID()))
		if !errors.Is(err, want) {
			t.Fatalf("err=%v", err)
		}
	})

	t.Run("release", func(t *testing.T) {
		want := errors.New("lease release")
		writer := openWriter(t, runID())
		executor := newExecutor(t, &scriptedDriver{}, Options{Leases: fixedLeaseManager{lease: fixedLease{err: want}}})
		_, err := executor.Execute(context.Background(), plan, runID(), writer)
		if !errors.Is(err, want) {
			t.Fatalf("err=%v", err)
		}
		state, reduceErr := journal.Reduce(plan, writer.Events())
		if reduceErr != nil {
			t.Fatal(reduceErr)
		}
		op, _ := state.Operation("op-a")
		if op.State != domain.StatePublished {
			t.Fatalf("op=%+v", op)
		}
	})

	t.Run("closed writer", func(t *testing.T) {
		writer := openWriter(t, runID())
		if err := writer.Close(); err != nil {
			t.Fatal(err)
		}
		_, err := newExecutor(t, &scriptedDriver{}, Options{}).Execute(context.Background(), plan, runID(), writer)
		if !errors.Is(err, journal.ErrWriterClosed) {
			t.Fatalf("err=%v", err)
		}
	})
}

func TestExecuteRejectsRunMismatch(t *testing.T) {
	plan := testPlan(t, []operationSpec{{id: "op-a"}})
	writer := openWriter(t, runID())
	if _, err := writer.Append(journal.Entry{RunID: runID(), Type: journal.EventRunStarted}); err != nil {
		t.Fatal(err)
	}
	other, _ := domain.NewRunID("run-2")
	_, err := newExecutor(t, &scriptedDriver{}, Options{}).Execute(context.Background(), plan, other, writer)
	if err == nil || !strings.Contains(err.Error(), "journal belongs to run") {
		t.Fatalf("err=%v", err)
	}
}

func TestExecuteReconcileFailureAndContractViolation(t *testing.T) {
	plan := testPlan(t, []operationSpec{{id: "op-a", sideEffecting: true}})

	preseed := func(t *testing.T) *journal.Writer {
		writer := openWriter(t, runID())
		for _, entry := range []journal.Entry{
			{RunID: runID(), Type: journal.EventRunStarted},
			{RunID: runID(), Type: journal.EventAttemptStarted, OperationID: "op-a", TargetID: "target-a", Payload: journal.Payload{Attempt: 1}},
			{RunID: runID(), Type: journal.EventSideEffectDispatched, OperationID: "op-a", TargetID: "target-a", Payload: journal.Payload{Attempt: 1}},
		} {
			if _, err := writer.Append(entry); err != nil {
				t.Fatal(err)
			}
		}
		return writer
	}

	t.Run("retryable reconcile failure", func(t *testing.T) {
		writer := preseed(t)
		driver := &scriptedDriver{reconcile: func(context.Context, Request) (Result, error) {
			return Result{}, &DriverError{Code: "RECONCILE_TRANSIENT", Retryable: true}
		}}
		state, err := newExecutor(t, driver, Options{}).Execute(context.Background(), plan, runID(), writer)
		if err != nil {
			t.Fatal(err)
		}
		op, _ := state.Operation("op-a")
		if op.State != domain.StateWaitingExternal || !op.ReconcileRequired || !op.Ambiguous || !op.Retryable || op.ErrorCode != "RECONCILE_TRANSIENT" {
			t.Fatalf("op=%+v", op)
		}
	})

	t.Run("malformed reconcile result", func(t *testing.T) {
		writer := preseed(t)
		driver := &scriptedDriver{reconcile: func(context.Context, Request) (Result, error) {
			return Result{State: domain.StatePublished}, nil
		}}
		_, err := newExecutor(t, driver, Options{}).Execute(context.Background(), plan, runID(), writer)
		if !errors.Is(err, ErrDriverContract) {
			t.Fatalf("err=%v", err)
		}
		state, reduceErr := journal.Reduce(plan, writer.Events())
		if reduceErr != nil {
			t.Fatal(reduceErr)
		}
		op, _ := state.Operation("op-a")
		if op.State != domain.StateWaitingExternal || !op.ReconcileRequired || !op.Ambiguous || op.ErrorCode != "DRIVER_CONTRACT_ERROR" {
			t.Fatalf("op=%+v", op)
		}
	})
}

func TestExecuteKnownNonAmbiguousSideEffectFailure(t *testing.T) {
	plan := testPlan(t, []operationSpec{{id: "op-a", sideEffecting: true}})
	driver := &scriptedDriver{apply: func(context.Context, Request) (Result, error) {
		return Result{}, &DriverError{Code: "REJECTED_BEFORE_SUBMIT", Ambiguous: false}
	}}
	state, err := newExecutor(t, driver, Options{}).Execute(context.Background(), plan, runID(), openWriter(t, runID()))
	if err != nil {
		t.Fatal(err)
	}
	op, _ := state.Operation("op-a")
	if op.State != domain.StateFailed || op.ReconcileRequired || op.Ambiguous || op.ErrorCode != "REJECTED_BEFORE_SUBMIT" {
		t.Fatalf("op=%+v", op)
	}
}

func TestExecuteRejectedResultIsTerminal(t *testing.T) {
	plan := testPlan(t, []operationSpec{{id: "op-a"}})
	driver := &scriptedDriver{apply: func(context.Context, Request) (Result, error) {
		return Result{State: domain.StateRejected, ProviderState: "rejected", Evidence: json.RawMessage(`{"provider":"test"}`)}, nil
	}}
	state, err := newExecutor(t, driver, Options{}).Execute(context.Background(), plan, runID(), openWriter(t, runID()))
	if err != nil {
		t.Fatal(err)
	}
	op, _ := state.Operation("op-a")
	if !state.Completed || op.State != domain.StateRejected {
		t.Fatalf("state=%+v op=%+v", state, op)
	}
}

func TestHelpersAndValidationBranches(t *testing.T) {
	plan := testPlan(t, []operationSpec{{id: "op-a"}, {id: "op-b", dependencies: []string{"op-a"}}})
	operations := plan.Operations()
	if key := leaseKey(plan.ID(), operations[0].ID()); key == "" || !strings.ContainsRune(key, '\x00') {
		t.Fatalf("key=%q", key)
	}
	if !hasTerminalUnpublishedDependency(operations[1], map[domain.OperationID]journal.OperationState{}, 3) {
		t.Fatal("missing dependency not terminal")
	}
	states := map[domain.OperationID]journal.OperationState{
		"op-a": {ID: "op-a", State: domain.StateRejected},
	}
	if !hasTerminalUnpublishedDependency(operations[1], states, 3) {
		t.Fatal("rejected dependency not terminal")
	}
	states["op-a"] = journal.OperationState{ID: "op-a", State: domain.StateFailed, Attempt: 1, Retryable: true}
	if hasTerminalUnpublishedDependency(operations[1], states, 3) {
		t.Fatal("retryable dependency treated as terminal")
	}
	states["op-a"] = journal.OperationState{ID: "op-a", State: domain.StateFailed, Attempt: 3, Retryable: true}
	if !hasTerminalUnpublishedDependency(operations[1], states, 3) {
		t.Fatal("exhausted dependency not terminal")
	}

	for _, value := range []string{" leading", "trailing ", "line\nbreak", strings.Repeat("x", 257), string([]byte{0xff})} {
		if err := validateText("value", value, false); err == nil {
			t.Fatalf("accepted %q", value)
		}
	}
	if err := validateText("value", "", true); err != nil {
		t.Fatal(err)
	}
	if err := validateText("value", strings.Repeat("x", 256), false); err != nil {
		t.Fatal(err)
	}
	if (&DriverError{}).Error() != "driver error" {
		t.Fatal("driver fallback text changed")
	}

	leases := NewMemoryLeases()
	leases.held = nil
	lease, err := leases.Acquire(context.Background(), LeaseRequest{Key: "key", OperationID: "op-a"})
	if err != nil {
		t.Fatal(err)
	}
	if err := lease.Release(); err != nil {
		t.Fatal(err)
	}
	if err := (&memoryLease{}).Release(); err == nil {
		t.Fatal("invalid empty lease accepted")
	}
}

type coverageLease struct {
	state      LeaseState
	renewState LeaseState
	renewErr   error
	releaseErr error
}

func (l *coverageLease) State() LeaseState {
	return l.state
}

func (l *coverageLease) PreviousExpired() (LeaseState, bool) {
	return LeaseState{}, false
}

func (l *coverageLease) Renew(context.Context) (LeaseState, error) {
	if l.renewErr != nil {
		return l.state, l.renewErr
	}
	if l.renewState.ID != "" {
		l.state = l.renewState
	}
	return l.state, nil
}

func (l *coverageLease) Release() error {
	return l.releaseErr
}

type boundaryReportingDriver struct {
	enabled bool
}

func (d boundaryReportingDriver) Apply(context.Context, Request) (Result, error) {
	return published(), nil
}

func (d boundaryReportingDriver) Reconcile(context.Context, Request) (Result, error) {
	return published(), nil
}

func (d boundaryReportingDriver) ReportsExecutionBoundaries() bool {
	return d.enabled
}

func TestExecutionObserverOrderingAndDuplicates(t *testing.T) {
	plan := testPlan(t, []operationSpec{{id: "op-a", sideEffecting: true}})
	operation := plan.Operations()[0]
	writer := openWriter(t, runID())
	for _, entry := range []journal.Entry{
		{RunID: runID(), Type: journal.EventRunStarted},
		{RunID: runID(), Type: journal.EventAttemptStarted, OperationID: operation.ID(), TargetID: operation.TargetID(), Payload: journal.Payload{Attempt: 1}},
	} {
		if _, err := writer.Append(entry); err != nil {
			t.Fatal(err)
		}
	}
	observer := &journalExecutionObserver{
		runID: runID(), writer: writer, operation: operation, attempt: 1,
	}
	if err := observer.SideEffectDispatched(); err == nil {
		t.Fatal("dispatch before provider start accepted")
	}
	if err := observer.ProviderResponseReceived(); err == nil {
		t.Fatal("response before provider start accepted")
	}
	if err := observer.ProviderProcessStarted(); err != nil {
		t.Fatal(err)
	}
	if err := observer.ProviderProcessStarted(); err == nil {
		t.Fatal("duplicate provider start accepted")
	}
	if err := observer.SideEffectDispatched(); err != nil {
		t.Fatal(err)
	}
	if err := observer.SideEffectDispatched(); err == nil {
		t.Fatal("duplicate dispatch accepted")
	}
	if err := observer.ProviderResponseReceived(); err != nil {
		t.Fatal(err)
	}
	if err := observer.ProviderResponseReceived(); err == nil {
		t.Fatal("duplicate response accepted")
	}
	started, dispatched, responded := observer.snapshot()
	if !started || !dispatched || !responded {
		t.Fatalf("snapshot=(%v,%v,%v)", started, dispatched, responded)
	}
}

func TestExecutionBoundaryHelpers(t *testing.T) {
	if reportsExecutionBoundaries(&scriptedDriver{}) {
		t.Fatal("ordinary driver reported execution boundaries")
	}
	if reportsExecutionBoundaries(boundaryReportingDriver{}) {
		t.Fatal("disabled boundary reporter accepted")
	}
	if !reportsExecutionBoundaries(boundaryReportingDriver{enabled: true}) {
		t.Fatal("enabled boundary reporter ignored")
	}

	if !providerResponseReceived(nil) {
		t.Fatal("successful response not recognized")
	}
	if !providerResponseReceived(&DriverError{Code: "PERMANENT"}) {
		t.Fatal("structured provider response not recognized")
	}
	if providerResponseReceived(&DriverError{Code: " bad"}) {
		t.Fatal("invalid driver error recognized as response")
	}
	if providerResponseReceived(errors.New("transport failure")) {
		t.Fatal("transport failure recognized as response")
	}
}

func TestLeaseHelperFailurePaths(t *testing.T) {
	plan := testPlan(t, []operationSpec{{id: "op-a"}})
	operation := plan.Operations()[0]
	writer := openWriter(t, runID())
	now := time.Now().UTC()
	valid := LeaseState{
		ID: "lease-a", Owner: "worker-a", OperationID: operation.ID(),
		AcquiredAt: now.Add(-time.Second), ExpiresAt: now.Add(time.Second),
	}
	mismatch := valid
	mismatch.OperationID = "op-other"
	if err := appendLeaseEvent(runID(), writer, operation, journal.EventLeaseAcquired, mismatch); err == nil {
		t.Fatal("mismatched lease operation accepted")
	}

	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	active := &coverageLease{state: valid}
	if err := renewLease(cancelled, func() {}, runID(), writer, operation, active); err != nil {
		t.Fatalf("cancelled renewal err=%v", err)
	}

	expiredState := valid
	expiredState.ExpiresAt = now.Add(-time.Millisecond)
	expired := &coverageLease{state: expiredState}
	cancelCalled := false
	if err := renewLease(context.Background(), func() { cancelCalled = true }, runID(), writer, operation, expired); !errors.Is(err, ErrLeaseExpired) {
		t.Fatalf("expired renewal err=%v", err)
	}
	if !cancelCalled {
		t.Fatal("expired renewal did not cancel task")
	}

	for _, entry := range []journal.Entry{
		{RunID: runID(), Type: journal.EventRunStarted},
		{RunID: runID(), Type: journal.EventOperationReady, OperationID: operation.ID(), TargetID: operation.TargetID()},
		{RunID: runID(), Type: journal.EventLeaseAcquired, OperationID: operation.ID(), TargetID: operation.TargetID(), Payload: journal.Payload{Lease: &journal.LeasePayload{
			ID: expiredState.ID, Owner: expiredState.Owner, AcquiredAt: expiredState.AcquiredAt, ExpiresAt: expiredState.ExpiresAt,
		}}},
	} {
		if _, err := writer.Append(entry); err != nil {
			t.Fatal(err)
		}
	}
	releaseExpired := &coverageLease{state: expiredState, releaseErr: ErrLeaseExpired}
	if err := releaseLease(runID(), writer, operation, releaseExpired); !errors.Is(err, ErrLeaseExpired) {
		t.Fatalf("expired release err=%v", err)
	}
	events := writer.Events()
	if events[len(events)-1].Type != journal.EventLeaseExpired {
		t.Fatalf("events=%+v", events)
	}

	want := errors.New("release failed")
	releaseFailed := &coverageLease{state: valid, releaseErr: want}
	count := len(writer.Events())
	if err := releaseLease(runID(), writer, operation, releaseFailed); !errors.Is(err, want) {
		t.Fatalf("release err=%v", err)
	}
	if len(writer.Events()) != count {
		t.Fatal("generic release failure journaled a terminal lease event")
	}
}

func TestLeaseExpirySchedulingHelpers(t *testing.T) {
	plan := testPlan(t, []operationSpec{{id: "op-a"}})
	writer := openWriter(t, runID())
	now := time.Now().UTC()
	for _, entry := range []journal.Entry{
		{RunID: runID(), Type: journal.EventRunStarted},
		{RunID: runID(), Type: journal.EventOperationReady, OperationID: "op-a", TargetID: "target-a"},
		{RunID: runID(), Type: journal.EventLeaseAcquired, OperationID: "op-a", TargetID: "target-a", Payload: journal.Payload{Lease: &journal.LeasePayload{
			ID: "lease-a", Owner: "worker-a", AcquiredAt: now.Add(-time.Second), ExpiresAt: now.Add(time.Second),
		}}},
	} {
		if _, err := writer.Append(entry); err != nil {
			t.Fatal(err)
		}
	}
	state, err := journal.Reduce(plan, writer.Events())
	if err != nil {
		t.Fatal(err)
	}
	expiry, ok := nextLeaseExpiry(state)
	if !ok || !expiry.Equal(now.Add(time.Second)) {
		t.Fatalf("expiry=%v ok=%v", expiry, ok)
	}
	changed, err := expireJournalLeases(runID(), writer, state, now)
	if err != nil || changed {
		t.Fatalf("future lease changed=%v err=%v", changed, err)
	}
	changed, err = expireJournalLeases(runID(), writer, state, now.Add(2*time.Second))
	if err != nil || !changed {
		t.Fatalf("expired lease changed=%v err=%v", changed, err)
	}

	empty, err := journal.Reduce(plan, []journal.Event{{
		SchemaVersion: journal.SchemaVersion,
		Sequence:      1,
		RunID:         runID(),
		Type:          journal.EventRunStarted,
		ObservedAt:    time.Unix(1, 0).UTC(),
		Payload:       journal.Payload{},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if expiry, ok := nextLeaseExpiry(empty); ok || !expiry.IsZero() {
		t.Fatalf("unexpected expiry=%v ok=%v", expiry, ok)
	}
}

func TestExecutionObserverWriterFailures(t *testing.T) {
	plan := testPlan(t, []operationSpec{{id: "op-a", sideEffecting: true}})
	operation := plan.Operations()[0]
	writer := openWriter(t, runID())
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}

	start := &journalExecutionObserver{runID: runID(), writer: writer, operation: operation, attempt: 1}
	if err := start.ProviderProcessStarted(); !errors.Is(err, journal.ErrWriterClosed) {
		t.Fatalf("start err=%v", err)
	}
	dispatch := &journalExecutionObserver{runID: runID(), writer: writer, operation: operation, attempt: 1, started: true}
	if err := dispatch.SideEffectDispatched(); !errors.Is(err, journal.ErrWriterClosed) {
		t.Fatalf("dispatch err=%v", err)
	}
	response := &journalExecutionObserver{runID: runID(), writer: writer, operation: operation, attempt: 1, started: true}
	if err := response.ProviderResponseReceived(); !errors.Is(err, journal.ErrWriterClosed) {
		t.Fatalf("response err=%v", err)
	}
}

func TestResultEntryAndCategoryBranches(t *testing.T) {
	plan := testPlan(t, []operationSpec{{id: "op-a"}})
	operation := plan.Operations()[0]
	cases := []struct {
		state     domain.NormalizedState
		retryable bool
		eventType journal.EventType
		category  string
	}{
		{domain.StatePublished, false, journal.EventOperationPublished, journal.ResultCategoryPublished},
		{domain.StateWaitingExternal, false, journal.EventOperationWaitingExternal, journal.ResultCategoryWaitingExternal},
		{domain.StateRejected, false, journal.EventOperationRejected, journal.ResultCategoryRejected},
		{domain.StateFailed, true, journal.EventOperationFailed, journal.ResultCategoryFailedRetryable},
		{domain.StateFailed, false, journal.EventOperationFailed, journal.ResultCategoryFailedPermanent},
		{domain.StateCancelled, false, journal.EventOperationResult, journal.ResultCategoryCancelled},
	}
	for _, tc := range cases {
		entry := resultEntry(runID(), operation, 1, tc.state, "provider", json.RawMessage(`{"ok":true}`), "", tc.retryable)
		if entry.Type != tc.eventType || entry.Payload.ResultCategory != tc.category {
			t.Fatalf("state=%s entry=%+v", tc.state, entry)
		}
		if tc.eventType != journal.EventOperationResult && entry.Payload.State != "" {
			t.Fatalf("state=%s explicit event retained state=%s", tc.state, entry.Payload.State)
		}
	}
	if got := resultCategory(domain.StateRunning, false); got != "" {
		t.Fatalf("unexpected result category=%q", got)
	}

	reconcile := reconcileResultEntry(runID(), operation, 1, domain.StatePublished, "published", json.RawMessage(`{"ok":true}`), "", false)
	if reconcile.Type != journal.EventReconcileResult || reconcile.Payload.ResultCategory != journal.ResultCategoryPublished {
		t.Fatalf("reconcile=%+v", reconcile)
	}
	ambiguous := ambiguousEntry(runID(), operation, 1, "UNKNOWN")
	if ambiguous.Type != journal.EventOutcomeAmbiguous || ambiguous.Payload.ResultCategory != journal.ResultCategoryAmbiguous {
		t.Fatalf("ambiguous=%+v", ambiguous)
	}
}

func TestOperationContextBranches(t *testing.T) {
	ctx, cancel := operationContext(context.Background(), time.Millisecond)
	if _, ok := ctx.Deadline(); !ok {
		t.Fatal("timeout context has no deadline")
	}
	cancel()

	ctx, cancel = operationContext(context.Background(), 0)
	if _, ok := ctx.Deadline(); ok {
		t.Fatal("zero-timeout context unexpectedly has a deadline")
	}
	cancel()
	if !errors.Is(ctx.Err(), context.Canceled) {
		t.Fatalf("context err=%v", ctx.Err())
	}
}

func TestRenewLeaseErrorAndJournalFailure(t *testing.T) {
	plan := testPlan(t, []operationSpec{{id: "op-a"}})
	operation := plan.Operations()[0]
	now := time.Now().UTC()
	state := LeaseState{
		ID: "lease-a", Owner: "worker-a", OperationID: operation.ID(),
		AcquiredAt: now, ExpiresAt: now.Add(20 * time.Millisecond),
	}

	want := errors.New("renew failed")
	failing := &coverageLease{state: state, renewErr: want}
	cancelCalled := false
	if err := renewLease(context.Background(), func() { cancelCalled = true }, runID(), openWriter(t, runID()), operation, failing); !errors.Is(err, want) {
		t.Fatalf("renew err=%v", err)
	}
	if !cancelCalled {
		t.Fatal("renewal failure did not cancel task")
	}

	writer := openWriter(t, runID())
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	renewed := state
	renewed.ExpiresAt = now.Add(time.Second)
	journalFail := &coverageLease{state: state, renewState: renewed}
	cancelCalled = false
	if err := renewLease(context.Background(), func() { cancelCalled = true }, runID(), writer, operation, journalFail); !errors.Is(err, journal.ErrWriterClosed) {
		t.Fatalf("journal renewal err=%v", err)
	}
	if !cancelCalled {
		t.Fatal("journal renewal failure did not cancel task")
	}
}

func TestMemoryLeaseAdditionalBranches(t *testing.T) {
	if id, err := randomLeaseID(); err != nil || len(id) != 32 {
		t.Fatalf("id=%q err=%v", id, err)
	}

	zeroClock := newMemoryLeases("worker-a", time.Second, func() time.Time { return time.Time{} }, func() (string, error) {
		return "lease-a", nil
	})
	if _, err := zeroClock.Acquire(context.Background(), LeaseRequest{Key: "key", OperationID: "op-a"}); err == nil {
		t.Fatal("zero lease clock accepted")
	}

	now := time.Now().UTC()
	leases := newMemoryLeases("worker-a", time.Second, func() time.Time { return now }, func() (string, error) {
		return "lease-a", nil
	})
	lease, err := leases.Acquire(context.Background(), LeaseRequest{Key: "key", OperationID: "op-a"})
	if err != nil {
		t.Fatal(err)
	}
	if err := lease.Release(); err != nil {
		t.Fatal(err)
	}
	if err := lease.Release(); err != nil {
		t.Fatalf("second release err=%v", err)
	}
	if _, err := lease.Renew(context.Background()); !errors.Is(err, ErrLeaseExpired) {
		t.Fatalf("renew after release err=%v", err)
	}
}

func TestRecoveryAndQuiescenceHelperBranches(t *testing.T) {
	t.Run("ready journaling", func(t *testing.T) {
		plan := testPlan(t, []operationSpec{{id: "op-a"}})
		writer := openWriter(t, runID())
		if _, err := writer.Append(journal.Entry{RunID: runID(), Type: journal.EventRunStarted}); err != nil {
			t.Fatal(err)
		}
		state, err := journal.Reduce(plan, writer.Events())
		if err != nil {
			t.Fatal(err)
		}
		changed, err := journalReadyOperations(runID(), writer, state)
		if err != nil || !changed {
			t.Fatalf("changed=%v err=%v", changed, err)
		}
		state, err = journal.Reduce(plan, writer.Events())
		if err != nil {
			t.Fatal(err)
		}
		changed, err = journalReadyOperations(runID(), writer, state)
		if err != nil || changed {
			t.Fatalf("second changed=%v err=%v", changed, err)
		}
		if executorQuiescent(state, 3) {
			t.Fatal("ready operation considered quiescent")
		}
	})

	cases := []struct {
		name          string
		sideEffecting bool
		providerStart bool
		wantCode      string
	}{
		{"before provider", false, false, "INTERRUPTED_BEFORE_PROVIDER"},
		{"non-side provider crash", false, true, "PROVIDER_INTERRUPTED"},
		{"side provider crash", true, true, "INTERRUPTED_BEFORE_DISPATCH"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			plan := testPlan(t, []operationSpec{{id: "op-a", sideEffecting: tc.sideEffecting}})
			writer := openWriter(t, runID())
			entries := []journal.Entry{
				{RunID: runID(), Type: journal.EventRunStarted},
				{RunID: runID(), Type: journal.EventOperationReady, OperationID: "op-a", TargetID: "target-a"},
				{RunID: runID(), Type: journal.EventAttemptStarted, OperationID: "op-a", TargetID: "target-a", Payload: journal.Payload{Attempt: 1, AttemptReason: journal.AttemptReasonInitial}},
			}
			if tc.providerStart {
				entries = append(entries, journal.Entry{
					RunID: runID(), Type: journal.EventProviderProcessStarted, OperationID: "op-a", TargetID: "target-a",
					Payload: journal.Payload{Attempt: 1},
				})
			}
			for _, entry := range entries {
				if _, err := writer.Append(entry); err != nil {
					t.Fatal(err)
				}
			}
			state, err := journal.Reduce(plan, writer.Events())
			if err != nil {
				t.Fatal(err)
			}
			changed, err := recoverUndispatched(runID(), writer, state, map[domain.OperationID]domain.Operation{"op-a": plan.Operations()[0]})
			if err != nil || !changed {
				t.Fatalf("changed=%v err=%v", changed, err)
			}
			state, err = journal.Reduce(plan, writer.Events())
			if err != nil {
				t.Fatal(err)
			}
			op, _ := state.Operation("op-a")
			if op.State != domain.StateFailed || op.ErrorCode != tc.wantCode || !op.Retryable {
				t.Fatalf("op=%+v", op)
			}
			if executorQuiescent(state, 3) {
				t.Fatal("retryable failure below budget considered quiescent")
			}
			if !executorQuiescent(state, 1) {
				t.Fatal("exhausted retry budget not quiescent")
			}
		})
	}
}
