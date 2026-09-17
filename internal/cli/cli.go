// Package cli implements the command-line boundary for Distroplane.
package cli

import (
	"fmt"
	"io"

	"github.com/aalsanie/distroplane/internal/buildinfo"
)

const (
	// ExitOK indicates successful command execution.
	ExitOK = 0
	// ExitUsage indicates invalid command-line input.
	ExitUsage = 2
)

const usage = `Distroplane — the release distribution control plane.

Usage:
  distroplane help
  distroplane version
`

// Run executes the CLI command described by args.
func Run(args []string, stdout, stderr io.Writer, info buildinfo.Info) int {
	if len(args) == 0 {
		_, _ = io.WriteString(stdout, usage)
		return ExitOK
	}

	switch args[0] {
	case "help", "-h", "--help":
		if len(args) != 1 {
			return usageError(stderr, "help does not accept arguments")
		}
		_, _ = io.WriteString(stdout, usage)
		return ExitOK
	case "version", "--version":
		if len(args) != 1 {
			return usageError(stderr, "version does not accept arguments")
		}
		_, _ = fmt.Fprintln(stdout, info.String())
		return ExitOK
	default:
		return usageError(stderr, fmt.Sprintf("unknown command %q", args[0]))
	}
}

func usageError(stderr io.Writer, message string) int {
	_, _ = fmt.Fprintf(stderr, "error: %s\n\n%s", message, usage)
	return ExitUsage
}
