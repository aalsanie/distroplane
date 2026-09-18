package executor

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/aalsanie/distroplane/internal/domain"
	"github.com/aalsanie/distroplane/internal/journal"
)

type fixedLeaseManager struct {
	lease Lease
	err   error
}

func (m fixedLeaseManager) Acquire(context.Context, string) (Lease, error) {
	return m.lease, m.err
}

type fixedLease struct {
	err error
}

func (l fixedLease) Release() error {
	return l.err
}

func TestDefaultsAndWait(t *testing.T) {
	executor, err := New(&scriptedDriver{}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if executor.maxConcurrency != defaultMaxConcurrency || executor.maxAttempts != defaultMaxAttempts || executor.leases == nil || executor.backoff == nil {
		t.Fatalf("executor=%+v", executor)
	}
	for _, tc := range []struct {
		attempt uint32
		want    time.Duration
	}{
		{0, 0},
		{1, 250 * time.Millisecond},
		{2, 500 * time.Millisecond},
		{6, 4 * time.Second},
	} {
		if got := defaultBackoff(tc.attempt); got != tc.want {
			t.Fatalf("attempt=%d got=%s want=%s", tc.attempt, got, tc.want)
		}
	}
	if err := wait(context.Background(), 0); err != nil {
		t.Fatal(err)
	}
	if err := wait(context.Background(), time.Millisecond); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := wait(ctx, 0); !errors.Is(err, context.Canceled) {
		t.Fatalf("err=%v", err)
	}
	if err := wait(ctx, time.Hour); !errors.Is(err, context.Canceled) {
		t.Fatalf("err=%v", err)
	}
}

func TestClassifyCallError(t *testing.T) {
	parent := context.Background()
	callCtx := context.Background()
	cases := []struct {
		name      string
		parent    context.Context
		callCtx   context.Context
		err       error
		code      string
		retryable bool
		ambiguous bool
		cancelled bool
	}{
		{"deadline", parent, callCtx, context.DeadlineExceeded, "TIMEOUT", true, true, false},
		{"driver", parent, callCtx, &DriverError{Code: "TRANSIENT", Retryable: true}, "TRANSIENT", true, false, false},
		{"driver ambiguous", parent, callCtx, &DriverError{Code: "UNKNOWN", Ambiguous: true}, "UNKNOWN", false, true, false},
		{"invalid driver", parent, callCtx, &DriverError{Code: " bad"}, "DRIVER_ERROR", false, true, false},
		{"generic", parent, callCtx, errors.New("broken"), "DRIVER_ERROR", false, true, false},
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	cases = append(cases,
		struct {
			name      string
			parent    context.Context
			callCtx   context.Context
			err       error
			code      string
			retryable bool
			ambiguous bool
			cancelled bool
		}{"cancelled", cancelled, cancelled, context.Canceled, "CANCELLED", false, true, true},
	)
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			code, retryable, ambiguous, wasCancelled := classifyCallError(tc.parent, tc.callCtx, tc.err)
			if code != tc.code || retryable != tc.retryable || ambiguous != tc.ambiguous || wasCancelled != tc.cancelled {
				t.Fatalf("got=(%q,%v,%v,%v)", code, retryable, ambiguous, wasCancelled)
			}
		})
	}
}

func TestExecuteInfrastructureFailures(t *testing.T) {
	plan := testPlan(t, []operationSpec{{id: "op-a"}})

	t.Run("acquire", func(t *testing.T) {
		want := errors.New("lease acquire")
		executor := newExecutor(t, &scriptedDriver{}, Options{Leases: fixedLeaseManager{err: want}})
		_, err := executor.Execute(context.Background(), plan, runID(), openWriter(t, runID()))
		if !errors.Is(err, want) {
			t.Fatalf("err=%v", err)
		}
	})

	t.Run("release", func(t *testing.T) {
		want := errors.New("lease release")
		writer := openWriter(t, runID())
		executor := newExecutor(t, &scriptedDriver{}, Options{Leases: fixedLeaseManager{lease: fixedLease{err: want}}})
		_, err := executor.Execute(context.Background(), plan, runID(), writer)
		if !errors.Is(err, want) {
			t.Fatalf("err=%v", err)
		}
		state, reduceErr := journal.Reduce(plan, writer.Events())
		if reduceErr != nil {
			t.Fatal(reduceErr)
		}
		op, _ := state.Operation("op-a")
		if op.State != domain.StatePublished {
			t.Fatalf("op=%+v", op)
		}
	})

	t.Run("closed writer", func(t *testing.T) {
		writer := openWriter(t, runID())
		if err := writer.Close(); err != nil {
			t.Fatal(err)
		}
		_, err := newExecutor(t, &scriptedDriver{}, Options{}).Execute(context.Background(), plan, runID(), writer)
		if !errors.Is(err, journal.ErrWriterClosed) {
			t.Fatalf("err=%v", err)
		}
	})
}

func TestExecuteRejectsRunMismatch(t *testing.T) {
	plan := testPlan(t, []operationSpec{{id: "op-a"}})
	writer := openWriter(t, runID())
	if _, err := writer.Append(journal.Entry{RunID: runID(), Type: journal.EventRunStarted}); err != nil {
		t.Fatal(err)
	}
	other, _ := domain.NewRunID("run-2")
	_, err := newExecutor(t, &scriptedDriver{}, Options{}).Execute(context.Background(), plan, other, writer)
	if err == nil || !strings.Contains(err.Error(), "journal belongs to run") {
		t.Fatalf("err=%v", err)
	}
}

func TestExecuteReconcileFailureAndContractViolation(t *testing.T) {
	plan := testPlan(t, []operationSpec{{id: "op-a", sideEffecting: true}})

	preseed := func(t *testing.T) *journal.Writer {
		writer := openWriter(t, runID())
		for _, entry := range []journal.Entry{
			{RunID: runID(), Type: journal.EventRunStarted},
			{RunID: runID(), Type: journal.EventAttemptStarted, OperationID: "op-a", TargetID: "target-a", Payload: journal.Payload{Attempt: 1}},
			{RunID: runID(), Type: journal.EventSideEffectDispatched, OperationID: "op-a", TargetID: "target-a", Payload: journal.Payload{Attempt: 1}},
		} {
			if _, err := writer.Append(entry); err != nil {
				t.Fatal(err)
			}
		}
		return writer
	}

	t.Run("retryable reconcile failure", func(t *testing.T) {
		writer := preseed(t)
		driver := &scriptedDriver{reconcile: func(context.Context, Request) (Result, error) {
			return Result{}, &DriverError{Code: "RECONCILE_TRANSIENT", Retryable: true}
		}}
		state, err := newExecutor(t, driver, Options{}).Execute(context.Background(), plan, runID(), writer)
		if err != nil {
			t.Fatal(err)
		}
		op, _ := state.Operation("op-a")
		if op.State != domain.StateWaitingExternal || !op.ReconcileRequired || !op.Ambiguous || !op.Retryable || op.ErrorCode != "RECONCILE_TRANSIENT" {
			t.Fatalf("op=%+v", op)
		}
	})

	t.Run("malformed reconcile result", func(t *testing.T) {
		writer := preseed(t)
		driver := &scriptedDriver{reconcile: func(context.Context, Request) (Result, error) {
			return Result{State: domain.StatePublished}, nil
		}}
		_, err := newExecutor(t, driver, Options{}).Execute(context.Background(), plan, runID(), writer)
		if !errors.Is(err, ErrDriverContract) {
			t.Fatalf("err=%v", err)
		}
		state, reduceErr := journal.Reduce(plan, writer.Events())
		if reduceErr != nil {
			t.Fatal(reduceErr)
		}
		op, _ := state.Operation("op-a")
		if op.State != domain.StateWaitingExternal || !op.ReconcileRequired || !op.Ambiguous || op.ErrorCode != "DRIVER_CONTRACT_ERROR" {
			t.Fatalf("op=%+v", op)
		}
	})
}

func TestExecuteKnownNonAmbiguousSideEffectFailure(t *testing.T) {
	plan := testPlan(t, []operationSpec{{id: "op-a", sideEffecting: true}})
	driver := &scriptedDriver{apply: func(context.Context, Request) (Result, error) {
		return Result{}, &DriverError{Code: "REJECTED_BEFORE_SUBMIT", Ambiguous: false}
	}}
	state, err := newExecutor(t, driver, Options{}).Execute(context.Background(), plan, runID(), openWriter(t, runID()))
	if err != nil {
		t.Fatal(err)
	}
	op, _ := state.Operation("op-a")
	if op.State != domain.StateFailed || op.ReconcileRequired || op.Ambiguous || op.ErrorCode != "REJECTED_BEFORE_SUBMIT" {
		t.Fatalf("op=%+v", op)
	}
}

func TestExecuteRejectedResultIsTerminal(t *testing.T) {
	plan := testPlan(t, []operationSpec{{id: "op-a"}})
	driver := &scriptedDriver{apply: func(context.Context, Request) (Result, error) {
		return Result{State: domain.StateRejected, ProviderState: "rejected", Evidence: json.RawMessage(`{"provider":"test"}`)}, nil
	}}
	state, err := newExecutor(t, driver, Options{}).Execute(context.Background(), plan, runID(), openWriter(t, runID()))
	if err != nil {
		t.Fatal(err)
	}
	op, _ := state.Operation("op-a")
	if !state.Completed || op.State != domain.StateRejected {
		t.Fatalf("state=%+v op=%+v", state, op)
	}
}

func TestHelpersAndValidationBranches(t *testing.T) {
	plan := testPlan(t, []operationSpec{{id: "op-a"}, {id: "op-b", dependencies: []string{"op-a"}}})
	operations := plan.Operations()
	if key := leaseKey(plan.ID(), operations[0].ID()); key == "" || !strings.ContainsRune(key, '\x00') {
		t.Fatalf("key=%q", key)
	}
	if !hasTerminalUnpublishedDependency(operations[1], map[domain.OperationID]journal.OperationState{}, 3) {
		t.Fatal("missing dependency not terminal")
	}
	states := map[domain.OperationID]journal.OperationState{
		"op-a": {ID: "op-a", State: domain.StateRejected},
	}
	if !hasTerminalUnpublishedDependency(operations[1], states, 3) {
		t.Fatal("rejected dependency not terminal")
	}
	states["op-a"] = journal.OperationState{ID: "op-a", State: domain.StateFailed, Attempt: 1, Retryable: true}
	if hasTerminalUnpublishedDependency(operations[1], states, 3) {
		t.Fatal("retryable dependency treated as terminal")
	}
	states["op-a"] = journal.OperationState{ID: "op-a", State: domain.StateFailed, Attempt: 3, Retryable: true}
	if !hasTerminalUnpublishedDependency(operations[1], states, 3) {
		t.Fatal("exhausted dependency not terminal")
	}

	for _, value := range []string{" leading", "trailing ", "line\nbreak", strings.Repeat("x", 257), string([]byte{0xff})} {
		if err := validateText("value", value, false); err == nil {
			t.Fatalf("accepted %q", value)
		}
	}
	if err := validateText("value", "", true); err != nil {
		t.Fatal(err)
	}
	if err := validateText("value", strings.Repeat("x", 256), false); err != nil {
		t.Fatal(err)
	}
	if (&DriverError{}).Error() != "driver error" {
		t.Fatal("driver fallback text changed")
	}

	leases := &MemoryLeases{}
	lease, err := leases.Acquire(context.Background(), "key")
	if err != nil {
		t.Fatal(err)
	}
	if err := lease.Release(); err != nil {
		t.Fatal(err)
	}
	if err := (&memoryLease{}).Release(); err == nil {
		t.Fatal("invalid empty lease accepted")
	}
}
