package winget

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

const testToken = "winget-token-secret"

type repoFixture struct {
	remote string
}

func newRepo(t *testing.T) repoFixture {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is required for WinGet provider integration tests")
	}
	root := t.TempDir()
	remote := filepath.Join(root, "winget.git")
	runExternalGit(t, "", "init", "--bare", remote)
	seed := filepath.Join(root, "seed")
	runExternalGit(t, "", "init", seed)
	runExternalGit(t, seed, "config", "user.name", "Distroplane Test")
	runExternalGit(t, seed, "config", "user.email", "test@example.com")
	if err := os.WriteFile(filepath.Join(seed, "README.md"), []byte("winget\n"), 0o644); err != nil {
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

func baseConfiguration(repo repoFixture, server *httptest.Server) configuration {
	return configuration{
		Artifact: "installer", Repository: repo.remote, Branch: "main", UpdateBranch: "distroplane/contoso-demo-1.2.3",
		Package: packageConfig{
			Identifier: "Contoso.Demo", Version: "1.2.3", DefaultLocale: "en-US",
			Publisher: "Contoso", Name: "Demo", License: "MIT", ShortDescription: "Demo application",
			PackageURL: "https://example.test/demo", PublisherURL: "https://example.test",
			ReleaseNotesURL: "https://example.test/demo/releases/1.2.3",
		},
		Installer: installerConfig{
			Architecture: "x64", Type: "msi", URL: "https://example.test/demo-1.2.3.msi",
		},
		Authentication: authenticationConfig{Credential: "winget-token"},
		PullRequest: pullRequestConfig{
			API: server.URL, Repository: "microsoft/winget-pkgs", HeadOwner: "contoso",
			Title: "New version: Contoso.Demo version 1.2.3", Body: "Automated submission", AllowInsecureHTTP: true,
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
			Name: "installer", Digest: "sha256:" + strings.Repeat("a", 64), Size: 123,
		}}},
		Target: protocol.Target{ID: "winget", Configuration: raw},
	}
}

func plannedPayload(t *testing.T, provider Provider, cfg configuration) (protocol.PlanResponse, operationPayload) {
	t.Helper()
	response, providerErr := provider.Plan(context.Background(), planRequest(t, cfg))
	if providerErr != nil {
		t.Fatal(providerErr)
	}
	var payload operationPayload
	if len(response.Operations) != 1 {
		t.Fatalf("operations=%+v", response.Operations)
	}
	if err := json.Unmarshal(response.Operations[0].ProviderPayload, &payload); err != nil {
		t.Fatal(err)
	}
	return response, payload
}

func applyRequest(payload operationPayload) protocol.ApplyRequest {
	raw, _ := json.Marshal(payload)
	return protocol.ApplyRequest{
		PlanID: "plan", TargetID: "winget", OperationID: "submit", IdempotencyKey: "key", Attempt: 1, ProviderPayload: raw,
	}
}

func reconcileRequest(payload operationPayload) protocol.ReconcileRequest {
	raw, _ := json.Marshal(payload)
	return protocol.ReconcileRequest{
		PlanID: "plan", TargetID: "winget", OperationID: "submit", IdempotencyKey: "key", Attempt: 1, ProviderPayload: raw,
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

type apiFixture struct {
	mu                       sync.Mutex
	mode                     string
	state                    string
	checks                   string
	number                   int64
	branch                   string
	base                     string
	headSHA                  string
	url                      string
	postCount                int
	destinationCommit        string
	destinationFiles         map[string]string
	destinationCommitStatus  int
	destinationContentStatus int
	destinationMalformed     bool
	hidePullList             bool
	moveDestinationTo        string
	contentRefs              []string
}

func newAPIServer(t *testing.T, mode string) (*apiFixture, *httptest.Server) {
	t.Helper()
	fixture := &apiFixture{
		mode: mode, state: "open", checks: "pending", number: 42,
		headSHA: strings.Repeat("b", 40), url: "https://example.test/pr/42",
		destinationCommit: strings.Repeat("d", 40), destinationFiles: map[string]string{},
	}
	server := httptest.NewServer(http.HandlerFunc(fixture.serveHTTP))
	return fixture, server
}

func (f *apiFixture) serveHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Authorization") != "Bearer "+testToken {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	if strings.Contains(r.URL.Path, "/check-runs") {
		f.serveChecks(w)
		return
	}
	if r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/contents/") {
		f.serveDestinationContent(w, r)
		return
	}
	if r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/commits/") {
		f.serveDestinationCommit(w)
		return
	}
	if r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/pulls/") && !strings.HasSuffix(r.URL.Path, "/pulls") {
		f.servePullRequestByNumber(w, r)
		return
	}
	if r.Method == http.MethodGet {
		f.mu.Lock()
		defer f.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if f.branch == "" || f.hidePullList {
			_, _ = io.WriteString(w, "[]")
			return
		}
		_, _ = io.WriteString(w, "[")
		f.writePullRequest(w)
		_, _ = io.WriteString(w, "]")
		return
	}
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	var body map[string]string
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	f.mu.Lock()
	f.postCount++
	mode := f.mode
	if mode == "normal" || mode == "ambiguous" {
		head := body["head"]
		if _, branch, ok := strings.Cut(head, ":"); ok {
			f.branch = branch
		} else {
			f.branch = head
		}
		f.base = body["base"]
	}
	f.mu.Unlock()
	switch mode {
	case "ambiguous":
		if hijacker, ok := w.(http.Hijacker); ok {
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
	f.mu.Lock()
	defer f.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	f.writePullRequest(w)
}

func (f *apiFixture) serveDestinationCommit(w http.ResponseWriter) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.destinationCommitStatus != 0 {
		w.WriteHeader(f.destinationCommitStatus)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	if f.destinationMalformed {
		_, _ = io.WriteString(w, `{"sha":`)
		return
	}
	commit := f.destinationCommit
	_, _ = fmt.Fprintf(w, `{"sha":%q}`, commit)
	if f.moveDestinationTo != "" {
		f.destinationCommit = f.moveDestinationTo
		f.moveDestinationTo = ""
	}
}

func (f *apiFixture) serveDestinationContent(w http.ResponseWriter, r *http.Request) {
	const prefix = "/repos/microsoft/winget-pkgs/contents/"
	path := strings.TrimPrefix(r.URL.Path, prefix)
	if path == r.URL.Path {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.contentRefs = append(f.contentRefs, r.URL.Query().Get("ref"))
	if f.destinationContentStatus != 0 {
		w.WriteHeader(f.destinationContentStatus)
		return
	}
	content, ok := f.destinationFiles[path]
	if !ok {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	_, _ = io.WriteString(w, content)
}

func (f *apiFixture) servePullRequestByNumber(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.branch == "" || !strings.HasSuffix(r.URL.Path, fmt.Sprintf("/pulls/%d", f.number)) {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	f.writePullRequest(w)
}

func (f *apiFixture) writePullRequest(w io.Writer) {
	merged := "null"
	if f.state == "merged" {
		merged = `"2026-09-18T00:00:00Z"`
	}
	_, _ = fmt.Fprintf(w,
		`{"number":%d,"html_url":%q,"state":%q,"merged_at":%s,"head":{"ref":%q,"sha":%q},"base":{"ref":%q}}`,
		f.number, f.url, f.state, merged, f.branch, f.headSHA, f.base)
}

func (f *apiFixture) publishDestination(files []manifestFile) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.destinationFiles = make(map[string]string, len(files))
	for _, file := range files {
		f.destinationFiles[file.Path] = file.Content
	}
}

func (f *apiFixture) serveChecks(w http.ResponseWriter) {
	f.mu.Lock()
	defer f.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	switch f.checks {
	case "passed":
		_, _ = io.WriteString(w, `{"check_runs":[{"name":"Manifest Validation","status":"completed","conclusion":"success"},{"name":"Installer Validation","status":"completed","conclusion":"neutral"}]}`)
	case "failed":
		_, _ = io.WriteString(w, `{"check_runs":[{"name":"Manifest Validation","status":"completed","conclusion":"failure"}]}`)
	case "none":
		_, _ = io.WriteString(w, `{"check_runs":[]}`)
	default:
		_, _ = io.WriteString(w, `{"check_runs":[{"name":"Manifest Validation","status":"in_progress","conclusion":null}]}`)
	}
}

func TestDescribeAndPlanGeneratesCommunityManifestSet(t *testing.T) {
	repo := newRepo(t)
	_, server := newAPIServer(t, "normal")
	defer server.Close()
	provider := authenticatedProvider(server.Client())
	description, providerErr := (Provider{Version: "1.2.3"}).Describe(context.Background(), protocol.DescribeRequest{})
	if providerErr != nil || description.Provider.Name != Name || description.Provider.Version != "1.2.3" {
		t.Fatalf("description=%+v err=%v", description, providerErr)
	}
	response, payload := plannedPayload(t, provider, baseConfiguration(repo, server))
	if len(response.Requirements) != 2 || response.Requirements[0].Name != "git" || response.Requirements[1].Name != "winget-token" {
		t.Fatalf("requirements=%+v", response.Requirements)
	}
	if response.Operations[0].SideEffecting != true || response.Operations[0].ID != "submit" {
		t.Fatalf("operation=%+v", response.Operations[0])
	}
	if len(payload.Files) != 3 || payload.ManifestVersion != defaultManifestVersion {
		t.Fatalf("payload=%+v", payload)
	}
	joined := payload.Files[0].Content + payload.Files[1].Content + payload.Files[2].Content
	if strings.Contains(joined, "ManifestType: singleton") || !strings.Contains(joined, "ManifestType: installer") ||
		!strings.Contains(joined, "ManifestType: defaultLocale") || !strings.Contains(joined, strings.ToUpper(strings.Repeat("a", 64))) {
		t.Fatalf("manifests=%s", joined)
	}
	for _, file := range payload.Files {
		if !strings.HasPrefix(file.Path, "manifests/c/Contoso/Demo/1.2.3/") {
			t.Fatalf("path=%q", file.Path)
		}
		if !strings.HasPrefix(file.Content, "# yaml-language-server: $schema=") {
			t.Fatalf("missing schema header: %s", file.Content)
		}
	}
}

func TestSuccessfulSubmissionWaitsForExternalReview(t *testing.T) {
	repo := newRepo(t)
	fixture, server := newAPIServer(t, "normal")
	defer server.Close()
	provider := authenticatedProvider(server.Client())
	_, payload := plannedPayload(t, provider, baseConfiguration(repo, server))
	response, providerErr := provider.Apply(context.Background(), applyRequest(payload))
	if providerErr != nil || response.Result.State != protocol.ResultWaitingExternal || response.Result.ProviderState != "submitted" {
		t.Fatalf("response=%+v err=%v", response, providerErr)
	}
	if strings.Contains(string(response.Result.Evidence), testToken) || !strings.Contains(string(response.Result.Evidence), fixture.url) {
		t.Fatalf("evidence=%s", response.Result.Evidence)
	}
}

func TestValidationAndReviewStates(t *testing.T) {
	repo := newRepo(t)
	fixture, server := newAPIServer(t, "normal")
	defer server.Close()
	provider := authenticatedProvider(server.Client())
	_, payload := plannedPayload(t, provider, baseConfiguration(repo, server))
	if _, providerErr := provider.Apply(context.Background(), applyRequest(payload)); providerErr != nil {
		t.Fatal(providerErr)
	}
	response, providerErr := provider.Reconcile(context.Background(), reconcileRequest(payload))
	if providerErr != nil || response.Result.State != protocol.ResultWaitingExternal || response.Result.ProviderState != "validation-pending" {
		t.Fatalf("pending=%+v err=%v", response, providerErr)
	}

	fixture.mu.Lock()
	fixture.checks = "passed"
	fixture.mu.Unlock()
	response, providerErr = provider.Reconcile(context.Background(), reconcileRequest(payload))
	if providerErr != nil || response.Result.State != protocol.ResultWaitingExternal || response.Result.ProviderState != "review-pending" {
		t.Fatalf("passed=%+v err=%v", response, providerErr)
	}

	fixture.mu.Lock()
	fixture.checks = "failed"
	fixture.mu.Unlock()
	response, providerErr = provider.Reconcile(context.Background(), reconcileRequest(payload))
	if providerErr != nil || response.Result.State != protocol.ResultRejected || response.Result.ProviderState != "validation-failed" {
		t.Fatalf("failed=%+v err=%v", response, providerErr)
	}
	if !strings.Contains(string(response.Result.Evidence), "Manifest Validation") {
		t.Fatalf("evidence=%s", response.Result.Evidence)
	}
}

func TestMergedPullRequestWaitsForDestinationContent(t *testing.T) {
	repo := newRepo(t)
	fixture, server := newAPIServer(t, "normal")
	defer server.Close()
	provider := authenticatedProvider(server.Client())
	_, payload := plannedPayload(t, provider, baseConfiguration(repo, server))
	response, providerErr := provider.Apply(context.Background(), applyRequest(payload))
	if providerErr != nil || response.Result.State != protocol.ResultWaitingExternal {
		t.Fatalf("apply=%+v err=%v", response, providerErr)
	}
	fixture.mu.Lock()
	fixture.state = "merged"
	fixture.checks = "passed"
	fixture.mu.Unlock()
	reconciled, providerErr := provider.Reconcile(context.Background(), reconcileRequest(payload))
	if providerErr != nil || reconciled.Result.State != protocol.ResultWaitingExternal || reconciled.Result.ProviderState != "merged-awaiting-destination" {
		t.Fatalf("merged=%+v err=%v", reconciled, providerErr)
	}

	repo.promote(t, payload.UpdateBranch, payload.Branch)
	reconciled, providerErr = provider.Reconcile(context.Background(), reconcileRequest(payload))
	if providerErr != nil || reconciled.Result.State != protocol.ResultWaitingExternal || reconciled.Result.ProviderState != "merged-awaiting-destination" {
		t.Fatalf("fork base incorrectly established publication: %+v err=%v", reconciled, providerErr)
	}

	fixture.publishDestination(payload.Files)
	reconciled, providerErr = provider.Reconcile(context.Background(), reconcileRequest(payload))
	if providerErr != nil || reconciled.Result.State != protocol.ResultPublished || reconciled.Result.ProviderState != "published" {
		t.Fatalf("published=%+v err=%v", reconciled, providerErr)
	}
	var got evidence
	if err := json.Unmarshal(reconciled.Result.Evidence, &got); err != nil {
		t.Fatal(err)
	}
	if got.DestinationCommit != strings.Repeat("d", 40) {
		t.Fatalf("evidence=%+v", got)
	}
}

func TestClosedPullRequestIsRejected(t *testing.T) {
	repo := newRepo(t)
	fixture, server := newAPIServer(t, "normal")
	defer server.Close()
	provider := authenticatedProvider(server.Client())
	_, payload := plannedPayload(t, provider, baseConfiguration(repo, server))
	if _, providerErr := provider.Apply(context.Background(), applyRequest(payload)); providerErr != nil {
		t.Fatal(providerErr)
	}
	fixture.mu.Lock()
	fixture.state = "closed"
	fixture.mu.Unlock()
	response, providerErr := provider.Reconcile(context.Background(), reconcileRequest(payload))
	if providerErr != nil || response.Result.State != protocol.ResultRejected || response.Result.ProviderState != "pull-request-closed" {
		t.Fatalf("response=%+v err=%v", response, providerErr)
	}
}

func TestAmbiguousSubmissionReconcilesWithoutDuplicatePR(t *testing.T) {
	repo := newRepo(t)
	fixture, server := newAPIServer(t, "ambiguous")
	defer server.Close()
	provider := authenticatedProvider(server.Client())
	_, payload := plannedPayload(t, provider, baseConfiguration(repo, server))
	_, providerErr := provider.Apply(context.Background(), applyRequest(payload))
	if providerErr == nil || providerErr.Code != protocol.ErrorAmbiguousOutcome {
		t.Fatalf("err=%+v", providerErr)
	}
	response, providerErr := provider.Reconcile(context.Background(), reconcileRequest(payload))
	if providerErr != nil || response.Result.State != protocol.ResultWaitingExternal {
		t.Fatalf("response=%+v err=%v", response, providerErr)
	}
	fixture.mu.Lock()
	count := fixture.postCount
	fixture.mu.Unlock()
	if count != 1 {
		t.Fatalf("post count=%d", count)
	}
}

func TestForkBaseDoesNotEstablishPublication(t *testing.T) {
	repo := newRepo(t)
	_, server := newAPIServer(t, "normal")
	defer server.Close()
	provider := authenticatedProvider(server.Client())
	cfg := baseConfiguration(repo, server)
	_, first := plannedPayload(t, provider, cfg)
	if _, providerErr := provider.Apply(context.Background(), applyRequest(first)); providerErr != nil {
		t.Fatal(providerErr)
	}
	cfg.Package.ShortDescription = "Different description"
	_, second := plannedPayload(t, provider, cfg)
	response, providerErr := provider.Apply(context.Background(), applyRequest(second))
	if providerErr != nil || response.Result.State != protocol.ResultRejected || response.Result.ProviderState != "branch-conflict" {
		t.Fatalf("conflict=%+v err=%v", response, providerErr)
	}
	repo.promote(t, first.UpdateBranch, first.Branch)
	response, providerErr = provider.Apply(context.Background(), applyRequest(first))
	if providerErr != nil || response.Result.State != protocol.ResultWaitingExternal || response.Result.ProviderState != "submitted" {
		t.Fatalf("fork base incorrectly established publication: %+v err=%v", response, providerErr)
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

func TestLostPushConfirmationIsReconciledBeforeSubmission(t *testing.T) {
	repo := newRepo(t)
	fixture, server := newAPIServer(t, "normal")
	defer server.Close()
	provider := authenticatedProvider(server.Client())
	provider.Git = &lostPushRunner{delegate: execGitRunner{}}
	_, payload := plannedPayload(t, provider, baseConfiguration(repo, server))
	response, providerErr := provider.Apply(context.Background(), applyRequest(payload))
	if providerErr != nil || response.Result.State != protocol.ResultWaitingExternal {
		t.Fatalf("response=%+v err=%v", response, providerErr)
	}
	fixture.mu.Lock()
	posts := fixture.postCount
	fixture.mu.Unlock()
	if posts != 1 {
		t.Fatalf("pull request submissions=%d", posts)
	}
}

func TestDestinationContentUsesOneResolvedCommit(t *testing.T) {
	repo := newRepo(t)
	fixture, server := newAPIServer(t, "normal")
	defer server.Close()
	provider := authenticatedProvider(server.Client())
	_, payload := plannedPayload(t, provider, baseConfiguration(repo, server))
	fixture.publishDestination(payload.Files)
	fixture.mu.Lock()
	resolved := fixture.destinationCommit
	fixture.moveDestinationTo = strings.Repeat("e", 40)
	fixture.mu.Unlock()

	response, providerErr := provider.Reconcile(context.Background(), reconcileRequest(payload))
	if providerErr != nil || response.Result.State != protocol.ResultPublished || response.Result.ProviderState != "published" {
		t.Fatalf("response=%+v err=%v", response, providerErr)
	}
	fixture.mu.Lock()
	refs := append([]string(nil), fixture.contentRefs...)
	current := fixture.destinationCommit
	fixture.mu.Unlock()
	if current == resolved || len(refs) != len(payload.Files) {
		t.Fatalf("resolved=%q current=%q refs=%v", resolved, current, refs)
	}
	for _, ref := range refs {
		if ref != resolved {
			t.Fatalf("destination manifest read from moving ref %q, want %q", ref, resolved)
		}
	}
	var got evidence
	if err := json.Unmarshal(response.Result.Evidence, &got); err != nil {
		t.Fatal(err)
	}
	if got.DestinationCommit != resolved {
		t.Fatalf("evidence=%+v", got)
	}
}

func TestDestinationMissingAndConflictAreNotPublished(t *testing.T) {
	t.Run("partial missing", func(t *testing.T) {
		repo := newRepo(t)
		fixture, server := newAPIServer(t, "normal")
		defer server.Close()
		provider := authenticatedProvider(server.Client())
		_, payload := plannedPayload(t, provider, baseConfiguration(repo, server))
		fixture.publishDestination(payload.Files)
		fixture.mu.Lock()
		delete(fixture.destinationFiles, payload.Files[0].Path)
		fixture.mu.Unlock()

		response, providerErr := provider.Reconcile(context.Background(), reconcileRequest(payload))
		if providerErr != nil || response.Result.State != protocol.ResultWaitingExternal || response.Result.ProviderState != "absent" {
			t.Fatalf("response=%+v err=%v", response, providerErr)
		}
	})

	t.Run("conflict", func(t *testing.T) {
		repo := newRepo(t)
		fixture, server := newAPIServer(t, "normal")
		defer server.Close()
		provider := authenticatedProvider(server.Client())
		_, payload := plannedPayload(t, provider, baseConfiguration(repo, server))
		fixture.publishDestination(payload.Files)
		fixture.mu.Lock()
		fixture.destinationFiles[payload.Files[0].Path] = payload.Files[0].Content + "# conflict\n"
		fixture.mu.Unlock()

		response, providerErr := provider.Reconcile(context.Background(), reconcileRequest(payload))
		if providerErr != nil || response.Result.State != protocol.ResultRejected || response.Result.ProviderState != "destination-conflict" {
			t.Fatalf("response=%+v err=%v", response, providerErr)
		}
	})
}

func TestPreviousPullRequestEvidenceRecoversAfterSourceBranchDeletion(t *testing.T) {
	repo := newRepo(t)
	fixture, server := newAPIServer(t, "normal")
	defer server.Close()
	provider := authenticatedProvider(server.Client())
	_, payload := plannedPayload(t, provider, baseConfiguration(repo, server))
	applied, providerErr := provider.Apply(context.Background(), applyRequest(payload))
	if providerErr != nil || applied.Result.State != protocol.ResultWaitingExternal {
		t.Fatalf("apply=%+v err=%v", applied, providerErr)
	}

	runExternalGit(t, "", "--git-dir", repo.remote, "update-ref", "-d", "refs/heads/"+payload.UpdateBranch)
	fixture.mu.Lock()
	fixture.state = "merged"
	fixture.checks = "passed"
	fixture.hidePullList = true
	fixture.mu.Unlock()

	request := reconcileRequest(payload)
	request.Previous = &applied.Result
	response, providerErr := provider.Reconcile(context.Background(), request)
	if providerErr != nil || response.Result.State != protocol.ResultWaitingExternal || response.Result.ProviderState != "merged-awaiting-destination" {
		t.Fatalf("response=%+v err=%v", response, providerErr)
	}
	var got evidence
	if err := json.Unmarshal(response.Result.Evidence, &got); err != nil {
		t.Fatal(err)
	}
	if got.PullRequestNumber != fixture.number || got.PullRequestState != "merged" {
		t.Fatalf("evidence=%+v", got)
	}
	fixture.mu.Lock()
	posts := fixture.postCount
	fixture.mu.Unlock()
	if posts != 1 {
		t.Fatalf("pull request submissions=%d", posts)
	}
}

func TestDestinationObservationErrorsPreserveCategories(t *testing.T) {
	for _, tc := range []struct {
		name          string
		commitStatus  int
		contentStatus int
		malformed     bool
		want          protocol.ErrorCode
	}{
		{name: "authentication", commitStatus: http.StatusUnauthorized, want: protocol.ErrorAuthentication},
		{name: "authorization", commitStatus: http.StatusForbidden, want: protocol.ErrorAuthorization},
		{name: "rate limit", commitStatus: http.StatusTooManyRequests, want: protocol.ErrorTransientExternal},
		{name: "malformed commit", malformed: true, want: protocol.ErrorPermanentExternal},
		{name: "content rate limit", contentStatus: http.StatusTooManyRequests, want: protocol.ErrorTransientExternal},
		{name: "content server error", contentStatus: http.StatusInternalServerError, want: protocol.ErrorTransientExternal},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := newRepo(t)
			fixture, server := newAPIServer(t, "normal")
			defer server.Close()
			provider := authenticatedProvider(server.Client())
			_, payload := plannedPayload(t, provider, baseConfiguration(repo, server))
			fixture.mu.Lock()
			fixture.destinationCommitStatus = tc.commitStatus
			fixture.destinationContentStatus = tc.contentStatus
			fixture.destinationMalformed = tc.malformed
			fixture.mu.Unlock()

			_, providerErr := provider.Reconcile(context.Background(), reconcileRequest(payload))
			if providerErr == nil || providerErr.Code != tc.want {
				t.Fatalf("err=%+v want=%s", providerErr, tc.want)
			}
		})
	}
}

func TestMissingCredentialFailsExecution(t *testing.T) {
	repo := newRepo(t)
	_, server := newAPIServer(t, "normal")
	defer server.Close()
	provider := Provider{Client: server.Client(), Getenv: func(string) string { return "" }}
	_, payload := plannedPayload(t, provider, baseConfiguration(repo, server))
	_, providerErr := provider.Apply(context.Background(), applyRequest(payload))
	if providerErr == nil || providerErr.Code != protocol.ErrorAuthentication {
		t.Fatalf("err=%v", providerErr)
	}
}
