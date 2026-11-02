package journal

import (
	"encoding/json"
	"testing"
)

func TestJournalCredentialFieldTaxonomyAndNestedEvidence(t *testing.T) {
	for _, key := range []string{
		"authorization", "credential", "credentials", "password",
		"passwd", "secret", "token", "access_token", "refresh-token",
		"api_key", "clientSecret", "private-key",
	} {
		if !secretKey(key) {
			t.Errorf("sensitive field %q was not recognized", key)
		}
		raw, err := json.Marshal(map[string]any{"public": []any{
			map[string]any{key: "hidden"},
		}})
		if err != nil {
			t.Fatal(err)
		}
		if err := validateEvidence(raw); err == nil {
			t.Errorf("secret-bearing field %q accepted as journal evidence", key)
		}
	}
	for _, key := range []string{"package", "version", "sha256", "url", "receipt"} {
		if secretKey(key) {
			t.Fatalf("public field %q incorrectly classified as secret", key)
		}
	}
	for _, raw := range []string{
		`{"public":[false,{"nested":"safe"}]}`, `[null,{"file":"https://example.test/artifact"}]`,
	} {
		if err := validateEvidence([]byte(raw)); err != nil {
			t.Errorf("safe evidence rejected: %s (%v)", raw, err)
		}
	}
	for _, raw := range []string{
		`{"public":true} {"extra":true}`, `{"public":`, `{"secret":"value"}`,
	} {
		if err := validateEvidence([]byte(raw)); err == nil {
			t.Errorf("invalid or secret evidence accepted: %s", raw)
		}
	}
}
