package protocol

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestProtocolRejectsEveryMalformedExecutionCoordinate(t *testing.T) {
	base := ApplyRequest{PlanID: "plan", TargetID: "target",
		OperationID: "publish", IdempotencyKey: "idempotent",
		Attempt: 1, ProviderPayload: json.RawMessage(`{}`)}
	if err := base.Validate(); err != nil {
		t.Fatalf("valid request rejected: %v", err)
	}
	for name, mutate := range map[string]func(*ApplyRequest){
		"plan": func(p *ApplyRequest) { p.PlanID = "" },
		"target": func(p *ApplyRequest) { p.TargetID = "" },
		"operation": func(p *ApplyRequest) { p.OperationID = "" },
		"idempotency": func(p *ApplyRequest) { p.IdempotencyKey = "" },
		"unicode": func(p *ApplyRequest) { p.OperationID = string([]byte{0xff}) },
		"whitespace": func(p *ApplyRequest) { p.IdempotencyKey = " trailing " },
		"oversized": func(p *ApplyRequest) { p.PlanID = strings.Repeat("x", 513) },
		"attempt": func(p *ApplyRequest) { p.Attempt = 0 },
		"payload": func(p *ApplyRequest) { p.ProviderPayload = json.RawMessage(`{`) },
		"empty-payload": func(p *ApplyRequest) { p.ProviderPayload = nil },
	} {
		t.Run(name, func(t *testing.T) {
			request := base
			mutate(&request)
			if err := request.Validate(); err == nil {
				t.Fatal("malformed apply request accepted")
			}
		})
	}
	if err := (ReconcileRequest{PlanID: base.PlanID, TargetID: base.TargetID,
		OperationID: base.OperationID, IdempotencyKey: base.IdempotencyKey,
		Attempt: 1, ProviderPayload: base.ProviderPayload,
		Previous: &DistributionResult{State: "FUTURE", Evidence: json.RawMessage(`{}`)}}).Validate(); err == nil {
		t.Fatal("invalid prior state was accepted")
	}
}

func TestProtocolEnforcesReleaseAndProviderIdentityBoundaries(t *testing.T) {
	artifact := Artifact{Name: "archive", Digest: "sha256:" + strings.Repeat("a", 64), Size: 1}
	if err := artifact.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []Artifact{
		{Name: "archive", Digest: "sha256:" + strings.Repeat("A", 64), Size: 1},
		{Name: "archive", Digest: "sha256:" + strings.Repeat("z", 64), Size: 1},
		{Name: "archive", Digest: artifact.Digest, Size: -1},
		{Name: "archive", Digest: artifact.Digest, Size: 1, MediaType: "invalid\nmedia"},
	} {
		if err := bad.Validate(); err == nil {
			t.Errorf("invalid artifact accepted: %+v", bad)
		}
	}
	if err := (Release{ID: "release", Artifacts: []Artifact{artifact, artifact}}).Validate(); err == nil {
		t.Fatal("duplicate release artifact accepted")
	}
	for _, requirement := range []Requirement{
		{Kind: "", Name: "name"},
		{Kind: "credential", Name: ""},
		{Kind: "credential", Name: "token", Metadata: json.RawMessage(`{`)},
	} {
		if err := requirement.Validate(); err == nil {
			t.Errorf("invalid requirement accepted: %+v", requirement)
		}
	}
	for _, c := range []Capability{CapabilityPlan, CapabilityApply, CapabilityReconcile} {
		if !c.Valid() {
			t.Errorf("supported capability %q rejected", c)
		}
	}
	if (Capability("future")).Valid() {
		t.Fatal("unsupported provider capability accepted")
	}
}
