package executor

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/aalsanie/distroplane/internal/domain"
	"github.com/aalsanie/distroplane/internal/journal"
)

func leaseRequest(key string) LeaseRequest {
	return LeaseRequest{Key: key, OperationID: "op-a"}
}

func TestMemoryLeasesAcquireRenewRelease(t *testing.T) {
	now := time.Unix(100, 0).UTC()
	next := 0
	leases := newMemoryLeases("worker-a", 10*time.Second, func() time.Time { return now }, func() (string, error) {
		next++
		return "lease-" + string(rune('0'+next)), nil
	})

	lease, err := leases.Acquire(context.Background(), leaseRequest("key"))
	if err != nil {
		t.Fatal(err)
	}
	state := lease.State()
	if state.ID != "lease-1" || state.Owner != "worker-a" || state.OperationID != "op-a" || !state.AcquiredAt.Equal(now) || !state.ExpiresAt.Equal(now.Add(10*time.Second)) {
		t.Fatalf("state=%+v", state)
	}
	if _, ok := lease.PreviousExpired(); ok {
		t.Fatal("new lease reported an expired predecessor")
	}
	if _, err := leases.Acquire(context.Background(), leaseRequest("key")); !errors.Is(err, ErrLeaseHeld) {
		t.Fatalf("err=%v", err)
	}

	now = now.Add(6 * time.Second)
	renewed, err := lease.Renew(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if renewed.ID != state.ID || !renewed.AcquiredAt.Equal(state.AcquiredAt) || !renewed.ExpiresAt.After(state.ExpiresAt) {
		t.Fatalf("renewed=%+v original=%+v", renewed, state)
	}
	if got := lease.State(); got != renewed {
		t.Fatalf("state=%+v renewed=%+v", got, renewed)
	}

	if err := lease.Release(); err != nil {
		t.Fatal(err)
	}
	if err := lease.Release(); err != nil {
		t.Fatal(err)
	}
	replacement, err := leases.Acquire(context.Background(), leaseRequest("key"))
	if err != nil {
		t.Fatal(err)
	}
	if replacement.State().ID == state.ID {
		t.Fatal("released lease ID was reused")
	}
	if err := replacement.Release(); err != nil {
		t.Fatal(err)
	}
}

func TestMemoryLeasesExpiryAllowsReclaim(t *testing.T) {
	now := time.Unix(200, 0).UTC()
	ids := []string{"lease-a", "lease-b"}
	next := 0
	leases := newMemoryLeases("worker-a", 5*time.Second, func() time.Time { return now }, func() (string, error) {
		id := ids[next]
		next++
		return id, nil
	})

	first, err := leases.Acquire(context.Background(), leaseRequest("key"))
	if err != nil {
		t.Fatal(err)
	}
	firstState := first.State()
	now = firstState.ExpiresAt

	second, err := leases.Acquire(context.Background(), leaseRequest("key"))
	if err != nil {
		t.Fatal(err)
	}
	expired, ok := second.PreviousExpired()
	if !ok || expired != firstState {
		t.Fatalf("expired=%+v ok=%v want=%+v", expired, ok, firstState)
	}
	if second.State().ID != "lease-b" {
		t.Fatalf("second=%+v", second.State())
	}
	if _, err := first.Renew(context.Background()); !errors.Is(err, ErrLeaseExpired) {
		t.Fatalf("renew err=%v", err)
	}
	if err := first.Release(); !errors.Is(err, ErrLeaseExpired) {
		t.Fatalf("release err=%v", err)
	}
	if err := second.Release(); err != nil {
		t.Fatal(err)
	}
}

func TestMemoryLeasesRenewAfterDeadlineExpiresLease(t *testing.T) {
	now := time.Unix(300, 0).UTC()
	leases := newMemoryLeases("worker-a", time.Second, func() time.Time { return now }, func() (string, error) {
		return "lease-a", nil
	})
	lease, err := leases.Acquire(context.Background(), leaseRequest("key"))
	if err != nil {
		t.Fatal(err)
	}
	now = now.Add(time.Second)
	if state, err := lease.Renew(context.Background()); !errors.Is(err, ErrLeaseExpired) || state.ID != "lease-a" {
		t.Fatalf("state=%+v err=%v", state, err)
	}
	if _, err := leases.Acquire(context.Background(), leaseRequest("key")); err != nil {
		t.Fatal(err)
	}
}

func TestMemoryLeasesValidation(t *testing.T) {
	leases := NewMemoryLeases()
	if _, err := leases.Acquire(nil, leaseRequest("key")); err == nil {
		t.Fatal("nil context accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := leases.Acquire(ctx, leaseRequest("key")); !errors.Is(err, context.Canceled) {
		t.Fatalf("err=%v", err)
	}
	if _, err := leases.Acquire(context.Background(), leaseRequest("")); err == nil {
		t.Fatal("empty key accepted")
	}
	var nilManager *MemoryLeases
	if _, err := nilManager.Acquire(context.Background(), leaseRequest("key")); err == nil {
		t.Fatal("nil manager accepted")
	}
	if _, err := leases.Acquire(context.Background(), LeaseRequest{Key: "key"}); err == nil {
		t.Fatal("invalid operation ID accepted")
	}

	badManagers := []*MemoryLeases{
		newMemoryLeases("", time.Second, time.Now, randomLeaseID),
		newMemoryLeases("worker", 0, time.Now, randomLeaseID),
		newMemoryLeases("worker", time.Second, nil, randomLeaseID),
		newMemoryLeases("worker", time.Second, time.Now, nil),
	}
	for i, manager := range badManagers {
		if _, err := manager.Acquire(context.Background(), leaseRequest("key")); err == nil {
			t.Fatalf("bad manager %d accepted", i)
		}
	}
	failingID := newMemoryLeases("worker", time.Second, time.Now, func() (string, error) {
		return "", errors.New("id failed")
	})
	if _, err := failingID.Acquire(context.Background(), leaseRequest("key")); err == nil {
		t.Fatal("lease ID failure ignored")
	}
	invalidID := newMemoryLeases("worker", time.Second, time.Now, func() (string, error) {
		return " bad", nil
	})
	if _, err := invalidID.Acquire(context.Background(), leaseRequest("key")); err == nil {
		t.Fatal("invalid lease ID accepted")
	}

	var nilLease *memoryLease
	if state := nilLease.State(); state != (LeaseState{}) {
		t.Fatalf("state=%+v", state)
	}
	if _, ok := nilLease.PreviousExpired(); ok {
		t.Fatal("nil lease reported predecessor")
	}
	if err := nilLease.Release(); err != nil {
		t.Fatal(err)
	}
	if _, err := nilLease.Renew(context.Background()); err == nil {
		t.Fatal("nil lease renewed")
	}
	if _, err := (&memoryLease{}).Renew(nil); err == nil {
		t.Fatal("nil renewal context accepted")
	}
	if err := (&memoryLease{}).Release(); err == nil {
		t.Fatal("invalid empty lease accepted")
	}
}

func TestExecuteJournalsLeaseAndProviderLifecycle(t *testing.T) {
	plan := testPlan(t, []operationSpec{{id: "op-a", sideEffecting: true}})
	writer := openWriter(t, runID())
	state, err := newExecutor(t, &scriptedDriver{}, Options{}).Execute(context.Background(), plan, runID(), writer)
	if err != nil {
		t.Fatal(err)
	}
	if !state.Completed {
		t.Fatalf("state=%+v", state)
	}

	want := []journal.EventType{
		journal.EventRunStarted,
		journal.EventOperationReady,
		journal.EventLeaseAcquired,
		journal.EventAttemptStarted,
		journal.EventProviderProcessStarted,
		journal.EventSideEffectDispatched,
		journal.EventProviderResponseReceived,
		journal.EventOperationPublished,
		journal.EventLeaseReleased,
		journal.EventRunCompleted,
	}
	events := writer.Events()
	if len(events) != len(want) {
		t.Fatalf("events=%+v", events)
	}
	for i, eventType := range want {
		if events[i].Type != eventType {
			t.Fatalf("event %d=%s want=%s events=%+v", i, events[i].Type, eventType, events)
		}
	}
	acquired := events[2].Payload.Lease
	released := events[8].Payload.Lease
	if acquired == nil || released == nil || acquired.ID == "" || acquired.Owner == "" ||
		acquired.ID != released.ID || acquired.Owner != released.Owner ||
		!acquired.AcquiredAt.Equal(released.AcquiredAt) || !acquired.ExpiresAt.Equal(released.ExpiresAt) {
		t.Fatalf("acquired=%+v released=%+v", acquired, released)
	}
}

func TestExecuteRenewsLongRunningLease(t *testing.T) {
	plan := testPlan(t, []operationSpec{{id: "op-a"}})
	leases := newMemoryLeases("worker-a", 20*time.Millisecond, time.Now, func() (string, error) {
		return "lease-a", nil
	})
	driver := &scriptedDriver{apply: func(context.Context, Request) (Result, error) {
		time.Sleep(45 * time.Millisecond)
		return published(), nil
	}}
	writer := openWriter(t, runID())
	state, err := newExecutor(t, driver, Options{Leases: leases}).Execute(context.Background(), plan, runID(), writer)
	if err != nil {
		t.Fatal(err)
	}
	if !state.Completed {
		t.Fatalf("state=%+v", state)
	}
	renewals := 0
	for _, event := range writer.Events() {
		if event.Type == journal.EventLeaseRenewed {
			renewals++
		}
	}
	if renewals == 0 {
		t.Fatalf("events=%+v", writer.Events())
	}
}

func TestExecuteExpiresStaleJournalLeaseBeforeReclaim(t *testing.T) {
	plan := testPlan(t, []operationSpec{{id: "op-a"}})
	writer := openWriter(t, runID())
	now := time.Now().UTC()
	lease := &journal.LeasePayload{
		ID: "stale-lease", Owner: "dead-worker",
		AcquiredAt: now.Add(-2 * time.Second), ExpiresAt: now.Add(-time.Second),
	}
	for _, entry := range []journal.Entry{
		{RunID: runID(), Type: journal.EventRunStarted},
		{RunID: runID(), Type: journal.EventOperationReady, OperationID: "op-a", TargetID: "target-a"},
		{RunID: runID(), Type: journal.EventLeaseAcquired, OperationID: "op-a", TargetID: "target-a", Payload: journal.Payload{Lease: lease}},
	} {
		if _, err := writer.Append(entry); err != nil {
			t.Fatal(err)
		}
	}

	state, err := newExecutor(t, &scriptedDriver{}, Options{}).Execute(context.Background(), plan, runID(), writer)
	if err != nil {
		t.Fatal(err)
	}
	op, _ := state.Operation("op-a")
	if !state.Completed || op.State != domain.StatePublished || op.LeaseActive {
		t.Fatalf("state=%+v op=%+v", state, op)
	}
	expired := 0
	acquired := 0
	for _, event := range writer.Events() {
		switch event.Type {
		case journal.EventLeaseExpired:
			expired++
		case journal.EventLeaseAcquired:
			acquired++
		}
	}
	if expired != 1 || acquired != 2 {
		t.Fatalf("expired=%d acquired=%d events=%+v", expired, acquired, writer.Events())
	}
}

func TestExpiredLeaseAfterDispatchReconcilesWithoutReapply(t *testing.T) {
	plan := testPlan(t, []operationSpec{{id: "op-a", sideEffecting: true}})
	writer := openWriter(t, runID())
	now := time.Now().UTC()
	lease := &journal.LeasePayload{
		ID: "stale-lease", Owner: "dead-worker",
		AcquiredAt: now.Add(-2 * time.Second), ExpiresAt: now.Add(-time.Second),
	}
	for _, entry := range []journal.Entry{
		{RunID: runID(), Type: journal.EventRunStarted},
		{RunID: runID(), Type: journal.EventOperationReady, OperationID: "op-a", TargetID: "target-a"},
		{RunID: runID(), Type: journal.EventLeaseAcquired, OperationID: "op-a", TargetID: "target-a", Payload: journal.Payload{Lease: lease}},
		{RunID: runID(), Type: journal.EventAttemptStarted, OperationID: "op-a", TargetID: "target-a", Payload: journal.Payload{Attempt: 1}},
		{RunID: runID(), Type: journal.EventProviderProcessStarted, OperationID: "op-a", TargetID: "target-a", Payload: journal.Payload{Attempt: 1}},
		{RunID: runID(), Type: journal.EventSideEffectDispatched, OperationID: "op-a", TargetID: "target-a", Payload: journal.Payload{Attempt: 1}},
	} {
		if _, err := writer.Append(entry); err != nil {
			t.Fatal(err)
		}
	}

	driver := &scriptedDriver{reconcile: func(context.Context, Request) (Result, error) {
		return published(), nil
	}}
	state, err := newExecutor(t, driver, Options{}).Execute(context.Background(), plan, runID(), writer)
	if err != nil {
		t.Fatal(err)
	}
	driver.mu.Lock()
	applyCalls, reconcileCalls := len(driver.applyCalls), len(driver.reconcileCalls)
	driver.mu.Unlock()
	op, _ := state.Operation("op-a")
	if !state.Completed || op.State != domain.StatePublished || applyCalls != 0 || reconcileCalls != 1 {
		t.Fatalf("state=%+v op=%+v apply=%d reconcile=%d", state, op, applyCalls, reconcileCalls)
	}
}
