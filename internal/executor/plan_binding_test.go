package executor

import (
	"context"
	"errors"
	"testing"

	"github.com/aalsanie/distroplane/internal/domain"
	"github.com/aalsanie/distroplane/internal/journal"
)

func TestExecuteRejectsCompletedJournalFromOtherPlanBeforeDispatch(t *testing.T) {
	first := testPlan(t, []operationSpec{{id: "op-a", sideEffecting: true}})
	otherID, _ := domain.NewPlanID("plan-2")
	second, err := domain.NewPlan(otherID, first.SchemaVersion(), first.ProtocolVersion(), first.Release(), first.Targets(), first.Operations())
	if err != nil {
		t.Fatal(err)
	}
	driver := &scriptedDriver{}
	engine := newExecutor(t, driver, Options{})
	writer := openWriter(t, runID())
	state, err := engine.Execute(context.Background(), first, runID(), writer)
	if err != nil || !state.Completed {
		t.Fatalf("first execution: state=%+v err=%v", state, err)
	}
	if _, err := engine.Execute(context.Background(), second, runID(), writer); !errors.Is(err, journal.ErrPlanMismatch) {
		t.Fatalf("different plan reused completed journal: %v", err)
	}
	driver.mu.Lock()
	defer driver.mu.Unlock()
	if len(driver.applyCalls) != 1 || len(driver.reconcileCalls) != 0 {
		t.Fatalf("unexpected provider dispatch: apply=%d reconcile=%d", len(driver.applyCalls), len(driver.reconcileCalls))
	}
}
