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
			return "MISSING_ARTIFACT", "Restore the exact originally planned artifact bytes; do not regenerate a different artifact or plan"
		}
		return "STATE_CORRUPT", "Preserve and restore the exact original plan and provider snapshots before recovery; never recreate the run"
	case "provider_binding_failed", "plan_failed":
		if err != nil && strings.Contains(strings.ToLower(err.Error()), "executable") {
			return "PROVIDER_NOT_FOUND", "Restore the expected provider executable and verify it against the original plan digest"
		}
		return "VERSION_MISMATCH", "Inspect provider identity and compatibility with distroplane doctor; preserve the exact pinned provider for saved runs"
	case "run_already_exists":
		return "RUN_ALREADY_EXISTS", "Inspect the existing recorded run with distroplane status; do not publish this release again"
	case "release_conflict":
		return "RELEASE_CONFLICT", "Inspect the existing immutable plan and run; a different plan cannot replace the same release"
	case "run_selection_failed":
		if err != nil && strings.Contains(strings.ToLower(err.Error()), "ambiguous") {
			return "RUN_AMBIGUOUS", "List existing releases with distroplane runs, then select the exact recorded --run or --release; never choose arbitrarily"
		}
		return "STATE_CORRUPT", "Inspect the original run catalog, plan, and journal; never create a replacement run"
	case "invalid_run", "journal_read_failed", "journal_reduce_failed", "run_catalog_conflict", "journal_open_failed":
		return "STATE_CORRUPT", "Preserve and inspect the original plan, journal, and run binding; do not replan or republish"
	case "approval_required", "approval_denied", "approval_mismatch":
		return "APPROVAL_REQUIRED", "Review the current immutable plan; publication requires an explicit matching plan ID or separate approval"
	case "execution_failed":
		return "AWAITING_EXTERNAL", "Inspect the original run and journal first; reconcile unknown external effects before considering a retry"
	case "retry_not_authorized", "unsafe_retry":
		return "UNSAFE_RETRY", "Reconcile the original operation and verify absence; only an explicit one-shot retry with duplicate-risk acknowledgment may proceed"
	case "cancelled":
		return "INTERRUPTED", "Inspect the original journal and reconcile any ambiguous dispatch before resuming; never start a replacement run"
	case "missing_metadata", "invalid_template", "invalid_artifact":
		return "INVALID_CONFIG", "Correct the missing or inconsistent release metadata"
	case "usage":
		return "INVALID_ARGUMENT", "Use distroplane help for supported command syntax"
	default:
		return strings.ToUpper(kind), ""
	}
}
