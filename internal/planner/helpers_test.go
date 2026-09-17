package planner

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/aalsanie/distroplane/internal/config"
	"github.com/aalsanie/distroplane/internal/domain"
	"github.com/aalsanie/distroplane/internal/protocol"
)

type fakeClient struct {
	describe func(context.Context, Endpoint) (protocol.DescribeResponse, error)
	plan     func(context.Context, Endpoint, protocol.PlanRequest) (protocol.PlanResponse, error)
}

func (f fakeClient) Describe(ctx context.Context, e Endpoint) (protocol.DescribeResponse, error) {
	return f.describe(ctx, e)
}
func (f fakeClient) Plan(ctx context.Context, e Endpoint, r protocol.PlanRequest) (protocol.PlanResponse, error) {
	return f.plan(ctx, e, r)
}

type fakeResolver struct {
	path string
	err  error
}

func (r fakeResolver) Resolve(name, executable string) (string, error) {
	if r.err != nil {
		return "", r.err
	}
	if executable != "" {
		return executable, nil
	}
	return r.path + "-" + name, nil
}

type fakeHasher struct {
	digest domain.Digest
	size   int64
	err    error
}

func (h fakeHasher) Hash(string) (domain.Digest, int64, error) { return h.digest, h.size, h.err }

func mustDigest(t *testing.T, ch byte) domain.Digest {
	t.Helper()
	d, err := domain.NewSHA256Digest(strings.Repeat(string([]byte{ch}), 64))
	if err != nil {
		t.Fatal(err)
	}
	return d
}
func baseConfig(targets string) string {
	return `{"schemaVersion":"1","release":{"id":"v1","artifacts":[{"name":"app","source":"dist/app"}]},"targets":[` + targets + `]}`
}
func baseLoaded(t *testing.T, raw string) config.Loaded {
	t.Helper()
	cfg, err := config.Decode(strings.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	return config.Loaded{Config: cfg, BaseDir: "/work"}
}
func describe(version string, caps ...protocol.Capability) protocol.DescribeResponse {
	return protocol.DescribeResponse{Provider: protocol.ProviderIdentity{Name: "fake", Version: version}, ProtocolVersions: []string{protocol.Version}, Capabilities: caps, Requirements: []protocol.Requirement{{Kind: "network", Name: "registry", Metadata: json.RawMessage(`{"b":2,"a":1}`)}, {Kind: "network", Name: "registry", Metadata: json.RawMessage(`{"a":1,"b":2}`)}}}
}
func planResponse(payload string) protocol.PlanResponse {
	return protocol.PlanResponse{Operations: []protocol.PlannedOperation{{ID: "verify", Kind: "verify", Dependencies: []string{"publish"}, ProviderPayload: json.RawMessage(`{"ok":true}`)}, {ID: "publish", Kind: "publish", SideEffecting: true, TimeoutMillis: 5000, ProviderPayload: json.RawMessage(payload)}}, Requirements: []protocol.Requirement{{Kind: "credential", Name: "release", Metadata: json.RawMessage(`{"scope":"write"}`)}}}
}
func newPlanner(t *testing.T, desc protocol.DescribeResponse, response protocol.PlanResponse, digest domain.Digest) *Planner {
	t.Helper()
	c := fakeClient{describe: func(context.Context, Endpoint) (protocol.DescribeResponse, error) { return desc, nil }, plan: func(context.Context, Endpoint, protocol.PlanRequest) (protocol.PlanResponse, error) {
		return response, nil
	}}
	p, err := New(c, Options{Resolver: fakeResolver{path: "/provider"}, Hasher: fakeHasher{digest: digest, size: 3}})
	if err != nil {
		t.Fatal(err)
	}
	return p
}
