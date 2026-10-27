package config

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func mutateV2Fixture(t *testing.T, fixture string, change func(map[string]any, map[string]any)) []byte {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", "compatibility", fixture))
	if err != nil {
		t.Fatal(err)
	}
	input := convertLegacyFixture(t, raw)
	var root map[string]any
	if err := json.Unmarshal(input, &root); err != nil {
		t.Fatal(err)
	}
	destinations := root["destinations"].(map[string]any)
	var dest map[string]any
	for _, value := range destinations {
		dest = value.(map[string]any)
	}
	options := dest["options"].(map[string]any)
	change(dest, options)
	encoded, err := json.Marshal(root)
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}

func TestV2ProviderSpecificRequiredMetadata(t *testing.T) {
	cases := []struct {
		name, fixture, field string
		change               func(map[string]any, map[string]any)
	}{
		{"npm package path", "npm.json", "options.packagePath", func(d, o map[string]any) { delete(o, "packagePath") }},
		{"npm registry type", "npm.json", "options.registry", func(d, o map[string]any) { o["registry"] = true }},
		{"npm auth credential", "npm.json", "options.authentication.credential", func(d, o map[string]any) { delete(o["authentication"].(map[string]any), "credential") }},
		{"npm auth mode", "npm.json", "options.authentication.mode", func(d, o map[string]any) { o["authentication"].(map[string]any)["mode"] = "password" }},
		{"npm unexpected auth field", "npm.json", "options.authentication.password", func(d, o map[string]any) { o["authentication"].(map[string]any)["password"] = "do-not-log" }},
		{"npm null bool", "npm.json", "options.allowInsecureHTTP", func(d, o map[string]any) { o["allowInsecureHTTP"] = nil }},
		{"npm insecure url", "npm.json", "options.registry", func(d, o map[string]any) { o["registry"] = "http://unsafe.example" }},
		{"npm path interpolation", "npm.json", "options.packagePath", func(d, o map[string]any) { o["packagePath"] = "~/.npm/archive.tgz" }},
		{"sdkman candidate", "sdkman-state.json", "options.candidate", func(d, o map[string]any) { delete(o, "candidate") }},
		{"sdkman bad checksums", "sdkman-state.json", "options.checksums", func(d, o map[string]any) { o["checksums"] = []string{"bad"} }},
		{"sdkman state email", "sdkman-state.json", "options.authentication.emailCredential", func(d, o map[string]any) { delete(o["authentication"].(map[string]any), "emailCredential") }},
		{"sdkman state password", "sdkman-state.json", "options.authentication.passwordCredential", func(d, o map[string]any) { delete(o["authentication"].(map[string]any), "passwordCredential") }},
		{"sdkman invalid mode", "sdkman-state.json", "options.authentication.mode", func(d, o map[string]any) { o["authentication"].(map[string]any)["mode"] = "none" }},
		{"sdkman legacy key", "sdkman-legacy.json", "options.authentication.consumerKeyCredential", func(d, o map[string]any) { delete(o["authentication"].(map[string]any), "consumerKeyCredential") }},
		{"sdkman legacy token", "sdkman-legacy.json", "options.authentication.consumerTokenCredential", func(d, o map[string]any) { delete(o["authentication"].(map[string]any), "consumerTokenCredential") }},
		{"homebrew repository", "homebrew-formula.json", "options.repository", func(d, o map[string]any) { delete(o, "repository") }},
		{"homebrew mode", "homebrew-formula.json", "options.mode", func(d, o map[string]any) { o["mode"] = "unsafe" }},
		{"homebrew manifest type", "homebrew-formula.json", "options.manifest.type", func(d, o map[string]any) { o["manifest"].(map[string]any)["type"] = "unexpected" }},
		{"homebrew manifest binary", "homebrew-formula.json", "options.manifest.binary", func(d, o map[string]any) { delete(o["manifest"].(map[string]any), "binary") }},
		{"homebrew unknown manifest option", "homebrew-formula.json", "options.manifest.inject", func(d, o map[string]any) { o["manifest"].(map[string]any)["inject"] = "unexpected" }},
		{"homebrew pr repository", "homebrew-jvm.json", "options.pullRequest.repository", func(d, o map[string]any) { delete(o["pullRequest"].(map[string]any), "repository") }},
		{"winget publisher", "winget-msi.json", "options.package.publisher", func(d, o map[string]any) { delete(o["package"].(map[string]any), "publisher") }},
		{"winget installer URL", "winget-msi.json", "options.installer.url", func(d, o map[string]any) { delete(o["installer"].(map[string]any), "url") }},
		{"winget installer type", "winget-msi.json", "options.installer.type", func(d, o map[string]any) { o["installer"].(map[string]any)["type"] = 42 }},
		{"winget auth credential", "winget-msi.json", "options.authentication.credential", func(d, o map[string]any) { delete(o["authentication"].(map[string]any), "credential") }},
		{"winget pull request repository", "winget-msi.json", "options.pullRequest.repository", func(d, o map[string]any) { delete(o["pullRequest"].(map[string]any), "repository") }},
		{"winget nested installer bad type", "winget-zip.json", "options.installer.nestedInstallerFiles", func(d, o map[string]any) { o["installer"].(map[string]any)["nestedInstallerFiles"] = "invalid" }},
		{"winget nested file missing path", "winget-zip.json", "options.installer.nestedInstallerFiles[0].relativeFilePath", func(d, o map[string]any) {
			files := o["installer"].(map[string]any)["nestedInstallerFiles"].([]any)
			delete(files[0].(map[string]any), "relativeFilePath")
		}},
		{"winget unknown package key", "winget-msi.json", "options.package.secret", func(d, o map[string]any) { o["package"].(map[string]any)["secret"] = "not-allowed" }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			raw := mutateV2Fixture(t, tc.fixture, tc.change)
			_, err := Decode(bytes.NewReader(raw))
			if err == nil {
				t.Fatal("invalid provider metadata accepted")
			}
			if !strings.Contains(err.Error(), tc.field) {
				t.Fatalf("error did not name %s: %v", tc.field, err)
			}
			if strings.Contains(err.Error(), "do-not-log") {
				t.Fatalf("unsafe value was logged: %v", err)
			}
		})
	}
}

func TestV2CoreTypeAndDestinationSafety(t *testing.T) {
	basic := `{"schemaVersion":"2","release":{"id":"v2","artifacts":{"app":{"source":"dist/app"}}},"destinations":{"npm":{"artifact":"app","options":{"packagePath":"dist/app","registry":"https://registry.npmjs.org/","authentication":{"mode":"token","credential":"publish"}}}}}`
	cases := []struct{ label, raw, field string }{
		{"missing destinations", strings.Replace(basic, `"destinations":{"npm":`, `"destinations":{"other":`, 1), "destinations.other"},
		{"nonobject artifacts", strings.Replace(basic, `"artifacts":{"app":{"source":"dist/app"}}`, `"artifacts":[]`, 1), "release"},
		{"missing release id", strings.Replace(basic, `"id":"v2"`, `"id":""`, 1), "release.id"},
		{"array without explicit id", `{"schemaVersion":"2","release":{"id":"v2","artifacts":{"app":{"source":"dist/app"}}},"destinations":{"npm":[{"artifact":"app","options":{"packagePath":"dist/app","registry":"https://registry.npmjs.org/","authentication":{"mode":"token","credential":"publish"}}}]}}`, "destinations.npm[0].id"},
		{"unknown artifact key", strings.Replace(basic, `"source":"dist/app"`, `"source":"dist/app","digest":"inject"`, 1), "release.artifacts.app"},
		{"unknown config provider", strings.Replace(basic, `"npm":{`, `"pypi":{`, 1), "destinations.pypi"},
		{"single missing options", `{"schemaVersion":"2","release":{"id":"v2","artifacts":{"app":{"source":"dist/app"}}},"destinations":{"npm":{"artifact":"app","options":null}}}`, "destinations.npm.options"},
	}
	for _, tc := range cases {
		t.Run(tc.label, func(t *testing.T) {
			_, err := Decode(strings.NewReader(tc.raw))
			if err == nil {
				t.Fatal("invalid configuration accepted")
			}
			if !strings.Contains(err.Error(), tc.field) {
				t.Fatalf("missing path %q: %v", tc.field, err)
			}
		})
	}
}

func TestV2JSONSchemaReferenceIsValid(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "schemas", "config-v2.schema.json"))
	if err != nil {
		t.Fatal(err)
	}
	var schema map[string]any
	if err := json.Unmarshal(data, &schema); err != nil {
		t.Fatal(err)
	}
	if schema["type"] != "object" {
		t.Fatalf("invalid reference schema root: %v", schema["type"])
	}
}
