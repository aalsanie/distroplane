package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunInventoryCommandErrorsAndTextOutput(t *testing.T) {
	provider := buildFakeProvider(t)
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "artifact.bin"), []byte("release"), 0600); err != nil {
		t.Fatal(err)
	}
	config := map[string]any{
		"schemaVersion": "1",
		"release": map[string]any{
			"id": "test-release",
			"artifacts": []map[string]any{{"name": "app", "source": "artifact.bin"}},
		},
		"targets": []map[string]any{{
			"id": "target",
			"provider": map[string]any{"name": "fake", "executable": provider},
			"configuration": map[string]any{"mode": "published"},
		}},
	}
	encoded, err := json.Marshal(config)
	if err != nil { t.Fatal(err) }
	path := filepath.Join(root, "distroplane.json")
	if err := os.WriteFile(path, encoded, 0600); err != nil { t.Fatal(err) }
	run := func(want int, args ...string) (string, string) {
		t.Helper()
		var stdout, stderr bytes.Buffer
		code := execute(args, &stdout, &stderr)
		if code != want {
			t.Fatalf("command %v returned %d not %d: stdout=%s stderr=%s", args, code, want, stdout.String(), stderr.String())
		}
		return stdout.String(), stderr.String()
	}
	if _, diagnostic := run(exitUsage, "runs", "--config", path, "--unexpected", "--json"); !strings.Contains(diagnostic, "usage") {
		t.Fatalf("unexpected parser error: %s", diagnostic)
	}
	if _, diagnostic := run(exitInvalid, "runs", "--config", filepath.Join(root, "absent.json"), "--json"); !strings.Contains(diagnostic, "invalid_config") {
		t.Fatalf("missing configuration not rejected: %s", diagnostic)
	}
	if stdout, _ := run(exitOK, "runs", "--config", path); stdout != "" {
		t.Fatalf("unexpected runs in empty catalog: %s", stdout)
	}
	var planned struct{ Path string `json:"path"` }
	output, _ := run(exitOK, "plan", "--config", path, "--json")
	if err := json.Unmarshal([]byte(output), &planned); err != nil || planned.Path == "" {
		t.Fatalf("plan fixture failed: %s (%v)", output, err)
	}
	if stdout, _ := run(exitOK, "runs", "--config", path); !strings.Contains(stdout, "release test-release") || !strings.Contains(stdout, "state PLANNED") {
		t.Fatalf("text inventory incorrectly formatted: %q", stdout)
	}
	if _, diagnostic := run(exitInvalid, "runs", "--config", path, "--release", "wrong-release", "--json"); !strings.Contains(diagnostic, "run_selection_failed") {
		t.Fatalf("unknown release silently selected: %s", diagnostic)
	}
	// Corrupted catalog paths must be classified as invalid data, never
	// converted into an empty inventory.
	if err := os.WriteFile(filepath.Join(root, ".distroplane", "catalog", "plans", "garbage"), []byte("{}"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, diagnostic := run(exitInvalid, "runs", "--config", path, "--json"); !strings.Contains(diagnostic, "catalog_invalid") {
		t.Fatalf("corrupt catalog treated as empty: %s", diagnostic)
	}
}
