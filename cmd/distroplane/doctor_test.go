package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aalsanie/distroplane/internal/config"
	"github.com/aalsanie/distroplane/internal/domain"
)

func doctorFixtureNPM(t *testing.T, mode string) (string, string, string) {
	t.Helper()
	dir := t.TempDir()
	provider := copyExecutable(t, buildFakeProvider(t), filepath.Join(dir, executableName("distroplane-provider-npm")))
	if err := os.MkdirAll(filepath.Join(dir, "dist"), 0700); err != nil { t.Fatal(err) }
	if err := os.WriteFile(filepath.Join(dir, "dist", "package.tgz"), []byte("npm-archive"), 0600); err != nil { t.Fatal(err) }
	auth := `{"mode":"`+mode+`","credential":"npm-publish"}`
	cfg := `{"schemaVersion":"2","release":{"id":"release-npm","artifacts":{"npm":{"source":"dist/package.tgz"}}},"destinations":{"npm":{"artifact":"npm","executable":`+jsonString(provider)+`,"options":{"packagePath":"dist/package.tgz","registry":"https://registry.npmjs.org/","authentication":`+auth+`}}}}`
	path := filepath.Join(dir, "distroplane.json")
	if err := os.WriteFile(path, []byte(cfg), 0600); err != nil { t.Fatal(err) }
	if _, err := config.Load(path); err != nil { t.Fatal(err) }
	return dir, path, provider
}

func doctorRun(t *testing.T, want int, args ...string) doctorOutput {
	t.Helper()
	var stdout, stderr bytes.Buffer
	if code := runDoctorContext(context.Background(), args, &stdout, &stderr); code != want {
		t.Fatalf("doctor code=%d want=%d stdout=%s stderr=%s", code, want, stdout.String(), stderr.String())
	}
	if stderr.Len() != 0 { t.Fatalf("doctor report leaked into stderr: %s", stderr.String()) }
	var report doctorOutput
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil { t.Fatalf("invalid JSON %s: %v", stdout.String(), err) }
	if report.OutputSchemaVersion != outputSchemaVersion || len(report.Checks) == 0 { t.Fatalf("missing doctor checks: %+v", report) }
	return report
}

func hasDoctorCode(output doctorOutput, code, severity string) bool {
	for _, check := range output.Checks {
		if check.Code == code && check.Severity == severity { return true }
	}
	return false
}

func TestDoctorReadOnlyHealthyAndSchemaV1(t *testing.T) {
	_, cfg := publishFixture(t, `{"mode":"published"}`)
	before, err := os.ReadFile(cfg)
	if err != nil { t.Fatal(err) }
	result := doctorRun(t, exitOK, "--config", cfg, "--json")
	if !result.Healthy || !hasDoctorCode(result, "CONFIG_VALID", doctorPass) ||
		!hasDoctorCode(result, "SCHEMA_V1", doctorWarn) ||
		!hasDoctorCode(result, "ARTIFACT_READABLE", doctorPass) ||
		!hasDoctorCode(result, "PROVIDER_FOUND", doctorPass) ||
		!hasDoctorCode(result, "PROVIDER_VERSION", doctorPass) ||
		!hasDoctorCode(result, "REMOTE_SKIPPED", doctorSkip) {
		t.Fatalf("local doctor checks: %+v", result)
	}
	after, err := os.ReadFile(cfg)
	if err != nil || !bytes.Equal(before, after) { t.Fatalf("doctor changed config: %v", err) }
	var human bytes.Buffer
	if code := runDoctorContext(context.Background(), []string{"--config", cfg}, &human, &bytes.Buffer{}); code != exitOK || !strings.Contains(human.String(), "PASS") || !strings.Contains(human.String(), "WARN") || !strings.Contains(human.String(), "Next:") {
		t.Fatalf("human report code=%d: %s", code, human.String())
	}
}

func TestDoctorMissingAndInvalidConfig(t *testing.T) {
	root := t.TempDir()
	absent := filepath.Join(root, "missing.json")
	invalid := filepath.Join(root, "invalid.json")
	if err := os.WriteFile(invalid, []byte(`{"schemaVersion":`), 0600); err != nil { t.Fatal(err) }
	for _, tc := range []struct{ path, code string }{
		{absent, "MISSING_CONFIG"},
		{invalid, "INVALID_CONFIG"},
	} {
		report := doctorRun(t, exitInvalid, "--config", tc.path, "--json")
		if report.Healthy || !hasDoctorCode(report, tc.code, doctorFail) ||
			!hasDoctorCode(report, "DEPENDENCY_SKIPPED", doctorSkip) ||
			!hasDoctorCode(report, "REMOTE_SKIPPED", doctorSkip) {
			t.Fatalf("%s: %+v", tc.code, report)
		}
		for _, c := range report.Checks {
			if c.Severity == doctorFail && c.Fix == "" { t.Fatalf("missing remediation: %+v", c) }
		}
	}
}

func TestDoctorRejectsArtifactAndProviderFailures(t *testing.T) {
	for _, tc := range []struct{ name, code string }{
		{"missing artifact", "MISSING_ARTIFACT"},
		{"missing provider", "PROVIDER_NOT_FOUND"},
		{"version mismatch", "VERSION_MISMATCH"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir, cfg := publishFixture(t, `{"mode":"published"}`)
			loaded, err := config.Load(cfg)
			if err != nil { t.Fatal(err) }
			provider := loaded.Config.Targets[0].Provider.Executable
			switch tc.name {
			case "missing artifact":
				if err := os.Remove(filepath.Join(dir, "app.bin")); err != nil { t.Fatal(err) }
			case "missing provider":
				if err := os.Remove(provider); err != nil { t.Fatal(err) }
			case "version mismatch":
				mismatch := filepath.Join(dir, executableName("distroplane-provider-unrelated"))
				if err := os.Rename(provider, mismatch); err != nil { t.Fatal(err) }
				source, err := os.ReadFile(cfg)
				if err != nil { t.Fatal(err) }
				if err := os.WriteFile(cfg, []byte(strings.Replace(string(source), provider, mismatch, 1)), 0600); err != nil { t.Fatal(err) }
			}
			report := doctorRun(t, exitInvalid, "--config", cfg, "--json")
			if report.Healthy || !hasDoctorCode(report, tc.code, doctorFail) {
				t.Fatalf("missing %s: %+v", tc.code, report)
			}
		})
	}
}

func TestDoctorChecksCredentialsWithoutLeakingValues(t *testing.T) {
	_, cfg, _ := doctorFixtureNPM(t, "token")
	const secret = "doctor-credential-secret-canary"
	t.Setenv("DOCTOR_NPM_TOKEN", secret)
	missing := doctorRun(t, exitInvalid, "--config", cfg, "--json")
	if !hasDoctorCode(missing, "MISSING_CREDENTIAL", doctorFail) { t.Fatalf("ambient discovery incorrectly passed: %+v", missing) }
	t.Setenv("NPM_TOKEN", secret)
	ambient := doctorRun(t, exitInvalid, "--config", cfg, "--json")
	if !hasDoctorCode(ambient, "MISSING_CREDENTIAL", doctorFail) { t.Fatal("custom provider inherited NPM_TOKEN") }
	available := doctorRun(t, exitOK, "--config", cfg, "--credential", "npm-publish=DOCTOR_NPM_TOKEN", "--json")
	if !available.Healthy || !hasDoctorCode(available, "CREDENTIAL_AVAILABLE", doctorPass) { t.Fatalf("missing mapped credential: %+v", available) }
	raw, err := json.Marshal(available)
	if err != nil { t.Fatal(err) }
	if bytes.Contains(raw, []byte(secret)) || bytes.Contains(raw, []byte("DOCTOR_NPM_TOKEN")) { t.Fatal("secret or environment name leaked from diagnostics") }
}

func TestDoctorTrustedPublishingNeverUsesNPMTokenImplicitly(t *testing.T) {
	_, cfg, _ := doctorFixtureNPM(t, "trusted-publishing")
	t.Setenv("NPM_TOKEN", "must-not-be-used-for-oidc")
	report := doctorRun(t, exitInvalid, "--config", cfg, "--json")
	if !hasDoctorCode(report, "MISSING_CREDENTIAL", doctorFail) {
		t.Fatalf("OIDC requirement incorrectly reused token: %+v", report)
	}
}

func TestDoctorCanVerifySavedPlanDigestsWithoutPublication(t *testing.T) {
	dir, cfg := publishFixture(t, `{"mode":"published"}`)
	var out, diagnostics bytes.Buffer
	if code := runPlanContext(context.Background(), []string{"--config", cfg, "--json"}, &out, &diagnostics); code != exitOK {
		t.Fatalf("fixture plan code=%d error=%s", code, diagnostics.String())
	}
	var plan planOutput
	if err := json.Unmarshal(out.Bytes(), &plan); err != nil { t.Fatal(err) }
	good := doctorRun(t, exitOK, "--config", cfg, "--plan", plan.Path, "--json")
	if !hasDoctorCode(good, "PROVIDER_DIGEST_MATCH", doctorPass) || !hasDoctorCode(good, "ARTIFACT_DIGEST_MATCH", doctorPass) {
		t.Fatalf("unchanged plan not verified: %+v", good)
	}
	if err := os.WriteFile(filepath.Join(dir, "app.bin"), []byte("changed-archive"), 0600); err != nil { t.Fatal(err) }
	changed := doctorRun(t, exitInvalid, "--config", cfg, "--plan", plan.Path, "--json")
	if !hasDoctorCode(changed, "ARTIFACT_CHANGED", doctorFail) { t.Fatalf("changed archive ignored: %+v", changed) }
}

func TestDoctorDefaultCatalogVerificationAndCorruption(t *testing.T) {
	_, cfg := publishFixture(t, `{"mode":"published"}`)
	var out, diagnostic bytes.Buffer
	if code := runPlanContext(context.Background(), []string{"--config", cfg, "--json"}, &out, &diagnostic); code != exitOK { t.Fatal(diagnostic.String()) }
	good := doctorRun(t, exitOK, "--config", cfg, "--json")
	if !hasDoctorCode(good, "ARTIFACT_DIGEST_MATCH", doctorPass) { t.Fatal("catalog plan not selected") }
	var plan planOutput
	if err := json.Unmarshal(out.Bytes(), &plan); err != nil { t.Fatal(err) }
	if err := os.Remove(plan.Path); err != nil { t.Fatal(err) }
	corrupt := doctorRun(t, exitInvalid, "--config", cfg, "--json")
	if !hasDoctorCode(corrupt, "STATE_CORRUPT", doctorFail) { t.Fatalf("missing plan accepted: %+v", corrupt) }
}

func TestDoctorGitCheckHonorsActualPATH(t *testing.T) {
	dir, cfg := publishFixture(t, `{"mode":"published"}`)
	loaded, err := config.Load(cfg)
	if err != nil { t.Fatal(err) }
	provider := loaded.Config.Targets[0].Provider.Executable
	newPath := filepath.Join(dir, executableName("distroplane-provider-homebrew"))
	if err := os.Rename(provider, newPath); err != nil { t.Fatal(err) }
	data, err := os.ReadFile(cfg)
	if err != nil { t.Fatal(err) }
	revised := strings.ReplaceAll(string(data), `"name":"fake"`, `"name":"homebrew"`)
	revised = strings.Replace(revised, provider, newPath, 1)
	if err := os.WriteFile(cfg, []byte(revised), 0600); err != nil { t.Fatal(err) }
	t.Setenv("PATH", t.TempDir())
	report := doctorRun(t, exitInvalid, "--config", cfg, "--json")
	if !hasDoctorCode(report, "GIT_MISSING", doctorFail) { t.Fatalf("Git check missed: %+v", report) }
}

func TestDoctorRemoteIsOptInAndNeverAuthenticates(t *testing.T) {
	_, cfg, _ := doctorFixtureNPM(t, "token")
	t.Setenv("DOCTOR_NPM_TOKEN", "remote-test-secret")
	loaded, err := config.Load(cfg)
	if err != nil { t.Fatal(err) }
	endpoints := doctorRemoteURLs(loaded)
	if len(endpoints) != 1 || endpoints[0] != "https://registry.npmjs.org/" { t.Fatalf("unexpected URL list: %+v", endpoints) }
	probe := doctorProbe{remote: true, timeout: time.Second, credentials: credentialFlags{domain.CredentialRef("npm-publish"): "DOCTOR_NPM_TOKEN"}}
	calls := 0
	probe.dial = func(ctx context.Context, network, address string) (net.Conn, error) {
		calls++
		if network != "tcp" || address != "registry.npmjs.org:443" { t.Fatalf("unexpected remote probe: %s %s", network, address) }
		return nil, errors.New("network unreachable")
	}
	failure := inspectDoctor(context.Background(), cfg, probe)
	if failure.exit() != exitOperational || calls != 1 || !hasDoctorCode(failure.output, "REMOTE_UNREACHABLE", doctorFail) {
		t.Fatalf("unreachable remote not distinguished: %+v, calls=%d", failure, calls)
	}
	probe.dial = func(ctx context.Context, network, address string) (net.Conn, error) {
		client, server := net.Pipe()
		_ = server.Close()
		return client, nil
	}
	success := inspectDoctor(context.Background(), cfg, probe)
	if success.exit() != exitOK || !hasDoctorCode(success.output, "REMOTE_REACHABLE", doctorPass) { t.Fatalf("reachable host incorrectly failed: %+v", success) }
	unqueried := inspectDoctor(context.Background(), cfg, doctorProbe{credentials: probe.credentials})
	if !hasDoctorCode(unqueried.output, "REMOTE_SKIPPED", doctorSkip) { t.Fatal("remote check executed without opt-in") }
}

func TestDoctorRemoteURLsAndEndpointSafety(t *testing.T) {
	for _, tc := range []struct{ value, result string }{
		{"https://registry.npmjs.org/", "registry.npmjs.org:443"},
		{"https://example.com:8443/health", "example.com:8443"},
		{"https://[::1]:444/path", "[::1]:444"},
	} {
		address, err := doctorRemoteAddress(tc.value)
		if err != nil || address != tc.result { t.Fatalf("URL %q -> %q %v", tc.value, address, err) }
	}
	for _, value := range []string{"http://example.com", "https://", "ftp://example.com", "https://user:pass@example.com"} {
		if _, err := doctorRemoteAddress(value); err == nil { t.Fatalf("unsafe remote accepted: %q", value) }
	}
}

func TestDoctorCredentialDiscoveryUsesRequirementPurpose(t *testing.T) {
	inputs := []struct{ provider, field, mode, env string }{
		{"npm", "credential", "token", "NPM_TOKEN"},
		{"npm", "credential", "trusted-publishing", ""},
		{"homebrew", "credential", "", "HOMEBREW_GITHUB_TOKEN"},
		{"winget", "credential", "", "WINGET_GITHUB_TOKEN"},
		{"sdkman", "emailCredential", "state", "SDKMAN_VENDOR_EMAIL"},
		{"sdkman", "passwordCredential", "state", "SDKMAN_VENDOR_PASSWORD"},
		{"sdkman", "consumerKeyCredential", "legacy", "SDKMAN_CONSUMER_KEY"},
		{"sdkman", "consumerTokenCredential", "legacy", "SDKMAN_CONSUMER_TOKEN"},
	}
	for _, tc := range inputs {
		t.Run(tc.provider+tc.field+tc.mode, func(t *testing.T) {
			cfg := config.Target{Provider: config.Provider{Name: tc.provider}, Configuration: json.RawMessage(fmt.Sprintf(`{"authentication":{"mode":%q,%q:"ref"}}`, tc.mode, tc.field))}
			reqs := doctorCredentialRefs(cfg)
			if len(reqs) != 1 || reqs[0].ref != "ref" { t.Fatalf("missing reference: %+v", reqs) }
			source := doctorCredentialSource("ref", tc.provider, reqs[0].environment, nil, nil, true)
			if source != tc.env { t.Fatalf("source=%q want=%q", source, tc.env) }
			if doctorCredentialSource("ref", tc.provider, reqs[0].environment, nil, nil, false) != "" { t.Fatal("untrusted provider obtained default") }
			if got := doctorCredentialSource("ref", tc.provider, reqs[0].environment, map[string]string{"ref":"FROM_CONFIG"}, credentialFlags{domain.CredentialRef("ref"):"FROM_CLI"}, true); got != "FROM_CLI" {
				t.Fatalf("explicit precedence failed: %s", got)
			}
		})
	}
}

func TestDoctorFlagSafetyAndCancellation(t *testing.T) {
	for _, args := range [][]string{
		{"--timeout", "0s"},
		{"--timeout", "60s"},
		{"--unknown"},
		{"extra"},
		{"--credential", "not-a-mapping"},
	} {
		var out, diagnostic bytes.Buffer
		if code := runDoctorContext(context.Background(), args, &out, &diagnostic); code != exitUsage {
			t.Fatalf("bad flags accepted %v: %d", args, code)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var out, diagnostic bytes.Buffer
	if code := runDoctorContext(ctx, []string{"--json"}, &out, &diagnostic); code != exitOperational {
		t.Fatalf("cancelled doctor code=%d", code)
	}
}
