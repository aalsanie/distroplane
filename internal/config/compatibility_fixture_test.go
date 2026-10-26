package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStableProviderConfigurationV1Fixtures(t *testing.T) {
	paths, err := filepath.Glob(filepath.Join("testdata", "compatibility", "*.json"))
	if err != nil || len(paths) != 7 {
		t.Fatalf("configuration fixtures: files=%d err=%v", len(paths), err)
	}
	for _, path := range paths {
		t.Run(filepath.Base(path), func(t *testing.T) {
			raw, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			config, err := Decode(strings.NewReader(string(raw)))
			if err != nil {
				t.Fatalf("saved configuration no longer decodes: %v", err)
			}
			if config.SchemaVersion != "1" || len(config.Targets) != 1 || len(config.Release.Artifacts) != 1 ||
				len(config.Targets[0].Configuration) == 0 {
				t.Fatalf("configuration unexpectedly changed: %+v", config)
			}
			// Backward compatibility includes strict rejection of unsupported core versions.
			changed := strings.Replace(string(raw), `"schemaVersion": "1"`, `"schemaVersion": "2"`, 1)
			if changed == string(raw) {
				t.Fatal("schema marker missing")
			}
			if _, err := Decode(strings.NewReader(changed)); err == nil {
				t.Fatal("unsupported configuration version silently accepted")
			}
		})
	}
}
