package providerhost

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/aalsanie/distroplane/internal/planner"
	"github.com/aalsanie/distroplane/internal/protocol"
)

func TestOfficialGitProvidersExecuteThroughHost(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is required for provider host integration")
	}

	t.Run("homebrew", func(t *testing.T) {
		repository := newProviderHostRepository(t)
		executable := buildOfficialProvider(t, "distroplane-provider-homebrew")
		client, err := New(Options{})
		if err != nil {
			t.Fatal(err)
		}

		configuration, err := json.Marshal(map[string]any{
			"artifact":   "archive",
			"repository": repository,
			"branch":     "main",
			"path":       "Formula/acme.rb",
			"mode":       "direct",
			"authentication": map[string]any{
				"credential": "homebrew-token",
			},
			"manifest": map[string]any{
				"type":        "formula",
				"name":        "Acme",
				"description": "Acme command-line tool",
				"homepage":    "https://example.test/acme",
				"url":         "https://example.test/acme-1.2.3.tar.gz",
				"version":     "1.2.3",
				"binary":      "acme",
			},
		})
		if err != nil {
			t.Fatal(err)
		}
		plan, err := client.Plan(context.Background(), planner.Endpoint{Executable: executable}, protocol.PlanRequest{
			Release: protocol.Release{
				ID: "acme-1.2.3",
				Artifacts: []protocol.Artifact{{
					Name: "archive", Digest: "sha256:" + strings.Repeat("a", 64), Size: 123,
				}},
			},
			Target: protocol.Target{ID: "homebrew", Configuration: configuration},
		})
		if err != nil {
			t.Fatal(err)
		}
		if len(plan.Operations) != 1 {
			t.Fatalf("operations=%+v", plan.Operations)
		}

		token := "homebrew-provider-host-token"
		environment, err := client.processEnvironment([]string{"DISTROPLANE_HOMEBREW_TOKEN=" + token})
		if err != nil {
			t.Fatal(err)
		}
		response, err := client.applyWithOptions(context.Background(), executable, protocol.ApplyRequest{
			PlanID:          "plan-homebrew",
			TargetID:        "homebrew",
			OperationID:     plan.Operations[0].ID,
			IdempotencyKey:  "homebrew-key",
			Attempt:         1,
			ProviderPayload: plan.Operations[0].ProviderPayload,
		}, callOptions{environment: environment, redactions: [][]byte{[]byte(token)}})
		if err != nil {
			t.Fatal(err)
		}
		if response.Result.State != protocol.ResultPublished {
			t.Fatalf("result=%+v", response.Result)
		}
		manifest := runProviderHostGit(t, "", "--git-dir", repository, "show", "refs/heads/main:Formula/acme.rb")
		if !strings.Contains(manifest, "class Acme < Formula") {
			t.Fatalf("manifest=%s", manifest)
		}
	})

	t.Run("winget", func(t *testing.T) {
		repository := newProviderHostRepository(t)
		api := &providerHostWinGetAPI{token: "winget-provider-host-token"}
		server := httptest.NewServer(api)
		defer server.Close()

		executable := buildOfficialProvider(t, "distroplane-provider-winget")
		client, err := New(Options{})
		if err != nil {
			t.Fatal(err)
		}
		configuration, err := json.Marshal(map[string]any{
			"artifact":   "installer",
			"repository": repository,
			"branch":     "main",
			"authentication": map[string]any{
				"credential": "winget-token",
			},
			"package": map[string]any{
				"identifier":       "Example.Acme",
				"version":          "1.2.3",
				"publisher":        "Example",
				"name":             "Acme",
				"license":          "Apache-2.0",
				"shortDescription": "Acme command-line tool",
			},
			"installer": map[string]any{
				"architecture": "x64",
				"type":         "msi",
				"url":          "https://example.test/acme-1.2.3.msi",
			},
			"pullRequest": map[string]any{
				"api":               server.URL,
				"repository":        "microsoft/winget-pkgs",
				"headOwner":         "example",
				"allowInsecureHTTP": true,
			},
		})
		if err != nil {
			t.Fatal(err)
		}
		plan, err := client.Plan(context.Background(), planner.Endpoint{Executable: executable}, protocol.PlanRequest{
			Release: protocol.Release{
				ID: "acme-1.2.3",
				Artifacts: []protocol.Artifact{{
					Name: "installer", Digest: "sha256:" + strings.Repeat("b", 64), Size: 456,
				}},
			},
			Target: protocol.Target{ID: "winget", Configuration: configuration},
		})
		if err != nil {
			t.Fatal(err)
		}
		if len(plan.Operations) != 1 {
			t.Fatalf("operations=%+v", plan.Operations)
		}

		environment, err := client.processEnvironment([]string{"DISTROPLANE_WINGET_TOKEN=" + api.token})
		if err != nil {
			t.Fatal(err)
		}
		response, err := client.applyWithOptions(context.Background(), executable, protocol.ApplyRequest{
			PlanID:          "plan-winget",
			TargetID:        "winget",
			OperationID:     plan.Operations[0].ID,
			IdempotencyKey:  "winget-key",
			Attempt:         1,
			ProviderPayload: plan.Operations[0].ProviderPayload,
		}, callOptions{environment: environment, redactions: [][]byte{[]byte(api.token)}})
		if err != nil {
			t.Fatal(err)
		}
		if response.Result.State != protocol.ResultWaitingExternal || response.Result.ProviderState != "submitted" {
			t.Fatalf("result=%+v", response.Result)
		}
		var evidence struct {
			UpdateBranch string `json:"updateBranch"`
		}
		if err := json.Unmarshal(response.Result.Evidence, &evidence); err != nil {
			t.Fatal(err)
		}
		if evidence.UpdateBranch == "" {
			t.Fatalf("evidence=%s", response.Result.Evidence)
		}
		runProviderHostGit(t, "", "--git-dir", repository, "show-ref", "--verify", "refs/heads/"+evidence.UpdateBranch)

		api.mu.Lock()
		posts := api.posts
		api.mu.Unlock()
		if posts != 1 {
			t.Fatalf("pull request submissions=%d", posts)
		}
	})
}

func buildOfficialProvider(t testing.TB, commandName string) string {
	t.Helper()
	name := commandName
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	output := filepath.Join(t.TempDir(), name)
	command := exec.Command("go", "build", "-o", output, "../../cmd/"+commandName)
	if combined, err := command.CombinedOutput(); err != nil {
		t.Fatalf("build %s: %v\n%s", commandName, err, combined)
	}
	return output
}

func newProviderHostRepository(t testing.TB) string {
	t.Helper()
	root := t.TempDir()
	bare := filepath.Join(root, "remote.git")
	runProviderHostGit(t, "", "init", "--bare", bare)
	seed := filepath.Join(root, "seed")
	runProviderHostGit(t, "", "init", seed)
	runProviderHostGit(t, seed, "config", "user.name", "Distroplane Test")
	runProviderHostGit(t, seed, "config", "user.email", "test@example.com")
	if err := os.WriteFile(filepath.Join(seed, "README.md"), []byte("provider host integration\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runProviderHostGit(t, seed, "add", "README.md")
	runProviderHostGit(t, seed, "commit", "-m", "seed")
	runProviderHostGit(t, seed, "branch", "-M", "main")
	runProviderHostGit(t, seed, "remote", "add", "origin", bare)
	runProviderHostGit(t, seed, "push", "-u", "origin", "main")
	return bare
}

func runProviderHostGit(t testing.TB, dir string, args ...string) string {
	t.Helper()
	command := exec.Command("git", args...)
	command.Dir = dir
	command.Env = append(os.Environ(),
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_CONFIG_GLOBAL="+os.DevNull,
		"GIT_CONFIG_SYSTEM="+os.DevNull,
	)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", args, err, output)
	}
	return strings.TrimSpace(string(output))
}

type providerHostWinGetAPI struct {
	mu     sync.Mutex
	token  string
	posts  int
	branch string
	base   string
}

func (f *providerHostWinGetAPI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Authorization") != "Bearer "+f.token {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	if r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/commits/") {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"sha":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}`)
		return
	}
	if r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/contents/") {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	if !strings.HasSuffix(r.URL.Path, "/pulls") {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	switch r.Method {
	case http.MethodGet:
		f.mu.Lock()
		branch, base := f.branch, f.base
		f.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if branch == "" {
			_, _ = io.WriteString(w, "[]")
			return
		}
		_, _ = fmt.Fprintf(w, `[{"number":42,"html_url":"https://example.test/pr/42","state":"open","merged_at":null,"head":{"ref":%q,"sha":"0123456789abcdef0123456789abcdef01234567"},"base":{"ref":%q}}]`, branch, base)
	case http.MethodPost:
		var body map[string]string
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		_, branch, ok := strings.Cut(body["head"], ":")
		if !ok {
			branch = body["head"]
		}
		f.mu.Lock()
		f.posts++
		f.branch = branch
		f.base = body["base"]
		f.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = fmt.Fprintf(w, `{"number":42,"html_url":"https://example.test/pr/42","state":"open","merged_at":null,"head":{"ref":%q,"sha":"0123456789abcdef0123456789abcdef01234567"},"base":{"ref":%q}}`, branch, body["base"])
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}
