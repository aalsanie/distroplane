package executor

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/aalsanie/distroplane/internal/domain"
	"github.com/aalsanie/distroplane/internal/journal"
)

func TestCredentialBlockedPreDispatchResumesSameJournal(t *testing.T) {
	plan := testPlan(t, []operationSpec{{id: "op-a", sideEffecting: true}})
	path := filepath.Join(t.TempDir(), "release.journal")
	credentialsReady := false
	driver := &preparationDriver{}
	driver.prepare = func(ctx context.Context, request Request, operation DriverOperation) (Preparation, error) {
		if !credentialsReady {
			return Preparation{}, &DriverError{Code: "CREDENTIAL_UNAVAILABLE", Blocked: true}
		}
		return Preparation{Context: ctx}, nil
	}
	// More than MaxAttempts blocked invocations must not consume publish retries.
	for i := 0; i < 4; i++ {
		writer, err := journal.OpenWriter(path, runID())
		if err != nil {
			t.Fatal(err)
		}
		state, err := newExecutor(t, driver, Options{MaxAttempts: 1}).Execute(context.Background(), plan, runID(), writer)
		if err != nil {
			t.Fatal(err)
		}
		op, _ := state.Operation("op-a")
		if state.Completed || op.State != domain.StateBlocked || op.Attempt != uint32(i+1) ||
			op.BlockedAttempts != uint32(i+1) || op.ReconcileRequired || op.Ambiguous || op.Retryable ||
			op.ProviderStarted || op.ErrorCode != "CREDENTIAL_UNAVAILABLE" {
			t.Fatalf("blocked state=%+v operation=%+v", state, op)
		}
		if err := writer.Close(); err != nil {
			t.Fatal(err)
		}
	}
	driver.mu.Lock()
	if len(driver.applyCalls) != 0 || len(driver.reconcileCalls) != 0 {
		t.Fatalf("pre-dispatch block invoked a provider: apply=%d reconcile=%d", len(driver.applyCalls), len(driver.reconcileCalls))
	}
	driver.mu.Unlock()
	credentialsReady = true
	writer, err := journal.OpenWriter(path, runID())
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	state, err := newExecutor(t, driver, Options{MaxAttempts: 1}).Execute(context.Background(), plan, runID(), writer)
	if err != nil || !state.Completed {
		t.Fatalf("resumed state=%+v err=%v", state, err)
	}
	op, _ := state.Operation("op-a")
	if op.State != domain.StatePublished || op.Attempt != 5 || op.BlockedAttempts != 4 {
		t.Fatalf("resumed operation=%+v", op)
	}
	for _, event := range writer.Events() {
		if event.Type == journal.EventReconcileStarted {
			t.Fatalf("blocked preparation unexpectedly reconciled: %+v", event)
		}
	}
	driver.mu.Lock()
	defer driver.mu.Unlock()
	if len(driver.applyCalls) != 1 {
		t.Fatalf("provider published %d times, want once", len(driver.applyCalls))
	}
}

func TestPreDispatchBlockedCannotMaskCancellationOrPermanentError(t *testing.T) {
	plan := testPlan(t, []operationSpec{{id: "op-a", sideEffecting: true}})
	tests := []struct {
		name      string
		prepare   func(context.Context, Request, DriverOperation) (Preparation, error)
		wantState domain.NormalizedState
	}{
		{"permanent", func(context.Context, Request, DriverOperation) (Preparation, error) {
			return Preparation{}, &DriverError{Code: "PERMANENT"}
		}, domain.StateFailed},
		{"cancelled", func(ctx context.Context, _ Request, _ DriverOperation) (Preparation, error) {
			return Preparation{}, context.Canceled
		}, domain.StateCancelled},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			driver := &preparationDriver{prepare: tc.prepare}
			writer := openWriter(t, runID())
			state, err := newExecutor(t, driver, Options{MaxAttempts: 1}).Execute(context.Background(), plan, runID(), writer)
			if err != nil {
				t.Fatal(err)
			}
			op, _ := state.Operation("op-a")
			if op.State != tc.wantState || op.BlockedAttempts != 0 {
				t.Fatalf("state=%+v", op)
			}
		})
	}
}

func TestCredentialBlockNeverAllowsPostDispatchRetry(t *testing.T) {
	plan := testPlan(t, []operationSpec{{id: "op-a", sideEffecting: true}})
	driver := &scriptedDriver{
		apply: func(context.Context, Request) (Result, error) {
			return Result{}, &DriverError{Code: "CREDENTIAL_UNAVAILABLE", Blocked: true, Ambiguous: true}
		},
		reconcile: func(context.Context, Request) (Result, error) { return waiting(), nil },
	}
	writer := openWriter(t, runID())
	state, err := newExecutor(t, driver, Options{MaxAttempts: 1}).Execute(context.Background(), plan, runID(), writer)
	if err != nil {
		t.Fatal(err)
	}
	op, _ := state.Operation("op-a")
	if !op.ReconcileRequired || !op.Ambiguous || op.State == domain.StateBlocked {
		t.Fatalf("post-dispatch failure incorrectly marked blocked: %+v", op)
	}
	// A completed journal must not be silently replaced with a new run ID.
	other, _ := domain.NewRunID("other-run")
	_, err = newExecutor(t, driver, Options{}).Execute(context.Background(), plan, other, writer)
	if err == nil || errors.Is(err, journal.ErrPlanMismatch) {
		t.Fatalf("expected run mismatch, got %v", err)
	}
}
