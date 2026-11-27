package main

import (
	"fmt"
	"io"

	"github.com/aalsanie/distroplane/internal/domain"
	"github.com/aalsanie/distroplane/internal/journal"
)

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
	if _, err := domain.NewOperationID(operationText); err != nil {
		return writeCommandError(stderr, jsonMode, exitInvalid, "invalid_operation", err)
	}
	return writeCommandError(stderr, jsonMode, exitInvalid, "retry_not_authorized", journal.ErrRetryAuthorizationDisabled)
}
