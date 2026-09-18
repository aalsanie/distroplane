package journal

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

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
		{"attempt started", Entry{RunID: runID(), Type: EventAttemptStarted, OperationID: validOp, TargetID: validTarget, Payload: Payload{Attempt: 1}}, false},
		{"dispatched", Entry{RunID: runID(), Type: EventSideEffectDispatched, OperationID: validOp, TargetID: validTarget, Payload: Payload{Attempt: 1}}, false},
		{"ambiguous", Entry{RunID: runID(), Type: EventOutcomeAmbiguous, OperationID: validOp, TargetID: validTarget, Payload: Payload{Attempt: 1, ErrorCode: "AMBIGUOUS_OUTCOME"}}, false},
		{"result", Entry{RunID: runID(), Type: EventOperationResult, OperationID: validOp, TargetID: validTarget, Payload: Payload{Attempt: 1, State: domain.StatePublished, ProviderState: "published", Evidence: evidence(`{"ref":"x"}`)}}, false},
		{"reconcile started", Entry{RunID: runID(), Type: EventReconcileStarted, OperationID: validOp, TargetID: validTarget, Payload: Payload{Attempt: 1}}, false},
		{"reconcile result", Entry{RunID: runID(), Type: EventReconcileResult, OperationID: validOp, TargetID: validTarget, Payload: Payload{Attempt: 1, State: domain.StateWaitingExternal}}, false},
		{"bad run", Entry{Type: EventRunStarted}, true},
		{"unknown type", Entry{RunID: runID(), Type: "FUTURE"}, true},
		{"run started payload", Entry{RunID: runID(), Type: EventRunStarted, Payload: Payload{Attempt: 1}}, true},
		{"missing operation", Entry{RunID: runID(), Type: EventAttemptStarted, TargetID: validTarget, Payload: Payload{Attempt: 1}}, true},
		{"missing target", Entry{RunID: runID(), Type: EventAttemptStarted, OperationID: validOp, Payload: Payload{Attempt: 1}}, true},
		{"missing attempt", Entry{RunID: runID(), Type: EventAttemptStarted, OperationID: validOp, TargetID: validTarget}, true},
		{"unsupported attempt payload", Entry{RunID: runID(), Type: EventAttemptStarted, OperationID: validOp, TargetID: validTarget, Payload: Payload{Attempt: 1, State: domain.StateFailed}}, true},
		{"error code on start", Entry{RunID: runID(), Type: EventAttemptStarted, OperationID: validOp, TargetID: validTarget, Payload: Payload{Attempt: 1, ErrorCode: "X"}}, true},
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
