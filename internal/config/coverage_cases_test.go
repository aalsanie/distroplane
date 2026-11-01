package config

import (
	"errors"
	"io"
	"strings"
	"testing"
)

type brokenConfigReader struct{}

func (brokenConfigReader) Read([]byte) (int, error) {
	return 0, errors.New("controlled input failure")
}

func TestConfigurationInputAndValidationBranches(t *testing.T) {
	if _, err := Decode(brokenConfigReader{}); err == nil {
		t.Fatal("read failure accepted")
	}
	if _, err := Load("   "); err == nil {
		t.Fatal("blank config path accepted")
	}
	if _, err := Decode(strings.NewReader("not-json")); err == nil {
		t.Fatal("malformed config accepted")
	}
	if _, err := Decode(strings.NewReader(strings.Repeat(" ", MaxConfigBytes+1))); err == nil {
		t.Fatal("oversized config accepted")
	}
	valid := Config{
		SchemaVersion: SchemaVersion,
		Release: Release{ID: "release", Artifacts: []Artifact{{Name: "bin", Source: "bin.zip"}}},
		Targets: []Target{{ID: "destination", Provider: Provider{Name: "fake"}, Configuration: []byte(`{}`)}},
	}
	if err := valid.normalizeAndValidate(); err != nil {
		t.Fatalf("baseline config invalid: %v", err)
	}
	for name, mutate := range map[string]func(*Config){
		"schema": func(c *Config) { c.SchemaVersion = "999" },
		"release": func(c *Config) { c.Release.ID = "invalid\nrelease" },
		"no artifacts": func(c *Config) { c.Release.Artifacts = nil },
		"artifact name": func(c *Config) { c.Release.Artifacts[0].Name = "" },
		"artifact source": func(c *Config) { c.Release.Artifacts[0].Source = "" },
		"artifact media type": func(c *Config) { c.Release.Artifacts[0].MediaType = "bad\n" },
		"duplicate artifact": func(c *Config) { c.Release.Artifacts = append(c.Release.Artifacts, c.Release.Artifacts[0]) },
		"no targets": func(c *Config) { c.Targets = nil },
		"target identifier": func(c *Config) { c.Targets[0].ID = " x" },
		"duplicate target": func(c *Config) { c.Targets = append(c.Targets, c.Targets[0]) },
		"provider name": func(c *Config) { c.Targets[0].Provider.Name = "" },
		"provider executable": func(c *Config) { c.Targets[0].Provider.Executable = "bad\x00" },
		"configuration": func(c *Config) { c.Targets[0].Configuration = []byte(`{bad`) },
	} {
		t.Run(name, func(t *testing.T) {
			cfg := valid
			cfg.Release.Artifacts = append([]Artifact(nil), valid.Release.Artifacts...)
			cfg.Targets = append([]Target(nil), valid.Targets...)
			mutate(&cfg)
			if err := cfg.normalizeAndValidate(); err == nil {
				t.Fatal("invalid configuration accepted")
			}
		})
	}
	for _, val := range []string{string([]byte{0xff}), "bad\n", " space ", ""} {
		if err := validateText("test", val, false); err == nil {
			t.Errorf("invalid text accepted: %q", val)
		}
	}
	if err := validateText("optional", "", true); err != nil {
		t.Fatal(err)
	}
	if err := validateText("valid", "name", false); err != nil {
		t.Fatal(err)
	}
	var _ io.Reader = brokenConfigReader{}
}
