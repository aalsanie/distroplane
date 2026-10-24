package journal

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/aalsanie/distroplane/internal/domain"
)

func TestReduceRejectsCrossPlanReplayAfterPublication(t *testing.T) {
	original := testPlan(t)
	operation := original.Operations()[0]
	target := original.Targets()[0]
	first, err := domain.NewPlan(original.ID(), original.SchemaVersion(), original.ProtocolVersion(), original.Release(), []domain.Target{target}, []domain.Operation{operation})
	if err != nil {
		t.Fatal(err)
	}
	otherID, _ := domain.NewPlanID("plan-2")
	second, err := domain.NewPlan(otherID, first.SchemaVersion(), first.ProtocolVersion(), first.Release(), first.Targets(), first.Operations())
	if err != nil {
		t.Fatal(err)
	}
	// All target and operation IDs are identical; only the immutable plan identity differs.
	events := []Event{
		runStarted(1),
		attemptStarted(2, "op-a", "target-a", 1),
		dispatched(3, "op-a", "target-a", 1),
		event(4, EventOperationPublished, "op-a", "target-a", Payload{Attempt: 1}),
		event(5, EventRunCompleted, "", "", Payload{}),
	}
	state, err := Reduce(first, events)
	if err != nil || !state.Completed {
		t.Fatalf("original replay: state=%+v err=%v", state, err)
	}
	if _, err := Reduce(second, events); !errors.Is(err, ErrPlanMismatch) {
		t.Fatalf("different plan accepted completed publication: %v", err)
	}
}

func TestReadRejectsLegacyAndUnboundJournals(t *testing.T) {
	legacy := runStarted(1)
	legacy.SchemaVersion = 1
	legacy.PlanID = ""
	if _, err := Read(bytes.NewReader(rawFrame(t, legacy))); !errors.Is(err, ErrUnsupportedVersion) {
		t.Fatalf("legacy journal err=%v", err)
	}
	unbound := runStarted(1)
	unbound.PlanID = ""
	if _, err := Read(bytes.NewReader(rawFrame(t, unbound))); !errors.Is(err, ErrUnboundJournal) {
		t.Fatalf("unbound journal err=%v", err)
	}
}

func TestWriterRequiresAndPersistsPlanIdentity(t *testing.T) {
	path := filepath.Join(t.TempDir(), "run.journal")
	writer, err := OpenWriter(path, runID())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Append(Entry{RunID: runID(), Type: EventRunStarted}); !errors.Is(err, ErrUnboundJournal) {
		t.Fatalf("unbound start err=%v", err)
	}
	if len(writer.Events()) != 0 {
		t.Fatal("invalid start was journaled")
	}
	planID := testPlan(t).ID()
	if _, err := writer.Append(Entry{RunID: runID(), Type: EventRunStarted, PlanID: planID}); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := Read(bytes.NewReader(data))
	if err != nil || len(decoded.Events) != 1 || decoded.Events[0].PlanID != planID {
		t.Fatalf("durable plan binding: events=%+v err=%v", decoded.Events, err)
	}
}
