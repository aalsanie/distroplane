package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/aalsanie/distroplane/internal/journal"
)

func TestStableCommandOutputAndExitContract(t *testing.T) {
	if exitOK != 0 || exitOperational != 1 || exitUsage != 2 || exitInvalid != 3 ||
		exitPending != 4 || exitRejected != 5 || exitFailed != 6 {
		t.Fatal("public CLI exit-code contract changed")
	}
	var stdout, stderr bytes.Buffer
	if code := execute([]string{"version", "--json"}, &stdout, &stderr); code != exitOK {
		t.Fatalf("version code=%d stderr=%s", code, stderr.String())
	}
	var info map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &info); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"outputSchemaVersion", "version", "commit", "buildDate"} {
		if _, ok := info[key].(string); !ok {
			t.Fatalf("missing stable version field %s", key)
		}
	}
	if info["outputSchemaVersion"] != "1" {
		t.Fatalf("unexpected output version: %+v", info)
	}
	stdout.Reset()
	if code := execute([]string{"help"}, &stdout, &stderr); code != exitOK {
		t.Fatalf("help code=%d", code)
	}
	for _, command := range []string{"validate", "plan", "apply", "status", "reconcile", "retry", "evidence"} {
		if !strings.Contains(stdout.String(), "  "+command+" ") {
			t.Fatalf("help no longer documents %q", command)
		}
	}
	stdout.Reset()
	stderr.Reset()
	if code := execute([]string{"status", "--json"}, &stdout, &stderr); code != exitUsage {
		t.Fatalf("missing explicit paths changed code: %d", code)
	}
	var diagnostic errorOutput
	if err := json.Unmarshal(stderr.Bytes(), &diagnostic); err != nil ||
		diagnostic.OutputSchemaVersion != "1" || diagnostic.Error.Kind != "usage" {
		t.Fatalf("unexpected JSON error: %+v (%v)", diagnostic, err)
	}
}

func TestStableStatusJSONGoldenFixture(t *testing.T) {
	want, err := os.ReadFile(filepath.Join("testdata", "compatibility", "status-output.json"))
	if err != nil {
		t.Fatal(err)
	}
	value := stateOutput{
		OutputSchemaVersion: "1", PlanID: "plan-saved", RunID: "run-saved",
		Targets: []targetStateOutput{{ID: "primary", State: "WAITING_EXTERNAL", ReconcileRequired: true}},
		Operations: []operationStateOutput{{
			ID: "primary/publish", TargetID: "primary", State: "WAITING_EXTERNAL",
			Attempt: 1, ReconcileRequired: true, ProviderState: "review",
		}},
	}
	var out bytes.Buffer
	if code := writeJSON(&out, value); code != exitOK {
		t.Fatalf("status serialization failed: %d", code)
	}
	var actualValue, expectedValue any
	if err := json.Unmarshal(want, &expectedValue); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(out.Bytes(), &actualValue); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(expectedValue, actualValue) {
		t.Fatalf("status JSON changed:\nexpected: %s\nactual: %s", want, out.Bytes())
	}
}

// Exercise the same public command entrypoints used by existing automation.
// All publication here is confined to a child fake provider; no remote requests occur.
func TestStableLowLevelReleaseAndRecoveryCompatibility(t *testing.T) {
	provider := buildFakeProvider(t)
	cases := []struct {
		name         string
		mode         string
		reconcile    string
		credential   bool
		applyCode    int
		initialState string
		resumeCode   int
		finalState   string
	}{
		{"published", "published", "", false, exitOK, "PUBLISHED", exitOK, "PUBLISHED"},
		{"review", "waiting_external", "published", false, exitPending, "WAITING_EXTERNAL", exitOK, "PUBLISHED"},
		{"blocked", "published", "", true, exitPending, "BLOCKED", exitPending, "BLOCKED"},
		{"rejected", "rejected", "", false, exitRejected, "REJECTED", exitRejected, "REJECTED"},
		{"permanent-failure", "permanent_error", "", false, exitFailed, "FAILED", exitFailed, "FAILED"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			artifact := filepath.Join(dir, "app.bin")
			if err := os.WriteFile(artifact, []byte("release payload"), 0o600); err != nil {
				t.Fatal(err)
			}
			cfg := map[string]any{"mode": tc.mode}
			if tc.reconcile != "" {
				cfg["reconcileState"] = tc.reconcile
			}
			if tc.credential {
				cfg["credential"] = "fixture-secret"
			}
			configBytes, err := json.Marshal(map[string]any{
				"schemaVersion": "1",
				"release": map[string]any{
					"id":        "compat-release",
					"artifacts": []map[string]any{{"name": "app", "source": "app.bin"}},
				},
				"targets": []map[string]any{{
					"id":            "primary",
					"provider":      map[string]any{"name": "fake", "executable": provider},
					"configuration": cfg,
				}},
			})
			if err != nil {
				t.Fatal(err)
			}
			configPath := filepath.Join(dir, "distroplane.json")
			if err := os.WriteFile(configPath, configBytes, 0o600); err != nil {
				t.Fatal(err)
			}
			call := func(wantCode int, args ...string) []byte {
				t.Helper()
				var stdout, stderr bytes.Buffer
				code := execute(args, &stdout, &stderr)
				if code != wantCode {
					t.Fatalf("%v returned %d instead of %d: stdout=%s stderr=%s", args, code, wantCode, stdout.String(), stderr.String())
				}
				return append([]byte(nil), stdout.Bytes()...)
			}
			var validation validateOutput
			if err := json.Unmarshal(call(exitOK, "validate", "--config", configPath, "--json"), &validation); err != nil ||
				!validation.Valid || validation.SchemaVersion != "1" || validation.Targets != 1 {
				t.Fatalf("configuration output %+v err=%v", validation, err)
			}
			var planned planOutput
			if err := json.Unmarshal(call(exitOK, "plan", "--config", configPath, "--json"), &planned); err != nil ||
				planned.PlanID == "" || planned.Path == "" || planned.Operations != 1 || planned.Targets != 1 {
				t.Fatalf("plan output %+v err=%v", planned, err)
			}
			journalPath := filepath.Join(dir, "run.journal")
			apply := []string{"apply", "--config", configPath, "--plan", planned.Path, "--journal", journalPath, "--run", "run-compat", "--json"}
			var initial stateOutput
			if err := json.Unmarshal(call(tc.applyCode, apply...), &initial); err != nil ||
				initial.PlanID != planned.PlanID || initial.RunID != "run-compat" || len(initial.Targets) != 1 ||
				initial.Targets[0].State != tc.initialState {
				t.Fatalf("apply output %+v err=%v", initial, err)
			}
			var status stateOutput
			if err := json.Unmarshal(call(tc.applyCode, "status", "--plan", planned.Path, "--journal", journalPath, "--json"), &status); err != nil ||
				status.PlanID != initial.PlanID || status.RunID != initial.RunID ||
				len(status.Operations) != 1 || status.Targets[0].State != tc.initialState {
				t.Fatalf("status output %+v err=%v", status, err)
			}
			before, err := os.ReadFile(journalPath)
			if err != nil {
				t.Fatal(err)
			}
			var bundle struct {
				SchemaVersion string `json:"schemaVersion"`
				PlanID        string `json:"planId"`
				Journal       struct {
					SchemaVersion uint16 `json:"schemaVersion"`
				} `json:"journal"`
				Targets []struct {
					State string `json:"state"`
				} `json:"targets"`
			}
			if err := json.Unmarshal(call(exitOK, "evidence", "--plan", planned.Path, "--journal", journalPath), &bundle); err != nil ||
				bundle.SchemaVersion != "1" || bundle.PlanID != planned.PlanID || bundle.Journal.SchemaVersion != 2 ||
				len(bundle.Targets) != 1 || bundle.Targets[0].State != tc.initialState {
				t.Fatalf("evidence output %+v err=%v", bundle, err)
			}
			after, err := os.ReadFile(journalPath)
			if err != nil || !bytes.Equal(before, after) {
				t.Fatalf("status/evidence unexpectedly mutated journal: %v", err)
			}
			// Only an explicit reconcile follows; it must never dispatch publication.
			reconcile := []string{"reconcile", "--config", configPath, "--plan", planned.Path, "--journal", journalPath, "--json"}
			var resumed stateOutput
			if err := json.Unmarshal(call(tc.resumeCode, reconcile...), &resumed); err != nil ||
				resumed.PlanID != planned.PlanID || resumed.RunID != "run-compat" ||
				len(resumed.Targets) != 1 || resumed.Targets[0].State != tc.finalState {
				t.Fatalf("reconcile output %+v err=%v", resumed, err)
			}
			file, err := os.Open(journalPath)
			if err != nil {
				t.Fatal(err)
			}
			events, readErr := journal.Read(file)
			closeErr := file.Close()
			if readErr != nil || closeErr != nil {
				t.Fatalf("journal decode: %v, close: %v", readErr, closeErr)
			}
			dispatches := 0
			for _, event := range events.Events {
				if event.Type == journal.EventSideEffectDispatched {
					dispatches++
				}
			}
			expectedDispatches := 1
			if tc.credential {
				expectedDispatches = 0
			}
			if dispatches != expectedDispatches {
				t.Fatalf("unsafe duplicate publication: dispatched %d times, expected %d", dispatches, expectedDispatches)
			}
			if tc.name == "review" && !resumed.Completed {
				t.Fatal("reconciled external review did not complete")
			}
			if tc.name == "published" {
				// A completed run is replayed without any new journal records.
				recordCount := len(events.Events)
				call(exitOK, apply...)
				next, err := os.ReadFile(journalPath)
				if err != nil {
					t.Fatal(err)
				}
				again, err := journal.Read(bytes.NewReader(next))
				if err != nil || len(again.Events) != recordCount {
					t.Fatalf("completed release was reapplied: %v", err)
				}
			}
		})
	}
}
