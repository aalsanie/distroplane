package main

import (
	"context"
	"fmt"
	"io"
	"path/filepath"
	"strconv"

	"github.com/aalsanie/distroplane/internal/runstate"
)

// selectRecordedRun is read-only. In particular, RESERVED means the journal is
// absent after a durable claim; never invent a replacement journal or run ID.
func selectRecordedRun(configPath, releaseID, runID string) (runstate.Record, error) {
	configPath, err := filepath.Abs(configPath)
	if err != nil {
		return runstate.Record{}, err
	}
	store := runstate.Store{Root: filepath.Join(filepath.Dir(configPath), ".distroplane")}
	record, err := store.Select(releaseID, runID)
	if err != nil {
		return runstate.Record{}, err
	}
	if record.RunID == "" || record.JournalPath == "" {
		return runstate.Record{}, fmt.Errorf("release %q has no started run; publish must be explicitly approved first", record.ReleaseID)
	}
	if record.State == "RESERVED" {
		return runstate.Record{}, fmt.Errorf("run %q is reserved but its journal is missing; manual recovery required", record.RunID)
	}
	return record, nil
}

func runResumeContext(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	var configPath, releaseID, runID string
	var concurrency int
	var jsonMode, reconcileOnly bool
	mappings := credentialFlags{}
	fs := commandFlags("resume")
	fs.StringVar(&configPath, "config", "distroplane.json", "")
	fs.StringVar(&releaseID, "release", "", "")
	fs.StringVar(&runID, "run", "", "")
	fs.IntVar(&concurrency, "concurrency", 0, "")
	fs.BoolVar(&reconcileOnly, "reconcile-only", false, "")
	fs.BoolVar(&jsonMode, "json", false, "")
	fs.Var(mappings, "credential", "")
	if err := parseCommand(fs, args); err != nil {
		return writeCommandError(stderr, hasJSON(args), exitUsage, "usage", err)
	}
	if concurrency < 0 {
		return writeCommandError(stderr, jsonMode, exitUsage, "usage", fmt.Errorf("--concurrency must be zero or greater"))
	}
	if err := ctx.Err(); err != nil {
		return writeCommandError(stderr, jsonMode, exitOperational, "cancelled", err)
	}
	record, err := selectRecordedRun(configPath, releaseID, runID)
	if err != nil {
		return writeCommandError(stderr, jsonMode, exitInvalid, "run_selection_failed", err)
	}
	if record.Completed {
		// Finished work must not be re-executed or require retained artifacts.
		args := []string{"--plan", record.PlanPath, "--journal", record.JournalPath}
		if jsonMode {
			args = append(args, "--json")
		}
		return runStatus(args, stdout, stderr)
	}
	// All actual recovery is delegated to the existing verified executor,
	// including lease recovery and reconcile-before-retry requirements.
	operation := []string{"--config", configPath, "--plan", record.PlanPath,
		"--journal", record.JournalPath, "--run", record.RunID,
		"--concurrency", strconv.Itoa(concurrency)}
	if jsonMode {
		operation = append(operation, "--json")
	}
	for ref, environment := range mappings {
		operation = append(operation, "--credential", string(ref)+"="+environment)
	}
	return runExecutionContext(ctx, operation, stdout, stderr, reconcileOnly)
}

