package journal

import (
	"testing"

	"github.com/aalsanie/distroplane/internal/domain"
)

func TestCredentialResolvedEventValidation(t *testing.T) {
	entry := Entry{
		RunID: runID(), Type: EventCredentialResolved, OperationID: opID("op-a"), TargetID: targetID("target-a"),
		Payload: Payload{Attempt: 1, CredentialRef: domain.CredentialRef("release")},
	}
	if err := entry.validate(); err != nil {
		t.Fatal(err)
	}
	bad := entry
	bad.Payload.CredentialRef = ""
	if err := bad.validate(); err == nil {
		t.Fatal("missing credential reference accepted")
	}
	bad = entry
	bad.Payload.State = domain.StatePublished
	if err := bad.validate(); err == nil {
		t.Fatal("result payload accepted on credential event")
	}
	resultEntry := Entry{
		RunID: runID(), Type: EventOperationResult, OperationID: opID("op-a"), TargetID: targetID("target-a"),
		Payload: Payload{Attempt: 1, CredentialRef: domain.CredentialRef("release"), State: domain.StatePublished},
	}
	if err := resultEntry.validate(); err == nil {
		t.Fatal("credential reference accepted on result event")
	}
}

func TestReduceCredentialResolutionIsMetadataOnly(t *testing.T) {
	plan := testPlan(t)
	events := []Event{
		runStarted(1),
		attemptStarted(2, "op-a", "target-a", 1),
		event(3, EventCredentialResolved, "op-a", "target-a", Payload{Attempt: 1, CredentialRef: domain.CredentialRef("release")}),
		dispatched(4, "op-a", "target-a", 1),
		result(5, "op-a", "target-a", 1, domain.StatePublished),
	}
	state, err := Reduce(plan, events)
	if err != nil {
		t.Fatal(err)
	}
	op, _ := state.Operation(opID("op-a"))
	if op.State != domain.StatePublished || op.Attempt != 1 {
		t.Fatalf("op=%+v", op)
	}
	afterDispatch := []Event{
		runStarted(1),
		attemptStarted(2, "op-a", "target-a", 1),
		dispatched(3, "op-a", "target-a", 1),
		event(4, EventCredentialResolved, "op-a", "target-a", Payload{Attempt: 1, CredentialRef: domain.CredentialRef("release")}),
	}
	if _, err := Reduce(plan, afterDispatch); err == nil {
		t.Fatal("credential resolution after dispatch accepted")
	}
}

func TestReduceCredentialResolutionDuringReconcile(t *testing.T) {
	plan := testPlan(t)
	events := []Event{
		runStarted(1),
		attemptStarted(2, "op-c", "target-b", 1),
		dispatched(3, "op-c", "target-b", 1),
		event(4, EventOperationResult, "op-c", "target-b", Payload{Attempt: 1, State: domain.StateWaitingExternal}),
		reconcileStarted(5, "op-c", "target-b", 1),
		event(6, EventCredentialResolved, "op-c", "target-b", Payload{Attempt: 1, CredentialRef: domain.CredentialRef("release")}),
		reconcileResult(7, "op-c", "target-b", 1, domain.StatePublished),
	}
	if _, err := Reduce(plan, events); err != nil {
		t.Fatal(err)
	}
}
