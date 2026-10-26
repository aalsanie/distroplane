package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunCatalogCLIInventoryAndReusedExecution(t *testing.T) {
	root := t.TempDir()
	provider := buildFakeProvider(t)
	artifact := filepath.Join(root, "artifact.bin")
	if err := os.WriteFile(artifact, []byte("release payload"), 0600); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(root, "distroplane.json")
	cfg := map[string]any{
		"schemaVersion": "1",
		"release":       map[string]any{"id": "stable-release", "artifacts": []map[string]any{{"name": "app", "source": "artifact.bin"}}},
		"targets": []map[string]any{{
			"id":            "destination",
			"provider":      map[string]any{"name": "fake", "executable": provider},
			"configuration": map[string]any{"mode": "published"},
		}},
	}
	raw, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath, raw, 0600); err != nil {
		t.Fatal(err)
	}
	call := func(expected int, args ...string) []byte {
		t.Helper()
		var out, fail bytes.Buffer
		code := execute(args, &out, &fail)
		if code != expected {
			t.Fatalf("command %v code=%d (expected %d) out=%s err=%s", args, code, expected, out.String(), fail.String())
		}
		if code != exitOK {
			return fail.Bytes()
		}
		return out.Bytes()
	}
	type listOutput struct {
		Runs []struct {
			ReleaseID string `json:"releaseId"`
			PlanID    string `json:"planId"`
			RunID     string `json:"runId"`
			State     string `json:"state"`
			Completed bool   `json:"completed"`
		} `json:"runs"`
	}
	inventory := func(args ...string) listOutput {
		t.Helper()
		var v listOutput
		if err := json.Unmarshal(call(exitOK, args...), &v); err != nil {
			t.Fatal(err)
		}
		return v
	}
	empty := inventory("runs", "--config", configPath, "--json")
	if len(empty.Runs) != 0 {
		t.Fatalf("unexpected existing state %+v", empty)
	}
	var plan struct {
		PlanID string `json:"planId"`
		Path   string `json:"path"`
	}
	if err := json.Unmarshal(call(exitOK, "plan", "--config", configPath, "--json"), &plan); err != nil {
		t.Fatal(err)
	}
	if plan.PlanID == "" || plan.Path == "" {
		t.Fatal("missing plan")
	}
	planned := inventory("runs", "--config", configPath, "--json")
	if len(planned.Runs) != 1 || planned.Runs[0].State != "PLANNED" {
		t.Fatalf("planned=%+v", planned)
	}
	journalPath := filepath.Join(root, ".distroplane", "runs", "run-a.journal")
	apply := []string{"apply", "--config", configPath, "--plan", plan.Path, "--journal", journalPath, "--run", "run-a", "--json"}
	call(exitOK, apply...)
	published := inventory("runs", "--config", configPath, "--release", "stable-release", "--run", "run-a", "--json")
	if len(published.Runs) != 1 || !published.Runs[0].Completed || published.Runs[0].State != "COMPLETED" {
		t.Fatalf("published=%+v", published)
	}
	before, err := os.ReadFile(journalPath)
	if err != nil {
		t.Fatal(err)
	}
	call(exitOK, apply...)
	after, err := os.ReadFile(journalPath)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatalf("reapply changed historical journal: %v", err)
	}
	alternate := []string{"apply", "--config", configPath, "--plan", plan.Path, "--journal", filepath.Join(root, "other.journal"), "--run", "run-b", "--json"}
	diagnostic := call(exitInvalid, alternate...)
	if !strings.Contains(string(diagnostic), "run_catalog_conflict") {
		t.Fatalf("unexpected selection error %s", diagnostic)
	}
	if _, err := os.Stat(filepath.Join(root, "other.journal")); !os.IsNotExist(err) {
		t.Fatalf("duplicate journal created: %v", err)
	}
	if got := call(exitInvalid, "runs", "--config", configPath, "--run", "no-such-run", "--json"); !strings.Contains(string(got), "run_selection_failed") {
		t.Fatalf("unknown selection %s", got)
	}
}
