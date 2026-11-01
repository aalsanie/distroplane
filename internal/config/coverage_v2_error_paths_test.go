package config

import (
	"encoding/json"
	"path/filepath"
	"testing"
)

func TestV2PathResolverRejectsMalformedProviderOptions(t *testing.T) {
	cases := []struct{
		name, provider, configuration string
		valid bool
	}{
		{"malformed npm", "npm", "{", false},
		{"npm packagePath wrong type", "npm", `{"packagePath":14}`, false},
		{"npm absolute", "npm", `{"packagePath":"/tmp/package"}`, true},
		{"winget installer wrong type", "winget", `{"installer":"invalid"}`, false},
		{"winget ZIP archive path wrong type", "winget", `{"installer":{"type":"zip","archivePath":12}}`, false},
		{"winget ZIP absolute archive", "winget", `{"installer":{"type":"zip","archivePath":"/tmp/release.zip"}}`, true},
		{"winget nonZIP", "winget", `{"installer":{"type":"exe"}}`, true},
		{"homebrew options unchanged", "homebrew", `{}`, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T){
			cfg := Config{SchemaVersion: SchemaVersionV2, Targets: []Target{{
				Provider: Provider{Name: tc.provider}, Configuration: json.RawMessage(tc.configuration),
			}}}
			err := resolveV2Paths(&cfg, t.TempDir())
			if (err == nil) != tc.valid {
				t.Fatalf("provider=%s config=%s err=%v", tc.provider, tc.configuration, err)
			}
		})
	}
	if err := resolveV2Paths(&Config{SchemaVersion: SchemaVersion}, filepath.Join(t.TempDir(), "nonexistent")); err != nil {
		t.Fatalf("v1 config unexpectedly subject to v2 resolver: %v", err)
	}
}

func TestV2LiteralPathsRejectInterpolationSyntax(t *testing.T) {
	for _, path := range []string{
		"~/downloads/archive.zip", "${HOME}/archive.zip", "$(pwd)/archive.zip",
		"%{APPDATA}/archive.zip", " path", "bad\npath",
	} {
		if err := validLocalPath("artifact", path); err == nil {
			t.Errorf("interpolated or unsafe filesystem path accepted: %q", path)
		}
	}
	for _, path := range []string{"lib/archive.zip", "release.tgz", "/var/tmp/release.tgz"} {
		if err := validLocalPath("artifact", path); err != nil {
			t.Errorf("literal local path rejected: %q, %v", path, err)
		}
	}
	for _, provider := range []string{"npm", "sdkman", "homebrew", "winget"} {
		if !supportedV2Provider(provider) {
			t.Errorf("supported provider rejected: %s", provider)
		}
	}
	if supportedV2Provider("untrusted") {
		t.Fatal("unsupported provider accepted")
	}
	var decoded struct{ A string `json:"a"` }
	if err := strictV2JSON([]byte(`{"a":"ok"} {}`), &decoded); err == nil {
		t.Fatal("extra configuration object accepted")
	}
}
