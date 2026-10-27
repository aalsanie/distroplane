package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"github.com/aalsanie/distroplane/internal/domain"
	"github.com/aalsanie/distroplane/internal/runstate"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aalsanie/distroplane/internal/config"
)

func TestDoctorReportsMissingGitAndRetainedStateDamage(t *testing.T) {
	dir, path := doctorFixture(t, `{"mode":"published"}`)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err := json.Unmarshal(raw, &document); err != nil {
		t.Fatal(err)
	}
	document["targets"].([]any)[0].(map[string]any)["provider"].(map[string]any)["name"] = "homebrew"
	updated, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, updated, 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", t.TempDir())
	report := diagnose(context.Background(), path, false, nil)
	if !hasDoctorCheck(report, "GIT_NOT_FOUND", "FAIL") {
		t.Fatalf("missing Git not diagnosed: %+v", report)
	}
	state := filepath.Join(dir, ".distroplane")
	if err := os.WriteFile(state, []byte("not a catalog"), 0600); err != nil {
		t.Fatal(err)
	}
	report = diagnose(context.Background(), path, false, nil)
	if !hasDoctorCheck(report, "STATE_CORRUPT", "FAIL") {
		t.Fatalf("corrupt state not diagnosed: %+v", report)
	}
}

func TestDoctorDefaultCredentialOnlyForApprovedPurpose(t *testing.T) {
	t.Setenv("NPM_TOKEN", "secret-canary")
	cases := []struct {
		name     string
		auth     map[string]any
		explicit bool
		want     string
	}{
		{"explicit untrusted executable", map[string]any{"mode": "token", "credential": "npm-ref"}, true, "MISSING_CREDENTIAL"},
		{"implicit token source", map[string]any{"mode": "token", "credential": "npm-ref"}, false, "CREDENTIAL_AVAILABLE"},
		{"trusted publishing OIDC is not automatically sourced", map[string]any{"mode": "trusted-publishing", "credential": "npm-ref"}, false, "MISSING_CREDENTIAL"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			raw, err := json.Marshal(map[string]any{"authentication": tc.auth})
			if err != nil {
				t.Fatal(err)
			}
			exe := ""
			if tc.explicit {
				exe = "/custom/provider"
			}
			target := config.Target{ID: "primary", Provider: config.Provider{Name: "npm", Executable: exe}, Configuration: raw}
			var seen []string
			checkDoctorCredentials(target, nil, func(id, severity, subject, message, fix string) {
				seen = append(seen, id)
				if strings.Contains(message, "secret-canary") || strings.Contains(fix, "secret-canary") {
					t.Fatal("credential value leaked")
				}
			})
			found := false
			for _, id := range seen {
				if id == tc.want {
					found = true
				}
			}
			if !found {
				t.Fatalf("expected %s: %+v", tc.want, seen)
			}
		})
	}
}

func TestDoctorConfigV2AndUnsupportedCredentialConfig(t *testing.T) {
	dir, _ := doctorFixture(t, `{"mode":"published"}`)
	path := filepath.Join(dir, "config-v2.json")
	doc := `{"schemaVersion":"2","release":{"id":"r1","artifacts":{"app":{"source":"app.bin"}}},"destinations":{"npm":{"artifact":"app","executable":"missing-executable","options":{"packagePath":"app.bin","registry":"https://registry.npmjs.org/","authentication":{"mode":"token","credential":"npm-publish"}}}}}`
	if err := os.WriteFile(path, []byte(doc), 0600); err != nil {
		t.Fatal(err)
	}
	report := diagnose(context.Background(), path, false, nil)
	if hasDoctorCheck(report, "CONFIG_V1", "WARN") || !hasDoctorCheck(report, "CONFIG_VALID", "PASS") {
		t.Fatalf("schema v2 misdiagnosed: %+v", report)
	}
}

func TestDoctorVersionAndCancellationPaths(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var checks []doctorCheck
	probeProviderIdentity(ctx, "fake", "/does/not/exist", "provider:fake", func(id, severity, subject, message, fix string) {
		checks = append(checks, doctorCheck{ID: id, Severity: severity, Subject: subject, Message: message, Fix: fix})
	})
	if len(checks) != 1 || checks[0].ID != "PROVIDER_UNAVAILABLE" || checks[0].Severity != "FAIL" {
		t.Fatalf("cancelled provider probe did not fail closed: %+v", checks)
	}
}

type failedDoctorWriter struct{}

func (failedDoctorWriter) Write([]byte) (int, error) { return 0, errors.New("broken output") }

func TestDoctorJSONWriterFailureAndTextualSuggestions(t *testing.T) {
	_, path := doctorFixture(t, `{"mode":"published"}`)
	var stderr bytes.Buffer
	if code := runDoctorContext(context.Background(), []string{"--config", path, "--json"}, failedDoctorWriter{}, &stderr); code != exitOperational {
		t.Fatalf("output failure code=%d", code)
	}
	var stdout bytes.Buffer
	if code := runDoctorContext(context.Background(), []string{"--config", path}, &stdout, io.Discard); code != exitOK {
		t.Fatalf("unexpected human doctor failure: %d", code)
	}
	if !strings.Contains(stdout.String(), "Next:") {
		t.Fatalf("warning missing suggested action: %s", stdout.String())
	}
}

func TestDoctorConfiguredGitProviderReportsInstalledGit(t *testing.T) {
	_, path := doctorFixture(t, `{"mode":"published"}`)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	doc["targets"].([]any)[0].(map[string]any)["provider"].(map[string]any)["name"] = "homebrew"
	raw, err = json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	report := diagnose(context.Background(), path, false, nil)
	if !hasDoctorCheck(report, "GIT_FOUND", "PASS") {
		t.Fatalf("Git availability not checked: %+v", report)
	}
}

func TestDoctorInvalidCredentialMetadataAndAliases(t *testing.T) {
	for _, tc := range []struct {
		name       string
		options    string
		configured map[string]string
		want       string
	}{
		{"invalid config", `{"authentication":`, nil, "CREDENTIAL_UNKNOWN"},
		{"no auth", `{"tag":"stable"}`, nil, ""},
		{"null auth", `{"authentication":null}`, nil, ""},
		{"invalid variable name", `{"authentication":{"credential":"ref"}}`, map[string]string{"ref": "BAD-NAME"}, "MISSING_CREDENTIAL"},
		{"consumer key missing", `{"authentication":{"consumerKeyCredential":"key","consumerTokenCredential":"token"}}`, nil, "MISSING_CREDENTIAL"},
		{"email missing", `{"authentication":{"emailCredential":"email","passwordCredential":"password"}}`, nil, "MISSING_CREDENTIAL"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			target := config.Target{ID: "example", Provider: config.Provider{Name: "sdkman"}, Configuration: json.RawMessage(tc.options)}
			var checks []doctorCheck
			checkDoctorCredentials(target, tc.configured, func(id, severity, subject, message, fix string) {
				checks = append(checks, doctorCheck{ID: id, Severity: severity, Subject: subject, Message: message, Fix: fix})
			})
			found := false
			for _, check := range checks {
				if check.ID == tc.want {
					found = true
				}
			}
			if tc.want == "" && len(checks) == 0 {
				found = true
			}
			if !found {
				t.Fatalf("expected %q: %+v", tc.want, checks)
			}
		})
	}
}

func TestDoctorNetworkEndpointDeduplicatesGitProviders(t *testing.T) {
	dir, path := doctorFixture(t, `{"mode":"published"}`)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	target := doc["targets"].([]any)[0].(map[string]any)
	target["provider"].(map[string]any)["name"] = "homebrew"
	target["id"] = "tap"
	other := map[string]any{
		"id":            "winget",
		"provider":      map[string]any{"name": "winget", "executable": target["provider"].(map[string]any)["executable"]},
		"configuration": map[string]any{"mode": "published"},
	}
	doc["targets"] = []any{target, other}
	raw, err = json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	calls := 0
	report := diagnose(context.Background(), path, true, func(_ context.Context, endpoint string) error {
		if endpoint != "https://api.github.com/" {
			t.Fatalf("unapproved endpoint %s", endpoint)
		}
		calls++
		return nil
	})
	if calls != 1 || !hasDoctorCheck(report, "NETWORK_REACHABLE", "PASS") {
		t.Fatalf("unexpected GitHub probe fanout: calls=%d %+v", calls, report)
	}
	if _, err := os.Stat(filepath.Join(dir, ".distroplane")); !os.IsNotExist(err) {
		t.Fatalf("doctor created state: %v", err)
	}
}

func TestDoctorWarnsForReservedRunWithoutJournal(t *testing.T) {
	dir, path := doctorFixture(t, `{"mode":"published"}`)
	var stdout, stderr bytes.Buffer
	if code := runPlanContext(context.Background(), []string{"--config", path, "--json"}, &stdout, &stderr); code != exitOK {
		t.Fatalf("plan fixture failed: %d %s", code, stderr.String())
	}
	var planned planOutput
	if err := json.Unmarshal(stdout.Bytes(), &planned); err != nil {
		t.Fatal(err)
	}
	store := runstate.Store{Root: filepath.Join(dir, ".distroplane")}
	journalPath := filepath.Join(dir, "reserved.journal")
	if err := store.ClaimRun(planned.Path, journalPath, domain.RunID("run-pending-diagnostics")); err != nil {
		t.Fatal(err)
	}
	report := diagnose(context.Background(), path, false, nil)
	if !hasDoctorCheck(report, "RUN_RESERVED", "WARN") || !hasDoctorCheck(report, "STATE_VALID", "PASS") {
		t.Fatalf("reserved run was not distinguished: %+v", report)
	}
	if _, err := os.Stat(journalPath); !os.IsNotExist(err) {
		t.Fatalf("doctor re-created missing journal: %v", err)
	}
}

func TestDoctorInspectsExistingPendingRunWithoutMutation(t *testing.T) {
	_, path := doctorFixture(t, `{"mode":"waiting_external","reconcileState":"published"}`)
	var out, stderr bytes.Buffer
	if code := runPublishContext(context.Background(), []string{"--config", path, "--yes", "--json"}, nil, &out, &stderr); code != exitPending {
		t.Fatalf("pending fixture failed: %d %s", code, stderr.String())
	}
	var result publishOutput
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(result.JournalPath)
	if err != nil {
		t.Fatal(err)
	}
	report := diagnose(context.Background(), path, false, nil)
	if !hasDoctorCheck(report, "STATE_VALID", "PASS") {
		t.Fatalf("pending run not inspected: %+v", report)
	}
	after, err := os.ReadFile(result.JournalPath)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatalf("doctor modified pending journal: %v", err)
	}
}
