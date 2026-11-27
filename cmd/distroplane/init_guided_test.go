package main

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aalsanie/distroplane/internal/config"
	"github.com/aalsanie/distroplane/internal/frontend/official"
)

func TestInitCorrectsOnlyInvalidFieldsAndSelectsNumberedArtifact(t *testing.T) {
	dir, artifact := createInitProject(t)
	second := filepath.Join(dir, "target", "second é.zip")
	if err := os.WriteFile(second, []byte("another"), 0600); err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(dir, "guided.json")
	const canary = "literal-secret-canary"
	input := strings.Join([]string{"unknown", "sdkman", "", "v1", "99", "missing.zip", "1", "bad/name", "sample", "1.2.3", "https://user:"+canary+"@example.org/file.zip", "https://example.org/file.zip"}, "\n") + "\n"
	var stdout, stderr bytes.Buffer
	if code := runInitContext(context.Background(), []string{"--output", output}, strings.NewReader(input), true, &stdout, &stderr); code != exitOK {
		t.Fatalf("init: %d %s", code, stderr.String())
	}
	loaded, err := official.Load(output)
	if err != nil || loaded.Config.Release.Artifacts[0].Source != filepath.ToSlash(filepath.Join("target", filepath.Base(artifact))) {
		t.Fatalf("artifact choice: %v %+v", err, loaded.Config)
	}
	data, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stderr.String(), "Invalid answer") || !strings.Contains(stderr.String(), "1. target/release.tgz") || !strings.Contains(stderr.String(), "bytes)") {
		t.Fatalf("missing correction or candidate display: %s", stderr.String())
	}
	if strings.Contains(stdout.String()+stderr.String()+string(data), canary) || !strings.Contains(stdout.String(), "SDKMAN_VENDOR_EMAIL") {
		t.Fatal("secret exposed or credential instructions absent")
	}
	if strings.Count(stderr.String(), "sdkman.version (") != 1 {
		t.Fatalf("valid version prompt was unexpectedly repeated: %s", stderr.String())
	}
}

func TestInitFieldValidationAndNestedFilePaths(t *testing.T) {
	cases := []struct {
		key, good, bad, destination, kind string
	}{
		{"sdkman.url", "https://example.org/release.zip", "http://example.org/release.zip", "sdkman", ""},
		{"sdkman.candidate", "example", strings.Repeat("a", 129), "sdkman", ""},
		{"sdkman.version", "1.2.3", "a/b", "sdkman", ""},
		{"homebrew.repository", "https://github.com/example/tap", "https://secret@example.org/tap", "homebrew:formula", ""},
		{"winget.repository", "https://github.com/example/fork", "-bad", "winget:zip", ""},
		{"homebrew.pr-repository", "owner/tap", "owner", "homebrew:formula", ""},
		{"winget.pr-repository", "microsoft/winget-pkgs", "owner/tap/extra", "winget:zip", ""},
		{"homebrew.path", "Formula/tool.rb", "../tool.rb", "homebrew:formula", ""},
		{"homebrew.name", "MyTool", "my-tool", "homebrew:formula", ""},
		{"homebrew.name", "my-tool", "MyTool", "homebrew:cask", ""},
		{"homebrew.binary", "tool", "bin/tool", "homebrew:formula", ""},
		{"homebrew.binary", "tool", "tool name", "homebrew:jvm", ""},
		{"homebrew.java-version", "21", "22", "homebrew:jvm", ""},
		{"homebrew.install-kind", "app", "unknown", "homebrew:cask", ""},
		{"homebrew.install-source", "MyTool.app", "../MyTool.app", "homebrew:cask", ""},
		{"winget.identifier", "Example.Tool", "example", "winget:zip", ""},
		{"winget.version", "1.2.3", "a/b", "winget:zip", ""},
		{"winget.publisher", "Example", strings.Repeat("a", 513), "winget:zip", ""},
		{"winget.architecture", "arm64", "aarch64", "winget:zip", ""},
		{"winget.nested-type", "portable", "zip", "winget:zip", ""},
		{"winget.nested-files", "bin/tool.exe", "../tool.exe", "winget:zip", "portable"},
	}
	for _, tc := range cases {
		t.Run(tc.destination+"/"+tc.key, func(t *testing.T) {
			if err := validateInitField(tc.key, tc.good, tc.destination, tc.kind); err != nil {
				t.Fatal(err)
			}
			if err := validateInitField(tc.key, tc.bad, tc.destination, tc.kind); err == nil {
				t.Fatal("invalid value accepted")
			}
			if err := validateInitField(tc.key, "", tc.destination, tc.kind); err == nil {
				t.Fatal("empty value accepted")
			}
		})
	}
	for _, tc := range []struct{ value, kind string }{
		{`[{"relativeFilePath":"a.exe","relativeFilePath":"b.exe"}]`, "portable"},
		{`[]`, "portable"},
		{`[{"relativeFilePath":"a.exe"},{"relativeFilePath":"A.EXE"}]`, "portable"},
		{`[{"relativeFilePath":"a.exe","portableCommandAlias":"tool"},{"relativeFilePath":"b.exe","portableCommandAlias":"TOOL"}]`, "portable"},
		{`[{"relativeFilePath":"a.exe","unexpected":"x"}]`, "portable"},
		{`[{"relativeFilePath":"a.msi"},{"relativeFilePath":"b.msi"}]`, "msi"},
		{`[{"relativeFilePath":"a.msi","portableCommandAlias":"tool"}]`, "msi"},
		{"a.exe", "msi"},
		{"C:/a.exe", "portable"},
		{"", "portable"},
	} {
		if _, err := parseInitNestedFiles(tc.value, tc.kind); err == nil {
			t.Fatalf("unsafe nested layout accepted: %s", tc.value)
		}
	}
	if files, err := parseInitNestedFiles("bin/tool.msi", "msi"); err != nil || files[0]["relativeFilePath"] != "bin/tool.msi" {
		t.Fatalf("plain path: %v %v", files, err)
	}
}

type initBlockedInput struct {
	started chan struct{}
	release chan struct{}
}

func (r initBlockedInput) Read([]byte) (int, error) {
	close(r.started)
	<-r.release
	return 0, io.EOF
}

func TestInitCancellationAndEOFAtEverySDKMANPrompt(t *testing.T) {
	dir, _ := createInitProject(t)
	if err := os.WriteFile(filepath.Join(dir, "target", "second.zip"), []byte("other"), 0600); err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(dir, "existing.json")
	original := []byte("unchanged config")
	if err := os.WriteFile(output, original, 0600); err != nil {
		t.Fatal(err)
	}
	answers := []string{"sdkman", "v1", "1", "sample", "1.2.3", "https://example.org/release.zip"}
	for i := range answers {
		t.Run(answers[i], func(t *testing.T) {
			prefix := strings.Join(answers[:i], "\n")
			if prefix != "" {
				prefix += "\n"
			}
			args := []string{"--output", output, "--force"}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			blocked := initBlockedInput{started: make(chan struct{}), release: make(chan struct{})}
			defer close(blocked.release)
			var stdout, stderr bytes.Buffer
			result := make(chan int, 1)
			go func() {
				result <- runInitContext(ctx, args, io.MultiReader(strings.NewReader(prefix), blocked), true, &stdout, &stderr)
			}()
			select {
			case <-blocked.started:
			case <-time.After(3 * time.Second):
				t.Fatal("prompt did not reach blocked input")
			}
			cancel()
			select {
			case code := <-result:
				if code != exitOperational || !strings.Contains(stderr.String(), "cancelled") {
					t.Fatalf("cancellation: %d %s", code, stderr.String())
				}
			case <-time.After(3 * time.Second):
				t.Fatal("cancelled prompt remained blocked")
			}
			if code := runInitContext(context.Background(), args, strings.NewReader(prefix), true, io.Discard, io.Discard); code == exitOK {
				t.Fatal("EOF accepted at required prompt")
			}
			if data, err := os.ReadFile(output); err != nil || !bytes.Equal(data, original) {
				t.Fatalf("original configuration changed: %v", err)
			}
			if _, err := os.Stat(output+".bak"); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("backup created before completion: %v", err)
			}
		})
	}
}

func TestInitExplicitCredentialSourcesAndSecretRefusal(t *testing.T) {
	dir, _ := createInitProject(t)
	t.Setenv("EXPLICIT_NPM_TOKEN", "literal-secret-canary")
	args := append(initArgs(dir, "npm"), "--credential", "npm-publish=EXPLICIT_NPM_TOKEN")
	result, diagnostic := invokeInit(t, exitOK, "", false, args...)
	if len(result.CredentialSources) != 1 || !result.CredentialSources[0].Explicit || result.CredentialSources[0].Environment != "EXPLICIT_NPM_TOKEN" {
		t.Fatalf("explicit source not reported: %+v", result)
	}
	loaded, err := official.Load(result.ConfigPath)
	if err != nil || loaded.Config.CredentialSources["npm-publish"] != "EXPLICIT_NPM_TOKEN" {
		t.Fatalf("explicit source not saved: %v %+v", err, loaded.Config)
	}
	data, err := os.ReadFile(result.ConfigPath)
	if err != nil || strings.Contains(string(data)+diagnostic, "literal-secret-canary") {
		t.Fatal("credential value leaked")
	}
	for _, extra := range [][]string{
		{"--credential", "npm-publish=literal-secret-canary"},
		{"--credential", "unknown=EXPLICIT_NPM_TOKEN"},
		{"--set", "npm.registry=https://example.org/?token=literal-secret-canary"},
	} {
		path := filepath.Join(dir, "refused.json")
		var out, stderr bytes.Buffer
		args := append([]string{"--release", "v1", "--destination", "npm", "--output", path, "--json"}, extra...)
		if code := runInitContext(context.Background(), args, nil, false, &out, &stderr); code != exitInvalid || out.Len() != 0 || strings.Contains(stderr.String(), "literal-secret-canary") {
			t.Fatalf("unsafe input: code=%d %s", code, stderr.String())
		}
		if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatal("refused configuration written")
		}
	}
	other := filepath.Join(dir, "colliding.json")
	args = append([]string{"--release", "v1", "--destination", "sdkman", "--output", other, "--json",
		"--credential", "sdkman-vendor-email=SHARED_SOURCE", "--credential", "sdkman-vendor-password=shared_source"},
		optionFlags("sdkman.candidate=sample", "sdkman.version=1", "sdkman.url=https://example.org/file.zip")...)
	invokeInit(t, exitInvalid, "", false, args...)
	if _, err := os.Stat(other); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("colliding configuration written")
	}
	cfg, err := official.Decode(strings.NewReader(`{"schemaVersion":"1","release":{"id":"x","artifacts":[{"name":"app","source":"app.tgz"}]},"targets":[{"id":"npm","provider":{"name":"npm"},"configuration":{"authentication":{"mode":"trusted-publishing","credential":"publish"}}}]}`))
	if err != nil {
		t.Fatal(err)
	}
	sources, err := initSources(cfg, nil)
	if err != nil || len(sources) != 1 || sources[0].Environment != "" {
		t.Fatalf("OIDC source guessed: %+v %v", sources, err)
	}
	var summary bytes.Buffer
	writeInitSummary(&summary, initOutput{CredentialSources: sources})
	if !strings.Contains(summary.String(), "--credential publish=ENV") {
		t.Fatal("missing explicit OIDC mapping instruction")
	}
}

func TestInitDiscoveryDoesNotTraverseIntermediateSymlinks(t *testing.T) {
	root := t.TempDir()
	external := t.TempDir()
	if err := os.Mkdir(filepath.Join(external, "distributions"), 0700); err != nil {
		t.Fatal(err)
	}
	archive := filepath.Join(external, "distributions", "sample.zip")
	if err := os.WriteFile(archive, []byte("archive"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(external, filepath.Join(root, "build")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if found, err := discoverArtifacts(root); err != nil || len(found) != 0 {
		t.Fatalf("discovery traversed symlink: %v %v", found, err)
	}
	if _, _, err := inspectInitArtifact(root, "build/distributions/sample.zip"); err == nil {
		t.Fatal("explicit artifact traversed symlink")
	}
}

func TestInitPromptBoundsAndInvalidExplicitRegistry(t *testing.T) {
	if _, err := promptInit(bufio.NewReader(strings.NewReader(strings.Repeat("a", 5000))), io.Discard, "field"); !errors.Is(err, bufio.ErrBufferFull) {
		t.Fatalf("oversized prompt: %v", err)
	}
	cfg := config.Config{Targets: []config.Target{{Provider: config.Provider{Name: "npm"}, Configuration: []byte("invalid")}}}
	if err := validateInitRegistry(cfg); err == nil {
		t.Fatal("invalid registry accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := (initContextReader{ctx: ctx, input: strings.NewReader("x")}).Read(make([]byte, 1)); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled input read: %v", err)
	}
}
