package winget

import (
	"context"
	"errors"
	"os"
	"path/filepath"
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
		if g.mode != "missing" {
			dir := args[len(args)-1]
			for _, file := range g.files {
				if err := os.MkdirAll(filepath.Dir(filepath.Join(dir, file.Path)), 0o700); err != nil {
					return gitOutput{}, err
				}
				content := file.Content
				if g.mode == "changed" {
					content = "different"
				}
				if err := os.WriteFile(filepath.Join(dir, file.Path), []byte(content), 0o600); err != nil {
					return gitOutput{}, err
				}
			}
		}
		return gitOutput{}, nil
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
