package journal

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/aalsanie/distroplane/internal/domain"
)

func TestEntryValidation(t *testing.T) {
	validOp := opID("op-a")
	validTarget := targetID("target-a")
	cases := []struct {
		name    string
		entry   Entry
		wantErr bool
	}{
		{"run started", Entry{RunID: runID(), Type: EventRunStarted}, false},
		{"operation ready", Entry{RunID: runID(), Type: EventOperationReady, OperationID: validOp, TargetID: validTarget}, false},
		{"lease acquired", Entry{RunID: runID(), Type: EventLeaseAcquired, OperationID: validOp, TargetID: validTarget, Payload: Payload{Lease: leasePayload("lease-a", "worker-a", 1, 10)}}, false},
		{"lease renewed", Entry{RunID: runID(), Type: EventLeaseRenewed, OperationID: validOp, TargetID: validTarget, Payload: Payload{Lease: leasePayload("lease-a", "worker-a", 1, 20)}}, false},
		{"lease expired", Entry{RunID: runID(), Type: EventLeaseExpired, OperationID: validOp, TargetID: validTarget, Payload: Payload{Lease: leasePayload("lease-a", "worker-a", 1, 10)}}, false},
		{"lease released", Entry{RunID: runID(), Type: EventLeaseReleased, OperationID: validOp, TargetID: validTarget, Payload: Payload{Lease: leasePayload("lease-a", "worker-a", 1, 10)}}, false},
		{"attempt started", Entry{RunID: runID(), Type: EventAttemptStarted, OperationID: validOp, TargetID: validTarget, Payload: Payload{Attempt: 1, AttemptReason: AttemptReasonInitial}}, false},
		{"retry started", Entry{RunID: runID(), Type: EventAttemptStarted, OperationID: validOp, TargetID: validTarget, Payload: Payload{Attempt: 2, AttemptReason: AttemptReasonRetry}}, false},
		{"provider process started", Entry{RunID: runID(), Type: EventProviderProcessStarted, OperationID: validOp, TargetID: validTarget, Payload: Payload{Attempt: 1}}, false},
		{"dispatched", Entry{RunID: runID(), Type: EventSideEffectDispatched, OperationID: validOp, TargetID: validTarget, Payload: Payload{Attempt: 1}}, false},
		{"provider response received", Entry{RunID: runID(), Type: EventProviderResponseReceived, OperationID: validOp, TargetID: validTarget, Payload: Payload{Attempt: 1}}, false},
		{"waiting external", Entry{RunID: runID(), Type: EventOperationWaitingExternal, OperationID: validOp, TargetID: validTarget, Payload: Payload{Attempt: 1, ProviderState: "pending"}}, false},
		{"published", Entry{RunID: runID(), Type: EventOperationPublished, OperationID: validOp, TargetID: validTarget, Payload: Payload{Attempt: 1, ProviderState: "published", Evidence: evidence(`{"ref":"x"}`), ResultCategory: ResultCategoryPublished}}, false},
		{"rejected", Entry{RunID: runID(), Type: EventOperationRejected, OperationID: validOp, TargetID: validTarget, Payload: Payload{Attempt: 1, ErrorCode: "REJECTED"}}, false},
		{"failed", Entry{RunID: runID(), Type: EventOperationFailed, OperationID: validOp, TargetID: validTarget, Payload: Payload{Attempt: 1, ErrorCode: "TRANSIENT", Retryable: true}}, false},
		{"ambiguous", Entry{RunID: runID(), Type: EventOutcomeAmbiguous, OperationID: validOp, TargetID: validTarget, Payload: Payload{Attempt: 1, ErrorCode: "AMBIGUOUS_OUTCOME", ResultCategory: ResultCategoryAmbiguous}}, false},
		{"result", Entry{RunID: runID(), Type: EventOperationResult, OperationID: validOp, TargetID: validTarget, Payload: Payload{Attempt: 1, State: domain.StatePublished, ProviderState: "published", Evidence: evidence(`{"ref":"x"}`)}}, false},
		{"reconcile started", Entry{RunID: runID(), Type: EventReconcileStarted, OperationID: validOp, TargetID: validTarget, Payload: Payload{Attempt: 1, AttemptReason: AttemptReasonReconcile}}, false},
		{"reconcile result", Entry{RunID: runID(), Type: EventReconcileResult, OperationID: validOp, TargetID: validTarget, Payload: Payload{Attempt: 1, State: domain.StateWaitingExternal}}, false},
		{"bad run", Entry{Type: EventRunStarted}, true},
		{"unknown type", Entry{RunID: runID(), Type: "FUTURE"}, true},
		{"run started payload", Entry{RunID: runID(), Type: EventRunStarted, Payload: Payload{Attempt: 1}}, true},
		{"missing operation", Entry{RunID: runID(), Type: EventAttemptStarted, TargetID: validTarget, Payload: Payload{Attempt: 1}}, true},
		{"missing target", Entry{RunID: runID(), Type: EventAttemptStarted, OperationID: validOp, Payload: Payload{Attempt: 1}}, true},
		{"missing attempt", Entry{RunID: runID(), Type: EventAttemptStarted, OperationID: validOp, TargetID: validTarget}, true},
		{"ready payload", Entry{RunID: runID(), Type: EventOperationReady, OperationID: validOp, TargetID: validTarget, Payload: Payload{Attempt: 1}}, true},
		{"lease metadata missing", Entry{RunID: runID(), Type: EventLeaseAcquired, OperationID: validOp, TargetID: validTarget}, true},
		{"lease metadata invalid expiry", Entry{RunID: runID(), Type: EventLeaseRenewed, OperationID: validOp, TargetID: validTarget, Payload: Payload{Lease: leasePayload("lease-a", "worker-a", 10, 10)}}, true},
		{"lease mixed payload", Entry{RunID: runID(), Type: EventLeaseReleased, OperationID: validOp, TargetID: validTarget, Payload: Payload{Attempt: 1, Lease: leasePayload("lease-a", "worker-a", 1, 10)}}, true},
		{"lease metadata on result", Entry{RunID: runID(), Type: EventOperationPublished, OperationID: validOp, TargetID: validTarget, Payload: Payload{Attempt: 1, Lease: leasePayload("lease-a", "worker-a", 1, 10)}}, true},
		{"provider response missing attempt", Entry{RunID: runID(), Type: EventProviderResponseReceived, OperationID: validOp, TargetID: validTarget}, true},
		{"explicit result carries state", Entry{RunID: runID(), Type: EventOperationPublished, OperationID: validOp, TargetID: validTarget, Payload: Payload{Attempt: 1, State: domain.StatePublished}}, true},
		{"explicit result credential", Entry{RunID: runID(), Type: EventOperationFailed, OperationID: validOp, TargetID: validTarget, Payload: Payload{Attempt: 1, CredentialRef: domain.CredentialRef("credential-a")}}, true},
		{"explicit result missing attempt", Entry{RunID: runID(), Type: EventOperationFailed, OperationID: validOp, TargetID: validTarget}, true},
		{"retryable published", Entry{RunID: runID(), Type: EventOperationPublished, OperationID: validOp, TargetID: validTarget, Payload: Payload{Attempt: 1, Retryable: true}}, true},
		{"unsupported attempt payload", Entry{RunID: runID(), Type: EventAttemptStarted, OperationID: validOp, TargetID: validTarget, Payload: Payload{Attempt: 1, State: domain.StateFailed}}, true},
		{"error code on start", Entry{RunID: runID(), Type: EventAttemptStarted, OperationID: validOp, TargetID: validTarget, Payload: Payload{Attempt: 1, ErrorCode: "X"}}, true},
		{"unknown attempt reason", Entry{RunID: runID(), Type: EventAttemptStarted, OperationID: validOp, TargetID: validTarget, Payload: Payload{Attempt: 1, AttemptReason: "UNKNOWN"}}, true},
		{"result category on start", Entry{RunID: runID(), Type: EventAttemptStarted, OperationID: validOp, TargetID: validTarget, Payload: Payload{Attempt: 1, ResultCategory: ResultCategoryPublished}}, true},
		{"attempt reason on result", Entry{RunID: runID(), Type: EventOperationPublished, OperationID: validOp, TargetID: validTarget, Payload: Payload{Attempt: 1, AttemptReason: AttemptReasonInitial}}, true},
		{"unknown result category", Entry{RunID: runID(), Type: EventOperationPublished, OperationID: validOp, TargetID: validTarget, Payload: Payload{Attempt: 1, ResultCategory: "UNKNOWN"}}, true},
		{"invalid result state", Entry{RunID: runID(), Type: EventOperationResult, OperationID: validOp, TargetID: validTarget, Payload: Payload{Attempt: 1, State: domain.StateRunning}}, true},
		{"bad provider state", Entry{RunID: runID(), Type: EventOperationResult, OperationID: validOp, TargetID: validTarget, Payload: Payload{Attempt: 1, State: domain.StateFailed, ProviderState: " bad"}}, true},
		{"bad error code", Entry{RunID: runID(), Type: EventOperationResult, OperationID: validOp, TargetID: validTarget, Payload: Payload{Attempt: 1, State: domain.StateFailed, ErrorCode: " bad"}}, true},
		{"bad evidence", Entry{RunID: runID(), Type: EventOperationResult, OperationID: validOp, TargetID: validTarget, Payload: Payload{Attempt: 1, State: domain.StateFailed, Evidence: evidence(`{`)}}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.entry.validate()
			if (err != nil) != tc.wantErr {
				t.Fatalf("err=%v wantErr=%v", err, tc.wantErr)
			}
		})
	}
}

func TestExplicitResultStates(t *testing.T) {
	cases := map[EventType]domain.NormalizedState{
		EventOperationWaitingExternal: domain.StateWaitingExternal,
		EventOperationPublished:       domain.StatePublished,
		EventOperationRejected:        domain.StateRejected,
		EventOperationFailed:          domain.StateFailed,
	}
	for eventType, want := range cases {
		got, ok := explicitResultState(eventType)
		if !ok || got != want {
			t.Fatalf("event=%s state=%s ok=%v want=%s", eventType, got, ok, want)
		}
	}
	if state, ok := explicitResultState(EventRunStarted); ok || state != "" {
		t.Fatalf("unexpected explicit state=%s ok=%v", state, ok)
	}
}

func TestEvidenceRejectsSecretFields(t *testing.T) {
	keys := []string{"token", "access_token", "Refresh-Token", "password", "passwd", "secret", "credential", "credentials", "Authorization", "api_key", "clientSecret", "private_key"}
	for _, key := range keys {
		raw, _ := json.Marshal(map[string]any{"nested": []any{map[string]any{key: "super-secret-value"}}})
		entry := Entry{RunID: runID(), Type: EventOperationResult, OperationID: opID("op-a"), TargetID: targetID("target-a"), Payload: Payload{Attempt: 1, State: domain.StateFailed, Evidence: raw}}
		if err := entry.validate(); err == nil {
			t.Fatalf("secret key %q accepted", key)
		}
	}
	entry := Entry{RunID: runID(), Type: EventOperationResult, OperationID: opID("op-a"), TargetID: targetID("target-a"), Payload: Payload{Attempt: 1, State: domain.StatePublished, Evidence: evidence(`{"reference":"token-shaped-but-not-secret"}`)}}
	if err := entry.validate(); err != nil {
		t.Fatal(err)
	}
}

func TestEventValidation(t *testing.T) {
	base := runStarted(1)
	if err := base.validate(); err != nil {
		t.Fatal(err)
	}
	variants := []Event{base, base, base}
	variants[0].SchemaVersion = 2
	variants[1].Sequence = 0
	variants[2].ObservedAt = variants[2].ObservedAt.Add(-variants[2].ObservedAt.Sub(variants[2].ObservedAt))
	variants[2].ObservedAt = variants[2].ObservedAt.Truncate(0)
	variants[2].ObservedAt = base.ObservedAt
	variants = append(variants, Event{SchemaVersion: 1, Sequence: 1, RunID: runID(), Type: EventRunStarted})
	for i, candidate := range variants {
		err := candidate.validate()
		if i < 2 && err == nil {
			t.Fatalf("variant %d accepted", i)
		}
		if i == 3 && err == nil {
			t.Fatal("zero timestamp accepted")
		}
	}
	if !errors.Is(variants[0].validate(), ErrUnsupportedVersion) {
		t.Fatal("version error not classified")
	}
	if !errors.Is(variants[1].validate(), ErrSequence) {
		t.Fatal("sequence error not classified")
	}
}

func TestEncodeFrameBoundsAndRoundTrip(t *testing.T) {
	e := event(1, EventOperationResult, "op-a", "target-a", Payload{Attempt: 1, State: domain.StatePublished, Evidence: evidence(`{"b":2,"a":1}`)})
	frame, err := encodeFrame(e)
	if err != nil {
		t.Fatal(err)
	}
	read, err := Read(strings.NewReader(string(frame)))
	if err != nil {
		t.Fatal(err)
	}
	if len(read.Events) != 1 || read.Events[0].Type != EventOperationResult {
		t.Fatalf("read=%+v", read)
	}

	large := e
	large.Payload.Evidence = json.RawMessage(`{"data":"` + strings.Repeat("x", MaxRecordBytes) + `"}`)
	if _, err := encodeFrame(large); err == nil {
		t.Fatal("oversized record accepted")
	}
}

func TestValidateTextBoundaries(t *testing.T) {
	if err := validateText("optional", "", true); err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{"", " leading", "trailing ", "line\nbreak", strings.Repeat("x", 257), string([]byte{0xff})} {
		if err := validateText("value", value, false); err == nil {
			t.Fatalf("accepted %q", value)
		}
	}
	if err := validateText("value", strings.Repeat("x", 256), false); err != nil {
		t.Fatal(err)
	}
}

func TestLeasePayloadValidationBranches(t *testing.T) {
	now := time.Unix(10, 0).UTC()
	cases := []struct {
		name  string
		lease LeasePayload
	}{
		{"missing id", LeasePayload{Owner: "worker-a", AcquiredAt: now, ExpiresAt: now.Add(time.Second)}},
		{"missing owner", LeasePayload{ID: "lease-a", AcquiredAt: now, ExpiresAt: now.Add(time.Second)}},
		{"missing acquired", LeasePayload{ID: "lease-a", Owner: "worker-a", ExpiresAt: now.Add(time.Second)}},
		{"missing expiry", LeasePayload{ID: "lease-a", Owner: "worker-a", AcquiredAt: now}},
		{"expiry before acquisition", LeasePayload{ID: "lease-a", Owner: "worker-a", AcquiredAt: now, ExpiresAt: now.Add(-time.Second)}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.lease.validate(); err == nil {
				t.Fatalf("lease=%+v accepted", tc.lease)
			}
		})
	}
	if err := (LeasePayload{ID: "lease-a", Owner: "worker-a", AcquiredAt: now, ExpiresAt: now.Add(time.Second)}).validate(); err != nil {
		t.Fatal(err)
	}
}

func TestAttemptReasonAndResultCategoryValidation(t *testing.T) {
	for _, value := range []string{AttemptReasonInitial, AttemptReasonRetry, AttemptReasonReconcile} {
		if !validAttemptReason(value) {
			t.Fatalf("attempt reason %q rejected", value)
		}
	}
	if validAttemptReason("UNKNOWN") {
		t.Fatal("unknown attempt reason accepted")
	}

	for _, value := range []string{
		ResultCategoryPublished,
		ResultCategoryWaitingExternal,
		ResultCategoryRejected,
		ResultCategoryFailedRetryable,
		ResultCategoryFailedPermanent,
		ResultCategoryCancelled,
		ResultCategoryAmbiguous,
	} {
		if !validResultCategory(value) {
			t.Fatalf("result category %q rejected", value)
		}
	}
	if validResultCategory("UNKNOWN") {
		t.Fatal("unknown result category accepted")
	}
}


func TestCloneEventCopiesLeaseMetadata(t *testing.T) {
	original := event(1, EventLeaseAcquired, "op-a", "target-a", Payload{
		Lease: leasePayload("lease-a", "worker-a", 1, 10),
	})
	cloned := cloneEvent(original)
	if cloned.Payload.Lease == original.Payload.Lease {
		t.Fatal("lease payload pointer was not copied")
	}
	cloned.Payload.Lease.ID = "changed"
	if original.Payload.Lease.ID != "lease-a" {
		t.Fatalf("original lease mutated: %+v", original.Payload.Lease)
	}

	events := cloneEvents([]Event{original})
	if len(events) != 1 || events[0].Payload.Lease == original.Payload.Lease {
		t.Fatalf("events=%+v", events)
	}
}
