package journal

import (
	"encoding/json"
	"testing"

	"github.com/aalsanie/distroplane/internal/domain"
)

func TestJournalRejectsCredentialAndOutcomeMetadataConfusion(t *testing.T) {
	cases := []struct {
		name string
		kind EventType
		payload Payload
		valid bool
	}{
		{"credential without attempt", EventCredentialResolved, Payload{CredentialRef: domain.CredentialRef("api-key")}, false},
		{"credential without ref", EventCredentialResolved, Payload{Attempt: 1}, false},
		{"credential valid", EventCredentialResolved, Payload{Attempt: 1, CredentialRef: domain.CredentialRef("api-key")}, true},
		{"credential with evidence", EventCredentialResolved, Payload{Attempt: 1, CredentialRef: domain.CredentialRef("api-key"), Evidence: json.RawMessage(`{}`)}, false},
		{"unknown attempt reason", EventAttemptStarted, Payload{Attempt: 1, AttemptReason: "bad-reason"}, false},
		{"attempt with error code", EventAttemptStarted, Payload{Attempt: 1, ErrorCode: "ERROR"}, false},
		{"ambiguous with reason", EventOutcomeAmbiguous, Payload{Attempt: 1, AttemptReason: AttemptReasonRetry}, false},
		{"result bad category", EventOperationResult, Payload{Attempt: 1, State: domain.StatePublished, ResultCategory: "FUTURE"}, false},
		{"result forbidden retry", EventOperationResult, Payload{Attempt: 1, State: domain.StatePublished, Retryable: true}, false},
		{"result forbidden lease", EventOperationResult, Payload{Attempt: 1, State: domain.StatePublished, Lease: &LeasePayload{}}, false},
		{"result multiline state", EventOperationResult, Payload{Attempt: 1, State: domain.StatePublished, ProviderState: "bad\nvalue"}, false},
		{"result corrupt evidence", EventOperationResult, Payload{Attempt: 1, State: domain.StatePublished, Evidence: json.RawMessage(`{`)}, false},
		{"result trailing evidence", EventOperationResult, Payload{Attempt: 1, State: domain.StatePublished, Evidence: json.RawMessage(`{} {}`)}, false},
		{"reconcile blocked category", EventReconcileResult, Payload{Attempt: 1, State: domain.StateBlocked, ResultCategory: ResultCategoryBlocked, ErrorCode: "BLOCKED"}, false},
		{"blocked category wrong state", EventOperationResult, Payload{Attempt: 1, State: domain.StatePublished, ResultCategory: ResultCategoryBlocked}, false},
		{"waiting with blocked category", EventOperationWaitingExternal, Payload{Attempt: 1, ResultCategory: ResultCategoryBlocked}, false},
		{"failed bad error code", EventOperationFailed, Payload{Attempt: 1, ErrorCode: "bad\nvalue"}, false},
		{"cancelled with retry", EventOperationCancelled, Payload{Retryable: true}, false},
		{"reconcile valid waiting", EventReconcileResult, Payload{Attempt: 1, State: domain.StateWaitingExternal}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.payload.validate(tc.kind)
			if (err == nil) != tc.valid {
				t.Fatalf("unexpected result for %s %+v: %v", tc.kind, tc.payload, err)
			}
		})
	}
	if _, err := Reduce(domain.Plan{}, nil); err == nil {
		t.Fatal("reduction accepted unbound plan")
	}
}
