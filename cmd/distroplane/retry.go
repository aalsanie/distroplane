package main

import (
	"fmt"
	"io"

	"github.com/aalsanie/distroplane/internal/domain"
	"github.com/aalsanie/distroplane/internal/journal"
	"github.com/aalsanie/distroplane/internal/planner"
)

// retry authorizes one subsequent apply attempt; it does not publish anything.
// A successful npm reconciliation observing an absent version is necessary but
// not sufficient: the operator must explicitly accept duplicate-publication risk.
func runRetry(args []string, stdout, stderr io.Writer) int {
	var planPath, journalPath, operationText, confirmPlan string
	var acknowledge, jsonMode bool
	fs := commandFlags("retry")
	fs.StringVar(&planPath, "plan", "", "")
	fs.StringVar(&journalPath, "journal", "", "")
	fs.StringVar(&operationText, "operation", "", "")
	fs.StringVar(&confirmPlan, "confirm-plan", "", "")
	fs.BoolVar(&acknowledge, "acknowledge-duplicate-risk", false, "")
	fs.BoolVar(&jsonMode, "json", false, "")
	if err := parseCommand(fs, args); err != nil {
		return writeCommandError(stderr, hasJSON(args), exitUsage, "usage", err)
	}
	if planPath == "" || journalPath == "" || operationText == "" ||
		confirmPlan == "" || !acknowledge {
		return writeCommandError(stderr, jsonMode, exitUsage, "usage",
			fmt.Errorf("--plan, --journal, --operation, --confirm-plan, and --acknowledge-duplicate-risk are required"))
	}
	operationID, err := domain.NewOperationID(operationText)
	if err != nil {
		return writeCommandError(stderr, jsonMode, exitInvalid, "invalid_operation", err)
	}
	persisted, err := planner.Load(planPath)
	if err != nil {
		return writeCommandError(stderr, jsonMode, exitInvalid, "invalid_plan", err)
	}
	if confirmPlan != string(persisted.ID()) {
		return writeCommandError(stderr, jsonMode, exitInvalid, "plan_confirmation_mismatch",
			fmt.Errorf("confirmation does not match immutable plan ID %q", persisted.ID()))
	}
	runID, err := resolveRunID(journalPath, "", false)
	if err != nil {
		return writeCommandError(stderr, jsonMode, exitInvalid, "invalid_run", err)
	}
	writer, err := journal.OpenWriter(journalPath, runID)
	if err != nil {
		return writeCommandError(stderr, jsonMode, exitOperational, "journal_open_failed", err)
	}
	authorized, authErr := writer.AuthorizeAmbiguousRetry(persisted.Plan(), runID, operationID)
	closeErr := writer.Close()
	if authErr != nil {
		return writeCommandError(stderr, jsonMode, exitInvalid, "retry_not_authorized", authErr)
	}
	if closeErr != nil {
		return writeCommandError(stderr, jsonMode, exitOperational, "journal_close_failed", closeErr)
	}
	output := struct {
		OutputSchemaVersion string `json:"outputSchemaVersion"`
		PlanID              string `json:"planId"`
		RunID               string `json:"runId"`
		OperationID         string `json:"operationId"`
		Attempt             uint32 `json:"priorAttempt"`
		Authorized          bool   `json:"authorized"`
	}{
		OutputSchemaVersion: outputSchemaVersion,
		PlanID: string(persisted.ID()), RunID: string(runID),
		OperationID: string(operationID), Attempt: authorized.Payload.Attempt, Authorized: true,
	}
	if jsonMode {
		return writeJSON(stdout, output)
	}
	fmt.Fprintf(stdout, "authorized one retry of operation %s in run %s (plan %s)\n", operationID, runID, persisted.ID())
	fmt.Fprintln(stdout, "WARNING: absence at reconciliation is not proof that a prior publish cannot complete.")
	fmt.Fprintln(stdout, "Run distroplane apply with the same plan and journal to perform the authorized attempt.")
	return exitOK
}
