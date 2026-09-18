package planner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/aalsanie/distroplane/internal/config"
	"github.com/aalsanie/distroplane/internal/domain"
	"github.com/aalsanie/distroplane/internal/protocol"
)

func TestBuildDeterministicAcrossOrdering(t *testing.T) {
	d := mustDigest(t, 'a')
	p := newPlanner(t, describe("1", protocol.CapabilityReconcile, protocol.CapabilityApply, protocol.CapabilityPlan), planResponse(`{"z":2,"a":1}`), d)
	a := baseLoaded(t, baseConfig(`{"id":"b","provider":{"name":"fake"},"configuration":{"z":2,"a":1}},{"id":"a","provider":{"name":"fake"},"configuration":{"a":1,"z":2}}`))
	b := baseLoaded(t, baseConfig(`{"id":"a","provider":{"name":"fake"},"configuration":{"z":2,"a":1}},{"id":"b","provider":{"name":"fake"},"configuration":{"a":1,"z":2}}`))
	first, err := p.Build(context.Background(), a)
	if err != nil {
		t.Fatal(err)
	}
	second, err := p.Build(context.Background(), b)
	if err != nil {
		t.Fatal(err)
	}
	if first.ID() != second.ID() {
		t.Fatalf("IDs differ: %s %s", first.ID(), second.ID())
	}
	if len(first.Plan().Operations()) != 4 {
		t.Fatalf("operations=%d", len(first.Plan().Operations()))
	}
	seen := map[domain.OperationID]bool{}
	for _, op := range first.Plan().Operations() {
		if seen[op.ID()] {
			t.Fatal("duplicate global operation ID")
		}
		seen[op.ID()] = true
		if op.SideEffecting() && op.IdempotencyKey() == "" {
			t.Fatal("missing idempotency key")
		}
		if !op.SideEffecting() && op.IdempotencyKey() != "" {
			t.Fatal("unexpected idempotency key")
		}
	}
	if got := fmt.Sprint(first.document.Targets[0].RequiredCapabilities); got != "[plan apply reconcile]" {
		t.Fatalf("caps=%s", got)
	}
	if len(first.document.Targets[0].Requirements) != 2 {
		t.Fatalf("requirements=%d", len(first.document.Targets[0].Requirements))
	}
	if len(first.Plan().Targets()[0].Requirements()) != 2 {
		t.Fatal("domain target requirements were not preserved")
	}
}

func TestBuildIdentityChangesAndIgnoresSourcePath(t *testing.T) {
	base := baseLoaded(t, baseConfig(`{"id":"a","provider":{"name":"fake"},"configuration":{"x":1}}`))
	da, db := mustDigest(t, 'a'), mustDigest(t, 'b')
	refPlanner := newPlanner(t, describe("1", protocol.CapabilityPlan, protocol.CapabilityApply, protocol.CapabilityReconcile), planResponse(`{"x":1}`), da)
	ref, err := refPlanner.Build(context.Background(), base)
	if err != nil {
		t.Fatal(err)
	}
	variants := []*Planner{
		newPlanner(t, describe("2", protocol.CapabilityPlan, protocol.CapabilityApply, protocol.CapabilityReconcile), planResponse(`{"x":1}`), da),
		newPlanner(t, describe("1", protocol.CapabilityPlan, protocol.CapabilityApply, protocol.CapabilityReconcile), planResponse(`{"x":1}`), db),
		newPlanner(t, describe("1", protocol.CapabilityPlan, protocol.CapabilityApply, protocol.CapabilityReconcile), planResponse(`{"x":2}`), da),
	}
	for _, vp := range variants {
		got, e := vp.Build(context.Background(), base)
		if e != nil {
			t.Fatal(e)
		}
		if got.ID() == ref.ID() {
			t.Fatal("expected different plan ID")
		}
	}
	moved := base
	moved.BaseDir = "/different/root"
	same, err := refPlanner.Build(context.Background(), moved)
	if err != nil {
		t.Fatal(err)
	}
	if same.ID() != ref.ID() {
		t.Fatal("source path changed PlanID")
	}
	aBytes, _ := ref.Bytes()
	bBytes, _ := same.Bytes()
	if string(aBytes) == string(bBytes) {
		t.Fatal("source metadata lost")
	}
}

func TestBuildCachesDescribe(t *testing.T) {
	calls := 0
	d := mustDigest(t, 'd')
	c := fakeClient{describe: func(context.Context, Endpoint) (protocol.DescribeResponse, error) {
		calls++
		return describe("1", protocol.CapabilityPlan, protocol.CapabilityApply, protocol.CapabilityReconcile), nil
	}, plan: func(context.Context, Endpoint, protocol.PlanRequest) (protocol.PlanResponse, error) {
		return planResponse(`{}`), nil
	}}
	p, _ := New(c, Options{Resolver: fakeResolver{path: "/same"}, Hasher: fakeHasher{digest: d, size: 1}})
	loaded := baseLoaded(t, baseConfig(`{"id":"a","provider":{"name":"fake","executable":"/same/fake"},"configuration":{}},{"id":"b","provider":{"name":"fake","executable":"/same/fake"},"configuration":{}}`))
	if _, err := p.Build(context.Background(), loaded); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("describe calls=%d", calls)
	}
}

func TestBuildErrors(t *testing.T) {
	d := mustDigest(t, 'e')
	loaded := baseLoaded(t, baseConfig(`{"id":"a","provider":{"name":"fake"},"configuration":{}}`))
	if _, err := New(nil, Options{}); err == nil {
		t.Fatal("nil client accepted")
	}
	var zero Planner
	if _, err := zero.Build(context.Background(), loaded); err == nil {
		t.Fatal("zero planner accepted")
	}
	good := newPlanner(t, describe("1", protocol.CapabilityPlan, protocol.CapabilityApply, protocol.CapabilityReconcile), planResponse(`{}`), d)
	if _, err := good.Build(nil, loaded); err == nil {
		t.Fatal("nil context accepted")
	}
	rp, _ := New(fakeClient{describe: func(context.Context, Endpoint) (protocol.DescribeResponse, error) {
		return protocol.DescribeResponse{}, nil
	}, plan: func(context.Context, Endpoint, protocol.PlanRequest) (protocol.PlanResponse, error) {
		return protocol.PlanResponse{}, nil
	}}, Options{Resolver: fakeResolver{err: errors.New("resolve")}, Hasher: fakeHasher{digest: d, size: 1}})
	if _, err := rp.Build(context.Background(), loaded); err == nil {
		t.Fatal("resolver error ignored")
	}
	hp, _ := New(fakeClient{describe: func(context.Context, Endpoint) (protocol.DescribeResponse, error) {
		return describe("1", protocol.CapabilityPlan), nil
	}, plan: func(context.Context, Endpoint, protocol.PlanRequest) (protocol.PlanResponse, error) {
		return protocol.PlanResponse{}, nil
	}}, Options{Resolver: fakeResolver{path: "/p"}, Hasher: fakeHasher{err: errors.New("hash")}})
	if _, err := hp.Build(context.Background(), loaded); err == nil {
		t.Fatal("hash error ignored")
	}
	zp, _ := New(fakeClient{describe: func(context.Context, Endpoint) (protocol.DescribeResponse, error) {
		return describe("1", protocol.CapabilityPlan), nil
	}, plan: func(context.Context, Endpoint, protocol.PlanRequest) (protocol.PlanResponse, error) {
		return protocol.PlanResponse{}, nil
	}}, Options{Resolver: fakeResolver{path: "/p"}, Hasher: fakeHasher{size: 1}})
	if _, err := zp.Build(context.Background(), loaded); err == nil {
		t.Fatal("invalid digest accepted")
	}
	cases := []struct {
		name string
		desc protocol.DescribeResponse
		derr error
		resp protocol.PlanResponse
		perr error
	}{
		{"describe", protocol.DescribeResponse{}, errors.New("describe"), protocol.PlanResponse{}, nil},
		{"name", protocol.DescribeResponse{Provider: protocol.ProviderIdentity{Name: "other", Version: "1"}, ProtocolVersions: []string{"1"}, Capabilities: []protocol.Capability{protocol.CapabilityPlan}}, nil, protocol.PlanResponse{}, nil},
		{"version", protocol.DescribeResponse{Provider: protocol.ProviderIdentity{Name: "fake", Version: "1"}, ProtocolVersions: []string{"2"}, Capabilities: []protocol.Capability{protocol.CapabilityPlan}}, nil, protocol.PlanResponse{}, nil},
		{"plan-capability", describe("1", protocol.CapabilityApply), nil, protocol.PlanResponse{}, nil},
		{"plan-call", describe("1", protocol.CapabilityPlan), nil, protocol.PlanResponse{}, errors.New("plan")},
		{"apply-capability", describe("1", protocol.CapabilityPlan), nil, protocol.PlanResponse{Operations: []protocol.PlannedOperation{{ID: "x", Kind: "x", ProviderPayload: json.RawMessage(`{}`)}}}, nil},
		{"reconcile-capability", describe("1", protocol.CapabilityPlan, protocol.CapabilityApply), nil, protocol.PlanResponse{Operations: []protocol.PlannedOperation{{ID: "x", Kind: "x", SideEffecting: true, ProviderPayload: json.RawMessage(`{}`)}}}, nil},
		{"invalid-response", describe("1", protocol.CapabilityPlan), nil, protocol.PlanResponse{Operations: []protocol.PlannedOperation{{ID: "", Kind: "x", ProviderPayload: json.RawMessage(`{}`)}}}, nil},
		{"timeout", describe("1", protocol.CapabilityPlan, protocol.CapabilityApply), nil, protocol.PlanResponse{Operations: []protocol.PlannedOperation{{ID: "x", Kind: "x", TimeoutMillis: ^uint64(0), ProviderPayload: json.RawMessage(`{}`)}}}, nil},
		{"payload-duplicate", describe("1", protocol.CapabilityPlan, protocol.CapabilityApply), nil, protocol.PlanResponse{Operations: []protocol.PlannedOperation{{ID: "x", Kind: "x", ProviderPayload: json.RawMessage(`{"a":1,"a":2}`)}}}, nil},
		{"requirement-duplicate", protocol.DescribeResponse{Provider: protocol.ProviderIdentity{Name: "fake", Version: "1"}, ProtocolVersions: []string{"1"}, Capabilities: []protocol.Capability{protocol.CapabilityPlan}, Requirements: []protocol.Requirement{{Kind: "x", Name: "y", Metadata: json.RawMessage(`{"a":1,"a":2}`)}}}, nil, protocol.PlanResponse{}, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := fakeClient{describe: func(context.Context, Endpoint) (protocol.DescribeResponse, error) { return tc.desc, tc.derr }, plan: func(context.Context, Endpoint, protocol.PlanRequest) (protocol.PlanResponse, error) {
				return tc.resp, tc.perr
			}}
			p, _ := New(c, Options{Resolver: fakeResolver{path: "/p"}, Hasher: fakeHasher{digest: d, size: 1}})
			if _, err := p.Build(context.Background(), loaded); err == nil {
				t.Fatal("expected error")
			}
		})
	}
}

func TestBuildRejectsOversizedIDs(t *testing.T) {
	d := mustDigest(t, '9')
	long := strings.Repeat("x", 300)
	p := newPlanner(t, describe("1", protocol.CapabilityPlan), protocol.PlanResponse{}, d)
	cfg, err := config.Decode(strings.NewReader(`{"schemaVersion":"1","release":{"id":"` + long + `","artifacts":[{"name":"a","source":"x"}]},"targets":[{"id":"t","provider":{"name":"fake"},"configuration":{}}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.Build(context.Background(), config.Loaded{Config: cfg, BaseDir: "/work"}); err == nil {
		t.Fatal("oversized release ID accepted")
	}
	cfg, err = config.Decode(strings.NewReader(`{"schemaVersion":"1","release":{"id":"r","artifacts":[{"name":"a","source":"x"}]},"targets":[{"id":"` + long + `","provider":{"name":"fake"},"configuration":{}}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.Build(context.Background(), config.Loaded{Config: cfg, BaseDir: "/work"}); err == nil {
		t.Fatal("oversized target ID accepted")
	}
}
