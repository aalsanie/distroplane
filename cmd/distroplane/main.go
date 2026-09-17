package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
)

var (
	version   = "0.0.0-dev"
	commit    = "unknown"
	buildDate = "unknown"
)

type versionInfo struct {
	Version   string `json:"version"`
	Commit    string `json:"commit"`
	BuildDate string `json:"buildDate"`
}

func main() {
	os.Exit(execute(os.Args[1:], os.Stdout, os.Stderr))
}

func execute(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		printHelp(stdout)
		return 0
	}

	switch args[0] {
	case "help", "-h", "--help":
		if len(args) != 1 {
			fmt.Fprintln(stderr, "help does not accept arguments")
			return 2
		}
		printHelp(stdout)
		return 0
	case "version":
		return runVersion(args[1:], stdout, stderr)
	default:
		fmt.Fprintf(stderr, "unknown command %q\n", args[0])
		fmt.Fprintln(stderr, "run 'distroplane help' for usage")
		return 2
	}
}

func runVersion(args []string, stdout, stderr io.Writer) int {
	info := versionInfo{Version: version, Commit: commit, BuildDate: buildDate}

	switch len(args) {
	case 0:
		fmt.Fprintf(stdout, "distroplane %s (commit %s, built %s)\n", info.Version, info.Commit, info.BuildDate)
		return 0
	case 1:
		if args[0] != "--json" {
			fmt.Fprintf(stderr, "unknown version option %q\n", args[0])
			return 2
		}
		data, err := json.Marshal(info)
		if err != nil {
			fmt.Fprintf(stderr, "encode version information: %v\n", err)
			return 1
		}
		fmt.Fprintln(stdout, string(data))
		return 0
	default:
		fmt.Fprintln(stderr, "version accepts only the optional --json flag")
		return 2
	}
}

func printHelp(w io.Writer) {
	fmt.Fprintln(w, "Distroplane — release distribution control plane")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Usage:")
	fmt.Fprintln(w, "  distroplane <command>")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Commands:")
	fmt.Fprintln(w, "  version [--json]  print version information")
	fmt.Fprintln(w, "  help              print this help")
}
