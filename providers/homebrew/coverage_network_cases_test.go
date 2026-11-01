package homebrew

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/aalsanie/distroplane/internal/protocol"
)

type missingRemoteAndFailClone struct{}

func (missingRemoteAndFailClone) Run(_ context.Context, _ string, _ []string, args ...string) (gitOutput, error) {
	if len(args) != 0 && args[0] == "ls-remote" {
		return gitOutput{}, gitExitError{Code: 2, Err: errors.New("remote branch absent")}
	}
	return gitOutput{}, errors.New("remote clone unavailable")
}

func TestHomebrewPublishNeverAssumesSuccessfulPushAfterNetworkLoss(t *testing.T) {
	p := Provider{Git: missingRemoteAndFailClone{}}
	payload := operationPayload{
		Mode: "pull-request", Repository: "/tmp/homebrew-tap.git",
		Branch: "main", UpdateBranch: "review",
		Path: "Formula/tool.rb", Content: "metadata",
	}
	if _, err := p.applyPullRequest(context.Background(), payload, "token"); err == nil ||
		err.Code != protocol.ErrorAmbiguousOutcome {
		t.Fatalf("lost submission incorrectly classified: %+v", err)
	}
	if _, err := p.applyDirect(context.Background(), payload, "token", branchObservation{exists: true}); err == nil ||
		err.Code != protocol.ErrorAmbiguousOutcome {
		t.Fatalf("lost direct push incorrectly classified: %+v", err)
	}
}

func TestHomebrewGitHubRequestsRespectCancellationAndTimeouts(t *testing.T) {
	payload := operationPayload{Mode: "pull-request", Branch: "main", UpdateBranch: "review",
		PullRequest: pullRequestConfig{API: "http://127.0.0.1:1", Repository: "owner/repo"}}
	cancelled, stop := context.WithCancel(context.Background())
	stop()
	expired, cancel := context.WithDeadline(context.Background(), time.Unix(0, 0))
	defer cancel()
	for _, tc := range []struct {
		name      string
		ctx       context.Context
		readCode  protocol.ErrorCode
		writeCode protocol.ErrorCode
	}{
		{"cancelled", cancelled, protocol.ErrorCancelled, protocol.ErrorCancelled},
		{"timeout", expired, protocol.ErrorTimeout, protocol.ErrorAmbiguousOutcome},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := (Provider{}).lookupPullRequest(tc.ctx, payload, "token"); err == nil ||
				err.Code != tc.readCode {
				t.Fatalf("GET error: %+v", err)
			}
			if _, err := (Provider{}).submitPullRequest(tc.ctx, payload, "token"); err == nil ||
				err.Code != tc.writeCode {
				t.Fatalf("POST error: %+v", err)
			}
		})
	}
	payload.PullRequest.API = "http://invalid\x00url"
	if _, err := (Provider{}).lookupPullRequest(context.Background(), payload, "token"); err == nil ||
		err.Code != protocol.ErrorProviderInternal {
		t.Fatalf("malformed lookup endpoint accepted: %+v", err)
	}
	if _, err := (Provider{}).submitPullRequest(context.Background(), payload, "token"); err == nil ||
		err.Code != protocol.ErrorProviderInternal {
		t.Fatalf("malformed submission endpoint accepted: %+v", err)
	}
}
