package journal

import (
	"testing"

	"github.com/aalsanie/distroplane/internal/domain"
)

func TestBlockedStateRequiresPreDispatchPreparationFailure(t *testing.T) {
	block := Payload{Attempt: 1, State: domain.StateBlocked, ErrorCode: "CREDENTIAL_UNAVAILABLE", ResultCategory: ResultCategoryBlocked}
	valid := []Event{
		runStarted(1),
		attemptStarted(2, "op-a", "target-a", 1),
		event(3, EventOperationResult, "op-a", "target-a", block),
	}
	state, err := Reduce(testPlan(t), valid)
	if err != nil {
		t.Fatal(err)
	}
	op, _ := state.Operation("op-a")
	if op.State != domain.StateBlocked || op.BlockedAttempts != 1 || op.ReconcileRequired || op.Ambiguous {
		t.Fatalf("operation=%+v", op)
	}
	for name, events := range map[string][]Event{
		"after process start": {
			runStarted(1), attemptStarted(2, "op-a", "target-a", 1),
			event(3, EventProviderProcessStarted, "op-a", "target-a", Payload{Attempt: 1}),
			event(4, EventOperationResult, "op-a", "target-a", block),
		},
		"after dispatch": {
			runStarted(1), attemptStarted(2, "op-a", "target-a", 1),
			dispatched(3, "op-a", "target-a", 1),
			event(4, EventOperationResult, "op-a", "target-a", block),
		},
		"run completed while blocked": {
			runStarted(1), attemptStarted(2, "op-a", "target-a", 1),
			event(3, EventOperationResult, "op-a", "target-a", block),
			event(4, EventRunCompleted, "", "", Payload{}),
		},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := Reduce(testPlan(t), events); err == nil {
				t.Fatal("unsafe blocked transition accepted")
			}
		})
	}
	for name, payload := range map[string]Payload{
		"missing error":                 {Attempt: 1, State: domain.StateBlocked, ResultCategory: ResultCategoryBlocked},
		"missing category":              {Attempt: 1, State: domain.StateBlocked, ErrorCode: "MISSING"},
		"retryable":                     {Attempt: 1, State: domain.StateBlocked, ResultCategory: ResultCategoryBlocked, ErrorCode: "MISSING", Retryable: true},
		"reconcile result":              {Attempt: 1, State: domain.StateBlocked, ResultCategory: ResultCategoryBlocked, ErrorCode: "MISSING"},
		"success with blocked category": {Attempt: 1, State: domain.StatePublished, ResultCategory: ResultCategoryBlocked},
	} {
		eventType := EventOperationResult
		if name == "reconcile result" {
			eventType = EventReconcileResult
		}
		if err := (Entry{RunID: runID(), Type: eventType, TargetID: "target-a", OperationID: "op-a", Payload: payload}).validate(); err == nil {
			t.Fatalf("%s accepted", name)
		}
	}
}

func TestBlockedStateAllowsSafeRetryWithoutReconciliation(t *testing.T) {
	block := Payload{Attempt: 1, State: domain.StateBlocked, ErrorCode: "CREDENTIAL_UNAVAILABLE", ResultCategory: ResultCategoryBlocked}
	events := []Event{
		runStarted(1),
		attemptStarted(2, "op-a", "target-a", 1),
		event(3, EventOperationResult, "op-a", "target-a", block),
		attemptStarted(4, "op-a", "target-a", 2),
		dispatched(5, "op-a", "target-a", 2),
		event(6, EventOperationPublished, "op-a", "target-a", Payload{Attempt: 2}),
	}
	state, err := Reduce(testPlan(t), events)
	if err != nil {
		t.Fatal(err)
	}
	op, _ := state.Operation("op-a")
	if op.State != domain.StatePublished || op.BlockedAttempts != 1 || op.ReconcileRequired {
		t.Fatalf("operation=%+v", op)
	}
}
