package protocol

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

type contractExample struct {
	Name    string          `json:"name"`
	Kind    string          `json:"kind"`
	Message json.RawMessage `json:"message"`
}

func TestProtocolContractFiles(t *testing.T) {
	schemaPath := filepath.Join("..", "..", "protocol", "schema", "v1", "protocol.schema.json")
	schema, err := os.ReadFile(schemaPath)
	if err != nil {
		t.Fatal(err)
	}
	if !json.Valid(schema) {
		t.Fatal("protocol schema is not valid JSON")
	}

	examplesPath := filepath.Join("..", "..", "protocol", "examples", "v1", "messages.json")
	raw, err := os.ReadFile(examplesPath)
	if err != nil {
		t.Fatal(err)
	}
	var examples []contractExample
	if err := json.Unmarshal(raw, &examples); err != nil {
		t.Fatal(err)
	}
	codec := NewCodec(DefaultMaxMessageBytes)
	seen := map[string]bool{}
	for _, example := range examples {
		switch example.Kind {
		case "request":
			request, err := codec.DecodeRequest(bytes.NewReader(example.Message))
			if err != nil {
				t.Fatalf("%s: %v", example.Name, err)
			}
			if err := request.CheckVersion(); err != nil {
				t.Fatalf("%s: %v", example.Name, err)
			}
			var errPayload error
			switch request.Operation {
			case OperationDescribe:
				errPayload = DecodePayload(request.Payload, &DescribeRequest{})
			case OperationPlan:
				errPayload = DecodePayload(request.Payload, &PlanRequest{})
			case OperationApply:
				errPayload = DecodePayload(request.Payload, &ApplyRequest{})
			case OperationReconcile:
				errPayload = DecodePayload(request.Payload, &ReconcileRequest{})
			default:
				t.Fatalf("%s: unknown operation", example.Name)
			}
			if errPayload != nil {
				t.Fatalf("%s: %v", example.Name, errPayload)
			}
			seen[string(request.Operation)+"-request"] = true
		case "response":
			response, err := codec.DecodeResponse(bytes.NewReader(example.Message))
			if err != nil {
				t.Fatalf("%s: %v", example.Name, err)
			}
			if response.ProtocolVersion != Version || !response.Operation.Valid() {
				t.Fatalf("%s: unsupported response version or operation", example.Name)
			}
			if response.Status == StatusOK {
				var errPayload error
				switch response.Operation {
				case OperationDescribe:
					errPayload = DecodePayload(response.Payload, &DescribeResponse{})
				case OperationPlan:
					errPayload = DecodePayload(response.Payload, &PlanResponse{})
				case OperationApply:
					errPayload = DecodePayload(response.Payload, &ApplyResponse{})
				case OperationReconcile:
					errPayload = DecodePayload(response.Payload, &ReconcileResponse{})
				}
				if errPayload != nil {
					t.Fatalf("%s: %v", example.Name, errPayload)
				}
			}
			seen[string(response.Operation)+"-response"] = true
		default:
			t.Fatalf("%s: unknown example kind %q", example.Name, example.Kind)
		}
	}
	for _, operation := range []Operation{OperationDescribe, OperationPlan, OperationApply, OperationReconcile} {
		if !seen[string(operation)+"-request"] || !seen[string(operation)+"-response"] {
			t.Fatalf("missing contract examples for %s", operation)
		}
	}
}
