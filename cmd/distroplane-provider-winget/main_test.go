package main

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"

	"github.com/aalsanie/distroplane/internal/protocol"
	wingetprovider "github.com/aalsanie/distroplane/providers/winget"
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
	if code := run(context.Background(), &input, &output, &diagnostics, wingetprovider.Provider{Version: "1.2.3"}); code != 0 {
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
	if description.Provider.Name != wingetprovider.Name || description.Provider.Version != "1.2.3" {
		t.Fatalf("description=%+v", description)
	}
}

func TestRunInvalidInput(t *testing.T) {
	var output, diagnostics bytes.Buffer
	if code := run(context.Background(), bytes.NewBufferString("{"), &output, &diagnostics, wingetprovider.Provider{}); code != 1 {
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
	description := executeDescribe(t)
	if description.Provider.Version != wingetprovider.DefaultVersion {
		t.Fatalf("version=%q", description.Provider.Version)
	}
}

func TestExecuteUsesConfiguredVersion(t *testing.T) {
	old := version
	version = " 2.3.4 "
	defer func() { version = old }()
	description := executeDescribe(t)
	if description.Provider.Version != "2.3.4" {
		t.Fatalf("version=%q", description.Provider.Version)
	}
	if commandProvider("4.5.6").Version != "4.5.6" {
		t.Fatal("command provider version mismatch")
	}
}

func executeDescribe(t *testing.T) protocol.DescribeResponse {
	t.Helper()
	codec := protocol.NewCodec(protocol.DefaultMaxMessageBytes)
	payload, _ := protocol.EncodePayload(protocol.DescribeRequest{})
	var input bytes.Buffer
	if err := codec.EncodeRequest(&input, protocol.Request{
		ProtocolVersion: protocol.Version, RequestID: "execute", Operation: protocol.OperationDescribe, Payload: payload,
	}); err != nil {
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
	return description
}
