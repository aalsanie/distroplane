package winget

import (
	"context"
	"testing"
	"time"

	"github.com/aalsanie/distroplane/internal/protocol"
)

func TestWinGetGitHubReadAndSubmissionFailuresPreserveCertainty(t *testing.T) {
	payload := operationPayload{Branch: "main", UpdateBranch: "review",
		PullRequest: pullRequestConfig{API: "http://127.0.0.1:1", Repository: "owner/repo", HeadOwner: "owner"}}
	cancelled, stop := context.WithCancel(context.Background())
	stop()
	expired, cancel := context.WithDeadline(context.Background(), time.Unix(0, 0))
	defer cancel()
	for _, scenario := range []struct {
		name string
		ctx context.Context
		readCode protocol.ErrorCode
		writeCode protocol.ErrorCode
	}{
		{"cancelled", cancelled, protocol.ErrorCancelled, protocol.ErrorCancelled},
		{"timeout", expired, protocol.ErrorTimeout, protocol.ErrorAmbiguousOutcome},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			provider := Provider{}
			if _, err := provider.resolveDestinationCommit(scenario.ctx, payload, "token"); err == nil ||
				err.Code != scenario.readCode {
				t.Fatalf("destination lookup: %+v", err)
			}
			if _, _, err := provider.readDestinationFile(scenario.ctx, payload, "manifests/package.yaml", "abc", "token"); err == nil ||
				err.Code != scenario.readCode {
				t.Fatalf("manifest lookup: %+v", err)
			}
			if _, err := provider.lookupPullRequest(scenario.ctx, payload, "token"); err == nil ||
				err.Code != scenario.readCode {
				t.Fatalf("pull request lookup: %+v", err)
			}
			if _, err := provider.lookupValidation(scenario.ctx, payload, pullRequestObservation{headSHA: "abc"}, "token"); err == nil ||
				err.Code != scenario.readCode {
				t.Fatalf("validation lookup: %+v", err)
			}
			if _, err := provider.submitPullRequest(scenario.ctx, payload, "token"); err == nil ||
				err.Code != scenario.writeCode {
				t.Fatalf("pull request submission: %+v", err)
			}
		})
	}
}

func TestWinGetGitHubRequestConstructionRejectsUnsafeEndpoints(t *testing.T) {
	p := Provider{}
	payload := operationPayload{Branch: "main", UpdateBranch: "review",
		PullRequest: pullRequestConfig{API: "http://bad\x00url", Repository: "owner/repo", HeadOwner: "owner"}}
	if _, err := p.resolveDestinationCommit(context.Background(), payload, "token"); err == nil ||
		err.Code != protocol.ErrorProviderInternal {
		t.Fatalf("unsafe commit endpoint was accepted: %+v", err)
	}
	if _, _, err := p.readDestinationFile(context.Background(), payload, "manifest.yaml", "commit", "token"); err == nil ||
		err.Code != protocol.ErrorProviderInternal {
		t.Fatalf("unsafe content endpoint was accepted: %+v", err)
	}
	if _, err := p.lookupPullRequest(context.Background(), payload, "token"); err == nil ||
		err.Code != protocol.ErrorProviderInternal {
		t.Fatalf("unsafe PR endpoint was accepted: %+v", err)
	}
	if _, err := p.lookupValidation(context.Background(), payload, pullRequestObservation{headSHA: "abc"}, "token"); err == nil ||
		err.Code != protocol.ErrorProviderInternal {
		t.Fatalf("unsafe validation endpoint was accepted: %+v", err)
	}
	if _, err := p.submitPullRequest(context.Background(), payload, "token"); err == nil ||
		err.Code != protocol.ErrorProviderInternal {
		t.Fatalf("unsafe submission endpoint was accepted: %+v", err)
	}
}
