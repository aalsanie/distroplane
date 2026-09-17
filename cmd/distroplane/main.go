package main

import (
	"io"
	"os"

	"github.com/aalsanie/distroplane/internal/buildinfo"
	"github.com/aalsanie/distroplane/internal/cli"
)

var (
	arguments           = func() []string { return os.Args[1:] }
	stdout    io.Writer = os.Stdout
	stderr    io.Writer = os.Stderr
	exit                = os.Exit
)

func main() {
	exit(cli.Run(arguments(), stdout, stderr, buildinfo.Current()))
}
