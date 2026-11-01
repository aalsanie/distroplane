package providerhost

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/aalsanie/distroplane/internal/protocol"
)

func TestRedactionCoversNullableAndStructuredProviderOutputs(t *testing.T) {
	empty := newRedactor(nil)
	original := json.RawMessage(`{"plain":"unchanged"}`)
	if string(empty.json(original)) != string(original) {
		t.Fatal("empty redactor altered JSON")
	}
	if len(empty.json(nil)) != 0 {
		t.Fatal("nil JSON changed")
	}
	sensitive := newRedactor([][]byte{[]byte("sensitive"), []byte("sensitive"), nil})
	if got := sensitive.text("no secrets"); got != "no secrets" {
		t.Fatal(got)
	}
	if got := string(sensitive.json(json.RawMessage(`{"nested":["sensitive",{"sensitive":true},false,null]}`))); strings.Contains(got, "sensitive") {
		t.Fatalf("nested JSON not redacted: %s", got)
	}
	if got := string(sensitive.json(json.RawMessage(`{bad`))); got != "{bad" {
		t.Fatalf("invalid JSON modified: %s", got)
	}
	err := &protocol.ProviderError{Message: "sensitive", Details: json.RawMessage(`{"token":"sensitive"}`)}
	redactProviderError(err, sensitive)
	if strings.Contains(err.Message+string(err.Details), "sensitive") {
		t.Fatalf("provider error leaked: %+v", err)
	}
	redactProviderError(nil, sensitive)
	redactProviderError(&protocol.ProviderError{}, empty)
	apply := &protocol.ApplyResponse{Result: protocol.DistributionResult{
		ProviderState: "sensitive", Evidence: json.RawMessage(`{"url":"sensitive"}`),
	}}
	reconcile := &protocol.ReconcileResponse{Result: protocol.DistributionResult{
		ProviderState: "sensitive", Evidence: json.RawMessage(`["sensitive"]`),
	}}
	for _, destination := range []any{apply, reconcile} {
		redactDestination(destination, sensitive)
	}
	if strings.Contains(apply.Result.ProviderState+string(apply.Result.Evidence), "sensitive") ||
		strings.Contains(reconcile.Result.ProviderState+string(reconcile.Result.Evidence), "sensitive") {
		t.Fatal("provider success response leaked credential material")
	}
	redactDestination(&protocol.DescribeResponse{}, sensitive)
	redactDestination(apply, empty)
	redactDistributionResult(nil, sensitive)
}
