package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aalsanie/distroplane/internal/journal"
	"github.com/aalsanie/distroplane/internal/runstate"
)

func recoveryCall(t *testing.T, expected int, args ...string) ([]byte, []byte) {
	t.Helper()
	var out, diagnostic bytes.Buffer
	code := execute(args, &out, &diagnostic)
	if code != expected {
		t.Fatalf("%v: code=%d want=%d stdout=%s stderr=%s", args, code, expected, out.String(), diagnostic.String())
	}
	return out.Bytes(), diagnostic.Bytes()
}

func TestResumeReconcilesPendingRunWithoutDuplicateDispatch(t *testing.T) {
	dir, cfg := publishFixture(t, `{"mode":"waiting_external","reconcileState":"published"}`)
	original, _ := recoveryCall(t, exitPending, "publish", "--config", cfg, "--yes", "--json")
	var first publishOutput
	if err := json.Unmarshal(original, &first); err != nil {
		t.Fatal(err)
	}
	if first.Run == nil || first.Run.Completed {
		t.Fatalf("expected pending publication: %+v", first)
	}
	preResume, _ := recoveryCall(t, exitPending, "status", "--config", cfg, "--json")
	var pre stateOutput
	if err := json.Unmarshal(preResume, &pre); err != nil {
		t.Fatal(err)
	}
	if pre.PlanID != first.Plan.PlanID || pre.RunID != first.Run.RunID {
		t.Fatalf("automatic selection chose another identity: %+v", pre)
	}
	out, _ := recoveryCall(t, exitOK, "resume", "--config", cfg, "--release", "release-publish", "--run", first.Run.RunID, "--json")
	var finished stateOutput
	if err := json.Unmarshal(out, &finished); err != nil {
		t.Fatal(err)
	}
	if !finished.Completed || finished.RunID != first.Run.RunID || finished.PlanID != first.Plan.PlanID {
		t.Fatalf("resume changed run identity: %+v", finished)
	}
	before, err := os.ReadFile(first.JournalPath)
	if err != nil {
		t.Fatal(err)
	}
	events, err := readJournal(first.JournalPath)
	if err != nil {
		t.Fatal(err)
	}
	dispatches := 0
	for _, e := range events.Events {
		if e.Type == journal.EventSideEffectDispatched {
			dispatches++
		}
	}
	if dispatches != 1 {
		t.Fatalf("initial publish dispatched %d times", dispatches)
	}
	recoveryCall(t, exitOK, "resume", "--config", cfg, "--json")
	after, err := os.ReadFile(first.JournalPath)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatalf("completed resume mutated the journal: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, ".distroplane", "runs")); err != nil {
		t.Fatalf("run storage disappeared: %v", err)
	}
}

func TestResumeBlockedCredentialAndReconcileOnlyNoDispatch(t *testing.T) {
	_, cfg := publishFixture(t, `{"mode":"published","credential":"publish-auth"}`)
	original, _ := recoveryCall(t, exitPending, "publish", "--config", cfg, "--yes", "--json")
	var first publishOutput
	if err := json.Unmarshal(original, &first); err != nil {
		t.Fatal(err)
	}
	if first.Run == nil {
		t.Fatal("missing blocked state")
	}
	out, _ := recoveryCall(t, exitPending, "resume", "--config", cfg, "--reconcile-only", "--json")
	var blocked stateOutput
	if err := json.Unmarshal(out, &blocked); err != nil {
		t.Fatal(err)
	}
	if blocked.RunID != first.Run.RunID || blocked.Completed {
		t.Fatalf("reconcile-only incorrectly published: %+v", blocked)
	}
	before, err := readJournal(first.JournalPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range before.Events {
		if e.Type == journal.EventSideEffectDispatched {
			t.Fatal("reconcile-only dispatched blocked publication")
		}
	}
	const secret = "resume-token-canary"
	t.Setenv("RECOVERY_ENV_TOKEN", secret)
	out, _ = recoveryCall(t, exitOK, "resume", "--config", cfg, "--credential", "publish-auth=RECOVERY_ENV_TOKEN", "--json")
	var final stateOutput
	if err := json.Unmarshal(out, &final); err != nil || !final.Completed || final.RunID != first.Run.RunID {
		t.Fatalf("credential recovery failed: %+v err=%v", final, err)
	}
	data, err := os.ReadFile(first.JournalPath)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(data, []byte(secret)) || bytes.Contains(out, []byte(secret)) {
		t.Fatal("credential value exposed")
	}
	events, err := readJournal(first.JournalPath)
	if err != nil {
		t.Fatal(err)
	}
	dispatched := 0
	for _, e := range events.Events {
		if e.Type == journal.EventSideEffectDispatched {
			dispatched++
		}
	}
	if dispatched != 1 {
		t.Fatalf("recovery dispatched=%d", dispatched)
	}
}

func TestAutomaticStatusAndCompletedResumeDoNotRequireConfigOrArtifact(t *testing.T) {
	_, cfg := publishFixture(t, `{"mode":"published"}`)
	raw, _ := recoveryCall(t, exitOK, "publish", "--config", cfg, "--yes", "--json")
	var result publishOutput
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(cfg); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(filepath.Dir(cfg), "app.bin")); err != nil {
		t.Fatal(err)
	}
	auto, _ := recoveryCall(t, exitOK, "status", "--config", cfg, "--release", "release-publish", "--json")
	var selected stateOutput
	if err := json.Unmarshal(auto, &selected); err != nil {
		t.Fatal(err)
	}
	if !selected.Completed || selected.RunID != result.Run.RunID {
		t.Fatalf("status should replay retained journal: %+v", selected)
	}
	original, err := os.ReadFile(result.JournalPath)
	if err != nil {
		t.Fatal(err)
	}
	resumed, _ := recoveryCall(t, exitOK, "resume", "--config", cfg, "--json")
	var recovered stateOutput
	if err := json.Unmarshal(resumed, &recovered); err != nil || !recovered.Completed {
		t.Fatalf("completed resume must not need artifact: %+v %v", recovered, err)
	}
	replayed, err := os.ReadFile(result.JournalPath)
	if err != nil || !bytes.Equal(original, replayed) {
		t.Fatalf("read-only resume modified journal: %v", err)
	}
}

func TestResumeMissingJournalAndCorruptJournalFailClosed(t *testing.T) {
	for _, corruption := range []string{"missing", "corrupt"} {
		t.Run(corruption, func(t *testing.T) {
			_, cfg := publishFixture(t, `{"mode":"waiting_external"}`)
			raw, _ := recoveryCall(t, exitPending, "publish", "--config", cfg, "--yes", "--json")
			var result publishOutput
			if err := json.Unmarshal(raw, &result); err != nil {
				t.Fatal(err)
			}
			if corruption == "missing" {
				if err := os.Remove(result.JournalPath); err != nil {
					t.Fatal(err)
				}
			} else {
				if err := os.WriteFile(result.JournalPath, []byte("corrupted durable record"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			for _, command := range []string{"resume", "status"} {
				_, diagnostic := recoveryCall(t, exitInvalid, command, "--config", cfg, "--json")
				if !bytes.Contains(diagnostic, []byte("run_selection_failed")) {
					t.Fatalf("%s did not fail closed: %s", command, diagnostic)
				}
			}
			if corruption == "missing" {
				if _, err := os.Stat(result.JournalPath); !os.IsNotExist(err) {
					t.Fatalf("missing journal was recreated: %v", err)
				}
			}
		})
	}
}

func TestResumeDoesNotRegenerateOrSelectUnapprovedPlan(t *testing.T) {
	_, cfg := publishFixture(t, `{"mode":"published"}`)
	_, d := recoveryCall(t, exitInvalid, "resume", "--config", cfg, "--json")
	if !bytes.Contains(d, []byte("run_selection_failed")) {
		t.Fatalf("no-catalog selection: %s", d)
	}
	recoveryCall(t, exitOK, "plan", "--config", cfg, "--json")
	_, d = recoveryCall(t, exitInvalid, "resume", "--config", cfg, "--json")
	if !bytes.Contains(d, []byte("run_selection_failed")) {
		t.Fatalf("unapproved plan selected: %s", d)
	}
	records, err := (runstate.Store{Root: filepath.Join(filepath.Dir(cfg), ".distroplane")}).Inventory()
	if err != nil || len(records) != 1 || records[0].RunID != "" {
		t.Fatalf("resume claimed unapproved plan: %+v %v", records, err)
	}
}

func TestResumeRejectsLostArtifactsButAllowsReconciliation(t *testing.T) {
	dir, cfg := publishFixture(t, `{"mode":"waiting_external","reconcileState":"published"}`)
	raw, _ := recoveryCall(t, exitPending, "publish", "--config", cfg, "--yes", "--json")
	var result publishOutput
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(dir, "app.bin")); err != nil {
		t.Fatal(err)
	}
	_, d := recoveryCall(t, exitInvalid, "resume", "--config", cfg, "--json")
	if !bytes.Contains(d, []byte("artifact_changed")) {
		t.Fatalf("missing artifact did not block execution: %s", d)
	}
	out, _ := recoveryCall(t, exitOK, "resume", "--config", cfg, "--reconcile-only", "--json")
	var final stateOutput
	if err := json.Unmarshal(out, &final); err != nil || !final.Completed || final.RunID != result.Run.RunID {
		t.Fatalf("reconcile-only failed without artifact: %+v %v", final, err)
	}
}

func TestResumeAndStatusRejectInvalidSelection(t *testing.T) {
	_, cfg := publishFixture(t, `{"mode":"published"}`)
	for _, args := range [][]string{
		{"resume", "--config", cfg, "--concurrency", "-1"},
		{"resume", "--config", cfg, "--unrecognized"},
		{"status", "--plan", "only"},
		{"status", "--journal", "only"},
		{"status", "--plan", "plan", "--journal", "journal", "--release", "release"},
	} {
		_, diagnostic := recoveryCall(t, exitUsage, args...)
		if len(diagnostic) == 0 {
			t.Fatalf("missing usage diagnostic: %v", args)
		}
	}
	for _, cmd := range []string{"status", "resume"} {
		_, diagnostic := recoveryCall(t, exitInvalid, cmd, "--config", cfg, "--release", "missing", "--json")
		if !strings.Contains(string(diagnostic), "run_selection_failed") {
			t.Fatalf("unknown release accepted: %s", diagnostic)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var out, diagnostic bytes.Buffer
	if code := runResumeContext(ctx, []string{"--config", cfg}, &out, &diagnostic); code != exitOperational {
		t.Fatalf("cancelled resume code=%d stderr=%s", code, diagnostic.String())
	}
}

func TestRecoverySelectionRejectsAmbiguousCatalogWithoutChoosingLatest(t *testing.T) {
	dir, cfg := publishFixture(t, `{"mode":"published"}`)
	raw, err := os.ReadFile(cfg)
	if err != nil {
		t.Fatal(err)
	}
	other := filepath.Join(dir, "other-config.json")
	changed := strings.Replace(string(raw), "release-publish", "release-other", 1)
	if err := os.WriteFile(other, []byte(changed), 0600); err != nil {
		t.Fatal(err)
	}
	recoveryCall(t, exitOK, "publish", "--config", cfg, "--yes", "--json")
	recoveryCall(t, exitOK, "publish", "--config", other, "--yes", "--json")
	for _, command := range []string{"resume", "status"} {
		_, diagnostic := recoveryCall(t, exitInvalid, command, "--config", cfg, "--json")
		if !bytes.Contains(diagnostic, []byte("run_selection_failed")) || !bytes.Contains(diagnostic, []byte("ambiguous")) {
			t.Fatalf("%s selected arbitrarily: %s", command, diagnostic)
		}
	}
	out, _ := recoveryCall(t, exitOK, "status", "--config", cfg, "--release", "release-other", "--json")
	var selected stateOutput
	if err := json.Unmarshal(out, &selected); err != nil || !selected.Completed {
		t.Fatalf("explicit release selection failed: %+v %v", selected, err)
	}
	out, _ = recoveryCall(t, exitOK, "resume", "--config", cfg, "--run", selected.RunID, "--json")
	var resumed stateOutput
	if err := json.Unmarshal(out, &resumed); err != nil || resumed.RunID != selected.RunID {
		t.Fatalf("explicit run selection failed: %+v %v", resumed, err)
	}
	records, err := (runstate.Store{Root: filepath.Join(dir, ".distroplane")}).Inventory()
	if err != nil || len(records) != 2 {
		t.Fatalf("recovery lost durable plans: %+v %v", records, err)
	}
}
