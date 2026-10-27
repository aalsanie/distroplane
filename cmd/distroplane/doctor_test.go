package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/aalsanie/distroplane/internal/config"
)

func doctorFixture(t *testing.T, configuration string) (string, string) {
	t.Helper()
	return publishFixture(t, configuration)
}

func runDoctorTest(t *testing.T, want int, args ...string) doctorOutput {
	t.Helper()
	var stdout, stderr bytes.Buffer
	code := runDoctorContext(context.Background(), append(args, "--json"), &stdout, &stderr)
	if code != want {
		t.Fatalf("doctor %v code=%d want=%d stdout=%s stderr=%s", args, code, want, stdout.String(), stderr.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("doctor unexpectedly logged to stderr: %s", stderr.String())
	}
	var report doctorOutput
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	if report.OutputSchemaVersion != outputSchemaVersion {
		t.Fatalf("schema changed: %+v", report)
	}
	for _, check := range report.Checks {
		switch check.Severity {
		case "PASS", "FAIL", "WARN", "SKIP":
		default:
			t.Fatalf("nonstandard severity %q", check.Severity)
		}
		if check.ID == "" || check.Subject == "" || check.Message == "" {
			t.Fatalf("missing diagnostic fields: %+v", check)
		}
		if check.Severity == "FAIL" && check.Fix == "" {
			t.Fatalf("failure lacks safe next action: %+v", check)
		}
	}
	return report
}

func hasDoctorCheck(report doctorOutput, id, severity string) bool {
	for _, check := range report.Checks {
		if check.ID == id && check.Severity == severity {
			return true
		}
	}
	return false
}

func TestDoctorReadOnlySuccessAndOutputModes(t *testing.T) {
	dir, path := doctorFixture(t, `{"mode":"published"}`)
	report := runDoctorTest(t, exitOK, "--config", path)
	for _, name := range []string{"CLI_RUNTIME", "CONFIG_VALID", "CONFIG_V1", "ARTIFACT_VALID", "PROVIDER_VALID", "GIT_NOT_REQUIRED", "STATE_VALID", "NETWORK_NOT_CHECKED"} {
		found := false
		for _, item := range report.Checks {
			if item.ID == name {
				found = true
			}
		}
		if !found {
			t.Fatalf("missing diagnostic %s: %+v", name, report)
		}
	}
	if !report.Ready {
		t.Fatalf("doctor readiness false: %+v", report)
	}
	if _, err := os.Stat(filepath.Join(dir, ".distroplane")); !os.IsNotExist(err) {
		t.Fatalf("doctor wrote state: %v", err)
	}
	var human, errors bytes.Buffer
	if code := runDoctorContext(context.Background(), []string{"--config", path}, &human, &errors); code != exitOK ||
		!strings.Contains(human.String(), "PASS CONFIG_VALID") || !strings.Contains(human.String(), "WARN CONFIG_V1") {
		t.Fatalf("human diagnostics code=%d stdout=%s stderr=%s", code, human.String(), errors.String())
	}
}

func TestDoctorMissingConfigAndMalformedConfigAreDistinct(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "distroplane.json")
	result := runDoctorTest(t, exitInvalid, "--config", file)
	if result.Ready || !hasDoctorCheck(result, "MISSING_CONFIG", "FAIL") || !hasDoctorCheck(result, "NETWORK_NOT_CHECKED", "SKIP") {
		t.Fatalf("missing config classification: %+v", result)
	}
	if err := os.WriteFile(file, []byte(`{"schemaVersion":`), 0600); err != nil {
		t.Fatal(err)
	}
	result = runDoctorTest(t, exitInvalid, "--config", file)
	if !hasDoctorCheck(result, "INVALID_CONFIG", "FAIL") || hasDoctorCheck(result, "MISSING_CONFIG", "FAIL") {
		t.Fatalf("malformed config misclassified: %+v", result)
	}
}

func TestDoctorMissingArtifactAndExecutableAreDistinct(t *testing.T) {
	dir, path := doctorFixture(t, `{"mode":"published"}`)
	artifact := filepath.Join(dir, "app.bin")
	if err := os.Remove(artifact); err != nil {
		t.Fatal(err)
	}
	result := runDoctorTest(t, exitInvalid, "--config", path)
	if !hasDoctorCheck(result, "MISSING_ARTIFACT", "FAIL") || !hasDoctorCheck(result, "PROVIDER_VALID", "PASS") {
		t.Fatalf("missing artifact not isolated: %+v", result)
	}
	if err := os.WriteFile(artifact, []byte("restored"), 0600); err != nil {
		t.Fatal(err)
	}
	// v1 target config explicitly selects a provider executable.
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err := json.Unmarshal(raw, &document); err != nil {
		t.Fatal(err)
	}
	targets := document["targets"].([]any)
	p := targets[0].(map[string]any)["provider"].(map[string]any)
	p["executable"] = filepath.Join(dir, "missing-provider")
	replacement, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, replacement, 0600); err != nil {
		t.Fatal(err)
	}
	result = runDoctorTest(t, exitInvalid, "--config", path)
	if !hasDoctorCheck(result, "PROVIDER_NOT_FOUND", "FAIL") || !hasDoctorCheck(result, "ARTIFACT_VALID", "PASS") {
		t.Fatalf("missing provider not isolated: %+v", result)
	}
}

func TestDoctorArtifactRegularityAndUnreadableFile(t *testing.T) {
	dir, path := doctorFixture(t, `{"mode":"published"}`)
	artifact := filepath.Join(dir, "app.bin")
	if err := os.Remove(artifact); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(artifact, 0700); err != nil {
		t.Fatal(err)
	}
	report := runDoctorTest(t, exitInvalid, "--config", path)
	if !hasDoctorCheck(report, "INVALID_ARTIFACT", "FAIL") {
		t.Fatalf("nonregular artifact accepted: %+v", report)
	}
	if err := os.Remove(artifact); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(artifact, nil, 0600); err != nil {
		t.Fatal(err)
	}
	report = runDoctorTest(t, exitInvalid, "--config", path)
	if !hasDoctorCheck(report, "INVALID_ARTIFACT", "FAIL") {
		t.Fatalf("empty artifact accepted: %+v", report)
	}
}

func TestDoctorCancellationAndFlagContract(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	r := diagnose(ctx, "missing.json", true, nil)
	if r.Ready || !hasDoctorCheck(r, "CANCELLED", "FAIL") {
		t.Fatalf("cancelled diagnostic not classified: %+v", r)
	}
	var out, diagnostic bytes.Buffer
	if code := runDoctorContext(context.Background(), []string{"--invalid", "--json"}, &out, &diagnostic); code != exitUsage {
		t.Fatalf("bad doctor flag accepted: code=%d", code)
	}
	var parsed errorOutput
	if err := json.Unmarshal(diagnostic.Bytes(), &parsed); err != nil || parsed.Error.Kind != "usage" {
		t.Fatalf("usage error not JSON: %s %v", diagnostic.String(), err)
	}
	if out.Len() != 0 {
		t.Fatal("usage failure wrote to stdout")
	}
}

func TestDoctorCredentialAvailabilityWithoutSecretExposure(t *testing.T) {
	const canary = "highly-sensitive-token-do-not-log"
	t.Setenv("NPM_TOKEN", canary)
	t.Setenv("CUSTOM_NPM_TOKEN", canary)
	dir, _ := doctorFixture(t, `{"mode":"published"}`)
	configPath := filepath.Join(dir, "doctor-v2.json")
	v2 := `{"schemaVersion":"2","release":{"id":"app","artifacts":{"app":{"source":"app.bin"}}},"credentialSources":{"npm-publish":"CUSTOM_NPM_TOKEN"},"destinations":{"npm":{"artifact":"app","executable":"missing-provider","options":{"packagePath":"app.bin","registry":"https://registry.npmjs.org/","authentication":{"mode":"token","credential":"npm-publish"}}}}}`
	if err := os.WriteFile(configPath, []byte(v2), 0600); err != nil {
		t.Fatal(err)
	}
	report := runDoctorTest(t, exitInvalid, "--config", configPath)
	if !hasDoctorCheck(report, "CREDENTIAL_AVAILABLE", "PASS") {
		t.Fatalf("mapped credential unrecognized: %+v", report)
	}
	raw, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(raw, []byte(canary)) || bytes.Contains(raw, []byte("CUSTOM_NPM_TOKEN")) {
		t.Fatal("credential value or source name leaked")
	}
	t.Setenv("CUSTOM_NPM_TOKEN", "")
	report = runDoctorTest(t, exitInvalid, "--config", configPath)
	if !hasDoctorCheck(report, "MISSING_CREDENTIAL", "FAIL") {
		t.Fatalf("absent credential not detected: %+v", report)
	}
}

func TestDoctorCredentialModesFailClosed(t *testing.T) {
	cases := []struct {
		name       string
		provider   string
		auth       map[string]any
		sources    map[string]string
		credential bool
		want       string
	}{
		{"npm token missing", "npm", map[string]any{"credential": "ref", "mode": "token"}, nil, false, "MISSING_CREDENTIAL"},
		{"npm OIDC no default", "npm", map[string]any{"credential": "ref", "mode": "trusted-publishing"}, nil, false, "MISSING_CREDENTIAL"},
		{"sdkman state", "sdkman", map[string]any{"emailCredential": "email", "passwordCredential": "password", "mode": "state"}, map[string]string{"email": "EMAIL_ENV", "password": "PASSWORD_ENV"}, true, "CREDENTIAL_AVAILABLE"},
		{"homebrew", "homebrew", map[string]any{"credential": "ref"}, map[string]string{"ref": "HOMEBREW_ENV"}, true, "CREDENTIAL_AVAILABLE"},
		{"winget", "winget", map[string]any{"credential": "ref"}, map[string]string{"ref": "WINGET_ENV"}, true, "CREDENTIAL_AVAILABLE"},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			for _, env := range tt.sources {
				t.Setenv(env, "fixture-secret")
			}
			if tt.name == "npm token missing" {
				t.Setenv("NPM_TOKEN", "")
			}
			options, _ := json.Marshal(map[string]any{"authentication": tt.auth})
			target := config.Target{ID: "one", Provider: config.Provider{Name: tt.provider}, Configuration: options}
			var checks []doctorCheck
			checkDoctorCredentials(target, tt.sources, func(id, severity, subject, message, fix string) {
				checks = append(checks, doctorCheck{ID: id, Severity: severity, Subject: subject, Message: message, Fix: fix})
			})
			found := false
			for _, check := range checks {
				if check.ID == tt.want {
					found = true
				}
			}
			if !found {
				t.Fatalf("missing %s: %+v", tt.want, checks)
			}
		})
	}
}

func TestDoctorNetworkOptInIsDeterministicAndDoesNotUseCredentials(t *testing.T) {
	dir, path := doctorFixture(t, `{"mode":"published"}`)
	var requests []string
	result := diagnose(context.Background(), path, false, func(_ context.Context, endpoint string) error {
		requests = append(requests, endpoint)
		return nil
	})
	if len(requests) != 0 || !hasDoctorCheck(result, "NETWORK_NOT_CHECKED", "SKIP") {
		t.Fatalf("network contacted without consent: %+v calls=%v", result, requests)
	}
	// Custom v1 providers have no implicit public endpoint.
	result = diagnose(context.Background(), path, true, func(_ context.Context, endpoint string) error {
		requests = append(requests, endpoint)
		return nil
	})
	if len(requests) != 0 || !hasDoctorCheck(result, "NETWORK_UNSUPPORTED", "SKIP") {
		t.Fatalf("unsafe arbitrary endpoint accessed: %+v %v", result, requests)
	}
	if _, err := os.Stat(filepath.Join(dir, ".distroplane")); !os.IsNotExist(err) {
		t.Fatalf("network doctor mutated state: %v", err)
	}
}

func TestDoctorKnownRemoteProbeChecksAndErrors(t *testing.T) {
	for _, tc := range []struct {
		provider string
		kind     string
		err      error
	}{
		{"npm", "NETWORK_REACHABLE", nil},
		{"npm", "NETWORK_UNREACHABLE", errors.New("simulated offline")},
		{"sdkman", "NETWORK_REACHABLE", nil},
		{"homebrew", "NETWORK_REACHABLE", nil},
		{"winget", "NETWORK_UNREACHABLE", errors.New("offline")},
	} {
		t.Run(tc.provider+"-"+tc.kind, func(t *testing.T) {
			dir, _ := doctorFixture(t, `{"mode":"published"}`)
			cfg := filepath.Join(dir, "distroplane.json")
			raw, err := os.ReadFile(cfg)
			if err != nil {
				t.Fatal(err)
			}
			var doc map[string]any
			if err := json.Unmarshal(raw, &doc); err != nil {
				t.Fatal(err)
			}
			target := doc["targets"].([]any)[0].(map[string]any)
			target["provider"].(map[string]any)["name"] = tc.provider
			newRaw, err := json.Marshal(doc)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(cfg, newRaw, 0600); err != nil {
				t.Fatal(err)
			}
			calls := 0
			report := diagnose(context.Background(), cfg, true, func(ctx context.Context, url string) error {
				calls++
				if !strings.HasPrefix(url, "https://") {
					t.Fatal("insecure probe")
				}
				return tc.err
			})
			if calls != 1 || !hasDoctorCheck(report, tc.kind, map[bool]string{true: "WARN", false: "PASS"}[tc.err != nil]) {
				t.Fatalf("probe not classified: %+v calls=%d", report, calls)
			}
		})
	}
}

func TestDoctorPublicProbeRejectsUnapprovedEndpoints(t *testing.T) {
	for _, addr := range []string{"http://registry.npmjs.org/", "https://localhost/", "https://127.0.0.1/", "https://user:pass@api.github.com/", "https://example.invalid/"} {
		if err := publicDoctorProbe(context.Background(), addr); err == nil {
			t.Fatalf("unsafe diagnostic endpoint accepted: %s", addr)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := publicDoctorProbe(ctx, "https://api.github.com/"); err == nil {
		t.Fatal("cancelled network probe succeeded")
	}
}

func TestDoctorCLIOutputIsOneParseableDocument(t *testing.T) {
	_, path := doctorFixture(t, `{"mode":"published"}`)
	var out, errOut bytes.Buffer
	if code := runDoctorContext(context.Background(), []string{"--config", path, "--json"}, &out, &errOut); code != exitOK {
		t.Fatalf("doctor: code=%d stderr=%s", code, errOut.String())
	}
	var single doctorOutput
	d := json.NewDecoder(&out)
	if err := d.Decode(&single); err != nil {
		t.Fatal(err)
	}
	var extra any
	if err := d.Decode(&extra); !errors.Is(err, io.EOF) {
		t.Fatal("extra JSON document")
	}
	if errOut.Len() != 0 {
		t.Fatalf("valid doctor emitted stderr: %s", errOut.String())
	}
}

func TestDoctorPlatformAndSchemaVersionStillReadable(t *testing.T) {
	if runtime.GOOS == "" || runtime.GOARCH == "" {
		t.Fatal("missing runtime architecture")
	}
}

func TestDoctorProviderIdentityAndProtocolDiagnostics(t *testing.T) {
	dir, configPath := doctorFixture(t, `{"mode":"published"}`)
	raw, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	target := doc["targets"].([]any)[0].(map[string]any)
	target["provider"].(map[string]any)["name"] = "wrong-name"
	updated, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath, updated, 0600); err != nil {
		t.Fatal(err)
	}
	report := runDoctorTest(t, exitInvalid, "--config", configPath)
	if !hasDoctorCheck(report, "VERSION_MISMATCH", "FAIL") {
		t.Fatalf("wrong provider executable identity allowed: %+v", report)
	}
	if _, err := os.Stat(filepath.Join(dir, ".distroplane")); !os.IsNotExist(err) {
		t.Fatalf("doctor saved state: %v", err)
	}
}

func TestDoctorNamedProviderHandshakeError(t *testing.T) {
	dir, path := doctorFixture(t, `{"mode":"published"}`)
	file := filepath.Join(dir, "bad-provider")
	if err := os.WriteFile(file, []byte("not a valid executable"), 0700); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	doc["targets"].([]any)[0].(map[string]any)["provider"].(map[string]any)["executable"] = file
	raw, err = json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	report := runDoctorTest(t, exitInvalid, "--config", path)
	if !hasDoctorCheck(report, "PROVIDER_UNAVAILABLE", "FAIL") {
		t.Fatalf("unexecutable provider not detected: %+v", report)
	}
}
