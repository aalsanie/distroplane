package evidence

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestEvidenceSecretKeyTaxonomyRejectsCredentials(t *testing.T) {
	names := []string{
		"authorization", "credential", "credentials", "password", "passwd",
		"secret", "token", "access_token", "refresh-token", "api_key",
		"clientSecret", "private-key", "signature", "X-Amz-Credential",
		"X-Amz-Security-Token", "X-Amz-Signature", "GoogleAccessId",
	}
	unsafe := make(map[string]any, len(names))
	for _, name := range names {
		if !secretKey(name) {
			t.Errorf("credential-bearing field was not recognized: %q", name)
		}
		unsafe[name] = "credential-canary"
		if uri, err := sanitizeURI("https://example.test/file?"+name+"=credential-canary"); err != nil ||
			strings.Contains(uri, "credential-canary") {
			t.Errorf("credential-bearing query parameter leaked for %q: %q, %v", name, uri, err)
		}
	}
	cleaned := sanitizeValue(map[string]any{"nested": unsafe, "safe": "ok"})
	data, err := json.Marshal(cleaned)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "credential-canary") || !strings.Contains(string(data), "ok") {
		t.Fatalf("sanitizer did not preserve safe fields or redact every credential: %s", data)
	}
	for _, name := range []string{"version", "id", "description", "sha256", "url", "digest"} {
		if secretKey(name) {
			t.Errorf("safe metadata field blocked: %q", name)
		}
	}
	if uri, err := sanitizeURI("https://user:password@example.test/path"); err != nil ||
		strings.Contains(uri, "password") || strings.Contains(uri, "user") {
		t.Fatalf("URL userinfo leaked: %s (%v)", uri, err)
	}
	if _, err := normalizeEvidence(json.RawMessage("{")); err == nil {
		t.Fatal("malformed evidence was normalized")
	}
	if got := externalReferences(json.RawMessage("{")); got != nil {
		t.Fatalf("malformed evidence yielded references: %v", got)
	}
}
