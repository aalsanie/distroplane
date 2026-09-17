package protocol

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"
)

type stubHandler struct {
	providerError *ProviderError
	invalidResult bool
}

func (h stubHandler) Describe(context.Context, DescribeRequest) (DescribeResponse, *ProviderError) {
	if h.providerError != nil {
		return DescribeResponse{}, h.providerError
	}
	if h.invalidResult {
		return DescribeResponse{}, nil
	}
	return DescribeResponse{Provider: ProviderIdentity{Name: "stub", Version: "1"}, ProtocolVersions: []string{Version}, Capabilities: []Capability{CapabilityPlan, CapabilityApply, CapabilityReconcile}}, nil
}
func (h stubHandler) Plan(context.Context, PlanRequest) (PlanResponse, *ProviderError) {
	if h.providerError != nil {
		return PlanResponse{}, h.providerError
	}
	if h.invalidResult {
		return PlanResponse{Operations: []PlannedOperation{{}}}, nil
	}
	return PlanResponse{Operations: []PlannedOperation{}}, nil
}
func (h stubHandler) Apply(context.Context, ApplyRequest) (ApplyResponse, *ProviderError) {
	if h.providerError != nil {
		return ApplyResponse{}, h.providerError
	}
	if h.invalidResult {
		return ApplyResponse{}, nil
	}
	return ApplyResponse{Result: validResult()}, nil
}
func (h stubHandler) Reconcile(context.Context, ReconcileRequest) (ReconcileResponse, *ProviderError) {
	if h.providerError != nil {
		return ReconcileResponse{}, h.providerError
	}
	if h.invalidResult {
		return ReconcileResponse{}, nil
	}
	return ReconcileResponse{Result: validResult()}, nil
}

func requestFor(t *testing.T, op Operation, payload any) Request {
	t.Helper()
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	return Request{ProtocolVersion: Version, RequestID: "r", Operation: op, Payload: raw}
}

func serve(t *testing.T, request Request, handler Handler) Response {
	t.Helper()
	codec := NewCodec(DefaultMaxMessageBytes)
	var in, out bytes.Buffer
	if err := codec.EncodeRequest(&in, request); err != nil {
		t.Fatal(err)
	}
	if err := ServeOnce(context.Background(), &in, &out, handler, codec); err != nil {
		t.Fatal(err)
	}
	response, err := codec.DecodeResponse(&out)
	if err != nil {
		t.Fatal(err)
	}
	return response
}

func TestServeOnceDispatchAndErrors(t *testing.T) {
	operations := []struct {
		op      Operation
		payload any
	}{
		{OperationDescribe, DescribeRequest{}},
		{OperationPlan, validPlanRequest()},
		{OperationApply, validApplyRequest()},
		{OperationReconcile, ReconcileRequest{PlanID: "p", TargetID: "t", OperationID: "o", IdempotencyKey: "k", Attempt: 1, ProviderPayload: json.RawMessage(`{}`)}},
	}
	for _, tc := range operations {
		response := serve(t, requestFor(t, tc.op, tc.payload), stubHandler{})
		if response.Status != StatusOK || response.Operation != tc.op {
			t.Fatalf("%s failed: %+v", tc.op, response)
		}
	}

	providerErr := NewProviderError(ErrorTransientExternal, "temporary", true)
	response := serve(t, requestFor(t, OperationApply, validApplyRequest()), stubHandler{providerError: &providerErr})
	if response.Status != StatusError || response.Error.Code != ErrorTransientExternal {
		t.Fatalf("unexpected provider error: %+v", response)
	}

	invalidProviderErr := ProviderError{Code: "bad", Message: "bad"}
	response = serve(t, requestFor(t, OperationDescribe, DescribeRequest{}), stubHandler{providerError: &invalidProviderErr})
	if response.Error.Code != ErrorProviderInternal {
		t.Fatalf("invalid provider error leaked: %+v", response)
	}

	for _, tc := range operations {
		response = serve(t, requestFor(t, tc.op, tc.payload), stubHandler{invalidResult: true})
		if response.Status != StatusError || response.Error.Code != ErrorProviderInternal {
			t.Fatalf("invalid handler result for %s not contained", tc.op)
		}
	}

	badPayload := Request{ProtocolVersion: Version, RequestID: "r", Operation: OperationPlan, Payload: json.RawMessage(`{}`)}
	response = serve(t, badPayload, stubHandler{})
	if response.Error.Code != ErrorProtocol {
		t.Fatalf("invalid payload not protocol error: %+v", response)
	}

	future := requestFor(t, OperationDescribe, DescribeRequest{})
	future.ProtocolVersion = "2"
	response = serve(t, future, stubHandler{})
	if response.Status != StatusError || response.Error.Code != ErrorProtocol {
		t.Fatal("unsupported version not rejected")
	}

	unknown := Request{ProtocolVersion: Version, RequestID: "r", Operation: "future", Payload: json.RawMessage(`{}`)}
	response = serve(t, unknown, stubHandler{})
	if response.Error.Code != ErrorProtocol {
		t.Fatal("unknown operation not rejected")
	}

	codec := NewCodec(DefaultMaxMessageBytes)
	if err := ServeOnce(nil, strings.NewReader(`{}`), io.Discard, stubHandler{}, codec); err == nil {
		t.Fatal("nil context accepted")
	}
	if err := ServeOnce(context.Background(), strings.NewReader(`{}`), io.Discard, nil, codec); err == nil {
		t.Fatal("nil handler accepted")
	}
	if err := ServeOnce(context.Background(), strings.NewReader(`bad`), io.Discard, stubHandler{}, codec); err == nil {
		t.Fatal("malformed request accepted")
	}
}

func TestEncodePayloadMarshalError(t *testing.T) {
	value := struct {
		C chan int `json:"c"`
	}{C: make(chan int)}
	if _, err := EncodePayload(value); err == nil {
		t.Fatal("marshal error ignored")
	}
}

func FuzzDecodeRequest(f *testing.F) {
	f.Add([]byte(`{"protocolVersion":"1","requestId":"r","operation":"describe","payload":{}}`))
	f.Add([]byte(`bad`))
	codec := NewCodec(DefaultMaxMessageBytes)
	f.Fuzz(func(t *testing.T, data []byte) { _, _ = codec.DecodeRequest(bytes.NewReader(data)) })
}

func FuzzDecodeResponse(f *testing.F) {
	f.Add([]byte(`{"protocolVersion":"1","requestId":"r","operation":"describe","status":"ok","payload":{}}`))
	f.Add([]byte(`bad`))
	codec := NewCodec(DefaultMaxMessageBytes)
	f.Fuzz(func(t *testing.T, data []byte) { _, _ = codec.DecodeResponse(bytes.NewReader(data)) })
}
