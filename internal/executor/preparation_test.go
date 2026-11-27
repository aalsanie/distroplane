package executor

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/aalsanie/distroplane/internal/domain"
	"github.com/aalsanie/distroplane/internal/journal"
)

type preparationDriver struct {
	scriptedDriver
	prepare func(context.Context, Request, DriverOperation) (Preparation, error)
}

func (d *preparationDriver) Prepare(ctx context.Context, request Request, operation DriverOperation) (Preparation, error) {
	if d.prepare == nil {
		return Preparation{Context: ctx}, nil
	}
	return d.prepare(ctx, request, operation)
}

func TestPreparationCompletesBeforeSideEffectDispatch(t *testing.T) {
	plan := testPlan(t, []operationSpec{{id: "op-a", sideEffecting: true}})
	writer := openWriter(t, runID())
	entered := make(chan struct{})
	release := make(chan struct{})
	driver := &preparationDriver{}
	driver.prepare = func(ctx context.Context, request Request, operation DriverOperation) (Preparation, error) {
		if operation != DriverApply || request.Attempt != 1 {
			t.Fatalf("operation=%v request=%+v", operation, request)
		}
		close(entered)
		select {
		case <-release:
			return Preparation{Context: ctx, CredentialRefs: []domain.CredentialRef{"release"}}, nil
		case <-ctx.Done():
			return Preparation{}, ctx.Err()
		}
	}
	engine := newExecutor(t, driver, Options{MaxAttempts: 1})
	done := make(chan error, 1)
	go func() {
		_, err := engine.Execute(context.Background(), plan, runID(), writer)
		done <- err
	}()
	<-entered
	for _, event := range writer.Events() {
		if event.Type == journal.EventSideEffectDispatched {
			t.Fatal("side effect marked dispatched before preparation completed")
		}
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	events := writer.Events()
	attempt, credential, dispatch := -1, -1, -1
	for i, event := range events {
		switch event.Type {
		case journal.EventAttemptStarted:
			attempt = i
		case journal.EventCredentialResolved:
			credential = i
		case journal.EventSideEffectDispatched:
			dispatch = i
		}
	}
	if attempt < 0 || credential <= attempt || dispatch <= credential {
		t.Fatalf("events=%+v", events)
	}
}

func TestPreparationFailureDoesNotDispatch(t *testing.T) {
	plan := testPlan(t, []operationSpec{{id: "op-a", sideEffecting: true}})
	driver := &preparationDriver{}
	driver.prepare = func(context.Context, Request, DriverOperation) (Preparation, error) {
		return Preparation{}, &DriverError{Code: "CREDENTIAL_UNAVAILABLE"}
	}
	writer := openWriter(t, runID())
	state, err := newExecutor(t, driver, Options{MaxAttempts: 1}).Execute(context.Background(), plan, runID(), writer)
	if err != nil {
		t.Fatal(err)
	}
	op, _ := state.Operation("op-a")
	if op.State != domain.StateFailed || op.ErrorCode != "CREDENTIAL_UNAVAILABLE" || op.Ambiguous || op.ReconcileRequired {
		t.Fatalf("op=%+v", op)
	}
	for _, event := range writer.Events() {
		if event.Type == journal.EventSideEffectDispatched || event.Type == journal.EventOutcomeAmbiguous {
			t.Fatalf("event=%+v", event)
		}
	}
}

func TestCancellationBeforeDispatchStopsNewSideEffects(t *testing.T) {
	plan := testPlan(t, []operationSpec{{id: "op-a", sideEffecting: true}})
	entered := make(chan struct{})
	driver := &preparationDriver{}
	driver.prepare = func(ctx context.Context, _ Request, _ DriverOperation) (Preparation, error) {
		close(entered)
		<-ctx.Done()
		return Preparation{}, ctx.Err()
	}
	writer := openWriter(t, runID())
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	var state journal.DerivedState
	var executeErr error
	go func() {
		state, executeErr = newExecutor(t, driver, Options{MaxAttempts: 1}).Execute(ctx, plan, runID(), writer)
		close(done)
	}()
	<-entered
	cancel()
	<-done
	if !errors.Is(executeErr, context.Canceled) || !state.Cancelled {
		t.Fatalf("state=%+v err=%v", state, executeErr)
	}
	for _, event := range writer.Events() {
		if event.Type == journal.EventSideEffectDispatched {
			t.Fatalf("side effect dispatched after cancellation: %+v", event)
		}
	}
}

func TestPreparationContextCancellationIsNotAmbiguous(t *testing.T) {
	plan := testPlan(t, []operationSpec{{id: "op-a", sideEffecting: true}})
	driver := &preparationDriver{}
	driver.prepare = func(ctx context.Context, _ Request, _ DriverOperation) (Preparation, error) {
		cancelled, cancel := context.WithCancel(ctx)
		cancel()
		return Preparation{}, cancelled.Err()
	}
	writer := openWriter(t, runID())
	state, err := newExecutor(t, driver, Options{MaxAttempts: 1}).Execute(context.Background(), plan, runID(), writer)
	if err != nil {
		t.Fatal(err)
	}
	op, _ := state.Operation("op-a")
	if op.State != domain.StateCancelled || op.Ambiguous || op.ReconcileRequired {
		t.Fatalf("op=%+v", op)
	}
}

func TestExecuteRecoversUndispatchedAttempt(t *testing.T) {
	plan := testPlan(t, []operationSpec{{id: "op-a", sideEffecting: true}})
	writer := openWriter(t, runID())
	for _, entry := range []journal.Entry{
		{RunID: runID(), Type: journal.EventRunStarted, PlanID: "plan-1"},
		{RunID: runID(), Type: journal.EventAttemptStarted, OperationID: "op-a", TargetID: "target-a", Payload: journal.Payload{Attempt: 1}},
	} {
		if _, err := writer.Append(entry); err != nil {
			t.Fatal(err)
		}
	}
	driver := &scriptedDriver{}
	state, err := newExecutor(t, driver, Options{MaxAttempts: 2}).Execute(context.Background(), plan, runID(), writer)
	if err != nil {
		t.Fatal(err)
	}
	op, _ := state.Operation("op-a")
	if !state.Completed || op.State != domain.StatePublished || op.Attempt != 2 {
		t.Fatalf("state=%+v op=%+v", state, op)
	}
	seenRecovery := false
	for _, event := range writer.Events() {
		if event.Type == journal.EventOperationFailed && event.Payload.ErrorCode == "INTERRUPTED_BEFORE_PROVIDER" {
			seenRecovery = true
		}
	}
	if !seenRecovery {
		t.Fatal("undispatched attempt was not recovered")
	}
}

func TestExecuteRecoversProviderStartBeforeDispatch(t *testing.T) {
	plan := testPlan(t, []operationSpec{{id: "op-a", sideEffecting: true}})
	writer := openWriter(t, runID())
	for _, entry := range []journal.Entry{
		{RunID: runID(), Type: journal.EventRunStarted, PlanID: "plan-1"},
		{RunID: runID(), Type: journal.EventAttemptStarted, OperationID: "op-a", TargetID: "target-a", Payload: journal.Payload{Attempt: 1}},
		{RunID: runID(), Type: journal.EventProviderProcessStarted, OperationID: "op-a", TargetID: "target-a", Payload: journal.Payload{Attempt: 1}},
	} {
		if _, err := writer.Append(entry); err != nil {
			t.Fatal(err)
		}
	}
	state, err := newExecutor(t, &scriptedDriver{}, Options{MaxAttempts: 2}).Execute(context.Background(), plan, runID(), writer)
	if err != nil {
		t.Fatal(err)
	}
	op, _ := state.Operation("op-a")
	if !state.Completed || op.State != domain.StatePublished || op.Attempt != 2 {
		t.Fatalf("state=%+v op=%+v", state, op)
	}
	seenRecovery := false
	for _, event := range writer.Events() {
		if event.Type == journal.EventOperationFailed && event.Payload.ErrorCode == "INTERRUPTED_BEFORE_DISPATCH" {
			seenRecovery = true
		}
	}
	if !seenRecovery {
		t.Fatal("provider-started attempt was not recovered safely")
	}
}

func TestPreparationInfrastructureFailurePersists(t *testing.T) {
	plan := testPlan(t, []operationSpec{{id: "op-a"}})
	driver := &preparationDriver{}
	driver.prepare = func(context.Context, Request, DriverOperation) (Preparation, error) {
		return Preparation{}, errors.New("preflight failed")
	}
	state, err := newExecutor(t, driver, Options{MaxAttempts: 1}).Execute(context.Background(), plan, runID(), openWriter(t, runID()))
	if err != nil {
		t.Fatal(err)
	}
	op, _ := state.Operation("op-a")
	if op.State != domain.StateFailed || op.ErrorCode != "DRIVER_ERROR" {
		t.Fatalf("op=%+v", op)
	}
}

func TestLegacyOperationBudgetIncludesPreparation(t *testing.T) {
	for _, reconcile := range []bool{false, true} {
		t.Run(fmt.Sprintf("reconcile=%t", reconcile), func(t *testing.T) {
			plan := testPlan(t, []operationSpec{{id: "op-a", sideEffecting: true}})
			writer := openWriter(t, runID())
			if reconcile {
				for _, entry := range []journal.Entry{
					{RunID: runID(), Type: journal.EventRunStarted, PlanID: plan.ID()},
					{RunID: runID(), Type: journal.EventAttemptStarted, OperationID: "op-a", TargetID: "target-a", Payload: journal.Payload{Attempt: 1}},
					{RunID: runID(), Type: journal.EventSideEffectDispatched, OperationID: "op-a", TargetID: "target-a", Payload: journal.Payload{Attempt: 1}},
				} {
					if _, err := writer.Append(entry); err != nil {
						t.Fatal(err)
					}
				}
			}
			parent, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
			defer cancel()
			var deadline time.Time
			var prepared bool
			driver := &preparationDriver{}
			start := time.Now()
			driver.prepare = func(ctx context.Context, request Request, operation DriverOperation) (Preparation, error) {
				want := DriverApply
				if reconcile {
					want = DriverReconcile
				}
				if operation != want || request.Attempt != 1 || request.Operation.Timeout() != 0 {
					return Preparation{}, fmt.Errorf("unexpected preparation: %v, attempt %d, timeout %s", operation, request.Attempt, request.Operation.Timeout())
				}
				var ok bool
				deadline, ok = ctx.Deadline()
				if !ok || deadline.Before(start.Add(120*time.Second)) || deadline.After(time.Now().Add(120*time.Second)) {
					return Preparation{}, fmt.Errorf("unexpected legacy deadline: %v", deadline)
				}
				prepared = true
				return Preparation{Context: ctx}, nil
			}
			call := func(ctx context.Context, request Request) (Result, error) {
				got, ok := ctx.Deadline()
				if !prepared || !ok || !got.Equal(deadline) || request.Operation.IdempotencyKey() != "key-op-a" {
					return Result{}, errors.New("preparation budget or original execution identity lost")
				}
				return published(), nil
			}
			driver.apply, driver.reconcile = call, call
			state, err := newExecutor(t, driver, Options{MaxAttempts: 1, ReconcileOnly: reconcile}).Execute(parent, plan, runID(), writer)
			if err != nil {
				t.Fatal(err)
			}
			op, _ := state.Operation("op-a")
			if !state.Completed || op.State != domain.StatePublished || op.Attempt != 1 {
				t.Fatalf("operation=%+v", op)
			}
			wantApply, wantReconcile := 1, 0
			if reconcile {
				wantApply, wantReconcile = 0, 1
			}
			if len(driver.applyCalls) != wantApply || len(driver.reconcileCalls) != wantReconcile {
				t.Fatalf("apply=%d reconcile=%d", len(driver.applyCalls), len(driver.reconcileCalls))
			}
			for i, event := range writer.Events() {
				if _, err := journal.Reduce(plan, writer.Events()[:i+1]); err != nil {
					t.Fatalf("prefix through %s: %v", event.Type, err)
				}
			}
			if plan.Operations()[0].Timeout() != 0 {
				t.Fatal("legacy operation was rewritten")
			}
		})
	}
}
