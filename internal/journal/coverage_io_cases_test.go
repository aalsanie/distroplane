package journal

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/aalsanie/distroplane/internal/domain"
)

type interruptedJournalReader struct{}

func (interruptedJournalReader) Read([]byte) (int, error) {
	return 0, errors.New("storage returned an I/O failure")
}

func TestJournalReadDistinguishesIOFailureFromTruncatedTail(t *testing.T) {
	if _, err := Read(interruptedJournalReader{}); err == nil {
		t.Fatal("I/O failure mistaken for clean journal EOF")
	}
	event := Event{SchemaVersion: SchemaVersion, Sequence: 1,
		RunID: domain.RunID("run-storage"), PlanID: domain.PlanID("plan-storage"),
		Type: EventRunStarted, ObservedAt: time.Now().UTC()}
	frame, err := encodeFrame(event)
	if err != nil {
		t.Fatal(err)
	}
	failingBody := io.MultiReader(bytes.NewReader(frame[:frameHeaderBytes]), interruptedJournalReader{})
	if _, err := Read(failingBody); err == nil {
		t.Fatal("record body I/O error was ignored")
	}
	truncated := frame[:len(frame)-2]
	if got, err := Read(bytes.NewReader(truncated)); err != nil || !got.TruncatedTail {
		t.Fatalf("truncated record was not recoverable: %+v err=%v", got, err)
	}
	raw, err := json.Marshal(event)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := decodeEvent(append(raw, []byte(" {}")...)); err == nil {
		t.Fatal("trailing journal JSON accepted")
	}
	if _, err := decodeEvent([]byte(`{"unsupported":true}`)); err == nil {
		t.Fatal("unknown journal fields accepted")
	}
}

func TestOversizedJournalEvidenceCannotBeFramed(t *testing.T) {
	evidence := json.RawMessage(`"` + strings.Repeat("x", MaxRecordBytes) + `"`)
	event := Event{SchemaVersion: SchemaVersion, Sequence: 1,
		RunID: domain.RunID("run-large"), Type: EventOperationResult,
		TargetID: domain.TargetID("target"), OperationID: domain.OperationID("operation"),
		ObservedAt: time.Now().UTC(), Payload: Payload{
			Attempt: 1, State: domain.StatePublished, Evidence: evidence,
		}}
	if _, err := encodeFrame(event); err == nil {
		t.Fatal("oversized durable record was accepted")
	}
}
