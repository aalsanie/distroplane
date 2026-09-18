package sdkman

import (
	"context"
	"testing"

	"github.com/aalsanie/distroplane/internal/protocol"
)

func TestDuplicateReleaseIsIdempotent(t *testing.T) {
	fixture, server := newAPI(t, "normal", "normal")
	defer server.Close()
	provider := providerFor(server)
	payload := plannedPayload(t, provider, server.URL, nil)

	first, providerErr := provider.Apply(context.Background(), applyRequest(payload))
	if providerErr != nil || first.Result.State != protocol.ResultPublished {
		t.Fatalf("first=%+v err=%v", first, providerErr)
	}
	second, providerErr := provider.Apply(context.Background(), applyRequest(payload))
	if providerErr != nil || second.Result.State != protocol.ResultPublished {
		t.Fatalf("second=%+v err=%v", second, providerErr)
	}

	fixture.mu.Lock()
	releases := fixture.releases
	fixture.mu.Unlock()
	if releases != 2 {
		t.Fatalf("release calls=%d", releases)
	}
}
