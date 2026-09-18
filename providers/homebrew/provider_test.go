package homebrew

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/aalsanie/distroplane/internal/protocol"
)

const testToken = "homebrew-token-secret"

type repoFixture struct {
	remote string
}

func newRepo(t *testing.T) repoFixture {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is required for Homebrew provider integration tests")
	}
	root := t.TempDir()
	remote := filepath.Join(root, "tap.git")
	runExternalGit(t, "", "init", "--bare", remote)
	seed := filepath.Join(root, "seed")
	runExternalGit(t, "", "init", seed)
	runExternalGit(t, seed, "config", "user.name", "Distroplane Test")
	runExternalGit(t, seed, "config", "user.email", "test@example.com")
	if err := os.WriteFile(filepath.Join(seed, "README.md"), []byte("tap\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runExternalGit(t, seed, "add", "README.md")
	runExternalGit(t, seed, "commit", "-m", "seed")
	runExternalGit(t, seed, "branch", "-M", "main")
	runExternalGit(t, seed, "remote", "add", "origin", remote)
	runExternalGit(t, seed, "push", "-u", "origin", "main")
	return repoFixture{remote: remote}
}

func runExternalGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	command := exec.Command("git", args...)
	command.Dir = dir
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", args, err, output)
	}
	return strings.TrimSpace(string(output))
}

func (r repoFixture) promote(t *testing.T, source, destination string) {
	t.Helper()
	commit := runExternalGit(t, "", "--git-dir", r.remote, "rev-parse", "refs/heads/"+source)
	runExternalGit(t, "", "--git-dir", r.remote, "update-ref", "refs/heads/"+destination, commit)
}

func formulaConfiguration(repository, mode string) configuration {
	return configuration{
		Artifact:   "archive",
		Repository: repository,
		Branch:     "main",
		Path:       "Formula/demo.rb",
		Mode:       mode,
		Manifest: manifestConfig{
			Type: "formula", Name: "Demo", Description: "Demo CLI", Homepage: "https://example.test",
			URL: "https://example.test/demo-1.2.3.tar.gz", Version: "1.2.3", Binary: "demo", License: "MIT",
		},
	}
}

func planRequest(t *testing.T, cfg configuration) protocol.PlanRequest {
	t.Helper()
	raw, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return protocol.PlanRequest{
		Release: protocol.Release{ID: "v1.2.3", Artifacts: []protocol.Artifact{{
			Name: "archive", Digest: "sha256:" + strings.Repeat("a", 64), Size: 123,
		}}},
		Target: protocol.Target{ID: "brew", Configuration: raw},
	}
}

func plannedPayload(t *testing.T, provider Provider, cfg configuration) (protocol.PlanResponse, operationPayload) {
	t.Helper()
	response, providerErr := provider.Plan(context.Background(), planRequest(t, cfg))
	if providerErr != nil {
		t.Fatal(providerErr)
	}
	if len(response.Operations) != 1 {
		t.Fatalf("operations=%+v", response.Operations)
	}
	var payload operationPayload
	if err := json.Unmarshal(response.Operations[0].ProviderPayload, &payload); err != nil {
		t.Fatal(err)
	}
	return response, payload
}

func applyRequest(payload operationPayload) protocol.ApplyRequest {
	raw, _ := json.Marshal(payload)
	return protocol.ApplyRequest{
		PlanID: "plan", TargetID: "brew", OperationID: "publish", IdempotencyKey: "key", Attempt: 1, ProviderPayload: raw,
	}
}

func reconcileRequest(payload operationPayload) protocol.ReconcileRequest {
	raw, _ := json.Marshal(payload)
	return protocol.ReconcileRequest{
		PlanID: "plan", TargetID: "brew", OperationID: "publish", IdempotencyKey: "key", Attempt: 1, ProviderPayload: raw,
	}
}

func authenticatedProvider(client *http.Client) Provider {
	return Provider{Client: client, Getenv: func(name string) string {
		if name == TokenEnvironment {
			return testToken
		}
		return ""
	}}
}

func TestDescribeAndPlanFormula(t *testing.T) {
	repo := newRepo(t)
	provider := Provider{Version: "1.2.3"}
	description, providerErr := provider.Describe(context.Background(), protocol.DescribeRequest{})
	if providerErr != nil || description.Provider.Name != Name || description.Provider.Version != "1.2.3" || len(description.Capabilities) != 3 {
		t.Fatalf("description=%+v err=%v", description, providerErr)
	}
	response, payload := plannedPayload(t, provider, formulaConfiguration(repo.remote, "direct"))
	if len(response.Requirements) != 1 || response.Requirements[0].Kind != "executable" || response.Requirements[0].Name != "git" {
		t.Fatalf("requirements=%+v", response.Requirements)
	}
	if !strings.Contains(payload.Content, `class Demo < Formula`) || !strings.Contains(payload.Content, strings.Repeat("a", 64)) || payload.ContentSHA256 == "" {
		t.Fatalf("payload=%+v", payload)
	}
	if payload.Commit.AuthorName != "Distroplane" || payload.Commit.Message == "" {
		t.Fatalf("commit=%+v", payload.Commit)
	}
}

func TestPlanCaskAndCredentialRequirement(t *testing.T) {
	repo := newRepo(t)
	cfg := formulaConfiguration(repo.remote, "direct")
	cfg.Path = "Casks/demo.rb"
	cfg.Authentication.Credential = "tap-token"
	cfg.Manifest = manifestConfig{
		Type: "cask", Name: "demo", Description: "Demo App", Homepage: "https://example.test",
		URL: "https://example.test/demo.zip", Version: "1.2.3", InstallKind: "app", InstallSource: "Demo.app",
	}
	response, payload := plannedPayload(t, Provider{}, cfg)
	if len(response.Requirements) != 2 || response.Requirements[1].Name != "tap-token" || !strings.Contains(string(response.Requirements[1].Metadata), TokenEnvironment) {
		t.Fatalf("requirements=%+v", response.Requirements)
	}
	if !strings.Contains(payload.Content, `cask "demo" do`) || !strings.Contains(payload.Content, `app "Demo.app"`) {
		t.Fatalf("content=%s", payload.Content)
	}
}

func TestDirectApplyAlreadyAppliedAndReconcile(t *testing.T) {
	repo := newRepo(t)
	provider := Provider{}
	_, payload := plannedPayload(t, provider, formulaConfiguration(repo.remote, "direct"))
	response, providerErr := provider.Apply(context.Background(), applyRequest(payload))
	if providerErr != nil || response.Result.State != protocol.ResultPublished || response.Result.ProviderState != "applied" {
		t.Fatalf("response=%+v err=%v", response, providerErr)
	}
	if strings.Contains(string(response.Result.Evidence), testToken) {
		t.Fatal("secret leaked into evidence")
	}
	again, providerErr := provider.Apply(context.Background(), applyRequest(payload))
	if providerErr != nil || again.Result.ProviderState != "already-applied" {
		t.Fatalf("again=%+v err=%v", again, providerErr)
	}
	reconciled, providerErr := provider.Reconcile(context.Background(), reconcileRequest(payload))
	if providerErr != nil || reconciled.Result.State != protocol.ResultPublished {
		t.Fatalf("reconciled=%+v err=%v", reconciled, providerErr)
	}
}

type prFixture struct {
	mu        sync.Mutex
	mode      string
	number    int64
	branch    string
	base      string
	state     string
	url       string
	postCount int
}

func newPRServer(t *testing.T, mode string) (*prFixture, *httptest.Server) {
	t.Helper()
	fixture := &prFixture{mode: mode, number: 17, state: "open", url: "https://example.test/pr/17"}
	server := httptest.NewServer(http.HandlerFunc(fixture.serveHTTP))
	return fixture, server
}

func (f *prFixture) serveHTTP(w http.ResponseWriter, request *http.Request) {
	if request.Header.Get("Authorization") != "Bearer "+testToken {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	if request.Method == http.MethodGet {
		f.mu.Lock()
		defer f.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if f.branch == "" {
			_, _ = io.WriteString(w, "[]")
			return
		}
		merged := "null"
		if f.state == "merged" {
			merged = `"2026-09-18T00:00:00Z"`
		}
		_, _ = fmt.Fprintf(w, `[{"number":%d,"html_url":%q,"state":%q,"merged_at":%s,"head":{"ref":%q},"base":{"ref":%q}}]`, f.number, f.url, f.state, merged, f.branch, f.base)
		return
	}
	if request.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	var body map[string]string
	if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	f.mu.Lock()
	f.postCount++
	mode := f.mode
	if mode == "normal" || mode == "ambiguous" {
		f.branch = body["head"]
		f.base = body["base"]
	}
	f.mu.Unlock()
	switch mode {
	case "ambiguous":
		hijacker, ok := w.(http.Hijacker)
		if ok {
			connection, _, err := hijacker.Hijack()
			if err == nil {
				_ = connection.Close()
				return
			}
		}
		w.WriteHeader(http.StatusInternalServerError)
		return
	case "rejected":
		w.WriteHeader(http.StatusUnprocessableEntity)
		return
	case "forbidden":
		w.WriteHeader(http.StatusForbidden)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_, _ = fmt.Fprintf(w, `{"number":%d,"html_url":%q,"state":"open"}`, f.number, f.url)
}

func prConfiguration(repo repoFixture, server *httptest.Server) configuration {
	cfg := formulaConfiguration(repo.remote, "pull-request")
	cfg.Authentication.Credential = "tap-token"
	cfg.UpdateBranch = "distroplane/demo-1.2.3"
	cfg.PullRequest = pullRequestConfig{
		API: server.URL, Repository: "owner/homebrew-tap", Title: "Update Demo", Body: "Automated update", AllowInsecureHTTP: true,
	}
	return cfg
}

func TestPullRequestFlowAndMergeReconciliation(t *testing.T) {
	repo := newRepo(t)
	fixture, server := newPRServer(t, "normal")
	defer server.Close()
	provider := authenticatedProvider(server.Client())
	_, payload := plannedPayload(t, provider, prConfiguration(repo, server))
	response, providerErr := provider.Apply(context.Background(), applyRequest(payload))
	if providerErr != nil || response.Result.State != protocol.ResultWaitingExternal || response.Result.ProviderState != "pull-request-open" {
		t.Fatalf("response=%+v err=%v", response, providerErr)
	}
	if !strings.Contains(string(response.Result.Evidence), fixture.url) {
		t.Fatalf("evidence=%s", response.Result.Evidence)
	}
	reconciled, providerErr := provider.Reconcile(context.Background(), reconcileRequest(payload))
	if providerErr != nil || reconciled.Result.State != protocol.ResultWaitingExternal {
		t.Fatalf("reconciled=%+v err=%v", reconciled, providerErr)
	}
	repo.promote(t, payload.UpdateBranch, payload.Branch)
	fixture.mu.Lock()
	fixture.state = "merged"
	fixture.mu.Unlock()
	reconciled, providerErr = provider.Reconcile(context.Background(), reconcileRequest(payload))
	if providerErr != nil || reconciled.Result.State != protocol.ResultPublished {
		t.Fatalf("merged=%+v err=%v", reconciled, providerErr)
	}
}

func TestPullRequestBranchConflict(t *testing.T) {
	repo := newRepo(t)
	_, server := newPRServer(t, "normal")
	defer server.Close()
	provider := authenticatedProvider(server.Client())
	cfg := prConfiguration(repo, server)
	_, first := plannedPayload(t, provider, cfg)
	response, providerErr := provider.Apply(context.Background(), applyRequest(first))
	if providerErr != nil || response.Result.State != protocol.ResultWaitingExternal {
		t.Fatalf("first=%+v err=%v", response, providerErr)
	}
	cfg.Manifest.Description = "Different description"
	_, second := plannedPayload(t, provider, cfg)
	response, providerErr = provider.Apply(context.Background(), applyRequest(second))
	if providerErr != nil || response.Result.State != protocol.ResultRejected || response.Result.ProviderState != "branch-conflict" {
		t.Fatalf("second=%+v err=%v", response, providerErr)
	}
}

func TestAmbiguousPullRequestSubmissionReconciles(t *testing.T) {
	repo := newRepo(t)
	_, server := newPRServer(t, "ambiguous")
	defer server.Close()
	provider := authenticatedProvider(server.Client())
	_, payload := plannedPayload(t, provider, prConfiguration(repo, server))
	_, providerErr := provider.Apply(context.Background(), applyRequest(payload))
	if providerErr == nil || providerErr.Code != protocol.ErrorAmbiguousOutcome {
		t.Fatalf("err=%+v", providerErr)
	}
	response, providerErr := provider.Reconcile(context.Background(), reconcileRequest(payload))
	if providerErr != nil || response.Result.State != protocol.ResultWaitingExternal || response.Result.ProviderState != "pull-request-open" {
		t.Fatalf("response=%+v err=%v", response, providerErr)
	}
}

type lostPushRunner struct {
	delegate GitRunner
	mu       sync.Mutex
	lost     bool
}

func (r *lostPushRunner) Run(ctx context.Context, dir string, env []string, args ...string) (gitOutput, error) {
	output, err := r.delegate.Run(ctx, dir, env, args...)
	if err != nil {
		return output, err
	}
	if len(args) > 0 && args[0] == "push" {
		r.mu.Lock()
		defer r.mu.Unlock()
		if !r.lost {
			r.lost = true
			return output, errors.New("confirmation lost")
		}
	}
	return output, nil
}

func TestLostPushConfirmationIsObserved(t *testing.T) {
	repo := newRepo(t)
	provider := Provider{Git: &lostPushRunner{delegate: execGitRunner{}}}
	_, payload := plannedPayload(t, provider, formulaConfiguration(repo.remote, "direct"))
	response, providerErr := provider.Apply(context.Background(), applyRequest(payload))
	if providerErr != nil || response.Result.State != protocol.ResultPublished {
		t.Fatalf("response=%+v err=%v", response, providerErr)
	}
}
