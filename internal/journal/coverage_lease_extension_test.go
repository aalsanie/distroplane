package journal

import (
	"testing"
	"time"
)

func TestJournalLeaseRenewalMonotonicityAndIdentity(t *testing.T) {
	plan := testPlan(t)
	acquired := event(2, EventLeaseAcquired, "op-a", "target-a", Payload{
		Lease: leasePayload("lease-a", "worker-a", 2, 20),
	})
	renewed := event(3, EventLeaseRenewed, "op-a", "target-a", Payload{
		Lease: leasePayload("lease-a", "worker-a", 2, 30),
	})
	state, err := Reduce(plan, []Event{runStarted(1), acquired, renewed})
	if err != nil {
		t.Fatalf("valid monotonic lease renewal rejected: %v", err)
	}
	operation, ok := state.Operation("op-a")
	if !ok || !operation.LeaseActive || !operation.LeaseExpiresAt.Equal(time.Unix(30, 0).UTC()) {
		t.Fatalf("renewal not reflected in derived state: %+v", operation)
	}
	for name, modify := range map[string]func(*Event){
		"same expiry":     func(e *Event) { e.Payload.Lease.ExpiresAt = time.Unix(20, 0).UTC() },
		"wrong owner":     func(e *Event) { e.Payload.Lease.Owner = "other-worker" },
		"wrong lease":     func(e *Event) { e.Payload.Lease.ID = "unrelated-lease" },
		"wrong operation": func(e *Event) { e.OperationID = "unknown" },
	} {
		t.Run(name, func(t *testing.T) {
			changed := renewed
			copied := *renewed.Payload.Lease
			changed.Payload.Lease = &copied
			modify(&changed)
			if _, err := Reduce(plan, []Event{runStarted(1), acquired, changed}); err == nil {
				t.Fatal("unbound or non-increasing lease renewal was accepted")
			}
		})
	}
}
