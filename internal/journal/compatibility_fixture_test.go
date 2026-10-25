package journal

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/aalsanie/distroplane/internal/domain"
)

// These checked-in journal events pin the v2 replay contract across CLI releases.
// They deliberately exercise the public framing and reduction paths, not mocked state.
func TestStableJournalV2ReplayFixtures(t *testing.T) {
	paths, err := filepath.Glob(filepath.Join("testdata", "compatibility", "journal-v2", "*.json"))
	if err != nil || len(paths) != 7 {
		t.Fatalf("journal fixtures: files=%d err=%v", len(paths), err)
	}
	for _, path := range paths {
		t.Run(filepath.Base(path), func(t *testing.T) {
			raw, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			var fixture struct {
				Events []Event `json:"events"`
				Expected struct {
					State             domain.NormalizedState `json:"state"`
					Attempt           uint32 `json:"attempt"`
					ReconcileRequired bool `json:"reconcileRequired"`
					Ambiguous         bool `json:"ambiguous"`
					BlockedAttempts   uint32 `json:"blockedAttempts"`
				} `json:"expected"`
			}
			if err := json.Unmarshal(raw, &fixture); err != nil {
				t.Fatal(err)
			}
			if len(fixture.Events) < 2 {
				t.Fatal("fixture has no meaningful history")
			}
			var framed []byte
			for _, record := range fixture.Events {
				if record.SchemaVersion != SchemaVersion {
					t.Fatalf("unexpected schema version %d", record.SchemaVersion)
				}
				framed = append(framed, mustFrame(t, record)...)
			}
			read, err := Read(bytes.NewReader(framed))
			if err != nil {
				t.Fatal(err)
			}
			if read.TruncatedTail || read.ValidBytes != int64(len(framed)) || len(read.Events) != len(fixture.Events) {
				t.Fatalf("journal framing changed: %+v", read)
			}
			plan := testPlan(t)
			state, err := Reduce(plan, read.Events)
			if err != nil {
				t.Fatal(err)
			}
			operation, ok := state.Operation(opID("op-c"))
			if !ok || operation.State != fixture.Expected.State ||
				operation.Attempt != fixture.Expected.Attempt ||
				operation.ReconcileRequired != fixture.Expected.ReconcileRequired ||
				operation.Ambiguous != fixture.Expected.Ambiguous ||
				operation.BlockedAttempts != fixture.Expected.BlockedAttempts {
				t.Fatalf("replay mismatch: %+v (expected %+v)", operation, fixture.Expected)
			}
			// Truncated final records must not erase the durable prefix.
			if len(fixture.Events) > 1 {
				withTail := append(append([]byte(nil), framed...), []byte("DP")...)
				partial, err := Read(bytes.NewReader(withTail))
				if err != nil || !partial.TruncatedTail || len(partial.Events) != len(read.Events) {
					t.Fatalf("truncated tail recovery changed: %+v err=%v", partial, err)
				}
			}
			// A historical journal cannot be replayed against another plan identity.
			mutated := append([]Event(nil), read.Events...)
			mutated[0].PlanID = "different-plan"
			if _, err := Reduce(plan, mutated); !errors.Is(err, ErrPlanMismatch) {
				t.Fatalf("cross-plan replay accepted: %v", err)
			}
		})
	}
}

func TestHistoricalJournalV1RemainsUnsupported(t *testing.T) {
	value := runStarted(1)
	value.SchemaVersion = 1
	if _, err := Read(bytes.NewReader(rawFrame(t, value))); !errors.Is(err, ErrUnsupportedVersion) {
		t.Fatalf("journal v1 must be rejected explicitly: %v", err)
	}
}
