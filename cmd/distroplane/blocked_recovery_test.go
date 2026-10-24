package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aalsanie/distroplane/internal/domain"
	"github.com/aalsanie/distroplane/internal/journal"
)

func TestCLIMultiTargetRecoverMissingCredentialInOriginalRun(t *testing.T) {
	dir := t.TempDir()
	binary := buildFakeProvider(t)
	alpha := copyExecutable(t, binary, filepath.Join(dir, executableName("distroplane-provider-alpha")))
	beta := copyExecutable(t, binary, filepath.Join(dir, executableName("distroplane-provider-beta")))
	if err := os.WriteFile(filepath.Join(dir, "app.bin"), []byte("artifact"), 0o600); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(dir, "distroplane.json")
	config := `{
		"schemaVersion":"1",
		"release":{"id":"v1","artifacts":[{"name":"app","source":"app.bin"}]},
		"targets":[
			{"id":"alpha","provider":{"name":"alpha","executable":` + jsonString(alpha) + `},"configuration":{"mode":"published"}},
			{"id":"beta","provider":{"name":"beta","executable":` + jsonString(beta) + `},"configuration":{"mode":"published","credential":"release"}}
		]
	}`
	if err := os.WriteFile(configPath, []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	if code := execute([]string{"plan", "--config", configPath, "--json"}, &stdout, &stderr); code != exitOK {
		t.Fatalf("plan code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	var planned planOutput
	if err := json.Unmarshal(stdout.Bytes(), &planned); err != nil {
		t.Fatal(err)
	}
	journalPath := filepath.Join(dir, "run.journal")
	applyArgs := []string{"apply", "--config", configPath, "--plan", planned.Path, "--journal", journalPath, "--run", "run-credential-recovery", "--json"}
	stdout.Reset()
	stderr.Reset()
	if code := execute(applyArgs, &stdout, &stderr); code != exitPending {
		t.Fatalf("initial apply code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	var initial stateOutput
	if err := json.Unmarshal(stdout.Bytes(), &initial); err != nil {
		t.Fatal(err)
	}
	if initial.Completed || len(initial.Targets) != 2 {
		t.Fatalf("initial=%+v", initial)
	}
	states := map[string]string{}
	for _, target := range initial.Targets {
		states[target.ID] = target.State
	}
	if states["alpha"] != string(domain.StatePublished) || states["beta"] != string(domain.StateBlocked) {
		t.Fatalf("initial states=%v", states)
	}
	stdout.Reset()
	stderr.Reset()
	if code := execute([]string{"status", "--plan", planned.Path, "--journal", journalPath, "--json"}, &stdout, &stderr); code != exitPending {
		t.Fatalf("blocked status code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	before, err := readJournal(journalPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(before.Events) == 0 || before.Events[0].PlanID != domain.PlanID(planned.PlanID) {
		t.Fatalf("journal is not bound to plan %s", planned.PlanID)
	}
	stdout.Reset()
	stderr.Reset()
	if code := execute([]string{"reconcile", "--config", configPath, "--plan", planned.Path, "--journal", journalPath, "--json"}, &stdout, &stderr); code != exitPending {
		t.Fatalf("blocked reconcile code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	afterReconcile, err := readJournal(journalPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range afterReconcile.Events {
		if event.Type == journal.EventReconcileStarted {
			t.Fatalf("credential-blocked run unexpectedly reconciled: %+v", event)
		}
	}
	const secret = "do-not-leak-secret"
	t.Setenv("DISTROPLANE_TEST_TOKEN", secret)
	stdout.Reset()
	stderr.Reset()
	if code := execute(append(append([]string(nil), applyArgs...), "--credential", "release=DISTROPLANE_TEST_TOKEN"), &stdout, &stderr); code != exitOK {
		t.Fatalf("resumed apply code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	var finished stateOutput
	if err := json.Unmarshal(stdout.Bytes(), &finished); err != nil || !finished.Completed {
		t.Fatalf("finished=%+v err=%v", finished, err)
	}
	read, err := readJournal(journalPath)
	if err != nil {
		t.Fatal(err)
	}
	attempts := map[domain.TargetID]int{}
	dispatches := map[domain.TargetID]int{}
	completions := 0
	for _, entry := range read.Events {
		switch entry.Type {
		case journal.EventAttemptStarted:
			attempts[entry.TargetID]++
		case journal.EventSideEffectDispatched:
			dispatches[entry.TargetID]++
		case journal.EventRunCompleted:
			completions++
		}
	}
	if attempts["alpha"] != 1 || attempts["beta"] != 2 || dispatches["alpha"] != 1 || dispatches["beta"] != 1 || completions != 1 {
		t.Fatalf("attempts=%v dispatches=%v completions=%d", attempts, dispatches, completions)
	}
	data, err := os.ReadFile(journalPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), secret) {
		t.Fatal("credential leaked to journal")
	}
	stdout.Reset()
	stderr.Reset()
	if code := execute(applyArgs, &stdout, &stderr); code != exitOK {
		t.Fatalf("completed run should be idempotent: code=%d stderr=%s", code, stderr.String())
	}
	reloaded, err := readJournal(journalPath)
	if err != nil || len(reloaded.Events) != len(read.Events) {
		t.Fatalf("completed run was modified: err=%v", err)
	}
}
