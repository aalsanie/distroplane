package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const validConfig = `{
  "schemaVersion":"1",
  "release":{"id":"v1","artifacts":[{"name":"app","source":"dist/app","mediaType":"application/octet-stream"}]},
  "targets":[{"id":"primary","provider":{"name":"fake"},"configuration":{"z":2,"a":1}}]
}`

func TestDecodeNormalizesAndValidates(t *testing.T) {
	cfg, err := Decode(strings.NewReader(validConfig))
	if err != nil {
		t.Fatal(err)
	}
	if got := string(cfg.Targets[0].Configuration); got != `{"a":1,"z":2}` {
		t.Fatalf("configuration=%s", got)
	}
}

func TestDecodeRejectsInvalidConfig(t *testing.T) {
	cases := map[string]string{
		"nil reader":                  "",
		"unsupported schema":          strings.Replace(validConfig, `"schemaVersion":"1"`, `"schemaVersion":"2"`, 1),
		"missing artifacts":           `{"schemaVersion":"1","release":{"id":"v1","artifacts":[]},"targets":[{"id":"t","provider":{"name":"p"},"configuration":{}}]}`,
		"missing targets":             `{"schemaVersion":"1","release":{"id":"v1","artifacts":[{"name":"a","source":"x"}]},"targets":[]}`,
		"duplicate artifact":          `{"schemaVersion":"1","release":{"id":"v1","artifacts":[{"name":"a","source":"x"},{"name":"a","source":"y"}]},"targets":[{"id":"t","provider":{"name":"p"},"configuration":{}}]}`,
		"duplicate target":            `{"schemaVersion":"1","release":{"id":"v1","artifacts":[{"name":"a","source":"x"}]},"targets":[{"id":"t","provider":{"name":"p"},"configuration":{}},{"id":"t","provider":{"name":"p"},"configuration":{}}]}`,
		"unknown field":               strings.Replace(validConfig, `"schemaVersion":"1"`, `"schemaVersion":"1","unexpected":true`, 1),
		"duplicate JSON key":          `{"schemaVersion":"1","schemaVersion":"1","release":{"id":"v1","artifacts":[{"name":"a","source":"x"}]},"targets":[{"id":"t","provider":{"name":"p"},"configuration":{}}]}`,
		"duplicate configuration key": `{"schemaVersion":"1","release":{"id":"v1","artifacts":[{"name":"a","source":"x"}]},"targets":[{"id":"t","provider":{"name":"p"},"configuration":{"x":1,"x":2}}]}`,
		"bad release id":              strings.Replace(validConfig, `"id":"v1"`, `"id":" v1"`, 1),
		"bad artifact source":         strings.Replace(validConfig, `"source":"dist/app"`, `"source":" dist/app"`, 1),
		"bad media type":              strings.Replace(validConfig, `"mediaType":"application/octet-stream"`, `"mediaType":" x"`, 1),
		"bad target id":               strings.Replace(validConfig, `"id":"primary"`, `"id":" primary"`, 1),
		"bad provider name":           strings.Replace(validConfig, `"name":"fake"`, `"name":" fake"`, 1),
		"bad executable":              strings.Replace(validConfig, `"provider":{"name":"fake"}`, `"provider":{"name":"fake","executable":" x"}`, 1),
	}
	if _, err := Decode(nil); err == nil {
		t.Fatal("nil reader accepted")
	}
	for name, raw := range cases {
		if name == "nil reader" {
			continue
		}
		t.Run(name, func(t *testing.T) {
			if _, err := Decode(strings.NewReader(raw)); err == nil {
				t.Fatal("expected error")
			}
		})
	}
}

func TestDecodeRejectsOversizedConfig(t *testing.T) {
	raw := strings.Repeat(" ", MaxConfigBytes+1)
	if _, err := Decode(strings.NewReader(raw)); err == nil {
		t.Fatal("expected oversized error")
	}
}

func TestLoad(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "distroplane.json")
	if err := os.WriteFile(path, []byte(validConfig), 0o600); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	abs, _ := filepath.Abs(dir)
	if loaded.BaseDir != abs {
		t.Fatalf("base dir=%q want %q", loaded.BaseDir, abs)
	}
	if loaded.Config.Release.ID != "v1" {
		t.Fatal("wrong config")
	}

	if _, err := Load(""); err == nil {
		t.Fatal("empty path accepted")
	}
	if _, err := Load(filepath.Join(dir, "missing.json")); err == nil {
		t.Fatal("missing path accepted")
	}
}

func FuzzDecode(f *testing.F) {
	f.Add(validConfig)
	f.Add(`{"schemaVersion":"1"}`)
	f.Fuzz(func(t *testing.T, raw string) {
		_, _ = Decode(strings.NewReader(raw))
	})
}
