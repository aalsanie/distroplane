package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/aalsanie/distroplane/internal/protocol"
	wingetprovider "github.com/aalsanie/distroplane/providers/winget"
)

var version = wingetprovider.DefaultVersion

type process struct {
	input       io.Reader
	output      io.Writer
	diagnostics io.Writer
}

func main() {
	os.Exit(execute(context.Background(), process{input: os.Stdin, output: os.Stdout, diagnostics: os.Stderr}))
}

func execute(ctx context.Context, streams process) int {
	providerVersion := strings.TrimSpace(version)
	if providerVersion == "" {
		providerVersion = wingetprovider.DefaultVersion
	}
	return run(ctx, streams.input, streams.output, streams.diagnostics, commandProvider(providerVersion))
}

func commandProvider(providerVersion string) wingetprovider.Provider {
	return wingetprovider.Provider{Version: providerVersion}
}

func run(ctx context.Context, input io.Reader, output, diagnostics io.Writer, provider wingetprovider.Provider) int {
	if err := protocol.ServeOnce(ctx, input, output, provider, protocol.NewCodec(protocol.DefaultMaxMessageBytes)); err != nil {
		_, _ = fmt.Fprintln(diagnostics, err)
		return 1
	}
	return 0
}
