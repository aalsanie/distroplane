package fakeprovider

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"

	"github.com/aalsanie/distroplane/internal/protocol"
)

const digest = "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

func planRequest(config string) protocol.PlanRequest {
	return protocol.PlanRequest{
		Release: protocol.Release{ID: "v1", Artifacts: []protocol.Artifact{{Name: "tool", Digest: digest, Size: 1}}},
		Target:  protocol.Target{ID: "target", Configuration: json.RawMessage(config)},
	}
}

func executionRequest(payload string) protocol.ApplyRequest {
	return protocol.ApplyRequest{PlanID: "p", TargetID: "t", OperationID: "o", IdempotencyKey: "k", Attempt: 1, ProviderPayload: json.RawMessage(payload)}
}

func TestDescribe(t *testing.T) {
	result, err := (Provider{}).Describe(context.Background(), protocol.DescribeRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if err := result.Validate(); err != nil {
		t.Fatal(err)
	}
	if result.Provider.Name != "fake" {
		t.Fatal("wrong provider")
	}
}

func TestPlan(t *testing.T) {
	provider := Provider{}
	response, providerErr := provider.Plan(context.Background(), planRequest(`{"mode":"published","credential":"npm"}`))
	if providerErr != nil {
		t.Fatal(providerErr)
	}
	if err := response.Validate(); err != nil {
		t.Fatal(err)
	}
	if len(response.Operations) != 1 || len(response.Requirements) != 1 {
		t.Fatal("unexpected plan")
	}

	response, providerErr = provider.Plan(context.Background(), planRequest(`{"mode":"noop"}`))
	if providerErr != nil || len(response.Operations) != 0 {
		t.Fatal("noop plan failed")
	}

	_, providerErr = provider.Plan(context.Background(), planRequest(`{"mode":"plan_error"}`))
	if providerErr == nil || providerErr.Code != protocol.ErrorConfiguration {
		t.Fatal("plan error not returned")
	}

	_, providerErr = provider.Plan(context.Background(), planRequest(`{`))
	if providerErr == nil || providerErr.Code != protocol.ErrorConfiguration {
		t.Fatal("invalid config not rejected")
	}
}

func TestApplyModes(t *testing.T) {
	provider := Provider{}
	successful := map[string]protocol.ResultState{
		`{}`:                          protocol.ResultPublished,
		`{"mode":"published"}`:        protocol.ResultPublished,
		`{"mode":"noop"}`:             protocol.ResultPublished,
		`{"mode":"waiting_external"}`: protocol.ResultWaitingExternal,
		`{"mode":"rejected"}`:         protocol.ResultRejected,
	}
	for payload, expected := range successful {
		response, providerErr := provider.Apply(context.Background(), executionRequest(payload))
		if providerErr != nil {
			t.Fatalf("%s: %v", payload, providerErr)
		}
		if response.Result.State != expected {
			t.Fatalf("%s: got %s", payload, response.Result.State)
		}
		if err := response.Validate(); err != nil {
			t.Fatal(err)
		}
	}

	failures := map[string]protocol.ErrorCode{
		`{"mode":"transient_error"}`:      protocol.ErrorTransientExternal,
		`{"mode":"permanent_error"}`:      protocol.ErrorPermanentExternal,
		`{"mode":"authentication_error"}`: protocol.ErrorAuthentication,
		`{"mode":"authorization_error"}`:  protocol.ErrorAuthorization,
		`{"mode":"timeout"}`:              protocol.ErrorTimeout,
		`{"mode":"cancelled"}`:            protocol.ErrorCancelled,
		`{"mode":"ambiguous"}`:            protocol.ErrorAmbiguousOutcome,
		`{"mode":"internal_error"}`:       protocol.ErrorProviderInternal,
		`{"mode":"unknown"}`:              protocol.ErrorConfiguration,
		`{`:                               protocol.ErrorConfiguration,
	}
	for payload, expected := range failures {
		_, providerErr := provider.Apply(context.Background(), executionRequest(payload))
		if providerErr == nil || providerErr.Code != expected {
			t.Fatalf("%s: %#v", payload, providerErr)
		}
	}
}

func TestReconcileModes(t *testing.T) {
	provider := Provider{}
	base := executionRequest(`{}`)
	makeRequest := func(payload string) protocol.ReconcileRequest {
		return protocol.ReconcileRequest{PlanID: base.PlanID, TargetID: base.TargetID, OperationID: base.OperationID, IdempotencyKey: base.IdempotencyKey, Attempt: 2, ProviderPayload: json.RawMessage(payload)}
	}
	successful := map[string]protocol.ResultState{
		`{}`:                          protocol.ResultPublished,
		`{"mode":"published"}`:        protocol.ResultPublished,
		`{"mode":"noop"}`:             protocol.ResultPublished,
		`{"mode":"waiting_external"}`: protocol.ResultWaitingExternal,
		`{"mode":"rejected"}`:         protocol.ResultRejected,
		`{"mode":"ambiguous"}`:        protocol.ResultPublished,
		`{"mode":"ambiguous","reconcileState":"waiting_external"}`: protocol.ResultWaitingExternal,
	}
	for payload, expected := range successful {
		response, providerErr := provider.Reconcile(context.Background(), makeRequest(payload))
		if providerErr != nil {
			t.Fatalf("%s: %v", payload, providerErr)
		}
		if response.Result.State != expected {
			t.Fatalf("%s: got %s", payload, response.Result.State)
		}
		if err := response.Validate(); err != nil {
			t.Fatal(err)
		}
	}
	failures := map[string]protocol.ErrorCode{
		`{"reconcileState":"transient_error"}`: protocol.ErrorTransientExternal,
		`{"reconcileState":"unknown"}`:         protocol.ErrorConfiguration,
		`{`:                                    protocol.ErrorConfiguration,
	}
	for payload, expected := range failures {
		_, providerErr := provider.Reconcile(context.Background(), makeRequest(payload))
		if providerErr == nil || providerErr.Code != expected {
			t.Fatalf("%s: %#v", payload, providerErr)
		}
	}
}

func TestHelpers(t *testing.T) {
	cfg, err := parseConfig(json.RawMessage(`{"mode":"published"}`))
	if err != nil || cfg.Mode != "published" {
		t.Fatal("parse config failed")
	}
	if _, err := parseConfig(json.RawMessage(`{`)); err == nil {
		t.Fatal("invalid config accepted")
	}
	result := applyResult(protocol.ResultPublished, "published")
	if result.Result.State != protocol.ResultPublished {
		t.Fatal("apply result failed")
	}
	errValue := providerError(protocol.ErrorRejected, "rejected", false)
	if errValue.Code != protocol.ErrorRejected {
		t.Fatal("provider error failed")
	}
}

func TestProtocolConformanceRoundTrip(t *testing.T) {
	codec := protocol.NewCodec(protocol.DefaultMaxMessageBytes)
	cases := []protocol.Request{
		{ProtocolVersion: protocol.Version, RequestID: "describe", Operation: protocol.OperationDescribe, Payload: json.RawMessage(`{}`)},
		{ProtocolVersion: protocol.Version, RequestID: "plan", Operation: protocol.OperationPlan, Payload: mustJSON(t, planRequest(`{"mode":"published"}`))},
		{ProtocolVersion: protocol.Version, RequestID: "apply", Operation: protocol.OperationApply, Payload: mustJSON(t, executionRequest(`{"mode":"published"}`))},
		{ProtocolVersion: protocol.Version, RequestID: "reconcile", Operation: protocol.OperationReconcile, Payload: mustJSON(t, protocol.ReconcileRequest{PlanID: "p", TargetID: "t", OperationID: "o", IdempotencyKey: "k", Attempt: 2, ProviderPayload: json.RawMessage(`{"mode":"ambiguous"}`)})},
	}
	for _, request := range cases {
		var input, output bytes.Buffer
		if err := codec.EncodeRequest(&input, request); err != nil {
			t.Fatal(err)
		}
		if err := protocol.ServeOnce(context.Background(), &input, &output, Provider{}, codec); err != nil {
			t.Fatal(err)
		}
		response, err := codec.DecodeResponse(&output)
		if err != nil {
			t.Fatal(err)
		}
		if err := response.CheckCorrelation(request); err != nil {
			t.Fatal(err)
		}
		if response.Status != protocol.StatusOK {
			t.Fatalf("%s: %+v", request.Operation, response)
		}
	}
}

func mustJSON(t *testing.T, value any) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
