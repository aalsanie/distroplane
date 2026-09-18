package journal

import (
	"path/filepath"
	"testing"

	"github.com/aalsanie/distroplane/internal/domain"
)

func TestReduceRunLifecycleAndOperationCancellation(t *testing.T) {
	plan := testPlan(t)
	state, err := Reduce(plan, []Event{
		runStarted(1),
		event(2, EventOperationCancelled, "op-b", "target-a", Payload{ErrorCode: "DEPENDENCY_TERMINAL"}),
		event(3, EventOperationCancelled, "op-a", "target-a", Payload{ErrorCode: "DEPENDENCY_TERMINAL"}),
		event(4, EventOperationCancelled, "op-c", "target-b", Payload{ErrorCode: "DEPENDENCY_TERMINAL"}),
		event(5, EventRunCompleted, "", "", Payload{}),
	})
	if err != nil {
		t.Fatal(err)
	}
	if !state.Completed || state.Cancelled {
		t.Fatalf("state=%+v", state)
	}
	for _, operation := range state.Operations() {
		if operation.State != domain.StateCancelled || operation.ErrorCode != "DEPENDENCY_TERMINAL" {
			t.Fatalf("operation=%+v", operation)
		}
	}
	if _, err := Reduce(plan, append([]Event{
		runStarted(1),
		event(2, EventOperationCancelled, "op-a", "target-a", Payload{}),
		event(3, EventOperationCancelled, "op-b", "target-a", Payload{}),
		event(4, EventOperationCancelled, "op-c", "target-b", Payload{}),
		event(5, EventRunCompleted, "", "", Payload{}),
	}, event(6, EventRunCancelled, "", "", Payload{}))); err == nil {
		t.Fatal("event after run completion accepted")
	}
}

func TestReduceRunCancellationPreservesAmbiguousReconciliation(t *testing.T) {
	plan := testPlan(t)
	state, err := Reduce(plan, []Event{
		runStarted(1),
		attemptStarted(2, "op-a", "target-a", 1),
		dispatched(3, "op-a", "target-a", 1),
		event(4, EventRunCancelled, "", "", Payload{}),
		reconcileStarted(5, "op-a", "target-a", 1),
		reconcileResult(6, "op-a", "target-a", 1, domain.StatePublished),
	})
	if err != nil {
		t.Fatal(err)
	}
	if !state.Cancelled || state.Completed {
		t.Fatalf("state=%+v", state)
	}
	opA, _ := state.Operation(opID("op-a"))
	if opA.State != domain.StatePublished || opA.ReconcileRequired || opA.Ambiguous {
		t.Fatalf("op-a=%+v", opA)
	}
	opB, _ := state.Operation(opID("op-b"))
	if opB.State != domain.StateCancelled || opB.ErrorCode != "RUN_CANCELLED" {
		t.Fatalf("op-b=%+v", opB)
	}
	if _, err := Reduce(plan, []Event{
		runStarted(1),
		event(2, EventRunCancelled, "", "", Payload{}),
		attemptStarted(3, "op-a", "target-a", 1),
	}); err == nil {
		t.Fatal("new attempt after run cancellation accepted")
	}
}

func TestReduceRetryableFailureState(t *testing.T) {
	plan := testPlan(t)
	state, err := Reduce(plan, []Event{
		runStarted(1),
		attemptStarted(2, "op-c", "target-b", 1),
		dispatched(3, "op-c", "target-b", 1),
		event(4, EventOperationResult, "op-c", "target-b", Payload{Attempt: 1, State: domain.StateFailed, ErrorCode: "TRANSIENT", Retryable: true}),
	})
	if err != nil {
		t.Fatal(err)
	}
	op, _ := state.Operation(opID("op-c"))
	if op.State != domain.StateFailed || !op.Retryable || op.ErrorCode != "TRANSIENT" {
		t.Fatalf("op=%+v", op)
	}
	state, err = Reduce(plan, []Event{
		runStarted(1),
		attemptStarted(2, "op-c", "target-b", 1),
		dispatched(3, "op-c", "target-b", 1),
		event(4, EventOperationResult, "op-c", "target-b", Payload{Attempt: 1, State: domain.StateFailed, Retryable: true}),
		attemptStarted(5, "op-c", "target-b", 2),
	})
	if err != nil {
		t.Fatal(err)
	}
	op, _ = state.Operation(opID("op-c"))
	if op.State != domain.StateRunning || op.Attempt != 2 || op.Retryable {
		t.Fatalf("op=%+v", op)
	}
}

func TestWriterEventsSnapshotAndReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "run.journal")
	writer, err := OpenWriter(path, runID())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Append(Entry{RunID: runID(), Type: EventRunStarted}); err != nil {
		t.Fatal(err)
	}
	events := writer.Events()
	if len(events) != 1 {
		t.Fatalf("events=%d", len(events))
	}
	events[0].Type = EventRunCancelled
	if writer.Events()[0].Type != EventRunStarted {
		t.Fatal("events snapshot mutated writer")
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	writer, err = OpenWriter(path, runID())
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	if got := writer.Events(); len(got) != 1 || got[0].Type != EventRunStarted {
		t.Fatalf("events=%+v", got)
	}
}

func TestLifecycleEntryValidation(t *testing.T) {
	for _, typ := range []EventType{EventRunCompleted, EventRunCancelled} {
		if err := (Entry{RunID: runID(), Type: typ}).validate(); err != nil {
			t.Fatalf("type=%s err=%v", typ, err)
		}
		if err := (Entry{RunID: runID(), Type: typ, OperationID: opID("op-a")}).validate(); err == nil {
			t.Fatalf("type=%s accepted operation data", typ)
		}
	}
	valid := Entry{RunID: runID(), Type: EventOperationCancelled, OperationID: opID("op-a"), TargetID: targetID("target-a"), Payload: Payload{ErrorCode: "DEPENDENCY_TERMINAL"}}
	if err := valid.validate(); err != nil {
		t.Fatal(err)
	}
	valid.Payload.Retryable = true
	if err := valid.validate(); err == nil {
		t.Fatal("retryable operation cancellation accepted")
	}
	if err := (Payload{Attempt: 1, State: domain.StatePublished, Retryable: true}).validate(EventOperationResult); err == nil {
		t.Fatal("retryable published result accepted")
	}
}
