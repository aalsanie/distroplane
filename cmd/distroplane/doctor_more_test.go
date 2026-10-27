package main

import (
	"context"
	"encoding/json"
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
		name string
		auth map[string]any
		explicit bool
		want string
	}{
		{"explicit untrusted executable", map[string]any{"mode":"token","credential":"npm-ref"}, true, "MISSING_CREDENTIAL"},
		{"implicit token source", map[string]any{"mode":"token","credential":"npm-ref"}, false, "CREDENTIAL_AVAILABLE"},
		{"trusted publishing OIDC is not automatically sourced", map[string]any{"mode":"trusted-publishing","credential":"npm-ref"}, false, "MISSING_CREDENTIAL"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			raw, err := json.Marshal(map[string]any{"authentication":tc.auth})
			if err != nil { t.Fatal(err) }
			exe := ""
			if tc.explicit { exe = "/custom/provider" }
			target := config.Target{ID:"primary",Provider:config.Provider{Name:"npm",Executable:exe},Configuration:raw}
			var seen []string
			checkDoctorCredentials(target,nil,func(id,severity,subject,message,fix string) {
				seen=append(seen,id)
				if strings.Contains(message,"secret-canary") || strings.Contains(fix,"secret-canary") {
					t.Fatal("credential value leaked")
				}
			})
			found := false
			for _, id := range seen { if id == tc.want {found=true} }
			if !found {t.Fatalf("expected %s: %+v",tc.want,seen)}
		})
	}
}

func TestDoctorConfigV2AndUnsupportedCredentialConfig(t *testing.T) {
	dir, _ := doctorFixture(t,`{"mode":"published"}`)
	path := filepath.Join(dir,"config-v2.json")
	doc := `{"schemaVersion":"2","release":{"id":"r1","artifacts":{"app":{"source":"app.bin"}}},"destinations":{"npm":{"artifact":"app","executable":"missing-executable","options":{"packagePath":"app.bin","registry":"https://registry.npmjs.org/","authentication":{"mode":"token","credential":"npm-publish"}}}}}`
	if err := os.WriteFile(path, []byte(doc),0600);err!=nil {t.Fatal(err)}
	report := diagnose(context.Background(),path,false,nil)
	if hasDoctorCheck(report,"CONFIG_V1","WARN") || !hasDoctorCheck(report,"CONFIG_VALID","PASS") {
		t.Fatalf("schema v2 misdiagnosed: %+v", report)
	}
}

func TestDoctorVersionAndCancellationPaths(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var checks []doctorCheck
	probeProviderIdentity(ctx, "fake", "/does/not/exist", "provider:fake", func(id,severity,subject,message,fix string) {
		checks=append(checks,doctorCheck{ID:id,Severity:severity,Subject:subject,Message:message,Fix:fix})
	})
	if len(checks)!=1 || checks[0].ID!="PROVIDER_UNAVAILABLE" || checks[0].Severity!="FAIL" {
		t.Fatalf("cancelled provider probe did not fail closed: %+v",checks)
	}
}
