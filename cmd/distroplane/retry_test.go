package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aalsanie/distroplane/internal/domain"
	"github.com/aalsanie/distroplane/internal/journal"
	"github.com/aalsanie/distroplane/internal/planner"
)

func TestCLIRetryAuthorizesOneReconciledAbsentNpmPublication(t *testing.T) {
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
	stdout.Reset()
	stderr.Reset()
	if code := execute(append(append([]string(nil), baseArgs...), "--acknowledge-duplicate-risk", "--json"), &stdout, &stderr); code != exitOK {
		t.Fatalf("authorization failed code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	var authorized struct {
		PlanID      string `json:"planId"`
		RunID       string `json:"runId"`
		OperationID string `json:"operationId"`
		Authorized  bool   `json:"authorized"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &authorized); err != nil ||
		!authorized.Authorized || authorized.PlanID != planned.PlanID ||
		authorized.RunID != string(runID) || authorized.OperationID != string(operation.ID()) {
		t.Fatalf("invalid authorization output=%+v err=%v", authorized, err)
	}
	read, err := readJournal(journalPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(read.Events) != len(entries)+1 || read.Events[len(entries)].Type != journal.EventRetryAuthorized {
		t.Fatalf("authorization was not journaled")
	}
	stdout.Reset()
	stderr.Reset()
	if code := execute([]string{"status", "--plan", planned.Path, "--journal", journalPath, "--json"}, &stdout, &stderr); code != exitPending {
		t.Fatalf("authorization unexpectedly completed run: code=%d stderr=%s", code, stderr.String())
	}
	stdout.Reset()
	stderr.Reset()
	if code := execute(append(append([]string(nil), baseArgs...), "--acknowledge-duplicate-risk"), &stdout, &stderr); code != exitInvalid {
		t.Fatalf("duplicate authorization succeeded: code=%d stdout=%s", code, stdout.String())
	}
	readAgain, err := readJournal(journalPath)
	if err != nil || len(readAgain.Events) != len(read.Events) {
		t.Fatalf("failed authorization modified journal: %v", err)
	}
	if strings.Contains(stdout.String(), "authorized one retry") {
		t.Fatal("duplicate retry reported success")
	}

	// Exercise the text output path independently; approval never republishes.
	textJournalPath := filepath.Join(dir, "text.journal")
	textWriter, err := journal.OpenWriter(textJournalPath, runID)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if _, err := textWriter.Append(entry); err != nil {
			t.Fatal(err)
		}
	}
	if err := textWriter.Close(); err != nil {
		t.Fatal(err)
	}
	stdout.Reset()
	stderr.Reset()
	if code := execute([]string{
		"retry", "--plan", planned.Path, "--journal", textJournalPath,
		"--operation", string(operation.ID()), "--confirm-plan", planned.PlanID,
		"--acknowledge-duplicate-risk",
	}, &stdout, &stderr); code != exitOK ||
		!strings.Contains(stdout.String(), "authorized one retry") ||
		!strings.Contains(stdout.String(), "WARNING:") {
		t.Fatalf("text retry code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
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
