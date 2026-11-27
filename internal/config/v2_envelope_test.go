package config

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const genericV2Fixture = `{"schemaVersion":"2","release":{"id":"release","artifacts":{"app":{"source":"app.bin","mediaType":"application/octet-stream"}}},"destinations":{"custom":{"artifact":"app","options":{"opaque":[1,2]}}},"credentialSources":{"ref":"SOURCE_ENV"}}`

func genericV2Normalizer(provider string, raw json.RawMessage, artifact, field string) (json.RawMessage, error) {
	if provider != "custom" || artifact != "app" || !strings.HasPrefix(field, "destinations.custom") {
		return nil, errors.New("unexpected destination binding")
	}
	if len(raw) == 0 {
		return nil, errors.New("missing options")
	}
	return raw, nil
}

func TestV2EnvelopeDelegatesOpaqueOptions(t *testing.T) {
	cfg, err := DecodeWithOptions(strings.NewReader(genericV2Fixture), genericV2Normalizer)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.SchemaVersion != SchemaVersionV2 || len(cfg.Targets) != 1 || cfg.Targets[0].ID != "custom" ||
		cfg.Targets[0].Provider.Name != "custom" || string(cfg.Targets[0].Configuration) != "{\"opaque\":[1,2]}" ||
		cfg.CredentialSources["ref"] != "SOURCE_ENV" {
		t.Fatalf("unexpected normalized envelope: %+v", cfg)
	}
	multiple := strings.Replace(genericV2Fixture, "\"custom\":{\"artifact\":\"app\",\"options\":{\"opaque\":[1,2]}}", "\"custom\":[{\"id\":\"one\",\"artifact\":\"app\",\"executable\":\"./provider\",\"options\":{}},{\"id\":\"two\",\"artifact\":\"app\",\"options\":{}}]", 1)
	cfg, err = DecodeWithOptions(strings.NewReader(multiple), genericV2Normalizer)
	if err != nil || len(cfg.Targets) != 2 || cfg.Targets[0].Provider.Executable != "./provider" || cfg.Targets[1].ID != "two" {
		t.Fatalf("multiple destinations: %+v %v", cfg, err)
	}
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(genericV2Fixture), 0600); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadWithOptions(path, genericV2Normalizer)
	if err != nil || loaded.BaseDir != filepath.Dir(path) {
		t.Fatalf("loaded=%+v err=%v", loaded, err)
	}
	if _, err := Decode(strings.NewReader(genericV2Fixture)); err == nil {
		t.Fatal("v2 options accepted without a frontend")
	}
	if _, err := Load(path); err == nil {
		t.Fatal("v2 options loaded without a frontend")
	}
}

func TestV2EnvelopeValidationPrecedesNormalization(t *testing.T) {
	replace := func(old, next string) string {
		return strings.Replace(genericV2Fixture, old, next, 1)
	}
	cases := []struct{ name, raw string }{
		{"unknown envelope", replace("\"schemaVersion\":\"2\"", "\"schemaVersion\":\"2\",\"extra\":true")},
		{"release ID", replace("\"id\":\"release\"", "\"id\":\"\"")},
		{"no artifacts", replace("\"app\":{\"source\":\"app.bin\",\"mediaType\":\"application/octet-stream\"}", "")},
		{"no destinations", replace("\"custom\":{\"artifact\":\"app\",\"options\":{\"opaque\":[1,2]}}", "")},
		{"credential array", replace("{\"ref\":\"SOURCE_ENV\"}", "[]")},
		{"credential number", replace("\"SOURCE_ENV\"", "4")},
		{"credential reference", replace("\"ref\":", "\"\":")},
		{"credential source", replace("\"SOURCE_ENV\"", "\"BAD-ENV\"")},
		{"artifact shape", replace("{\"source\":\"app.bin\",\"mediaType\":\"application/octet-stream\"}", "false")},
		{"artifact name", replace("\"app\":", "\" app\":")},
		{"artifact path", replace("\"app.bin\"", "\"~/app.bin\"")},
		{"artifact type", replace("\"application/octet-stream\"", "\" bad\"")},
		{"empty array", replace("{\"artifact\":\"app\",\"options\":{\"opaque\":[1,2]}}", "[]")},
		{"destination shape", replace("{\"artifact\":\"app\",\"options\":{\"opaque\":[1,2]}}", "4")},
		{"destination ID", replace("\"artifact\":\"app\"", "\"id\":\" bad\",\"artifact\":\"app\"")},
		{"missing array ID", replace("{\"artifact\":\"app\",\"options\":{\"opaque\":[1,2]}}", "[{\"artifact\":\"app\",\"options\":{}}]")},
		{"duplicate ID", replace("{\"artifact\":\"app\",\"options\":{\"opaque\":[1,2]}}", "[{\"id\":\"same\",\"artifact\":\"app\",\"options\":{}},{\"id\":\"same\",\"artifact\":\"app\",\"options\":{}}]")},
		{"artifact reference", replace("\"artifact\":\"app\"", "\"artifact\":\"\"")},
		{"undefined artifact", replace("\"artifact\":\"app\"", "\"artifact\":\"missing\"")},
		{"executable path", replace("\"artifact\":\"app\"", "\"artifact\":\"app\",\"executable\":\"~/provider\"")},
		{"normalizer error", replace("\"custom\":", "\"other\":")},
		{"duplicate key", replace("\"schemaVersion\":\"2\"", "\"schemaVersion\":\"2\",\"schemaVersion\":\"2\"")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.raw == genericV2Fixture {
				t.Fatal("invalid fixture mutation")
			}
			if _, err := DecodeWithOptions(strings.NewReader(tc.raw), genericV2Normalizer); err == nil {
				t.Fatal("invalid envelope accepted")
			}
		})
	}
	if _, err := DecodeWithOptions(strings.NewReader(genericV2Fixture), func(string, json.RawMessage, string, string) (json.RawMessage, error) {
		return json.RawMessage("{"), nil
	}); err == nil {
		t.Fatal("invalid normalized JSON accepted")
	}
	for _, path := range []string{"~/archive", "$" + "{HOME}/archive", "$(pwd)/archive", "%{APPDATA}/archive", " bad", "bad\npath"} {
		if err := ValidateLocalPath("path", path); err == nil {
			t.Fatalf("unsafe path accepted: %q", path)
		}
	}
	var decoded struct {
		A string `json:"a"`
	}
	if err := strictV2JSON([]byte("{\"a\":\"ok\"} {}"), &decoded); err == nil {
		t.Fatal("trailing configuration accepted")
	}
}
