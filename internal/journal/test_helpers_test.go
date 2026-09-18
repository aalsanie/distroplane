package journal

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/aalsanie/distroplane/internal/domain"
)

func testPlan(t testing.TB) domain.Plan {
	t.Helper()
	digest, err := domain.NewSHA256Digest(strings.Repeat("a", 64))
	if err != nil {
		t.Fatal(err)
	}
	artifact, err := domain.NewArtifact("app", "/tmp/app", digest, 1, "application/octet-stream")
	if err != nil {
		t.Fatal(err)
	}
	releaseID, _ := domain.NewReleaseID("release-1")
	release, err := domain.NewRelease(releaseID, []domain.Artifact{artifact})
	if err != nil {
		t.Fatal(err)
	}
	providerName, _ := domain.NewProviderName("fake")
	providerVersion, _ := domain.NewProviderVersion("1")
	provider, _ := domain.NewProviderRef(providerName, providerVersion)
	configuration, _ := domain.NewJSONValue([]byte(`{}`))
	t1ID, _ := domain.NewTargetID("target-a")
	t2ID, _ := domain.NewTargetID("target-b")
	t3ID, _ := domain.NewTargetID("target-noop")
	t1, _ := domain.NewTarget(t1ID, provider, configuration)
	t2, _ := domain.NewTarget(t2ID, provider, configuration)
	t3, _ := domain.NewTarget(t3ID, provider, configuration)
	payload, _ := domain.NewJSONValue([]byte(`{}`))
	op1ID, _ := domain.NewOperationID("op-a")
	op2ID, _ := domain.NewOperationID("op-b")
	op3ID, _ := domain.NewOperationID("op-c")
	op1, err := domain.NewOperation(op1ID, t1ID, provider, "publish", nil, true, "key-a", time.Second, payload)
	if err != nil {
		t.Fatal(err)
	}
	op2, err := domain.NewOperation(op2ID, t1ID, provider, "verify", []domain.OperationID{op1ID}, false, "", time.Second, payload)
	if err != nil {
		t.Fatal(err)
	}
	op3, err := domain.NewOperation(op3ID, t2ID, provider, "publish", nil, true, "key-c", time.Second, payload)
	if err != nil {
		t.Fatal(err)
	}
	planID, _ := domain.NewPlanID("plan-1")
	plan, err := domain.NewPlan(planID, "1", "1", release, []domain.Target{t1, t2, t3}, []domain.Operation{op1, op2, op3})
	if err != nil {
		t.Fatal(err)
	}
	return plan
}

func runID() domain.RunID {
	id, _ := domain.NewRunID("run-1")
	return id
}

func opID(value string) domain.OperationID  { id, _ := domain.NewOperationID(value); return id }
func targetID(value string) domain.TargetID { id, _ := domain.NewTargetID(value); return id }

func event(seq uint64, typ EventType, op, target string, payload Payload) Event {
	return Event{
		SchemaVersion: SchemaVersion,
		Sequence:      seq,
		RunID:         runID(),
		Type:          typ,
		OperationID:   domain.OperationID(op),
		TargetID:      domain.TargetID(target),
		ObservedAt:    time.Unix(int64(seq), 0).UTC(),
		Payload:       payload,
	}
}

func runStarted(seq uint64) Event { return event(seq, EventRunStarted, "", "", Payload{}) }
func attemptStarted(seq uint64, op, target string, attempt uint32) Event {
	return event(seq, EventAttemptStarted, op, target, Payload{Attempt: attempt})
}
func dispatched(seq uint64, op, target string, attempt uint32) Event {
	return event(seq, EventSideEffectDispatched, op, target, Payload{Attempt: attempt})
}
func result(seq uint64, op, target string, attempt uint32, state domain.NormalizedState) Event {
	return event(seq, EventOperationResult, op, target, Payload{Attempt: attempt, State: state})
}
func reconcileStarted(seq uint64, op, target string, attempt uint32) Event {
	return event(seq, EventReconcileStarted, op, target, Payload{Attempt: attempt})
}
func reconcileResult(seq uint64, op, target string, attempt uint32, state domain.NormalizedState) Event {
	return event(seq, EventReconcileResult, op, target, Payload{Attempt: attempt, State: state})
}

func mustFrame(t testing.TB, e Event) []byte {
	t.Helper()
	frame, err := encodeFrame(e)
	if err != nil {
		t.Fatal(err)
	}
	return frame
}

func evidence(value string) json.RawMessage { return json.RawMessage(value) }
