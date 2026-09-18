package planner

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/aalsanie/distroplane/internal/protocol"
)

const providerExecutablePrefix = "distroplane-provider-"

type ProviderClient interface {
	Describe(context.Context, Endpoint) (protocol.DescribeResponse, error)
	Plan(context.Context, Endpoint, protocol.PlanRequest) (protocol.PlanResponse, error)
}

type Endpoint struct {
	Name       string
	Executable string
}

type ProviderResolver interface {
	Resolve(name, configuredExecutable string) (string, error)
}

type executableResolver struct {
	lookPath func(string) (string, error)
	abs      func(string) (string, error)
	stat     func(string) (os.FileInfo, error)
}

func newExecutableResolver() executableResolver {
	return executableResolver{lookPath: exec.LookPath, abs: filepath.Abs, stat: os.Stat}
}

func ResolveExecutable(baseDir, name, configuredExecutable string) (string, error) {
	configured := configuredExecutable
	if configured != "" && !filepath.IsAbs(configured) && strings.ContainsAny(configured, `/\`) {
		if strings.TrimSpace(baseDir) == "" {
			return "", fmt.Errorf("base directory must not be empty for relative provider executable")
		}
		configured = filepath.Join(baseDir, configured)
	}
	return newExecutableResolver().Resolve(name, configured)
}

func (r executableResolver) Resolve(name, configuredExecutable string) (string, error) {
	if strings.TrimSpace(name) == "" {
		return "", fmt.Errorf("provider name must not be empty")
	}
	candidate := configuredExecutable
	if candidate == "" {
		candidate = providerExecutablePrefix + name
	} else if filepath.IsAbs(candidate) || strings.ContainsAny(candidate, `/\`) {
		absolute, err := r.abs(candidate)
		if err != nil {
			return "", fmt.Errorf("resolve provider executable %q: %w", candidate, err)
		}
		info, err := r.stat(absolute)
		if err != nil {
			return "", fmt.Errorf("resolve provider executable %q: %w", absolute, err)
		}
		if info.IsDir() {
			return "", fmt.Errorf("provider executable %q is a directory", absolute)
		}
	}
	resolved, err := r.lookPath(candidate)
	if err != nil {
		return "", fmt.Errorf("resolve provider executable %q: %w", candidate, err)
	}
	absolute, err := r.abs(resolved)
	if err != nil {
		return "", fmt.Errorf("resolve provider executable %q: %w", resolved, err)
	}
	info, err := r.stat(absolute)
	if err != nil {
		return "", fmt.Errorf("resolve provider executable %q: %w", absolute, err)
	}
	if info.IsDir() {
		return "", fmt.Errorf("provider executable %q is a directory", absolute)
	}
	return filepath.Clean(absolute), nil
}
