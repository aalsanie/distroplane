package main

import (
	"fmt"
	"io"
	"runtime"
	"strings"
)

type stateTextSelection struct {
	configPath     string
	planPath       string
	journalPath    string
	runID          string
	catalog        bool
	configVerified bool
}

func (s stateTextSelection) command(verb string) string {
	var args []string
	add := func(flag, value string) bool {
		quoted, ok := shellArgument(value, runtime.GOOS)
		if !ok {
			return false
		}
		args = append(args, flag, quoted)
		return true
	}
	switch verb {
	case "status", "resume":
		if s.catalog {
			if !add("--config", s.configPath) || !add("--run", s.runID) {
				return ""
			}
		} else if verb == "status" {
			if !add("--plan", s.planPath) || !add("--journal", s.journalPath) {
				return ""
			}
		} else {
			return ""
		}
	case "reconcile":
		if s.catalog {
			if !add("--config", s.configPath) || !add("--run", s.runID) {
				return ""
			}
			return "distroplane resume " + strings.Join(args, " ") + " --reconcile-only"
		}
		if !s.configVerified || !add("--config", s.configPath) ||
			!add("--plan", s.planPath) || !add("--journal", s.journalPath) {
			return ""
		}
	case "evidence":
		if !add("--plan", s.planPath) || !add("--journal", s.journalPath) {
			return ""
		}
	default:
		return ""
	}
	return "distroplane " + verb + " " + strings.Join(args, " ")
}

type releaseAssessment struct {
	ambiguous         bool
	external          bool
	blocked           bool
	credentialBlocked bool
	rejected          bool
	failed            bool
	cancelled         bool
	published         bool
	other             bool
}

func assessRelease(output stateOutput) releaseAssessment {
	a := releaseAssessment{published: output.Completed && !output.Cancelled &&
		!output.JournalTruncatedTail && len(output.Targets) > 0 && len(output.Operations) > 0}
	for _, target := range output.Targets {
		if target.State != "PUBLISHED" || target.ReconcileRequired || target.Ambiguous {
			a.published = false
		}
		if target.Ambiguous {
			a.ambiguous = true
		}
		if target.ReconcileRequired && !target.Ambiguous && target.State == "WAITING_EXTERNAL" {
			a.external = true
		}
	}
	for _, operation := range output.Operations {
		if operation.State != "PUBLISHED" || operation.ReconcileRequired || operation.Ambiguous {
			a.published = false
		}
		if operation.Ambiguous {
			a.ambiguous = true
		}
		switch operation.State {
		case "WAITING_EXTERNAL":
			if !operation.Ambiguous {
				a.external = true
			}
		case "BLOCKED":
			a.blocked = true
			if operation.ErrorCode == "CREDENTIAL_UNAVAILABLE" ||
				operation.ErrorCode == "CREDENTIAL_RESOLUTION_FAILED" {
				a.credentialBlocked = true
			}
		case "REJECTED":
			a.rejected = true
		case "FAILED":
			a.failed = true
		case "CANCELLED":
			a.cancelled = true
		case "PUBLISHED":
		default:
			a.other = true
		}
	}
	a.cancelled = a.cancelled || output.Cancelled
	if len(output.Operations) == 0 {
		for _, target := range output.Targets {
			switch target.State {
			case "WAITING_EXTERNAL":
				a.external = a.external || !target.Ambiguous
			case "BLOCKED":
				a.blocked = true
			case "REJECTED":
				a.rejected = true
			case "FAILED":
				a.failed = true
			case "CANCELLED":
				a.cancelled = true
			case "PUBLISHED":
			default:
				a.other = true
			}
		}
	}
	return a
}

func (a releaseAssessment) label(output stateOutput) string {
	terminal := a.failed || a.cancelled || a.rejected
	outstanding := a.ambiguous || a.external || a.blocked || a.other
	switch {
	case output.JournalTruncatedTail:
		return "durable-prefix state only; final journal record was truncated"
	case terminal && outstanding:
		return "mixed: terminal outcomes and outstanding publication work"
	case a.ambiguous:
		return "uncertain external outcome; publication may already have occurred"
	case a.external:
		return "waiting for external review or acceptance; public indexing is not verified"
	case a.blocked && a.credentialBlocked:
		return "blocked before publication dispatch by an unavailable credential"
	case a.blocked:
		return "blocked by an unresolved prerequisite or dependency"
	case a.failed || a.cancelled:
		return "failed or cancelled; inspect the recorded cause"
	case a.rejected:
		return "finished with rejection; publication was not successful"
	case a.published:
		return "recorded as published; public availability is not independently verified"
	case output.Completed:
		return "finished without all targets published"
	default:
		return "incomplete; inspect the original release state"
	}
}

func (a releaseAssessment) next(output stateOutput, s stateTextSelection) string {
	switch {
	case a.ambiguous:
		if command := s.command("reconcile"); command != "" {
			return "The remote side effect may already have occurred. Reconcile the original run before any retry: " + command
		}
		return "The remote side effect may already have occurred. Preserve the original plan and journal; perform read-only reconciliation before any retry. Restore the original configuration if necessary"
	case a.external:
		if command := s.command("reconcile"); command != "" {
			return "Check the original external submission without another publish attempt: " + command
		}
		return "Preserve the original plan and journal; check external acceptance through read-only reconciliation, not a new publication"
	case output.JournalTruncatedTail:
		return "Preserve the original journal and verify the durable record before deciding whether any recovery is safe"
	case a.blocked:
		prefix := "Resolve the recorded prerequisite"
		if a.credentialBlocked {
			prefix = "Restore the approved credential environment source"
		}
		if command := s.command("resume"); command != "" {
			return prefix + ", then resume this same recorded run: " + command
		}
		return prefix + "; inspect the original run catalog before resuming. Never create a replacement run"
	case a.failed || a.cancelled || a.rejected:
		if command := s.command("status"); command != "" {
			return "Inspect the recorded cause with " + command + "; do not start a replacement publication or retry automatically"
		}
		return "Inspect the recorded cause of failure or rejection and preserve the original run; do not retry automatically"
	case a.published:
		if command := s.command("evidence"); command != "" {
			return "Inspect the recorded evidence with " + command + "; confirm public availability separately"
		}
		return "Inspect the recorded evidence and verify public destination availability separately"
	default:
		if command := s.command("status"); command != "" {
			return "Inspect the original run with " + command + "; do not start a replacement publication"
		}
		return "Inspect the original plan and journal before attempting any further publication"
	}
}

func writeStateTextWithSelection(w io.Writer, output stateOutput, selection stateTextSelection) {
	assessment := assessRelease(output)
	fmt.Fprintf(w, "run %s\nplan %s\ncompleted %t\n", safeDisplay(output.RunID), safeDisplay(output.PlanID), output.Completed)
	fmt.Fprintf(w, "outcome %s\n", assessment.label(output))
	for _, target := range output.Targets {
		fmt.Fprintf(w, "target %s %s", safeDisplay(target.ID), safeDisplay(target.State))
		if target.ReconcileRequired {
			fmt.Fprint(w, " reconcile-required")
		}
		if target.Ambiguous {
			fmt.Fprint(w, " ambiguous")
		}
		fmt.Fprintln(w)
	}
	for _, operation := range output.Operations {
		if operation.State == "PUBLISHED" {
			continue
		}
		fmt.Fprintf(w, "operation %s [%s] %s", safeDisplay(operation.ID),
			safeDisplay(operation.TargetID), safeDisplay(operation.State))
		if operation.Ambiguous {
			fmt.Fprint(w, " ambiguous")
		}
		if operation.ErrorCode == "CREDENTIAL_UNAVAILABLE" ||
			operation.ErrorCode == "CREDENTIAL_RESOLUTION_FAILED" ||
			operation.ErrorCode == "DEPENDENCY_TERMINAL" {
			fmt.Fprintf(w, " %s", operation.ErrorCode)
		}
		fmt.Fprintln(w)
	}
	fmt.Fprintf(w, "Next: %s\n", safeDisplay(assessment.next(output, selection)))
}
