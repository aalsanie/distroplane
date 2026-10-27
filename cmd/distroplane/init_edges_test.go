package main

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/aalsanie/distroplane/internal/config"
)

type initErrorReader struct{}

func (initErrorReader) Read([]byte) (int, error) {
	return 0, errors.New("input unavailable")
}

func TestInitRequiredProviderMetadataAndModes(t *testing.T) {
	cases := []struct {
		name   string
		values map[string]string
		want   string
	}{
		{"npm mode", map[string]string{"npm.auth-mode": "trusted-publishing"}, "trusted-publishing"},
		{"sdkman legacy", map[string]string{"sdkman.candidate": "sample", "sdkman.version": "1.2.3", "sdkman.url": "https://example.invalid/sample.zip", "sdkman.auth-mode": "legacy", "sdkman.platform": "UNIVERSAL"}, "legacy"},
		{"sdkman invalid auth", map[string]string{"sdkman.candidate": "sample", "sdkman.version": "1.2.3", "sdkman.url": "https://example.invalid/sample.zip", "sdkman.auth-mode": "invalid"}, "error"},
		{"homebrew pull request", map[string]string{"homebrew.repository": "https://github.com/example/homebrew-tap.git", "homebrew.path": "Formula/sample.rb", "homebrew.name": "Sample", "homebrew.description": "Sample app", "homebrew.homepage": "https://example.invalid/", "homebrew.url": "https://example.invalid/sample.tgz", "homebrew.version": "1.2.3", "homebrew.binary": "sample", "homebrew.mode": "pull-request", "homebrew.pr-repository": "example/homebrew-tap"}, "pull-request"},
		{"homebrew missing PR repository", map[string]string{"homebrew.repository": "https://github.com/example/homebrew-tap.git", "homebrew.path": "Formula/sample.rb", "homebrew.name": "Sample", "homebrew.description": "Sample app", "homebrew.homepage": "https://example.invalid/", "homebrew.url": "https://example.invalid/sample.tgz", "homebrew.version": "1.2.3", "homebrew.binary": "sample", "homebrew.mode": "pull-request"}, "error"},
		{"homebrew missing JVM version", map[string]string{"homebrew.repository": "https://github.com/example/homebrew-tap.git", "homebrew.path": "Formula/sample.rb", "homebrew.name": "Sample", "homebrew.description": "Sample app", "homebrew.homepage": "https://example.invalid/", "homebrew.url": "https://example.invalid/sample.zip", "homebrew.version": "1.2.3", "homebrew.binary": "sample"}, "error"},
		{"homebrew invalid cask", map[string]string{"homebrew.repository": "https://github.com/example/homebrew-tap.git", "homebrew.path": "Casks/sample.rb", "homebrew.name": "sample", "homebrew.description": "Sample app", "homebrew.homepage": "https://example.invalid/", "homebrew.url": "https://example.invalid/sample.zip", "homebrew.version": "1.2.3", "homebrew.install-kind": "app"}, "error"},
		{"winget invalid ZIP entries", map[string]string{"winget.repository": "https://github.com/example/winget-pkgs.git", "winget.identifier": "Example.Sample", "winget.version": "1.2.3", "winget.publisher": "Example", "winget.name": "Sample", "winget.license": "MIT", "winget.description": "Sample", "winget.url": "https://example.invalid/sample.zip", "winget.architecture": "x64", "winget.pr-repository": "microsoft/winget-pkgs", "winget.nested-type": "portable", "winget.nested-files": "not-json"}, "error"},
		{"winget empty ZIP entries", map[string]string{"winget.repository": "https://github.com/example/winget-pkgs.git", "winget.identifier": "Example.Sample", "winget.version": "1.2.3", "winget.publisher": "Example", "winget.name": "Sample", "winget.license": "MIT", "winget.description": "Sample", "winget.url": "https://example.invalid/sample.zip", "winget.architecture": "x64", "winget.pr-repository": "microsoft/winget-pkgs", "winget.nested-type": "portable", "winget.nested-files": "[]"}, "error"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			values := make(map[string]string)
			for key, value := range tc.values {
				values[key] = value
			}
			b := initBuilder{fields: values}
			var destination string
			switch {
			case strings.HasPrefix(tc.name, "npm"):
				destination = "npm"
			case strings.HasPrefix(tc.name, "sdkman"):
				destination = "sdkman"
			case strings.Contains(tc.name, "JVM"):
				destination = "homebrew:jvm"
			case strings.Contains(tc.name, "cask"):
				destination = "homebrew:cask"
			case strings.HasPrefix(tc.name, "homebrew"):
				destination = "homebrew:formula"
			default:
				destination = "winget:zip"
			}
			opts, err := b.providerOptions(destination, "dist/archive.zip")
			if tc.want == "error" {
				if err == nil {
					t.Fatalf("invalid template accepted: %v", opts)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			switch destination {
			case "npm", "sdkman":
				if opts["authentication"].(map[string]any)["mode"] != tc.want {
					t.Fatalf("authentication mode: %v", opts)
				}
			case "homebrew:formula":
				if opts["mode"] != "pull-request" || opts["pullRequest"].(map[string]any)["repository"] != "example/homebrew-tap" {
					t.Fatalf("review metadata: %v", opts)
				}
			}
		})
	}
}

func TestInitBuilderFieldPromptAndDefaults(t *testing.T) {
	var diagnostic bytes.Buffer
	b := initBuilder{fields: map[string]string{"preset": "chosen", "empty": ""}, reader: bufio.NewReader(strings.NewReader("entered\n")), prompts: true, stderr: &diagnostic}
	if got, err := b.required("preset"); err != nil || got != "chosen" {
		t.Fatalf("explicit metadata: %q %v", got, err)
	}
	if _, exists := b.fields["preset"]; exists {
		t.Fatal("field was not consumed")
	}
	if got := b.defaulted("default", "stable"); got != "stable" {
		t.Fatalf("default not respected: %q", got)
	}
	if got, err := b.required("prompt"); err != nil || got != "entered" || !strings.Contains(diagnostic.String(), "prompt") {
		t.Fatalf("prompt not honored: %q %v", got, err)
	}
	if _, err := b.required("empty"); err == nil {
		t.Fatal("empty metadata accepted")
	}
	b.prompts = false
	if _, err := b.required("absent"); err == nil || !strings.Contains(err.Error(), "absent") {
		t.Fatalf("missing field accepted: %v", err)
	}
	if got := b.optional("unused"); got != "" {
		t.Fatalf("optional field generated: %q", got)
	}
	if _, err := (&initBuilder{}).providerOptions("invalid", "archive.zip"); err == nil {
		t.Fatal("unknown template accepted")
	}
}

func TestInitPromptSafetyErrorsAndEOF(t *testing.T) {
	var out bytes.Buffer
	reader := bufio.NewReader(initErrorReader{})
	if _, err := promptInit(reader, &out, "destination"); err == nil {
		t.Fatal("unreadable input accepted")
	}
	if _, err := promptInit(bufio.NewReader(strings.NewReader(" \n")), &out, "destination"); err == nil {
		t.Fatal("empty input accepted")
	}
	if val, err := promptInit(bufio.NewReader(strings.NewReader("npm")), &out, "destination"); err != nil || val != "npm" {
		t.Fatalf("EOF input incorrectly rejected: %q %v", val, err)
	}
	dir, _ := createInitProject(t)
	args := []string{"--output", filepath.Join(dir, "distroplane.json")}
	for _, tc := range []struct {
		name  string
		input io.Reader
		kind  string
	}{
		{"unreadable destination", initErrorReader{}, "missing_destinations"},
		{"empty destination", strings.NewReader("\n"), "missing_destinations"},
		{"unreadable release", io.MultiReader(strings.NewReader("npm\n"), initErrorReader{}), "missing_release"},
		{"empty release", strings.NewReader("npm\n\n"), "missing_release"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			if code := runInitContext(context.Background(), args, tc.input, true, &stdout, &stderr); code != exitUsage || !strings.Contains(stderr.String(), tc.kind) {
				t.Fatalf("expected %s: code=%d stderr=%s", tc.kind, code, stderr.String())
			}
		})
	}
}

func TestInitPromptsForMultipleCandidatesAndProviderMetadata(t *testing.T) {
	dir, first := createInitProject(t)
	dist := filepath.Join(dir, "dist")
	if err := os.MkdirAll(dist, 0700); err != nil {
		t.Fatal(err)
	}
	second := filepath.Join(dist, "other.zip")
	if err := os.WriteFile(second, []byte("zip"), 0600); err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(dir, "distroplane.json")
	args := []string{"--destination", "sdkman", "--release", "test-release", "--output", output}
	input := strings.Join([]string{first, "sample", "1.2.3", "https://example.invalid/tool.zip"}, "\n") + "\n"
	var stdout, stderr bytes.Buffer
	if code := runInitContext(context.Background(), args, strings.NewReader(input), true, &stdout, &stderr); code != exitOK {
		t.Fatalf("wizard code=%d stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "Artifact candidates") || !strings.Contains(stderr.String(), "sdkman.candidate") {
		t.Fatalf("prompt omitted candidate/metadata: %s", stderr.String())
	}
	loaded, err := config.Load(output)
	if err != nil || loaded.Config.Release.ID != "test-release" {
		t.Fatalf("invalid interactive config: %+v %v", loaded.Config, err)
	}
}

func TestInitDiscoveryCandidateBoundsAndIgnoredEntries(t *testing.T) {
	dir := t.TempDir()
	dist := filepath.Join(dir, "dist")
	if err := os.MkdirAll(dist, 0700); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < maxArtifactCandidates+1; i++ {
		path := filepath.Join(dist, fmt.Sprintf("artifact-%02d.zip", i))
		if err := os.WriteFile(path, []byte("payload"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := discoverArtifacts(dir); err == nil || !strings.Contains(err.Error(), "too many artifact candidates") {
		t.Fatalf("candidate limit not enforced: %v", err)
	}
	old := time.Now().Add(-90 * 24 * time.Hour)
	now := time.Now().Add(24 * time.Hour)
	if err := os.Chtimes(filepath.Join(dist, "artifact-00.zip"), old, old); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(filepath.Join(dist, "artifact-01.zip"), now, now); err != nil {
		t.Fatal(err)
	}
	candidates, err := discoverArtifacts(dir)
	if err != nil || len(candidates) != maxArtifactCandidates-1 {
		t.Fatalf("invalid bounded list: %v %+v", err, candidates)
	}
	link := filepath.Join(dir, "build")
	if err := os.Symlink(dist, link); err == nil {
		candidates, err := discoverArtifacts(dir)
		if err != nil || len(candidates) != maxArtifactCandidates-1 {
			t.Fatalf("symlinked build directory scanned: %+v %v", candidates, err)
		}
	}
}

func TestInitEarlyOutputAndInputErrors(t *testing.T) {
	dir, _ := createInitProject(t)
	cases := []struct {
		name   string
		output string
		extra  []string
		want   int
		kind   string
	}{
		{"empty output", "", nil, exitUsage, "usage"},
		{"directory output", dir, nil, exitInvalid, "unsafe_output"},
		{"unknown metadata provider", filepath.Join(dir, "new1.json"), []string{"--set", "other.secret=VALUE"}, exitUsage, "unknown_option"},
		{"bad option syntax", filepath.Join(dir, "new2.json"), []string{"--set", "nonempty"}, exitUsage, "invalid_option"},
		{"invalid release ID", filepath.Join(dir, "new3.json"), []string{"--release", ""}, exitUsage, "missing_release"},
		{"missing specified artifact", filepath.Join(dir, "new4.json"), []string{"--artifact", "missing.zip"}, exitInvalid, "invalid_artifact"},
		{"unknown provider", filepath.Join(dir, "new5.json"), []string{"--destination", "not-supported"}, exitUsage, "invalid_destination"},
		{"invalid external URL", filepath.Join(dir, "new6.json"), []string{"--set", "npm.registry=http://insecure.example/"}, exitInvalid, "invalid_template"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			output := tc.output
			args := []string{"--destination", "npm", "--release", "release", "--output", output, "--json"}
			args = append(args, tc.extra...)
			var stdout, stderr bytes.Buffer
			code := runInitContext(context.Background(), args, nil, false, &stdout, &stderr)
			if code != tc.want || !strings.Contains(stderr.String(), tc.kind) {
				t.Fatalf("code=%d expected=%d kind=%s stderr=%s", code, tc.want, tc.kind, stderr.String())
			}
		})
	}
}

func TestInitExclusiveConfigInstallAndOverwriteRejection(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	if _, err := writeInitConfig(path, []byte("{}"), false); err != nil {
		t.Fatal(err)
	}
	if _, err := writeInitConfig(path, []byte("{\"new\":true}"), false); err == nil {
		t.Fatal("existing configuration overwritten")
	}
	before, err := os.ReadFile(path)
	if err != nil || string(before) != "{}" {
		t.Fatalf("existing file modified: %v %s", err, before)
	}
	if _, err := writeInitConfig(path, []byte("{\"new\":true}"), true); err != nil {
		t.Fatal(err)
	}
	bak, err := os.ReadFile(path + ".bak")
	if err != nil || string(bak) != "{}" {
		t.Fatalf("backup incomplete: %v %s", err, bak)
	}
	if _, err := writeInitConfig(filepath.Join(dir, "missing-parent", "config.json"), []byte("{}"), false); err == nil {
		t.Fatal("missing parent allowed")
	}
	if _, err := writeInitConfig(dir, []byte("{}"), true); err == nil {
		t.Fatal("directory overwrite allowed")
	}
}

func TestInitSymlinkOutputNeverModified(t *testing.T) {
	dir, _ := createInitProject(t)
	original := filepath.Join(dir, "original.json")
	if err := os.WriteFile(original, []byte("existing"), 0600); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(dir, "distroplane.json")
	if err := os.Symlink(original, alias); err != nil {
		if runtime.GOOS == "windows" {
			t.Skipf("symlink privileges unavailable: %v", err)
		}
		t.Fatal(err)
	}
	_, diagnostic := invokeInit(t, exitInvalid, "", false, append(initArgs(dir, "npm"), "--force")...)
	if !strings.Contains(diagnostic, "unsafe_output") {
		t.Fatalf("symlink followed: %s", diagnostic)
	}
	data, err := os.ReadFile(original)
	if err != nil || string(data) != "existing" {
		t.Fatalf("original file changed: %v %s", err, data)
	}
}

func TestInitNoninteractiveWithNoArtifactCannotPrompt(t *testing.T) {
	dir := t.TempDir()
	args := []string{"--output", filepath.Join(dir, "config.json"), "--destination", "npm", "--release", "v1", "--non-interactive", "--json"}
	var stdout, stderr bytes.Buffer
	if code := runInitContext(context.Background(), args, strings.NewReader("release.zip\n"), true, &stdout, &stderr); code != exitInvalid {
		t.Fatalf("noninteractive mode prompted: code=%d", code)
	}
	if !strings.Contains(stderr.String(), "missing_artifact") {
		t.Fatalf("no explicit artifact failure: %s", stderr.String())
	}
}
