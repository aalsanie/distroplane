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

	"github.com/aalsanie/distroplane/internal/domain"
	"github.com/aalsanie/distroplane/internal/runstate"
)

func TestCLIGuidanceShellArgumentAndUnsafePath(t *testing.T) {
	for _, tc := range []struct {
		path, host, want string
		ok               bool
	}{
		{"a b", "linux", "'a b'", true},
		{"a'b", "linux", "'a'\\''b'", true},
		{"a'b", "windows", "'a''b'", true},
		{"C:\\Program Files\\thing", "windows", "'C:\\Program Files\\thing'", true},
		{"δistroplane/$HOME;$(bad)", "linux", "'δistroplane/$HOME;$(bad)'", true},
		{"a\nb", "linux", "", false},
		{"a\x1bb", "windows", "", false},
		{"a\u202eb", "linux", "", false},
		{"a\u2028b", "windows", "", false},
		{"", "linux", "", false},
		{string([]byte{0xff}), "linux", "", false},
	} {
		got, ok := shellArgument(tc.path, tc.host)
		if got != tc.want || ok != tc.ok {
			t.Errorf("shellArgument(%q, %q) = %q, %t; want %q, %t",
				tc.path, tc.host, got, ok, tc.want, tc.ok)
		}
	}
	if !strings.Contains(safeDisplay("x\nFake successful publish"), `\n`) {
		t.Fatal("control character was not escaped")
	}
	if got := safeDisplay("hello"); got != "hello" {
		t.Fatalf("safe display changed: %q", got)
	}
}

func TestCLIGuidanceInitHumanNextActionUsesExactGeneratedPath(t *testing.T) {
	dir, artifact := createInitProject(t)
	path := filepath.Join(dir, "配置 it's $(not-executed) [ready].json")
	args := []string{"--output", path, "--artifact", artifact, "--destination", "npm", "--release", "v1.2.3"}
	var out, diagnostic bytes.Buffer
	if got := runInitContext(context.Background(), args, nil, false, &out, &diagnostic); got != exitOK {
		t.Fatalf("init exited %d: %s", got, diagnostic.String())
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal(err)
	}
	quoted, ok := shellArgument(path, runtime.GOOS)
	if !ok || !strings.Contains(out.String(), "distroplane doctor --config "+quoted) {
		t.Fatalf("follow-up does not use exact safe path: %s", out.String())
	}
	if strings.Contains(out.String(), "plan --config") || strings.Count(out.String(), "Next:") != 1 {
		t.Fatalf("misleading next action: %s", out.String())
	}
	var jsonOut bytes.Buffer
	if got := runInitContext(context.Background(), append(args, "--force", "--json"), nil, false, &jsonOut, &diagnostic); got != exitOK {
		t.Fatalf("forced JSON init: code %d stderr=%s", got, diagnostic.String())
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(jsonOut.Bytes(), &obj); err != nil {
		t.Fatal(err)
	}
	var configPath, backup string
	if err := json.Unmarshal(obj["configPath"], &configPath); err != nil || configPath != path {
		t.Fatalf("JSON config path changed: %s %v", jsonOut.String(), err)
	}
	if err := json.Unmarshal(obj["backupPath"], &backup); err != nil || backup != path+".bak" {
		t.Fatalf("JSON backup path changed: %s %v", jsonOut.String(), err)
	}
	if strings.Contains(jsonOut.String(), "Next:") || !strings.Contains(jsonOut.String(), "outputSchemaVersion") {
		t.Fatalf("JSON polluted by human advice: %s", jsonOut.String())
	}
}

func TestCLIGuidanceInitFollowUpRejectsTerminalInjection(t *testing.T) {
	var out bytes.Buffer
	writeInitFollowUp(&out, "evil\nNext: distroplane publish --yes")
	if strings.Contains(out.String(), "--yes") || strings.Contains(out.String(), "distroplane doctor --config") {
		t.Fatalf("unsafe literal command rendered: %s", out.String())
	}
}

func TestCLIGuidanceDoctorNewReleaseLocalAndRecordedStatus(t *testing.T) {
	_, cfg := publishFixture(t, `{"mode":"published"}`)
	var out, diagnostic bytes.Buffer
	args := []string{"--config", cfg}
	if got := runDoctorContext(context.Background(), args, &out, &diagnostic); got != exitOK {
		t.Fatalf("local doctor exited %d: %s", got, diagnostic.String())
	}
	first := out.String()
	if !strings.Contains(first, "Readiness: LOCALLY READY FOR REVIEW") ||
		!strings.Contains(first, "remote connectivity not requested") ||
		!strings.Contains(first, "Checks: FAIL 0") ||
		!strings.Contains(first, "distroplane publish --config") ||
		!strings.Contains(first, "explicitly approve") ||
		strings.Contains(first, "distroplane resume") {
		t.Fatalf("new-release guidance incorrect:\n%s", first)
	}
	var published bytes.Buffer
	if got := runPublishContext(context.Background(),
		[]string{"--config", cfg, "--yes", "--json"}, nil, &published, &diagnostic); got != exitOK {
		t.Fatalf("fixture publish exited %d: %s", got, diagnostic.String())
	}
	var result publishOutput
	if err := json.Unmarshal(published.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if got := runDoctorContext(context.Background(), args, &out, &diagnostic); got != exitOK {
		t.Fatalf("existing-run doctor exited %d: %s", got, diagnostic.String())
	}
	recorded := out.String()
	if !strings.Contains(recorded, "EXISTING RELEASE REQUIRES INSPECTION") ||
		!strings.Contains(recorded, "distroplane status --config") ||
		!strings.Contains(recorded, "--run") ||
		!strings.Contains(recorded, result.Run.RunID) ||
		strings.Contains(recorded, "distroplane publish --config") {
		t.Fatalf("existing run incorrectly encouraged republish:\n%s", recorded)
	}
	if diagnostic.Len() != 0 {
		t.Fatalf("unexpected doctor diagnostic: %s", diagnostic.String())
	}
}

func TestCLIGuidanceDoctorPendingRunNeverSuggestsRepublish(t *testing.T) {
	_, cfg := publishFixture(t, `{"mode":"waiting_external","reconcileState":"published"}`)
	var published, diagnostic, out bytes.Buffer
	if got := runPublishContext(context.Background(),
		[]string{"--config", cfg, "--yes", "--json"}, nil, &published, &diagnostic); got != exitPending {
		t.Fatalf("pending publish exited %d: %s", got, diagnostic.String())
	}
	if got := runDoctorContext(context.Background(), []string{"--config", cfg}, &out, &diagnostic); got != exitOK {
		t.Fatalf("pending-run doctor exited %d: %s", got, diagnostic.String())
	}
	message := out.String()
	if !strings.Contains(message, "EXISTING RELEASE REQUIRES INSPECTION") ||
		!strings.Contains(message, "RECONCILE_REQUIRED") ||
		!strings.Contains(message, "distroplane status --config") ||
		strings.Contains(message, "distroplane publish --config") {
		t.Fatalf("pending run incorrectly encouraged new publication:\n%s", message)
	}
}

func TestCLIGuidanceDoctorMissingConfigRemainsInvalid(t *testing.T) {
	cfg := filepath.Join(t.TempDir(), "not-created.json")
	var out, errOut bytes.Buffer
	if got := runDoctorContext(context.Background(), []string{"--config", cfg}, &out, &errOut); got != exitInvalid {
		t.Fatalf("doctor exit %d stderr=%s", got, errOut.String())
	}
	message := out.String()
	if !strings.Contains(message, "Readiness: NOT READY") ||
		!strings.Contains(message, "MISSING_CONFIG") ||
		!strings.Contains(message, "FAIL 1") ||
		!strings.Contains(message, "Next: Create the configuration") ||
		strings.Contains(message, "distroplane publish") {
		t.Fatalf("invalid-config guidance:\n%s", message)
	}
	var machine bytes.Buffer
	if got := runDoctorContext(context.Background(), []string{"--config", cfg, "--json"}, &machine, &errOut); got != exitInvalid {
		t.Fatalf("machine doctor exit %d", got)
	}
	var result doctorOutput
	if err := json.Unmarshal(machine.Bytes(), &result); err != nil || result.Healthy || !hasDoctorCode(result, "MISSING_CONFIG", doctorFail) {
		t.Fatalf("existing JSON contract changed: %+v, err %v", result, err)
	}
	if strings.Contains(machine.String(), "Next:") || errOut.Len() != 0 {
		t.Fatalf("human prose contaminated JSON: %s stderr=%s", machine.String(), errOut.String())
	}
}

func TestCLIGuidanceDoctorPresentationPrioritizesAndSanitizes(t *testing.T) {
	report := doctorOutput{Checks: []doctorFinding{
		{Code: "REMOTE_UNREACHABLE", Severity: doctorFail, Subject: "network", Message: "unreachable", Fix: "Check DNS"},
		{Code: "MISSING_CREDENTIAL", Severity: doctorFail, Subject: "npm/ref", Message: "missing", Fix: "Set approved env"},
		{Code: "SCHEMA_V1", Severity: doctorWarn, Subject: "config", Message: "older", Fix: "Update config"},
		{Code: "USER\nMALICIOUS", Severity: doctorWarn, Subject: "bad\x1b[1m", Message: "unsafe", Fix: "Update config"},
		{Code: "CONFIG_VALID", Severity: doctorPass, Subject: "config", Message: "ok"},
		{Code: "PLAN_SKIPPED", Severity: doctorSkip, Subject: "plan", Message: "unused"},
	}}
	if fail, warn, pass, skip := doctorCounts(report); fail != 2 || warn != 2 || pass != 1 || skip != 1 {
		t.Fatalf("unexpected counts: %d/%d/%d/%d", fail, warn, pass, skip)
	}
	if doctorPriority(report.Checks[1]) >= doctorPriority(report.Checks[0]) ||
		doctorPriority(report.Checks[2]) <= doctorPriority(report.Checks[1]) {
		t.Fatal("severity ordering incorrect")
	}
	var out bytes.Buffer
	writeDoctorHuman(&out, filepath.Join(t.TempDir(), "uncreated.json"), true, report)
	message := out.String()
	if !strings.Contains(message, "Readiness: NOT READY; remote probe failed") ||
		!strings.Contains(message, "FAIL 2  WARN 2  PASS 1  SKIP 1") ||
		!strings.Contains(message, "remote TCP probe failed") ||
		strings.Index(message, "MISSING_CREDENTIAL") >= strings.Index(message, "REMOTE_UNREACHABLE") ||
		strings.Contains(message, "\nMALICIOUS [") ||
		strings.Count(message, "Fix: Update config") != 1 ||
		strings.Count(message, "Next:") != 1 {
		t.Fatalf("doctor presentation incorrect:\n%s", message)
	}
}

func TestCLIGuidanceDoctorRemoteScopeNeverOverstatesChecks(t *testing.T) {
	for _, tc := range []struct {
		name     string
		remote   bool
		code     string
		severity string
		fragment string
	}{
		{"not requested", false, "REMOTE_SKIPPED", doctorSkip, "not requested"},
		{"no known endpoint", true, "REMOTE_SKIPPED", doctorSkip, "not verified"},
		{"reachable", true, "REMOTE_REACHABLE", doctorPass, "authorization and publication not verified"},
		{"failed", true, "REMOTE_UNREACHABLE", doctorFail, "probe failed"},
		{"invalid", true, "REMOTE_INVALID", doctorFail, "probe failed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			report := doctorOutput{Checks: []doctorFinding{{Code: tc.code, Severity: tc.severity}}}
			if scope := doctorRemoteScope(report, tc.remote); !strings.Contains(scope, tc.fragment) {
				t.Fatalf("%s: wrong scope %q", tc.name, scope)
			}
		})
	}
}

func TestCLIGuidanceDoctorSavedPlanWithoutRunIsNotNewPublication(t *testing.T) {
	_, cfg := publishFixture(t, "{\"mode\":\"published\"}")
	var out, errOut bytes.Buffer
	if got := runPlanContext(context.Background(), []string{"--config", cfg, "--json"}, &out, &errOut); got != exitOK {
		t.Fatalf("plan fixture exited %d: %s", got, errOut.String())
	}
	out.Reset()
	if got := runDoctorContext(context.Background(), []string{"--config", cfg}, &out, &errOut); got != exitOK {
		t.Fatalf("doctor exited %d: %s", got, errOut.String())
	}
	message := out.String()
	if !strings.Contains(message, "EXISTING RELEASE REQUIRES INSPECTION") ||
		!strings.Contains(message, "distroplane runs --config") ||
		strings.Contains(message, "distroplane publish --config") {
		t.Fatalf("saved plan was treated as a new release:\n%s", message)
	}
}

func TestCLIGuidanceDoctorReservedRunDemandsManualRecovery(t *testing.T) {
	dir, cfg := publishFixture(t, "{\"mode\":\"published\"}")
	var out, errOut bytes.Buffer
	if got := runPlanContext(context.Background(), []string{"--config", cfg, "--json"}, &out, &errOut); got != exitOK {
		t.Fatalf("plan fixture exited %d: %s", got, errOut.String())
	}
	var plan planOutput
	if err := json.Unmarshal(out.Bytes(), &plan); err != nil {
		t.Fatal(err)
	}
	journalPath := filepath.Join(dir, "never-written.journal")
	store := runstate.Store{Root: filepath.Join(dir, ".distroplane")}
	if err := store.ClaimRun(plan.Path, journalPath, domain.RunID("run-ux-reserved")); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if got := runDoctorContext(context.Background(), []string{"--config", cfg}, &out, &errOut); got != exitOK {
		t.Fatalf("doctor exited %d: %s", got, errOut.String())
	}
	if !strings.Contains(out.String(), "RUN_RESERVED") ||
		!strings.Contains(out.String(), "preserve the existing run reservation") &&
			!strings.Contains(out.String(), "Preserve the existing run reservation") ||
		strings.Contains(out.String(), "distroplane publish --config") {
		t.Fatalf("reserved run guidance:\n%s", out.String())
	}
	if _, err := os.Stat(journalPath); !os.IsNotExist(err) {
		t.Fatalf("doctor changed missing journal state: %v", err)
	}
}

func TestCLIGuidanceDoctorCorruptCatalogDoesNotSuggestPublication(t *testing.T) {
	dir, cfg := publishFixture(t, "{\"mode\":\"published\"}")
	var out, errOut bytes.Buffer
	if got := runPlanContext(context.Background(), []string{"--config", cfg, "--json"}, &out, &errOut); got != exitOK {
		t.Fatalf("plan fixture exited %d: %s", got, errOut.String())
	}
	orphan := filepath.Join(dir, ".distroplane", "catalog", "runs", "orphan.json")
	if err := os.WriteFile(orphan, []byte("{}"), 0600); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if got := runDoctorContext(context.Background(), []string{"--config", cfg}, &out, &errOut); got != exitInvalid {
		t.Fatalf("corrupt catalog exit %d: %s", got, errOut.String())
	}
	message := out.String()
	if !strings.Contains(message, "STATE_CORRUPT") ||
		!strings.Contains(message, "Preserve and restore the exact original") ||
		strings.Contains(message, "distroplane publish --config") {
		t.Fatalf("corrupted catalog guidance:\n%s", message)
	}
}

func TestCLIGuidanceDoctorInvalidConfigCorrectsBeforeValidate(t *testing.T) {
	cfg := filepath.Join(t.TempDir(), "broken with spaces.json")
	if err := os.WriteFile(cfg, []byte("{"), 0600); err != nil {
		t.Fatal(err)
	}
	var out, errOut bytes.Buffer
	if got := runDoctorContext(context.Background(), []string{"--config", cfg}, &out, &errOut); got != exitInvalid {
		t.Fatalf("invalid config doctor exited %d: %s", got, errOut.String())
	}
	quoted, _ := shellArgument(cfg, runtime.GOOS)
	if !strings.Contains(out.String(), "INVALID_CONFIG") ||
		!strings.Contains(out.String(), "distroplane validate --config "+quoted) ||
		strings.Contains(out.String(), "distroplane publish --config") {
		t.Fatalf("invalid configuration guidance:\n%s", out.String())
	}
}

func TestCLIGuidancePriorities(t *testing.T) {
	for _, tc := range []struct {
		code, severity string
		priority       int
	}{
		{"STATE_CORRUPT", doctorFail, 0},
		{"MISSING_CONFIG", doctorFail, 2},
		{"INVALID_CONFIG", doctorFail, 2},
		{"MISSING_ARTIFACT", doctorFail, 3},
		{"ARTIFACT_HASH_FAILED", doctorFail, 3},
		{"ARTIFACT_CHANGED", doctorFail, 3},
		{"PROVIDER_NOT_FOUND", doctorFail, 4},
		{"VERSION_MISMATCH", doctorFail, 4},
		{"ARCH_MISMATCH", doctorFail, 4},
		{"PROVIDER_DIGEST_CHANGED", doctorFail, 4},
		{"MISSING_CREDENTIAL", doctorFail, 5},
		{"CREDENTIAL_COLLISION", doctorFail, 5},
		{"INVALID_CREDENTIAL_MAPPING", doctorFail, 5},
		{"GIT_MISSING", doctorFail, 6},
		{"RUN_RESERVED", doctorWarn, 10},
		{"RECONCILE_REQUIRED", doctorWarn, 11},
		{"PLAN_AMBIGUOUS", doctorWarn, 12},
		{"SCHEMA_V1", doctorWarn, 20},
		{"CONFIG_VALID", doctorPass, 30},
		{"REMOTE_SKIPPED", doctorSkip, 30},
	} {
		if got := doctorPriority(doctorFinding{Code: tc.code, Severity: tc.severity}); got != tc.priority {
			t.Errorf("priority %s/%s: got %d want %d", tc.code, tc.severity, got, tc.priority)
		}
	}
}

func TestCLIGuidanceNoGuessedCommands(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing config.json")
	for _, tc := range []struct {
		name            string
		checks          []doctorFinding
		want, forbidden string
	}{
		{"corrupt", []doctorFinding{{Code: "STATE_CORRUPT", Severity: doctorFail}}, "Preserve and restore", "publish --config"},
		{"reserved", []doctorFinding{{Code: "RUN_RESERVED", Severity: doctorWarn}}, "Preserve the existing run reservation", "publish --config"},
		{"multiple", []doctorFinding{{Code: "PLAN_AMBIGUOUS", Severity: doctorWarn}}, "distroplane runs --config", "publish --config"},
		{"missing", []doctorFinding{{Code: "MISSING_CONFIG", Severity: doctorFail}}, "Create the configuration", "publish --config"},
		{"invalid", []doctorFinding{{Code: "INVALID_CONFIG", Severity: doctorFail}}, "distroplane validate --config", "publish --config"},
		{"provider", []doctorFinding{{Code: "PROVIDER_NOT_FOUND", Severity: doctorFail, Fix: "Install provider"}}, "Install provider", "publish --config"},
		{"unknown failure", []doctorFinding{{Code: "GIT_MISSING", Severity: doctorFail}}, "Correct the reported prerequisite", "publish --config"},
		{"ready", []doctorFinding{{Code: "CONFIG_VALID", Severity: doctorPass}}, "distroplane publish --config", "resume --config"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := doctorPrimaryNext(doctorOutput{Checks: tc.checks}, path)
			if !strings.Contains(got, tc.want) || (tc.forbidden != "" && strings.Contains(got, tc.forbidden)) {
				t.Fatalf("next action: %q", got)
			}
		})
	}
	if _, ok := cliConfigCommand("doctor", "bad\npath"); ok {
		t.Fatal("unsafe command made copyable")
	}
	var initOut bytes.Buffer
	writeInitFollowUp(&initOut, "bad\npath")
	if strings.Count(initOut.String(), "Next:") != 1 {
		t.Fatalf("unsafe init fallback: %q", initOut.String())
	}
}

func TestCLIGuidanceDoctorHeadlinesAndWarnings(t *testing.T) {
	cfg := filepath.Join(t.TempDir(), "no-config.json")
	for _, tc := range []struct {
		name   string
		remote bool
		checks []doctorFinding
		want   string
	}{
		{"ready", false, []doctorFinding{{Code: "CONFIG_VALID", Severity: doctorPass}}, "LOCALLY READY FOR REVIEW"},
		{"local broken", false, []doctorFinding{{Code: "GIT_MISSING", Severity: doctorFail}}, "Readiness: NOT READY ("},
		{"remote reach", true, []doctorFinding{{Code: "REMOTE_REACHABLE", Severity: doctorPass}}, "CHECKS PASSED; NOT PUBLICATION VERIFIED"},
		{"pending", false, []doctorFinding{{Code: "RECONCILE_REQUIRED", Severity: doctorWarn}}, "EXISTING RELEASE REQUIRES INSPECTION"},
		{"reserved", false, []doctorFinding{{Code: "RUN_RESERVED", Severity: doctorWarn}}, "EXISTING RELEASE REQUIRES INSPECTION"},
		{"ambiguous", false, []doctorFinding{{Code: "PLAN_AMBIGUOUS", Severity: doctorWarn}}, "EXISTING RELEASE REQUIRES INSPECTION"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var out bytes.Buffer
			writeDoctorHuman(&out, cfg, tc.remote, doctorOutput{Checks: tc.checks})
			if !strings.Contains(out.String(), tc.want) {
				t.Fatalf("wrong readiness: %s", out.String())
			}
		})
	}
}

func TestCLIGuidanceInvalidConfigPathDoesNotExposeControl(t *testing.T) {
	path := "not-safe\nfake --yes"
	if got, ok := cliConfigCommand("publish", path); ok || got != "" {
		t.Fatalf("accepted unsafe command %q", got)
	}
	for _, checks := range [][]doctorFinding{
		{{Code: "INVALID_CONFIG", Severity: doctorFail}},
		{{Code: "PLAN_AMBIGUOUS", Severity: doctorWarn}},
		{{Code: "VERSION_MISMATCH", Severity: doctorFail, Fix: "Reinstall provider"}},
		{{Code: "CONFIG_VALID", Severity: doctorPass}},
	} {
		got := doctorPrimaryNext(doctorOutput{Checks: checks}, path)
		if strings.Contains(got, "\n") || strings.Contains(got, "--yes") ||
			strings.Contains(got, "distroplane publish --config") {
			t.Fatalf("unsafe next step: %q", got)
		}
	}
}

func TestCLIGuidanceDoesNotRepeatRemoteOrProviderChecks(t *testing.T) {
	report := doctorOutput{Checks: []doctorFinding{
		{Code: "REMOTE_REACHABLE", Severity: doctorPass, Subject: "network", Message: "tcp accessible"},
		{Code: "PROVIDER_FOUND", Severity: doctorPass, Subject: "target", Message: "found"},
		{Code: "PROVIDER_VERSION", Severity: doctorPass, Subject: "target", Message: "compatible"},
		{Code: "SCHEMA_V2", Severity: doctorPass, Subject: "config", Message: "valid"},
	}}
	var output bytes.Buffer
	writeDoctorHuman(&output, filepath.Join(t.TempDir(), "unknown.json"), true, report)
	got := output.String()
	if strings.Contains(got, "PASS PROVIDER_FOUND") || strings.Count(got, "Next:") != 1 ||
		!strings.Contains(got, "authorization and publication not verified") {
		t.Fatalf("excessive healthy diagnostics: %s", got)
	}
}
