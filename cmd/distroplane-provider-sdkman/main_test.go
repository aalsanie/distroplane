package main

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"

	"github.com/aalsanie/distroplane/internal/protocol"
	sdkmanprovider "github.com/aalsanie/distroplane/providers/sdkman"
)

func TestRunDescribe(t *testing.T) {
	codec := protocol.NewCodec(protocol.DefaultMaxMessageBytes)
	payload, err := protocol.EncodePayload(protocol.DescribeRequest{})
	if err != nil {
		t.Fatal(err)
	}
	var input bytes.Buffer
	if err := codec.EncodeRequest(&input, protocol.Request{
		ProtocolVersion: protocol.Version, RequestID: "request", Operation: protocol.OperationDescribe, Payload: payload,
	}); err != nil {
		t.Fatal(err)
	}
	var output, diagnostics bytes.Buffer
	if code := run(context.Background(), &input, &output, &diagnostics, sdkmanprovider.Provider{Version: "1.2.3"}); code != 0 {
		t.Fatalf("code=%d diagnostics=%s", code, diagnostics.String())
	}
	response, err := codec.DecodeResponse(&output)
	if err != nil {
		t.Fatal(err)
	}
	var description protocol.DescribeResponse
	if err := json.Unmarshal(response.Payload, &description); err != nil {
		t.Fatal(err)
	}
	if description.Provider.Name != sdkmanprovider.Name || description.Provider.Version != "1.2.3" {
		t.Fatalf("description=%+v", description)
	}
}

func TestRunInvalidInput(t *testing.T) {
	var output, diagnostics bytes.Buffer
	if code := run(context.Background(), bytes.NewBufferString("{"), &output, &diagnostics, sdkmanprovider.Provider{}); code != 1 {
		t.Fatalf("code=%d", code)
	}
	if diagnostics.Len() == 0 {
		t.Fatal("expected diagnostic")
	}
}

func TestExecuteUsesDefaultVersion(t *testing.T) {
	old := version
	version = "  "
	defer func() { version = old }()
	codec := protocol.NewCodec(protocol.DefaultMaxMessageBytes)
	payload, _ := protocol.EncodePayload(protocol.DescribeRequest{})
	var input bytes.Buffer
	if err := codec.EncodeRequest(&input, protocol.Request{ProtocolVersion: protocol.Version, RequestID: "default", Operation: protocol.OperationDescribe, Payload: payload}); err != nil {
		t.Fatal(err)
	}
	var output, diagnostics bytes.Buffer
	if code := execute(context.Background(), process{input: &input, output: &output, diagnostics: &diagnostics}); code != 0 {
		t.Fatalf("code=%d diagnostics=%s", code, diagnostics.String())
	}
	response, err := codec.DecodeResponse(&output)
	if err != nil {
		t.Fatal(err)
	}
	var description protocol.DescribeResponse
	if err := json.Unmarshal(response.Payload, &description); err != nil {
		t.Fatal(err)
	}
	if description.Provider.Version != sdkmanprovider.DefaultVersion {
		t.Fatalf("version=%q", description.Provider.Version)
	}
}
