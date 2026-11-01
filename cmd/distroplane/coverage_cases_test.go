package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aalsanie/distroplane/internal/domain"
)

type failingCoverageWriter struct{}

func (failingCoverageWriter) Write([]byte) (int, error) {
	return 0, errors.New("controlled output write failure")
}

func TestCLIPublishedRunStatusAndTruncatedJournalBoundary(t *testing.T) {
	dir := t.TempDir()
	artifact := filepath.Join(dir, "app.bin")
	if err := os.WriteFile(artifact, []byte("release bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	binary := buildFakeProvider(t)
	configPath := filepath.Join(dir, "distroplane.json")
	cfg := `{"schemaVersion":"1","release":{"id":"coverage","artifacts":[{"name":"app","source":"app.bin"}]},"targets":[{"id":"primary","provider":{"name":"fake","executable":` +
		jsonString(binary) + `},"configuration":{"mode":"published"}}]}`
	if err := os.WriteFile(configPath, []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	var out, diagnostics bytes.Buffer
	stateDir := filepath.Join(dir, "plans-output")
	if code := execute([]string{"plan", "--config", configPath, "--state-dir", stateDir, "--json"}, &out, &diagnostics); code != exitOK {
		t.Fatalf("plan code=%d stderr=%s", code, diagnostics.String())
	}
	var planned planOutput
	if err := json.Unmarshal(out.Bytes(), &planned); err != nil || planned.Path == "" {
		t.Fatalf("plan=%+v err=%v", planned, err)
	}
	if !strings.HasPrefix(planned.Path, stateDir) {
		t.Fatalf("absolute state-dir ignored: %s", planned.Path)
	}
	journalPath := filepath.Join(dir, "run.journal")
	applyArgs := []string{"apply", "--config", configPath, "--plan", planned.Path, "--journal", journalPath, "--run", "coverage-run", "--json"}
	out.Reset()
	diagnostics.Reset()
	if code := execute(applyArgs, &out, &diagnostics); code != exitOK {
		t.Fatalf("apply code=%d stderr=%s", code, diagnostics.String())
	}
	if code := execute(applyArgs, failingCoverageWriter{}, &diagnostics); code != exitOperational {
		t.Fatalf("unwritable JSON output accepted: %d", code)
	}
	statusArgs := []string{"status", "--plan", planned.Path, "--journal", journalPath, "--json"}
	if code := execute(statusArgs, failingCoverageWriter{}, &diagnostics); code != exitOperational {
		t.Fatalf("status output write failure accepted: %d", code)
	}
	file, err := os.OpenFile(journalPath, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	_, err = file.Write([]byte{0x01})
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		t.Fatal(err)
	}
	out.Reset()
	diagnostics.Reset()
	if code := execute([]string{"status", "--plan", planned.Path, "--journal", journalPath}, &out, &diagnostics); code != exitOK {
		t.Fatalf("durable-prefix status code=%d stderr=%s", code, diagnostics.String())
	}
	if !strings.Contains(diagnostics.String(), "truncated final record") {
		t.Fatalf("missing recovery warning: %s", diagnostics.String())
	}
}

func TestCLIRejectsMissingAndCorruptJournalFiles(t *testing.T) {
	var stdout, stderr bytes.Buffer
	for _, args := range [][]string{
		{"apply", "--plan", "missing", "--journal", "missing", "--json"},
		{"reconcile", "--plan", "missing", "--journal", "missing", "--json"},
		{"status", "--plan", "missing", "--journal", "missing", "--json"},
		{"evidence", "--plan", "missing", "--journal", "missing", "--json"},
	} {
		stdout.Reset()
		stderr.Reset()
		if code := execute(args, &stdout, &stderr); code == exitOK {
			t.Fatalf("missing plan accepted: %v", args)
		}
	}
	for _, state := range []domain.NormalizedState{domain.StateWaitingExternal, domain.StateCancelled} {
		got := stateExit(resultState(t, state))
		if got != exitPending && got != exitFailed {
			t.Errorf("unexpected exit %d for %s", got, state)
		}
	}
}
