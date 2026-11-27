package planner

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/aalsanie/distroplane/internal/domain"
	"github.com/aalsanie/distroplane/internal/protocol"
)

func TestNewPlanMaterializesDefaultOperationTimeout(t *testing.T) {
	loaded := baseLoaded(t, baseConfig("{\"id\":\"target\",\"provider\":{\"name\":\"fake\"},\"configuration\":{}}"))
	build := func(timeout uint64) DistributionPlan {
		t.Helper()
		response := protocol.PlanResponse{Operations: []protocol.PlannedOperation{{
			ID: "publish", Kind: "publish", SideEffecting: true, TimeoutMillis: timeout, ProviderPayload: json.RawMessage("{}"),
		}}}
		builder := newPlanner(t, describe("1", protocol.CapabilityPlan, protocol.CapabilityApply, protocol.CapabilityReconcile), response, mustDigest(t, 'a'))
		plan, err := builder.Build(context.Background(), loaded)
		if err != nil {
			t.Fatal(err)
		}
		return plan
	}
	implicit := build(0)
	explicit := build(uint64(domain.DefaultOperationTimeout / time.Millisecond))
	override := build(5000)
	if implicit.ID() != explicit.ID() || implicit.ID() == override.ID() {
		t.Fatal("timeout semantics not included in deterministic plan identity")
	}
	for _, tc := range []struct {
		plan DistributionPlan
		want time.Duration
	}{
		{implicit, 120 * time.Second},
		{explicit, 120 * time.Second},
		{override, 5 * time.Second},
	} {
		if got := tc.plan.Plan().Operations()[0].Timeout(); got != tc.want {
			t.Fatalf("timeout=%s want %s", got, tc.want)
		}
		if got := tc.plan.document.Operations[0].TimeoutMillis; got != uint64(tc.want/time.Millisecond) {
			t.Fatalf("stored timeout=%d", got)
		}
		raw, err := tc.plan.Bytes()
		if err != nil {
			t.Fatal(err)
		}
		decoded, err := Decode(bytes.NewReader(raw))
		if err != nil {
			t.Fatal(err)
		}
		if decoded.ID() != tc.plan.ID() || decoded.Plan().Operations()[0].Timeout() != tc.want ||
			decoded.Plan().Operations()[0].IdempotencyKey() != tc.plan.Plan().Operations()[0].IdempotencyKey() {
			t.Fatal("timeout or execution identity changed on load")
		}
	}

	legacy := implicit.document
	legacy.Operations[0].TimeoutMillis = 0
	id, err := semanticPlanID(semanticPlan{
		SchemaVersion: legacy.SchemaVersion, ProtocolVersion: legacy.ProtocolVersion,
		Release: semanticReleaseFromDomain(implicit.Plan().Release()), Targets: legacy.Targets,
		Operations: semanticOperationsFromDocument(legacy.Operations),
	})
	if err != nil {
		t.Fatal(err)
	}
	legacy.PlanID = string(id)
	op := &legacy.Operations[0]
	op.IdempotencyKey = deriveIdempotencyKey(id, domain.TargetID(op.TargetID), domain.OperationID(op.ID), implicit.Plan().Release().Artifacts())
	raw, err := json.Marshal(legacy)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := Decode(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	reencoded, err := decoded.Bytes()
	if err != nil || !bytes.Equal(raw, reencoded) || decoded.ID() != id || decoded.Plan().Operations()[0].Timeout() != 0 ||
		decoded.Plan().Operations()[0].IdempotencyKey() != op.IdempotencyKey {
		t.Fatalf("legacy plan rewritten: %v", err)
	}
}
