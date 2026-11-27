package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestReleaseGuidanceSharedRecordedStates(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "tests", "consumer", "release-guidance-cases.json"))
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		SchemaVersion string `json:"schemaVersion"`
		Cases         []struct {
			Name     string          `json:"name"`
			Kind     string          `json:"kind"`
			Exit     int             `json:"exit"`
			State    json.RawMessage `json:"state"`
			Expected struct {
				Primary   string `json:"primary"`
				MayExist  bool   `json:"mayExist"`
				Evidence  string `json:"evidence"`
				Selection string `json:"selection"`
			} `json:"expected"`
		} `json:"cases"`
	}
	if err := json.Unmarshal(data, &fixture); err != nil || fixture.SchemaVersion != "1" || len(fixture.Cases) != 19 {
		t.Fatalf("invalid release guidance cases: %v", err)
	}
	labels := map[string]string{
		"published":  "recorded as published",
		"external":   "waiting for external review",
		"ambiguous":  "uncertain external outcome",
		"credential": "unavailable credential",
		"blocked":    "unresolved prerequisite",
		"rejected":   "finished with rejection",
		"failed":     "failed or cancelled",
		"mixed":      "mixed: terminal outcomes",
		"truncated":  "durable-prefix state only",
		"noop":       "completed no-op",
		"legacy":     "unsupported retry authorization",
		"incomplete": "incomplete",
	}
	for _, tc := range fixture.Cases {
		t.Run(tc.Name, func(t *testing.T) {
			var state stateOutput
			if err := json.Unmarshal(tc.State, &state); err != nil {
				t.Fatal(err)
			}
			if state.OutputSchemaVersion != outputSchemaVersion || state.PlanID == "" || state.RunID == "" || len(state.Targets) == 0 {
				t.Fatalf("fixture identity or shape changed: %+v", state)
			}
			assessment := assessRelease(state)
			if label := labels[tc.Kind]; label == "" || !strings.Contains(assessment.label(state), label) {
				t.Fatalf("expected %q classification, got %q", tc.Kind, assessment.label(state))
			}
			selection := stateTextSelection{
				configPath: filepath.Join("release with spaces", "it's; $HOME.json"),
				planPath:   "original plan.json", journalPath: "original journal", runID: state.RunID,
				catalog: true, configVerified: true,
			}
			var human bytes.Buffer
			writeStateTextWithSelection(&human, state, selection)
			message := human.String()
			if strings.Count(message, "Next:") != 1 || !strings.Contains(message, "run "+state.RunID) ||
				!strings.Contains(message, "plan "+state.PlanID) ||
				strings.Contains(message, "distroplane publish") || strings.Contains(message, "resume --plan") ||
				strings.Contains(message, "--yes") {
				t.Fatalf("unsafe or incomplete guidance: %s", message)
			}
			if tc.Expected.Selection != "exact run" || assessment.mayExist != tc.Expected.MayExist ||
				!strings.Contains(message, fmt.Sprintf("publication may already exist (recorded run): %t", tc.Expected.MayExist)) ||
				!strings.Contains(message, tc.Expected.Evidence) {
				t.Fatalf("recorded evidence or uncertainty mismatch: %s", message)
			}
			if assessment.published != (tc.Kind == "published") {
				t.Fatalf("unsafe publication claim for %s", tc.Name)
			}
			runArg, ok := shellArgument(state.RunID, runtime.GOOS)
			if !ok {
				t.Fatal("safe original run ID could not be quoted")
			}
			next := assessment.next(state, selection)
			switch tc.Expected.Primary {
			case "reconcile":
				if !strings.Contains(next, "--reconcile-only") || !strings.Contains(next, "--run "+runArg) {
					t.Fatalf("missing exact original-run reconciliation: %s", next)
				}
			case "resume":
				if !strings.Contains(next, "distroplane resume --config") || !strings.Contains(next, "--run "+runArg) ||
					strings.Contains(next, "--reconcile-only") {
					t.Fatalf("blocked run requires exact original-state resume: %s", next)
				}
			case "status":
				if !strings.Contains(next, "distroplane status --config") || !strings.Contains(next, "--run "+runArg) {
					t.Fatalf("missing exact status: %s", next)
				}
			case "manual":
				if !strings.Contains(next, "human decision") && !strings.Contains(next, "manual recovery") {
					t.Fatalf("missing human decision: %s", next)
				}
				if strings.Contains(next, "distroplane resume") {
					t.Fatal("manual recovery recommended a resume command")
				}
			case "preserve":
				if !strings.Contains(next, "durable prefix") || strings.Contains(next, "distroplane resume") {
					t.Fatalf("unsafe truncated history guidance: %s", next)
				}
			case "evidence", "no-op":
				if !strings.Contains(next, "distroplane evidence --plan") {
					t.Fatalf("missing evidence selection: %s", next)
				}
			default:
				t.Fatalf("unknown primary action %q", tc.Expected.Primary)
			}
		})
	}
}

func TestReleaseGuidanceNativeNoOp(t *testing.T) {
	_, config := publishFixture(t, `{"mode":"noop"}`)
	var out, diagnostic bytes.Buffer
	if code := runPublishContext(t.Context(), []string{"--config", config, "--yes", "--json"}, nil, &out, &diagnostic); code != exitOK {
		t.Fatalf("no-op publication exit=%d: %s", code, diagnostic.String())
	}
	var result publishOutput
	if err := json.Unmarshal(out.Bytes(), &result); err != nil || result.Run == nil ||
		!result.Run.Completed || len(result.Run.Operations) != 0 || !assessRelease(*result.Run).noop {
		t.Fatalf("no-op output=%s err=%v", out.String(), err)
	}
	out.Reset()
	if code := runStatus([]string{"--config", config, "--run", result.Run.RunID}, &out, &diagnostic); code != exitOK ||
		!strings.Contains(out.String(), "completed no-op") || strings.Contains(out.String(), "recorded as published") {
		t.Fatalf("no-op status exit=%d output=%s", code, out.String())
	}
}
