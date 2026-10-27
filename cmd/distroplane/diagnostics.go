package main

import (
	"errors"
	"os"
	"strings"
)

// Stable diagnostic codes and safe next steps are additive to the existing
// error.kind contract. Exit statuses remain 0, 1, 2, 3, 4, 5, or 6.
func diagnosticGuidance(kind string, err error) (string, string) {
	switch kind {
	case "invalid_config":
		if errors.Is(err, os.ErrNotExist) {
			return "MISSING_CONFIG", "Run distroplane init or specify --config PATH"
		}
		return "INVALID_CONFIG", "Correct the configuration and run distroplane validate"
	case "invalid_plan", "artifact_changed":
		if kind == "artifact_changed" {
			return "MISSING_ARTIFACT", "Restore the exact planned artifact bytes before attempting a resume"
		}
		return "STATE_CORRUPT", "Restore the original immutable plan and associated provider binaries"
	case "provider_binding_failed", "plan_failed":
		if err != nil && strings.Contains(strings.ToLower(err.Error()), "executable") {
			return "PROVIDER_NOT_FOUND", "Install the required provider executable"
		}
		return "VERSION_MISMATCH", "Inspect provider installation and configuration with distroplane doctor"
	case "run_selection_failed":
		if err != nil && strings.Contains(strings.ToLower(err.Error()), "ambiguous") {
			return "RUN_AMBIGUOUS", "Select the exact run using --release or --run"
		}
		return "STATE_CORRUPT", "Inspect the saved plan, run catalog, and journal"
	case "invalid_run", "journal_read_failed", "journal_reduce_failed", "run_catalog_conflict", "journal_open_failed":
		return "STATE_CORRUPT", "Keep the original journal and investigate the recorded run"
	case "approval_required", "approval_denied", "approval_mismatch":
		return "APPROVAL_REQUIRED", "Review the immutable plan and explicitly approve its exact plan ID"
	case "execution_failed":
		return "AWAITING_EXTERNAL", "Inspect status and reconcile ambiguous operations before any retry"
	case "retry_not_authorized", "unsafe_retry":
		return "UNSAFE_RETRY", "Reconcile the operation and use explicit one-shot retry authorization"
	case "cancelled":
		return "INTERRUPTED", "Inspect the existing run; never start a fresh publication for an uncertain operation"
	case "missing_metadata", "invalid_template", "invalid_artifact":
		return "INVALID_CONFIG", "Correct the missing or inconsistent release metadata"
	case "usage":
		return "INVALID_ARGUMENT", "Use distroplane help for supported command syntax"
	default:
		return strings.ToUpper(kind), ""
	}
}
