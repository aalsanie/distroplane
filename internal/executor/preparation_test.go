package executor

import (
	"context"
	"errors"
	"testing"

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
		{RunID: runID(), Type: journal.EventRunStarted},
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
		if event.Type == journal.EventOperationResult && event.Payload.ErrorCode == "INTERRUPTED_BEFORE_DISPATCH" {
			seenRecovery = true
		}
	}
	if !seenRecovery {
		t.Fatal("undispatched attempt was not recovered")
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
