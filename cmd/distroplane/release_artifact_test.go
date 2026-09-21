package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestReleaseArtifactsExecuteGitProviderThroughCLI(t *testing.T) {
	dist := os.Getenv("DISTROPLANE_RELEASE_SMOKE_DIR")
	if dist == "" {
		t.Skip("release artifact smoke is not configured")
	}
	version := os.Getenv("DISTROPLANE_RELEASE_SMOKE_VERSION")
	commit := os.Getenv("DISTROPLANE_RELEASE_SMOKE_COMMIT")
	if version == "" || commit == "" {
		t.Fatal("release artifact smoke version and commit are required")
	}
	if _, err := exec.LookPath("git"); err != nil {
		t.Fatal("git is required for release artifact smoke")
	}

	extension := ""
	if runtime.GOOS == "windows" {
		extension = ".exe"
	}
	suffix := fmt.Sprintf("%s_%s_%s%s", version, runtime.GOOS, runtime.GOARCH, extension)
	cliAsset := filepath.Join(dist, "distroplane_"+suffix)
	providerAsset := filepath.Join(dist, "distroplane-provider-homebrew_"+suffix)
	for _, path := range []string{cliAsset, providerAsset} {
		if info, err := os.Stat(path); err != nil || info.IsDir() {
			t.Fatalf("release artifact %q is unavailable: %v", path, err)
		}
	}

	bin := t.TempDir()
	cli := filepath.Join(bin, "distroplane"+extension)
	provider := filepath.Join(bin, "distroplane-provider-homebrew"+extension)
	copyReleaseExecutable(t, cliAsset, cli)
	copyReleaseExecutable(t, providerAsset, provider)
	pathValue := bin + string(os.PathListSeparator) + os.Getenv("PATH")

	versionOutput := runReleaseCLI(t, cli, pathValue, "version", "--json")
	var identity struct {
		Version string `json:"version"`
		Commit  string `json:"commit"`
	}
	if err := json.Unmarshal([]byte(versionOutput), &identity); err != nil {
		t.Fatalf("decode release identity: %v: %s", err, versionOutput)
	}
	if identity.Version != version || identity.Commit != commit {
		t.Fatalf("release identity=%+v want version=%q commit=%q", identity, version, commit)
	}

	root := t.TempDir()
	repository := filepath.Join(root, "tap.git")
	runReleaseGit(t, "", "init", "--bare", repository)
	seed := filepath.Join(root, "seed")
	runReleaseGit(t, "", "init", seed)
	runReleaseGit(t, seed, "config", "user.name", "Distroplane Release Smoke")
	runReleaseGit(t, seed, "config", "user.email", "release-smoke@example.invalid")
	if err := os.WriteFile(filepath.Join(seed, "README.md"), []byte("release smoke\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runReleaseGit(t, seed, "add", "README.md")
	runReleaseGit(t, seed, "commit", "-m", "seed")
	runReleaseGit(t, seed, "branch", "-M", "main")
	runReleaseGit(t, seed, "remote", "add", "origin", repository)
	runReleaseGit(t, seed, "push", "-u", "origin", "main")

	artifact := filepath.Join(root, "acme.tar.gz")
	if err := os.WriteFile(artifact, []byte("candidate artifact bytes\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(root, "distroplane.json")
	config := map[string]any{
		"schemaVersion": "1",
		"release": map[string]any{
			"id": "acme-1.2.3",
			"artifacts": []map[string]any{{
				"name": "archive", "source": artifact, "mediaType": "application/gzip",
			}},
		},
		"targets": []map[string]any{{
			"id":       "homebrew",
			"provider": map[string]any{"name": "homebrew"},
			"configuration": map[string]any{
				"artifact":   "archive",
				"repository": repository,
				"branch":     "main",
				"path":       "Formula/acme.rb",
				"mode":       "direct",
				"manifest": map[string]any{
					"type":        "formula",
					"name":        "Acme",
					"description": "Acme command-line tool",
					"homepage":    "https://example.invalid/acme",
					"url":         "https://example.invalid/acme-1.2.3.tar.gz",
					"version":     "1.2.3",
					"binary":      "acme",
				},
			},
		}},
	}
	raw, err := json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath, raw, 0o600); err != nil {
		t.Fatal(err)
	}

	stateDir := filepath.Join(root, "state")
	planOutput := runReleaseCLI(t, cli, pathValue,
		"plan", "--config", configPath, "--state-dir", stateDir, "--json")
	var planned struct {
		PlanID string `json:"planId"`
		Path   string `json:"path"`
	}
	if err := json.Unmarshal([]byte(planOutput), &planned); err != nil {
		t.Fatalf("decode plan output: %v: %s", err, planOutput)
	}
	if planned.PlanID == "" || planned.Path == "" {
		t.Fatalf("plan output=%s", planOutput)
	}

	journal := filepath.Join(root, "run.journal")
	applyOutput := runReleaseCLI(t, cli, pathValue,
		"apply", "--config", configPath, "--plan", planned.Path, "--journal", journal, "--json")
	var applied struct {
		Completed bool `json:"completed"`
	}
	if err := json.Unmarshal([]byte(applyOutput), &applied); err != nil {
		t.Fatalf("decode apply output: %v: %s", err, applyOutput)
	}
	if !applied.Completed {
		t.Fatalf("apply output=%s", applyOutput)
	}
	if info, err := os.Stat(journal); err != nil || info.Size() == 0 {
		t.Fatalf("candidate CLI did not persist a journal: %v", err)
	}

	manifest := runReleaseGit(t, "", "--git-dir", repository, "show", "refs/heads/main:Formula/acme.rb")
	if !strings.Contains(manifest, "class Acme < Formula") || !strings.Contains(manifest, "version \"1.2.3\"") {
		t.Fatalf("published manifest=%s", manifest)
	}
}

func copyReleaseExecutable(t *testing.T, source, destination string) {
	t.Helper()
	data, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(destination, data, 0o700); err != nil {
		t.Fatal(err)
	}
}

func runReleaseCLI(t *testing.T, executable, pathValue string, args ...string) string {
	t.Helper()
	command := exec.Command(executable, args...)
	command.Env = replaceReleaseEnvironment(os.Environ(), "PATH", pathValue)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("%s %v: %v\n%s", executable, args, err, output)
	}
	return strings.TrimSpace(string(output))
}

func runReleaseGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	command := exec.Command("git", args...)
	command.Dir = dir
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, output)
	}
	return strings.TrimSpace(string(output))
}

func replaceReleaseEnvironment(values []string, key, value string) []string {
	result := make([]string, 0, len(values)+1)
	prefix := key + "="
	for _, item := range values {
		if runtime.GOOS == "windows" {
			name, _, ok := strings.Cut(item, "=")
			if ok && strings.EqualFold(name, key) {
				continue
			}
		} else if strings.HasPrefix(item, prefix) {
			continue
		}
		result = append(result, item)
	}
	return append(result, key+"="+value)
}
