package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/aalsanie/distroplane/internal/config"
)

func createInitProject(t *testing.T) (string, string) {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "target"), 0700); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(dir, "target", "release.tgz")
	if err := os.WriteFile(file, []byte("archive-fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	return dir, file
}

func invokeInit(t *testing.T, want int, input string, interactive bool, args ...string) (initOutput, string) {
	t.Helper()
	var out, diagnostic bytes.Buffer
	code := runInitContext(context.Background(), args, strings.NewReader(input), interactive, &out, &diagnostic)
	if code != want {
		t.Fatalf("init %v: code=%d want=%d stdout=%s stderr=%s", args, code, want, out.String(), diagnostic.String())
	}
	var result initOutput
	if want == exitOK && strings.Contains(strings.Join(args, " "), "--json") {
		if err := json.Unmarshal(out.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
	}
	return result, diagnostic.String()
}

func initArgs(dir, dest string) []string {
	return []string{"--output", filepath.Join(dir, "distroplane.json"), "--destination", dest, "--release", "v1.2.3", "--json"}
}

func optionFlags(values ...string) []string {
	var out []string
	for _, value := range values {
		out = append(out, "--set", value)
	}
	return out
}

func TestInitGeneratesAllOfficialVariantTemplates(t *testing.T) {
	cases := []struct {
		name   string
		fields []string
		kind   string
	}{
		{"npm", nil, "token"},
		{"sdkman", []string{"sdkman.candidate=sample", "sdkman.version=1.2.3", "sdkman.url=https://example.invalid/sample.zip"}, "state"},
		{"homebrew:formula", []string{"homebrew.repository=https://github.com/example/homebrew-tap.git", "homebrew.path=Formula/sample.rb", "homebrew.name=Sample", "homebrew.description=Sample CLI", "homebrew.homepage=https://example.invalid/", "homebrew.url=https://example.invalid/release.tgz", "homebrew.version=1.2.3", "homebrew.binary=sample"}, "formula"},
		{"homebrew:cask", []string{"homebrew.repository=https://github.com/example/homebrew-tap.git", "homebrew.path=Casks/sample.rb", "homebrew.name=Sample", "homebrew.description=Sample App", "homebrew.homepage=https://example.invalid/", "homebrew.url=https://example.invalid/release.zip", "homebrew.version=1.2.3", "homebrew.install-kind=app", "homebrew.install-source=Sample.app"}, "cask"},
		{"homebrew:jvm", []string{"homebrew.repository=https://github.com/example/homebrew-tap.git", "homebrew.path=Formula/sample.rb", "homebrew.name=Sample", "homebrew.description=Sample JVM CLI", "homebrew.homepage=https://example.invalid/", "homebrew.url=https://example.invalid/release.zip", "homebrew.version=1.2.3", "homebrew.binary=sample", "homebrew.java-version=21"}, "jvm-distribution"},
		{"winget:msi", []string{"winget.repository=https://github.com/example/winget-pkgs.git", "winget.identifier=Example.Sample", "winget.version=1.2.3", "winget.publisher=Example", "winget.name=Sample", "winget.license=MIT", "winget.description=Sample CLI", "winget.url=https://example.invalid/release.msi", "winget.architecture=x64", "winget.pr-repository=microsoft/winget-pkgs"}, "msi"},
		{"winget:zip", []string{"winget.repository=https://github.com/example/winget-pkgs.git", "winget.identifier=Example.Sample", "winget.version=1.2.3", "winget.publisher=Example", "winget.name=Sample", "winget.license=MIT", "winget.description=Sample CLI", "winget.url=https://example.invalid/release.zip", "winget.architecture=x64", "winget.pr-repository=microsoft/winget-pkgs", "winget.nested-type=portable", `winget.nested-files=[{"relativeFilePath":"bin/sample.exe","portableCommandAlias":"sample"}]`}, "zip"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			dir, artifactPath := createInitProject(t)
			extension := ""
			switch test.name {
			case "homebrew:jvm", "winget:zip":
				extension = ".zip"
			case "winget:msi":
				extension = ".msi"
			}
			if extension != "" {
				if err := os.Remove(artifactPath); err != nil {
					t.Fatal(err)
				}
				artifactPath = filepath.Join(dir, "target", "release"+extension)
				if err := os.WriteFile(artifactPath, []byte("archive-fixture"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			args := append(initArgs(dir, test.name), optionFlags(test.fields...)...)
			result, _ := invokeInit(t, exitOK, "", false, args...)
			if result.OutputSchemaVersion != outputSchemaVersion || result.Artifact.SHA256 == "" || len(result.Destinations) != 1 {
				t.Fatalf("invalid output: %+v", result)
			}
			data, err := os.ReadFile(result.ConfigPath)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(data), "SECRET") || !bytes.Contains(data, []byte(`"schemaVersion": "2"`)) {
				t.Fatalf("unsafe generated config: %s", data)
			}
			loaded, err := config.Load(result.ConfigPath)
			if err != nil || len(loaded.Config.Targets) != 1 {
				t.Fatalf("invalid generated v2 config: %v %+v", err, loaded.Config.Targets)
			}
			var options map[string]any
			if err := json.Unmarshal(loaded.Config.Targets[0].Configuration, &options); err != nil {
				t.Fatal(err)
			}
			switch test.name {
			case "npm":
				if options["registry"] != "https://registry.npmjs.org/" {
					t.Fatalf("npm default: %v", options)
				}
				if options["packagePath"] != result.Artifact.Path {
					t.Fatalf("npm package path: %v", options)
				}
			case "sdkman":
				if options["authentication"].(map[string]any)["mode"] != "state" {
					t.Fatal("SDKMAN state default missing")
				}
			case "homebrew:jvm":
				m := options["manifest"].(map[string]any)
				if m["installLayout"] != "jvm-distribution" || m["javaVersion"] != "21" {
					t.Fatalf("JVM install metadata: %v", m)
				}
			case "winget:zip":
				installer := options["installer"].(map[string]any)
				if installer["archivePath"] != result.Artifact.Path {
					t.Fatalf("ZIP archive path not normalized: %v", installer)
				}
				if installer["nestedInstallerType"] != "portable" {
					t.Fatalf("ZIP metadata: %v", installer)
				}
			}
		})
	}
}

func TestInitMultiDestinationSharedArtifact(t *testing.T) {
	dir, _ := createInitProject(t)
	base := initArgs(dir, "npm")
	base = append(base, "--destination", "sdkman", "--destination", "homebrew:formula")
	flags := optionFlags(
		"sdkman.candidate=sample", "sdkman.version=1.2.3", "sdkman.url=https://example.invalid/s.zip",
		"homebrew.repository=https://github.com/example/homebrew-tap.git", "homebrew.path=Formula/sample.rb",
		"homebrew.name=Sample", "homebrew.description=Sample CLI", "homebrew.homepage=https://example.invalid/",
		"homebrew.url=https://example.invalid/s.tar.gz", "homebrew.version=1.2.3", "homebrew.binary=sample",
	)
	result, _ := invokeInit(t, exitOK, "", false, append(base, flags...)...)
	loaded, err := config.Load(result.ConfigPath)
	if err != nil || len(loaded.Config.Targets) != 3 || len(loaded.Config.Release.Artifacts) != 1 {
		t.Fatalf("multi-destination artifact mismatch: %+v %v", loaded.Config, err)
	}
	for _, target := range loaded.Config.Targets {
		var payload map[string]any
		if err := json.Unmarshal(target.Configuration, &payload); err != nil {
			t.Fatal(err)
		}
		if payload["artifact"] != "release" {
			t.Fatalf("target %s uses different artifact", target.ID)
		}
	}
}

func TestInitArtifactDiscoveryAndAmbiguity(t *testing.T) {
	dir, candidate := createInitProject(t)
	output := filepath.Join(dir, "distroplane.json")
	_, err := discoverArtifacts(dir)
	if err != nil {
		t.Fatal(err)
	}
	args := initArgs(dir, "npm")
	result, _ := invokeInit(t, exitOK, "", false, args...)
	if result.Artifact.Path != candidate {
		t.Fatalf("single candidate not selected: %+v", result.Artifact)
	}
	if err := os.Remove(output); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "build", "distributions"), 0700); err != nil {
		t.Fatal(err)
	}
	second := filepath.Join(dir, "build", "distributions", "another.zip")
	if err := os.WriteFile(second, []byte("second"), 0600); err != nil {
		t.Fatal(err)
	}
	_, diagnostic := invokeInit(t, exitInvalid, "", false, args...)
	if !strings.Contains(diagnostic, "ambiguous_artifact") || !strings.Contains(diagnostic, "build/distributions/another.zip") {
		t.Fatalf("ambiguous candidates not reported: %s", diagnostic)
	}
	args = append(initArgs(dir, "npm"), "--artifact", candidate)
	result, _ = invokeInit(t, exitOK, "", false, args...)
	if result.Artifact.Path != candidate {
		t.Fatal("explicit artifact ignored")
	}
}

func TestInitRejectsStaleAndUnsafeCandidates(t *testing.T) {
	dir, candidate := createInitProject(t)
	old := time.Now().Add(-90 * 24 * time.Hour)
	if err := os.Chtimes(candidate, old, old); err != nil {
		t.Fatal(err)
	}
	_, diag := invokeInit(t, exitInvalid, "", false, initArgs(dir, "npm")...)
	if !strings.Contains(diag, "missing_artifact") {
		t.Fatalf("stale artifact selected: %s", diag)
	}
	explicit := append(initArgs(dir, "npm"), "--artifact", candidate)
	result, _ := invokeInit(t, exitOK, "", false, explicit...)
	if result.Artifact.Path != candidate {
		t.Fatal("explicit old file rejected")
	}
	link := filepath.Join(dir, "dist", "shortcut.zip")
	if err := os.MkdirAll(filepath.Dir(link), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(candidate, link); err == nil {
		if _, _, err := inspectInitArtifact(dir, link); err == nil {
			t.Fatal("symlink artifact accepted")
		}
	}
	for _, name := range []string{"readme.txt", ".hidden", "output.json"} {
		if candidateExtension(name) {
			t.Fatalf("not an artifact: %s", name)
		}
	}
	for _, name := range []string{"app.tgz", "app.tar.gz", "dist.zip", "setup.msi", "setup.exe", "app.jar"} {
		if !candidateExtension(name) {
			t.Fatalf("artifact ignored: %s", name)
		}
	}
}

func TestInitNeverOverwritesWithoutBackup(t *testing.T) {
	dir, _ := createInitProject(t)
	output := filepath.Join(dir, "distroplane.json")
	old := []byte("existing-user-config")
	if err := os.WriteFile(output, old, 0600); err != nil {
		t.Fatal(err)
	}
	_, diagnostic := invokeInit(t, exitInvalid, "", false, initArgs(dir, "npm")...)
	if !strings.Contains(diagnostic, "config_exists") {
		t.Fatalf("config overwrite accepted: %s", diagnostic)
	}
	retained, _ := os.ReadFile(output)
	if !bytes.Equal(retained, old) {
		t.Fatal("original modified")
	}
	result, _ := invokeInit(t, exitOK, "", false, append(initArgs(dir, "npm"), "--force")...)
	if result.BackupPath != output+".bak" {
		t.Fatalf("backup not returned: %+v", result)
	}
	bak, err := os.ReadFile(result.BackupPath)
	if err != nil || !bytes.Equal(bak, old) {
		t.Fatalf("backup mismatch: %v", err)
	}
	next, err := os.ReadFile(output)
	if err != nil || bytes.Equal(next, old) {
		t.Fatalf("new config not saved: %v", err)
	}
	_, diagnostic = invokeInit(t, exitOperational, "", false, append(initArgs(dir, "npm"), "--force")...)
	if !strings.Contains(diagnostic, "backup") {
		t.Fatalf("backup overwritten: %s", diagnostic)
	}
}

func TestInitRejectsMissingMetadataInvalidInputsAndSecrets(t *testing.T) {
	cases := []struct {
		name     string
		extra    []string
		fragment string
	}{
		{"missing release", []string{"--destination", "npm"}, "missing_release"},
		{"no destination", []string{"--release", "v1"}, "invalid_destination"},
		{"unknown destination", []string{"--release", "v1", "--destination", "unknown"}, "invalid_destination"},
		{"duplicate destination", []string{"--release", "v1", "--destination", "npm", "--destination", "npm"}, "invalid_destination"},
		{"missing sdkman metadata", []string{"--release", "v1", "--destination", "sdkman"}, "missing_metadata"},
		{"missing homebrew metadata", []string{"--release", "v1", "--destination", "homebrew"}, "missing_metadata"},
		{"missing winget metadata", []string{"--release", "v1", "--destination", "winget"}, "missing_metadata"},
		{"bad npm auth", []string{"--release", "v1", "--destination", "npm", "--set", "npm.auth-mode=bogus"}, "invalid_template"},
		{"unknown metadata", []string{"--release", "v1", "--destination", "npm", "--set", "npm.password=hidden"}, "unknown_option"},
		{"duplicate metadata", []string{"--release", "v1", "--destination", "npm", "--set", "npm.registry=https://registry.npmjs.org/", "--set", "npm.registry=https://registry.npmjs.org/"}, "invalid_option"},
		{"bad flag syntax", []string{"--release", "v1", "--destination", "npm", "--set", "npm.registry"}, "invalid_option"},
		{"bad concurrency option", []string{"--release", "v1", "--destination", "npm", "--does-not-exist"}, "usage"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir, _ := createInitProject(t)
			args := append([]string{"--output", filepath.Join(dir, "distroplane.json"), "--json"}, tc.extra...)
			_, diagnostic := invokeInit(t, map[bool]int{true: exitUsage, false: exitInvalid}[tc.fragment == "invalid_destination" || tc.fragment == "missing_release" || tc.fragment == "invalid_option" || tc.fragment == "unknown_option" || tc.fragment == "usage"], "", false, args...)
			if !strings.Contains(diagnostic, tc.fragment) {
				t.Fatalf("diagnostic %q did not include %q", diagnostic, tc.fragment)
			}
			if strings.Contains(diagnostic, "hidden") && tc.fragment != "unknown_option" {
				t.Fatal("metadata content leaked")
			}
			if _, err := os.Stat(filepath.Join(dir, "distroplane.json")); !os.IsNotExist(err) {
				t.Fatalf("invalid template was written: %v", err)
			}
		})
	}
}

func TestInitInteractivePromptsAndNoninteractiveRefusal(t *testing.T) {
	dir, candidate := createInitProject(t)
	path := filepath.Join(dir, "distroplane.json")
	args := []string{"--output", path}
	_, diag := invokeInit(t, exitUsage, "", false, args...)
	if !strings.Contains(diag, "invalid_destination") {
		t.Fatalf("noninteractive guessed destination: %s", diag)
	}
	input := "npm\nrelease-interactive\n"
	var out, errOut bytes.Buffer
	code := runInitContext(context.Background(), args, strings.NewReader(input), true, &out, &errOut)
	if code != exitOK || !strings.Contains(out.String(), "configuration") || !strings.Contains(errOut.String(), "release ID") || !strings.Contains(errOut.String(), "SHA-256") {
		t.Fatalf("interactive init code=%d out=%s diag=%s", code, out.String(), errOut.String())
	}
	loaded, err := config.Load(path)
	if err != nil || loaded.Config.Release.ID != "release-interactive" || loaded.Config.Release.Artifacts[0].Source != filepath.ToSlash(filepath.Join("target", filepath.Base(candidate))) {
		t.Fatalf("interactive config invalid: %+v %v", loaded.Config.Release, err)
	}
}

func TestInitJSONAndExplicitArtifactOutsideProject(t *testing.T) {
	dir, _ := createInitProject(t)
	outer := t.TempDir()
	path := filepath.Join(outer, "package.tgz")
	if err := os.WriteFile(path, []byte("external-artifact"), 0600); err != nil {
		t.Fatal(err)
	}
	args := append(initArgs(dir, "npm"), "--artifact", path, "--non-interactive")
	out, _ := invokeInit(t, exitOK, "", false, args...)
	if out.Artifact.Path != path {
		t.Fatal("absolute artifact source changed")
	}
	got, err := config.Load(out.ConfigPath)
	if err != nil || got.Config.Release.Artifacts[0].Source != path {
		t.Fatalf("external source invalid: %+v %v", got.Config, err)
	}
}

func TestInitBoundedDiscoveryAndFileErrors(t *testing.T) {
	dir, _ := createInitProject(t)
	dist := filepath.Join(dir, "dist")
	if err := os.MkdirAll(dist, 0700); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < maxDiscoveryEntries+1; i++ {
		if err := os.WriteFile(filepath.Join(dist, fmt.Sprintf("%03d.txt", i)), []byte("unrelated"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	_, err := discoverArtifacts(dir)
	if err == nil || !strings.Contains(err.Error(), "too many entries") {
		t.Fatalf("oversized directory accepted: %v", err)
	}
	if _, _, err := inspectInitArtifact(dir, "missing.zip"); err == nil {
		t.Fatal("missing file accepted")
	}
	if _, _, err := inspectInitArtifact(dir, "target"); err == nil {
		t.Fatal("directory accepted")
	}
	if runtime.GOOS != "windows" {
		if _, _, err := inspectInitArtifact(dir, "/dev/null"); err == nil {
			t.Fatal("special file accepted")
		}
	}
}

func TestInitCancelledDoesNotWriteConfig(t *testing.T) {
	dir, _ := createInitProject(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var out, diagnostic bytes.Buffer
	args := initArgs(dir, "npm")
	if code := runInitContext(ctx, args, nil, false, &out, &diagnostic); code != exitOperational {
		t.Fatalf("cancellation code=%d", code)
	}
	if _, err := os.Stat(filepath.Join(dir, "distroplane.json")); !os.IsNotExist(err) {
		t.Fatalf("cancelled wrote config: %v", err)
	}
}
