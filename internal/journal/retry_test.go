package journal

import (
	"bytes"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/aalsanie/distroplane/internal/domain"
)

func npmRetryPlan(t *testing.T) domain.Plan {
	t.Helper()
	base := testPlan(t)
	name, _ := domain.NewProviderName("npm")
	version, _ := domain.NewProviderVersion("1")
	provider, err := domain.NewProviderRef(name, version)
	if err != nil {
		t.Fatal(err)
	}
	configuration, _ := domain.NewJSONValue([]byte(`{}`))
	targetID, _ := domain.NewTargetID("target-a")
	target, err := domain.NewTarget(targetID, provider, configuration)
	if err != nil {
		t.Fatal(err)
	}
	payload, _ := domain.NewJSONValue([]byte(`{}`))
	operationID, _ := domain.NewOperationID("op-a")
	operation, err := domain.NewOperation(operationID, targetID, provider, "publish", nil, true, "key-a", time.Second, payload)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := domain.NewPlan(base.ID(), base.SchemaVersion(), base.ProtocolVersion(), base.Release(), []domain.Target{target}, []domain.Operation{operation})
	if err != nil {
		t.Fatal(err)
	}
	return plan
}

func ambiguousNpmEvents() []Event {
	return []Event{
		runStarted(1),
		attemptStarted(2, "op-a", "target-a", 1),
		dispatched(3, "op-a", "target-a", 1),
		event(4, EventOutcomeAmbiguous, "op-a", "target-a", Payload{Attempt: 1, ErrorCode: "AMBIGUOUS_OUTCOME"}),
		reconcileStarted(5, "op-a", "target-a", 1),
		reconcileResult(6, "op-a", "target-a", 1, domain.StateWaitingExternal),
	}
}

func TestRetryAuthorizationOneShotAfterReconciledNpmAbsence(t *testing.T) {
	plan := npmRetryPlan(t)
	events := ambiguousNpmEvents()
	events[5].Payload.ProviderState = "absent"
	before, err := Reduce(plan, events)
	if err != nil {
		t.Fatal(err)
	}
	op, _ := before.Operation("op-a")
	if !op.ReconcileRequired || !op.Ambiguous || op.State != domain.StateWaitingExternal {
		t.Fatalf("reconciled state=%+v", op)
	}
	events = append(events,
		event(7, EventRetryAuthorized, "op-a", "target-a", Payload{Attempt: 1}),
		attemptStarted(8, "op-a", "target-a", 2),
		dispatched(9, "op-a", "target-a", 2),
		event(10, EventOperationPublished, "op-a", "target-a", Payload{Attempt: 2}),
		event(11, EventRunCompleted, "", "", Payload{}),
	)
	state, err := Reduce(plan, events)
	if err != nil || !state.Completed {
		t.Fatalf("recovery state=%+v err=%v", state, err)
	}
	op, _ = state.Operation("op-a")
	if op.State != domain.StatePublished || op.Attempt != 2 {
		t.Fatalf("recovery operation=%+v", op)
	}
}

func TestRetryAuthorizationRejectsUnreconciledAndUnsafeOutcomes(t *testing.T) {
	plan := npmRetryPlan(t)
	absent := ambiguousNpmEvents()
	absent[5].Payload.ProviderState = "absent"
	var cases = map[string]struct {
		plan   domain.Plan
		events []Event
	}{
		"never reconciled":      {plan, absent[:4]},
		"reconciliation failed": {plan, ambiguousNpmEvents()},
		"non npm provider":      {testPlan(t), absent},
		"ordinary waiting": {plan, []Event{
			runStarted(1), attemptStarted(2, "op-a", "target-a", 1),
			dispatched(3, "op-a", "target-a", 1),
			event(4, EventOperationWaitingExternal, "op-a", "target-a", Payload{Attempt: 1, ProviderState: "absent"}),
		}},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			start := append([]Event(nil), tc.events...)
			if _, err := Reduce(tc.plan, start); err != nil {
				t.Fatalf("bad setup: %v", err)
			}
			seq := uint64(len(start) + 1)
			start = append(start, event(seq, EventRetryAuthorized, "op-a", "target-a", Payload{Attempt: 1}))
			if _, err := Reduce(tc.plan, start); err == nil {
				t.Fatal("unsafe retry authorization was accepted")
			}
		})
	}
	for name, appended := range map[string][]Event{
		"duplicate authorization": {
			event(7, EventRetryAuthorized, "op-a", "target-a", Payload{Attempt: 1}),
			event(8, EventRetryAuthorized, "op-a", "target-a", Payload{Attempt: 1}),
		},
		"wrong attempt": {event(7, EventRetryAuthorized, "op-a", "target-a", Payload{Attempt: 2})},
		"after run cancel": {event(7, EventRunCancelled, "", "", Payload{}), event(8, EventRetryAuthorized, "op-a", "target-a", Payload{Attempt: 1})},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := Reduce(plan, append(append([]Event(nil), absent...), appended...)); err == nil {
				t.Fatal("unsafe authorization accepted")
			}
		})
	}
}

func TestDurableOperatorAuthorizationValidatesBeforeAppend(t *testing.T) {
	plan := npmRetryPlan(t)
	path := filepath.Join(t.TempDir(), "run.journal")
	writer, err := OpenWriter(path, runID())
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	// Preload a valid stream, preserving the writer's actual timestamps and sequence.
	for _, e := range ambiguousNpmEvents() {
		if e.Type == EventReconcileResult {
			e.Payload.ProviderState = "absent"
		}
		entry := Entry{RunID: e.RunID, PlanID: e.PlanID, Type: e.Type, OperationID: e.OperationID, TargetID: e.TargetID, Payload: e.Payload}
		if _, err := writer.Append(entry); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := writer.AuthorizeAmbiguousRetry(plan, runID(), "missing"); err == nil {
		t.Fatal("unknown operation accepted")
	}
	if len(writer.Events()) != 6 {
		t.Fatal("failed authorization appended a record")
	}
	authorized, err := writer.AuthorizeAmbiguousRetry(plan, runID(), "op-a")
	if err != nil || authorized.Type != EventRetryAuthorized || authorized.Payload.Attempt != 1 {
		t.Fatalf("authorization=%+v err=%v", authorized, err)
	}
	if _, err := writer.AuthorizeAmbiguousRetry(plan, runID(), "op-a"); err == nil {
		t.Fatal("authorization reused without a subsequent reconciliation")
	}
	if len(writer.Events()) != 7 {
		t.Fatal("duplicate authorization appended a record")
	}
	var buf bytes.Buffer
	for _, e := range writer.Events() {
		frame, err := encodeFrame(e)
		if err != nil {
			t.Fatal(err)
		}
		buf.Write(frame)
	}
	read, err := Read(&buf)
	if err != nil || len(read.Events) != 7 {
		t.Fatalf("durable roundtrip err=%v events=%d", err, len(read.Events))
	}
	state, err := Reduce(plan, read.Events)
	if err != nil {
		t.Fatal(err)
	}
	op, _ := state.Operation("op-a")
	if op.State != domain.StateReady || op.ReconcileRequired || op.Ambiguous {
		t.Fatalf("authorized state=%+v", op)
	}
	other := testPlan(t)
	otherPlan, err := domain.NewPlan("plan-other", other.SchemaVersion(), other.ProtocolVersion(), other.Release(), other.Targets(), other.Operations())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := writer.AuthorizeAmbiguousRetry(otherPlan, runID(), "op-a"); !errors.Is(err, ErrPlanMismatch) {
		t.Fatalf("cross-plan authorization err=%v", err)
	}
}
