package journal

import (
	"errors"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/aalsanie/distroplane/internal/domain"
)

func TestReduceInterruptedReconciliation(t *testing.T) {
	plan := testPlan(t)
	for _, state := range []domain.NormalizedState{domain.StateRunning, domain.StateWaitingExternal} {
		for _, boundary := range []EventType{EventReconcileStarted, EventCredentialResolved, EventProviderProcessStarted, EventProviderResponseReceived} {
			for _, end := range []EventType{EventLeaseExpired, EventLeaseReleased} {
				t.Run(string(state)+"/"+string(boundary)+"/"+string(end), func(t *testing.T) {
					events := []Event{runStarted(1), attemptStarted(2, "op-c", "target-b", 1), dispatched(3, "op-c", "target-b", 1)}
					add := func(typ EventType, payload Payload) {
						events = append(events, event(uint64(len(events)+1), typ, "op-c", "target-b", payload))
					}
					if state == domain.StateWaitingExternal {
						add(EventOperationWaitingExternal, Payload{Attempt: 1, ProviderState: "pending", Evidence: evidence(`{"ref":"original"}`)})
					}
					acquiredAt := int64(len(events) + 1)
					expiresAt := acquiredAt + 1
					switch boundary {
					case EventCredentialResolved:
						expiresAt++
					case EventProviderProcessStarted:
						expiresAt += 2
					case EventProviderResponseReceived:
						expiresAt += 3
					}
					if end == EventLeaseReleased {
						expiresAt += 10
					}
					lease := leasePayload("first", "worker", acquiredAt, expiresAt+1)
					add(EventLeaseAcquired, Payload{Lease: lease})
					add(EventReconcileStarted, Payload{Attempt: 1})
					if boundary != EventReconcileStarted {
						add(EventCredentialResolved, Payload{Attempt: 1, CredentialRef: "registry"})
					}
					if boundary == EventProviderProcessStarted || boundary == EventProviderResponseReceived {
						add(EventProviderProcessStarted, Payload{Attempt: 1})
					}
					if boundary == EventProviderResponseReceived {
						add(EventProviderResponseReceived, Payload{Attempt: 1})
					}
					before, err := Reduce(plan, events)
					if err != nil {
						t.Fatal(err)
					}
					original, _ := before.Operation("op-c")
					add(end, Payload{Lease: lease})
					after, err := Reduce(plan, events)
					if err != nil {
						t.Fatal(err)
					}
					op, _ := after.Operation("op-c")
					if op.State != original.State || op.Attempt != 1 || !op.ReconcileRequired ||
						op.Ambiguous != original.Ambiguous || op.LeaseActive || op.ProviderStarted || op.ProviderResponded ||
						op.ProviderState != original.ProviderState || string(op.Evidence) != string(original.Evidence) {
						t.Fatalf("before=%+v after=%+v", original, op)
					}
					for _, late := range []EventType{EventReconcileStarted, EventProviderProcessStarted, EventProviderResponseReceived, EventOperationPublished, EventOperationResult, EventReconcileResult, EventCredentialResolved, EventOutcomeAmbiguous, EventSideEffectDispatched} {
						payload := Payload{Attempt: 1}
						switch late {
						case EventOperationResult, EventReconcileResult:
							payload.State = domain.StatePublished
						case EventCredentialResolved:
							payload.CredentialRef = "registry"
						}
						candidate := append(append([]Event(nil), events...), event(uint64(len(events)+1), late, "op-c", "target-b", payload))
						if _, err := Reduce(plan, candidate); err == nil {
							t.Fatalf("accepted stale %s", late)
						}
					}
					next := leasePayload("second", "worker", int64(len(events)+1), int64(len(events)+20))
					add(EventLeaseAcquired, Payload{Lease: next})
					add(EventReconcileStarted, Payload{Attempt: 1})
					add(EventReconcileResult, Payload{Attempt: 1, State: domain.StateWaitingExternal, ErrorCode: "TIMEOUT", Retryable: true})
					pending, err := Reduce(plan, events)
					if err != nil {
						t.Fatal(err)
					}
					op, _ = pending.Operation("op-c")
					if !op.ReconcileRequired || op.ProviderState != original.ProviderState || string(op.Evidence) != string(original.Evidence) || op.ErrorCode != "TIMEOUT" {
						t.Fatalf("failed observation replaced evidence: %+v", op)
					}
					add(EventLeaseReleased, Payload{Lease: next})
					add(EventLeaseAcquired, Payload{Lease: leasePayload("third", "worker", int64(len(events)+1), int64(len(events)+20))})
					add(EventReconcileStarted, Payload{Attempt: 1})
					add(EventReconcileResult, Payload{Attempt: 1, State: domain.StatePublished, ProviderState: "published", Evidence: evidence(`{"ref":"new"}`)})
					for i := 1; i <= len(events); i++ {
						if _, err := Reduce(plan, events[:i]); err != nil {
							t.Fatalf("prefix %d: %v", i, err)
						}
					}
					final, err := Reduce(plan, events)
					if err != nil {
						t.Fatal(err)
					}
					op, _ = final.Operation("op-c")
					if op.State != domain.StatePublished || op.Attempt != 1 || op.ReconcileRequired || op.Ambiguous || string(op.Evidence) != `{"ref":"new"}` {
						t.Fatalf("final=%+v", op)
					}
					again, err := Reduce(plan, events)
					if err != nil || !reflect.DeepEqual(final, again) {
						t.Fatalf("non-deterministic replay: %v", err)
					}
				})
			}
		}
	}
}

func TestReduceUnleasedObservationAdoptionAndDuplicateStart(t *testing.T) {
	plan := testPlan(t)
	events := []Event{runStarted(1), attemptStarted(2, "op-c", "target-b", 1), dispatched(3, "op-c", "target-b", 1), reconcileStarted(4, "op-c", "target-b", 1)}
	duplicate := append(append([]Event(nil), events...), reconcileStarted(5, "op-c", "target-b", 1))
	if _, err := Reduce(plan, duplicate); err == nil {
		t.Fatal("duplicate start accepted")
	}
	events = append(events, event(5, EventLeaseAcquired, "op-c", "target-b", Payload{Lease: leasePayload("new", "worker", 5, 20)}),
		reconcileStarted(6, "op-c", "target-b", 1), reconcileResult(7, "op-c", "target-b", 1, domain.StatePublished))
	state, err := Reduce(plan, events)
	if err != nil {
		t.Fatal(err)
	}
	op, _ := state.Operation("op-c")
	if op.State != domain.StatePublished || op.Attempt != 1 {
		t.Fatalf("operation=%+v", op)
	}
}

func TestAppendWithLeaseAuthority(t *testing.T) {
	path := filepath.Join(t.TempDir(), "run.journal")
	writer, err := OpenWriter(path, runID())
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	if _, err := (*Writer)(nil).AppendWithLease(Entry{}, LeasePayload{}); !errors.Is(err, ErrWriterClosed) {
		t.Fatalf("nil writer: %v", err)
	}
	now := time.Now().UTC()
	writer.clock = func() time.Time { return now }
	first := LeasePayload{ID: "first", Owner: "worker", AcquiredAt: now, ExpiresAt: now.Add(time.Second)}
	second := LeasePayload{ID: "second", Owner: "worker", AcquiredAt: now.Add(time.Second), ExpiresAt: now.Add(3 * time.Second)}
	entry := Entry{RunID: runID(), OperationID: "op-c", TargetID: "target-b", Type: EventReconcileResult, Payload: Payload{Attempt: 1, State: domain.StatePublished}}
	appendEntry := func(e Entry) {
		t.Helper()
		if _, err := writer.Append(e); err != nil {
			t.Fatal(err)
		}
	}
	appendLease := func(typ EventType, op domain.OperationID, lease LeasePayload) {
		t.Helper()
		appendEntry(Entry{RunID: runID(), Type: typ, OperationID: op, TargetID: "target-b", Payload: Payload{Lease: &lease}})
	}
	reject := func(lease LeasePayload) {
		t.Helper()
		count := len(writer.Events())
		if _, err := writer.AppendWithLease(entry, lease); !errors.Is(err, ErrLeaseAuthority) {
			t.Fatalf("stale lease accepted: %v", err)
		}
		if len(writer.Events()) != count {
			t.Fatal("rejected append changed journal")
		}
	}
	appendEntry(Entry{RunID: runID(), Type: EventRunStarted, PlanID: "plan-1"})
	reject(first)
	appendLease(EventLeaseAcquired, "op-c", first)
	other := first
	other.ID = "other-operation"
	appendLease(EventLeaseAcquired, "op-a", other)
	wrong := first
	wrong.Owner = "other"
	reject(wrong)
	wrong = first
	wrong.ID = "wrong"
	reject(wrong)
	wrong = first
	wrong.AcquiredAt = now.Add(-time.Second)
	reject(wrong)
	wrongTarget := entry
	wrongTarget.TargetID = "target-a"
	if _, err := writer.AppendWithLease(wrongTarget, first); !errors.Is(err, ErrLeaseAuthority) {
		t.Fatalf("wrong target: %v", err)
	}
	renewed := first
	renewed.ExpiresAt = now.Add(2 * time.Second)
	appendLease(EventLeaseRenewed, "op-c", renewed)
	now = first.ExpiresAt
	if _, err := writer.AppendWithLease(entry, first); err != nil {
		t.Fatalf("renewed authority rejected: %v", err)
	}
	now = renewed.ExpiresAt
	reject(first)
	appendLease(EventLeaseExpired, "op-c", renewed)
	reject(first)
	appendLease(EventLeaseAcquired, "op-c", second)
	reject(first)
	if _, err := writer.AppendWithLease(entry, second); err != nil {
		t.Fatal(err)
	}
	appendLease(EventLeaseReleased, "op-c", second)
	reject(second)
	writer.poisoned = true
	if _, err := writer.AppendWithLease(entry, second); !errors.Is(err, ErrWriterPoisoned) {
		t.Fatalf("poisoned writer: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := writer.AppendWithLease(entry, second); !errors.Is(err, ErrWriterClosed) {
		t.Fatalf("closed writer: %v", err)
	}
}

func TestFailedObservationPreservesEvidenceAcrossResultFormats(t *testing.T) {
	plan := testPlan(t)
	for _, typ := range []EventType{EventReconcileResult, EventOperationWaitingExternal} {
		t.Run(string(typ), func(t *testing.T) {
			events := []Event{
				runStarted(1),
				attemptStarted(2, "op-c", "target-b", 1),
				dispatched(3, "op-c", "target-b", 1),
				event(4, EventOperationWaitingExternal, "op-c", "target-b", Payload{Attempt: 1, ProviderState: "pending", Evidence: evidence(`{"ref":"original"}`)}),
				reconcileStarted(5, "op-c", "target-b", 1),
			}
			payload := Payload{Attempt: 1, ErrorCode: "TIMEOUT", Retryable: true}
			if typ == EventReconcileResult {
				payload.State = domain.StateWaitingExternal
			}
			events = append(events, event(6, typ, "op-c", "target-b", payload))
			state, err := Reduce(plan, events)
			if err != nil {
				t.Fatal(err)
			}
			op, _ := state.Operation("op-c")
			if op.State != domain.StateWaitingExternal || !op.ReconcileRequired || op.Ambiguous || op.Attempt != 1 ||
				op.ProviderState != "pending" || string(op.Evidence) != `{"ref":"original"}` || op.ErrorCode != "TIMEOUT" {
				t.Fatalf("operation=%+v", op)
			}
		})
	}
}

func TestInterruptedObservationInvalidatesEarlierRetryAuthority(t *testing.T) {
	plan := npmRetryPlan(t)
	events := ambiguousNpmEvents()
	events[5].Payload.ProviderState = "absent"
	events[5].Payload.Evidence = evidence(`{"ref":"prior"}`)
	first := leasePayload("observation-1", "worker", 7, 9)
	events = append(events,
		event(7, EventLeaseAcquired, "op-a", "target-a", Payload{Lease: first}),
		reconcileStarted(8, "op-a", "target-a", 1),
		event(9, EventLeaseExpired, "op-a", "target-a", Payload{Lease: first}),
	)
	assertNoRetry := func(events []Event) {
		t.Helper()
		state, err := Reduce(plan, events)
		if err != nil {
			t.Fatal(err)
		}
		op, _ := state.Operation("op-a")
		if !op.ReconcileRequired || !op.Ambiguous || op.Attempt != 1 || op.ProviderState != "absent" || string(op.Evidence) != `{"ref":"prior"}` {
			t.Fatalf("operation=%+v", op)
		}
		next := append(append([]Event(nil), events...), event(uint64(len(events)+1), EventRetryAuthorized, "op-a", "target-a", Payload{Attempt: 1}))
		if _, err := Reduce(plan, next); err == nil {
			t.Fatal("old absence authorized another apply")
		}
	}
	assertNoRetry(events)
	second := leasePayload("observation-2", "worker", 10, 20)
	events = append(events,
		event(10, EventLeaseAcquired, "op-a", "target-a", Payload{Lease: second}),
		reconcileStarted(11, "op-a", "target-a", 1),
		event(12, EventReconcileResult, "op-a", "target-a", Payload{Attempt: 1, State: domain.StateWaitingExternal, ErrorCode: "TIMEOUT", Retryable: true}),
		event(13, EventLeaseReleased, "op-a", "target-a", Payload{Lease: second}),
	)
	assertNoRetry(events)
	third := leasePayload("observation-3", "worker", 14, 20)
	events = append(events,
		event(14, EventLeaseAcquired, "op-a", "target-a", Payload{Lease: third}),
		reconcileStarted(15, "op-a", "target-a", 1),
		event(16, EventReconcileResult, "op-a", "target-a", Payload{Attempt: 1, State: domain.StateWaitingExternal, ProviderState: "absent", Evidence: evidence(`{"ref":"current"}`)}),
		event(17, EventLeaseReleased, "op-a", "target-a", Payload{Lease: third}),
		event(18, EventRetryAuthorized, "op-a", "target-a", Payload{Attempt: 1}),
	)
	state, err := Reduce(plan, events)
	if err != nil {
		t.Fatal(err)
	}
	op, _ := state.Operation("op-a")
	if op.State != domain.StateReady || op.Attempt != 1 || op.ReconcileRequired || op.Ambiguous {
		t.Fatalf("fresh absence did not authorize retry: %+v", op)
	}
}
