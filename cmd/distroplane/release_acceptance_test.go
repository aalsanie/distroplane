package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// These recorded states are also consumed by the Maven, Gradle and Action tests.
// This test exercises only human presentation; machine contracts remain unchanged.
func TestReleaseGuidanceSharedRecordedStates(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "tests", "consumer", "release-guidance-cases.json"))
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		SchemaVersion string `json:"schemaVersion"`
		Cases         []struct {
			Name  string          `json:"name"`
			Kind  string          `json:"kind"`
			Exit  int             `json:"exit"`
			State json.RawMessage `json:"state"`
		} `json:"cases"`
	}
	if err := json.Unmarshal(data, &fixture); err != nil || fixture.SchemaVersion != "1" || len(fixture.Cases) != 12 {
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
	}
	for _, tc := range fixture.Cases {
		t.Run(tc.Name, func(t *testing.T) {
			var state stateOutput
			if err := json.Unmarshal(tc.State, &state); err != nil {
				t.Fatal(err)
			}
			if state.OutputSchemaVersion != outputSchemaVersion || state.PlanID != "plan-acceptance" ||
				state.RunID != "run-acceptance" || len(state.Targets) == 0 || len(state.Operations) == 0 {
				t.Fatalf("fixture identity or shape changed: %+v", state)
			}
			if got := assessRelease(state).label(state); !strings.Contains(got, labels[tc.Kind]) {
				t.Fatalf("expected %q classification, got %q", tc.Kind, got)
			}
			selection := stateTextSelection{
				configPath: filepath.Join("release with spaces", "distroplane.json"),
				runID:      state.RunID, catalog: true, configVerified: true,
			}
			var human bytes.Buffer
			writeStateTextWithSelection(&human, state, selection)
			message := human.String()
			if strings.Count(message, "Next:") != 1 || !strings.Contains(message, "run run-acceptance") ||
				!strings.Contains(message, "plan plan-acceptance") ||
				strings.Contains(message, "distroplane publish") || strings.Contains(message, "resume --plan") ||
				strings.Contains(message, "--yes") {
				t.Fatalf("unsafe or incomplete guidance: %s", message)
			}
			if tc.Kind != "published" && strings.Contains(message, "outcome recorded as published") {
				t.Fatalf("non-publication treated as published: %s", message)
			}
			runArg, safeRun := shellArgument(state.RunID, runtime.GOOS)
			if !safeRun {
				t.Fatal("safe original run ID could not be quoted")
			}
			if tc.Kind == "ambiguous" || tc.Kind == "external" || tc.Kind == "mixed" {
				if !strings.Contains(message, "--reconcile-only") || !strings.Contains(message, "--run "+runArg) {
					t.Fatalf("missing exact original-run reconciliation: %s", message)
				}
			}
			if tc.Kind == "credential" || tc.Kind == "blocked" {
				if !strings.Contains(message, "distroplane resume --config") ||
					!strings.Contains(message, "--run "+runArg) {
					t.Fatalf("blocked run requires exact original-state resume: %s", message)
				}
			}
			if tc.Kind == "mixed" {
				if !strings.Contains(message, "target npm") || !strings.Contains(message, "target winget") {
					t.Fatalf("mixed target outcome hidden: %s", message)
				}
			}
			if (tc.Kind == "truncated" || tc.Kind != "published") && assessRelease(state).published {
				t.Fatalf("unsafe publication claim for %s", tc.Name)
			}
		})
	}
}
