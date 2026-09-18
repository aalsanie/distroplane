package homebrew

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aalsanie/distroplane/internal/protocol"
)

func TestValidationHelpers(t *testing.T) {
	if _, err := parseConfiguration(json.RawMessage(`{`)); err == nil {
		t.Fatal("malformed configuration accepted")
	}
	if err := decodeStrict([]byte(`{} {}`), &map[string]any{}); err == nil {
		t.Fatal("trailing JSON accepted")
	}
	if err := decodeStrict([]byte(`{} x`), &map[string]any{}); err == nil {
		t.Fatal("invalid trailing JSON accepted")
	}

	valid := formulaConfiguration("/tmp/tap.git", "direct")
	cases := map[string]func(*configuration){
		"artifact":            func(c *configuration) { c.Artifact = "" },
		"repository":          func(c *configuration) { c.Repository = "-bad" },
		"branch":              func(c *configuration) { c.Branch = "bad..branch" },
		"path":                func(c *configuration) { c.Path = "../escape" },
		"mode":                func(c *configuration) { c.Mode = "other" },
		"direct update":       func(c *configuration) { c.UpdateBranch = "update" },
		"credential":          func(c *configuration) { c.Authentication.Credential = " bad " },
		"commit":              func(c *configuration) { c.Commit.AuthorEmail = "bad address" },
		"pull request direct": func(c *configuration) { c.PullRequest.Repository = "owner/repo" },
		"manifest":            func(c *configuration) { c.Manifest.Type = "file" },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			cfg := valid
			mutate(&cfg)
			raw, _ := json.Marshal(cfg)
			if _, err := parseConfiguration(raw); err == nil {
				t.Fatal("invalid configuration accepted")
			}
		})
	}

	pr := valid
	pr.Mode = "pull-request"
	raw, _ := json.Marshal(pr)
	if _, err := parseConfiguration(raw); err == nil || !strings.Contains(err.Error(), "authentication") {
		t.Fatalf("err=%v", err)
	}
}

func TestManifestValidationAndEscaping(t *testing.T) {
	artifact := artifactFixture()
	formula := manifestConfig{
		Type: "formula", Name: "Demo", Description: `Demo #{system("bad")}`, Homepage: "https://example.test",
		URL: "https://example.test/demo.tar.gz", Version: "1.0.0", Binary: "demo", License: "MIT",
	}
	content, err := renderManifest(formula, artifact)
	if err != nil || !strings.Contains(content, `\#{system`) {
		t.Fatalf("content=%q err=%v", content, err)
	}
	for name, mutate := range map[string]func(*manifestConfig){
		"description":    func(c *manifestConfig) { c.Description = "bad\nvalue" },
		"homepage":       func(c *manifestConfig) { c.Homepage = "://bad" },
		"url":            func(c *manifestConfig) { c.URL = "ftp://example.test/a" },
		"formula class":  func(c *manifestConfig) { c.Name = "bad" },
		"formula binary": func(c *manifestConfig) { c.Binary = "bin/demo" },
		"formula cask":   func(c *manifestConfig) { c.InstallKind = "app" },
	} {
		t.Run(name, func(t *testing.T) {
			value := formula
			mutate(&value)
			if err := validateManifest(value); err == nil {
				t.Fatal("invalid manifest accepted")
			}
		})
	}
	cask := manifestConfig{Type: "cask", Name: "demo", Description: "Demo", Homepage: "https://example.test", URL: "https://example.test/a.zip", Version: "1", InstallKind: "app", InstallSource: "Demo.app"}
	if err := validateManifest(cask); err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*manifestConfig){
		"token":         func(c *manifestConfig) { c.Name = "Bad Token" },
		"kind":          func(c *manifestConfig) { c.InstallKind = "script" },
		"source":        func(c *manifestConfig) { c.InstallSource = "" },
		"formula field": func(c *manifestConfig) { c.License = "MIT" },
	} {
		t.Run(name, func(t *testing.T) {
			value := cask
			mutate(&value)
			if err := validateManifest(value); err == nil {
				t.Fatal("invalid cask accepted")
			}
		})
	}
	badArtifact := artifact
	badArtifact.Digest = "md5:bad"
	if _, err := renderManifest(formula, badArtifact); err == nil {
		t.Fatal("non-SHA artifact accepted")
	}
}

func artifactFixture() protocol.Artifact {
	return protocol.Artifact{Name: "archive", Digest: "sha256:" + strings.Repeat("a", 64), Size: 123}
}

func TestRepositoryAndReferenceValidation(t *testing.T) {
	for _, value := range []string{"", "-repo", "https://user@example.test/repo", "bad\nrepo"} {
		if validateRepository(value) == nil {
			t.Fatalf("repository accepted: %q", value)
		}
	}
	for _, value := range []string{"repo", "/tmp/repo", `C:\\repo`, "https://example.test/repo"} {
		if err := validateRepository(value); err != nil {
			t.Fatalf("repository=%q err=%v", value, err)
		}
	}
	for _, value := range []string{"", "../a", "/a", `a\\b`, "a\n"} {
		if _, err := normalizeRepoPath(value); err == nil {
			t.Fatalf("path accepted: %q", value)
		}
	}
	if value, err := normalizeRepoPath("Formula/../Casks/demo.rb"); err != nil || value != "Casks/demo.rb" {
		t.Fatalf("value=%q err=%v", value, err)
	}
	for _, value := range []string{"", "-bad", ".bad", "bad..ref", "bad@{x", "bad.lock", "bad ref", "bad/"} {
		if validBranch(value) {
			t.Fatalf("branch accepted: %q", value)
		}
	}
	if !validBranch("feature/release-1") {
		t.Fatal("valid branch rejected")
	}
	if validCredentialRef("") || validCredentialRef("bad ref") || validCredentialRef(" bad") || !validCredentialRef("credential.ref") {
		t.Fatal("credential reference validation mismatch")
	}
	branch := deterministicUpdateBranch("Release 1.2.3 / Candidate", []byte{1, 2, 3, 4, 5, 6, 7})
	if !strings.HasPrefix(branch, "distroplane/release-1-2-3---candidate-") {
		t.Fatalf("branch=%q", branch)
	}
}

func TestPullRequestValidation(t *testing.T) {
	valid := pullRequestConfig{Repository: "owner/repo"}
	if err := validatePullRequest(&valid); err != nil || valid.API != "https://api.github.com" {
		t.Fatalf("cfg=%+v err=%v", valid, err)
	}
	for name, cfg := range map[string]pullRequestConfig{
		"repository": {Repository: "owner repo"},
		"api":        {Repository: "owner/repo", API: "ftp://example.test"},
		"http":       {Repository: "owner/repo", API: "http://example.test"},
		"user":       {Repository: "owner/repo", API: "https://user@example.test"},
		"query":      {Repository: "owner/repo", API: "https://example.test?q=1"},
		"title":      {Repository: "owner/repo", Title: "bad\n"},
		"body":       {Repository: "owner/repo", Body: "bad\x00"},
	} {
		t.Run(name, func(t *testing.T) {
			if err := validatePullRequest(&cfg); err == nil {
				t.Fatal("invalid pull request accepted")
			}
		})
	}
	httpCfg := pullRequestConfig{Repository: "owner/repo", API: "http://example.test/", AllowInsecureHTTP: true}
	if err := validatePullRequest(&httpCfg); err != nil || httpCfg.API != "http://example.test" {
		t.Fatalf("cfg=%+v err=%v", httpCfg, err)
	}
}

func TestCommitAndPayloadValidation(t *testing.T) {
	for _, cfg := range []commitConfig{{Message: "bad\n"}, {AuthorName: "bad\r"}, {AuthorEmail: "bad"}} {
		if validateCommit(cfg) == nil {
			t.Fatalf("commit accepted: %+v", cfg)
		}
	}
	payload := operationPayload{
		Repository: "/tmp/repo", Branch: "main", Path: "Formula/demo.rb", Mode: "direct",
		Content: "x\n", Commit: commitConfig{Message: "m", AuthorName: "a", AuthorEmail: "a@b"},
		Artifact: "archive", ArtifactDigest: "sha256:" + strings.Repeat("a", 64), ArtifactSize: 1,
	}
	sum := sha256Hex([]byte(payload.Content))
	payload.ContentSHA256 = sum
	raw, _ := json.Marshal(payload)
	if _, err := parsePayload(raw); err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*operationPayload){
		"missing":   func(p *operationPayload) { p.Repository = "" },
		"mode":      func(p *operationPayload) { p.Mode = "other" },
		"branch":    func(p *operationPayload) { p.Branch = "bad branch" },
		"content":   func(p *operationPayload) { p.Content = "changed" },
		"pr branch": func(p *operationPayload) { p.Mode = "pull-request"; p.UpdateBranch = "" },
		"path":      func(p *operationPayload) { p.Path = "../bad" },
	} {
		t.Run(name, func(t *testing.T) {
			value := payload
			mutate(&value)
			raw, _ := json.Marshal(value)
			if _, err := parsePayload(raw); err == nil {
				t.Fatal("invalid payload accepted")
			}
		})
	}
	if _, err := parsePayload(json.RawMessage(`{"unknown":true}`)); err == nil {
		t.Fatal("unknown payload accepted")
	}
}

func sha256Hex(raw []byte) string {
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func TestPlanAndExecutionErrorSurfaces(t *testing.T) {
	provider := Provider{}
	badPlan := protocol.PlanRequest{}
	if _, providerErr := provider.Plan(context.Background(), badPlan); providerErr == nil || providerErr.Code != protocol.ErrorProtocol {
		t.Fatalf("err=%v", providerErr)
	}
	req := planRequest(t, formulaConfiguration("/tmp/repo", "direct"))
	req.Release.Artifacts[0].Name = "other"
	if _, providerErr := provider.Plan(context.Background(), req); providerErr == nil || providerErr.Code != protocol.ErrorConfiguration {
		t.Fatalf("err=%v", providerErr)
	}
	if _, providerErr := provider.Apply(context.Background(), protocol.ApplyRequest{}); providerErr == nil || providerErr.Code != protocol.ErrorProtocol {
		t.Fatalf("err=%v", providerErr)
	}
	if _, providerErr := provider.Reconcile(context.Background(), protocol.ReconcileRequest{}); providerErr == nil || providerErr.Code != protocol.ErrorProtocol {
		t.Fatalf("err=%v", providerErr)
	}
	badRaw := json.RawMessage(`{}`)
	apply := protocol.ApplyRequest{PlanID: "p", TargetID: "t", OperationID: "o", IdempotencyKey: "k", Attempt: 1, ProviderPayload: badRaw}
	if _, providerErr := provider.Apply(context.Background(), apply); providerErr == nil || providerErr.Code != protocol.ErrorConfiguration {
		t.Fatalf("err=%v", providerErr)
	}
	reconcile := protocol.ReconcileRequest{PlanID: "p", TargetID: "t", OperationID: "o", IdempotencyKey: "k", Attempt: 1, ProviderPayload: badRaw}
	if _, providerErr := provider.Reconcile(context.Background(), reconcile); providerErr == nil || providerErr.Code != protocol.ErrorConfiguration {
		t.Fatalf("err=%v", providerErr)
	}
}

func TestTokenClientAndEnvironment(t *testing.T) {
	provider := Provider{}
	if token, providerErr := provider.token(false); providerErr != nil || token != "" {
		t.Fatalf("token=%q err=%v", token, providerErr)
	}
	t.Setenv(TokenEnvironment, testToken)
	if token, providerErr := provider.token(true); providerErr != nil || token != testToken {
		t.Fatalf("token=%q err=%v", token, providerErr)
	}
	if provider.client() != http.DefaultClient || provider.gitRunner() == nil {
		t.Fatal("default dependencies unavailable")
	}
	_ = os.Unsetenv(TokenEnvironment)
	if _, providerErr := provider.token(true); providerErr == nil || providerErr.Code != protocol.ErrorAuthentication {
		t.Fatalf("err=%v", providerErr)
	}
}

func TestGitErrorClassificationAndRedaction(t *testing.T) {
	exit := gitExitError{Code: 7, Err: errors.New("exit")}
	if exit.Error() != "exit" || !errors.Is(exit, exit.Err) {
		t.Fatal("git exit error contract")
	}
	buffer := &limitedBuffer{remaining: 3}
	if n, _ := buffer.Write([]byte("abcdef")); n != 6 || buffer.String() != "abc" {
		t.Fatalf("buffer=%q n=%d", buffer.String(), n)
	}
	for name, tc := range map[string]struct {
		ctx    context.Context
		err    error
		stderr string
		code   protocol.ErrorCode
	}{
		"missing":   {context.Background(), exec.ErrNotFound, "", protocol.ErrorConfiguration},
		"cancelled": {ctx: cancelledContext(), err: errors.New("killed"), code: protocol.ErrorCancelled},
		"timeout":   {ctx: expiredContext(), err: errors.New("killed"), code: protocol.ErrorTimeout},
		"auth":      {context.Background(), errors.New("exit"), "authentication failed " + testToken, protocol.ErrorAuthentication},
		"forbidden": {context.Background(), errors.New("exit"), "permission denied", protocol.ErrorAuthorization},
		"transient": {context.Background(), errors.New("exit"), "offline", protocol.ErrorTransientExternal},
	} {
		t.Run(name, func(t *testing.T) {
			providerErr := classifyGitReadError(tc.ctx, tc.err, tc.stderr, testToken)
			if providerErr.Code != tc.code || strings.Contains(providerErr.Message, testToken) {
				t.Fatalf("err=%+v", providerErr)
			}
		})
	}
	for name, tc := range map[string]struct {
		err  error
		code protocol.ErrorCode
	}{
		"missing": {exec.ErrNotFound, protocol.ErrorConfiguration},
		"auth":    {errors.New("authentication failed " + testToken), protocol.ErrorAuthentication},
		"forbid":  {errors.New("permission denied"), protocol.ErrorAuthorization},
		"unknown": {errors.New("network reset"), protocol.ErrorAmbiguousOutcome},
	} {
		t.Run(name, func(t *testing.T) {
			providerErr := classifyGitPushError(tc.err, testToken)
			if providerErr.Code != tc.code || strings.Contains(providerErr.Message, testToken) {
				t.Fatalf("err=%+v", providerErr)
			}
		})
	}
	encoded := gitAuthorization(testToken)
	if strings.Contains(sanitizeGitOutput(encoded+" "+testToken, testToken), testToken) || strings.Contains(sanitizeGitOutput(encoded, testToken), encoded) {
		t.Fatal("git credential not redacted")
	}
	if len(sanitizeGitOutput(strings.Repeat("x", 3000), "")) != 2048 {
		t.Fatal("git diagnostics not bounded")
	}
}

func cancelledContext() context.Context {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	return ctx
}

func expiredContext() context.Context {
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	_ = cancel
	return ctx
}

func TestGitEnvironmentDoesNotInheritAmbientSecrets(t *testing.T) {
	t.Setenv("UNRELATED_SECRET", "should-not-pass")
	env := gitEnvironment("https://github.com/example/homebrew-tap.git", testToken)
	joined := strings.Join(env, "\n")
	if strings.Contains(joined, "UNRELATED_SECRET") || strings.Contains(joined, testToken) {
		t.Fatalf("env=%s", joined)
	}
	if !strings.Contains(joined, "GIT_CONFIG_KEY_0=http.https://github.com/example/homebrew-tap.git/.extraHeader") ||
		!strings.Contains(joined, "GIT_CONFIG_VALUE_0=Authorization: Basic") {
		t.Fatalf("env=%s", joined)
	}
	local := strings.Join(gitEnvironment(filepath.Join(t.TempDir(), "tap.git"), testToken), "\n")
	if strings.Contains(local, "GIT_CONFIG_VALUE_0=") {
		t.Fatalf("local repository unexpectedly received HTTP credentials: %s", local)
	}
}

func TestRepositoryPathSymlinkProtection(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "Formula")); err != nil {
		if errors.Is(err, os.ErrPermission) {
			t.Skip("symlink unavailable")
		}
		t.Fatal(err)
	}
	if err := writeRepositoryFile(root, "Formula/demo.rb", []byte("x")); err == nil {
		t.Fatal("symlink traversal accepted")
	}
	if _, err := readRepositoryFile(root, "Formula/demo.rb"); err == nil {
		t.Fatal("symlink traversal read accepted")
	}
}

func TestDirectAbsentAndConflictReconciliation(t *testing.T) {
	repo := newRepo(t)
	provider := Provider{}
	_, payload := plannedPayload(t, provider, formulaConfiguration(repo.remote, "direct"))
	payload.Branch = "missing"
	response, providerErr := provider.Reconcile(context.Background(), reconcileRequest(payload))
	if providerErr != nil || response.Result.State != protocol.ResultWaitingExternal {
		t.Fatalf("response=%+v err=%v", response, providerErr)
	}
	_, providerErr = provider.Apply(context.Background(), applyRequest(payload))
	if providerErr == nil || providerErr.Code != protocol.ErrorPermanentExternal {
		t.Fatalf("err=%v", providerErr)
	}

	_, payload = plannedPayload(t, provider, formulaConfiguration(repo.remote, "direct"))
	response, providerErr = provider.Reconcile(context.Background(), reconcileRequest(payload))
	if providerErr != nil || response.Result.State != protocol.ResultRejected || response.Result.ProviderState != "conflict" {
		t.Fatalf("response=%+v err=%v", response, providerErr)
	}
}

func TestNoChangeCommitAndPush(t *testing.T) {
	repo := newRepo(t)
	provider := Provider{}
	_, payload := plannedPayload(t, provider, formulaConfiguration(repo.remote, "direct"))
	first, providerErr := provider.Apply(context.Background(), applyRequest(payload))
	if providerErr != nil {
		t.Fatal(providerErr)
	}
	var evidenceValue evidence
	if err := json.Unmarshal(first.Result.Evidence, &evidenceValue); err != nil {
		t.Fatal(err)
	}
	commit, err := provider.commitAndPush(context.Background(), payload, payload.Branch, "")
	if err != nil || commit != evidenceValue.Commit {
		t.Fatalf("commit=%q want=%q err=%v", commit, evidenceValue.Commit, err)
	}
}

func TestPullRequestHTTPClassifiers(t *testing.T) {
	for name, tc := range map[string]struct {
		status int
		write  bool
		code   protocol.ErrorCode
	}{
		"auth":       {http.StatusUnauthorized, false, protocol.ErrorAuthentication},
		"forbidden":  {http.StatusForbidden, false, protocol.ErrorAuthorization},
		"read rate":  {http.StatusTooManyRequests, false, protocol.ErrorTransientExternal},
		"write rate": {http.StatusTooManyRequests, true, protocol.ErrorAmbiguousOutcome},
		"read 500":   {http.StatusInternalServerError, false, protocol.ErrorTransientExternal},
		"write 500":  {http.StatusInternalServerError, true, protocol.ErrorAmbiguousOutcome},
		"bad":        {http.StatusBadRequest, false, protocol.ErrorPermanentExternal},
	} {
		t.Run(name, func(t *testing.T) {
			if got := classifyPullRequestStatus(tc.status, tc.write); got.Code != tc.code {
				t.Fatalf("got=%+v", got)
			}
		})
	}
	if classifyPullRequestReadError(cancelledContext(), errors.New("x")).Code != protocol.ErrorCancelled {
		t.Fatal("cancel classification")
	}
	if classifyPullRequestReadError(expiredContext(), errors.New("x")).Code != protocol.ErrorTimeout {
		t.Fatal("timeout classification")
	}
	if classifyPullRequestReadError(context.Background(), errors.New("x")).Code != protocol.ErrorTransientExternal {
		t.Fatal("transient classification")
	}
}

func TestPullRequestLookupAndSubmissionErrors(t *testing.T) {
	payload := operationPayload{
		Mode: "pull-request", Branch: "main", UpdateBranch: "update",
		PullRequest: pullRequestConfig{API: "http://example.invalid", Repository: "owner/repo"},
	}
	for name, status := range map[string]int{
		"auth":      http.StatusUnauthorized,
		"forbidden": http.StatusForbidden,
		"rate":      http.StatusTooManyRequests,
		"server":    http.StatusInternalServerError,
		"bad":       http.StatusBadRequest,
	} {
		t.Run(name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(status) }))
			defer server.Close()
			value := payload
			value.PullRequest.API = server.URL
			provider := Provider{Client: server.Client()}
			if _, providerErr := provider.lookupPullRequest(context.Background(), value, testToken); providerErr == nil {
				t.Fatal("lookup status accepted")
			}
			if _, providerErr := provider.submitPullRequest(context.Background(), value, testToken); providerErr == nil {
				t.Fatal("submission status accepted")
			}
		})
	}

	malformed := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodPost {
			w.WriteHeader(http.StatusCreated)
		}
		_, _ = w.Write([]byte("{"))
	}))
	defer malformed.Close()
	payload.PullRequest.API = malformed.URL
	provider := Provider{Client: malformed.Client()}
	if _, providerErr := provider.lookupPullRequest(context.Background(), payload, testToken); providerErr == nil || providerErr.Code != protocol.ErrorPermanentExternal {
		t.Fatalf("lookup err=%v", providerErr)
	}
	if _, providerErr := provider.submitPullRequest(context.Background(), payload, testToken); providerErr == nil || providerErr.Code != protocol.ErrorPermanentExternal {
		t.Fatalf("submit err=%v", providerErr)
	}
}

func TestRejectedAndClosedPullRequestStates(t *testing.T) {
	repo := newRepo(t)
	fixture, server := newPRServer(t, "rejected")
	defer server.Close()
	provider := authenticatedProvider(server.Client())
	_, payload := plannedPayload(t, provider, prConfiguration(repo, server))
	_, providerErr := provider.Apply(context.Background(), applyRequest(payload))
	if providerErr == nil || providerErr.Code != protocol.ErrorRejected {
		t.Fatalf("err=%v", providerErr)
	}
	fixture.mu.Lock()
	fixture.mode = "normal"
	fixture.state = "closed"
	fixture.branch = payload.UpdateBranch
	fixture.base = payload.Branch
	fixture.mu.Unlock()
	response, providerErr := provider.Reconcile(context.Background(), reconcileRequest(payload))
	if providerErr != nil || response.Result.State != protocol.ResultRejected || response.Result.ProviderState != "pull-request-closed" {
		t.Fatalf("response=%+v err=%v", response, providerErr)
	}
}

type missingGitRunner struct{}

func (missingGitRunner) Run(context.Context, string, []string, ...string) (gitOutput, error) {
	return gitOutput{}, exec.ErrNotFound
}

func TestMissingGitFailsBeforeSideEffect(t *testing.T) {
	provider := Provider{Git: missingGitRunner{}}
	_, payload := plannedPayload(t, provider, formulaConfiguration("/tmp/tap.git", "direct"))
	_, providerErr := provider.Apply(context.Background(), applyRequest(payload))
	if providerErr == nil || providerErr.Code != protocol.ErrorConfiguration {
		t.Fatalf("err=%v", providerErr)
	}
}

func TestSmallBranchCoverage(t *testing.T) {
	description, providerErr := (Provider{}).Describe(context.Background(), protocol.DescribeRequest{})
	if providerErr != nil || description.Provider.Version != DefaultVersion {
		t.Fatalf("description=%+v err=%v", description, providerErr)
	}
	if branch := deterministicUpdateBranch("!!!", []byte{1, 2, 3, 4, 5, 6}); !strings.HasPrefix(branch, "distroplane/release-") {
		t.Fatalf("branch=%q", branch)
	}
	if branch := deterministicUpdateBranch(strings.Repeat("A", 80), []byte{1, 2, 3, 4, 5, 6}); len(branch) > 80 {
		t.Fatalf("branch too long: %q", branch)
	}
	if result, err := (Provider{}).lookupPullRequest(context.Background(), operationPayload{Mode: "direct"}, ""); err != nil || result.exists {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	if _, err := decodePullRequest(strings.NewReader("{")); err == nil {
		t.Fatal("malformed pull request accepted")
	}
	if _, err := decodePullRequest(strings.NewReader(`{"number":0,"html_url":"https://example.test/pr/1"}`)); err == nil {
		t.Fatal("invalid pull request accepted")
	}
	value, err := decodePullRequest(strings.NewReader(`{"number":1,"html_url":"https://example.test/pr/1"}`))
	if err != nil || value.state != "open" {
		t.Fatalf("value=%+v err=%v", value, err)
	}
}

func TestWriteReadRepositoryFile(t *testing.T) {
	root := t.TempDir()
	if err := writeRepositoryFile(root, "Formula/demo.rb", []byte("content")); err != nil {
		t.Fatal(err)
	}
	raw, err := readRepositoryFile(root, "Formula/demo.rb")
	if err != nil || string(raw) != "content" {
		t.Fatalf("raw=%q err=%v", raw, err)
	}
	if _, err := readRepositoryFile(root, "Formula/missing.rb"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("err=%v", err)
	}
}

func TestMissingCredentialOnExecution(t *testing.T) {
	payload := operationPayload{
		Repository: "/tmp/repo", Branch: "main", Path: "Formula/demo.rb", Mode: "direct",
		Content: "x\n", ContentSHA256: sha256Hex([]byte("x\n")), Authenticated: true,
		Artifact: "archive", ArtifactDigest: "sha256:" + strings.Repeat("a", 64), ArtifactSize: 1,
	}
	provider := Provider{Getenv: func(string) string { return "" }}
	_, providerErr := provider.Apply(context.Background(), applyRequest(payload))
	if providerErr == nil || providerErr.Code != protocol.ErrorAuthentication {
		t.Fatalf("err=%v", providerErr)
	}
	_, providerErr = provider.Reconcile(context.Background(), reconcileRequest(payload))
	if providerErr == nil || providerErr.Code != protocol.ErrorAuthentication {
		t.Fatalf("err=%v", providerErr)
	}
}
