package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aalsanie/distroplane/internal/config"
	"github.com/aalsanie/distroplane/internal/domain"
	"github.com/aalsanie/distroplane/internal/journal"
)

func TestCLICredentialBackedApply(t *testing.T) {
	dir := t.TempDir()
	providerBinary := buildFakeProvider(t)
	alpha := copyExecutable(t, providerBinary, filepath.Join(dir, executableName("distroplane-provider-alpha")))
	artifact := filepath.Join(dir, "app.bin")
	if err := os.WriteFile(artifact, []byte("artifact"), 0o600); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(dir, "distroplane.json")
	rawConfig := `{"schemaVersion":"1","release":{"id":"v1","artifacts":[{"name":"app","source":"app.bin"}]},"targets":[{"id":"alpha","provider":{"name":"alpha","executable":` + jsonString(alpha) + `},"configuration":{"mode":"published","credential":"release"}}]}`
	if err := os.WriteFile(configPath, []byte(rawConfig), 0o600); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	if code := execute([]string{"plan", "--config", configPath, "--state-dir", "state", "--json"}, &stdout, &stderr); code != exitOK {
		t.Fatalf("plan code=%d stderr=%s", code, stderr.String())
	}
	var planned planOutput
	if err := json.Unmarshal(stdout.Bytes(), &planned); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(planned.Path, filepath.Join("state", "plans")) {
		t.Fatalf("path=%q", planned.Path)
	}

	stdout.Reset()
	stderr.Reset()
	withoutCredential := filepath.Join(dir, "without-credential.journal")
	if code := execute([]string{"apply", "--config", configPath, "--plan", planned.Path, "--journal", withoutCredential, "--json"}, &stdout, &stderr); code != exitFailed {
		t.Fatalf("missing credential code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}

	const secret = "cli-secret-canary"
	t.Setenv("DISTROPLANE_TEST_TOKEN", secret)
	stdout.Reset()
	stderr.Reset()
	withCredential := filepath.Join(dir, "with-credential.journal")
	if code := execute([]string{"apply", "--config", configPath, "--plan", planned.Path, "--journal", withCredential, "--credential", "release=DISTROPLANE_TEST_TOKEN", "--json"}, &stdout, &stderr); code != exitOK {
		t.Fatalf("credential apply code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	if strings.Contains(stdout.String(), secret) || strings.Contains(stderr.String(), secret) {
		t.Fatal("credential leaked through CLI output")
	}
}

func TestExecutionDriverBindingAndCredentialValidation(t *testing.T) {
	plan := minimalStatePlan(t)
	loaded := config.Loaded{BaseDir: t.TempDir()}
	if _, err := executionDriver(loaded, plan, nil); err == nil || !strings.Contains(err.Error(), "missing from config") {
		t.Fatalf("missing target err=%v", err)
	}

	loaded.Config.Targets = []config.Target{{ID: "target", Provider: config.Provider{Name: "other"}}}
	if _, err := executionDriver(loaded, plan, nil); err == nil || !strings.Contains(err.Error(), "config has") {
		t.Fatalf("provider mismatch err=%v", err)
	}

	providerBinary := buildFakeProvider(t)
	loaded.Config.Targets = []config.Target{{ID: "target", Provider: config.Provider{Name: "fake", Executable: providerBinary}}}
	if _, err := executionDriver(loaded, plan, credentialFlags{domain.CredentialRef("release"): "bad-name"}); err == nil {
		t.Fatal("invalid credential source accepted")
	}
	if _, err := executionDriver(loaded, plan, credentialFlags{domain.CredentialRef("release"): "TOKEN"}); err != nil {
		t.Fatalf("valid credential mapping: %v", err)
	}

	flags := credentialFlags{}
	if err := flags.Set(" bad=TOKEN"); err == nil {
		t.Fatal("invalid credential reference accepted")
	}
}

func TestResolveRunIDRejectsCorruptJournal(t *testing.T) {
	path := filepath.Join(t.TempDir(), "run.journal")
	runID, _ := domain.NewRunID("run-corrupt")
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
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	raw[0] = 'X'
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := resolveRunID(path, "", true); err == nil {
		t.Fatal("corrupt journal accepted")
	}
}

func TestStateExitTerminalResults(t *testing.T) {
	if code := stateExit(resultState(t, domain.StateRejected)); code != exitRejected {
		t.Fatalf("rejected exit=%d", code)
	}
	if code := stateExit(resultState(t, domain.StateFailed)); code != exitFailed {
		t.Fatalf("failed exit=%d", code)
	}
	if code := stateExit(resultState(t, domain.StatePublished)); code != exitOK {
		t.Fatalf("published exit=%d", code)
	}
}

func resultState(t *testing.T, result domain.NormalizedState) journal.DerivedState {
	t.Helper()
	plan := singleOperationPlan(t)
	runID, _ := domain.NewRunID("run-result")
	writer, err := journal.OpenWriter(filepath.Join(t.TempDir(), "run.journal"), runID)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	operation := plan.Operations()[0]
	entries := []journal.Entry{
		{RunID: runID, Type: journal.EventRunStarted},
		{RunID: runID, Type: journal.EventAttemptStarted, OperationID: operation.ID(), TargetID: operation.TargetID(), Payload: journal.Payload{Attempt: 1}},
		{RunID: runID, Type: journal.EventSideEffectDispatched, OperationID: operation.ID(), TargetID: operation.TargetID(), Payload: journal.Payload{Attempt: 1}},
	}
	payload := journal.Payload{Attempt: 1, State: result}
	switch result {
	case domain.StatePublished, domain.StateRejected:
		payload.Evidence = json.RawMessage(`{"provider":"test"}`)
	case domain.StateFailed:
		payload.ErrorCode = "TEST_FAILURE"
	}
	entries = append(entries, journal.Entry{RunID: runID, Type: journal.EventOperationResult, OperationID: operation.ID(), TargetID: operation.TargetID(), Payload: payload})
	for _, entry := range entries {
		if _, err := writer.Append(entry); err != nil {
			t.Fatal(err)
		}
	}
	if result == domain.StatePublished {
		if _, err := writer.Append(journal.Entry{RunID: runID, Type: journal.EventRunCompleted}); err != nil {
			t.Fatal(err)
		}
	}
	state, err := journal.Reduce(plan, writer.Events())
	if err != nil {
		t.Fatal(err)
	}
	return state
}

func singleOperationPlan(t *testing.T) domain.Plan {
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
	operationID, _ := domain.NewOperationID("operation")
	payload, _ := domain.NewJSONValue([]byte(`{}`))
	operation, _ := domain.NewOperation(operationID, targetID, provider, "publish", nil, true, "key", 0, payload)
	planID, _ := domain.NewPlanID("plan-result")
	plan, err := domain.NewPlan(planID, "1", "1", release, []domain.Target{target}, []domain.Operation{operation})
	if err != nil {
		t.Fatal(err)
	}
	return plan
}
