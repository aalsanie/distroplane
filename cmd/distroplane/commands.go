package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/aalsanie/distroplane/internal/config"
	"github.com/aalsanie/distroplane/internal/credentials"
	"github.com/aalsanie/distroplane/internal/domain"
	"github.com/aalsanie/distroplane/internal/executor"
	"github.com/aalsanie/distroplane/internal/journal"
	"github.com/aalsanie/distroplane/internal/planner"
	"github.com/aalsanie/distroplane/internal/providerhost"
)

type credentialFlags map[domain.CredentialRef]string

func (c credentialFlags) String() string { return "" }

func (c credentialFlags) Set(value string) error {
	refText, environment, ok := strings.Cut(value, "=")
	if !ok || refText == "" || environment == "" {
		return fmt.Errorf("credential must use REF=ENV format")
	}
	ref, err := domain.NewCredentialRef(refText)
	if err != nil {
		return err
	}
	if _, exists := c[ref]; exists {
		return fmt.Errorf("credential %q is already mapped", ref)
	}
	c[ref] = environment
	return nil
}

type validateOutput struct {
	OutputSchemaVersion string `json:"outputSchemaVersion"`
	Valid               bool   `json:"valid"`
	SchemaVersion       string `json:"schemaVersion"`
	Targets             int    `json:"targets"`
}

type planOutput struct {
	OutputSchemaVersion string `json:"outputSchemaVersion"`
	PlanID              string `json:"planId"`
	Path                string `json:"path"`
	Targets             int    `json:"targets"`
	Operations          int    `json:"operations"`
}

type targetStateOutput struct {
	ID                string `json:"id"`
	State             string `json:"state"`
	ReconcileRequired bool   `json:"reconcileRequired"`
	Ambiguous         bool   `json:"ambiguous"`
}

type operationStateOutput struct {
	ID                string          `json:"id"`
	TargetID          string          `json:"targetId"`
	State             string          `json:"state"`
	Attempt           uint32          `json:"attempt"`
	ReconcileRequired bool            `json:"reconcileRequired"`
	Ambiguous         bool            `json:"ambiguous"`
	Retryable         bool            `json:"retryable"`
	ProviderState     string          `json:"providerState,omitempty"`
	Evidence          json.RawMessage `json:"evidence,omitempty"`
	ErrorCode         string          `json:"errorCode,omitempty"`
}

type stateOutput struct {
	OutputSchemaVersion  string                 `json:"outputSchemaVersion"`
	PlanID               string                 `json:"planId"`
	RunID                string                 `json:"runId"`
	Completed            bool                   `json:"completed"`
	Cancelled            bool                   `json:"cancelled"`
	JournalTruncatedTail bool                   `json:"journalTruncatedTail,omitempty"`
	Targets              []targetStateOutput    `json:"targets"`
	Operations           []operationStateOutput `json:"operations"`
}

type errorOutput struct {
	OutputSchemaVersion string `json:"outputSchemaVersion"`
	Error struct {
		Kind    string `json:"kind"`
		Message string `json:"message"`
	} `json:"error"`
}

func runValidate(args []string, stdout, stderr io.Writer) int {
	var configPath string
	var jsonMode bool
	fs := commandFlags("validate")
	fs.StringVar(&configPath, "config", "distroplane.json", "")
	fs.BoolVar(&jsonMode, "json", false, "")
	if err := parseCommand(fs, args); err != nil {
		return writeCommandError(stderr, hasJSON(args), exitUsage, "usage", err)
	}
	loaded, err := config.Load(configPath)
	if err != nil {
		return writeCommandError(stderr, jsonMode, exitInvalid, "invalid_config", err)
	}
	output := validateOutput{
		OutputSchemaVersion: outputSchemaVersion,
		Valid:               true,
		SchemaVersion:       loaded.Config.SchemaVersion,
		Targets:             len(loaded.Config.Targets),
	}
	if jsonMode {
		return writeJSON(stdout, output)
	}
	fmt.Fprintf(stdout, "valid config %s (%d targets)\n", configPath, output.Targets)
	return exitOK
}

func runPlan(args []string, stdout, stderr io.Writer) int {
	return runPlanContext(context.Background(), args, stdout, stderr)
}

func runPlanContext(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	var configPath, stateDir string
	var jsonMode bool
	fs := commandFlags("plan")
	fs.StringVar(&configPath, "config", "distroplane.json", "")
	fs.StringVar(&stateDir, "state-dir", "", "")
	fs.BoolVar(&jsonMode, "json", false, "")
	if err := parseCommand(fs, args); err != nil {
		return writeCommandError(stderr, hasJSON(args), exitUsage, "usage", err)
	}
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
	plan, err := builder.Build(ctx, loaded)
	if err != nil {
		return writeCommandError(stderr, jsonMode, exitOperational, "plan_failed", err)
	}
	if stateDir == "" {
		stateDir = filepath.Join(loaded.BaseDir, ".distroplane")
	} else if !filepath.IsAbs(stateDir) {
		stateDir = filepath.Join(loaded.BaseDir, stateDir)
	}
	path, err := (planner.Store{Root: stateDir}).Save(plan)
	if err != nil {
		return writeCommandError(stderr, jsonMode, exitOperational, "persist_plan_failed", err)
	}
	output := planOutput{
		OutputSchemaVersion: outputSchemaVersion,
		PlanID:              string(plan.ID()),
		Path:                path,
		Targets:             len(plan.Plan().Targets()),
		Operations:          len(plan.Plan().Operations()),
	}
	if jsonMode {
		return writeJSON(stdout, output)
	}
	fmt.Fprintf(stdout, "plan %s\npath %s\ntargets %d\noperations %d\n", output.PlanID, output.Path, output.Targets, output.Operations)
	return exitOK
}

func runApply(args []string, stdout, stderr io.Writer) int {
	return runExecutionContext(context.Background(), args, stdout, stderr, false)
}

func runReconcile(args []string, stdout, stderr io.Writer) int {
	return runExecutionContext(context.Background(), args, stdout, stderr, true)
}

func runExecutionContext(ctx context.Context, args []string, stdout, stderr io.Writer, reconcileOnly bool) int {
	command := "apply"
	if reconcileOnly {
		command = "reconcile"
	}
	var configPath, planPath, journalPath, requestedRun string
	var concurrency int
	var jsonMode bool
	credentialMap := credentialFlags{}
	fs := commandFlags(command)
	fs.StringVar(&configPath, "config", "distroplane.json", "")
	fs.StringVar(&planPath, "plan", "", "")
	fs.StringVar(&journalPath, "journal", "", "")
	fs.StringVar(&requestedRun, "run", "", "")
	fs.IntVar(&concurrency, "concurrency", 0, "")
	fs.BoolVar(&jsonMode, "json", false, "")
	fs.Var(credentialMap, "credential", "")
	if err := parseCommand(fs, args); err != nil {
		return writeCommandError(stderr, hasJSON(args), exitUsage, "usage", err)
	}
	if planPath == "" || journalPath == "" {
		return writeCommandError(stderr, jsonMode, exitUsage, "usage", fmt.Errorf("--plan and --journal are required"))
	}
	if concurrency < 0 {
		return writeCommandError(stderr, jsonMode, exitUsage, "usage", fmt.Errorf("--concurrency must be zero or greater"))
	}
	persisted, err := planner.Load(planPath)
	if err != nil {
		return writeCommandError(stderr, jsonMode, exitInvalid, "invalid_plan", err)
	}
	if !reconcileOnly {
		if err := planner.VerifyArtifacts(persisted.Plan(), nil); err != nil {
			return writeCommandError(stderr, jsonMode, exitInvalid, "artifact_changed", err)
		}
	}
	loaded, err := config.Load(configPath)
	if err != nil {
		return writeCommandError(stderr, jsonMode, exitInvalid, "invalid_config", err)
	}
	driver, err := executionDriver(loaded, persisted.Plan(), credentialMap)
	if err != nil {
		return writeCommandError(stderr, jsonMode, exitOperational, "provider_binding_failed", err)
	}
	runID, err := resolveRunID(journalPath, requestedRun, !reconcileOnly)
	if err != nil {
		return writeCommandError(stderr, jsonMode, exitInvalid, "invalid_run", err)
	}
	writer, err := journal.OpenWriter(journalPath, runID)
	if err != nil {
		return writeCommandError(stderr, jsonMode, exitOperational, "journal_open_failed", err)
	}
	engine, err := executor.New(driver, executor.Options{ReconcileOnly: reconcileOnly, MaxConcurrency: concurrency})
	if err != nil {
		_ = writer.Close()
		return writeCommandError(stderr, jsonMode, exitOperational, "runtime", err)
	}
	state, executeErr := engine.Execute(ctx, persisted.Plan(), runID, writer)
	closeErr := writer.Close()
	if executeErr != nil {
		return writeCommandError(stderr, jsonMode, exitOperational, "execution_failed", executeErr)
	}
	if closeErr != nil {
		return writeCommandError(stderr, jsonMode, exitOperational, "journal_close_failed", closeErr)
	}
	output := stateView(persisted.Plan(), state, false)
	code := stateExit(state)
	if jsonMode {
		if writeCode := writeJSON(stdout, output); writeCode != exitOK {
			return writeCode
		}
		return code
	}
	writeStateText(stdout, output)
	return code
}

func runStatus(args []string, stdout, stderr io.Writer) int {
	var planPath, journalPath string
	var jsonMode bool
	fs := commandFlags("status")
	fs.StringVar(&planPath, "plan", "", "")
	fs.StringVar(&journalPath, "journal", "", "")
	fs.BoolVar(&jsonMode, "json", false, "")
	if err := parseCommand(fs, args); err != nil {
		return writeCommandError(stderr, hasJSON(args), exitUsage, "usage", err)
	}
	if planPath == "" || journalPath == "" {
		return writeCommandError(stderr, jsonMode, exitUsage, "usage", fmt.Errorf("--plan and --journal are required"))
	}
	persisted, err := planner.Load(planPath)
	if err != nil {
		return writeCommandError(stderr, jsonMode, exitInvalid, "invalid_plan", err)
	}
	read, err := readJournal(journalPath)
	if err != nil {
		return writeCommandError(stderr, jsonMode, exitOperational, "journal_read_failed", err)
	}
	if len(read.Events) == 0 {
		return writeCommandError(stderr, jsonMode, exitInvalid, "invalid_run", fmt.Errorf("journal contains no events"))
	}
	state, err := journal.Reduce(persisted.Plan(), read.Events)
	if err != nil {
		return writeCommandError(stderr, jsonMode, exitOperational, "journal_reduce_failed", err)
	}
	output := stateView(persisted.Plan(), state, read.TruncatedTail)
	code := stateExit(state)
	if jsonMode {
		if writeCode := writeJSON(stdout, output); writeCode != exitOK {
			return writeCode
		}
		return code
	}
	if read.TruncatedTail {
		fmt.Fprintln(stderr, "warning: journal has a truncated final record; status reflects the durable prefix")
	}
	writeStateText(stdout, output)
	return code
}

func executionDriver(loaded config.Loaded, plan domain.Plan, mappings credentialFlags) (*providerhost.Driver, error) {
	targetConfig := make(map[string]config.Target, len(loaded.Config.Targets))
	for _, target := range loaded.Config.Targets {
		targetConfig[target.ID] = target
	}
	bindingByProvider := make(map[domain.ProviderRef]string)
	for _, target := range plan.Targets() {
		configured, ok := targetConfig[string(target.ID())]
		if !ok {
			return nil, fmt.Errorf("plan target %q is missing from config", target.ID())
		}
		if configured.Provider.Name != string(target.Provider().Name()) {
			return nil, fmt.Errorf("plan target %q provider is %q, config has %q", target.ID(), target.Provider().Name(), configured.Provider.Name)
		}
		executable, err := planner.ResolveExecutable(loaded.BaseDir, configured.Provider.Name, configured.Provider.Executable)
		if err != nil {
			return nil, err
		}
		if existing, exists := bindingByProvider[target.Provider()]; exists && existing != executable {
			return nil, fmt.Errorf("provider %q@%q resolves to multiple executables", target.Provider().Name(), target.Provider().Version())
		}
		bindingByProvider[target.Provider()] = executable
	}
	providers := make([]domain.ProviderRef, 0, len(bindingByProvider))
	for provider := range bindingByProvider {
		providers = append(providers, provider)
	}
	sort.Slice(providers, func(i, j int) bool {
		if providers[i].Name() != providers[j].Name() {
			return providers[i].Name() < providers[j].Name()
		}
		return providers[i].Version() < providers[j].Version()
	})
	bindings := make([]providerhost.Binding, 0, len(providers))
	for _, provider := range providers {
		bindings = append(bindings, providerhost.Binding{Provider: provider, Executable: bindingByProvider[provider]})
	}
	client, err := providerhost.New(providerhost.Options{})
	if err != nil {
		return nil, err
	}
	if len(mappings) == 0 {
		return providerhost.NewDriver(client, bindings)
	}
	sources := make(map[domain.CredentialRef]string, len(mappings))
	for ref, environment := range mappings {
		sources[ref] = environment
	}
	resolver, err := credentials.NewEnvironmentResolver(sources)
	if err != nil {
		return nil, err
	}
	return providerhost.NewDriverWithCredentials(client, resolver, bindings)
}

func resolveRunID(path, requested string, allowCreate bool) (domain.RunID, error) {
	var requestedID domain.RunID
	var err error
	if requested != "" {
		requestedID, err = domain.NewRunID(requested)
		if err != nil {
			return "", err
		}
	}
	read, err := readJournal(path)
	if err == nil && len(read.Events) != 0 {
		existing := read.Events[0].RunID
		if requestedID.Valid() && existing != requestedID {
			return "", fmt.Errorf("journal belongs to run %q", existing)
		}
		return existing, nil
	}
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	if !allowCreate {
		return "", fmt.Errorf("reconcile requires an existing journal")
	}
	if requestedID.Valid() {
		return requestedID, nil
	}
	var random [16]byte
	if _, err := rand.Read(random[:]); err != nil {
		return "", err
	}
	return domain.NewRunID("run-" + hex.EncodeToString(random[:]))
}

func readJournal(path string) (journal.ReadResult, error) {
	file, err := os.Open(path)
	if err != nil {
		return journal.ReadResult{}, err
	}
	defer file.Close()
	return journal.Read(file)
}

func stateView(plan domain.Plan, state journal.DerivedState, truncated bool) stateOutput {
	output := stateOutput{
		OutputSchemaVersion: outputSchemaVersion,
		PlanID: string(plan.ID()), RunID: string(state.RunID), Completed: state.Completed, Cancelled: state.Cancelled,
		JournalTruncatedTail: truncated, Targets: []targetStateOutput{}, Operations: []operationStateOutput{},
	}
	for _, target := range state.Targets() {
		output.Targets = append(output.Targets, targetStateOutput{
			ID: string(target.ID), State: string(target.State), ReconcileRequired: target.ReconcileRequired, Ambiguous: target.Ambiguous,
		})
	}
	for _, operation := range state.Operations() {
		output.Operations = append(output.Operations, operationStateOutput{
			ID: string(operation.ID), TargetID: string(operation.TargetID), State: string(operation.State), Attempt: operation.Attempt,
			ReconcileRequired: operation.ReconcileRequired, Ambiguous: operation.Ambiguous, Retryable: operation.Retryable,
			ProviderState: operation.ProviderState, Evidence: append(json.RawMessage(nil), operation.Evidence...), ErrorCode: operation.ErrorCode,
		})
	}
	return output
}

func stateExit(state journal.DerivedState) int {
	pending := !state.Completed
	rejected := false
	failed := state.Cancelled
	for _, operation := range state.Operations() {
		switch operation.State {
		case domain.StateFailed, domain.StateCancelled:
			failed = true
		case domain.StateRejected:
			rejected = true
		case domain.StatePublished:
		default:
			pending = true
		}
		if operation.ReconcileRequired {
			pending = true
		}
	}
	if failed {
		return exitFailed
	}
	if rejected {
		return exitRejected
	}
	if pending {
		return exitPending
	}
	return exitOK
}

func writeStateText(w io.Writer, output stateOutput) {
	fmt.Fprintf(w, "run %s\nplan %s\ncompleted %t\n", output.RunID, output.PlanID, output.Completed)
	for _, target := range output.Targets {
		fmt.Fprintf(w, "target %s %s", target.ID, target.State)
		if target.ReconcileRequired {
			fmt.Fprint(w, " reconcile-required")
		}
		if target.Ambiguous {
			fmt.Fprint(w, " ambiguous")
		}
		fmt.Fprintln(w)
	}
}

func commandFlags(name string) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	return fs
}

func parseCommand(fs *flag.FlagSet, args []string) error {
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("unexpected arguments: %s", strings.Join(fs.Args(), " "))
	}
	return nil
}

func hasJSON(args []string) bool {
	for _, arg := range args {
		if arg == "--json" {
			return true
		}
	}
	return false
}

func writeCommandError(w io.Writer, jsonMode bool, code int, kind string, err error) int {
	if jsonMode {
		var output errorOutput
		output.OutputSchemaVersion = outputSchemaVersion
		output.Error.Kind = kind
		output.Error.Message = err.Error()
		if writeJSON(w, output) != exitOK {
			return exitOperational
		}
		return code
	}
	fmt.Fprintf(w, "%s: %v\n", kind, err)
	return code
}

func writeJSON(w io.Writer, value any) int {
	encoder := json.NewEncoder(w)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		return exitOperational
	}
	return exitOK
}
