package executor

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestLeaseRenewAndReleaseRejectChangedExecutionAuthority(t *testing.T) {
	now := time.Unix(500, 0).UTC()
	m := newMemoryLeases("worker", 2*time.Second, func() time.Time { return now },
		func() (string, error) { return "lease-token", nil })
	lease, err := m.Acquire(context.Background(), LeaseRequest{Key: "publish", OperationID: "publish"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := lease.Renew(nil); err == nil {
		t.Fatal("nil renewal context accepted")
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := lease.Renew(cancelled); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled renewal: %v", err)
	}
	m.owner = ""
	if _, err := lease.Renew(context.Background()); err == nil {
		t.Fatal("lease renewed despite an invalid authority identity")
	}
	if err := lease.Release(); err == nil {
		t.Fatal("lease released despite invalid manager state")
	}
	m.owner = "worker"
	if err := lease.Release(); err != nil {
		t.Fatal(err)
	}
	m.held = nil
	second, err := m.Acquire(context.Background(), LeaseRequest{Key: "publish", OperationID: "publish"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := second.Renew(context.Background()); err != nil {
		t.Fatal(err)
	}
	m.held = nil
	if _, err := second.Renew(context.Background()); !errors.Is(err, ErrLeaseExpired) {
		t.Fatalf("missing lease state accepted: %v", err)
	}
	if err := second.Release(); !errors.Is(err, ErrLeaseExpired) {
		t.Fatalf("release of missing lease was not rejected: %v", err)
	}
	third, err := m.Acquire(context.Background(), LeaseRequest{Key: "publish", OperationID: "publish"})
	if err != nil {
		t.Fatal(err)
	}
	now = now.Add(3 * time.Second)
	if err := third.Release(); !errors.Is(err, ErrLeaseExpired) {
		t.Fatalf("expired lease released without detecting expiry: %v", err)
	}
}
