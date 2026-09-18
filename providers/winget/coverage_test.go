package winget

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

func TestConfigurationValidationSurfaces(t *testing.T) {
	repo := newRepo(t)
	_, server := newAPIServer(t, "normal")
	defer server.Close()
	valid := baseConfiguration(repo, server)
	valid.UpdateBranch = ""
	valid.Branch = ""
	valid.ManifestRoot = ""
	valid.ManifestVersion = ""
	valid.Package.DefaultLocale = ""
	valid.Commit = commitConfig{}
	valid.PullRequest.Title = ""
	raw, _ := json.Marshal(valid)
	cfg, err := parseConfiguration(raw)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Branch != defaultBranch || cfg.ManifestRoot != defaultManifestRoot || cfg.ManifestVersion != defaultManifestVersion {
		t.Fatalf("defaults=%+v", cfg)
	}

	for name, mutate := range map[string]func(*configuration){
		"artifact":         func(c *configuration) { c.Artifact = "" },
		"repository":       func(c *configuration) { c.Repository = "-bad" },
		"repository user":  func(c *configuration) { c.Repository = "https://user@example.test/repo" },
		"branch":           func(c *configuration) { c.Branch = "bad..branch" },
		"update branch":    func(c *configuration) { c.UpdateBranch = "bad branch" },
		"manifest root":    func(c *configuration) { c.ManifestRoot = "../escape" },
		"manifest version": func(c *configuration) { c.ManifestVersion = "2.0" },
		"identifier":       func(c *configuration) { c.Package.Identifier = "bad" },
		"version":          func(c *configuration) { c.Package.Version = "../bad" },
		"locale":           func(c *configuration) { c.Package.DefaultLocale = "bad locale" },
		"publisher":        func(c *configuration) { c.Package.Publisher = "" },
		"package url":      func(c *configuration) { c.Package.PackageURL = "ftp://example.test/a" },
		"architecture":     func(c *configuration) { c.Installer.Architecture = "sparc" },
		"installer type":   func(c *configuration) { c.Installer.Type = "script" },
		"installer url":    func(c *configuration) { c.Installer.URL = "file:///tmp/a" },
		"credential":       func(c *configuration) { c.Authentication.Credential = " bad " },
		"commit":           func(c *configuration) { c.Commit.AuthorEmail = "bad address" },
		"pr repository":    func(c *configuration) { c.PullRequest.Repository = "owner repo" },
		"pr api":           func(c *configuration) { c.PullRequest.API = "ftp://example.test" },
		"pr http": func(c *configuration) {
			c.PullRequest.API = "http://example.test"
			c.PullRequest.AllowInsecureHTTP = false
		},
		"pr head":  func(c *configuration) { c.PullRequest.HeadOwner = "bad/head" },
		"pr title": func(c *configuration) { c.PullRequest.Title = "bad\n" },
	} {
		t.Run(name, func(t *testing.T) {
			cfg := valid
			mutate(&cfg)
			raw, _ := json.Marshal(cfg)
			if _, err := parseConfiguration(raw); err == nil {
				t.Fatal("invalid configuration accepted")
			}
		})
	}
	if _, err := parseConfiguration(json.RawMessage(`{`)); err == nil {
		t.Fatal("malformed configuration accepted")
	}
	if err := decodeStrict([]byte(`{} {}`), &map[string]any{}); err == nil {
		t.Fatal("trailing JSON accepted")
	}
	if err := decodeStrict([]byte(`{} x`), &map[string]any{}); err == nil {
		t.Fatal("invalid trailing JSON accepted")
	}
}

func TestManifestRenderingAndHelperValidation(t *testing.T) {
	artifact := protocol.Artifact{Name: "installer", Digest: "sha256:" + strings.Repeat("a", 64), Size: 1}
	cfg := configuration{
		ManifestRoot: "manifests", ManifestVersion: "1.12.0",
		Package: packageConfig{
			Identifier: "Contoso.Demo.Tool", Version: "2026.09", DefaultLocale: "en-US",
			Publisher: "Contoso", Name: "Demo", License: "MIT", ShortDescription: "Demo",
		},
		Installer: installerConfig{Architecture: "arm64", Type: "portable", URL: "https://example.test/demo.exe"},
	}
	files, err := renderManifestSet(cfg, artifact)
	if err != nil || len(files) != 3 {
		t.Fatalf("files=%+v err=%v", files, err)
	}
	for _, file := range files {
		if !strings.HasPrefix(file.Path, "manifests/c/Contoso/Demo/Tool/2026.09/") {
			t.Fatalf("path=%q", file.Path)
		}
	}
	if manifestTreeHash(files) == "" || manifestTreeHash([]manifestFile{files[2], files[0], files[1]}) != manifestTreeHash(files) {
		t.Fatal("tree hash is not deterministic")
	}
	if !strings.Contains(yamlQuote(`a"b`), `\"`) {
		t.Fatal("yaml quoting failed")
	}
	if schemaHeader("version", "1.12.0") != "# yaml-language-server: $schema=https://aka.ms/winget-manifest.version.1.12.0.schema.json\n" {
		t.Fatal("schema header mismatch")
	}
	bad := artifact
	bad.Digest = "md5:bad"
	if _, err := renderManifestSet(cfg, bad); err == nil {
		t.Fatal("non-SHA256 artifact accepted")
	}
	if _, err := manifestDirectory("manifests", "invalid", "1"); err == nil {
		t.Fatal("invalid identifier accepted")
	}
	for _, value := range []string{"", "../a", "/a", `a\b`, "a\n"} {
		if _, err := normalizeRepoPath(value); err == nil {
			t.Fatalf("path accepted: %q", value)
		}
	}
	if value, err := normalizeRepoPath("a/../b"); err != nil || value != "b" {
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
	if validCredentialRef("") || validCredentialRef("bad ref") || !validCredentialRef("credential.ref") {
		t.Fatal("credential validation mismatch")
	}
	if branch := deterministicUpdateBranch("!!!", strings.Repeat("a", 64)); !strings.HasPrefix(branch, "distroplane/release-") {
		t.Fatalf("fallback branch slug mismatch: %q", branch)
	}
	long := deterministicUpdateBranch(strings.Repeat("A", 80), strings.Repeat("b", 64))
	if len(long) > 80 {
		t.Fatalf("branch too long: %q", long)
	}
}

func TestPayloadAndPlanErrors(t *testing.T) {
	provider := Provider{}
	if _, providerErr := provider.Plan(context.Background(), protocol.PlanRequest{}); providerErr == nil || providerErr.Code != protocol.ErrorProtocol {
		t.Fatalf("err=%v", providerErr)
	}
	repo := newRepo(t)
	_, server := newAPIServer(t, "normal")
	defer server.Close()
	req := planRequest(t, baseConfiguration(repo, server))
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

	cfg := baseConfiguration(repo, server)
	_, payload := plannedPayload(t, authenticatedProvider(server.Client()), cfg)
	raw, _ := json.Marshal(payload)
	if _, err := parsePayload(raw); err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*operationPayload){
		"missing":       func(p *operationPayload) { p.Repository = "" },
		"branch":        func(p *operationPayload) { p.Branch = "bad branch" },
		"update":        func(p *operationPayload) { p.UpdateBranch = "bad branch" },
		"files":         func(p *operationPayload) { p.Files = p.Files[:2] },
		"duplicate":     func(p *operationPayload) { p.Files[1].Path = p.Files[0].Path; p.TreeSHA256 = manifestTreeHash(p.Files) },
		"path":          func(p *operationPayload) { p.Files[0].Path = "../bad"; p.TreeSHA256 = manifestTreeHash(p.Files) },
		"content":       func(p *operationPayload) { p.Files[0].Content = "" },
		"tree":          func(p *operationPayload) { p.TreeSHA256 = strings.Repeat("0", 64) },
		"digest prefix": func(p *operationPayload) { p.ArtifactDigest = "md5:bad" },
		"digest hex":    func(p *operationPayload) { p.ArtifactDigest = "sha256:" + strings.Repeat("z", 64) },
		"manifest":      func(p *operationPayload) { p.ManifestVersion = "bad" },
	} {
		t.Run(name, func(t *testing.T) {
			value := payload
			value.Files = append([]manifestFile(nil), payload.Files...)
			mutate(&value)
			raw, _ := json.Marshal(value)
			if _, err := parsePayload(raw); err == nil {
				t.Fatal("invalid payload accepted")
			}
		})
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

func TestGitErrorClassificationEnvironmentAndRedaction(t *testing.T) {
	exit := gitExitError{Code: 7, Err: errors.New("exit")}
	if exit.Error() != "exit" || !errors.Is(exit, exit.Err) {
		t.Fatal("git exit contract")
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
		"cancelled": {cancelledContext(), errors.New("x"), "", protocol.ErrorCancelled},
		"timeout":   {expiredContext(), errors.New("x"), "", protocol.ErrorTimeout},
		"auth":      {context.Background(), errors.New("x"), "authentication failed " + testToken, protocol.ErrorAuthentication},
		"forbidden": {context.Background(), errors.New("x"), "permission denied", protocol.ErrorAuthorization},
		"transient": {context.Background(), errors.New("x"), "offline", protocol.ErrorTransientExternal},
	} {
		t.Run(name, func(t *testing.T) {
			got := classifyGitReadError(tc.ctx, tc.err, tc.stderr, testToken)
			if got.Code != tc.code || strings.Contains(got.Message, testToken) {
				t.Fatalf("got=%+v", got)
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
			got := classifyGitPushError(tc.err, testToken)
			if got.Code != tc.code || strings.Contains(got.Message, testToken) {
				t.Fatalf("got=%+v", got)
			}
		})
	}
	encoded := gitAuthorization(testToken)
	if strings.Contains(sanitizeGitOutput(encoded+" "+testToken, testToken), testToken) ||
		strings.Contains(sanitizeGitOutput(encoded, testToken), encoded) {
		t.Fatal("credential not redacted")
	}
	if len(sanitizeGitOutput(strings.Repeat("x", 3000), "")) != 2048 {
		t.Fatal("diagnostics not bounded")
	}
	t.Setenv("UNRELATED_SECRET", "should-not-pass")
	env := strings.Join(gitEnvironment("https://github.com/example/winget.git", testToken), "\n")
	if strings.Contains(env, "UNRELATED_SECRET") || strings.Contains(env, testToken) ||
		!strings.Contains(env, "GIT_CONFIG_KEY_0=http.https://github.com/example/winget.git/.extraHeader") {
		t.Fatalf("env=%s", env)
	}
	local := strings.Join(gitEnvironment(filepath.Join(t.TempDir(), "winget.git"), testToken), "\n")
	if strings.Contains(local, "GIT_CONFIG_VALUE_0=") {
		t.Fatalf("local env=%s", local)
	}
}

func cancelledContext() context.Context {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	return ctx
}

func expiredContext() context.Context {
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()
	return ctx
}

func TestGitHubClassifiersAndResponseDecoding(t *testing.T) {
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
			if got := classifyGitHubStatus(tc.status, tc.write); got.Code != tc.code {
				t.Fatalf("got=%+v", got)
			}
		})
	}
	if classifyGitHubReadError(cancelledContext()).Code != protocol.ErrorCancelled ||
		classifyGitHubReadError(expiredContext()).Code != protocol.ErrorTimeout ||
		classifyGitHubReadError(context.Background()).Code != protocol.ErrorTransientExternal {
		t.Fatal("read error classification mismatch")
	}
	if _, err := decodePullRequest(strings.NewReader("{")); err == nil {
		t.Fatal("malformed PR accepted")
	}
	if _, err := decodePullRequest(strings.NewReader(`{"number":0,"html_url":"x"}`)); err == nil {
		t.Fatal("invalid PR accepted")
	}
	value, err := decodePullRequest(strings.NewReader(`{"number":1,"html_url":"https://example.test/pr/1","head":{"sha":"abc"}}`))
	if err != nil || value.state != "open" || value.headSHA != "abc" {
		t.Fatalf("value=%+v err=%v", value, err)
	}
}

func TestGitHubStatusAndValidationFailures(t *testing.T) {
	payload := operationPayload{
		Branch: "main", UpdateBranch: "update",
		PullRequest: pullRequestConfig{API: "http://example.test", Repository: "owner/repo", HeadOwner: "owner"},
	}
	for name, status := range map[string]int{
		"auth": http.StatusUnauthorized, "forbidden": http.StatusForbidden, "rate": http.StatusTooManyRequests,
		"server": http.StatusInternalServerError, "bad": http.StatusBadRequest,
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
				t.Fatal("submit status accepted")
			}
			pr := pullRequestObservation{exists: true, headSHA: "abc"}
			if _, providerErr := provider.lookupValidation(context.Background(), value, pr, testToken); providerErr == nil {
				t.Fatal("validation status accepted")
			}
		})
	}
	malformed := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.Contains(r.URL.Path, "/check-runs") {
			_, _ = w.Write([]byte("{"))
			return
		}
		if r.Method == http.MethodPost {
			w.WriteHeader(http.StatusCreated)
		}
		_, _ = w.Write([]byte("{"))
	}))
	defer malformed.Close()
	payload.PullRequest.API = malformed.URL
	provider := Provider{Client: malformed.Client()}
	if _, err := provider.lookupPullRequest(context.Background(), payload, testToken); err == nil || err.Code != protocol.ErrorPermanentExternal {
		t.Fatalf("lookup err=%v", err)
	}
	if _, err := provider.submitPullRequest(context.Background(), payload, testToken); err == nil || err.Code != protocol.ErrorPermanentExternal {
		t.Fatalf("submit err=%v", err)
	}
	if _, err := provider.lookupValidation(context.Background(), payload, pullRequestObservation{headSHA: "abc"}, testToken); err == nil || err.Code != protocol.ErrorPermanentExternal {
		t.Fatalf("validation err=%v", err)
	}
	if observed, err := provider.lookupValidation(context.Background(), payload, pullRequestObservation{}, testToken); err != nil || observed.state != "pending" {
		t.Fatalf("observed=%+v err=%v", observed, err)
	}
}

func TestRejectedSubmissionAndAbsentReconciliation(t *testing.T) {
	repo := newRepo(t)
	_, rejectedServer := newAPIServer(t, "rejected")
	defer rejectedServer.Close()
	provider := authenticatedProvider(rejectedServer.Client())
	_, payload := plannedPayload(t, provider, baseConfiguration(repo, rejectedServer))
	_, providerErr := provider.Apply(context.Background(), applyRequest(payload))
	if providerErr == nil || providerErr.Code != protocol.ErrorRejected {
		t.Fatalf("err=%v", providerErr)
	}

	freshRepo := newRepo(t)
	_, normalServer := newAPIServer(t, "normal")
	defer normalServer.Close()
	provider = authenticatedProvider(normalServer.Client())
	_, payload = plannedPayload(t, provider, baseConfiguration(freshRepo, normalServer))
	response, providerErr := provider.Reconcile(context.Background(), reconcileRequest(payload))
	if providerErr != nil || response.Result.State != protocol.ResultWaitingExternal || response.Result.ProviderState != "absent" {
		t.Fatalf("response=%+v err=%v", response, providerErr)
	}
	if _, err := provider.commitAndPush(context.Background(), payload, testToken); err != nil {
		t.Fatal(err)
	}
	response, providerErr = provider.Reconcile(context.Background(), reconcileRequest(payload))
	if providerErr != nil || response.Result.ProviderState != "branch-pushed" {
		t.Fatalf("response=%+v err=%v", response, providerErr)
	}
}

type missingGitRunner struct{}

func (missingGitRunner) Run(context.Context, string, []string, ...string) (gitOutput, error) {
	return gitOutput{}, exec.ErrNotFound
}

func TestMissingGitAndRepositoryPathProtection(t *testing.T) {
	repo := newRepo(t)
	_, server := newAPIServer(t, "normal")
	defer server.Close()
	provider := authenticatedProvider(server.Client())
	provider.Git = missingGitRunner{}
	_, payload := plannedPayload(t, provider, baseConfiguration(repo, server))
	_, providerErr := provider.Apply(context.Background(), applyRequest(payload))
	if providerErr == nil || providerErr.Code != protocol.ErrorConfiguration {
		t.Fatalf("err=%v", providerErr)
	}

	root := t.TempDir()
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "manifests")); err != nil {
		if errors.Is(err, os.ErrPermission) {
			t.Skip("symlink unavailable")
		}
		t.Fatal(err)
	}
	if err := writeRepositoryFile(root, "manifests/a/file.yaml", []byte("x")); err == nil {
		t.Fatal("symlink traversal accepted")
	}
	if _, err := readRepositoryFile(root, "manifests/a/file.yaml"); err == nil {
		t.Fatal("symlink traversal read accepted")
	}
}

func TestProviderDefaultsAndSmallHelpers(t *testing.T) {
	description, providerErr := (Provider{}).Describe(context.Background(), protocol.DescribeRequest{})
	if providerErr != nil || description.Provider.Version != DefaultVersion {
		t.Fatalf("description=%+v err=%v", description, providerErr)
	}
	if (Provider{}).client() != http.DefaultClient || (Provider{}).gitRunner() == nil {
		t.Fatal("default dependencies unavailable")
	}
	t.Setenv(TokenEnvironment, testToken)
	provider := Provider{Getenv: os.Getenv}
	if token, providerErr := provider.token(); providerErr != nil || token != testToken {
		t.Fatalf("token=%q err=%v", token, providerErr)
	}
	_ = os.Unsetenv(TokenEnvironment)
	if _, providerErr := provider.token(); providerErr == nil || providerErr.Code != protocol.ErrorAuthentication {
		t.Fatalf("err=%v", providerErr)
	}
	if _, err := selectArtifact([]protocol.Artifact{{Name: "a"}}, "b"); err == nil {
		t.Fatal("missing artifact accepted")
	}
	sum := sha256.Sum256([]byte("x"))
	if hex.EncodeToString(sum[:]) == "" {
		t.Fatal("hash helper failed")
	}
}
