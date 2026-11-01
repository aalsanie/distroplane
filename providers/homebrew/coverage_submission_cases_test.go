package homebrew

import (
	"context"
	"testing"

	"github.com/aalsanie/distroplane/internal/protocol"
)

func TestApplyReusesReviewedPullRequestAndNeverDuplicatesSubmission(t *testing.T) {
	for _, state := range []string{"open", "closed"} {
		t.Run(state, func(t *testing.T) {
			repo := newRepo(t)
			fixture, server := newPRServer(t, "normal")
			defer server.Close()
			provider := authenticatedProvider(server.Client())
			_, payload := plannedPayload(t, provider, prConfiguration(repo, server))
			first, err := provider.Apply(context.Background(), applyRequest(payload))
			if err != nil || first.Result.State != protocol.ResultWaitingExternal {
				t.Fatalf("initial publish: %+v err=%v", first, err)
			}
			fixture.mu.Lock()
			fixture.state = state
			fixture.mu.Unlock()
			next, err := provider.Apply(context.Background(), applyRequest(payload))
			if err != nil {
				t.Fatalf("reuse submission: %v", err)
			}
			want := protocol.ResultWaitingExternal
			if state == "closed" {
				want = protocol.ResultRejected
			}
			if next.Result.State != want {
				t.Fatalf("state=%s want=%s", next.Result.State, want)
			}
		})
	}
}
