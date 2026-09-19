package planner

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/aalsanie/distroplane/internal/domain"
	"github.com/aalsanie/distroplane/internal/protocol"
)

func TestPlannerHandlesOneThousandTargets(t *testing.T) {
	const count = 1000
	targets := make([]string, count)
	for i := range targets {
		targets[i] = fmt.Sprintf(`{"id":"target-%04d","provider":{"name":"fake"},"configuration":{"index":%d}}`, i, i)
	}
	loaded := baseLoaded(t, baseConfig(strings.Join(targets, ",")))
	digest, err := domain.NewSHA256Digest(strings.Repeat("a", 64))
	if err != nil {
		t.Fatal(err)
	}
	var describeCalls int
	var planCalls int
	client := fakeClient{
		describe: func(context.Context, Endpoint) (protocol.DescribeResponse, error) {
			describeCalls++
			return describe("1.0.0", protocol.CapabilityPlan), nil
		},
		plan: func(context.Context, Endpoint, protocol.PlanRequest) (protocol.PlanResponse, error) {
			planCalls++
			return protocol.PlanResponse{}, nil
		},
	}
	planner, err := New(client, Options{
		Resolver: fakeResolver{path: "/provider"},
		Hasher:   fakeHasher{digest: digest, size: 1},
	})
	if err != nil {
		t.Fatal(err)
	}
	plan, err := planner.Build(context.Background(), loaded)
	if err != nil {
		t.Fatal(err)
	}
	if got := len(plan.Plan().Targets()); got != count {
		t.Fatalf("targets=%d want=%d", got, count)
	}
	if describeCalls != 1 {
		t.Fatalf("describe calls=%d want=1", describeCalls)
	}
	if planCalls != count {
		t.Fatalf("plan calls=%d want=%d", planCalls, count)
	}
}
