package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/aalsanie/distroplane/internal/config"
	"github.com/aalsanie/distroplane/internal/domain"
	"github.com/aalsanie/distroplane/internal/evidence"
	"github.com/aalsanie/distroplane/internal/journal"
	"github.com/aalsanie/distroplane/internal/planner"
	"github.com/aalsanie/distroplane/internal/protocol"
)

func TestFrozenV1CompatibilityContract(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "protocol", "schema", "v1", "compatibility.json"))
	if err != nil {
		t.Fatal(err)
	}
	var manifest struct {
		Status          string `json:"status"`
		ProtocolVersion string `json:"protocolVersion"`
		SchemaVersions  struct {
			Config    string `json:"config"`
			Plan      string `json:"plan"`
			Evidence  string `json:"evidence"`
			Journal   uint16 `json:"journal"`
			CLIOutput string `json:"cliOutput"`
		} `json:"schemaVersions"`
		NormalizedStates []string       `json:"normalizedStates"`
		ExitCodes        map[string]int `json:"exitCodes"`
	}
	if err := json.Unmarshal(raw, &manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.Status != "frozen" || manifest.ProtocolVersion != protocol.Version {
		t.Fatalf("protocol v1 contract is not frozen: %+v", manifest)
	}
	if manifest.SchemaVersions.Config != config.SchemaVersion ||
		manifest.SchemaVersions.Plan != planner.PlanSchemaVersion ||
		manifest.SchemaVersions.Evidence != evidence.SchemaVersion ||
		manifest.SchemaVersions.Journal != journal.SchemaVersion ||
		manifest.SchemaVersions.CLIOutput != outputSchemaVersion {
		t.Fatalf("schema contract drift: %+v", manifest.SchemaVersions)
	}
	states := []string{
		string(domain.StatePlanned), string(domain.StateReady), string(domain.StateRunning),
		string(domain.StateBlocked), string(domain.StateWaitingExternal), string(domain.StatePublished),
		string(domain.StateRejected), string(domain.StateFailed), string(domain.StateCancelled),
	}
	if !reflect.DeepEqual(manifest.NormalizedStates, states) {
		t.Fatalf("normalized state contract drift: manifest=%v implementation=%v", manifest.NormalizedStates, states)
	}
	for _, state := range states {
		if !domain.NormalizedState(state).Valid() {
			t.Fatalf("frozen state %q is invalid", state)
		}
	}
	exits := map[string]int{
		"success": exitOK, "operational": exitOperational, "usage": exitUsage,
		"invalid": exitInvalid, "pending": exitPending, "rejected": exitRejected, "failed": exitFailed,
	}
	if !reflect.DeepEqual(manifest.ExitCodes, exits) {
		t.Fatalf("exit code contract drift: manifest=%v implementation=%v", manifest.ExitCodes, exits)
	}
}
