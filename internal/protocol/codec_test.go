package protocol

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"
)

type failReader struct{}

func (failReader) Read([]byte) (int, error) { return 0, errors.New("read failure") }

type failWriter struct{}

func (failWriter) Write([]byte) (int, error) { return 0, errors.New("write failure") }

func TestCodec(t *testing.T) {
	codec := NewCodec(0)
	request := Request{ProtocolVersion: Version, RequestID: "r1", Operation: OperationDescribe, Payload: json.RawMessage(`{}`)}
	var buffer bytes.Buffer
	if err := codec.EncodeRequest(&buffer, request); err != nil {
		t.Fatal(err)
	}
	decoded, err := codec.DecodeRequest(&buffer)
	if err != nil {
		t.Fatal(err)
	}
	if decoded.RequestID != request.RequestID {
		t.Fatal("request mismatch")
	}

	payload, err := EncodePayload(validPlanRequest())
	if err != nil {
		t.Fatal(err)
	}
	var planRequest PlanRequest
	if err := DecodePayload(payload, &planRequest); err != nil {
		t.Fatal(err)
	}
	if err := DecodePayload(payload, (*PlanRequest)(nil)); err == nil {
		t.Fatal("nil destination accepted")
	}
	if _, err := EncodePayload(PlanRequest{}); err == nil {
		t.Fatal("invalid payload encoded")
	}

	response := Response{ProtocolVersion: Version, RequestID: "r1", Operation: OperationDescribe, Status: StatusOK, Payload: json.RawMessage(`{}`)}
	buffer.Reset()
	if err := codec.EncodeResponse(&buffer, response); err != nil {
		t.Fatal(err)
	}
	if _, err := codec.DecodeResponse(&buffer); err != nil {
		t.Fatal(err)
	}

	if _, err := codec.DecodeRequest(nil); err == nil {
		t.Fatal("nil reader accepted")
	}
	if _, err := codec.DecodeRequest(failReader{}); err == nil {
		t.Fatal("reader error ignored")
	}
	if _, err := codec.DecodeRequest(strings.NewReader("   ")); err == nil {
		t.Fatal("empty input accepted")
	}
	if _, err := codec.DecodeRequest(bytes.NewReader([]byte{0xff})); err == nil {
		t.Fatal("invalid utf8 accepted")
	}
	small := NewCodec(4)
	if _, err := small.DecodeRequest(strings.NewReader(`{"x":1}`)); err == nil {
		t.Fatal("oversized input accepted")
	}
	if err := small.EncodeRequest(io.Discard, request); err == nil {
		t.Fatal("oversized output accepted")
	}
	if err := codec.EncodeRequest(nil, request); err == nil {
		t.Fatal("nil writer accepted")
	}
	if err := codec.EncodeRequest(io.Discard, Request{}); err == nil {
		t.Fatal("invalid request encoded")
	}
	if err := codec.EncodeResponse(io.Discard, Response{}); err == nil {
		t.Fatal("invalid response encoded")
	}
	if err := codec.EncodeResponse(failWriter{}, response); err == nil {
		t.Fatal("writer error ignored")
	}

	invalidJSON := []string{
		`{`,
		`{"protocolVersion":"1","protocolVersion":"1","requestId":"r","operation":"describe","payload":{}}`,
		`{"protocolVersion":"1","requestId":"r","operation":"describe","payload":{"x":1,"x":2}}`,
		`{"protocolVersion":"1","requestId":"r","operation":"describe","payload":{}} {}`,
		`[]`,
	}
	for i, raw := range invalidJSON {
		if _, err := codec.DecodeRequest(strings.NewReader(raw)); err == nil {
			t.Fatalf("invalid json %d accepted", i)
		}
	}

	unknownFields := `{"protocolVersion":"1","requestId":"r","operation":"describe","futureField":true,"payload":{"futurePayloadField":1}}`
	if _, err := codec.DecodeRequest(strings.NewReader(unknownFields)); err != nil {
		t.Fatalf("unknown fields must be forward-compatible: %v", err)
	}

	trailing := `{"protocolVersion":"1","requestId":"r","operation":"describe","status":"ok","payload":{}} trailing`
	if _, err := codec.DecodeResponse(strings.NewReader(trailing)); err == nil {
		t.Fatal("trailing response accepted")
	}
}

func TestDuplicateScannerArraysAndScalars(t *testing.T) {
	valid := []string{`null`, `true`, `1`, `"x"`, `[1,{"a":2},[3]]`, `{"a":[{"b":1}]}`}
	for _, raw := range valid {
		if err := rejectDuplicateKeys([]byte(raw)); err != nil {
			t.Fatalf("%s: %v", raw, err)
		}
	}
	invalid := []string{`{"a":1,"a":2}`, `[{"a":1,"a":2}]`, `{`, `[`, `{} {}`}
	for _, raw := range invalid {
		if err := rejectDuplicateKeys([]byte(raw)); err == nil {
			t.Fatalf("%s accepted", raw)
		}
	}
}
