package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
)

func TestDiagnosticGuidanceStableCodes(t *testing.T) {
	cases := []struct {
		kind string
		err error
		want string
	}{
		{"invalid_config", os.ErrNotExist, "MISSING_CONFIG"},
		{"invalid_config", errors.New("bad configuration"), "INVALID_CONFIG"},
		{"invalid_plan", errors.New("bad plan"), "STATE_CORRUPT"},
		{"artifact_changed", errors.New("artifact bytes changed"), "MISSING_ARTIFACT"},
		{"provider_binding_failed", errors.New("resolve executable failed"), "PROVIDER_NOT_FOUND"},
		{"provider_binding_failed", errors.New("wrong provider identity"), "VERSION_MISMATCH"},
		{"run_selection_failed", errors.New("ambiguous selection"), "RUN_AMBIGUOUS"},
		{"run_selection_failed", errors.New("missing journal"), "STATE_CORRUPT"},
		{"invalid_run", errors.New("bad history"), "STATE_CORRUPT"},
		{"journal_read_failed", errors.New("corrupt"), "STATE_CORRUPT"},
		{"run_catalog_conflict", errors.New("conflict"), "STATE_CORRUPT"},
		{"approval_required", errors.New("not approved"), "APPROVAL_REQUIRED"},
		{"execution_failed", errors.New("unknown result"), "AWAITING_EXTERNAL"},
		{"unsafe_retry", errors.New("dangerous"), "UNSAFE_RETRY"},
		{"cancelled", context.Canceled, "INTERRUPTED"},
		{"missing_metadata", errors.New("missing"), "INVALID_CONFIG"},
		{"usage", errors.New("bad flag"), "INVALID_ARGUMENT"},
	}
	for _, tc := range cases {
		t.Run(tc.kind+"-"+tc.want, func(t *testing.T) {
			code, hint := diagnosticGuidance(tc.kind, tc.err)
			if code != tc.want || hint == "" {
				t.Fatalf("got code=%s hint=%q want=%s", code, hint, tc.want)
			}
		})
	}
	if code, hint := diagnosticGuidance("unknown_state", errors.New("anything")); code != "UNKNOWN_STATE" || hint != "" {
		t.Fatalf("unknown diagnostic was fabricated: %s %q", code, hint)
	}
}

func TestJSONDiagnosticIncludesCodeAndHintWithoutChangingLegacyKind(t *testing.T) {
	var stderr bytes.Buffer
	if status := writeCommandError(&stderr, true, exitInvalid, "invalid_config", os.ErrNotExist); status != exitInvalid {
		t.Fatal("invalid exit semantics")
	}
	var value errorOutput
	if err := json.Unmarshal(stderr.Bytes(), &value); err != nil {
		t.Fatal(err)
	}
	if value.OutputSchemaVersion != outputSchemaVersion || value.Error.Kind != "invalid_config" ||
		value.Error.Code != "MISSING_CONFIG" || value.Error.Hint == "" {
		t.Fatalf("diagnostic schema mismatch: %+v", value)
	}
	var human bytes.Buffer
	if status := writeCommandError(&human, false, exitPending, "execution_failed", errors.New("requires reconciliation")); status != exitPending {
		t.Fatal("pending exit meaning changed")
	}
	if !strings.Contains(human.String(), "Next:") || !strings.Contains(human.String(), "execution_failed") {
		t.Fatalf("no safe next action: %s", human.String())
	}
}
