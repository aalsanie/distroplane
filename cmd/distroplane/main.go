package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/signal"
)

var (
	version   = "0.0.0-dev"
	commit    = "unknown"
	buildDate = "unknown"
)

const (
	outputSchemaVersion = "1"

	exitOK          = 0
	exitOperational = 1
	exitUsage       = 2
	exitInvalid     = 3
	exitPending     = 4
	exitRejected    = 5
	exitFailed      = 6
)

type versionInfo struct {
	OutputSchemaVersion string `json:"outputSchemaVersion"`
	Version             string `json:"version"`
	Commit              string `json:"commit"`
	BuildDate           string `json:"buildDate"`
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), shutdownSignals()...)
	defer stop()
	os.Exit(executeContext(ctx, os.Args[1:], os.Stdout, os.Stderr))
}

func execute(args []string, stdout, stderr io.Writer) int {
	return executeContext(context.Background(), args, stdout, stderr)
}

func executeContext(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	if ctx == nil {
		fmt.Fprintln(stderr, "runtime: context must not be nil")
		return exitOperational
	}
	if len(args) == 0 {
		printHelp(stdout)
		return exitOK
	}
	if len(args) == 2 && (args[1] == "--help" || args[1] == "-h") {
		if printCommandHelp(args[0], stdout) {
			return exitOK
		}
	}
	switch args[0] {
	case "help", "-h", "--help":
		if len(args) != 1 {
			fmt.Fprintln(stderr, "help does not accept arguments")
			return exitUsage
		}
		printHelp(stdout)
		return exitOK
	case "version":
		return runVersion(args[1:], stdout, stderr)
	case "validate":
		return runValidate(args[1:], stdout, stderr)
	case "doctor":
		return runDoctorContext(ctx, args[1:], stdout, stderr)
	case "init":
		return runInitContext(ctx, args[1:], os.Stdin, stdinIsTerminal(), stdout, stderr)
	case "plan":
		return runPlanContext(ctx, args[1:], stdout, stderr)
	case "publish":
		return runPublishContext(ctx, args[1:], os.Stdin, stdout, stderr)
	case "resume":
		return runResumeContext(ctx, args[1:], stdout, stderr)
	case "apply":
		return runExecutionContext(ctx, args[1:], stdout, stderr, false)
	case "runs":
		return runRuns(args[1:], stdout, stderr)
	case "status":
		return runStatus(args[1:], stdout, stderr)
	case "reconcile":
		return runExecutionContext(ctx, args[1:], stdout, stderr, true)
	case "retry":
		return runRetry(args[1:], stdout, stderr)
	case "evidence":
		return runEvidence(args[1:], stdout, stderr)
	default:
		fmt.Fprintf(stderr, "unknown command %q\n", args[0])
		fmt.Fprintln(stderr, "run 'distroplane help' for usage")
		return exitUsage
	}
}

func runVersion(args []string, stdout, stderr io.Writer) int {
	info := versionInfo{OutputSchemaVersion: outputSchemaVersion, Version: version, Commit: commit, BuildDate: buildDate}
	switch len(args) {
	case 0:
		fmt.Fprintf(stdout, "distroplane %s (commit %s, built %s)\n", info.Version, info.Commit, info.BuildDate)
		return exitOK
	case 1:
		if args[0] != "--json" {
			fmt.Fprintf(stderr, "unknown version option %q\n", args[0])
			return exitUsage
		}
		data, err := json.Marshal(info)
		if err != nil {
			fmt.Fprintf(stderr, "encode version information: %v\n", err)
			return exitOperational
		}
		fmt.Fprintln(stdout, string(data))
		return exitOK
	default:
		fmt.Fprintln(stderr, "version accepts only the optional --json flag")
		return exitUsage
	}
}

func printHelp(w io.Writer) {
	fmt.Fprintln(w, "Distroplane — release distribution control plane")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Get started:")
	fmt.Fprintln(w, "  distroplane init       create release configuration")
	fmt.Fprintln(w, "  distroplane doctor     check that everything is ready")
	fmt.Fprintln(w, "  distroplane publish    review and publish the release")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Check an existing release:")
	fmt.Fprintln(w, "  distroplane status     see what happened")
	fmt.Fprintln(w, "  distroplane resume     continue a pending release safely")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Commands:")
	fmt.Fprintln(w, "  init [--destination PROVIDER] [--release ID] [--artifact PATH]")
	fmt.Fprintln(w, "  doctor [--config PATH] [--remote] [--json]")
	fmt.Fprintln(w, "  publish [--config PATH] [--dry-run] [--yes | --confirm-plan ID]")
	fmt.Fprintln(w, "  status [--config PATH] [--release ID] [--run ID]")
	fmt.Fprintln(w, "  resume [--config PATH] [--release ID] [--run ID] [--reconcile-only]")
	fmt.Fprintln(w, "  validate [--config PATH] [--json]")
	fmt.Fprintln(w, "  plan [--config PATH] [--json]")
	fmt.Fprintln(w, "  runs [--config PATH] [--json]")
	fmt.Fprintln(w, "  apply --config PATH --plan PATH --journal PATH")
	fmt.Fprintln(w, "  reconcile --config PATH --plan PATH --journal PATH")
	fmt.Fprintln(w, "  retry --plan PATH --journal PATH --operation ID --confirm-plan ID --acknowledge-duplicate-risk")
	fmt.Fprintln(w, "  evidence --plan PATH --journal PATH [--output PATH]")
	fmt.Fprintln(w, "  version [--json]")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Run distroplane <command> --help for command options.")
}

func printCommandHelp(command string, w io.Writer) bool {
	var summary string
	switch command {
	case "init":
		summary = "Create distroplane.json from a prebuilt artifact.\n  distroplane init [--destination PROVIDER] [--release ID] [--artifact PATH] [--set FIELD=VALUE]\n  Options: --output PATH, --force, --non-interactive, --json\n  Providers: npm, sdkman, homebrew:formula, homebrew:cask, homebrew:jvm, winget:msi, winget:zip"
	case "doctor":
		summary = "Check local readiness without publishing.\n  distroplane doctor [--config PATH] [--plan PATH] [--remote] [--timeout 3s] [--json]"
	case "publish":
		summary = "Preview, approve and publish a release.\n  distroplane publish [--config PATH] [--dry-run] [--yes | --confirm-plan ID] [--credential REF=ENV] [--json]"
	case "status":
		summary = "Inspect an existing release without publishing.\n  distroplane status [--config PATH] [--release ID] [--run ID] [--json]"
	case "resume":
		summary = "Continue an existing release without creating a new one.\n  distroplane resume [--config PATH] [--release ID] [--run ID] [--reconcile-only] [--credential REF=ENV] [--json]"
	case "validate":
		summary = "Validate release configuration.\n  distroplane validate [--config PATH] [--json]"
	case "plan":
		summary = "Save an immutable release plan without publishing.\n  distroplane plan [--config PATH] [--state-dir PATH] [--json]"
	case "runs":
		summary = "List existing releases.\n  distroplane runs [--config PATH] [--release ID] [--run ID] [--json]"
	case "apply":
		summary = "Execute or resume a reviewed plan.\n  distroplane apply --config PATH --plan PATH --journal PATH [--credential REF=ENV] [--json]"
	case "reconcile":
		summary = "Inspect uncertain destination outcomes without publishing.\n  distroplane reconcile --config PATH --plan PATH --journal PATH [--credential REF=ENV] [--json]"
	case "retry":
		summary = "Authorize one reconciled npm retry after accepting duplicate risk.\n  distroplane retry --plan PATH --journal PATH --operation ID --confirm-plan ID --acknowledge-duplicate-risk"
	case "evidence":
		summary = "Export recorded release evidence.\n  distroplane evidence --plan PATH --journal PATH [--output PATH] [--json]"
	case "version":
		summary = "Show the CLI version.\n  distroplane version [--json]"
	default:
		return false
	}
	fmt.Fprintln(w, summary)
	return true
}

func stdinIsTerminal() bool {
	info, err := os.Stdin.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}
