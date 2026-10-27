package main

import (
	"bufio"
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInitArtifactSelectorErrorsAndExplicitMapping(t *testing.T) {
	dir := t.TempDir()
	for _, tc := range []struct {
		name    string
		choices []string
		want    string
	}{
		{"unknown provider", []string{"winget=installer.msi"}, "was not selected"},
		{"duplicate provider", []string{"npm=first.tgz", "npm=second.tgz"}, "duplicate"},
		{"empty provider", []string{"npm="}, "empty"},
		{"duplicate shared", []string{"first.tgz", "second.tgz"}, "multiple shared"},
		{"missing artifact", nil, "no recent build artifacts"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := initArtifactPaths([]string{"npm"}, tc.choices, dir, false, nil, nil)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("expected %q, got %v", tc.want, err)
			}
		})
	}
	got, err := initArtifactPaths([]string{"npm", "sdkman"}, []string{"npm=release.tgz", "sdkman=release.zip"}, dir, false, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got["npm"] != filepath.Join(dir, "release.tgz") || got["sdkman"] != filepath.Join(dir, "release.zip") {
		t.Fatalf("scoped mapping changed: %+v", got)
	}
}

func TestInitArtifactSelectorPromptsWithoutCandidates(t *testing.T) {
	dir := t.TempDir()
	var diagnostic bytes.Buffer
	got, err := initArtifactPaths([]string{"npm"}, nil, dir, true,
		bufio.NewReader(strings.NewReader("target/package.tgz\n")), &diagnostic)
	if err != nil {
		t.Fatal(err)
	}
	if got["npm"] != filepath.Join(dir, "target", "package.tgz") {
		t.Fatalf("prompt path changed: %+v", got)
	}
	if !strings.Contains(diagnostic.String(), "artifact path") {
		t.Fatalf("prompt was omitted: %s", diagnostic.String())
	}
	_, err = initArtifactPaths([]string{"npm"}, nil, dir, true,
		bufio.NewReader(strings.NewReader("\n")), io.Discard)
	if err == nil {
		t.Fatal("empty candidate selection accepted")
	}
}

func TestInitForceOnNewFileDoesNotCreateBackup(t *testing.T) {
	dir, _ := createInitProject(t)
	output := filepath.Join(dir, "fresh-config.json")
	args := []string{"--destination", "npm", "--release", "fresh", "--output", output, "--force", "--json"}
	result, _ := invokeInit(t, exitOK, "", false, args...)
	if result.BackupPath != "" {
		t.Fatalf("fabricated backup: %+v", result)
	}
	if _, err := os.Stat(output + ".bak"); !os.IsNotExist(err) {
		t.Fatalf("backup written without previous file: %v", err)
	}
}

func TestInitRejectsUnsafeMetadataAndDoesNotPersist(t *testing.T) {
	dir, _ := createInitProject(t)
	for _, tc := range []struct {
		value string
		want  string
	}{
		{"npm.registry=ftp://insecure.example/", "invalid_template"},
		{"npm.auth-mode=unsafe", "invalid_template"},
		{"npm.packagePath=/tmp/override.tgz", "unknown_option"},
		{"npm.credential=secret-string", "unknown_option"},
	} {
		var output, diagnostic bytes.Buffer
		args := []string{"--destination", "npm", "--release", "release", "--output",
			filepath.Join(dir, "distroplane.json"), "--json", "--set", tc.value}
		code := runInitContext(context.Background(), args, nil, false, &output, &diagnostic)
		wantCode := exitInvalid
		if tc.want == "unknown_option" {
			wantCode = exitUsage
		}
		if code != wantCode || !strings.Contains(diagnostic.String(), tc.want) {
			t.Fatalf("unsafe metadata accepted: code=%d stderr=%s", code, diagnostic.String())
		}
		if _, err := os.Stat(filepath.Join(dir, "distroplane.json")); !os.IsNotExist(err) {
			t.Fatalf("unsafe metadata persisted: %v", err)
		}
	}
}

func TestInitArtifactSelectsOnlyRegularNonemptyFiles(t *testing.T) {
	dir := t.TempDir()
	empty := filepath.Join(dir, "empty.tgz")
	if err := os.WriteFile(empty, nil, 0600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := inspectInitArtifact(dir, empty); err == nil {
		t.Fatal("empty archive selected")
	}
	if _, _, err := inspectInitArtifact(dir, ""); err == nil {
		t.Fatal("empty artifact path selected")
	}
	if _, _, err := inspectInitArtifact(dir, filepath.Join(dir, "missing.tgz")); err == nil {
		t.Fatal("nonexistent artifact selected")
	}
}
