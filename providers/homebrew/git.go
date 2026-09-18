package homebrew

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
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
		return gitResult(protocol.ResultPublished, "already-applied", payload, base.commit, pullRequestObservation{}), nil
	}
	if payload.Mode == "direct" {
		return p.applyDirect(ctx, payload, token, base)
	}
	return p.applyPullRequest(ctx, payload, token)
}

func (p Provider) reconcile(ctx context.Context, payload operationPayload, token string) (protocol.DistributionResult, *protocol.ProviderError) {
	base, providerErr := p.observeBranch(ctx, payload, payload.Branch, token)
	if providerErr != nil {
		return protocol.DistributionResult{}, providerErr
	}
	if base.exists && base.exact {
		state := "published"
		if payload.Mode == "direct" {
			state = "applied"
		}
		return gitResult(protocol.ResultPublished, state, payload, base.commit, pullRequestObservation{}), nil
	}
	if payload.Mode == "direct" {
		if base.exists {
			return gitResult(protocol.ResultRejected, "conflict", payload, base.commit, pullRequestObservation{}), nil
		}
		return gitResult(protocol.ResultWaitingExternal, "absent", payload, "", pullRequestObservation{}), nil
	}

	update, providerErr := p.observeBranch(ctx, payload, payload.UpdateBranch, token)
	if providerErr != nil {
		return protocol.DistributionResult{}, providerErr
	}
	if update.exists && !update.exact {
		return gitResult(protocol.ResultRejected, "branch-conflict", payload, update.commit, pullRequestObservation{}), nil
	}
	pr, providerErr := p.lookupPullRequest(ctx, payload, token)
	if providerErr != nil {
		return protocol.DistributionResult{}, providerErr
	}
	if pr.exists && pr.state == "closed" {
		return gitResult(protocol.ResultRejected, "pull-request-closed", payload, update.commit, pr), nil
	}
	if pr.exists {
		return gitResult(protocol.ResultWaitingExternal, "pull-request-open", payload, update.commit, pr), nil
	}
	if update.exists {
		return gitResult(protocol.ResultWaitingExternal, "branch-pushed", payload, update.commit, pr), nil
	}
	return gitResult(protocol.ResultWaitingExternal, "absent", payload, "", pr), nil
}

func (p Provider) applyDirect(ctx context.Context, payload operationPayload, token string, base branchObservation) (protocol.DistributionResult, *protocol.ProviderError) {
	if !base.exists {
		return protocol.DistributionResult{}, providerError(protocol.ErrorPermanentExternal, "Homebrew base branch does not exist", false)
	}
	commit, pushErr := p.commitAndPush(ctx, payload, payload.Branch, token)
	if pushErr == nil {
		return gitResult(protocol.ResultPublished, "applied", payload, commit, pullRequestObservation{}), nil
	}
	observed, providerErr := p.observeBranch(ctx, payload, payload.Branch, token)
	if providerErr == nil && observed.exists && observed.exact {
		return gitResult(protocol.ResultPublished, "applied", payload, observed.commit, pullRequestObservation{}), nil
	}
	if providerErr == nil && observed.exists {
		return gitResult(protocol.ResultRejected, "branch-conflict", payload, observed.commit, pullRequestObservation{}), nil
	}
	return protocol.DistributionResult{}, classifyGitPushError(pushErr, token)
}

func (p Provider) applyPullRequest(ctx context.Context, payload operationPayload, token string) (protocol.DistributionResult, *protocol.ProviderError) {
	update, providerErr := p.observeBranch(ctx, payload, payload.UpdateBranch, token)
	if providerErr != nil {
		return protocol.DistributionResult{}, providerErr
	}
	commit := update.commit
	if update.exists && !update.exact {
		return gitResult(protocol.ResultRejected, "branch-conflict", payload, update.commit, pullRequestObservation{}), nil
	}
	if !update.exists {
		var gitErr error
		commit, gitErr = p.commitAndPush(ctx, payload, payload.UpdateBranch, token)
		if gitErr != nil {
			observed, observeErr := p.observeBranch(ctx, payload, payload.UpdateBranch, token)
			if observeErr == nil && observed.exists && observed.exact {
				commit = observed.commit
			} else if observeErr == nil && observed.exists {
				return gitResult(protocol.ResultRejected, "branch-conflict", payload, observed.commit, pullRequestObservation{}), nil
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
		return gitResult(protocol.ResultRejected, "pull-request-closed", payload, commit, pr), nil
	}
	return gitResult(protocol.ResultWaitingExternal, "pull-request-open", payload, commit, pr), nil
}

func (p Provider) commitAndPush(ctx context.Context, payload operationPayload, destinationBranch, token string) (string, error) {
	work, err := os.MkdirTemp("", "distroplane-homebrew-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(work)
	env := gitEnvironment(payload.Repository, token)
	runner := p.gitRunner()
	if _, err := runner.Run(ctx, "", env, "clone", "--no-tags", "--single-branch", "--branch", payload.Branch, "--", payload.Repository, work); err != nil {
		return "", fmt.Errorf("clone Homebrew tap: %w", err)
	}
	if err := writeRepositoryFile(work, payload.Path, []byte(payload.Content)); err != nil {
		return "", err
	}
	if _, err := runner.Run(ctx, work, env, "add", "--", filepath.FromSlash(payload.Path)); err != nil {
		return "", fmt.Errorf("stage Homebrew manifest: %w", err)
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
		return "", fmt.Errorf("inspect Homebrew manifest change: %s: %w", sanitizeGitOutput(status.Stderr, token), err)
	}
	if _, err := runner.Run(ctx, work, env,
		"-c", "user.name="+payload.Commit.AuthorName,
		"-c", "user.email="+payload.Commit.AuthorEmail,
		"commit", "--no-gpg-sign", "-m", payload.Commit.Message,
	); err != nil {
		return "", fmt.Errorf("commit Homebrew manifest: %w", err)
	}
	current, err := runner.Run(ctx, work, env, "rev-parse", "HEAD")
	if err != nil {
		return "", fmt.Errorf("resolve Homebrew commit: %w", err)
	}
	commit := strings.TrimSpace(current.Stdout)
	push, err := runner.Run(ctx, work, env, "push", "origin", "HEAD:refs/heads/"+destinationBranch)
	if err != nil {
		return commit, fmt.Errorf("push Homebrew tap: %s: %w", sanitizeGitOutput(push.Stderr, token), err)
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
	work, err := os.MkdirTemp("", "distroplane-homebrew-observe-")
	if err != nil {
		return branchObservation{}, providerError(protocol.ErrorProviderInternal, "create Homebrew reconciliation workspace", false)
	}
	defer os.RemoveAll(work)
	clone, err := runner.Run(ctx, "", env, "clone", "--quiet", "--no-tags", "--depth", "1", "--single-branch", "--branch", branch, "--", payload.Repository, work)
	if err != nil {
		return branchObservation{}, classifyGitReadError(ctx, err, clone.Stderr, token)
	}
	raw, readErr := readRepositoryFile(work, payload.Path)
	if errors.Is(readErr, os.ErrNotExist) {
		return branchObservation{exists: true, commit: fields[0]}, nil
	}
	if readErr != nil {
		return branchObservation{}, providerError(protocol.ErrorPermanentExternal, readErr.Error(), false)
	}
	sum := sha256.Sum256(raw)
	return branchObservation{exists: true, exact: hex.EncodeToString(sum[:]) == payload.ContentSHA256, commit: fields[0]}, nil
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
		return providerError(protocol.ErrorAuthentication, "Homebrew repository authentication failed", false)
	}
	if strings.Contains(message, "permission denied") || strings.Contains(message, "403") {
		return providerError(protocol.ErrorAuthorization, "Homebrew repository authorization failed", false)
	}
	return providerError(protocol.ErrorTransientExternal, "Homebrew repository is unavailable", true)
}

func classifyGitPushError(err error, token string) *protocol.ProviderError {
	if errors.Is(err, exec.ErrNotFound) {
		return providerError(protocol.ErrorConfiguration, "git executable is unavailable", false)
	}
	message := strings.ToLower(sanitizeGitOutput(err.Error(), token))
	if strings.Contains(message, "authentication failed") || strings.Contains(message, "could not read username") || strings.Contains(message, "401") {
		return providerError(protocol.ErrorAuthentication, "Homebrew repository authentication failed", false)
	}
	if strings.Contains(message, "permission denied") || strings.Contains(message, "403") {
		return providerError(protocol.ErrorAuthorization, "Homebrew repository authorization failed", false)
	}
	return providerError(protocol.ErrorAmbiguousOutcome, "Homebrew repository update outcome is unknown; reconcile before retry", false)
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
		return fmt.Errorf("create Homebrew manifest directory: %w", err)
	}
	target, err = secureRepositoryPath(root, relative, false)
	if err != nil {
		return err
	}
	if err := os.WriteFile(target, content, 0o644); err != nil {
		return fmt.Errorf("write Homebrew manifest: %w", err)
	}
	return nil
}

func readRepositoryFile(root, relative string) ([]byte, error) {
	target, err := secureRepositoryPath(root, relative, false)
	if err != nil {
		return nil, err
	}
	raw, err := os.ReadFile(target)
	if err != nil {
		return nil, err
	}
	return raw, nil
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

func gitResult(state protocol.ResultState, providerState string, payload operationPayload, commit string, pr pullRequestObservation) protocol.DistributionResult {
	value := evidence{
		Repository: payload.Repository, Branch: payload.Branch, Path: payload.Path, Mode: payload.Mode,
		Commit: commit, ContentSHA256: payload.ContentSHA256, PublicationState: providerState,
		PullRequestURL: pr.url, PullRequestNumber: pr.number, PullRequestState: pr.state,
	}
	raw, _ := json.Marshal(value)
	return protocol.DistributionResult{State: state, ProviderState: providerState, Evidence: raw}
}
