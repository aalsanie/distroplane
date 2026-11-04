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

	"github.com/aalsanie/distroplane/internal/journal"
)

func guidanceState(state string, completed, reconcile, ambiguous bool, code string) stateOutput {
	return stateOutput{
		OutputSchemaVersion: outputSchemaVersion,
		PlanID: "plan-a", RunID: "run-a", Completed: completed,
		Targets: []targetStateOutput{{ID: "alpha", State: state, ReconcileRequired: reconcile, Ambiguous: ambiguous}},
		Operations: []operationStateOutput{{
			ID: "op-a", TargetID: "alpha", State: state, ReconcileRequired: reconcile,
			Ambiguous: ambiguous, ErrorCode: code,
		}},
	}
}

func TestReleaseGuidanceStateDecisions(t *testing.T) {
	tests := []struct {
		name, state, errorCode, label, advice string
		completed, reconcile, ambiguous       bool
	}{
		{"published", "PUBLISHED", "", "recorded as published", "recorded evidence", true, false, false},
		{"external", "WAITING_EXTERNAL", "", "waiting for external review", "external acceptance", false, true, false},
		{"ambiguous", "WAITING_EXTERNAL", "", "uncertain external outcome", "may already have occurred", false, true, true},
		{"credential blocked", "BLOCKED", "CREDENTIAL_UNAVAILABLE", "blocked before publication dispatch", "approved credential", false, false, false},
		{"resolution blocked", "BLOCKED", "CREDENTIAL_RESOLUTION_FAILED", "blocked before publication dispatch", "approved credential", false, false, false},
		{"dependency blocked", "BLOCKED", "DEPENDENCY_TERMINAL", "unresolved prerequisite", "recorded prerequisite", false, false, false},
		{"rejected", "REJECTED", "", "finished with rejection", "recorded cause", true, false, false},
		{"failed", "FAILED", "", "failed or cancelled", "recorded cause", true, false, false},
		{"cancelled", "CANCELLED", "", "failed or cancelled", "recorded cause", true, false, false},
		{"planned", "PLANNED", "", "incomplete", "original run", false, false, false},
		{"ready", "READY", "", "incomplete", "original run", false, false, false},
		{"running", "RUNNING", "", "incomplete", "original run", false, false, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out := guidanceState(tt.state, tt.completed, tt.reconcile, tt.ambiguous, tt.errorCode)
			a := assessRelease(out)
			if label := a.label(out); !strings.Contains(label, tt.label) {
				t.Fatalf("label %q, want %q", label, tt.label)
			}
			if next := a.next(out, stateTextSelection{}); !strings.Contains(next, tt.advice) {
				t.Fatalf("next %q, want %q", next, tt.advice)
			}
			var human bytes.Buffer
			writeStateText(&human, out)
			if !strings.Contains(human.String(), "run run-a\nplan plan-a\ncompleted") ||
				!strings.Contains(human.String(), "target alpha "+tt.state) ||
				strings.Count(human.String(), "Next:") != 1 {
				t.Fatalf("missing release identity or action: %s", human.String())
			}
		})
	}
}

func TestReleaseGuidanceMixedOutcomesPreserveAllTargets(t *testing.T) {
	for _, first := range []string{"FAILED", "REJECTED", "CANCELLED"} {
		t.Run(first, func(t *testing.T) {
			for _, ambiguous := range []bool{false, true} {
				out := guidanceState(first, false, false, false, "")
				out.Targets = append(out.Targets, targetStateOutput{
					ID: "beta", State: "WAITING_EXTERNAL", ReconcileRequired: true, Ambiguous: ambiguous,
				})
				out.Operations = append(out.Operations, operationStateOutput{
					ID: "op-b", TargetID: "beta", State: "WAITING_EXTERNAL", ReconcileRequired: true, Ambiguous: ambiguous,
				})
				a := assessRelease(out)
				if !strings.HasPrefix(a.label(out), "mixed:") {
					t.Fatalf("mixed state hidden: %q", a.label(out))
				}
				sel := stateTextSelection{configPath: "/project/cfg.json", planPath: "/project/plan.json",
					journalPath: "/project/run.journal", runID: "run-a", catalog: true, configVerified: true}
				var human bytes.Buffer
				writeStateTextWithSelection(&human, out, sel)
				msg := human.String()
				if !strings.Contains(msg, "target alpha "+first) ||
					!strings.Contains(msg, "target beta WAITING_EXTERNAL") ||
					!strings.Contains(msg, "distroplane resume --config") ||
					!strings.Contains(msg, "--reconcile-only") ||
					strings.Contains(msg, "distroplane publish") {
					t.Fatalf("mixed state or recovery missing: %s", msg)
				}
				if ambiguous && !strings.Contains(msg, "may already have occurred") {
					t.Fatalf("ambiguous side effect hidden: %s", msg)
				}
			}
		})
	}
}

func TestReleaseGuidancePartialPublicationIsNotAllPublished(t *testing.T) {
	out := guidanceState("PUBLISHED", false, false, false, "")
	a := assessRelease(out)
	if a.published || strings.Contains(a.label(out), "recorded as published") {
		t.Fatalf("incomplete run presented as published: %+v", a)
	}
	out.Completed = true
	out.Operations = append(out.Operations, operationStateOutput{ID: "unsettled", State: "READY"})
	if assessRelease(out).published {
		t.Fatal("unfinished operation hidden by completed flag")
	}
	out.Operations = nil
	if assessRelease(out).published {
		t.Fatal("missing operations treated as complete publication")
	}
	out.Operations = []operationStateOutput{{State: "PUBLISHED"}}
	out.Targets = nil
	if assessRelease(out).published {
		t.Fatal("missing targets treated as complete publication")
	}
	out.Targets = []targetStateOutput{{ID: "alpha", State: "PUBLISHED"}}
	out.Cancelled = true
	if a := assessRelease(out); a.published || !a.cancelled {
		t.Fatalf("cancelled run presented as published: %+v", a)
	}
	out.Cancelled = false
	out.JournalTruncatedTail = true
	if a := assessRelease(out); a.published ||
		!strings.Contains(a.label(out), "durable-prefix") ||
		!strings.Contains(a.next(out, stateTextSelection{}), "Preserve the original journal") {
		t.Fatalf("truncated journal presented as published: %+v", a)
	}
}

func TestReleaseGuidanceSelectorsAreSupportedAndQuoted(t *testing.T) {
	config := filepath.Join("root with space", "it's; $HOME.json")
	plan := filepath.Join("original", "plan a.json")
	journal := filepath.Join("original", "journal b")
	id := "run-123"
	cat := stateTextSelection{configPath: config, runID: id, catalog: true}
	cfgQuoted, cfgOK := shellArgument(config, runtime.GOOS)
	runQuoted, runOK := shellArgument(id, runtime.GOOS)
	if !cfgOK || !runOK {
		t.Fatal("safe selectors rejected")
	}
	for _, tt := range []struct {
		verb, fragment string
	}{
		{"status", "distroplane status --config " + cfgQuoted + " --run " + runQuoted},
		{"resume", "distroplane resume --config " + cfgQuoted + " --run " + runQuoted},
		{"reconcile", "distroplane resume --config " + cfgQuoted + " --run " + runQuoted + " --reconcile-only"},
	} {
		if got := cat.command(tt.verb); got != tt.fragment {
			t.Fatalf("%s command=%q want=%q", tt.verb, got, tt.fragment)
		}
	}
	explicit := stateTextSelection{configPath: config, planPath: plan, journalPath: journal, configVerified: true}
	for _, verb := range []string{"status", "reconcile", "evidence"} {
		got := explicit.command(verb)
		if !strings.HasPrefix(got, "distroplane "+verb+" ") ||
			!strings.Contains(got, "--plan ") || !strings.Contains(got, "--journal ") ||
			(verb == "reconcile" && !strings.Contains(got, "--config ")) {
			t.Fatalf("bad exact-path command %q", got)
		}
	}
	if explicit.command("resume") != "" {
		t.Fatal("unsupported resume --plan/--journal invented")
	}
	explicit.configVerified = false
	if explicit.command("reconcile") != "" {
		t.Fatal("reconciliation recommended with an unverified config")
	}
	if explicit.command("unknown") != "" {
		t.Fatal("unsupported command invented")
	}
	for _, field := range []string{"config", "plan", "journal", "run"} {
		bad := cat
		switch field {
		case "config":
			bad.configPath = "bad\nconfig"
		case "run":
			bad.runID = "bad\nrun"
		case "plan":
			bad = explicit
			bad.planPath = "bad\nplan"
		case "journal":
			bad = explicit
			bad.journalPath = "bad\njournal"
		}
		verb := "reconcile"
		if field == "plan" || field == "journal" {
			verb = "status"
		}
		if result := bad.command(verb); result != "" {
			t.Fatalf("unsafe %s selector made copyable: %q", field, result)
		}
	}
	if cat.command("evidence") != "" {
		t.Fatal("evidence command invented without plan/journal")
	}
}

func TestReleaseGuidanceSafeFallbacksAndTruncatedState(t *testing.T) {
	out := guidanceState("WAITING_EXTERNAL", false, true, true, "")
	out.JournalTruncatedTail = true
	out.Operations[0].ErrorCode = "PROVIDER\nSECRET"
	out.Operations[0].ID = "operation\nfake"
	out.Targets[0].ID = "target\x1b[0m"
	out.PlanID = "plan\x1b[0m"
	out.RunID = "run\nspoof"
	var human bytes.Buffer
	writeStateTextWithSelection(&human, out, stateTextSelection{})
	msg := human.String()
	if strings.Contains(msg, "\x1b") || strings.Contains(msg, "\nspoof") ||
		strings.Contains(msg, "\nfake") || strings.Contains(msg, "PROVIDER\nSECRET") ||
		!strings.Contains(msg, "durable-prefix") ||
		!strings.Contains(msg, "original plan and journal") ||
		strings.Count(msg, "Next:") != 1 {
		t.Fatalf("untrusted data rendered unsafely: %q", msg)
	}
	out = guidanceState("REJECTED", true, false, false, "")
	a := assessRelease(out)
	if !strings.Contains(a.next(out, stateTextSelection{}), "Inspect the recorded cause") {
		t.Fatalf("no safe rejection fallback: %+v", a)
	}
	out = guidanceState("BLOCKED", false, false, false, "")
	if !strings.Contains(assessRelease(out).next(out, stateTextSelection{}), "run catalog") {
		t.Fatal("no same-run blocked fallback")
	}
	out = guidanceState("PUBLISHED", true, false, false, "")
	if !strings.Contains(assessRelease(out).next(out, stateTextSelection{}), "recorded evidence") {
		t.Fatal("publication incorrectly assumes remote acceptance")
	}
	out = guidanceState("PLANNED", false, false, false, "")
	if !strings.Contains(assessRelease(out).next(out, stateTextSelection{}), "original plan and journal") {
		t.Fatal("missing generic safe fallback")
	}
}

func TestReleaseGuidanceHumanPublishStatusAndResume(t *testing.T) {
	for _, tc := range []struct {
		mode          string
		exit          int
		label, advice string
	}{
		{"published", exitOK, "recorded as published", "recorded evidence"},
		{"waiting_external", exitPending, "waiting for external review", "--reconcile-only"},
		{"ambiguous", exitPending, "uncertain external outcome", "--reconcile-only"},
		{"rejected", exitRejected, "finished with rejection", "distroplane status --config"},
		{"permanent_error", exitFailed, "failed or cancelled", "distroplane status --config"},
	} {
		t.Run(tc.mode, func(t *testing.T) {
			_, config := publishFixture(t, `{"mode":"` + tc.mode + `"}`)
			var human, errOut bytes.Buffer
			exit := runPublishContext(context.Background(),
				[]string{"--config", config, "--yes"}, nil, &human, &errOut)
			if exit != tc.exit {
				t.Fatalf("publish exit=%d expected=%d stderr=%s stdout=%s", exit, tc.exit, errOut.String(), human.String())
			}
			if !strings.Contains(human.String(), tc.label) ||
				!strings.Contains(human.String(), tc.advice) ||
				strings.Count(human.String(), "Next:") != 1 {
				t.Fatalf("publish human guidance: %s", human.String())
			}
			if strings.Contains(human.String(), "distroplane publish --config") {
				t.Fatalf("publication suggested new publication: %s", human.String())
			}
			var status bytes.Buffer
			statusCode := runStatus([]string{"--config", config}, &status, &errOut)
			if statusCode != tc.exit || !strings.Contains(status.String(), tc.label) ||
				!strings.Contains(status.String(), tc.advice) {
				t.Fatalf("status code=%d output=%s stderr=%s", statusCode, status.String(), errOut.String())
			}
		})
	}
}

func TestReleaseGuidanceExplicitStatusNoUnverifiedReconcile(t *testing.T) {
	_, config := publishFixture(t, `{"mode":"waiting_external","reconcileState":"published"}`)
	var publish, errOut bytes.Buffer
	if code := runPublishContext(context.Background(),
		[]string{"--config", config, "--yes", "--json"}, nil, &publish, &errOut); code != exitPending {
		t.Fatalf("fixture publish code=%d stderr=%s", code, errOut.String())
	}
	var result publishOutput
	if err := json.Unmarshal(publish.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(result.JournalPath)
	if err != nil {
		t.Fatal(err)
	}
	var status bytes.Buffer
	if code := runStatus([]string{"--plan", result.Plan.Path, "--journal", result.JournalPath},
		&status, &errOut); code != exitPending {
		t.Fatalf("explicit status code=%d stderr=%s", code, errOut.String())
	}
	if !strings.Contains(status.String(), "waiting for external review") ||
		!strings.Contains(status.String(), "read-only reconciliation") ||
		strings.Contains(status.String(), "resume --plan") ||
		strings.Contains(status.String(), "distroplane reconcile --config") {
		t.Fatalf("unverified configuration used for recovery: %s", status.String())
	}
	after, err := os.ReadFile(result.JournalPath)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatalf("status changed original journal: %v", err)
	}
	status.Reset()
	if code := runStatus([]string{"--plan", result.Plan.Path, "--journal", result.JournalPath, "--json"},
		&status, &errOut); code != exitPending {
		t.Fatalf("JSON status code=%d stderr=%s", code, errOut.String())
	}
	var machine stateOutput
	if err := json.Unmarshal(status.Bytes(), &machine); err != nil ||
		machine.RunID != result.Run.RunID || machine.OutputSchemaVersion != outputSchemaVersion ||
		!machine.Targets[0].ReconcileRequired {
		t.Fatalf("status JSON changed: %+v error=%v", machine, err)
	}
}

func TestReleaseGuidanceCredentialBlockedRecoveryUsesSameRun(t *testing.T) {
	_, config := publishFixture(t, `{"mode":"published","credential":"publish-auth"}`)
	var human, errOut bytes.Buffer
	if got := runPublishContext(context.Background(), []string{"--config", config, "--yes"},
		nil, &human, &errOut); got != exitPending {
		t.Fatalf("blocked publish=%d stderr=%s", got, errOut.String())
	}
	if !strings.Contains(human.String(), "blocked before publication dispatch") ||
		!strings.Contains(human.String(), "CREDENTIAL_UNAVAILABLE") ||
		!strings.Contains(human.String(), "distroplane resume --config") ||
		strings.Contains(human.String(), "--reconcile-only") {
		t.Fatalf("blocked recovery suggestion: %s", human.String())
	}
	var status bytes.Buffer
	if got := runStatus([]string{"--config", config}, &status, &errOut); got != exitPending {
		t.Fatalf("blocked status=%d stderr=%s", got, errOut.String())
	}
	if !strings.Contains(status.String(), "approved credential environment") ||
		!strings.Contains(status.String(), "distroplane resume --config") {
		t.Fatalf("status did not preserve exact run: %s", status.String())
	}
}

func TestReleaseGuidanceDiagnosticCodesAndSafeHints(t *testing.T) {
	tests := []struct {
		kind, code, contains string
	}{
		{"run_already_exists", "RUN_ALREADY_EXISTS", "existing recorded run"},
		{"release_conflict", "RELEASE_CONFLICT", "immutable plan"},
		{"approval_mismatch", "APPROVAL_REQUIRED", "matching plan ID"},
		{"artifact_changed", "MISSING_ARTIFACT", "exact originally planned"},
		{"invalid_plan", "STATE_CORRUPT", "original plan"},
		{"unsafe_retry", "UNSAFE_RETRY", "duplicate-risk"},
		{"execution_failed", "AWAITING_EXTERNAL", "original run"},
		{"cancelled", "INTERRUPTED", "original journal"},
		{"run_selection_failed", "STATE_CORRUPT", "original run catalog"},
	}
	for _, tc := range tests {
		code, hint := diagnosticGuidance(tc.kind, os.ErrNotExist)
		if code != tc.code || !strings.Contains(hint, tc.contains) {
			t.Fatalf("%s: code=%s hint=%s", tc.kind, code, hint)
		}
	}
}

func TestReleaseGuidanceApplyAndReadOnlyReconcile(t *testing.T) {
	dir, config := publishFixture(t, "{\"mode\":\"waiting_external\",\"reconcileState\":\"published\"}")
	var stdout, stderr bytes.Buffer
	if code := runPlanContext(context.Background(), []string{"--config", config, "--json"}, &stdout, &stderr); code != exitOK {
		t.Fatalf("plan code=%d stderr=%s", code, stderr.String())
	}
	var plan planOutput
	if err := json.Unmarshal(stdout.Bytes(), &plan); err != nil {
		t.Fatal(err)
	}
	journalPath := filepath.Join(dir, "original-run.journal")
	stdout.Reset()
	if code := runExecutionContext(context.Background(),
		[]string{"--config", config, "--plan", plan.Path, "--journal", journalPath},
		&stdout, &stderr, false); code != exitPending {
		t.Fatalf("apply code=%d stderr=%s stdout=%s", code, stderr.String(), stdout.String())
	}
	reconciliation := stateTextSelection{configPath: config, planPath: plan.Path,
		journalPath: journalPath, configVerified: true}.command("reconcile")
	if !strings.Contains(stdout.String(), reconciliation) ||
		!strings.Contains(stdout.String(), "waiting for external review") ||
		strings.Contains(stdout.String(), "resume --plan") {
		t.Fatalf("apply did not identify exact read-only command: %s", stdout.String())
	}
	before, err := readJournal(journalPath)
	if err != nil {
		t.Fatal(err)
	}
	dispatchesBefore := 0
	for _, e := range before.Events {
		if e.Type == journal.EventSideEffectDispatched {
			dispatchesBefore++
		}
	}
	stdout.Reset()
	if code := runExecutionContext(context.Background(),
		[]string{"--config", config, "--plan", plan.Path, "--journal", journalPath},
		&stdout, &stderr, true); code != exitOK {
		t.Fatalf("reconcile code=%d stderr=%s stdout=%s", code, stderr.String(), stdout.String())
	}
	if !strings.Contains(stdout.String(), "recorded as published") ||
		!strings.Contains(stdout.String(), "distroplane evidence --plan") ||
		strings.Contains(stdout.String(), "distroplane publish --config") {
		t.Fatalf("reconciled outcome missing: %s", stdout.String())
	}
	after, err := readJournal(journalPath)
	if err != nil {
		t.Fatal(err)
	}
	dispatchesAfter := 0
	for _, e := range after.Events {
		if e.Type == journal.EventSideEffectDispatched {
			dispatchesAfter++
		}
	}
	if dispatchesBefore != 1 || dispatchesAfter != 1 {
		t.Fatalf("reconciliation re-dispatched publication: before=%d after=%d", dispatchesBefore, dispatchesAfter)
	}
}

func TestReleaseGuidanceResumeUsesOriginalRunAndCompletedStatus(t *testing.T) {
	_, config := publishFixture(t, "{\"mode\":\"waiting_external\",\"reconcileState\":\"published\"}")
	var stdout, stderr bytes.Buffer
	if code := runPublishContext(context.Background(), []string{"--config", config, "--yes", "--json"},
		nil, &stdout, &stderr); code != exitPending {
		t.Fatalf("initial publish code=%d stderr=%s", code, stderr.String())
	}
	var first publishOutput
	if err := json.Unmarshal(stdout.Bytes(), &first); err != nil {
		t.Fatal(err)
	}
	stdout.Reset()
	if code := runResumeContext(context.Background(),
		[]string{"--config", config, "--run", first.Run.RunID}, &stdout, &stderr); code != exitOK {
		t.Fatalf("resume code=%d stderr=%s stdout=%s", code, stderr.String(), stdout.String())
	}
	if !strings.Contains(stdout.String(), "run "+first.Run.RunID) ||
		!strings.Contains(stdout.String(), "recorded as published") ||
		!strings.Contains(stdout.String(), "distroplane evidence --plan") ||
		strings.Contains(stdout.String(), "distroplane publish") {
		t.Fatalf("resume output changed identity or encouraged duplicate: %s", stdout.String())
	}
	before, err := os.ReadFile(first.JournalPath)
	if err != nil {
		t.Fatal(err)
	}
	stdout.Reset()
	if code := runResumeContext(context.Background(),
		[]string{"--config", config, "--run", first.Run.RunID}, &stdout, &stderr); code != exitOK {
		t.Fatalf("completed resume code=%d stderr=%s", code, stderr.String())
	}
	after, err := os.ReadFile(first.JournalPath)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatalf("completed resume changed durable journal: %v", err)
	}
	if !strings.Contains(stdout.String(), "recorded as published") {
		t.Fatalf("completed resume did not report actual result: %s", stdout.String())
	}
}

func TestReleaseGuidanceTargetFallbackAndUnknownStates(t *testing.T) {
	for _, tc := range []struct {
		state, expected string
	}{
		{"WAITING_EXTERNAL", "external review"},
		{"BLOCKED", "unresolved prerequisite"},
		{"REJECTED", "finished with rejection"},
		{"FAILED", "failed or cancelled"},
		{"CANCELLED", "failed or cancelled"},
		{"PUBLISHED", "finished without all targets published"},
		{"READY", "incomplete"},
	} {
		out := stateOutput{Completed: true, Targets: []targetStateOutput{{ID: "target", State: tc.state}}}
		a := assessRelease(out)
		if !strings.Contains(a.label(out), tc.expected) {
			t.Fatalf("%s target-only state incorrectly classified as %s", tc.state, a.label(out))
		}
	}
	out := guidanceState("PUBLISHED", true, true, false, "")
	if assessRelease(out).published {
		t.Fatal("unresolved published target incorrectly reported as complete")
	}
	out = guidanceState("WAITING_EXTERNAL", false, true, false, "")
	out.Operations[0].Ambiguous = true
	if !assessRelease(out).ambiguous {
		t.Fatal("operation ambiguity hidden when target aggregation is stale")
	}
}

func TestReleaseGuidanceOperationCodesAreAllowlisted(t *testing.T) {
	out := guidanceState("BLOCKED", false, false, false, "CREDENTIAL_RESOLUTION_FAILED")
	var b bytes.Buffer
	writeStateText(&b, out)
	if !strings.Contains(b.String(), "CREDENTIAL_RESOLUTION_FAILED") {
		t.Fatalf("safe error code omitted: %s", b.String())
	}
	out.Operations[0].ErrorCode = "DEPENDENCY_TERMINAL"
	b.Reset()
	writeStateText(&b, out)
	if !strings.Contains(b.String(), "DEPENDENCY_TERMINAL") {
		t.Fatalf("dependency reason omitted: %s", b.String())
	}
	out.Operations[0].ErrorCode = "attacker controlled error with token"
	b.Reset()
	writeStateText(&b, out)
	if strings.Contains(b.String(), "attacker controlled") {
		t.Fatalf("raw provider error displayed: %s", b.String())
	}
}
