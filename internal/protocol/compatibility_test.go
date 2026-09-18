package protocol

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

type compatibilityManifest struct {
	Status                 string   `json:"status"`
	ProtocolVersion        string   `json:"protocolVersion"`
	ReservedEnvelopeFields []string `json:"reservedEnvelopeFields"`
	ExtensionPoints        []string `json:"extensionPoints"`
	UnknownFields          string   `json:"unknownFields"`
	UnknownOperations      string   `json:"unknownOperations"`
	UnknownCapabilities    string   `json:"unknownCapabilities"`
	VersionNegotiation     struct {
		Advertisement string `json:"advertisement"`
		Selection     string `json:"selection"`
		Current       string `json:"current"`
	} `json:"versionNegotiation"`
}

func TestProtocolV1CompatibilityManifest(t *testing.T) {
	path := filepath.Join("..", "..", "protocol", "schema", "v1", "compatibility.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var manifest compatibilityManifest
	if err := json.Unmarshal(raw, &manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.Status != "candidate" || manifest.ProtocolVersion != Version || manifest.UnknownFields != "ignored" {
		t.Fatalf("manifest=%+v", manifest)
	}
	if manifest.UnknownOperations != "rejected" || manifest.UnknownCapabilities != "rejected" || manifest.VersionNegotiation.Current != Version {
		t.Fatalf("manifest=%+v", manifest)
	}
	if len(manifest.ReservedEnvelopeFields) == 0 || len(manifest.ExtensionPoints) == 0 {
		t.Fatal("compatibility contract is incomplete")
	}
}

func TestProtocolV1UnknownFieldsAreForwardCompatible(t *testing.T) {
	codec := NewCodec(DefaultMaxMessageBytes)
	request := []byte(`{"protocolVersion":"1","requestId":"x","operation":"describe","futureEnvelope":true,"payload":{"futurePayload":true}}`)
	if _, err := codec.DecodeRequest(bytes.NewReader(request)); err != nil {
		t.Fatalf("unknown request fields rejected: %v", err)
	}
	response := []byte(`{"protocolVersion":"1","requestId":"x","operation":"describe","status":"ok","futureEnvelope":true,"payload":{"provider":{"name":"fake","version":"1"},"protocolVersions":["1"],"capabilities":["plan"],"futurePayload":true}}`)
	if _, err := codec.DecodeResponse(bytes.NewReader(response)); err != nil {
		t.Fatalf("unknown response fields rejected: %v", err)
	}
}
