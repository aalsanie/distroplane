package executor

import (
	"context"
	"testing"

	"github.com/aalsanie/distroplane/internal/domain"
	"github.com/aalsanie/distroplane/internal/journal"
)

func TestReconcileOnlyDoesNotApplyReadyOperation(t *testing.T) {
	plan := testPlan(t, []operationSpec{{id: "op-a", sideEffecting: true}})
	driver := &scriptedDriver{}
	writer := openWriter(t, runID())
	engine := newExecutor(t, driver, Options{ReconcileOnly: true})
	state, err := engine.Execute(context.Background(), plan, runID(), writer)
	if err != nil {
		t.Fatal(err)
	}
	op, _ := state.Operation("op-a")
	if op.State != domain.StateReady {
		t.Fatalf("state=%s", op.State)
	}
	if len(driver.applyCalls) != 0 || len(driver.reconcileCalls) != 0 {
		t.Fatalf("apply=%d reconcile=%d", len(driver.applyCalls), len(driver.reconcileCalls))
	}
}

func TestReconcileOnlyProcessesPendingWithoutPublishingDownstream(t *testing.T) {
	plan := testPlan(t, []operationSpec{
		{id: "op-a", sideEffecting: true},
		{id: "op-b", dependencies: []string{"op-a"}, sideEffecting: true},
	})
	writer := openWriter(t, runID())
	for _, entry := range []journal.Entry{
		{RunID: runID(), Type: journal.EventRunStarted},
		{RunID: runID(), Type: journal.EventAttemptStarted, OperationID: "op-a", TargetID: "target-a", Payload: journal.Payload{Attempt: 1}},
		{RunID: runID(), Type: journal.EventSideEffectDispatched, OperationID: "op-a", TargetID: "target-a", Payload: journal.Payload{Attempt: 1}},
		{RunID: runID(), Type: journal.EventOperationResult, OperationID: "op-a", TargetID: "target-a", Payload: journal.Payload{Attempt: 1, State: domain.StateWaitingExternal, ProviderState: "pending", Evidence: []byte(`{"id":"1"}`)}},
	} {
		if _, err := writer.Append(entry); err != nil {
			t.Fatal(err)
		}
	}
	driver := &scriptedDriver{}
	engine := newExecutor(t, driver, Options{ReconcileOnly: true})
	state, err := engine.Execute(context.Background(), plan, runID(), writer)
	if err != nil {
		t.Fatal(err)
	}
	a, _ := state.Operation("op-a")
	b, _ := state.Operation("op-b")
	if a.State != domain.StatePublished || b.State != domain.StateReady {
		t.Fatalf("a=%+v b=%+v", a, b)
	}
	if len(driver.reconcileCalls) != 1 || len(driver.applyCalls) != 0 {
		t.Fatalf("apply=%d reconcile=%d", len(driver.applyCalls), len(driver.reconcileCalls))
	}
}
