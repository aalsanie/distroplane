package homebrew

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestGitEnvironmentAndBufferErrorBoundaries(t *testing.T) {
	for _, repository := range []string{
		"https://example.test/repo.git", "http://example.test/repo.git", "/tmp/example.git",
	} {
		env := strings.Join(gitEnvironment(repository, ""), "\n")
		if strings.Contains(env, "GIT_CONFIG_VALUE_0=") {
			t.Fatalf("credential header added to unauthenticated Git: %s", env)
		}
	}
	buf := &limitedBuffer{remaining: 20}
	if n, err := buf.Write([]byte("short")); err != nil || n != 5 || buf.String() != "short" {
		t.Fatalf("short diagnostic output rejected: %q n=%d err=%v", buf.String(), n, err)
	}
	if n, err := buf.Write([]byte("longer")); err != nil || n != 6 || buf.String() != "shortlonger" {
		t.Fatalf("bounded git output not accumulated correctly: %q n=%d err=%v", buf.String(), n, err)
	}
}

func TestGitOperationsFailWhenTemporaryWorkspaceUnavailable(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("TMPDIR has platform-specific precedence on Windows")
	}
	root := t.TempDir()
	blocked := filepath.Join(root, "blocked")
	if err := os.WriteFile(blocked, []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TMPDIR", blocked)
	payload := operationPayload{
		Repository: "/tmp/example.git", Branch: "main", UpdateBranch: "update",
		Path: "Formula/demo.rb", Content: "metadata",
	}
	if _, err := (Provider{Git: coverageGitRunner{}}).commitAndPush(
		context.Background(), payload, payload.UpdateBranch, "token"); err == nil {
		t.Fatal("commit accepted without a writable temporary workspace")
	}
	observer := Provider{Git: coverageObservationGit{mode: "exact",
		path: payload.Path, content: payload.Content,}}
	if _, err := observer.observeBranch(context.Background(), payload, payload.Branch, "token"); err == nil {
		t.Fatal("reconciliation accepted without a secure scratch workspace")
	}
}
