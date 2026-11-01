package winget

import (
	"context"
	"testing"

	"github.com/aalsanie/distroplane/internal/protocol"
)

func TestApplyDoesNotRepublishReviewedSubmission(t *testing.T) {
	for _, tc := range []struct {
		name string
		state string
		expected protocol.ResultState
		providerState string
	}{
		{"open", "open", protocol.ResultWaitingExternal, "submitted"},
		{"merged", "merged", protocol.ResultWaitingExternal, "merged-awaiting-destination"},
		{"closed", "closed", protocol.ResultRejected, "pull-request-closed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := newRepo(t)
			fixture, server := newAPIServer(t, "normal")
			defer server.Close()
			provider := authenticatedProvider(server.Client())
			_, payload := plannedPayload(t, provider, baseConfiguration(repo, server))
			first, err := provider.Apply(context.Background(), applyRequest(payload))
			if err != nil || first.Result.State != protocol.ResultWaitingExternal {
				t.Fatalf("initial apply: %+v err=%v", first, err)
			}
			fixture.mu.Lock()
			fixture.state = tc.state
			initialPosts := fixture.postCount
			fixture.mu.Unlock()
			second, err := provider.Apply(context.Background(), applyRequest(payload))
			if err != nil || second.Result.State != tc.expected || second.Result.ProviderState != tc.providerState {
				t.Fatalf("re-apply: %+v err=%v", second, err)
			}
			fixture.mu.Lock()
			posts := fixture.postCount
			fixture.mu.Unlock()
			if posts != initialPosts {
				t.Fatal("second apply created an extra pull request")
			}
		})
	}
}
