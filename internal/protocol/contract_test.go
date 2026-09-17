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
			seen[string(request.Operation)+"-request"] = true
		case "response":
			response, err := codec.DecodeResponse(bytes.NewReader(example.Message))
			if err != nil {
				t.Fatalf("%s: %v", example.Name, err)
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
