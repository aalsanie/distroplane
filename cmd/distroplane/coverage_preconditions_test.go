package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCLIRejectsDurabilityAndExecutionPreconditions(t *testing.T) {
	root := t.TempDir()
	artifact := filepath.Join(root, "application.bin")
	if err := os.WriteFile(artifact, []byte("verified-archive"), 0o600); err != nil {
		t.Fatal(err)
	}
	providerExecutable := buildFakeProvider(t)
	configPath := filepath.Join(root, "distroplane.json")
	config := `{"schemaVersion":"1","release":{"id":"error-paths","artifacts":[{"name":"application","source":"application.bin"}]},"targets":[{"id":"primary","provider":{"name":"fake","executable":` +
		jsonString(providerExecutable) + `},"configuration":{"mode":"published"}}]}`
	if err := os.WriteFile(configPath, []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	run := func(args ...string) (int, string) {
		t.Helper()
		var stdout, stderr bytes.Buffer
		code := execute(args, &stdout, &stderr)
		return code, stderr.String()
	}
	var output, diagnostics bytes.Buffer
	if code := execute([]string{"plan", "--config", configPath, "--json"}, &output, &diagnostics); code != exitOK {
		t.Fatalf("baseline plan failed: code=%d err=%s", code, diagnostics.String())
	}
	var planned planOutput
	if err := json.Unmarshal(output.Bytes(), &planned); err != nil {
		t.Fatal(err)
	}
	for name, operation := range map[string]func() int{
		"missing catalog selectors": func() int { code, _ := run("status", "--config", configPath, "--run", "not-found"); return code },
		"empty journal": func() int {
			path := filepath.Join(root, "empty.journal")
			if err := os.WriteFile(path, nil, 0o600); err != nil {
				t.Fatal(err)
			}
			code, _ := run("status", "--plan", planned.Path, "--journal", path)
			return code
		},
		"missing journal": func() int {
			code, _ := run("status", "--plan", planned.Path, "--journal", filepath.Join(root, "not-found"))
			return code
		},
		"corrupt journal": func() int {
			path := filepath.Join(root, "corrupt.journal")
			if err := os.WriteFile(path, []byte("not-journal"), 0o600); err != nil {
				t.Fatal(err)
			}
			code, _ := run("status", "--plan", planned.Path, "--journal", path)
			return code
		},
		"reconcile nonexistent run": func() int {
			code, _ := run("reconcile", "--plan", planned.Path, "--journal", filepath.Join(root, "no-reconcile"), "--config", configPath)
			return code
		},
		"plan output filesystem": func() int {
			blocked := filepath.Join(root, "blocked")
			if err := os.WriteFile(blocked, []byte("not a directory"), 0o600); err != nil {
				t.Fatal(err)
			}
			code, _ := run("plan", "--config", configPath, "--state-dir", blocked)
			return code
		},
	} {
		t.Run(name, func(t *testing.T) {
			if code := operation(); code == exitOK {
				t.Fatal("invalid durable state was accepted")
			}
		})
	}
	if code := execute([]string{"validate", "--config", configPath, "--json"}, failingCoverageWriter{}, &diagnostics); code == exitOK {
		t.Fatal("validation claimed success despite a failed JSON write")
	}
	if code := execute([]string{"plan", "--config", configPath, "--json"}, failingCoverageWriter{}, &diagnostics); code == exitOK {
		t.Fatal("plan claimed success despite a failed JSON write")
	}
	if err := os.WriteFile(artifact, []byte("tampered"), 0o600); err != nil {
		t.Fatal(err)
	}
	if code, _ := run("apply", "--config", configPath, "--plan", planned.Path, "--journal", filepath.Join(root, "tampered.journal")); code == exitOK {
		t.Fatal("modified artifact was published")
	}
	if err := os.WriteFile(artifact, []byte("verified-archive"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(configPath); err != nil {
		t.Fatal(err)
	}
	if code, _ := run("apply", "--config", configPath, "--plan", planned.Path, "--journal", filepath.Join(root, "missing-config.journal")); code == exitOK {
		t.Fatal("execution accepted missing configuration")
	}
	if !strings.Contains(planned.Path, ".distroplane") {
		t.Fatalf("unexpected plan location: %q", planned.Path)
	}
}
