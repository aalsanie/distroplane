package winget

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/aalsanie/distroplane/internal/protocol"
)

const maxGitOutput = 256 << 10

type GitRunner interface {
	Run(context.Context, string, []string, ...string) (gitOutput, error)
}

type gitOutput struct {
	Stdout string
	Stderr string
}

type execGitRunner struct{}

type gitExitError struct {
	Code int
	Err  error
}

func (e gitExitError) Error() string { return e.Err.Error() }
func (e gitExitError) Unwrap() error { return e.Err }

type limitedBuffer struct {
	bytes.Buffer
	remaining int
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	original := len(p)
	if b.remaining > 0 {
		if len(p) > b.remaining {
			p = p[:b.remaining]
		}
		_, _ = b.Buffer.Write(p)
		b.remaining -= len(p)
	}
	return original, nil
}

func (execGitRunner) Run(ctx context.Context, dir string, env []string, args ...string) (gitOutput, error) {
	command := exec.CommandContext(ctx, "git", args...)
	command.Dir = dir
	command.Env = env
	stdout := &limitedBuffer{remaining: maxGitOutput}
	stderr := &limitedBuffer{remaining: maxGitOutput}
	command.Stdout = stdout
	command.Stderr = stderr
	err := command.Run()
	result := gitOutput{Stdout: stdout.String(), Stderr: stderr.String()}
	if err == nil {
		return result, nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return result, gitExitError{Code: exitErr.ExitCode(), Err: err}
	}
	return result, err
}

type branchObservation struct {
	exists bool
	exact  bool
	commit string
}

func (p Provider) apply(ctx context.Context, payload operationPayload, token string) (protocol.DistributionResult, *protocol.ProviderError) {
	base, providerErr := p.observeBranch(ctx, payload, payload.Branch, token)
	if providerErr != nil {
		return protocol.DistributionResult{}, providerErr
	}
	if base.exists && base.exact {
		return wingetResult(protocol.ResultPublished, "already-published", payload, base.commit, pullRequestObservation{}, validationObservation{}), nil
	}

	update, providerErr := p.observeBranch(ctx, payload, payload.UpdateBranch, token)
	if providerErr != nil {
		return protocol.DistributionResult{}, providerErr
	}
	commit := update.commit
	if update.exists && !update.exact {
		return wingetResult(protocol.ResultRejected, "branch-conflict", payload, update.commit, pullRequestObservation{}, validationObservation{}), nil
	}
	if !update.exists {
		var gitErr error
		commit, gitErr = p.commitAndPush(ctx, payload, token)
		if gitErr != nil {
			observed, observeErr := p.observeBranch(ctx, payload, payload.UpdateBranch, token)
			if observeErr == nil && observed.exists && observed.exact {
				commit = observed.commit
			} else if observeErr == nil && observed.exists {
				return wingetResult(protocol.ResultRejected, "branch-conflict", payload, observed.commit, pullRequestObservation{}, validationObservation{}), nil
			} else {
				return protocol.DistributionResult{}, classifyGitPushError(gitErr, token)
			}
		}
	}

	pr, providerErr := p.lookupPullRequest(ctx, payload, token)
	if providerErr != nil {
		return protocol.DistributionResult{}, providerErr
	}
	if !pr.exists {
		pr, providerErr = p.submitPullRequest(ctx, payload, token)
		if providerErr != nil {
			return protocol.DistributionResult{}, providerErr
		}
	}
	if pr.state == "closed" {
		return wingetResult(protocol.ResultRejected, "pull-request-closed", payload, commit, pr, validationObservation{}), nil
	}
	return wingetResult(protocol.ResultWaitingExternal, "submitted", payload, commit, pr, validationObservation{}), nil
}

func (p Provider) reconcile(ctx context.Context, payload operationPayload, token string) (protocol.DistributionResult, *protocol.ProviderError) {
	base, providerErr := p.observeBranch(ctx, payload, payload.Branch, token)
	if providerErr != nil {
		return protocol.DistributionResult{}, providerErr
	}
	if base.exists && base.exact {
		return wingetResult(protocol.ResultPublished, "published", payload, base.commit, pullRequestObservation{}, validationObservation{state: "passed"}), nil
	}

	update, providerErr := p.observeBranch(ctx, payload, payload.UpdateBranch, token)
	if providerErr != nil {
		return protocol.DistributionResult{}, providerErr
	}
	if update.exists && !update.exact {
		return wingetResult(protocol.ResultRejected, "branch-conflict", payload, update.commit, pullRequestObservation{}, validationObservation{}), nil
	}
	pr, providerErr := p.lookupPullRequest(ctx, payload, token)
	if providerErr != nil {
		return protocol.DistributionResult{}, providerErr
	}
	if !pr.exists {
		if update.exists {
			return wingetResult(protocol.ResultWaitingExternal, "branch-pushed", payload, update.commit, pr, validationObservation{}), nil
		}
		return wingetResult(protocol.ResultWaitingExternal, "absent", payload, "", pr, validationObservation{}), nil
	}
	if pr.state == "merged" {
		return wingetResult(protocol.ResultPublished, "merged", payload, update.commit, pr, validationObservation{state: "passed"}), nil
	}
	if pr.state == "closed" {
		return wingetResult(protocol.ResultRejected, "pull-request-closed", payload, update.commit, pr, validationObservation{}), nil
	}
	validation, providerErr := p.lookupValidation(ctx, payload, pr, token)
	if providerErr != nil {
		return protocol.DistributionResult{}, providerErr
	}
	if validation.state == "failed" {
		return wingetResult(protocol.ResultRejected, "validation-failed", payload, update.commit, pr, validation), nil
	}
	if validation.state == "passed" {
		return wingetResult(protocol.ResultWaitingExternal, "review-pending", payload, update.commit, pr, validation), nil
	}
	return wingetResult(protocol.ResultWaitingExternal, "validation-pending", payload, update.commit, pr, validation), nil
}

func (p Provider) commitAndPush(ctx context.Context, payload operationPayload, token string) (string, error) {
	work, err := os.MkdirTemp("", "distroplane-winget-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(work)
	env := gitEnvironment(payload.Repository, token)
	runner := p.gitRunner()
	if _, err := runner.Run(ctx, "", env, "clone", "--no-tags", "--single-branch", "--branch", payload.Branch, "--", payload.Repository, work); err != nil {
		return "", fmt.Errorf("clone WinGet submission repository: %w", err)
	}
	paths := make([]string, 0, len(payload.Files))
	for _, file := range payload.Files {
		if err := writeRepositoryFile(work, file.Path, []byte(file.Content)); err != nil {
			return "", err
		}
		paths = append(paths, filepath.FromSlash(file.Path))
	}
	args := append([]string{"add", "--"}, paths...)
	if _, err := runner.Run(ctx, work, env, args...); err != nil {
		return "", fmt.Errorf("stage WinGet manifests: %w", err)
	}
	status, err := runner.Run(ctx, work, env, "diff", "--cached", "--quiet", "--exit-code")
	if err == nil {
		current, revErr := runner.Run(ctx, work, env, "rev-parse", "HEAD")
		if revErr != nil {
			return "", revErr
		}
		return strings.TrimSpace(current.Stdout), nil
	}
	var exitErr gitExitError
	if !errors.As(err, &exitErr) || exitErr.Code != 1 {
		return "", fmt.Errorf("inspect WinGet manifest change: %s: %w", sanitizeGitOutput(status.Stderr, token), err)
	}
	if _, err := runner.Run(ctx, work, env,
		"-c", "user.name="+payload.Commit.AuthorName,
		"-c", "user.email="+payload.Commit.AuthorEmail,
		"commit", "--no-gpg-sign", "-m", payload.Commit.Message,
	); err != nil {
		return "", fmt.Errorf("commit WinGet manifests: %w", err)
	}
	current, err := runner.Run(ctx, work, env, "rev-parse", "HEAD")
	if err != nil {
		return "", fmt.Errorf("resolve WinGet commit: %w", err)
	}
	commit := strings.TrimSpace(current.Stdout)
	push, err := runner.Run(ctx, work, env, "push", "origin", "HEAD:refs/heads/"+payload.UpdateBranch)
	if err != nil {
		return commit, fmt.Errorf("push WinGet submission branch: %s: %w", sanitizeGitOutput(push.Stderr, token), err)
	}
	return commit, nil
}

func (p Provider) observeBranch(ctx context.Context, payload operationPayload, branch, token string) (branchObservation, *protocol.ProviderError) {
	env := gitEnvironment(payload.Repository, token)
	runner := p.gitRunner()
	output, err := runner.Run(ctx, "", env, "ls-remote", "--exit-code", "--heads", payload.Repository, "refs/heads/"+branch)
	if err != nil {
		var exitErr gitExitError
		if errors.As(err, &exitErr) && exitErr.Code == 2 {
			return branchObservation{}, nil
		}
		return branchObservation{}, classifyGitReadError(ctx, err, output.Stderr, token)
	}
	fields := strings.Fields(output.Stdout)
	if len(fields) < 1 {
		return branchObservation{}, providerError(protocol.ErrorPermanentExternal, "git returned malformed branch metadata", false)
	}
	work, err := os.MkdirTemp("", "distroplane-winget-observe-")
	if err != nil {
		return branchObservation{}, providerError(protocol.ErrorProviderInternal, "create WinGet reconciliation workspace", false)
	}
	defer os.RemoveAll(work)
	clone, err := runner.Run(ctx, "", env, "clone", "--quiet", "--no-tags", "--depth", "1", "--single-branch", "--branch", branch, "--", payload.Repository, work)
	if err != nil {
		return branchObservation{}, classifyGitReadError(ctx, err, clone.Stderr, token)
	}
	for _, file := range payload.Files {
		raw, readErr := readRepositoryFile(work, file.Path)
		if errors.Is(readErr, os.ErrNotExist) {
			return branchObservation{exists: true, commit: fields[0]}, nil
		}
		if readErr != nil {
			return branchObservation{}, providerError(protocol.ErrorPermanentExternal, "read WinGet manifest during reconciliation", false)
		}
		if string(raw) != file.Content {
			return branchObservation{exists: true, commit: fields[0]}, nil
		}
	}
	return branchObservation{exists: true, exact: true, commit: fields[0]}, nil
}

func (p Provider) gitRunner() GitRunner {
	if p.Git != nil {
		return p.Git
	}
	return execGitRunner{}
}

func gitEnvironment(repository, token string) []string {
	names := []string{"PATH", "SystemRoot", "WINDIR", "COMSPEC", "PATHEXT", "TEMP", "TMP", "TMPDIR", "SSL_CERT_FILE", "SSL_CERT_DIR"}
	env := make([]string, 0, len(names)+10)
	for _, name := range names {
		if value, ok := os.LookupEnv(name); ok && value != "" {
			env = append(env, name+"="+value)
		}
	}
	env = append(env,
		"GIT_TERMINAL_PROMPT=0",
		"GCM_INTERACTIVE=Never",
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_CONFIG_GLOBAL="+os.DevNull,
		"GIT_CONFIG_SYSTEM="+os.DevNull,
	)
	if runtime.GOOS == "windows" && os.Getenv("USERPROFILE") != "" {
		env = append(env, "USERPROFILE="+os.Getenv("USERPROFILE"))
	}
	if token != "" {
		parsed, err := url.Parse(repository)
		if err == nil && (parsed.Scheme == "http" || parsed.Scheme == "https") && parsed.Host != "" {
			scope := strings.TrimRight(parsed.String(), "/") + "/"
			env = append(env,
				"GIT_CONFIG_COUNT=1",
				"GIT_CONFIG_KEY_0=http."+scope+".extraHeader",
				"GIT_CONFIG_VALUE_0="+gitAuthorization(token),
			)
		}
	}
	return env
}

func classifyGitReadError(ctx context.Context, err error, stderr, token string) *protocol.ProviderError {
	if errors.Is(err, exec.ErrNotFound) {
		return providerError(protocol.ErrorConfiguration, "git executable is unavailable", false)
	}
	if errors.Is(ctx.Err(), context.Canceled) {
		return providerError(protocol.ErrorCancelled, "git operation was cancelled", false)
	}
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return providerError(protocol.ErrorTimeout, "git operation timed out", true)
	}
	message := strings.ToLower(sanitizeGitOutput(stderr, token))
	if strings.Contains(message, "authentication failed") || strings.Contains(message, "could not read username") || strings.Contains(message, "401") {
		return providerError(protocol.ErrorAuthentication, "WinGet submission repository authentication failed", false)
	}
	if strings.Contains(message, "permission denied") || strings.Contains(message, "403") {
		return providerError(protocol.ErrorAuthorization, "WinGet submission repository authorization failed", false)
	}
	return providerError(protocol.ErrorTransientExternal, "WinGet submission repository is unavailable", true)
}

func classifyGitPushError(err error, token string) *protocol.ProviderError {
	if errors.Is(err, exec.ErrNotFound) {
		return providerError(protocol.ErrorConfiguration, "git executable is unavailable", false)
	}
	message := strings.ToLower(sanitizeGitOutput(err.Error(), token))
	if strings.Contains(message, "authentication failed") || strings.Contains(message, "could not read username") || strings.Contains(message, "401") {
		return providerError(protocol.ErrorAuthentication, "WinGet submission repository authentication failed", false)
	}
	if strings.Contains(message, "permission denied") || strings.Contains(message, "403") {
		return providerError(protocol.ErrorAuthorization, "WinGet submission repository authorization failed", false)
	}
	return providerError(protocol.ErrorAmbiguousOutcome, "WinGet submission outcome is unknown; reconcile before retry", false)
}

func gitAuthorization(token string) string {
	encoded := base64.StdEncoding.EncodeToString([]byte("x-access-token:" + token))
	return "Authorization: Basic " + encoded
}

func sanitizeGitOutput(value, token string) string {
	value = strings.TrimSpace(value)
	if token != "" {
		value = strings.ReplaceAll(value, token, "[REDACTED]")
		value = strings.ReplaceAll(value, gitAuthorization(token), "Authorization: [REDACTED]")
	}
	if len(value) > 2048 {
		value = value[:2048]
	}
	return value
}

func writeRepositoryFile(root, relative string, content []byte) error {
	target, err := secureRepositoryPath(root, relative, true)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return fmt.Errorf("create WinGet manifest directory: %w", err)
	}
	target, err = secureRepositoryPath(root, relative, false)
	if err != nil {
		return err
	}
	if err := os.WriteFile(target, content, 0o644); err != nil {
		return fmt.Errorf("write WinGet manifest: %w", err)
	}
	return nil
}

func readRepositoryFile(root, relative string) ([]byte, error) {
	target, err := secureRepositoryPath(root, relative, false)
	if err != nil {
		return nil, err
	}
	return os.ReadFile(target)
}

func secureRepositoryPath(root, relative string, allowMissingParents bool) (string, error) {
	normalized, err := normalizeRepoPath(relative)
	if err != nil {
		return "", err
	}
	current := root
	parts := strings.Split(normalized, "/")
	for index, part := range parts {
		current = filepath.Join(current, part)
		info, statErr := os.Lstat(current)
		if statErr != nil {
			if errors.Is(statErr, os.ErrNotExist) && (allowMissingParents || index == len(parts)-1) {
				continue
			}
			return "", statErr
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return "", fmt.Errorf("repository path %q traverses a symbolic link", relative)
		}
		if index < len(parts)-1 && !info.IsDir() {
			return "", fmt.Errorf("repository path %q traverses a non-directory", relative)
		}
	}
	return filepath.Join(root, filepath.FromSlash(normalized)), nil
}

func wingetResult(state protocol.ResultState, providerState string, payload operationPayload, commit string, pr pullRequestObservation, validation validationObservation) protocol.DistributionResult {
	value := evidence{
		Repository: payload.PullRequest.Repository, BaseBranch: payload.Branch, UpdateBranch: payload.UpdateBranch,
		ManifestTreeSHA: payload.TreeSHA256, Commit: commit, PackageID: payload.PackageID, PackageVersion: payload.PackageVersion,
		PublicationState: providerState, PullRequestURL: pr.url, PullRequestNumber: pr.number, PullRequestState: pr.state,
		ValidationState: validation.state, ValidationCheck: validation.failedCheck,
	}
	raw, _ := json.Marshal(value)
	return protocol.DistributionResult{State: state, ProviderState: providerState, Evidence: raw}
}
