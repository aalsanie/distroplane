package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/aalsanie/distroplane/internal/domain"
	"github.com/aalsanie/distroplane/internal/journal"
)

func TestCLIEndToEndMultiProviderPlanApplyStatusReconcile(t *testing.T) {
	dir := t.TempDir()
	providerBinary := buildFakeProvider(t)
	alpha := copyExecutable(t, providerBinary, filepath.Join(dir, executableName("distroplane-provider-alpha")))
	beta := copyExecutable(t, providerBinary, filepath.Join(dir, executableName("distroplane-provider-beta")))
	artifact := filepath.Join(dir, "app.bin")
	if err := os.WriteFile(artifact, []byte("artifact"), 0o600); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(dir, "distroplane.json")
	rawConfig := `{
	  "schemaVersion":"1",
	  "release":{"id":"v1","artifacts":[{"name":"app","source":"app.bin"}]},
	  "targets":[
	    {"id":"alpha","provider":{"name":"alpha","executable":` + jsonString(alpha) + `},"configuration":{"mode":"published"}},
	    {"id":"beta","provider":{"name":"beta","executable":` + jsonString(beta) + `},"configuration":{"mode":"waiting_external","reconcileState":"published"}}
	  ]
	}`
	if err := os.WriteFile(configPath, []byte(rawConfig), 0o600); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	if code := execute([]string{"validate", "--config", configPath, "--json"}, &stdout, &stderr); code != exitOK {
		t.Fatalf("validate code=%d stderr=%s", code, stderr.String())
	}
	var validated validateOutput
	if err := json.Unmarshal(stdout.Bytes(), &validated); err != nil || validated.OutputSchemaVersion != outputSchemaVersion || !validated.Valid || validated.Targets != 2 {
		t.Fatalf("validated=%+v err=%v", validated, err)
	}

	stdout.Reset()
	stderr.Reset()
	if code := execute([]string{"plan", "--config", configPath, "--json"}, &stdout, &stderr); code != exitOK {
		t.Fatalf("plan code=%d stderr=%s", code, stderr.String())
	}
	var planned planOutput
	if err := json.Unmarshal(stdout.Bytes(), &planned); err != nil {
		t.Fatal(err)
	}
	if planned.OutputSchemaVersion != outputSchemaVersion || planned.Targets != 2 || planned.Operations != 2 || planned.PlanID == "" {
		t.Fatalf("planned=%+v", planned)
	}
	journalPath := filepath.Join(dir, "run.journal")

	stdout.Reset()
	stderr.Reset()
	if code := execute([]string{"apply", "--config", configPath, "--plan", planned.Path, "--journal", journalPath, "--run", "run-e2e", "--concurrency", "1", "--json"}, &stdout, &stderr); code != exitPending {
		t.Fatalf("apply code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	var applied stateOutput
	if err := json.Unmarshal(stdout.Bytes(), &applied); err != nil {
		t.Fatal(err)
	}
	if applied.OutputSchemaVersion != outputSchemaVersion || applied.RunID != "run-e2e" || len(applied.Targets) != 2 {
		t.Fatalf("applied=%+v", applied)
	}

	stdout.Reset()
	stderr.Reset()
	if code := execute([]string{"status", "--plan", planned.Path, "--journal", journalPath, "--json"}, &stdout, &stderr); code != exitPending {
		t.Fatalf("status code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}

	stdout.Reset()
	stderr.Reset()
	if code := execute([]string{"reconcile", "--config", configPath, "--plan", planned.Path, "--journal", journalPath, "--concurrency", "1", "--json"}, &stdout, &stderr); code != exitOK {
		t.Fatalf("reconcile code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	var reconciled stateOutput
	if err := json.Unmarshal(stdout.Bytes(), &reconciled); err != nil {
		t.Fatal(err)
	}
	if reconciled.OutputSchemaVersion != outputSchemaVersion || !reconciled.Completed {
		t.Fatalf("reconciled=%+v", reconciled)
	}
	for _, target := range reconciled.Targets {
		if target.State != string(domain.StatePublished) {
			t.Fatalf("target=%+v", target)
		}
	}

	stdout.Reset()
	stderr.Reset()
	if code := execute([]string{"status", "--plan", planned.Path, "--journal", journalPath}, &stdout, &stderr); code != exitOK {
		t.Fatalf("final status code=%d stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "completed true") || !strings.Contains(stdout.String(), "target alpha PUBLISHED") {
		t.Fatalf("stdout=%s", stdout.String())
	}
}

func TestCLIUsageAndInvalidInputs(t *testing.T) {
	cases := [][]string{
		{"validate", "extra"},
		{"plan", "--unknown"},
		{"apply"},
		{"status"},
		{"reconcile"},
		{"apply", "--plan", "p", "--journal", "j", "--concurrency", "-1"},
	}
	for _, args := range cases {
		var stdout, stderr bytes.Buffer
		if code := execute(args, &stdout, &stderr); code != exitUsage {
			t.Fatalf("%v code=%d stderr=%s", args, code, stderr.String())
		}
	}
	dir := t.TempDir()
	badConfig := filepath.Join(dir, "bad.json")
	if err := os.WriteFile(badConfig, []byte(`{"schemaVersion":"2"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	if code := execute([]string{"validate", "--config", badConfig, "--json"}, &stdout, &stderr); code != exitInvalid {
		t.Fatalf("code=%d stderr=%s", code, stderr.String())
	}
	var output errorOutput
	if err := json.Unmarshal(stderr.Bytes(), &output); err != nil || output.OutputSchemaVersion != outputSchemaVersion || output.Error.Kind != "invalid_config" {
		t.Fatalf("output=%+v err=%v", output, err)
	}
}

func TestCredentialFlagsRunIDAndStateExit(t *testing.T) {
	flags := credentialFlags{}
	if err := flags.Set("release=TOKEN"); err != nil {
		t.Fatal(err)
	}
	if err := flags.Set("release=OTHER"); err == nil {
		t.Fatal("duplicate credential accepted")
	}
	if err := flags.Set("bad"); err == nil {
		t.Fatal("bad credential accepted")
	}
	if flags.String() != "" {
		t.Fatal("credential String must redact mappings")
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "missing.journal")
	id, err := resolveRunID(path, "run-fixed", true)
	if err != nil || id != "run-fixed" {
		t.Fatalf("id=%q err=%v", id, err)
	}
	if _, err := resolveRunID(path, "", false); err == nil {
		t.Fatal("reconcile created a run")
	}
	generated, err := resolveRunID(path, "", true)
	if err != nil || !generated.Valid() || generated == "run-fixed" {
		t.Fatalf("generated=%q err=%v", generated, err)
	}

	plan := minimalStatePlan(t)
	runID, _ := domain.NewRunID("run-existing")
	writer, err := journal.OpenWriter(path, runID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Append(journal.Entry{RunID: runID, Type: journal.EventRunStarted}); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if got, err := resolveRunID(path, "", false); err != nil || got != runID {
		t.Fatalf("got=%q err=%v", got, err)
	}
	if _, err := resolveRunID(path, "other", true); err == nil {
		t.Fatal("mismatched run accepted")
	}
	read, err := readJournal(path)
	if err != nil {
		t.Fatal(err)
	}
	state, err := journal.Reduce(plan, read.Events)
	if err != nil {
		t.Fatal(err)
	}
	if stateExit(state) != exitPending {
		t.Fatalf("exit=%d", stateExit(state))
	}
	view := stateView(plan, state, true)
	if !view.JournalTruncatedTail || view.RunID != "run-existing" {
		t.Fatalf("view=%+v", view)
	}
}

func TestWriteHelpers(t *testing.T) {
	var buffer bytes.Buffer
	if code := writeCommandError(&buffer, false, exitInvalid, "bad", os.ErrInvalid); code != exitInvalid || !strings.Contains(buffer.String(), "bad:") {
		t.Fatalf("code=%d output=%q", code, buffer.String())
	}
	buffer.Reset()
	if code := writeCommandError(&buffer, true, exitInvalid, "bad", os.ErrInvalid); code != exitInvalid || !json.Valid(buffer.Bytes()) {
		t.Fatalf("code=%d output=%q", code, buffer.String())
	}
	if !hasJSON([]string{"x", "--json"}) || hasJSON([]string{"x"}) {
		t.Fatal("hasJSON mismatch")
	}
	var text bytes.Buffer
	writeStateText(&text, stateOutput{RunID: "run", PlanID: "plan", Completed: false, Targets: []targetStateOutput{{ID: "t", State: "WAITING_EXTERNAL", ReconcileRequired: true, Ambiguous: true}}})
	if !strings.Contains(text.String(), "reconcile-required ambiguous") {
		t.Fatalf("text=%q", text.String())
	}
}

func buildFakeProvider(t *testing.T) string {
	t.Helper()
	name := executableName("distroplane-provider-fake")
	path := filepath.Join(t.TempDir(), name)
	command := exec.Command("go", "build", "-o", path, "../distroplane-provider-fake")
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("build fake provider: %v\n%s", err, output)
	}
	return path
}

func copyExecutable(t *testing.T, source, destination string) string {
	t.Helper()
	data, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(destination, data, 0o700); err != nil {
		t.Fatal(err)
	}
	return destination
}

func executableName(name string) string {
	if runtime.GOOS == "windows" {
		return name + ".exe"
	}
	return name
}

func jsonString(value string) string {
	data, _ := json.Marshal(value)
	return string(data)
}

func minimalStatePlan(t *testing.T) domain.Plan {
	t.Helper()
	digest, _ := domain.NewSHA256Digest(strings.Repeat("a", 64))
	artifact, _ := domain.NewArtifact("app", filepath.Join(t.TempDir(), "app"), digest, 1, "")
	releaseID, _ := domain.NewReleaseID("release")
	release, _ := domain.NewRelease(releaseID, []domain.Artifact{artifact})
	providerName, _ := domain.NewProviderName("fake")
	providerVersion, _ := domain.NewProviderVersion("1")
	provider, _ := domain.NewProviderRef(providerName, providerVersion)
	targetID, _ := domain.NewTargetID("target")
	configuration, _ := domain.NewJSONValue([]byte(`{}`))
	target, _ := domain.NewTarget(targetID, provider, configuration)
	planID, _ := domain.NewPlanID("plan")
	plan, err := domain.NewPlan(planID, "1", "1", release, []domain.Target{target}, nil)
	if err != nil {
		t.Fatal(err)
	}
	return plan
}


func TestCLIPlanHonorsCancelledContext(t *testing.T) {
	dir := t.TempDir()
	providerBinary := buildFakeProvider(t)
	artifact := filepath.Join(dir, "app.bin")
	if err := os.WriteFile(artifact, []byte("artifact"), 0o600); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(dir, "distroplane.json")
	rawConfig := `{
	  "schemaVersion":"1",
	  "release":{"id":"v1","artifacts":[{"name":"app","source":"app.bin"}]},
	  "targets":[{"id":"fake","provider":{"name":"fake","executable":` + jsonString(providerBinary) + `},"configuration":{"mode":"published"}}]
	}`
	if err := os.WriteFile(configPath, []byte(rawConfig), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var stdout, stderr bytes.Buffer
	code := executeContext(ctx, []string{"plan", "--config", configPath, "--json"}, &stdout, &stderr)
	if code != exitOperational {
		t.Fatalf("code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	var output errorOutput
	if err := json.Unmarshal(stderr.Bytes(), &output); err != nil {
		t.Fatal(err)
	}
	if output.OutputSchemaVersion != outputSchemaVersion || output.Error.Kind != "plan_failed" {
		t.Fatalf("output=%+v", output)
	}
}
