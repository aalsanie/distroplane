package executor

import (
	"context"
	"errors"
	"testing"
)

func TestMemoryLeasesAcquireRelease(t *testing.T) {
	leases := NewMemoryLeases()
	lease, err := leases.Acquire(context.Background(), "key")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := leases.Acquire(context.Background(), "key"); !errors.Is(err, ErrLeaseHeld) {
		t.Fatalf("err=%v", err)
	}
	if err := lease.Release(); err != nil {
		t.Fatal(err)
	}
	if err := lease.Release(); err != nil {
		t.Fatal(err)
	}
	lease, err = leases.Acquire(context.Background(), "key")
	if err != nil {
		t.Fatal(err)
	}
	if err := lease.Release(); err != nil {
		t.Fatal(err)
	}
}

func TestMemoryLeasesValidation(t *testing.T) {
	leases := NewMemoryLeases()
	if _, err := leases.Acquire(nil, "key"); err == nil {
		t.Fatal("nil context accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := leases.Acquire(ctx, "key"); !errors.Is(err, context.Canceled) {
		t.Fatalf("err=%v", err)
	}
	if _, err := leases.Acquire(context.Background(), ""); err == nil {
		t.Fatal("empty key accepted")
	}
	var nilManager *MemoryLeases
	if _, err := nilManager.Acquire(context.Background(), "key"); err == nil {
		t.Fatal("nil manager accepted")
	}
	var nilLease *memoryLease
	if err := nilLease.Release(); err != nil {
		t.Fatal(err)
	}
	invalid := &memoryLease{manager: leases, key: "missing"}
	if err := invalid.Release(); err == nil {
		t.Fatal("invalid lease release accepted")
	}
}
