package planner

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aalsanie/distroplane/internal/config"
	"github.com/aalsanie/distroplane/internal/domain"
	"github.com/aalsanie/distroplane/internal/protocol"
)

type pathDigestHasher struct {
	artifact domain.Digest
	provider domain.Digest
}

func (h pathDigestHasher) Hash(path string) (domain.Digest, int64, error) {
	if strings.Contains(filepath.Base(path), "provider") {
		return h.provider, 7, nil
	}
	return h.artifact, 3, nil
}

func providerDigestPlanner(t testing.TB, providerDigest domain.Digest) (*Planner, config.Loaded) {
	t.Helper()
	artifactDigest, err := domain.NewSHA256Digest(strings.Repeat("a", 64))
	if err != nil {
		t.Fatal(err)
	}
	client := fakeClient{
		describe: func(context.Context, Endpoint) (protocol.DescribeResponse, error) {
			return describe("1.0.0", protocol.CapabilityPlan, protocol.CapabilityApply, protocol.CapabilityReconcile), nil
		},
		plan: func(context.Context, Endpoint, protocol.PlanRequest) (protocol.PlanResponse, error) {
			return protocol.PlanResponse{Operations: []protocol.PlannedOperation{{
				ID: "publish", Kind: "publish", SideEffecting: true, ProviderPayload: json.RawMessage(`{}`),
			}}}, nil
		},
	}
	p, err := New(client, Options{
		Resolver: fakeResolver{path: filepath.Join(string(filepath.Separator), "provider")},
		Hasher:   pathDigestHasher{artifact: artifactDigest, provider: providerDigest},
	})
	if err != nil {
		t.Fatal(err)
	}
	loaded := baseLoaded(t, baseConfig(`{"id":"target","provider":{"name":"fake"},"configuration":{}}`))
	return p, loaded
}

func TestBuildBindsProviderExecutableDigest(t *testing.T) {
	providerDigest, err := domain.NewSHA256Digest(strings.Repeat("b", 64))
	if err != nil {
		t.Fatal(err)
	}
	p, loaded := providerDigestPlanner(t, providerDigest)
	plan, err := p.Build(context.Background(), loaded)
	if err != nil {
		t.Fatal(err)
	}
	refName, _ := domain.NewProviderName("fake")
	refVersion, _ := domain.NewProviderVersion("1.0.0")
	ref, _ := domain.NewProviderRef(refName, refVersion)
	if got := plan.ProviderDigests()[ref]; got != providerDigest {
		t.Fatalf("provider digest=%q want=%q", got.String(), providerDigest.String())
	}

	data, err := plan.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := Decode(strings.NewReader(string(data)))
	if err != nil {
		t.Fatal(err)
	}
	if got := decoded.ProviderDigests()[ref]; got != providerDigest {
		t.Fatalf("decoded provider digest=%q want=%q", got.String(), providerDigest.String())
	}
}

func TestProviderExecutableDigestAffectsPlanIdentity(t *testing.T) {
	firstDigest, _ := domain.NewSHA256Digest(strings.Repeat("b", 64))
	secondDigest, _ := domain.NewSHA256Digest(strings.Repeat("c", 64))
	firstPlanner, loaded := providerDigestPlanner(t, firstDigest)
	secondPlanner, _ := providerDigestPlanner(t, secondDigest)

	first, err := firstPlanner.Build(context.Background(), loaded)
	if err != nil {
		t.Fatal(err)
	}
	second, err := secondPlanner.Build(context.Background(), loaded)
	if err != nil {
		t.Fatal(err)
	}
	if first.ID() == second.ID() {
		t.Fatal("provider executable replacement did not change plan identity")
	}
}


func TestVerifyProviderDigestsRejectsLegacyExecutionPlan(t *testing.T) {
	providerDigest, _ := domain.NewSHA256Digest(strings.Repeat("b", 64))
	p, loaded := providerDigestPlanner(t, providerDigest)
	plan, err := p.Build(context.Background(), loaded)
	if err != nil {
		t.Fatal(err)
	}
	if err := plan.VerifyProviderDigests(); err != nil {
		t.Fatalf("digest-bound plan rejected: %v", err)
	}

	plan.document.Targets[0].Provider.Digest = ""
	for i := range plan.document.Operations {
		plan.document.Operations[i].Provider.Digest = ""
	}
	if err := plan.VerifyProviderDigests(); err == nil || !strings.Contains(err.Error(), "create a new plan") {
		t.Fatalf("legacy execution plan accepted: %v", err)
	}
}
