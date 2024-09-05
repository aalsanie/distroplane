package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestExecuteHelpVariants(t *testing.T) {
	tests := [][]string{nil, {"help"}, {"-h"}, {"--help"}}
	for _, args := range tests {
		var stdout, stderr bytes.Buffer
		if code := execute(args, &stdout, &stderr); code != 0 {
			t.Fatalf("execute(%v) code = %d, want 0", args, code)
		}
		if !strings.Contains(stdout.String(), "release distribution control plane") {
			t.Fatalf("execute(%v) output = %q, missing product description", args, stdout.String())
		}
		if stderr.Len() != 0 {
			t.Fatalf("execute(%v) stderr = %q, want empty", args, stderr.String())
		}
	}
}

func TestExecuteHelpRejectsArguments(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := execute([]string{"help", "extra"}, &stdout, &stderr); code != 2 {
		t.Fatalf("code = %d, want 2", code)
	}
	if !strings.Contains(stderr.String(), "does not accept arguments") {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestExecuteUnknownCommand(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := execute([]string{"publish"}, &stdout, &stderr); code != 2 {
		t.Fatalf("code = %d, want 2", code)
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout = %q, want empty", stdout.String())
	}
	if got := stderr.String(); !strings.Contains(got, `unknown command "publish"`) || !strings.Contains(got, "distroplane help") {
		t.Fatalf("stderr = %q", got)
	}
}

func TestVersionText(t *testing.T) {
	oldVersion, oldCommit, oldBuildDate := version, commit, buildDate
	t.Cleanup(func() { version, commit, buildDate = oldVersion, oldCommit, oldBuildDate })
	version, commit, buildDate = "1.2.3", "abc123", "2026-09-17T00:00:00Z"

	var stdout, stderr bytes.Buffer
	if code := execute([]string{"version"}, &stdout, &stderr); code != 0 {
		t.Fatalf("code = %d, want 0", code)
	}
	want := "distroplane 1.2.3 (commit abc123, built 2026-09-17T00:00:00Z)\n"
	if stdout.String() != want {
		t.Fatalf("stdout = %q, want %q", stdout.String(), want)
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
}

func TestVersionJSON(t *testing.T) {
	oldVersion, oldCommit, oldBuildDate := version, commit, buildDate
	t.Cleanup(func() { version, commit, buildDate = oldVersion, oldCommit, oldBuildDate })
	version, commit, buildDate = "1.2.3", "abc123", "unknown"

	var stdout, stderr bytes.Buffer
	if code := execute([]string{"version", "--json"}, &stdout, &stderr); code != 0 {
		t.Fatalf("code = %d, want 0; stderr=%s", code, stderr.String())
	}
	var got versionInfo
	if err := json.Unmarshal(bytes.TrimSpace(stdout.Bytes()), &got); err != nil {
		t.Fatalf("decode JSON: %v", err)
	}
	want := (versionInfo{Version: "1.2.3", Commit: "abc123", BuildDate: "unknown"})
	if got != want {
		t.Fatalf("version info = %#v, want %#v", got, want)
	}
}

func TestVersionRejectsInvalidArguments(t *testing.T) {
	tests := [][]string{{"version", "--yaml"}, {"version", "--json", "extra"}}
	for _, args := range tests {
		var stdout, stderr bytes.Buffer
		if code := execute(args, &stdout, &stderr); code != 2 {
			t.Fatalf("execute(%v) code = %d, want 2", args, code)
		}
		if stderr.Len() == 0 {
			t.Fatalf("execute(%v) stderr empty, want diagnostic", args)
		}
	}
}
