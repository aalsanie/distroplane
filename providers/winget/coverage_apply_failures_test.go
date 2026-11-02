package winget

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/aalsanie/distroplane/internal/protocol"
)

type absentThenFailGitRunner struct{}

func (absentThenFailGitRunner) Run(_ context.Context, _ string, _ []string, args ...string) (gitOutput, error) {
	if len(args) != 0 && args[0] == "ls-remote" {
		return gitOutput{}, gitExitError{Code: 2, Err: errors.New("branch not found")}
	}
	if len(args) != 0 && args[0] == "clone" {
		return gitOutput{Stderr: "network reset"}, errors.New("network reset")
	}
	return gitOutput{}, errors.New("unexpected git command")
}

func TestWinGetApplyFailsClosedOnUncertainSubmissionStages(t *testing.T) {
	for _, scenario := range []struct {
		name         string
		commitStatus int
		git          GitRunner
		code         protocol.ErrorCode
	}{
		{"destination unauthorized", http.StatusUnauthorized, nil, protocol.ErrorAuthentication},
		{"destination forbidden", http.StatusForbidden, nil, protocol.ErrorAuthorization},
		{"destination timeout", http.StatusRequestTimeout, nil, protocol.ErrorTransientExternal},
		{"missing Git", 0, missingGitRunner{}, protocol.ErrorConfiguration},
		{"lost clone", 0, absentThenFailGitRunner{}, protocol.ErrorAmbiguousOutcome},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			repo := newRepo(t)
			fixture, server := newAPIServer(t, "normal")
			defer server.Close()
			provider := authenticatedProvider(server.Client())
			provider.Git = scenario.git
			_, payload := plannedPayload(t, provider, baseConfiguration(repo, server))
			fixture.mu.Lock()
			fixture.destinationCommitStatus = scenario.commitStatus
			fixture.mu.Unlock()
			_, providerErr := provider.Apply(context.Background(), applyRequest(payload))
			if providerErr == nil || providerErr.Code != scenario.code {
				t.Fatalf("unexpected submission outcome: %+v, expected %s", providerErr, scenario.code)
			}
			fixture.mu.Lock()
			submissions := fixture.postCount
			fixture.mu.Unlock()
			if submissions != 0 {
				t.Fatalf("submission should not create review PR after failed preflight: %d", submissions)
			}
		})
	}
}
