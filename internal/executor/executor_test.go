package executor

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aalsanie/distroplane/internal/domain"
	"github.com/aalsanie/distroplane/internal/journal"
)

type scriptedDriver struct {
	mu             sync.Mutex
	apply          func(context.Context, Request) (Result, error)
	reconcile      func(context.Context, Request) (Result, error)
	applyCalls     []Request
	reconcileCalls []Request
	active         atomic.Int32
	peak           atomic.Int32
}

func (d *scriptedDriver) Apply(ctx context.Context, request Request) (Result, error) {
	d.mu.Lock()
	d.applyCalls = append(d.applyCalls, request)
	fn := d.apply
	d.mu.Unlock()
	active := d.active.Add(1)
	for {
		peak := d.peak.Load()
		if active <= peak || d.peak.CompareAndSwap(peak, active) {
			break
		}
	}
	defer d.active.Add(-1)
	if fn == nil {
		return published(), nil
	}
	return fn(ctx, request)
}

func (d *scriptedDriver) Reconcile(ctx context.Context, request Request) (Result, error) {
	d.mu.Lock()
	d.reconcileCalls = append(d.reconcileCalls, request)
	fn := d.reconcile
	d.mu.Unlock()
	if fn == nil {
		return published(), nil
	}
	return fn(ctx, request)
}

func published() Result {
	return Result{State: domain.StatePublished, ProviderState: "published", Evidence: json.RawMessage(`{"provider":"test"}`)}
}

func waiting() Result {
	return Result{State: domain.StateWaitingExternal, ProviderState: "pending", Evidence: json.RawMessage(`{"provider":"test"}`)}
}

func testPlan(t testing.TB, operations []operationSpec) domain.Plan {
	t.Helper()
	digest, err := domain.NewSHA256Digest(strings.Repeat("a", 64))
	if err != nil {
		t.Fatal(err)
	}
	artifact, err := domain.NewArtifact("app", filepath.Join(t.TempDir(), "app"), digest, 1, "application/octet-stream")
	if err != nil {
		t.Fatal(err)
	}
	releaseID, _ := domain.NewReleaseID("release-1")
	release, err := domain.NewRelease(releaseID, []domain.Artifact{artifact})
	if err != nil {
		t.Fatal(err)
	}
	providerName, _ := domain.NewProviderName("fake")
	providerVersion, _ := domain.NewProviderVersion("1")
	provider, _ := domain.NewProviderRef(providerName, providerVersion)
	configuration, _ := domain.NewJSONValue([]byte(`{}`))
	targetID, _ := domain.NewTargetID("target-a")
	target, _ := domain.NewTarget(targetID, provider, configuration)
	payload, _ := domain.NewJSONValue([]byte(`{}`))

	domainOperations := make([]domain.Operation, 0, len(operations))
	for _, spec := range operations {
		id, _ := domain.NewOperationID(spec.id)
		dependencies := make([]domain.OperationID, len(spec.dependencies))
		for i, dependency := range spec.dependencies {
			dependencies[i], _ = domain.NewOperationID(dependency)
		}
		key := ""
		if spec.sideEffecting {
			key = "key-" + spec.id
		}
		timeout := spec.timeout
		operation, err := domain.NewOperation(id, targetID, provider, "publish", dependencies, spec.sideEffecting, key, timeout, payload)
		if err != nil {
			t.Fatal(err)
		}
		domainOperations = append(domainOperations, operation)
	}
	planID, _ := domain.NewPlanID("plan-1")
	plan, err := domain.NewPlan(planID, "1", "1", release, []domain.Target{target}, domainOperations)
	if err != nil {
		t.Fatal(err)
	}
	return plan
}

type operationSpec struct {
	id            string
	dependencies  []string
	sideEffecting bool
	timeout       time.Duration
}

func runID() domain.RunID {
	id, _ := domain.NewRunID("run-1")
	return id
}

func openWriter(t testing.TB, run domain.RunID) *journal.Writer {
	t.Helper()
	writer, err := journal.OpenWriter(filepath.Join(t.TempDir(), "run.journal"), run)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = writer.Close() })
	return writer
}

func newExecutor(t testing.TB, driver Driver, options Options) *Executor {
	t.Helper()
	if options.Backoff == nil {
		options.Backoff = func(uint32) time.Duration { return 0 }
	}
	executor, err := New(driver, options)
	if err != nil {
		t.Fatal(err)
	}
	return executor
}

func TestExecuteDAGConcurrencyAndLifecycle(t *testing.T) {
	plan := testPlan(t, []operationSpec{
		{id: "op-a", sideEffecting: true},
		{id: "op-b", sideEffecting: true},
		{id: "op-c", dependencies: []string{"op-a", "op-b"}},
	})
	gate := make(chan struct{})
	started := make(chan struct{}, 2)
	driver := &scriptedDriver{apply: func(ctx context.Context, request Request) (Result, error) {
		if request.Operation.ID() == "op-a" || request.Operation.ID() == "op-b" {
			started <- struct{}{}
			select {
			case <-gate:
			case <-ctx.Done():
				return Result{}, ctx.Err()
			}
		}
		return published(), nil
	}}
	executor := newExecutor(t, driver, Options{MaxConcurrency: 2})
	writer := openWriter(t, runID())
	done := make(chan struct{})
	var state journal.DerivedState
	var executeErr error
	go func() {
		state, executeErr = executor.Execute(context.Background(), plan, runID(), writer)
		close(done)
	}()
	<-started
	<-started
	close(gate)
	<-done
	if executeErr != nil {
		t.Fatal(executeErr)
	}
	if !state.Completed || state.Cancelled {
		t.Fatalf("state=%+v", state)
	}
	if peak := driver.peak.Load(); peak != 2 {
		t.Fatalf("peak concurrency=%d", peak)
	}
	driver.mu.Lock()
	calls := append([]Request(nil), driver.applyCalls...)
	driver.mu.Unlock()
	if len(calls) != 3 || calls[2].Operation.ID() != "op-c" {
		t.Fatalf("calls=%+v", calls)
	}
	for _, operation := range state.Operations() {
		if operation.State != domain.StatePublished || operation.Attempt != 1 {
			t.Fatalf("operation=%+v", operation)
		}
	}
	events := writer.Events()
	if events[0].Type != journal.EventRunStarted || events[len(events)-1].Type != journal.EventRunCompleted {
		t.Fatalf("events=%+v", events)
	}
}

func TestExecuteRetriesOnlyRetryableFailures(t *testing.T) {
	plan := testPlan(t, []operationSpec{{id: "op-a"}})
	var attempts atomic.Uint32
	driver := &scriptedDriver{apply: func(context.Context, Request) (Result, error) {
		if attempts.Add(1) < 3 {
			return Result{}, &DriverError{Code: "TRANSIENT", Retryable: true}
		}
		return published(), nil
	}}
	executor := newExecutor(t, driver, Options{MaxAttempts: 3})
	state, err := executor.Execute(context.Background(), plan, runID(), openWriter(t, runID()))
	if err != nil {
		t.Fatal(err)
	}
	op, _ := state.Operation("op-a")
	if !state.Completed || op.State != domain.StatePublished || op.Attempt != 3 {
		t.Fatalf("state=%+v op=%+v", state, op)
	}

	permanent := &scriptedDriver{apply: func(context.Context, Request) (Result, error) {
		return Result{}, &DriverError{Code: "PERMANENT", Retryable: false}
	}}
	state, err = newExecutor(t, permanent, Options{MaxAttempts: 5}).Execute(context.Background(), plan, "run-2", openWriter(t, "run-2"))
	if err != nil {
		t.Fatal(err)
	}
	op, _ = state.Operation("op-a")
	if op.Attempt != 1 || op.Retryable || op.State != domain.StateFailed {
		t.Fatalf("op=%+v", op)
	}
}

func TestExecuteRetryBudgetCancelsDependents(t *testing.T) {
	plan := testPlan(t, []operationSpec{{id: "op-a"}, {id: "op-b", dependencies: []string{"op-a"}}})
	driver := &scriptedDriver{apply: func(context.Context, Request) (Result, error) {
		return Result{}, &DriverError{Code: "TRANSIENT", Retryable: true}
	}}
	state, err := newExecutor(t, driver, Options{MaxAttempts: 2}).Execute(context.Background(), plan, runID(), openWriter(t, runID()))
	if err != nil {
		t.Fatal(err)
	}
	a, _ := state.Operation("op-a")
	b, _ := state.Operation("op-b")
	if !state.Completed || a.Attempt != 2 || a.State != domain.StateFailed || b.State != domain.StateCancelled || b.ErrorCode != "DEPENDENCY_TERMINAL" {
		t.Fatalf("state=%+v a=%+v b=%+v", state, a, b)
	}
}

func TestExecuteAmbiguousSideEffectReconcilesBeforeAnyRetry(t *testing.T) {
	plan := testPlan(t, []operationSpec{{id: "op-a", sideEffecting: true}})
	driver := &scriptedDriver{
		apply: func(context.Context, Request) (Result, error) {
			return Result{}, &DriverError{Code: "LOST_RESPONSE", Retryable: true, Ambiguous: true}
		},
		reconcile: func(_ context.Context, request Request) (Result, error) {
			if request.Previous == nil || !request.Previous.Ambiguous || request.Attempt != 1 {
				t.Fatalf("request=%+v", request)
			}
			return published(), nil
		},
	}
	writer := openWriter(t, runID())
	state, err := newExecutor(t, driver, Options{MaxAttempts: 5}).Execute(context.Background(), plan, runID(), writer)
	if err != nil {
		t.Fatal(err)
	}
	op, _ := state.Operation("op-a")
	if op.State != domain.StatePublished || op.Attempt != 1 || op.ReconcileRequired || op.Ambiguous {
		t.Fatalf("op=%+v", op)
	}
	driver.mu.Lock()
	applyCalls, reconcileCalls := len(driver.applyCalls), len(driver.reconcileCalls)
	driver.mu.Unlock()
	if applyCalls != 1 || reconcileCalls != 1 {
		t.Fatalf("apply=%d reconcile=%d", applyCalls, reconcileCalls)
	}
	seenDispatch, seenAmbiguous, seenReconcile := false, false, false
	for _, event := range writer.Events() {
		seenDispatch = seenDispatch || event.Type == journal.EventSideEffectDispatched
		seenAmbiguous = seenAmbiguous || event.Type == journal.EventOutcomeAmbiguous
		seenReconcile = seenReconcile || event.Type == journal.EventReconcileResult
	}
	if !seenDispatch || !seenAmbiguous || !seenReconcile {
		t.Fatalf("events=%+v", writer.Events())
	}
}

func TestExecuteWaitingExternalDefersReconcileUntilResume(t *testing.T) {
	plan := testPlan(t, []operationSpec{{id: "op-a", sideEffecting: true}})
	driver := &scriptedDriver{
		apply:     func(context.Context, Request) (Result, error) { return waiting(), nil },
		reconcile: func(context.Context, Request) (Result, error) { return published(), nil },
	}
	writer := openWriter(t, runID())
	executor := newExecutor(t, driver, Options{})
	state, err := executor.Execute(context.Background(), plan, runID(), writer)
	if err != nil {
		t.Fatal(err)
	}
	op, _ := state.Operation("op-a")
	if state.Completed || op.State != domain.StateWaitingExternal || !op.ReconcileRequired {
		t.Fatalf("state=%+v op=%+v", state, op)
	}
	driver.mu.Lock()
	if len(driver.reconcileCalls) != 0 {
		t.Fatalf("unexpected reconcile calls=%d", len(driver.reconcileCalls))
	}
	driver.mu.Unlock()
	state, err = executor.Execute(context.Background(), plan, runID(), writer)
	if err != nil {
		t.Fatal(err)
	}
	op, _ = state.Operation("op-a")
	if !state.Completed || op.State != domain.StatePublished {
		t.Fatalf("state=%+v op=%+v", state, op)
	}
}

func TestExecuteRecoversDispatchedAttemptByReconciling(t *testing.T) {
	plan := testPlan(t, []operationSpec{{id: "op-a", sideEffecting: true}})
	writer := openWriter(t, runID())
	entries := []journal.Entry{
		{RunID: runID(), Type: journal.EventRunStarted},
		{RunID: runID(), Type: journal.EventAttemptStarted, OperationID: "op-a", TargetID: "target-a", Payload: journal.Payload{Attempt: 1}},
		{RunID: runID(), Type: journal.EventSideEffectDispatched, OperationID: "op-a", TargetID: "target-a", Payload: journal.Payload{Attempt: 1}},
	}
	for _, entry := range entries {
		if _, err := writer.Append(entry); err != nil {
			t.Fatal(err)
		}
	}
	driver := &scriptedDriver{reconcile: func(context.Context, Request) (Result, error) { return published(), nil }}
	state, err := newExecutor(t, driver, Options{}).Execute(context.Background(), plan, runID(), writer)
	if err != nil {
		t.Fatal(err)
	}
	driver.mu.Lock()
	applyCalls, reconcileCalls := len(driver.applyCalls), len(driver.reconcileCalls)
	driver.mu.Unlock()
	if applyCalls != 0 || reconcileCalls != 1 || !state.Completed {
		t.Fatalf("apply=%d reconcile=%d state=%+v", applyCalls, reconcileCalls, state)
	}
}

func TestExecuteCancellationAndLaterReconciliation(t *testing.T) {
	plan := testPlan(t, []operationSpec{{id: "op-a", sideEffecting: true, timeout: time.Second}, {id: "op-b"}})
	started := make(chan struct{})
	driver := &scriptedDriver{
		apply: func(ctx context.Context, request Request) (Result, error) {
			if request.Operation.ID() == "op-a" {
				close(started)
				<-ctx.Done()
				return Result{}, ctx.Err()
			}
			return published(), nil
		},
		reconcile: func(context.Context, Request) (Result, error) { return published(), nil },
	}
	ctx, cancel := context.WithCancel(context.Background())
	writer := openWriter(t, runID())
	executor := newExecutor(t, driver, Options{MaxConcurrency: 1})
	var state journal.DerivedState
	var err error
	done := make(chan struct{})
	go func() {
		state, err = executor.Execute(ctx, plan, runID(), writer)
		close(done)
	}()
	<-started
	cancel()
	<-done
	if !errors.Is(err, context.Canceled) || !state.Cancelled {
		t.Fatalf("state=%+v err=%v", state, err)
	}
	a, _ := state.Operation("op-a")
	b, _ := state.Operation("op-b")
	if !a.ReconcileRequired || !a.Ambiguous || b.State != domain.StateCancelled {
		t.Fatalf("a=%+v b=%+v", a, b)
	}
	state, err = executor.Execute(context.Background(), plan, runID(), writer)
	if err != nil {
		t.Fatal(err)
	}
	a, _ = state.Operation("op-a")
	if !state.Cancelled || a.State != domain.StatePublished || a.ReconcileRequired {
		t.Fatalf("state=%+v a=%+v", state, a)
	}
}

func TestExecuteSideEffectTimeoutIsAmbiguous(t *testing.T) {
	plan := testPlan(t, []operationSpec{{id: "op-a", sideEffecting: true, timeout: time.Millisecond}})
	driver := &scriptedDriver{
		apply: func(ctx context.Context, _ Request) (Result, error) {
			<-ctx.Done()
			return Result{}, ctx.Err()
		},
		reconcile: func(context.Context, Request) (Result, error) { return published(), nil },
	}
	state, err := newExecutor(t, driver, Options{}).Execute(context.Background(), plan, runID(), openWriter(t, runID()))
	if err != nil {
		t.Fatal(err)
	}
	op, _ := state.Operation("op-a")
	if !state.Completed || op.State != domain.StatePublished || op.Attempt != 1 {
		t.Fatalf("state=%+v op=%+v", state, op)
	}
}

func TestExecuteNonSideEffectTimeoutRetries(t *testing.T) {
	plan := testPlan(t, []operationSpec{{id: "op-a", timeout: time.Millisecond}})
	var calls atomic.Uint32
	driver := &scriptedDriver{apply: func(ctx context.Context, _ Request) (Result, error) {
		if calls.Add(1) == 1 {
			<-ctx.Done()
			return Result{}, ctx.Err()
		}
		return published(), nil
	}}
	state, err := newExecutor(t, driver, Options{MaxAttempts: 2}).Execute(context.Background(), plan, runID(), openWriter(t, runID()))
	if err != nil {
		t.Fatal(err)
	}
	op, _ := state.Operation("op-a")
	if op.State != domain.StatePublished || op.Attempt != 2 {
		t.Fatalf("op=%+v", op)
	}
}

func TestExecuteLeaseContentionDoesNotStartAttempt(t *testing.T) {
	plan := testPlan(t, []operationSpec{{id: "op-a"}})
	leases := NewMemoryLeases()
	lease, err := leases.Acquire(context.Background(), leaseKey(plan.ID(), "op-a"))
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Release()
	writer := openWriter(t, runID())
	state, err := newExecutor(t, &scriptedDriver{}, Options{Leases: leases}).Execute(context.Background(), plan, runID(), writer)
	if !errors.Is(err, ErrLeaseHeld) || state.Completed {
		t.Fatalf("state=%+v err=%v", state, err)
	}
	if len(writer.Events()) != 1 || writer.Events()[0].Type != journal.EventRunStarted {
		t.Fatalf("events=%+v", writer.Events())
	}
}

func TestExecuteRejectsMalformedDriverResultsSafely(t *testing.T) {
	for _, sideEffecting := range []bool{false, true} {
		t.Run(map[bool]string{false: "safe", true: "side-effecting"}[sideEffecting], func(t *testing.T) {
			plan := testPlan(t, []operationSpec{{id: "op-a", sideEffecting: sideEffecting}})
			driver := &scriptedDriver{apply: func(context.Context, Request) (Result, error) {
				return Result{State: domain.StatePublished}, nil
			}}
			writer := openWriter(t, runID())
			state, err := newExecutor(t, driver, Options{}).Execute(context.Background(), plan, runID(), writer)
			if !errors.Is(err, ErrDriverContract) {
				t.Fatalf("err=%v", err)
			}
			state, reduceErr := journal.Reduce(plan, writer.Events())
			if reduceErr != nil {
				t.Fatal(reduceErr)
			}
			op, _ := state.Operation("op-a")
			if sideEffecting {
				if !op.Ambiguous || !op.ReconcileRequired {
					t.Fatalf("op=%+v", op)
				}
			} else if op.State != domain.StateFailed {
				t.Fatalf("op=%+v", op)
			}
		})
	}
}

func TestExecuteUnknownSideEffectErrorBecomesAmbiguous(t *testing.T) {
	plan := testPlan(t, []operationSpec{{id: "op-a", sideEffecting: true}})
	driver := &scriptedDriver{
		apply:     func(context.Context, Request) (Result, error) { return Result{}, errors.New("transport broke") },
		reconcile: func(context.Context, Request) (Result, error) { return published(), nil },
	}
	state, err := newExecutor(t, driver, Options{}).Execute(context.Background(), plan, runID(), openWriter(t, runID()))
	if err != nil {
		t.Fatal(err)
	}
	op, _ := state.Operation("op-a")
	if op.State != domain.StatePublished || op.Attempt != 1 {
		t.Fatalf("op=%+v", op)
	}
}

func TestExecuteResumeCompletedRunIsNoop(t *testing.T) {
	plan := testPlan(t, []operationSpec{{id: "op-a"}})
	driver := &scriptedDriver{}
	writer := openWriter(t, runID())
	executor := newExecutor(t, driver, Options{})
	state, err := executor.Execute(context.Background(), plan, runID(), writer)
	if err != nil || !state.Completed {
		t.Fatalf("state=%+v err=%v", state, err)
	}
	count := len(writer.Events())
	state, err = executor.Execute(context.Background(), plan, runID(), writer)
	if err != nil || !state.Completed || len(writer.Events()) != count {
		t.Fatalf("state=%+v err=%v events=%d", state, err, len(writer.Events()))
	}
}

func TestNewAndExecuteValidation(t *testing.T) {
	if _, err := New(nil, Options{}); err == nil {
		t.Fatal("nil driver accepted")
	}
	if _, err := New(&scriptedDriver{}, Options{MaxConcurrency: -1}); err == nil {
		t.Fatal("negative concurrency accepted")
	}
	executor := newExecutor(t, &scriptedDriver{}, Options{})
	plan := testPlan(t, []operationSpec{{id: "op-a"}})
	writer := openWriter(t, runID())
	if _, err := executor.Execute(nil, plan, runID(), writer); err == nil {
		t.Fatal("nil context accepted")
	}
	if _, err := executor.Execute(context.Background(), domain.Plan{}, runID(), writer); err == nil {
		t.Fatal("invalid plan accepted")
	}
	if _, err := executor.Execute(context.Background(), plan, "", writer); err == nil {
		t.Fatal("invalid run accepted")
	}
	if _, err := executor.Execute(context.Background(), plan, runID(), nil); err == nil {
		t.Fatal("nil writer accepted")
	}
	var nilExecutor *Executor
	if _, err := nilExecutor.Execute(context.Background(), plan, runID(), writer); err == nil {
		t.Fatal("nil executor accepted")
	}
}

func TestResultAndDriverErrorValidation(t *testing.T) {
	if err := published().validate(); err != nil {
		t.Fatal(err)
	}
	for _, result := range []Result{
		{State: domain.StateFailed, Evidence: json.RawMessage(`{}`)},
		{State: domain.StatePublished},
		{State: domain.StatePublished, ProviderState: " bad", Evidence: json.RawMessage(`{}`)},
		{State: domain.StatePublished, Evidence: json.RawMessage(`{`)},
	} {
		if err := result.validate(); !errors.Is(err, ErrDriverContract) {
			t.Fatalf("result=%+v err=%v", result, err)
		}
	}
	if (&DriverError{Code: "TRANSIENT"}).Error() != "TRANSIENT" {
		t.Fatal("unexpected driver error text")
	}
	if (&DriverError{Message: "message"}).Error() != "message" {
		t.Fatal("driver message not preferred")
	}
	var nilError *DriverError
	if nilError.Error() != "" || nilError.valid() {
		t.Fatal("nil driver error invalidity not preserved")
	}
	if (&DriverError{Code: " bad"}).valid() {
		t.Fatal("invalid driver error code accepted")
	}
}
