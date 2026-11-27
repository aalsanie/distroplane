package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/aalsanie/distroplane/internal/fakeprovider"
	"github.com/aalsanie/distroplane/internal/protocol"
)

type timeoutProvider struct {
	fakeprovider.Provider
}

func recordPhase(phase string) {
	root := os.TempDir()
	file, err := os.OpenFile(filepath.Join(root, "provider-calls"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		os.Exit(71)
	}
	_, err = file.WriteString(phase + "\n")
	if closeErr := file.Close(); err != nil || closeErr != nil {
		os.Exit(71)
	}
	mode, err := os.ReadFile(filepath.Join(root, "provider-mode"))
	if err != nil {
		os.Exit(71)
	}
	for _, blocked := range strings.Fields(string(mode)) {
		if blocked == phase {
			time.Sleep(90 * time.Second)
			os.Exit(70)
		}
	}
}

func (p timeoutProvider) Describe(ctx context.Context, request protocol.DescribeRequest) (protocol.DescribeResponse, *protocol.ProviderError) {
	recordPhase("describe")
	return p.Provider.Describe(ctx, request)
}

func (p timeoutProvider) Plan(ctx context.Context, request protocol.PlanRequest) (protocol.PlanResponse, *protocol.ProviderError) {
	recordPhase("plan")
	response, err := p.Provider.Plan(ctx, request)
	if err == nil {
		for i := range response.Operations {
			response.Operations[i].TimeoutMillis = 5000
		}
	}
	return response, err
}

func (p timeoutProvider) Apply(ctx context.Context, request protocol.ApplyRequest) (protocol.ApplyResponse, *protocol.ProviderError) {
	recordPhase("apply")
	return p.Provider.Apply(ctx, request)
}

func (p timeoutProvider) Reconcile(ctx context.Context, request protocol.ReconcileRequest) (protocol.ReconcileResponse, *protocol.ProviderError) {
	recordPhase("reconcile")
	return p.Provider.Reconcile(ctx, request)
}

func main() {
	if err := protocol.ServeOnce(context.Background(), os.Stdin, os.Stdout, timeoutProvider{Provider: fakeprovider.Provider{Name: "fake"}}, protocol.NewCodec(protocol.DefaultMaxMessageBytes)); err != nil {
		os.Exit(71)
	}
}
