package journal

import (
	"bytes"
	"errors"
	"os"
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
		"wrong attempt":    {event(7, EventRetryAuthorized, "op-a", "target-a", Payload{Attempt: 2})},
		"after run cancel": {event(7, EventRunCancelled, "", "", Payload{}), event(8, EventRetryAuthorized, "op-a", "target-a", Payload{Attempt: 1})},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := Reduce(plan, append(append([]Event(nil), absent...), appended...)); err == nil {
				t.Fatal("unsafe authorization accepted")
			}
		})
	}
}

func TestNewAuthorizationCannotAppend(t *testing.T) {
	plan := npmRetryPlan(t)
	events := ambiguousNpmEvents()
	events[5].Payload.ProviderState = "absent"
	path := filepath.Join(t.TempDir(), "run.journal")
	writer, err := OpenWriter(path, runID())
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	for _, e := range events {
		entry := Entry{RunID: e.RunID, PlanID: e.PlanID, Type: e.Type, OperationID: e.OperationID, TargetID: e.TargetID, Payload: e.Payload}
		if _, err := writer.Append(entry); err != nil {
			t.Fatal(err)
		}
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	entry := Entry{RunID: runID(), Type: EventRetryAuthorized, OperationID: "op-a", TargetID: "target-a", Payload: Payload{Attempt: 1}}
	if _, err := writer.Append(entry); !errors.Is(err, ErrRetryAuthorizationDisabled) {
		t.Fatalf("append authorization: %v", err)
	}
	if _, err := writer.AppendWithLease(entry, *leasePayload("lease-a", "worker", 2, 10)); !errors.Is(err, ErrRetryAuthorizationDisabled) {
		t.Fatalf("leased authorization: %v", err)
	}
	after, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(before, after) || len(writer.Events()) != len(events) {
		t.Fatalf("refusal mutated journal: %v", err)
	}
	state, err := Reduce(plan, writer.Events())
	if err != nil || state.CheckExecutionAllowed() != nil {
		t.Fatalf("ordinary ambiguity became unexecutable: %v", err)
	}
	op, _ := state.Operation("op-a")
	if !op.ReconcileRequired || !op.Ambiguous {
		t.Fatalf("observation altered: %+v", op)
	}
}

func TestHistoricalAuthorizationReplayAndExecutionGuard(t *testing.T) {
	plan := npmRetryPlan(t)
	events := ambiguousNpmEvents()
	events[5].Payload.ProviderState = "absent"
	events = append(events,
		event(7, EventRetryAuthorized, "op-a", "target-a", Payload{Attempt: 1}),
		attemptStarted(8, "op-a", "target-a", 2),
		event(9, EventProviderProcessStarted, "op-a", "target-a", Payload{Attempt: 2}),
		dispatched(10, "op-a", "target-a", 2),
		event(11, EventOutcomeAmbiguous, "op-a", "target-a", Payload{Attempt: 2, ErrorCode: "AMBIGUOUS_OUTCOME"}),
		reconcileStarted(12, "op-a", "target-a", 2),
		reconcileResult(13, "op-a", "target-a", 2, domain.StatePublished),
		event(14, EventRunCompleted, "", "", Payload{}),
	)
	var buf bytes.Buffer
	for i, e := range events {
		frame, err := encodeFrame(e)
		if err != nil {
			t.Fatal(err)
		}
		buf.Write(frame)
		read, err := Read(bytes.NewReader(buf.Bytes()))
		if err != nil || len(read.Events) != i+1 {
			t.Fatalf("prefix %d: %v", i+1, err)
		}
		state, err := Reduce(plan, read.Events)
		if err != nil {
			t.Fatalf("prefix %d: %v", i+1, err)
		}
		wantBlocked := i >= 6 && i < 9
		if got := state.CheckExecutionAllowed(); (got != nil) != wantBlocked || (wantBlocked && !errors.Is(got, ErrLegacyRetryPending)) {
			t.Fatalf("prefix %d: execution=%v want blocked=%t", i+1, got, wantBlocked)
		}
	}
}
