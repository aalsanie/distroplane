package protocol

import (
	"encoding/json"
	"strings"
	"testing"
)

const testDigest = "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

func validArtifact() Artifact {
	return Artifact{Name: "tool", Digest: testDigest, Size: 42, MediaType: "application/octet-stream"}
}

func validRelease() Release { return Release{ID: "v1.0.0", Artifacts: []Artifact{validArtifact()}} }
func validTarget() Target {
	return Target{ID: "npm", Configuration: json.RawMessage(`{"mode":"published"}`)}
}
func validPlanRequest() PlanRequest {
	return PlanRequest{Release: validRelease(), Target: validTarget()}
}
func validPlannedOperation() PlannedOperation {
	return PlannedOperation{ID: "publish", Kind: "publish", SideEffecting: true, TimeoutMillis: 1000, ProviderPayload: json.RawMessage(`{}`)}
}
func validApplyRequest() ApplyRequest {
	return ApplyRequest{PlanID: "plan", TargetID: "target", OperationID: "publish", IdempotencyKey: "key", Attempt: 1, ProviderPayload: json.RawMessage(`{}`)}
}
func validResult() DistributionResult {
	return DistributionResult{State: ResultPublished, ProviderState: "published", Evidence: json.RawMessage(`{"url":"https://example.invalid"}`)}
}

func TestErrorCodesAndProviderError(t *testing.T) {
	valid := []ErrorCode{ErrorProtocol, ErrorConfiguration, ErrorAuthentication, ErrorAuthorization, ErrorTransientExternal, ErrorPermanentExternal, ErrorRejected, ErrorTimeout, ErrorCancelled, ErrorProviderInternal, ErrorAmbiguousOutcome}
	for _, code := range valid {
		if !code.Valid() {
			t.Fatalf("expected %q valid", code)
		}
		value := NewProviderError(code, "failure", code == ErrorTransientExternal)
		if err := value.Validate(); err != nil {
			t.Fatalf("%q: %v", code, err)
		}
	}
	cases := []ProviderError{
		{Code: "NOPE", Message: "failure"},
		{Code: ErrorProtocol, Message: ""},
		{Code: ErrorProtocol, Message: " bad"},
		{Code: ErrorProtocol, Message: strings.Repeat("x", 4097)},
		{Code: ErrorProtocol, Message: "bad\nmessage"},
		{Code: ErrorProtocol, Message: "failure", Details: json.RawMessage(`{`)},
	}
	for i, value := range cases {
		if err := value.Validate(); err == nil {
			t.Fatalf("case %d accepted", i)
		}
	}
	if ErrorCode("NOPE").Valid() {
		t.Fatal("unknown error code accepted")
	}
}

func TestOperationAndStatus(t *testing.T) {
	for _, op := range []Operation{OperationDescribe, OperationPlan, OperationApply, OperationReconcile} {
		if !op.Valid() {
			t.Fatalf("expected %q valid", op)
		}
	}
	if Operation("unknown").Valid() {
		t.Fatal("unknown operation accepted")
	}
	if !StatusOK.Valid() || !StatusError.Valid() || Status("other").Valid() {
		t.Fatal("status validity mismatch")
	}
}

func TestRequestValidationAndVersion(t *testing.T) {
	base := Request{ProtocolVersion: Version, RequestID: "r1", Operation: OperationDescribe, Payload: json.RawMessage(`{}`)}
	if err := base.Validate(); err != nil {
		t.Fatal(err)
	}
	if err := base.CheckVersion(); err != nil {
		t.Fatal(err)
	}

	cases := []Request{
		{ProtocolVersion: "", RequestID: "r", Operation: OperationDescribe, Payload: json.RawMessage(`{}`)},
		{ProtocolVersion: "x", RequestID: "r", Operation: OperationDescribe, Payload: json.RawMessage(`{}`)},
		{ProtocolVersion: strings.Repeat("1", 17), RequestID: "r", Operation: OperationDescribe, Payload: json.RawMessage(`{}`)},
		{ProtocolVersion: Version, RequestID: "", Operation: OperationDescribe, Payload: json.RawMessage(`{}`)},
		{ProtocolVersion: Version, RequestID: " r", Operation: OperationDescribe, Payload: json.RawMessage(`{}`)},
		{ProtocolVersion: Version, RequestID: strings.Repeat("x", 129), Operation: OperationDescribe, Payload: json.RawMessage(`{}`)},
		{ProtocolVersion: Version, RequestID: "r\n", Operation: OperationDescribe, Payload: json.RawMessage(`{}`)},
		{ProtocolVersion: Version, RequestID: "r", Operation: "", Payload: json.RawMessage(`{}`)},
		{ProtocolVersion: Version, RequestID: "r", Operation: OperationDescribe, Payload: nil},
		{ProtocolVersion: Version, RequestID: "r", Operation: OperationDescribe, Payload: json.RawMessage(`{`)},
	}
	for i, value := range cases {
		if err := value.Validate(); err == nil {
			t.Fatalf("case %d accepted", i)
		}
	}
	future := base
	future.ProtocolVersion = "2"
	if err := future.Validate(); err != nil {
		t.Fatal(err)
	}
	if err := future.CheckVersion(); err == nil {
		t.Fatal("unsupported version accepted")
	}
}

func TestResponseValidationAndCorrelation(t *testing.T) {
	request := Request{ProtocolVersion: Version, RequestID: "r1", Operation: OperationPlan, Payload: json.RawMessage(`{}`)}
	ok := Response{ProtocolVersion: Version, RequestID: "r1", Operation: OperationPlan, Status: StatusOK, Payload: json.RawMessage(`{}`)}
	if err := ok.Validate(); err != nil {
		t.Fatal(err)
	}
	if err := ok.CheckCorrelation(request); err != nil {
		t.Fatal(err)
	}
	providerErr := NewProviderError(ErrorConfiguration, "bad config", false)
	failed := Response{ProtocolVersion: Version, RequestID: "r1", Operation: OperationPlan, Status: StatusError, Error: &providerErr}
	if err := failed.Validate(); err != nil {
		t.Fatal(err)
	}

	invalidErr := NewProviderError("bad", "bad", false)
	cases := []Response{
		{ProtocolVersion: "", RequestID: "r", Operation: OperationPlan, Status: StatusOK, Payload: json.RawMessage(`{}`)},
		{ProtocolVersion: Version, RequestID: "", Operation: OperationPlan, Status: StatusOK, Payload: json.RawMessage(`{}`)},
		{ProtocolVersion: Version, RequestID: "r", Operation: "", Status: StatusOK, Payload: json.RawMessage(`{}`)},
		{ProtocolVersion: Version, RequestID: "r", Operation: OperationPlan, Status: "nope", Payload: json.RawMessage(`{}`)},
		{ProtocolVersion: Version, RequestID: "r", Operation: OperationPlan, Status: StatusOK, Payload: json.RawMessage(`{}`), Error: &providerErr},
		{ProtocolVersion: Version, RequestID: "r", Operation: OperationPlan, Status: StatusOK},
		{ProtocolVersion: Version, RequestID: "r", Operation: OperationPlan, Status: StatusOK, Payload: json.RawMessage(`{`)},
		{ProtocolVersion: Version, RequestID: "r", Operation: OperationPlan, Status: StatusError, Payload: json.RawMessage(`{}`), Error: &providerErr},
		{ProtocolVersion: Version, RequestID: "r", Operation: OperationPlan, Status: StatusError},
		{ProtocolVersion: Version, RequestID: "r", Operation: OperationPlan, Status: StatusError, Error: &invalidErr},
	}
	for i, value := range cases {
		if err := value.Validate(); err == nil {
			t.Fatalf("case %d accepted", i)
		}
	}

	mismatches := []Response{ok, ok, ok}
	mismatches[0].ProtocolVersion = "2"
	mismatches[1].RequestID = "other"
	mismatches[2].Operation = OperationApply
	for i, response := range mismatches {
		if err := response.CheckCorrelation(request); err == nil {
			t.Fatalf("correlation case %d accepted", i)
		}
	}
}

func TestDescribeResponseValidation(t *testing.T) {
	base := DescribeResponse{Provider: ProviderIdentity{Name: "fake", Version: "1"}, ProtocolVersions: []string{"1"}, Capabilities: []Capability{CapabilityPlan, CapabilityApply, CapabilityReconcile}}
	if err := base.Validate(); err != nil {
		t.Fatal(err)
	}
	cases := []DescribeResponse{
		{Provider: ProviderIdentity{Name: "", Version: "1"}, ProtocolVersions: []string{"1"}},
		{Provider: ProviderIdentity{Name: "fake", Version: ""}, ProtocolVersions: []string{"1"}},
		{Provider: base.Provider},
		{Provider: base.Provider, ProtocolVersions: []string{"x"}},
		{Provider: base.Provider, ProtocolVersions: []string{"1", "1"}},
		{Provider: base.Provider, ProtocolVersions: []string{"1"}, Capabilities: []Capability{"bad"}},
		{Provider: base.Provider, ProtocolVersions: []string{"1"}, Capabilities: []Capability{CapabilityPlan, CapabilityPlan}},
		{Provider: base.Provider, ProtocolVersions: []string{"1"}, Requirements: []Requirement{{}}},
	}
	for i, value := range cases {
		if err := value.Validate(); err == nil {
			t.Fatalf("case %d accepted", i)
		}
	}
}

func TestArtifactReleaseTargetValidation(t *testing.T) {
	if err := validArtifact().Validate(); err != nil {
		t.Fatal(err)
	}
	badArtifacts := []Artifact{
		{Digest: testDigest},
		{Name: "tool", Digest: "sha256:BAD", Size: 1},
		{Name: "tool", Digest: "sha256:" + strings.Repeat("A", 64), Size: 1},
		{Name: "tool", Digest: "sha256:" + strings.Repeat("g", 64), Size: 1},
		{Name: "tool", Digest: testDigest, Size: -1},
		{Name: "tool", Digest: testDigest, Size: 1, MediaType: " bad"},
	}
	for i, value := range badArtifacts {
		if err := value.Validate(); err == nil {
			t.Fatalf("artifact %d accepted", i)
		}
	}
	if err := validRelease().Validate(); err != nil {
		t.Fatal(err)
	}
	releases := []Release{
		{ID: "", Artifacts: []Artifact{validArtifact()}},
		{ID: "v1"},
		{ID: "v1", Artifacts: []Artifact{{Name: "bad"}}},
		{ID: "v1", Artifacts: []Artifact{validArtifact(), validArtifact()}},
	}
	for i, value := range releases {
		if err := value.Validate(); err == nil {
			t.Fatalf("release %d accepted", i)
		}
	}
	if err := validTarget().Validate(); err != nil {
		t.Fatal(err)
	}
	for i, value := range []Target{{Configuration: json.RawMessage(`{}`)}, {ID: "t"}, {ID: "t", Configuration: json.RawMessage(`{`)}} {
		if err := value.Validate(); err == nil {
			t.Fatalf("target %d accepted", i)
		}
	}
}

func TestRequirementAndPlanRequestValidation(t *testing.T) {
	requirement := Requirement{Kind: "credential", Name: "production", Metadata: json.RawMessage(`{"scope":"publish"}`)}
	if err := requirement.Validate(); err != nil {
		t.Fatal(err)
	}
	for i, value := range []Requirement{{Name: "x"}, {Kind: "credential"}, {Kind: "credential", Name: "x", Metadata: json.RawMessage(`{`)}} {
		if err := value.Validate(); err == nil {
			t.Fatalf("requirement %d accepted", i)
		}
	}
	request := validPlanRequest()
	if err := request.Validate(); err != nil {
		t.Fatal(err)
	}
	request.Release = Release{}
	if err := request.Validate(); err == nil {
		t.Fatal("invalid release accepted")
	}
	request = validPlanRequest()
	request.Target = Target{}
	if err := request.Validate(); err == nil {
		t.Fatal("invalid target accepted")
	}
}

func TestPlanResponseValidation(t *testing.T) {
	op := validPlannedOperation()
	response := PlanResponse{Operations: []PlannedOperation{op}, Requirements: []Requirement{{Kind: "credential", Name: "prod"}}}
	if err := response.Validate(); err != nil {
		t.Fatal(err)
	}
	if err := (PlanResponse{Operations: []PlannedOperation{}}).Validate(); err != nil {
		t.Fatal(err)
	}

	invalidOps := []PlannedOperation{
		{Kind: "publish", ProviderPayload: json.RawMessage(`{}`)},
		{ID: "x", ProviderPayload: json.RawMessage(`{}`)},
		{ID: "x", Kind: "publish"},
		{ID: "x", Kind: "publish", ProviderPayload: json.RawMessage(`{`)},
		{ID: "x", Kind: "publish", Dependencies: []string{"x"}, ProviderPayload: json.RawMessage(`{}`)},
		{ID: "x", Kind: "publish", Dependencies: []string{"y", "y"}, ProviderPayload: json.RawMessage(`{}`)},
		{ID: "x", Kind: "publish", Dependencies: []string{""}, ProviderPayload: json.RawMessage(`{}`)},
	}
	for i, value := range invalidOps {
		if err := value.Validate(); err == nil {
			t.Fatalf("operation %d accepted", i)
		}
	}

	if err := (PlanResponse{Operations: []PlannedOperation{op, op}}).Validate(); err == nil {
		t.Fatal("duplicate operations accepted")
	}
	missing := validPlannedOperation()
	missing.ID = "x"
	missing.Dependencies = []string{"missing"}
	if err := (PlanResponse{Operations: []PlannedOperation{missing}}).Validate(); err == nil {
		t.Fatal("missing dependency accepted")
	}
	a := validPlannedOperation()
	a.ID = "a"
	a.Dependencies = []string{"b"}
	b := validPlannedOperation()
	b.ID = "b"
	b.Dependencies = []string{"a"}
	if err := (PlanResponse{Operations: []PlannedOperation{a, b}}).Validate(); err == nil {
		t.Fatal("cycle accepted")
	}
	chainA := validPlannedOperation()
	chainA.ID = "a"
	chainB := validPlannedOperation()
	chainB.ID = "b"
	chainB.Dependencies = []string{"a"}
	if err := (PlanResponse{Operations: []PlannedOperation{chainA, chainB}}).Validate(); err != nil {
		t.Fatal(err)
	}
	if err := (PlanResponse{Requirements: []Requirement{{}}}).Validate(); err == nil {
		t.Fatal("invalid requirement accepted")
	}
}

func TestExecutionRequestsAndResults(t *testing.T) {
	apply := validApplyRequest()
	if err := apply.Validate(); err != nil {
		t.Fatal(err)
	}
	mutations := []func(*ApplyRequest){
		func(v *ApplyRequest) { v.PlanID = "" },
		func(v *ApplyRequest) { v.TargetID = "" },
		func(v *ApplyRequest) { v.OperationID = "" },
		func(v *ApplyRequest) { v.IdempotencyKey = "" },
		func(v *ApplyRequest) { v.Attempt = 0 },
		func(v *ApplyRequest) { v.ProviderPayload = json.RawMessage(`{`) },
	}
	for i, mutate := range mutations {
		value := apply
		mutate(&value)
		if err := value.Validate(); err == nil {
			t.Fatalf("apply mutation %d accepted", i)
		}
	}

	previous := validResult()
	reconcile := ReconcileRequest{PlanID: apply.PlanID, TargetID: apply.TargetID, OperationID: apply.OperationID, IdempotencyKey: apply.IdempotencyKey, Attempt: 2, ProviderPayload: json.RawMessage(`{}`), Previous: &previous}
	if err := reconcile.Validate(); err != nil {
		t.Fatal(err)
	}
	reconcile.PlanID = ""
	if err := reconcile.Validate(); err == nil {
		t.Fatal("invalid reconcile accepted")
	}
	reconcile = ReconcileRequest{PlanID: apply.PlanID, TargetID: apply.TargetID, OperationID: apply.OperationID, IdempotencyKey: apply.IdempotencyKey, Attempt: 2, ProviderPayload: json.RawMessage(`{}`), Previous: &DistributionResult{}}
	if err := reconcile.Validate(); err == nil {
		t.Fatal("invalid previous result accepted")
	}

	for _, state := range []ResultState{ResultWaitingExternal, ResultPublished, ResultRejected} {
		result := validResult()
		result.State = state
		if err := result.Validate(); err != nil {
			t.Fatalf("state %q: %v", state, err)
		}
		if !state.Valid() {
			t.Fatalf("state %q not valid", state)
		}
		if err := (ApplyResponse{Result: result}).Validate(); err != nil {
			t.Fatal(err)
		}
		if err := (ReconcileResponse{Result: result}).Validate(); err != nil {
			t.Fatal(err)
		}
	}
	if ResultState("bad").Valid() {
		t.Fatal("invalid state accepted")
	}
	badResults := []DistributionResult{
		{State: "bad", Evidence: json.RawMessage(`{}`)},
		{State: ResultPublished, ProviderState: " bad", Evidence: json.RawMessage(`{}`)},
		{State: ResultPublished},
		{State: ResultPublished, Evidence: json.RawMessage(`{`)},
	}
	for i, value := range badResults {
		if err := value.Validate(); err == nil {
			t.Fatalf("result %d accepted", i)
		}
	}
}
