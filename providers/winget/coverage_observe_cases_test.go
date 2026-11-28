package winget

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
)

type coverageObservationGit struct {
	mode  string
	files []manifestFile
}

func (g coverageObservationGit) Run(_ context.Context, _ string, _ []string, args ...string) (gitOutput, error) {
	switch args[0] {
	case "ls-remote":
		if g.mode == "absent" {
			return gitOutput{}, gitExitError{Code: 2, Err: errors.New("not found")}
		}
		if g.mode == "unavailable" {
			return gitOutput{Stderr: "network unavailable"}, errors.New("network unavailable")
		}
		if g.mode == "malformed" {
			return gitOutput{Stdout: "\n"}, nil
		}
		return gitOutput{Stdout: strings.Repeat("a", 40) + "\trefs/heads/main\n"}, nil
	case "clone":
		if g.mode == "clone-failure" {
			return gitOutput{Stderr: "authentication failed"}, errors.New("clone failed")
		}
		return gitOutput{}, nil
	case "rev-parse":
		if g.mode == "revision-failure" {
			return gitOutput{}, errors.New("revision failed")
		}
		if g.mode == "bad-revision" {
			return gitOutput{Stdout: "bad"}, nil
		}
		if g.mode == "moved" {
			return gitOutput{Stdout: strings.Repeat("b", 40) + "\n"}, nil
		}
		return gitOutput{Stdout: strings.Repeat("a", 40) + "\n"}, nil
	case "ls-tree":
		if g.mode == "tree-failure" {
			return gitOutput{}, errors.New("tree failed")
		}
		if g.mode == "missing" {
			return gitOutput{}, nil
		}
		if g.mode == "bad-tree" {
			return gitOutput{Stdout: "bad"}, nil
		}
		path, content := g.files[0].Path, g.files[0].Content
		mode := "100644"
		if g.mode == "symlink" {
			mode = "120000"
		}
		if g.mode == "wrong-size" {
			content += "extra"
		}
		return gitOutput{Stdout: fmt.Sprintf("%s blob %s %d\t%s\x00", mode, strings.Repeat("c", 40), len(content), path)}, nil
	case "cat-file":
		if g.mode == "blob-failure" {
			return gitOutput{}, errors.New("blob failed")
		}
		if g.mode == "changed" {
			return gitOutput{Stdout: "different"}, nil
		}
		return gitOutput{Stdout: g.files[0].Content}, nil
	default:
		return gitOutput{}, errors.New("unexpected git command")
	}
}

func TestObserveBranchDistinguishesMissingConflictAndExactManifest(t *testing.T) {
	payload := operationPayload{
		Repository: "https://example.test/repo.git", Branch: "main",
		Files: []manifestFile{{Path: "manifests/c/demo.yaml", Content: "manifest"}},
	}

	for _, tc := range []struct {
		mode                  string
		exists, exact, failed bool
	}{
		{"absent", false, false, false},
		{"unavailable", false, false, true},
		{"malformed", false, false, true},
		{"clone-failure", false, false, true},
		{"revision-failure", false, false, true},
		{"bad-revision", false, false, true},
		{"moved", false, false, true},
		{"tree-failure", false, false, true},
		{"bad-tree", false, false, true},
		{"symlink", false, false, true},
		{"wrong-size", true, false, false},
		{"blob-failure", false, false, true},
		{"missing", true, false, false},
		{"changed", true, false, false},
		{"exact", true, true, false},
	} {
		t.Run(tc.mode, func(t *testing.T) {
			provider := Provider{Git: coverageObservationGit{
				mode:  tc.mode,
				files: payload.Files,
			}}
			observed, err := provider.observeBranch(context.Background(), payload, payload.Branch, "token")
			if (err != nil) != tc.failed || observed.exists != tc.exists || observed.exact != tc.exact {
				t.Fatalf("observed=%+v err=%v", observed, err)
			}
			if observed.exists && observed.commit != strings.Repeat("a", 40) {
				t.Fatalf("missing source commit: %+v", observed)
			}
		})
	}
}
