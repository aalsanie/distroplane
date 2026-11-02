package journal

import (
	"strings"
	"testing"

	"github.com/aalsanie/distroplane/internal/domain"
)

func TestJournalEventsRejectUnknownOrMismatchedOperationIdentity(t *testing.T) {
	lease := Payload{Lease: leasePayload("lease-a", "worker-a", 2, 10)}
	attempt := Payload{Attempt: 1}
	result := Payload{Attempt: 1, State: domain.StatePublished}
	cases := []struct {
		name    string
		kind    EventType
		payload Payload
	}{
		{"ready", EventOperationReady, Payload{}},
		{"lease acquired", EventLeaseAcquired, lease},
		{"lease renewed", EventLeaseRenewed, lease},
		{"lease expired", EventLeaseExpired, lease},
		{"lease released", EventLeaseReleased, lease},
		{"operation cancelled", EventOperationCancelled, Payload{}},
		{"attempt started", EventAttemptStarted, attempt},
		{"credential resolved", EventCredentialResolved, Payload{Attempt: 1, CredentialRef: domain.CredentialRef("credential")}},
		{"process started", EventProviderProcessStarted, attempt},
		{"dispatched", EventSideEffectDispatched, attempt},
		{"response received", EventProviderResponseReceived, attempt},
		{"ambiguous outcome", EventOutcomeAmbiguous, attempt},
		{"waiting", EventOperationWaitingExternal, attempt},
		{"published", EventOperationPublished, attempt},
		{"rejected", EventOperationRejected, attempt},
		{"failed", EventOperationFailed, attempt},
		{"operation result", EventOperationResult, result},
		{"reconcile started", EventReconcileStarted, attempt},
		{"reconcile result", EventReconcileResult, result},
		{"retry authorized", EventRetryAuthorized, attempt},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for _, identity := range []struct {
				operation string
				target    string
				reason    string
			}{
				{"unknown", "target-a", "unknown operation"},
				{"op-a", "target-b", "does not match"},
			} {
				invalid := event(2, tc.kind, identity.operation, identity.target, tc.payload)
				if err := invalid.validate(); err != nil {
					t.Fatalf("malformed test event %s: %v", tc.kind, err)
				}
				_, err := Reduce(testPlan(t), []Event{runStarted(1), invalid})
				if err == nil || !strings.Contains(err.Error(), identity.reason) {
					t.Fatalf("event %s used unbound operation %q and target %q: %v",
						tc.kind, identity.operation, identity.target, err)
				}
			}
		})
	}
}
