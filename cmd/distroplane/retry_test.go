package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/binary"
	"encoding/json"
	"hash/crc32"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aalsanie/distroplane/internal/domain"
	"github.com/aalsanie/distroplane/internal/journal"
	"github.com/aalsanie/distroplane/internal/planner"
)

func TestCLIRetryRefusesNewAndUnconsumedHistoricalAuthorization(t *testing.T) {
	dir := t.TempDir()
	providerBinary := filepath.Join(dir, executableName("distroplane-provider-npm"))
	build := exec.Command("go", "build", "-o", providerBinary, "../distroplane-provider-npm")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build npm provider: %v\n%s", err, output)
	}
	archive := filepath.Join(dir, "package.tgz")
	var packageBytes bytes.Buffer
	gzipWriter := gzip.NewWriter(&packageBytes)
	tarWriter := tar.NewWriter(gzipWriter)
	manifest := []byte(`{"name":"distroplane-retry-test","version":"1.2.3"}`)
	if err := tarWriter.WriteHeader(&tar.Header{Name: "package/package.json", Mode: 0o600, Size: int64(len(manifest))}); err != nil {
		t.Fatal(err)
	}
	if _, err := tarWriter.Write(manifest); err != nil {
		t.Fatal(err)
	}
	if err := tarWriter.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gzipWriter.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(archive, packageBytes.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(dir, "distroplane.json")
	config := `{"schemaVersion":"1","release":{"id":"retry-demo","artifacts":[{"name":"package","source":"package.tgz"}]},"targets":[{"id":"npm","provider":{"name":"npm","executable":` +
		jsonString(providerBinary) + `},"configuration":{"artifact":"package","packagePath":` + jsonString(archive) +
		`,"registry":"https://registry.npmjs.org/","authentication":{"mode":"token","credential":"npm-publish"}}}]}`
	if err := os.WriteFile(configPath, []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	if code := execute([]string{"plan", "--config", configPath, "--json"}, &stdout, &stderr); code != exitOK {
		t.Fatalf("plan failed code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	var planned planOutput
	if err := json.Unmarshal(stdout.Bytes(), &planned); err != nil {
		t.Fatal(err)
	}
	persisted, err := planner.Load(planned.Path)
	if err != nil {
		t.Fatal(err)
	}
	operation := persisted.Plan().Operations()[0]
	runID, _ := domain.NewRunID("run-retry-cli")
	journalPath := filepath.Join(dir, "run.journal")
	writer, err := journal.OpenWriter(journalPath, runID)
	if err != nil {
		t.Fatal(err)
	}
	entries := []journal.Entry{
		{RunID: runID, PlanID: persisted.ID(), Type: journal.EventRunStarted},
		{RunID: runID, Type: journal.EventAttemptStarted, OperationID: operation.ID(), TargetID: operation.TargetID(), Payload: journal.Payload{Attempt: 1}},
		{RunID: runID, Type: journal.EventProviderProcessStarted, OperationID: operation.ID(), TargetID: operation.TargetID(), Payload: journal.Payload{Attempt: 1}},
		{RunID: runID, Type: journal.EventSideEffectDispatched, OperationID: operation.ID(), TargetID: operation.TargetID(), Payload: journal.Payload{Attempt: 1}},
		{RunID: runID, Type: journal.EventOutcomeAmbiguous, OperationID: operation.ID(), TargetID: operation.TargetID(), Payload: journal.Payload{Attempt: 1, ErrorCode: "AMBIGUOUS_OUTCOME"}},
		{RunID: runID, Type: journal.EventReconcileStarted, OperationID: operation.ID(), TargetID: operation.TargetID(), Payload: journal.Payload{Attempt: 1}},
		{RunID: runID, Type: journal.EventProviderProcessStarted, OperationID: operation.ID(), TargetID: operation.TargetID(), Payload: journal.Payload{Attempt: 1}},
		{RunID: runID, Type: journal.EventProviderResponseReceived, OperationID: operation.ID(), TargetID: operation.TargetID(), Payload: journal.Payload{Attempt: 1}},
		{RunID: runID, Type: journal.EventReconcileResult, OperationID: operation.ID(), TargetID: operation.TargetID(), Payload: journal.Payload{
			Attempt: 1, State: domain.StateWaitingExternal, ProviderState: "absent", Evidence: json.RawMessage(`{"publicationState":"absent"}`),
		}},
	}
	for _, entry := range entries {
		if _, err := writer.Append(entry); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}

	baseArgs := []string{"retry", "--plan", planned.Path, "--journal", journalPath, "--operation", string(operation.ID()), "--confirm-plan", planned.PlanID}
	for name, args := range map[string][]string{
		"missing acknowledgment": baseArgs,
		"wrong confirmation":     append(append([]string(nil), baseArgs...), "--acknowledge-duplicate-risk", "--confirm-plan", "wrong"),
	} {
		stdout.Reset()
		stderr.Reset()
		if code := execute(args, &stdout, &stderr); code == exitOK {
			t.Fatalf("%s: invalid authorization accepted", name)
		}
	}
	before, err := os.ReadFile(journalPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, jsonMode := range []bool{false, true} {
		stdout.Reset()
		stderr.Reset()
		args := append(append([]string(nil), baseArgs...), "--acknowledge-duplicate-risk")
		if jsonMode {
			args = append(args, "--json")
		}
		if code := execute(args, &stdout, &stderr); code != exitInvalid || stdout.Len() != 0 ||
			!strings.Contains(stderr.String(), "authorization is disabled") {
			t.Fatalf("retry refusal code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
		}
	}
	after, err := os.ReadFile(journalPath)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatalf("retry refusal changed journal: %v", err)
	}

	legacy := journal.Event{
		SchemaVersion: journal.SchemaVersion, Sequence: uint64(len(entries) + 1), RunID: runID,
		Type: journal.EventRetryAuthorized, OperationID: operation.ID(), TargetID: operation.TargetID(),
		ObservedAt: time.Now().UTC(), Payload: journal.Payload{Attempt: 1},
	}
	payload, err := json.Marshal(legacy)
	if err != nil {
		t.Fatal(err)
	}
	frame := make([]byte, 12+len(payload))
	copy(frame, "DPJ1")
	binary.BigEndian.PutUint32(frame[4:8], uint32(len(payload)))
	binary.BigEndian.PutUint32(frame[8:12], crc32.ChecksumIEEE(payload))
	copy(frame[12:], payload)
	historical := append(append([]byte(nil), before...), frame...)
	historical = append(historical, []byte("DP")...)
	if err := os.WriteFile(journalPath, historical, 0600); err != nil {
		t.Fatal(err)
	}
	stdout.Reset()
	stderr.Reset()
	if code := execute([]string{"status", "--plan", planned.Path, "--journal", journalPath, "--json"}, &stdout, &stderr); code != exitPending {
		t.Fatalf("historical state could not be inspected: %d %s %s", code, stdout.String(), stderr.String())
	}
	var status stateOutput
	if err := json.Unmarshal(stdout.Bytes(), &status); err != nil || status.Completed ||
		len(status.Operations) != 1 || status.Operations[0].State != string(domain.StateReady) || status.Operations[0].Attempt != 1 {
		t.Fatalf("historical semantics changed: %+v %v", status, err)
	}
	stdout.Reset()
	stderr.Reset()
	if code := execute([]string{"status", "--plan", planned.Path, "--journal", journalPath}, &stdout, &stderr); code != exitPending ||
		!strings.Contains(stdout.String(), "seek manual recovery") || !strings.Contains(stderr.String(), "unconsumed historical retry authorization") {
		t.Fatalf("historical recovery guidance: %d %s %s", code, stdout.String(), stderr.String())
	}
	for _, command := range []string{"apply", "reconcile"} {
		stdout.Reset()
		stderr.Reset()
		if code := execute([]string{command, "--config", configPath, "--plan", planned.Path, "--journal", journalPath, "--json"}, &stdout, &stderr); code != exitInvalid ||
			stdout.Len() != 0 || !strings.Contains(stderr.String(), "unconsumed historical retry authorization") || !strings.Contains(stderr.String(), "do not resume this grant") {
			t.Fatalf("%s did not fail closed: %d %s %s", command, code, stdout.String(), stderr.String())
		}
		after, err := os.ReadFile(journalPath)
		if err != nil || !bytes.Equal(historical, after) {
			t.Fatalf("%s changed historical journal or truncated its tail: %v", command, err)
		}
	}
}

func TestCLIRetryRejectsInvalidInputs(t *testing.T) {
	for _, args := range [][]string{
		{"retry"},
		{"retry", "--unknown"},
		{"retry", "--plan", "missing.json", "--journal", "missing.journal", "--operation", "op-a", "--confirm-plan", "plan-1", "--acknowledge-duplicate-risk"},
		{"retry", "--plan", "missing.json", "--journal", "missing.journal", "--operation", "invalid\noperation", "--confirm-plan", "plan-1", "--acknowledge-duplicate-risk"},
	} {
		var stdout, stderr bytes.Buffer
		if code := execute(args, &stdout, &stderr); code == exitOK {
			t.Fatalf("invalid retry accepted: %q", args)
		}
	}
}
