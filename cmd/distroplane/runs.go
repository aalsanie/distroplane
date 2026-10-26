package main

import (
	"fmt"
	"io"
	"path/filepath"

	"github.com/aalsanie/distroplane/internal/config"
	"github.com/aalsanie/distroplane/internal/runstate"
)

func runRuns(args []string, stdout, stderr io.Writer) int {
	var configPath, releaseID, runID string
	var jsonMode bool
	fs := commandFlags("runs")
	fs.StringVar(&configPath, "config", "distroplane.json", "")
	fs.StringVar(&releaseID, "release", "", "")
	fs.StringVar(&runID, "run", "", "")
	fs.BoolVar(&jsonMode, "json", false, "")
	if err := parseCommand(fs, args); err != nil {
		return writeCommandError(stderr, hasJSON(args), exitUsage, "usage", err)
	}
	loaded, err := config.Load(configPath)
	if err != nil {
		return writeCommandError(stderr, jsonMode, exitInvalid, "invalid_config", err)
	}
	store := runstate.Store{Root: filepath.Join(loaded.BaseDir, ".distroplane")}
	records, err := store.Inventory()
	if err != nil {
		return writeCommandError(stderr, jsonMode, exitInvalid, "catalog_invalid", err)
	}
	if releaseID != "" || runID != "" {
		match, err := store.Select(releaseID, runID)
		if err != nil {
			return writeCommandError(stderr, jsonMode, exitInvalid, "run_selection_failed", err)
		}
		records = []runstate.Record{match}
	}
	if jsonMode {
		return writeJSON(stdout, struct {
			OutputSchemaVersion string            `json:"outputSchemaVersion"`
			Runs                []runstate.Record `json:"runs"`
		}{OutputSchemaVersion: outputSchemaVersion, Runs: records})
	}
	for _, run := range records {
		fmt.Fprintf(stdout, "release %s plan %s run %s state %s\n", run.ReleaseID, run.PlanID, run.RunID, run.State)
	}
	return exitOK
}
