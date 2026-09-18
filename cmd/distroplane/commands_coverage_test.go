package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aalsanie/distroplane/internal/journal"
)

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, errors.New("write failed") }

func TestCLITextWorkflowAndFailureSurfaces(t *testing.T) {
	dir := t.TempDir()
	providerBinary := buildFakeProvider(t)
	alpha := copyExecutable(t, providerBinary, filepath.Join(dir, executableName("distroplane-provider-alpha")))
	artifact := filepath.Join(dir, "app.bin")
	if err := os.WriteFile(artifact, []byte("artifact"), 0o600); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(dir, "distroplane.json")
	rawConfig := `{"schemaVersion":"1","release":{"id":"v1","artifacts":[{"name":"app","source":"app.bin"}]},"targets":[{"id":"alpha","provider":{"name":"alpha","executable":` + jsonString(alpha) + `},"configuration":{"mode":"published"}}]}`
	if err := os.WriteFile(configPath, []byte(rawConfig), 0o600); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	if code := execute([]string{"validate", "--config", configPath}, &stdout, &stderr); code != exitOK || !strings.Contains(stdout.String(), "valid config") {
		t.Fatalf("validate code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}

	stdout.Reset()
	stderr.Reset()
	if code := execute([]string{"plan", "--config", configPath}, &stdout, &stderr); code != exitOK {
		t.Fatalf("plan code=%d stderr=%s", code, stderr.String())
	}
	var planPath string
	for _, line := range strings.Split(stdout.String(), "\n") {
		if strings.HasPrefix(line, "path ") {
			planPath = strings.TrimPrefix(line, "path ")
		}
	}
	if planPath == "" {
		t.Fatalf("plan output=%q", stdout.String())
	}

	journalPath := filepath.Join(dir, "run.journal")
	stdout.Reset()
	stderr.Reset()
	if code := execute([]string{"apply", "--config", configPath, "--plan", planPath, "--journal", journalPath, "--run", "run-text"}, &stdout, &stderr); code != exitOK {
		t.Fatalf("apply code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "completed true") {
		t.Fatalf("apply stdout=%q", stdout.String())
	}

	stdout.Reset()
	stderr.Reset()
	if code := execute([]string{"status", "--plan", planPath, "--journal", journalPath}, &stdout, &stderr); code != exitOK {
		t.Fatalf("status code=%d stderr=%s", code, stderr.String())
	}

	stdout.Reset()
	stderr.Reset()
	if code := execute([]string{"reconcile", "--config", configPath, "--plan", planPath, "--journal", journalPath}, &stdout, &stderr); code != exitOK {
		t.Fatalf("reconcile code=%d stderr=%s", code, stderr.String())
	}

	missingJournal := filepath.Join(dir, "missing.journal")
	stdout.Reset()
	stderr.Reset()
	if code := execute([]string{"reconcile", "--config", configPath, "--plan", planPath, "--journal", missingJournal, "--json"}, &stdout, &stderr); code != exitInvalid || !json.Valid(stderr.Bytes()) {
		t.Fatalf("missing reconcile code=%d stderr=%s", code, stderr.String())
	}

	stdout.Reset()
	stderr.Reset()
	if code := execute([]string{"status", "--plan", planPath, "--journal", missingJournal}, &stdout, &stderr); code != exitOperational || !strings.Contains(stderr.String(), "journal_read_failed") {
		t.Fatalf("missing status code=%d stderr=%s", code, stderr.String())
	}

	if err := os.WriteFile(artifact, []byte("changed"), 0o600); err != nil {
		t.Fatal(err)
	}
	stdout.Reset()
	stderr.Reset()
	if code := execute([]string{"apply", "--config", configPath, "--plan", planPath, "--journal", filepath.Join(dir, "changed.journal")}, &stdout, &stderr); code != exitInvalid || !strings.Contains(stderr.String(), "artifact_changed") {
		t.Fatalf("changed artifact code=%d stderr=%s", code, stderr.String())
	}
}

func TestCLIErrorsAndOutputContracts(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := execute([]string{"validate", "--config", "missing.json"}, &stdout, &stderr); code != exitInvalid {
		t.Fatalf("validate missing code=%d", code)
	}
	stdout.Reset()
	stderr.Reset()
	if code := execute([]string{"plan", "--config", "missing.json", "--json"}, &stdout, &stderr); code != exitInvalid || !json.Valid(stderr.Bytes()) {
		t.Fatalf("plan missing code=%d stderr=%s", code, stderr.String())
	}
	stdout.Reset()
	stderr.Reset()
	if code := execute([]string{"status", "--plan", "missing.json", "--journal", "missing.journal"}, &stdout, &stderr); code != exitInvalid {
		t.Fatalf("status missing plan code=%d stderr=%s", code, stderr.String())
	}
	stdout.Reset()
	stderr.Reset()
	if code := execute([]string{"apply", "--json", "--bad"}, &stdout, &stderr); code != exitUsage || !json.Valid(stderr.Bytes()) {
		t.Fatalf("usage code=%d stderr=%s", code, stderr.String())
	}

	if code := writeJSON(failingWriter{}, map[string]bool{"ok": true}); code != exitOperational {
		t.Fatalf("writeJSON code=%d", code)
	}
	if code := writeCommandError(failingWriter{}, true, exitInvalid, "bad", io.ErrClosedPipe); code != exitOperational {
		t.Fatalf("writeCommandError code=%d", code)
	}
	if stateExit(journalState(false, false)) != exitPending {
		t.Fatal("incomplete state must be pending")
	}
	if stateExit(journalState(true, false)) != exitOK {
		t.Fatal("completed state must succeed")
	}
	if stateExit(journalState(false, true)) != exitFailed {
		t.Fatal("cancelled state must fail")
	}
}

func journalState(completed, cancelled bool) journal.DerivedState {
	return journal.DerivedState{Completed: completed, Cancelled: cancelled}
}
