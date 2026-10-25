//go:build darwin

package homebrew

import (
	"archive/zip"
	"context"
	"encoding/json"
	"crypto/sha256"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aalsanie/distroplane/internal/protocol"
)

func nativeCommand(t *testing.T, dir string, env []string, program string, args ...string) string {
	t.Helper()
	cmd := exec.Command(program, args...)
	cmd.Dir = dir
	cmd.Env = env
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%s %v: %v\n%s", program, args, err, output)
	}
	return strings.TrimSpace(string(output))
}

func TestHomebrewJVMDistributionInstallsAndRunsWithRuntimeJars(t *testing.T) {
	// A separate macOS CI job opts in to avoid modifying developers' Homebrew
	// installations during ordinary tests.
	if os.Getenv("DISTROPLANE_BREW_JVM_INSTALL_TEST") != "1" {
		t.Skip("requires opt-in native Homebrew integration")
	}
	for _, command := range []string{"brew", "ruby", "java", "javac", "jar", "git"} {
		if _, err := exec.LookPath(command); err != nil {
			t.Fatalf("native Homebrew integration requires %s: %v", command, err)
		}
	}
	javaHome := os.Getenv("JAVA_HOME")
	if javaHome == "" {
		javaHome = nativeCommand(t, "", os.Environ(), "/usr/libexec/java_home")
	}
	if _, err := os.Stat(filepath.Join(javaHome, "bin", "java")); err != nil {
		t.Fatalf("Java home must expose a working runtime: %v", err)
	}
	dir := t.TempDir()
	src := filepath.Join(dir, "src")
	classes := filepath.Join(dir, "classes")
	libraries := filepath.Join(dir, "libraries")
	for _, folder := range []string{src, classes, libraries} {
		if err := os.MkdirAll(folder, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	helper := `public class DemoSupport {
  public static String message() { return "JVM_DISTRIBUTION_OK"; }
}
`
	main := `public class DemoMain {
  public static void main(String[] args) {
    System.out.println(DemoSupport.message());
  }
}
`
	if err := os.WriteFile(filepath.Join(src, "DemoSupport.java"), []byte(helper), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "DemoMain.java"), []byte(main), 0o600); err != nil {
		t.Fatal(err)
	}
	nativeCommand(t, dir, os.Environ(), "javac", "-d", classes,
		filepath.Join(src, "DemoSupport.java"), filepath.Join(src, "DemoMain.java"))
	appJar := filepath.Join(libraries, "demo-app.jar")
	dependencyJar := filepath.Join(libraries, "demo-support.jar")
	nativeCommand(t, dir, os.Environ(), "jar", "--create", "--file", appJar, "-C", classes, "DemoMain.class")
	nativeCommand(t, dir, os.Environ(), "jar", "--create", "--file", dependencyJar, "-C", classes, "DemoSupport.class")
	appBytes, err := os.ReadFile(appJar)
	if err != nil {
		t.Fatal(err)
	}
	helperBytes, err := os.ReadFile(dependencyJar)
	if err != nil {
		t.Fatal(err)
	}
	launcher := `#!/bin/sh
set -eu
APP_HOME=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
if [ -n "${JAVA_HOME:-}" ]; then
  exec "$JAVA_HOME/bin/java" -cp "$APP_HOME/lib/*" DemoMain "$@"
else
  exec java -cp "$APP_HOME/lib/*" DemoMain "$@"
fi
`
	archivePath := filepath.Join(dir, "demo-1.2.3.zip")
	archiveFile, err := os.Create(archivePath)
	if err != nil {
		t.Fatal(err)
	}
	archive := zip.NewWriter(archiveFile)
	members := []struct {
		name string
		data []byte
		mode os.FileMode
	}{
		{"demo-1.2.3/bin/demo", []byte(launcher), 0o755},
		{"demo-1.2.3/lib/demo-app.jar", appBytes, 0o644},
		{"demo-1.2.3/lib/demo-support.jar", helperBytes, 0o644},
		{"demo-1.2.3/README.md", []byte("Gradle-style application archive"), 0o644},
	}
	for _, member := range members {
		header := &zip.FileHeader{Name: member.name, Method: zip.Deflate}
		header.SetMode(member.mode)
		writer, err := archive.CreateHeader(header)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := writer.Write(member.data); err != nil {
			t.Fatal(err)
		}
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	if err := archiveFile.Close(); err != nil {
		t.Fatal(err)
	}
	bytes, err := os.ReadFile(archivePath)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(bytes)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/demo-1.2.3.zip" {
			http.NotFound(w, request)
			return
		}
		w.Header().Set("Content-Type", "application/zip")
		_, _ = w.Write(bytes)
	}))
	defer server.Close()

	repo := newRepo(t)
	cfg := jvmFormulaConfiguration(repo.remote, "direct")
	cfg.Manifest.Name = "DistroplaneJvmFixture"
	cfg.Manifest.Binary = "demo"
	cfg.Manifest.URL = server.URL + "/demo-1.2.3.zip"
	cfg.Path = "Formula/distroplane-jvm-fixture.rb"
	request := planRequest(t, cfg)
	request.Release.Artifacts = []protocol.Artifact{{
		Name: "archive", Size: int64(len(bytes)), Digest: fmt.Sprintf("sha256:%x", digest),
	}}
	response, providerErr := (Provider{}).Plan(context.Background(), request)
	if providerErr != nil || len(response.Operations) != 1 {
		t.Fatalf("JVM fixture planning failed: %+v error=%+v", response, providerErr)
	}
	var payload operationPayload
	if err := json.Unmarshal(response.Operations[0].ProviderPayload, &payload); err != nil {
		t.Fatal(err)
	}
	if payload.ArtifactDigest != request.Release.Artifacts[0].Digest ||
		!strings.Contains(payload.Content, `depends_on "openjdk@21"`) {
		t.Fatalf("invalid checksum or Java dependency: %s", payload.Content)
	}
	formulaFile := filepath.Join(dir, "distroplane-jvm-fixture.rb")
	if err := os.WriteFile(formulaFile, []byte(payload.Content), 0o644); err != nil {
		t.Fatal(err)
	}
	nativeCommand(t, dir, os.Environ(), "ruby", "-c", formulaFile)
	brewEnv := append(os.Environ(), "HOMEBREW_NO_AUTO_UPDATE=1", "HOMEBREW_NO_ANALYTICS=1",
		"HOMEBREW_NO_INSTALL_CLEANUP=1", "HOMEBREW_NO_ENV_HINTS=1",
		"HOMEBREW_NO_INSTALL_FROM_API=1", "JAVA_HOME="+javaHome)
	const formulaName = "distroplane-jvm-fixture"
	t.Cleanup(func() {
		cmd := exec.Command("brew", "uninstall", "--force", formulaName)
		cmd.Env = brewEnv
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Logf("Homebrew fixture cleanup: %v %s", err, output)
		}
	})
	nativeCommand(t, dir, brewEnv, "brew", "install", "--formula", "--build-from-source",
		"--ignore-dependencies", formulaFile)
	prefix := nativeCommand(t, dir, brewEnv, "brew", "--prefix", formulaName)
	for _, relative := range []string{"libexec/bin/demo", "libexec/lib/demo-app.jar", "libexec/lib/demo-support.jar"} {
		if _, err := os.Stat(filepath.Join(prefix, relative)); err != nil {
			t.Fatalf("Homebrew dropped required distribution file %s: %v", relative, err)
		}
	}
	output := nativeCommand(t, dir, brewEnv, filepath.Join(prefix, "bin", "demo"))
	if output != "JVM_DISTRIBUTION_OK" {
		t.Fatalf("Homebrew-installed CLI failed to load sibling runtime JAR: %q", output)
	}
	// The same immutable formula is published to the test tap and should
	// reconcile idempotently without a second publication.
	applied, providerErr := (Provider{}).Apply(context.Background(), applyRequest(payload))
	if providerErr != nil || applied.Result.State != protocol.ResultPublished {
		t.Fatalf("fixture publication failed: %+v error=%+v", applied, providerErr)
	}
	again, providerErr := (Provider{}).Apply(context.Background(), applyRequest(payload))
	if providerErr != nil || again.Result.ProviderState != "already-applied" {
		t.Fatalf("fixture idempotency failed: %+v error=%+v", again, providerErr)
	}
	reconciled, providerErr := (Provider{}).Reconcile(context.Background(), reconcileRequest(payload))
	if providerErr != nil || reconciled.Result.State != protocol.ResultPublished {
		t.Fatalf("fixture reconciliation failed: %+v error=%+v", reconciled, providerErr)
	}
}
