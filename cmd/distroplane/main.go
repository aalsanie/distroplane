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
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
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
	case "plan":
		return runPlanContext(ctx, args[1:], stdout, stderr)
	case "apply":
		return runExecutionContext(ctx, args[1:], stdout, stderr, false)
	case "status":
		return runStatus(args[1:], stdout, stderr)
	case "reconcile":
		return runExecutionContext(ctx, args[1:], stdout, stderr, true)
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
	fmt.Fprintln(w, "Usage:")
	fmt.Fprintln(w, "  distroplane <command> [options]")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Commands:")
	fmt.Fprintln(w, "  version [--json]                 print version information")
	fmt.Fprintln(w, "  validate --config PATH [--json]  validate release configuration")
	fmt.Fprintln(w, "  plan --config PATH [--json]      build and persist an immutable plan")
	fmt.Fprintln(w, "  apply --config PATH --plan PATH --journal PATH [--concurrency N] [--json]")
	fmt.Fprintln(w, "                                      execute or resume a plan")
	fmt.Fprintln(w, "  status --plan PATH --journal PATH [--json]")
	fmt.Fprintln(w, "                                      inspect journal-derived state")
	fmt.Fprintln(w, "  reconcile --config PATH --plan PATH --journal PATH [--concurrency N] [--json]")
	fmt.Fprintln(w, "                                      reconcile external state only")
	fmt.Fprintln(w, "  evidence --plan PATH --journal PATH [--output PATH] [--json]")
	fmt.Fprintln(w, "                                      export normalized release evidence")
	fmt.Fprintln(w, "  help                             print this help")
}
