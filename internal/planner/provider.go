package planner

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
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
	lookPath   func(string) (string, error)
	abs        func(string) (string, error)
	stat       func(string) (os.FileInfo, error)
	lstat      func(string) (os.FileInfo, error)
	executable func() (string, error)
	evalLinks  func(string) (string, error)
}

func newExecutableResolver() executableResolver {
	return executableResolver{
		lookPath:   exec.LookPath,
		abs:        filepath.Abs,
		stat:       os.Stat,
		lstat:      os.Lstat,
		executable: os.Executable,
		evalLinks:  filepath.EvalSymlinks,
	}
}

// ResolveExecutable resolves relative explicit paths against the configuration
// directory. Automatic provider discovery is relative to the running CLI binary.
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
	if !validProviderExecutableName(name) {
		return "", fmt.Errorf("invalid provider executable name %q", name)
	}
	if configuredExecutable != "" {
		return r.resolveConfigured(configuredExecutable)
	}
	candidate := providerExecutablePrefix + name
	if runtime.GOOS == "windows" {
		candidate += ".exe"
	}
	if resolved, found, err := r.trustedCandidate(candidate); found || err != nil {
		return resolved, err
	}
	return r.resolveFromPath(candidate)
}

func validProviderExecutableName(name string) bool {
	if name == "" || len(name) > 128 {
		return false
	}
	for i, c := range name {
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') ||
			(i > 0 && (c == '-' || c == '_' || c == '.')) {
			continue
		}
		return false
	}
	return true
}

func (r executableResolver) resolveConfigured(candidate string) (string, error) {
	if !filepath.IsAbs(candidate) && !strings.ContainsAny(candidate, `/\`) {
		return r.resolveFromPath(candidate)
	}
	return r.validateExecutable(candidate, false)
}

func (r executableResolver) resolveFromPath(name string) (string, error) {
	lookup := r.lookPath
	if lookup == nil {
		lookup = exec.LookPath
	}
	path, err := lookup(name)
	if err != nil {
		return "", fmt.Errorf("resolve provider executable %q: %w", name, err)
	}
	return r.validateExecutable(path, false)
}

func (r executableResolver) validateExecutable(path string, trusted bool) (string, error) {
	abs := r.abs
	if abs == nil {
		abs = filepath.Abs
	}
	full, err := abs(path)
	if err != nil {
		return "", fmt.Errorf("resolve provider executable %q: %w", path, err)
	}
	full = filepath.Clean(full)
	lstat := r.lstat
	if lstat == nil {
		lstat = os.Lstat
	}
	if trusted {
		link, err := lstat(full)
		if err != nil {
			return "", fmt.Errorf("inspect installed provider %q: %w", full, err)
		}
		if !link.Mode().IsRegular() {
			return "", fmt.Errorf("installed provider %q must be a regular file, not a symlink or special file", full)
		}
		if runtime.GOOS != "windows" && link.Mode().Perm()&0022 != 0 {
			return "", fmt.Errorf("installed provider %q must not be group/world writable", full)
		}
	}
	eval := r.evalLinks
	if eval == nil {
		eval = filepath.EvalSymlinks
	}
	canonical, err := eval(full)
	if err != nil {
		return "", fmt.Errorf("resolve provider executable %q: %w", full, err)
	}
	canonical, err = abs(canonical)
	if err != nil {
		return "", fmt.Errorf("canonicalize provider executable %q: %w", full, err)
	}
	info := r.stat
	if info == nil {
		info = os.Stat
	}
	file, err := info(canonical)
	if err != nil {
		return "", fmt.Errorf("inspect provider executable %q: %w", canonical, err)
	}
	if !file.Mode().IsRegular() {
		return "", fmt.Errorf("provider executable %q is not a regular file", canonical)
	}
	if runtime.GOOS != "windows" && file.Mode().Perm()&0111 == 0 {
		return "", fmt.Errorf("provider executable %q is not executable", canonical)
	}
	if trusted && runtime.GOOS != "windows" && file.Mode().Perm()&0022 != 0 {
		return "", fmt.Errorf("installed provider %q must not be group/world writable", canonical)
	}
	return filepath.Clean(canonical), nil
}

// A bundled provider must live in a trusted directory associated with the
// actual CLI executable; never search relative to the working directory or
// the configuration file. An unsafe installed provider is an error, not a
// reason to fall back to an attacker-controlled PATH entry.
func (r executableResolver) trustedCandidate(name string) (string, bool, error) {
	executable := r.executable
	if executable == nil {
		executable = os.Executable
	}
	cli, err := executable()
	if err != nil {
		return "", false, fmt.Errorf("locate Distroplane executable: %w", err)
	}
	abs := r.abs
	if abs == nil {
		abs = filepath.Abs
	}
	cli, err = abs(cli)
	if err != nil {
		return "", false, fmt.Errorf("canonicalize Distroplane executable: %w", err)
	}
	eval := r.evalLinks
	if eval == nil {
		eval = filepath.EvalSymlinks
	}
	cli, err = eval(cli)
	if err != nil {
		return "", false, fmt.Errorf("resolve Distroplane executable: %w", err)
	}
	base := filepath.Base(cli)
	if base != "distroplane" && base != "distroplane.exe" {
		return "", false, nil
	}
	bin := filepath.Dir(cli)
	dirs := []string{bin}
	if filepath.Base(bin) == "bin" {
		dirs = append([]string{filepath.Join(filepath.Dir(bin), "libexec")}, dirs...)
	}
	lstat := r.lstat
	if lstat == nil {
		lstat = os.Lstat
	}
	for _, dir := range dirs {
		candidate := filepath.Join(dir, name)
		_, err := lstat(candidate)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return "", true, fmt.Errorf("inspect installed provider %q: %w", candidate, err)
		}
		if err := r.validateTrustedDirectory(dir); err != nil {
			return "", true, err
		}
		resolved, err := r.validateExecutable(candidate, true)
		return resolved, true, err
	}
	return "", false, nil
}

func (r executableResolver) validateTrustedDirectory(dir string) error {
	lstat := r.lstat
	if lstat == nil {
		lstat = os.Lstat
	}
	info, err := lstat(dir)
	if err != nil {
		return fmt.Errorf("inspect provider directory %q: %w", dir, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("provider directory %q is not a real directory", dir)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0022 != 0 {
		return fmt.Errorf("provider directory %q must not be group/world writable", dir)
	}
	return nil
}
