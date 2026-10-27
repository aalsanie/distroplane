package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/aalsanie/distroplane/internal/config"
	"github.com/aalsanie/distroplane/internal/domain"
	"github.com/aalsanie/distroplane/internal/planner"
	"github.com/aalsanie/distroplane/internal/providerhost"
	"github.com/aalsanie/distroplane/internal/runstate"
)

type publishOutput struct {
	OutputSchemaVersion string       `json:"outputSchemaVersion"`
	Plan                planOutput   `json:"plan"`
	Approved            bool         `json:"approved"`
	JournalPath         string       `json:"journalPath,omitempty"`
	Run                 *stateOutput `json:"run,omitempty"`
}

// runPublishContext never authorizes provider side effects until the exact
// content-addressed plan has been presented and explicitly approved.
func runPublishContext(ctx context.Context, args []string, input io.Reader, stdout, stderr io.Writer) int {
	var configPath, confirmPlan string
	var yes, jsonMode, dryRun bool
	var concurrency int
	mappings := credentialFlags{}
	fs := commandFlags("publish")
	fs.StringVar(&configPath, "config", "distroplane.json", "")
	fs.StringVar(&confirmPlan, "confirm-plan", "", "")
	fs.IntVar(&concurrency, "concurrency", 0, "")
	fs.BoolVar(&yes, "yes", false, "")
	fs.BoolVar(&dryRun, "dry-run", false, "")
	fs.BoolVar(&jsonMode, "json", false, "")
	fs.Var(mappings, "credential", "")
	if err := parseCommand(fs, args); err != nil {
		return writeCommandError(stderr, hasJSON(args), exitUsage, "usage", err)
	}
	if concurrency < 0 {
		return writeCommandError(stderr, jsonMode, exitUsage, "usage", fmt.Errorf("--concurrency must be zero or greater"))
	}
	if dryRun && (yes || confirmPlan != "") {
		return writeCommandError(stderr, jsonMode, exitUsage, "usage", fmt.Errorf("--dry-run cannot be combined with approval flags"))
	}
	if jsonMode && !dryRun && !yes && confirmPlan == "" {
		return writeCommandError(stderr, jsonMode, exitUsage, "approval_required", fmt.Errorf("noninteractive JSON publication requires --yes or --confirm-plan"))
	}
	if err := ctx.Err(); err != nil {
		return writeCommandError(stderr, jsonMode, exitOperational, "cancelled", err)
	}

	// Compute the exact immutable plan in memory. Check the existing release
	// before persisting it: a rejected replan must not pollute the run catalog.
	loaded, err := config.Load(configPath)
	if err != nil {
		return writeCommandError(stderr, jsonMode, exitInvalid, "invalid_config", err)
	}
	client, err := providerhost.New(providerhost.Options{})
	if err != nil {
		return writeCommandError(stderr, jsonMode, exitOperational, "runtime", err)
	}
	builder, err := planner.New(client, planner.Options{})
	if err != nil {
		return writeCommandError(stderr, jsonMode, exitOperational, "runtime", err)
	}
	distribution, err := builder.Build(ctx, loaded)
	if err != nil {
		return writeCommandError(stderr, jsonMode, exitOperational, "plan_failed", err)
	}
	plan := distribution.Plan()
	root := filepath.Join(loaded.BaseDir, ".distroplane")
	catalog := runstate.Store{Root: root}
	records, err := catalog.Inventory()
	if err != nil {
		return writeCommandError(stderr, jsonMode, exitInvalid, "catalog_invalid", err)
	}
	for _, record := range records {
		if record.ReleaseID != string(plan.Release().ID()) {
			continue
		}
		if record.PlanID != string(plan.ID()) {
			return writeCommandError(stderr, jsonMode, exitInvalid, "release_conflict", fmt.Errorf("release %q has a different saved plan; inspect its existing runs", record.ReleaseID))
		}
		if record.RunID != "" {
			return writeCommandError(stderr, jsonMode, exitInvalid, "run_already_exists", fmt.Errorf("plan %q already has run %q; inspect its status rather than creating another publication", record.PlanID, record.RunID))
		}
	}
	if confirmPlan != "" && confirmPlan != string(plan.ID()) {
		return writeCommandError(stderr, jsonMode, exitInvalid, "approval_mismatch", fmt.Errorf("confirmed plan ID does not match the current immutable plan"))
	}
	if err := catalog.Ensure(); err != nil {
		return writeCommandError(stderr, jsonMode, exitOperational, "catalog_failed", err)
	}
	path, err := (planner.Store{Root: root}).Save(distribution)
	if err != nil {
		return writeCommandError(stderr, jsonMode, exitOperational, "persist_plan_failed", err)
	}
	if _, err := catalog.RegisterPlan(path); err != nil {
		return writeCommandError(stderr, jsonMode, exitOperational, "catalog_failed", err)
	}
	planned := planOutput{
		OutputSchemaVersion: outputSchemaVersion,
		PlanID: string(plan.ID()), Path: path,
		Targets: len(plan.Targets()), Operations: len(plan.Operations()),
	}

	if !jsonMode {
		printPublishPreview(stderr, plan, planned.Path)
	}
	if dryRun {
		out := publishOutput{OutputSchemaVersion: outputSchemaVersion, Plan: planned}
		if jsonMode {
			return writeJSON(stdout, out)
		}
		fmt.Fprintln(stdout, "preview only; no publication was authorized")
		return exitOK
	}
	if !yes && confirmPlan == "" {
		if input == nil {
			return writeCommandError(stderr, jsonMode, exitUsage, "approval_required", fmt.Errorf("publication needs explicit approval"))
		}
		fmt.Fprintf(stderr, "To publish, type: publish %s\n> ", planned.PlanID)
		line, readErr := bufio.NewReader(input).ReadString('\n')
		if readErr != nil && !errors.Is(readErr, io.EOF) {
			return writeCommandError(stderr, jsonMode, exitOperational, "approval_failed", fmt.Errorf("cannot read approval: %w", readErr))
		}
		if strings.TrimSpace(line) != "publish "+planned.PlanID {
			return writeCommandError(stderr, jsonMode, exitInvalid, "approval_denied", fmt.Errorf("publication was not approved"))
		}
	}
	if err := ctx.Err(); err != nil {
		return writeCommandError(stderr, jsonMode, exitOperational, "cancelled", err)
	}

	journalPath := filepath.Join(root, "runs", planned.PlanID+".journal")
	applyArgs := []string{"--config", configPath, "--plan", planned.Path, "--journal", journalPath, "--concurrency", strconv.Itoa(concurrency), "--json"}
	refs := make([]domain.CredentialRef, 0, len(mappings))
	for ref := range mappings {
		refs = append(refs, ref)
	}
	sort.Slice(refs, func(i, j int) bool { return refs[i] < refs[j] })
	for _, ref := range refs {
		applyArgs = append(applyArgs, "--credential", string(ref)+"="+mappings[ref])
	}
	var executionJSON bytes.Buffer
	code := runExecutionContext(ctx, applyArgs, &executionJSON, stderr, false)
	if executionJSON.Len() == 0 {
		return code
	}
	var run stateOutput
	if err := json.Unmarshal(executionJSON.Bytes(), &run); err != nil {
		return writeCommandError(stderr, jsonMode, exitOperational, "execution_output_invalid", err)
	}
	if !jsonMode {
		fmt.Fprintf(stdout, "plan %s\npath %s\njournal %s\n", planned.PlanID, planned.Path, journalPath)
		writeStateText(stdout, run)
		return code
	}
	out := publishOutput{
		OutputSchemaVersion: outputSchemaVersion, Plan: planned,
		Approved: true, JournalPath: journalPath, Run: &run,
	}
	if outputCode := writeJSON(stdout, out); outputCode != exitOK {
		return outputCode
	}
	return code
}

func printPublishPreview(w io.Writer, plan domain.Plan, path string) {
	fmt.Fprintf(w, "Release %s\nPlan %s\nSaved plan %s\n", plan.Release().ID(), plan.ID(), path)
	for _, artifact := range plan.Release().Artifacts() {
		fmt.Fprintf(w, "Artifact %s: %s (%s)\n", artifact.Name(), artifact.Source(), artifact.Digest())
	}
	for _, target := range plan.Targets() {
		fmt.Fprintf(w, "Target %s: %s\n", target.ID(), target.Provider().Name())
	}
	fmt.Fprintf(w, "Operations %d\n", len(plan.Operations()))
}
