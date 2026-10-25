package homebrew

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aalsanie/distroplane/internal/protocol"
)

func jvmFormulaConfiguration(repository, mode string) configuration {
	cfg := formulaConfiguration(repository, mode)
	cfg.Manifest.Binary = "demo"
	cfg.Manifest.InstallLayout = "jvm-distribution"
	cfg.Manifest.JavaVersion = "21"
	cfg.Manifest.URL = "https://example.test/demo-1.2.3.zip"
	return cfg
}

func TestJVMFormulaHasCompleteDistributionAndJavaRuntime(t *testing.T) {
	cfg := jvmFormulaConfiguration("/tmp/tap.git", "direct")
	if err := validateManifest(cfg.Manifest); err != nil {
		t.Fatal(err)
	}
	content, err := renderManifest(cfg.Manifest, artifactFixture())
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{
		`depends_on "openjdk@21"`,
		`roots = [Pathname.pwd]`,
		`(root/"bin"/"demo").file?`,
		`(root/"lib"/"*.jar")`,
		`libexec.install matches.first.children`,
		`(bin/"demo").write_env_script libexec/"bin"/"demo", Language::Java.overridable_java_home_env("21")`,
		`sha256 "` + strings.Repeat("a", 64) + `"`,
	} {
		if !strings.Contains(content, required) {
			t.Fatalf("missing %q from formula:\n%s", required, content)
		}
	}
	if strings.Contains(content, `bin.install "demo"`) {
		t.Fatalf("JVM layout incorrectly discarded libraries:\n%s", content)
	}
	second, err := renderManifest(cfg.Manifest, artifactFixture())
	if err != nil || content != second {
		t.Fatalf("JVM formula generation is not deterministic: %v", err)
	}
	if ruby, err := exec.LookPath("ruby"); err == nil {
		formula := filepath.Join(t.TempDir(), "demo.rb")
		if err := os.WriteFile(formula, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		cmd := exec.Command(ruby, "-c", formula)
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("invalid generated Ruby: %s: %v", output, err)
		}
	}
}

func TestJVMSingleBinaryAndCaskOutputIsUnchanged(t *testing.T) {
	cfg := formulaConfiguration("/tmp/tap.git", "direct")
	normal := renderFormula(cfg.Manifest, strings.Repeat("a", 64))
	if !strings.Contains(normal, `    bin.install "demo"`) ||
		strings.Contains(normal, "libexec") || strings.Contains(normal, "openjdk") {
		t.Fatalf("single-binary layout changed: %s", normal)
	}
	archive := cfg.Manifest
	archive.InstallLayout = "jvm-distribution"
	archive.JavaVersion = "17"
	archiveOutput := renderFormula(archive, strings.Repeat("a", 64))
	if !strings.Contains(archiveOutput, `depends_on "openjdk@17"`) ||
		!strings.Contains(archiveOutput, `overridable_java_home_env("17")`) {
		t.Fatalf("Java runtime mismatch: %s", archiveOutput)
	}
	cask := manifestConfig{
		Type: "cask", Name: "demo", Description: "Demo App",
		Homepage: "https://example.test", URL: "https://example.test/app.zip",
		Version: "1.2.3", InstallKind: "app", InstallSource: "Demo.app",
	}
	if err := validateManifest(cask); err != nil {
		t.Fatal(err)
	}
	output := renderCask(cask, strings.Repeat("a", 64))
	if strings.Contains(output, "libexec") || strings.Contains(output, "openjdk") {
		t.Fatalf("cask layout changed: %s", output)
	}
}

func TestJVMConfigurationFailsClosedForInvalidLayouts(t *testing.T) {
	base := jvmFormulaConfiguration("/tmp/tap.git", "direct")
	for name, mutate := range map[string]func(*manifestConfig){
		"unknown layout":           func(m *manifestConfig) { m.InstallLayout = "tarball" },
		"missing java version":     func(m *manifestConfig) { m.JavaVersion = "" },
		"unavailable java version": func(m *manifestConfig) { m.JavaVersion = "9" },
		"java injection":           func(m *manifestConfig) { m.JavaVersion = `21"; system("bad")` },
		"binary path":              func(m *manifestConfig) { m.Binary = "../bin/demo" },
		"binary whitespace":        func(m *manifestConfig) { m.Binary = "demo cli" },
		"binary interpolation":     func(m *manifestConfig) { m.Binary = `#{File.delete("x")}` },
		"binary dot":               func(m *manifestConfig) { m.Binary = ".." },
		"binary empty":             func(m *manifestConfig) { m.Binary = "" },
		"binary too long":          func(m *manifestConfig) { m.Binary = strings.Repeat("a", 65) },
		"java without layout":      func(m *manifestConfig) { m.InstallLayout = "" },
		"cask with JVM layout": func(m *manifestConfig) {
			m.Type = "cask"
			m.Name = "demo"
			m.Binary = ""
			m.InstallKind = "app"
			m.InstallSource = "Demo.app"
		},
	} {
		t.Run(name, func(t *testing.T) {
			cfg := base
			mutate(&cfg.Manifest)
			raw, _ := json.Marshal(cfg)
			_, err := parseConfiguration(raw)
			if err == nil {
				t.Fatal("invalid JVM distribution config was accepted")
			}
		})
	}
	for _, version := range []string{"8", "11", "17", "21", "25"} {
		if !supportedJavaVersion(version) {
			t.Fatalf("supported Java version rejected: %s", version)
		}
	}
}

func TestJVMPublicationDirectAndReconcileIsIdempotent(t *testing.T) {
	repo := newRepo(t)
	provider := Provider{}
	response, payload := plannedPayload(t, provider, jvmFormulaConfiguration(repo.remote, "direct"))
	if len(response.Operations) != 1 || len(response.Requirements) != 1 {
		t.Fatalf("unexpected plan: %+v", response)
	}
	expected := `depends_on "openjdk@21"`
	if !strings.Contains(payload.Content, expected) {
		t.Fatalf("missing Java dependency: %s", payload.Content)
	}
	first, err := provider.Apply(context.Background(), applyRequest(payload))
	if err != nil || first.Result.State != protocol.ResultPublished || first.Result.ProviderState != "applied" {
		t.Fatalf("first apply=%+v err=%+v", first, err)
	}
	again, err := provider.Apply(context.Background(), applyRequest(payload))
	if err != nil || again.Result.ProviderState != "already-applied" {
		t.Fatalf("repeated apply=%+v err=%+v", again, err)
	}
	reconciled, err := provider.Reconcile(context.Background(), reconcileRequest(payload))
	if err != nil || reconciled.Result.State != protocol.ResultPublished {
		t.Fatalf("reconcile=%+v err=%+v", reconciled, err)
	}
	path := filepath.Join(t.TempDir(), "homebrew-clone")
	runExternalGit(t, "", "clone", "-b", "main", repo.remote, path)
	raw, readErr := os.ReadFile(filepath.Join(path, "Formula", "demo.rb"))
	if readErr != nil || string(raw) != payload.Content {
		t.Fatalf("published content differs from planned JVM formula: %v", readErr)
	}
}

func TestJVMPublicationPullRequestReconcilesAfterMerge(t *testing.T) {
	repo := newRepo(t)
	fixture, server := newPRServer(t, "normal")
	defer server.Close()
	cfg := prConfiguration(repo, server)
	cfg.Manifest.InstallLayout = "jvm-distribution"
	cfg.Manifest.JavaVersion = "21"
	provider := authenticatedProvider(server.Client())
	_, payload := plannedPayload(t, provider, cfg)
	result, err := provider.Apply(context.Background(), applyRequest(payload))
	if err != nil || result.Result.State != protocol.ResultWaitingExternal {
		t.Fatalf("pull request apply=%+v err=%+v", result, err)
	}
	repo.promote(t, payload.UpdateBranch, payload.Branch)
	fixture.mu.Lock()
	fixture.state = "merged"
	fixture.mu.Unlock()
	observed, err := provider.Reconcile(context.Background(), reconcileRequest(payload))
	if err != nil || observed.Result.State != protocol.ResultPublished {
		t.Fatalf("merged JVM formula was not reconciled: %+v err=%v", observed, err)
	}
	if payload.ContentSHA256 == "" || !strings.Contains(payload.Content, `depends_on "openjdk@21"`) {
		t.Fatalf("invalid planned JVM formula: %+v", payload)
	}
}
