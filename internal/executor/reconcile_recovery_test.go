package executor

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/aalsanie/distroplane/internal/domain"
	"github.com/aalsanie/distroplane/internal/journal"
)

func appendObservationEntry(t *testing.T, writer *journal.Writer, id domain.OperationID, typ journal.EventType, payload journal.Payload) {
	t.Helper()
	if _, err := writer.Append(journal.Entry{RunID: runID(), Type: typ, OperationID: id, TargetID: "target-a", Payload: payload}); err != nil {
		t.Fatal(err)
	}
}

func seedPendingObservation(t *testing.T, writer *journal.Writer, id domain.OperationID, ambiguous bool) {
	t.Helper()
	appendObservationEntry(t, writer, id, journal.EventAttemptStarted, journal.Payload{Attempt: 1})
	appendObservationEntry(t, writer, id, journal.EventSideEffectDispatched, journal.Payload{Attempt: 1})
	if !ambiguous {
		appendObservationEntry(t, writer, id, journal.EventOperationWaitingExternal, journal.Payload{Attempt: 1, ProviderState: "pending", Evidence: []byte(`{"ref":"original"}`)})
	}
}

func openObservationJournal(t *testing.T, path string) *journal.Writer {
	t.Helper()
	writer, err := journal.OpenWriter(path, runID())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = writer.Close() })
	return writer
}

func beginObservationJournal(t *testing.T, path string, plan domain.Plan) *journal.Writer {
	t.Helper()
	writer := openObservationJournal(t, path)
	if _, err := writer.Append(journal.Entry{RunID: runID(), Type: journal.EventRunStarted, PlanID: plan.ID()}); err != nil {
		t.Fatal(err)
	}
	return writer
}

func assertObservationReplay(t *testing.T, plan domain.Plan, writer *journal.Writer) journal.DerivedState {
	t.Helper()
	events := writer.Events()
	for i := 1; i <= len(events); i++ {
		if _, err := journal.Reduce(plan, events[:i]); err != nil {
			t.Fatalf("prefix %d (%s): %v", i, events[i-1].Type, err)
		}
	}
	state, err := journal.Reduce(plan, events)
	if err != nil {
		t.Fatal(err)
	}
	return state
}

func TestExecuteRecoversEveryInterruptedObservationBoundary(t *testing.T) {
	boundaries := []journal.EventType{
		journal.EventLeaseAcquired, journal.EventReconcileStarted, journal.EventCredentialResolved,
		journal.EventProviderProcessStarted, journal.EventProviderResponseReceived, journal.EventReconcileResult,
	}
	for _, reconcileOnly := range []bool{false, true} {
		for _, ambiguous := range []bool{false, true} {
			for stage, boundary := range boundaries {
				t.Run(fmt.Sprintf("observe-only=%t/ambiguous=%t/%s", reconcileOnly, ambiguous, boundary), func(t *testing.T) {
					plan := testPlan(t, []operationSpec{{id: "op-a", sideEffecting: true}})
					path := filepath.Join(t.TempDir(), "run.journal")
					writer := beginObservationJournal(t, path, plan)
					seedPendingObservation(t, writer, "op-a", ambiguous)
					now := time.Now().UTC()
					for crash := range 2 {
						lease := &journal.LeasePayload{ID: fmt.Sprintf("interrupted-%d", crash), Owner: "old-worker", AcquiredAt: now.Add(-10 * time.Second), ExpiresAt: now.Add(-5 * time.Second)}
						appendObservationEntry(t, writer, "op-a", journal.EventLeaseAcquired, journal.Payload{Lease: lease})
						if stage >= 1 {
							appendObservationEntry(t, writer, "op-a", journal.EventReconcileStarted, journal.Payload{Attempt: 1})
						}
						if stage >= 2 {
							appendObservationEntry(t, writer, "op-a", journal.EventCredentialResolved, journal.Payload{Attempt: 1, CredentialRef: "registry"})
						}
						if stage >= 3 {
							appendObservationEntry(t, writer, "op-a", journal.EventProviderProcessStarted, journal.Payload{Attempt: 1})
						}
						if stage >= 4 {
							appendObservationEntry(t, writer, "op-a", journal.EventProviderResponseReceived, journal.Payload{Attempt: 1})
						}
						renewed := *lease
						renewed.ExpiresAt = now.Add(-time.Second)
						appendObservationEntry(t, writer, "op-a", journal.EventLeaseRenewed, journal.Payload{Lease: &renewed})
						if stage == 5 {
							appendObservationEntry(t, writer, "op-a", journal.EventReconcileResult, journal.Payload{Attempt: 1, State: domain.StatePublished, ProviderState: "published", Evidence: published().Evidence})
						}
						before := assertObservationReplay(t, plan, writer)
						if err := writer.Close(); err != nil {
							t.Fatal(err)
						}
						writer = openObservationJournal(t, path)
						after := assertObservationReplay(t, plan, writer)
						if !reflect.DeepEqual(before, after) {
							t.Fatal("reopening changed derived state")
						}
						if stage == 5 {
							break
						}
						if crash == 0 {
							changed, err := expireJournalLeases(runID(), writer, after, now)
							if err != nil || !changed {
								t.Fatalf("expiry changed=%t err=%v", changed, err)
							}
							assertObservationReplay(t, plan, writer)
							if err := writer.Close(); err != nil {
								t.Fatal(err)
							}
							writer = openObservationJournal(t, path)
						}
					}
					prefix := writer.Events()
					driver := &scriptedDriver{reconcile: func(_ context.Context, request Request) (Result, error) {
						if request.RunID != runID() || request.PlanID != plan.ID() || request.Operation.ID() != "op-a" ||
							request.Operation.IdempotencyKey() != "key-op-a" || request.Attempt != 1 || request.Previous == nil || request.Previous.Ambiguous != ambiguous {
							return Result{}, fmt.Errorf("unexpected recovery request: %+v", request)
						}
						if !ambiguous && (request.Previous.State != domain.StateWaitingExternal || request.Previous.ProviderState != "pending" ||
							string(request.Previous.Evidence) != `{"ref":"original"}`) {
							return Result{}, errors.New("prior observation lost")
						}
						return published(), nil
					}}
					state, err := newExecutor(t, driver, Options{ReconcileOnly: reconcileOnly, MaxAttempts: 1}).Execute(context.Background(), plan, runID(), writer)
					if err != nil {
						t.Fatal(err)
					}
					op, _ := state.Operation("op-a")
					wantCalls := 1
					if stage == 5 {
						wantCalls = 0
					}
					if !state.Completed || op.State != domain.StatePublished || op.Attempt != 1 || op.LeaseActive ||
						op.ReconcileRequired || len(driver.applyCalls) != 0 || len(driver.reconcileCalls) != wantCalls {
						t.Fatalf("operation=%+v apply=%d reconcile=%d", op, len(driver.applyCalls), len(driver.reconcileCalls))
					}
					if !reflect.DeepEqual(prefix, writer.Events()[:len(prefix)]) {
						t.Fatal("recovery rewrote journal history")
					}
					if err := writer.Close(); err != nil {
						t.Fatal(err)
					}
					writer = openObservationJournal(t, path)
					if replay := assertObservationReplay(t, plan, writer); !reflect.DeepEqual(state, replay) {
						t.Fatal("final replay differs")
					}
				})
			}
		}
	}
}

func TestReconcileCancellationAndTimeoutPreservePriorEvidence(t *testing.T) {
	for _, cancelRun := range []bool{false, true} {
		t.Run(fmt.Sprintf("cancel=%t", cancelRun), func(t *testing.T) {
			plan := testPlan(t, []operationSpec{{id: "op-a", sideEffecting: true, timeout: 2 * time.Second}})
			path := filepath.Join(t.TempDir(), "run.journal")
			writer := beginObservationJournal(t, path, plan)
			seedPendingObservation(t, writer, "op-a", false)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			var stale ExecutionObserver
			driver := &scriptedDriver{reconcile: func(ctx context.Context, request Request) (Result, error) {
				stale = request.Observer
				if cancelRun {
					cancel()
				}
				<-ctx.Done()
				return published(), nil
			}}
			_, err := newExecutor(t, driver, Options{MaxAttempts: 1}).Execute(ctx, plan, runID(), writer)
			if cancelRun && !errors.Is(err, context.Canceled) || !cancelRun && err != nil {
				t.Fatalf("execute: %v", err)
			}
			state := assertObservationReplay(t, plan, writer)
			op, _ := state.Operation("op-a")
			wantCode := "TIMEOUT"
			if cancelRun {
				wantCode = "CANCELLED"
			}
			if op.State != domain.StateWaitingExternal || !op.ReconcileRequired || op.Attempt != 1 || op.ErrorCode != wantCode ||
				op.ProviderState != "pending" || string(op.Evidence) != `{"ref":"original"}` || len(driver.applyCalls) != 0 || len(driver.reconcileCalls) != 1 {
				t.Fatalf("operation=%+v", op)
			}
			if err := writer.Close(); err != nil {
				t.Fatal(err)
			}
			writer = openObservationJournal(t, path)
			recovery := &scriptedDriver{reconcile: func(_ context.Context, request Request) (Result, error) {
				if err := stale.ProviderProcessStarted(); !errors.Is(err, journal.ErrLeaseAuthority) {
					return Result{}, fmt.Errorf("late start: %v", err)
				}
				if err := stale.ProviderResponseReceived(); !errors.Is(err, journal.ErrLeaseAuthority) {
					return Result{}, fmt.Errorf("late response: %v", err)
				}
				if err := request.Observer.SideEffectDispatched(); err == nil {
					return Result{}, errors.New("reconciliation permitted side-effect dispatch")
				}
				return published(), nil
			}}
			state, err = newExecutor(t, recovery, Options{ReconcileOnly: true, MaxAttempts: 1}).Execute(context.Background(), plan, runID(), writer)
			if err != nil {
				t.Fatal(err)
			}
			op, _ = state.Operation("op-a")
			if op.State != domain.StatePublished || op.Attempt != 1 || len(recovery.applyCalls) != 0 || len(recovery.reconcileCalls) != 1 {
				t.Fatalf("operation=%+v", op)
			}
			assertObservationReplay(t, plan, writer)
		})
	}
}

type lostObservationLeaseManager struct{}

func (lostObservationLeaseManager) Acquire(_ context.Context, request LeaseRequest) (Lease, error) {
	now := time.Now().UTC()
	return &coverageLease{
		state:    LeaseState{ID: "lost", Owner: "worker", OperationID: request.OperationID, AcquiredAt: now, ExpiresAt: now.Add(time.Second)},
		renewErr: ErrLeaseExpired, releaseErr: ErrLeaseExpired,
	}, nil
}

func TestReconcileRejectsResultAfterLeaseLoss(t *testing.T) {
	plan := testPlan(t, []operationSpec{{id: "op-a", sideEffecting: true}})
	path := filepath.Join(t.TempDir(), "run.journal")
	writer := beginObservationJournal(t, path, plan)
	seedPendingObservation(t, writer, "op-a", false)
	driver := &scriptedDriver{reconcile: func(ctx context.Context, _ Request) (Result, error) {
		<-ctx.Done()
		return published(), nil
	}}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err := newExecutor(t, driver, Options{Leases: lostObservationLeaseManager{}}).Execute(ctx, plan, runID(), writer)
	if !errors.Is(err, ErrLeaseExpired) {
		t.Fatalf("expected authority loss, got %v", err)
	}
	state := assertObservationReplay(t, plan, writer)
	op, _ := state.Operation("op-a")
	if !op.ReconcileRequired || op.State != domain.StateWaitingExternal || op.LeaseActive || op.Attempt != 1 || string(op.Evidence) != `{"ref":"original"}` {
		t.Fatalf("operation=%+v", op)
	}
	for _, event := range writer.Events() {
		if event.Type == journal.EventReconcileResult {
			t.Fatal("stale completion was journaled")
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	writer = openObservationJournal(t, path)
	recovery := &scriptedDriver{}
	state, err = newExecutor(t, recovery, Options{}).Execute(context.Background(), plan, runID(), writer)
	if err != nil || !state.Completed || len(recovery.applyCalls) != 0 || len(recovery.reconcileCalls) != 1 {
		t.Fatalf("recovery failed: %+v %v", state, err)
	}
	assertObservationReplay(t, plan, writer)
}

func TestObservationRecoveryKeepsIndependentLeases(t *testing.T) {
	plan := testPlan(t, []operationSpec{{id: "op-a", sideEffecting: true}, {id: "op-b", sideEffecting: true}})
	path := filepath.Join(t.TempDir(), "run.journal")
	writer := beginObservationJournal(t, path, plan)
	now := time.Now().UTC()
	for _, id := range []domain.OperationID{"op-a", "op-b"} {
		seedPendingObservation(t, writer, id, false)
		lease := &journal.LeasePayload{ID: string(id), Owner: "worker", AcquiredAt: now.Add(-2 * time.Second), ExpiresAt: now.Add(-time.Second)}
		appendObservationEntry(t, writer, id, journal.EventLeaseAcquired, journal.Payload{Lease: lease})
		appendObservationEntry(t, writer, id, journal.EventReconcileStarted, journal.Payload{Attempt: 1})
		if id == "op-b" {
			appendObservationEntry(t, writer, id, journal.EventReconcileResult, journal.Payload{Attempt: 1, State: domain.StatePublished, Evidence: published().Evidence})
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	writer = openObservationJournal(t, path)
	driver := &scriptedDriver{}
	state, err := newExecutor(t, driver, Options{MaxConcurrency: 2}).Execute(context.Background(), plan, runID(), writer)
	if err != nil || !state.Completed || len(driver.applyCalls) != 0 || len(driver.reconcileCalls) != 1 || driver.reconcileCalls[0].Operation.ID() != "op-a" {
		t.Fatalf("state=%+v err=%v calls=%+v", state, err, driver.reconcileCalls)
	}
	assertObservationReplay(t, plan, writer)
}

func TestObservationRecoveryRejectsInvalidHistory(t *testing.T) {
	plan := testPlan(t, []operationSpec{{id: "op-a", sideEffecting: true}})
	path := filepath.Join(t.TempDir(), "run.journal")
	writer := beginObservationJournal(t, path, plan)
	seedPendingObservation(t, writer, "op-a", true)
	now := time.Now().UTC()
	lease := &journal.LeasePayload{ID: "old", Owner: "worker", AcquiredAt: now.Add(-2 * time.Second), ExpiresAt: now.Add(-time.Second)}
	appendObservationEntry(t, writer, "op-a", journal.EventLeaseAcquired, journal.Payload{Lease: lease})
	appendObservationEntry(t, writer, "op-a", journal.EventReconcileStarted, journal.Payload{Attempt: 1})
	appendObservationEntry(t, writer, "op-a", journal.EventReconcileStarted, journal.Payload{Attempt: 1})
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	original, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	writer = openObservationJournal(t, path)
	driver := &scriptedDriver{}
	if _, err := newExecutor(t, driver, Options{}).Execute(context.Background(), plan, runID(), writer); err == nil {
		t.Fatal("duplicate reconciliation history accepted")
	}
	if len(driver.applyCalls) != 0 || len(driver.reconcileCalls) != 0 {
		t.Fatal("invalid journal contacted provider")
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(path)
	if err != nil || !reflect.DeepEqual(original, after) {
		t.Fatalf("invalid journal was modified: %v", err)
	}
	after[len(after)-1] ^= 1
	if err := os.WriteFile(path, after, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := journal.OpenWriter(path, runID()); !errors.Is(err, journal.ErrCorruptRecord) {
		t.Fatalf("complete corruption accepted: %v", err)
	}
}

func TestReconcileRecoveryAfterCompletedPendingObservation(t *testing.T) {
	plan := testPlan(t, []operationSpec{{id: "op-a", sideEffecting: true}})
	path := filepath.Join(t.TempDir(), "run.journal")
	writer := beginObservationJournal(t, path, plan)
	seedPendingObservation(t, writer, "op-a", true)
	now := time.Now().UTC()
	lease := &journal.LeasePayload{ID: "old", Owner: "worker", AcquiredAt: now.Add(-2 * time.Second), ExpiresAt: now.Add(-time.Second)}
	appendObservationEntry(t, writer, "op-a", journal.EventLeaseAcquired, journal.Payload{Lease: lease})
	appendObservationEntry(t, writer, "op-a", journal.EventReconcileStarted, journal.Payload{Attempt: 1})
	appendObservationEntry(t, writer, "op-a", journal.EventProviderProcessStarted, journal.Payload{Attempt: 1})
	appendObservationEntry(t, writer, "op-a", journal.EventProviderResponseReceived, journal.Payload{Attempt: 1})
	appendObservationEntry(t, writer, "op-a", journal.EventReconcileResult, journal.Payload{Attempt: 1, State: domain.StateWaitingExternal, ProviderState: "review", Evidence: []byte(`{"ref":"latest"}`)})
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	writer = openObservationJournal(t, path)
	driver := &scriptedDriver{reconcile: func(_ context.Context, request Request) (Result, error) {
		if request.Attempt != 1 || request.Previous == nil || !request.Previous.Ambiguous ||
			request.Previous.ProviderState != "review" || string(request.Previous.Evidence) != `{"ref":"latest"}` {
			return Result{}, fmt.Errorf("unexpected prior observation: %+v", request.Previous)
		}
		return published(), nil
	}}
	state, err := newExecutor(t, driver, Options{MaxAttempts: 1, ReconcileOnly: true}).Execute(context.Background(), plan, runID(), writer)
	if err != nil || !state.Completed || len(driver.applyCalls) != 0 || len(driver.reconcileCalls) != 1 {
		t.Fatalf("state=%+v err=%v apply=%d reconcile=%d", state, err, len(driver.applyCalls), len(driver.reconcileCalls))
	}
	assertObservationReplay(t, plan, writer)
}

func TestPersistCompletedObservationAfterContextCancellation(t *testing.T) {
	plan := testPlan(t, []operationSpec{{id: "op-a", sideEffecting: true}})
	path := filepath.Join(t.TempDir(), "run.journal")
	writer := beginObservationJournal(t, path, plan)
	seedPendingObservation(t, writer, "op-a", true)
	operation := plan.Operations()[0]
	lease, err := NewMemoryLeases().Acquire(context.Background(), LeaseRequest{Key: leaseKey(plan.ID(), operation.ID()), OperationID: operation.ID()})
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Release()
	if err := appendLeaseEvent(runID(), writer, operation, journal.EventLeaseAcquired, lease.State()); err != nil {
		t.Fatal(err)
	}
	appendObservationEntry(t, writer, operation.ID(), journal.EventReconcileStarted, journal.Payload{Attempt: 1})
	appendObservationEntry(t, writer, operation.ID(), journal.EventProviderProcessStarted, journal.Payload{Attempt: 1})
	appendObservationEntry(t, writer, operation.ID(), journal.EventProviderResponseReceived, journal.Payload{Attempt: 1})
	callCtx, cancel := context.WithCancel(context.Background())
	result := published()
	cancel()
	invocationWriter := reconciliationWriter{writer: writer, lease: lease.State()}
	if err := persistReconcileOutcome(context.Background(), callCtx, invocationWriter, runID(), operation, 1, true, result, nil); err != nil {
		t.Fatal(err)
	}
	if err := releaseLease(runID(), writer, operation, lease); err != nil {
		t.Fatal(err)
	}
	state := assertObservationReplay(t, plan, writer)
	op, _ := state.Operation(operation.ID())
	if op.State != domain.StatePublished || op.Attempt != 1 || op.ReconcileRequired || op.Ambiguous || op.LeaseActive {
		t.Fatalf("operation=%+v", op)
	}
}
