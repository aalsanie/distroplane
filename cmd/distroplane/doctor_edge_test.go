package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/aalsanie/distroplane/internal/config"
)

func doctorFixtureSDKMAN(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	binary := copyExecutable(t, buildFakeProvider(t), filepath.Join(dir, executableName("distroplane-provider-sdkman")))
	if err := os.MkdirAll(filepath.Join(dir, "dist"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "dist", "app.zip"), []byte("archive"), 0600); err != nil {
		t.Fatal(err)
	}
	raw := `{"schemaVersion":"2","release":{"id":"release","artifacts":{"app":{"source":"dist/app.zip"}}},"destinations":{"sdkman":{"artifact":"app","executable":` + jsonString(binary) + `,"options":{"candidate":"example","version":"1.2.3","url":"https://example.invalid/app.zip","authentication":{"mode":"state","emailCredential":"sdkman-email","passwordCredential":"sdkman-password"}}}}}`
	path := filepath.Join(dir, "distroplane.json")
	if err := os.WriteFile(path, []byte(raw), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := config.Load(path); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestDoctorRejectsSharedAndUnusedCredentialSources(t *testing.T) {
	cfg := doctorFixtureSDKMAN(t)
	t.Setenv("SHARED_CREDENTIAL", "credential-canary")
	report := doctorRun(t, exitInvalid, "--config", cfg,
		"--credential", "sdkman-email=SHARED_CREDENTIAL",
		"--credential", "sdkman-password=SHARED_CREDENTIAL", "--json")
	if !hasDoctorCode(report, "CREDENTIAL_COLLISION", doctorFail) {
		t.Fatalf("account secrets were aliased: %+v", report)
	}
	report = doctorRun(t, exitInvalid, "--config", cfg, "--credential", "unknown=SHARED_CREDENTIAL", "--json")
	if !hasDoctorCode(report, "UNUSED_CREDENTIAL_MAPPING", doctorFail) {
		t.Fatalf("unused mapping was accepted: %+v", report)
	}
	report = doctorRun(t, exitInvalid, "--config", cfg, "--credential", "sdkman-email=BAD-ENV", "--json")
	if !hasDoctorCode(report, "INVALID_CREDENTIAL_MAPPING", doctorFail) {
		t.Fatalf("invalid environment mapping passed: %+v", report)
	}
	t.Setenv("VENDOR_EMAIL", "hidden-email-canary")
	t.Setenv("VENDOR_PASSWORD", "hidden-password-canary")
	report = doctorRun(t, exitOK, "--config", cfg,
		"--credential", "sdkman-email=VENDOR_EMAIL", "--credential", "sdkman-password=VENDOR_PASSWORD", "--json")
	if !report.Healthy {
		t.Fatalf("distinct mappings did not pass: %+v", report)
	}
	output, _ := json.Marshal(report)
	for _, value := range []string{"hidden-email-canary", "hidden-password-canary", "VENDOR_PASSWORD", "VENDOR_EMAIL"} {
		if bytes.Contains(output, []byte(value)) {
			t.Fatal("credential diagnostic leaked values or environment names")
		}
	}
}

func TestDoctorDiagnosticReportDeterminism(t *testing.T) {
	_, cfg := publishFixture(t, `{"mode":"published"}`)
	a := doctorRun(t, exitOK, "--config", cfg, "--json")
	b := doctorRun(t, exitOK, "--config", cfg, "--json")
	rawA, _ := json.Marshal(a)
	rawB, _ := json.Marshal(b)
	if !bytes.Equal(rawA, rawB) {
		t.Fatalf("doctor checks vary between unchanged runs:\n%s\n%s", rawA, rawB)
	}
}

func TestDoctorDoesNotModifyPublishedJournal(t *testing.T) {
	_, cfg := publishFixture(t, `{"mode":"published"}`)
	var stdout, stderr bytes.Buffer
	if code := runPublishContext(context.Background(), []string{"--config", cfg, "--yes", "--json"}, nil, &stdout, &stderr); code != exitOK {
		t.Fatalf("fixture publish failed: %d %s", code, stderr.String())
	}
	var published publishOutput
	if err := json.Unmarshal(stdout.Bytes(), &published); err != nil {
		t.Fatal(err)
	}
	journal, err := os.ReadFile(published.JournalPath)
	if err != nil {
		t.Fatal(err)
	}
	out := doctorRun(t, exitOK, "--config", cfg, "--json")
	if !hasDoctorCode(out, "ARTIFACT_DIGEST_MATCH", doctorPass) {
		t.Fatalf("published plan was not verified: %+v", out)
	}
	retained, err := os.ReadFile(published.JournalPath)
	if err != nil || !bytes.Equal(journal, retained) {
		t.Fatalf("doctor rewrote journal: %v", err)
	}
}

func TestDoctorRejectsCorruptPlanBeforeExecution(t *testing.T) {
	_, cfg := publishFixture(t, `{"mode":"published"}`)
	report := doctorRun(t, exitInvalid, "--config", cfg, "--plan", filepath.Join(t.TempDir(), "missing-plan.json"), "--json")
	if !hasDoctorCode(report, "STATE_CORRUPT", doctorFail) {
		t.Fatalf("nonexistent plan accepted: %+v", report)
	}
}

func TestDoctorVersionDetectionDoesNotTrustProcessOutput(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows does not use POSIX executable permission bits")
	}
	dir, cfg := publishFixture(t, `{"mode":"published"}`)
	data, err := os.ReadFile(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "PRIVATE_TOKEN_CANARY") {
		t.Fatal("secret in fixture")
	}
	binary := filepath.Join(dir, executableName("distroplane-provider-fake"))
	if err := os.Chmod(binary, 0600); err != nil {
		t.Fatal(err)
	}
	report := doctorRun(t, exitInvalid, "--config", cfg, "--json")
	if !hasDoctorCode(report, "PROVIDER_NOT_FOUND", doctorFail) {
		t.Fatalf("non-executable binary passed installation check: %+v", report)
	}
}
