//go:build windows

package winget

import (
	"archive/zip"
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aalsanie/distroplane/internal/protocol"
)

// Exercises a real Windows executable inside the exact ZIP whose digest is
// included in a complete WinGet submission. Does not require modifying the
// runner's system-wide installed packages.
func TestZIPPortableWindowsExecutableAndSubmission(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "fixture.go")
	if err := os.WriteFile(source, []byte(`package main
import "fmt"
func main() { fmt.Println("WIN_PACKAGE_FIXTURE_OK") }
`), 0o600); err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(dir, "winget-fixture.exe")
	build := exec.Command("go", "build", "-o", binary, source)
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build Windows fixture: %v %s", err, out)
	}
	executable, err := os.ReadFile(binary)
	if err != nil {
		t.Fatal(err)
	}
	archive, artifact := zipArtifact(t, map[string][]byte{
		"bin/winget-fixture.exe": executable,
		"README.md":              []byte("WinGet portable ZIP integration fixture"),
	})
	repo := newRepo(t)
	fixture, server := newAPIServer(t, "normal")
	defer server.Close()
	provider := authenticatedProvider(server.Client())
	cfg := baseConfiguration(repo, server)
	cfg.Installer = zipInstaller(archive, "portable", nestedInstallerFile{
		RelativeFilePath: "bin/winget-fixture.exe", PortableCommandAlias: "winget-fixture",
	})
	request := planRequest(t, cfg)
	request.Release.Artifacts = []protocol.Artifact{artifact}
	plan, providerErr := provider.Plan(context.Background(), request)
	if providerErr != nil || len(plan.Operations) != 1 {
		t.Fatalf("plan=%+v error=%v", plan, providerErr)
	}
	var payload operationPayload
	if err := json.Unmarshal(plan.Operations[0].ProviderPayload, &payload); err != nil {
		t.Fatal(err)
	}
	installerManifest := ""
	for _, f := range payload.Files {
		if strings.HasSuffix(f.Path, ".installer.yaml") {
			installerManifest = f.Content
		}
	}
	if !strings.Contains(installerManifest, "NestedInstallerType: portable") ||
		!strings.Contains(installerManifest, "PortableCommandAlias: \"winget-fixture\"") {
		t.Fatalf("invalid installer manifest: %s", installerManifest)
	}
	// A Windows native executable from the archive must run after extraction.
	zipReader, err := zip.OpenReader(archive)
	if err != nil {
		t.Fatal(err)
	}
	defer zipReader.Close()
	unpacked := filepath.Join(dir, "installed-winget-fixture.exe")
	found := false
	for _, zf := range zipReader.File {
		if zf.Name != "bin/winget-fixture.exe" {
			continue
		}
		input, err := zf.Open()
		if err != nil {
			t.Fatal(err)
		}
		out, err := os.Create(unpacked)
		if err != nil {
			input.Close()
			t.Fatal(err)
		}
		_, copyErr := io.Copy(out, input)
		closeErr := out.Close()
		input.Close()
		if copyErr != nil || closeErr != nil {
			t.Fatalf("extract fixture failed: copy=%v close=%v", copyErr, closeErr)
		}
		found = true
	}
	if !found {
		t.Fatal("selected nested installer missing from actual archive")
	}
	execute := exec.Command(unpacked)
	if output, err := execute.CombinedOutput(); err != nil ||
		strings.TrimSpace(string(output)) != "WIN_PACKAGE_FIXTURE_OK" {
		t.Fatalf("installed ZIP fixture did not execute: output=%s error=%v", output, err)
	}

	result, providerErr := provider.Apply(context.Background(), applyRequest(payload))
	if providerErr != nil || result.Result.State != protocol.ResultWaitingExternal {
		t.Fatalf("ZIP submission failed: result=%+v err=%v", result, providerErr)
	}
	fixture.mu.Lock()
	fixtureState := fixture.branch
	fixture.mu.Unlock()
	if fixtureState == "" {
		t.Fatal("ZIP submission did not create a manifest update branch")
	}

	// Validate the actual generated three-file set with the native WinGet
	// manifest validator when present on the Windows runner.
	if wingetBinary, err := exec.LookPath("winget"); err == nil {
		manifestDir := filepath.Join(dir, "manifests")
		if err := os.MkdirAll(manifestDir, 0o755); err != nil {
			t.Fatal(err)
		}
		for _, f := range payload.Files {
			if err := os.WriteFile(filepath.Join(manifestDir, filepath.Base(f.Path)), []byte(f.Content), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		check := exec.Command(wingetBinary, "validate", "--manifest", manifestDir)
		if output, err := check.CombinedOutput(); err != nil {
			// Some WinGet versions report a warning-only exit code for the
			// documented aka.ms editor schema headers. Accept exactly that
			// narrow outcome; schema errors or other warnings must still fail.
			message := string(output)
			expected := "Manifest Warning: The schema header URL does not match the expected pattern."
			if !strings.Contains(message, "Manifest validation succeeded with warnings.") ||
				strings.Count(message, "Manifest Warning:") != len(payload.Files) ||
				strings.Count(message, expected) != len(payload.Files) ||
				strings.Contains(message, "Manifest Error:") {
				t.Fatalf("winget validate failed: %v\n%s", err, output)
			}
			t.Logf("winget validated manifest content with editor-only header warnings: %s", message)
		}
	} else {
		t.Log("winget validate unavailable on runner; native executable extraction and publication completed")
	}
}
