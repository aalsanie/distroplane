package homebrew

import (
	"context"
	"errors"
	"strings"
	"testing"
)

type coverageGitRunner struct {
	failure string
	change bool
}

func (r coverageGitRunner) Run(_ context.Context, _ string, _ []string, args ...string) (gitOutput, error) {
	name := args[0]
	if name == "-c" {
		name = "commit"
	}
	if name == r.failure {
		return gitOutput{Stderr: "controlled git failure"}, errors.New("controlled git failure")
	}
	if name == "diff" && r.change {
		return gitOutput{}, gitExitError{Code: 1, Err: errors.New("changes pending")}
	}
	if name == "rev-parse" {
		return gitOutput{Stdout: strings.Repeat("a", 40) + "\n"}, nil
	}
	return gitOutput{}, nil
}

func TestGitPublicationFailureBoundaries(t *testing.T) {
	payload := operationPayload{
		Repository: "https://example.test/tap.git", Branch: "main", UpdateBranch: "submission",
		Path: "Formula/demo.rb", Content: "class Demo; end\n",
	}
	for _, scenario := range []struct {
		name string
		fail string
		change bool
		shouldFail bool
	}{
		{"clone", "clone", false, true},
		{"stage", "add", false, true},
		{"inspect", "diff", false, true},
		{"unchanged", "", false, false},
		{"unchanged-revision", "rev-parse", false, true},
		{"commit", "commit", true, true},
		{"revision", "rev-parse", true, true},
		{"push", "push", true, true},
		{"changed", "", true, false},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			runner := coverageGitRunner{failure: scenario.fail, change: scenario.change}
			provider := Provider{Git: runner}
			commit, err := provider.commitAndPush(context.Background(), payload, payload.UpdateBranch, "token")
			if (err != nil) != scenario.shouldFail {
				t.Fatalf("commit=%q err=%v", commit, err)
			}
			if err == nil && commit != strings.Repeat("a", 40) {
				t.Fatalf("unexpected commit %q", commit)
			}
			if scenario.name == "push" && commit != strings.Repeat("a", 40) {
				t.Fatalf("push failure lost known commit: %q", commit)
			}
		})
	}
	invalid := payload
	invalid.Path = "../escape.rb"
	if _, err := (Provider{Git: coverageGitRunner{}}).commitAndPush(context.Background(), invalid, invalid.UpdateBranch, "token"); err == nil {
		t.Fatal("path traversal accepted before git add")
	}
}

func TestLimitedGitOutputPreservesCallerLength(t *testing.T) {
	buf := &limitedBuffer{remaining: 2}
	if n, err := buf.Write([]byte("abcdef")); err != nil || n != 6 || buf.String() != "ab" {
		t.Fatalf("truncated git stdout: %q n=%d err=%v", buf.String(), n, err)
	}
	if n, err := buf.Write([]byte("more")); err != nil || n != 4 || buf.String() != "ab" {
		t.Fatalf("bounded git output changed: %q n=%d err=%v", buf.String(), n, err)
	}
}
