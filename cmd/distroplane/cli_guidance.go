package main

import (
	"fmt"
	"io"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/aalsanie/distroplane/internal/config"
	"github.com/aalsanie/distroplane/internal/runstate"
)

// A displayed command is never evaluated by Distroplane. Quote for the host's
// default interactive shell (PowerShell on Windows, POSIX sh elsewhere).
// Unsafe terminal characters must not turn an advisory into forged output.
func shellArgument(value, hostOS string) (string, bool) {
	if value == "" || !utf8.ValidString(value) {
		return "", false
	}
	for _, r := range value {
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) || r == '\u2028' || r == '\u2029' {
			return "", false
		}
	}
	if hostOS == "windows" {
		return "'" + strings.ReplaceAll(value, "'", "''") + "'", true
	}
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'", true
}

func safeDisplay(value string) string {
	if _, ok := shellArgument(value, runtime.GOOS); !ok {
		return strconv.QuoteToASCII(value)
	}
	return value
}

func cliConfigCommand(verb, path string) (string, bool) {
	quoted, ok := shellArgument(path, runtime.GOOS)
	if !ok {
		return "", false
	}
	return "distroplane " + verb + " --config " + quoted, true
}

func writeInitFollowUp(w io.Writer, path string) {
	if command, ok := cliConfigCommand("doctor", path); ok {
		fmt.Fprintf(w, "Next: review generated metadata, then run %s\n", command)
		return
	}
	fmt.Fprintln(w, "Next: review generated metadata, then run distroplane doctor with the exact generated configuration path (not safely printable as a shell command)")
}

func doctorHasCode(report doctorOutput, code string) bool {
	for _, check := range report.Checks {
		if check.Code == code {
			return true
		}
	}
	return false
}

func doctorCounts(report doctorOutput) (fail, warn, pass, skip int) {
	for _, check := range report.Checks {
		switch check.Severity {
		case doctorFail:
			fail++
		case doctorWarn:
			warn++
		case doctorPass:
			pass++
		case doctorSkip:
			skip++
		}
	}
	return
}

func doctorRemoteScope(report doctorOutput, requested bool) string {
	if !requested {
		return "remote connectivity not requested"
	}
	if doctorHasCode(report, "REMOTE_INVALID") || doctorHasCode(report, "REMOTE_UNREACHABLE") {
		return "remote TCP probe failed; publication acceptance not verified"
	}
	if doctorHasCode(report, "REMOTE_REACHABLE") {
		return "remote TCP connectivity checked; authorization and publication not verified"
	}
	return "remote connectivity not verified (no reachable destination checked)"
}

func doctorPriority(check doctorFinding) int {
	switch check.Severity {
	case doctorFail:
		switch check.Code {
		case "STATE_CORRUPT":
			return 0
		case "MISSING_CONFIG", "INVALID_CONFIG":
			return 2
		case "MISSING_ARTIFACT", "ARTIFACT_HASH_FAILED", "ARTIFACT_CHANGED":
			return 3
		case "PROVIDER_NOT_FOUND", "VERSION_MISMATCH", "ARCH_MISMATCH", "PROVIDER_DIGEST_CHANGED":
			return 4
		case "MISSING_CREDENTIAL", "CREDENTIAL_COLLISION", "INVALID_CREDENTIAL_MAPPING":
			return 5
		default:
			return 6
		}
	case doctorWarn:
		switch check.Code {
		case "RUN_RESERVED":
			return 10
		case "RECONCILE_REQUIRED":
			return 11
		case "PLAN_AMBIGUOUS":
			return 12
		default:
			return 20
		}
	default:
		return 30
	}
}

// Catalog selection is advisory and read-only. No publication recommendation
// can be made if the existing release identity or durable catalog is suspect.
func doctorRecordedReleaseNext(configPath string) (string, bool) {
	loaded, err := config.Load(configPath)
	if err != nil {
		return "", false
	}
	root := filepath.Join(loaded.BaseDir, ".distroplane")
	records, err := (runstate.Store{Root: root}).Inventory()
	if err != nil {
		return "Preserve the original .distroplane state and investigate the run catalog; do not start a new publication", true
	}
	var selected []runstate.Record
	for _, record := range records {
		if record.ReleaseID == loaded.Config.Release.ID {
			selected = append(selected, record)
		}
	}
	if len(selected) > 1 {
		if command, ok := cliConfigCommand("runs", configPath); ok {
			return "Select the exact recorded release using " + command + "; do not start a new publication", true
		}
		return "Select the exact recorded release; its configuration path cannot be safely shown as a shell command", true
	}
	if len(selected) == 1 {
		record := selected[0]
		if record.State == "RESERVED" {
			return "The run has a durable reservation but no journal; preserve the original state for manual recovery", true
		}
		if record.RunID != "" && record.JournalPath != "" {
			if command, ok := cliConfigCommand("status", configPath); ok {
				if run, valid := shellArgument(record.RunID, runtime.GOOS); valid {
					return "Inspect the recorded release using " + command + " --run " + run + "; do not republish", true
				}
			}
			return "Inspect the original recorded run; its selector cannot be safely shown as a shell command", true
		}
		if command, ok := cliConfigCommand("runs", configPath); ok {
			return "An immutable plan already exists; inspect it using " + command + " before explicitly reviewing publication", true
		}
		return "Inspect the existing immutable plan before considering publication", true
	}
	return "", false
}

func doctorPrimaryNext(report doctorOutput, configPath string) string {
	if doctorHasCode(report, "STATE_CORRUPT") {
		return "Preserve and restore the exact original plan, artifacts, provider snapshots, and journal; do not create a replacement run"
	}
	if doctorHasCode(report, "RUN_RESERVED") {
		return "Preserve the existing run reservation and investigate the missing journal; never start a replacement publication"
	}
	if next, exists := doctorRecordedReleaseNext(configPath); exists {
		return next
	}
	if doctorHasCode(report, "PLAN_AMBIGUOUS") {
		if command, ok := cliConfigCommand("runs", configPath); ok {
			return "Select the existing release using " + command + " before any publication"
		}
		return "Select the original saved release before any publication"
	}
	fail, _, _, _ := doctorCounts(report)
	if fail > 0 {
		checks := append([]doctorFinding(nil), report.Checks...)
		sort.SliceStable(checks, func(i, j int) bool {
			return doctorPriority(checks[i]) < doctorPriority(checks[j])
		})
		for _, check := range checks {
			if check.Severity != doctorFail {
				continue
			}
			switch check.Code {
			case "MISSING_CONFIG":
				return "Create the configuration with distroplane init (supply destination and release); then rerun doctor"
			case "INVALID_CONFIG":
				if command, ok := cliConfigCommand("validate", configPath); ok {
					return "Correct the configuration, then run " + command
				}
				return "Correct the configuration before repeating validation"
			}
			action := check.Fix
			if action == "" {
				action = "Correct the reported prerequisite"
			}
			if command, ok := cliConfigCommand("doctor", configPath); ok {
				return action + "; then rerun " + command
			}
			return action + "; then repeat doctor using the original configuration"
		}
	}
	if command, ok := cliConfigCommand("publish", configPath); ok {
		return "Review the immutable plan and explicitly approve publication with " + command + " (no publication occurs before approval)"
	}
	return "Review the immutable plan and explicitly approve publication with distroplane publish using the original configuration path"
}

func writeDoctorHuman(w io.Writer, configPath string, remote bool, report doctorOutput) {
	fail, warn, pass, skip := doctorCounts(report)
	scope := doctorRemoteScope(report, remote)
	kind := "LOCALLY READY FOR REVIEW"
	switch {
	case fail > 0 && (doctorHasCode(report, "REMOTE_INVALID") || doctorHasCode(report, "REMOTE_UNREACHABLE")):
		kind = "NOT READY; remote probe failed"
	case fail > 0:
		kind = "NOT READY"
	case doctorHasCode(report, "RUN_RESERVED") || doctorHasCode(report, "RECONCILE_REQUIRED") || doctorHasCode(report, "PLAN_AMBIGUOUS"):
		kind = "EXISTING RELEASE REQUIRES INSPECTION"
	case remote && doctorHasCode(report, "REMOTE_REACHABLE"):
		kind = "CHECKS PASSED; NOT PUBLICATION VERIFIED"
	}
	if next, exists := doctorRecordedReleaseNext(configPath); exists && next != "" && fail == 0 {
		kind = "EXISTING RELEASE REQUIRES INSPECTION"
	}
	fmt.Fprintf(w, "Readiness: %s (%s)\n", kind, scope)
	fmt.Fprintf(w, "Checks: FAIL %d  WARN %d  PASS %d  SKIP %d\n", fail, warn, pass, skip)
	findings := make([]doctorFinding, 0, fail+warn)
	for _, check := range report.Checks {
		if check.Severity == doctorFail || check.Severity == doctorWarn {
			findings = append(findings, check)
		}
	}
	sort.SliceStable(findings, func(i, j int) bool {
		return doctorPriority(findings[i]) < doctorPriority(findings[j])
	})
	seenFixes := make(map[string]bool)
	for _, check := range findings {
		fmt.Fprintf(w, "%s %s [%s]: %s\n",
			check.Severity, safeDisplay(check.Code), safeDisplay(check.Subject), safeDisplay(check.Message))
		if check.Fix != "" && !seenFixes[check.Fix] {
			seenFixes[check.Fix] = true
			fmt.Fprintf(w, "  Fix: %s\n", safeDisplay(check.Fix))
		}
	}
	fmt.Fprintf(w, "Next: %s\n", safeDisplay(doctorPrimaryNext(report, configPath)))
}
