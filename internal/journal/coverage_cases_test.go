package journal

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/aalsanie/distroplane/internal/domain"
)

func TestJournalEventValidationDecisionMatrix(t *testing.T) {
	newEntry := func(kind EventType, payload Payload) Entry {
		entry := Entry{
			RunID: domain.RunID("run-coverage"), Type: kind,
			OperationID: domain.OperationID("publish"), TargetID: domain.TargetID("target"),
			Payload: payload,
		}
		if kind == EventRunStarted || kind == EventRunCompleted || kind == EventRunCancelled {
			entry.OperationID, entry.TargetID = "", ""
		}
		if kind == EventRunStarted {
			entry.PlanID = domain.PlanID("plan-coverage")
		}
		return entry
	}
	cases := []struct {
		name string
		kind EventType
		payload Payload
		valid bool
	}{
		{"ready", EventOperationReady, Payload{}, true},
		{"ready extra", EventOperationReady, Payload{Attempt: 1}, false},
		{"retry", EventRetryAuthorized, Payload{Attempt: 2}, true},
		{"retry empty", EventRetryAuthorized, Payload{}, false},
		{"retry metadata", EventRetryAuthorized, Payload{Attempt: 1, ErrorCode: "bad"}, false},
		{"attempt", EventAttemptStarted, Payload{Attempt: 1}, true},
		{"attempt missing", EventAttemptStarted, Payload{}, false},
		{"attempt invalid category", EventAttemptStarted, Payload{Attempt: 1, ResultCategory: "PUBLISHED"}, false},
		{"attempt with reason", EventAttemptStarted, Payload{Attempt: 1, AttemptReason: AttemptReasonInitial}, true},
		{"provider started", EventProviderProcessStarted, Payload{Attempt: 1}, true},
		{"provider started metadata", EventProviderProcessStarted, Payload{Attempt: 1, AttemptReason: AttemptReasonRetry}, false},
		{"dispatched", EventSideEffectDispatched, Payload{Attempt: 1}, true},
		{"response", EventProviderResponseReceived, Payload{Attempt: 1}, true},
		{"ambiguous", EventOutcomeAmbiguous, Payload{Attempt: 1, ErrorCode: "TIMEOUT"}, true},
		{"ambiguous bad reason", EventOutcomeAmbiguous, Payload{Attempt: 1, AttemptReason: AttemptReasonInitial}, false},
		{"reconcile started", EventReconcileStarted, Payload{Attempt: 1, AttemptReason: AttemptReasonReconcile}, true},
		{"waiting", EventOperationWaitingExternal, Payload{Attempt: 1, Retryable: true}, true},
		{"published", EventOperationPublished, Payload{Attempt: 1}, true},
		{"published retryable", EventOperationPublished, Payload{Attempt: 1, Retryable: true}, false},
		{"rejected", EventOperationRejected, Payload{Attempt: 1}, true},
		{"rejected credential", EventOperationRejected, Payload{Attempt: 1, CredentialRef: domain.CredentialRef("secret")}, false},
		{"failed", EventOperationFailed, Payload{Attempt: 1, Retryable: true}, true},
		{"failed missing attempt", EventOperationFailed, Payload{}, false},
		{"cancelled", EventOperationCancelled, Payload{ErrorCode: "CANCELLED"}, true},
		{"cancelled invalid", EventOperationCancelled, Payload{Attempt: 1}, false},
		{"result", EventOperationResult, Payload{Attempt: 1, State: domain.StatePublished}, true},
		{"result without attempt", EventOperationResult, Payload{State: domain.StatePublished}, false},
		{"result blocked", EventOperationResult, Payload{Attempt: 1, State: domain.StateBlocked, ResultCategory: ResultCategoryBlocked, ErrorCode: "BLOCKED"}, true},
		{"result invalid blocked", EventOperationResult, Payload{Attempt: 1, State: domain.StateBlocked}, false},
		{"reconcile result", EventReconcileResult, Payload{Attempt: 1, State: domain.StatePublished}, true},
		{"reconcile blocked", EventReconcileResult, Payload{Attempt: 1, State: domain.StateBlocked}, false},
		{"result secret field", EventOperationResult, Payload{Attempt: 1, State: domain.StatePublished, Evidence: json.RawMessage(`{"authorization":"secret"}`)}, false},
		{"result unknown state", EventOperationResult, Payload{Attempt: 1, State: domain.StateRunning}, false},
		{"result bad attempt reason", EventOperationResult, Payload{Attempt: 1, State: domain.StatePublished, AttemptReason: AttemptReasonInitial}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := newEntry(tc.kind, tc.payload).validate()
			if (err == nil) != tc.valid {
				t.Fatalf("event=%s payload=%+v valid=%v err=%v", tc.kind, tc.payload, tc.valid, err)
			}
		})
	}
	now := time.Now().UTC()
	base := Event{
		SchemaVersion: SchemaVersion, Sequence: 1, RunID: domain.RunID("run-coverage"),
		Type: EventRunStarted, PlanID: domain.PlanID("plan-coverage"), ObservedAt: now,
	}
	if err := base.validate(); err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*Event){
		"version": func(e *Event) { e.SchemaVersion = 0 },
		"sequence": func(e *Event) { e.Sequence = 0 },
		"clock": func(e *Event) { e.ObservedAt = time.Time{} },
		"run": func(e *Event) { e.RunID = "" },
		"plan": func(e *Event) { e.PlanID = "" },
		"unexpected payload": func(e *Event) { e.Payload.Attempt = 1 },
		"unknown type": func(e *Event) { e.Type = "FUTURE_EVENT" },
	} {
		t.Run(name, func(t *testing.T) {
			event := base
			mutate(&event)
			if err := event.validate(); err == nil {
				t.Fatal("invalid journal event accepted")
			}
		})
	}
}
