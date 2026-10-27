package main

import (
	"bufio"
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"runtime"
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

func TestInitArtifactSelectionPromptsOnAmbiguousCandidates(t *testing.T) {
	dir := t.TempDir()
	dist := filepath.Join(dir, "dist")
	if err := os.MkdirAll(dist, 0700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"first.tgz", "second.tgz"} {
		if err := os.WriteFile(filepath.Join(dist, name), []byte("fixture"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	var prompt bytes.Buffer
	selected, err := initArtifactPaths([]string{"npm"}, nil, dir, true,
		bufio.NewReader(strings.NewReader("dist/second.tgz\n")), &prompt)
	if err != nil || selected["npm"] != filepath.Join(dir, "dist", "second.tgz") {
		t.Fatalf("explicit prompt selection failed: %+v %v", selected, err)
	}
	if !strings.Contains(prompt.String(), "first.tgz") || !strings.Contains(prompt.String(), "second.tgz") {
		t.Fatalf("ambiguous choices were hidden: %s", prompt.String())
	}
}

func TestInitRejectsMissingWizardMetadataBeforeWriting(t *testing.T) {
	dir, _ := createInitProject(t)
	var output, stderr bytes.Buffer
	args := []string{"--output", filepath.Join(dir, "distroplane.json"),
		"--destination", "sdkman", "--release", "release"}
	if code := runInitContext(context.Background(), args, strings.NewReader("\n"), true, &output, &stderr); code != exitInvalid {
		t.Fatalf("incomplete metadata accepted: code=%d stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "sdkman.candidate") {
		t.Fatalf("missing required metadata was not identified: %s", stderr.String())
	}
	if _, err := os.Stat(filepath.Join(dir, "distroplane.json")); !os.IsNotExist(err) {
		t.Fatalf("incomplete wizard persisted config: %v", err)
	}
}

func TestInitDiscoveryIgnoresSymlinkAndEmptyArtifact(t *testing.T) {
	dir := t.TempDir()
	dist := filepath.Join(dir, "dist")
	if err := os.MkdirAll(dist, 0700); err != nil {
		t.Fatal(err)
	}
	archive := filepath.Join(dist, "actual.zip")
	if err := os.WriteFile(archive, []byte("data"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dist, "empty.zip"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(archive, filepath.Join(dist, "alias.zip")); err != nil && runtime.GOOS != "windows" {
		t.Fatal(err)
	}
	candidates, err := discoverArtifacts(dir)
	if err != nil || len(candidates) != 1 || candidates[0] != "dist/actual.zip" {
		t.Fatalf("unsafe candidate surfaced: %+v %v", candidates, err)
	}
}

func TestInitDirectWriteRefusesSymlinkParent(t *testing.T) {
	dir := t.TempDir()
	real := filepath.Join(dir, "real")
	if err := os.MkdirAll(real, 0700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "linked")
	if err := os.Symlink(real, link); err != nil {
		if runtime.GOOS == "windows" {
			t.Skipf("symlink not available: %v", err)
		}
		t.Fatal(err)
	}
	if _, err := writeInitConfig(filepath.Join(link, "config.json"), []byte("{}"), false); err == nil {
		t.Fatal("linked parent was accepted for config installation")
	}
}
