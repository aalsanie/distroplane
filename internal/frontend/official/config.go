package official

import (
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"

	"github.com/aalsanie/distroplane/internal/canonicaljson"
	"github.com/aalsanie/distroplane/internal/config"
)

func Decode(reader io.Reader) (config.Config, error) {
	return config.DecodeWithOptions(reader, normalizeOptions)
}

func Load(path string) (config.Loaded, error) {
	loaded, err := config.LoadWithOptions(path, normalizeOptions)
	if err != nil {
		return config.Loaded{}, err
	}
	if err := resolveV2Paths(&loaded.Config, loaded.BaseDir); err != nil {
		return config.Loaded{}, fmt.Errorf("config %q: %w", path, err)
	}
	return loaded, nil
}

func normalizeOptions(provider string, raw json.RawMessage, artifact, field string) (json.RawMessage, error) {
	if !supportedV2Provider(provider) {
		return nil, fmt.Errorf("destinations.%s: unsupported provider", provider)
	}
	return normalizeV2Options(provider, raw, artifact, field)
}

func supportedV2Provider(name string) bool {
	switch name {
	case "npm", "sdkman", "homebrew", "winget":
		return true
	}
	return false
}

func resolveV2Paths(cfg *config.Config, baseDir string) error {
	if cfg.SchemaVersion != config.SchemaVersionV2 {
		return nil
	}
	for i := range cfg.Targets {
		t := &cfg.Targets[i]
		if t.Provider.Name != "npm" && t.Provider.Name != "winget" {
			continue
		}
		var options map[string]json.RawMessage
		if err := json.Unmarshal(t.Configuration, &options); err != nil {
			return err
		}
		key := "packagePath"
		if t.Provider.Name == "winget" {
			key = "installer"
			var installer map[string]json.RawMessage
			if err := json.Unmarshal(options[key], &installer); err != nil {
				return err
			}
			var kind string
			_ = json.Unmarshal(installer["type"], &kind)
			if kind != "zip" {
				continue
			}
			var archive string
			if err := json.Unmarshal(installer["archivePath"], &archive); err != nil {
				return err
			}
			if !filepath.IsAbs(archive) {
				full, err := filepath.Abs(filepath.Join(baseDir, archive))
				if err != nil {
					return fmt.Errorf("destinations.winget.options.installer.archivePath: %w", err)
				}
				installer["archivePath"], err = json.Marshal(full)
				if err != nil {
					return err
				}
			}
			options[key], _ = json.Marshal(installer)
		} else {
			var packagePath string
			if err := json.Unmarshal(options[key], &packagePath); err != nil {
				return err
			}
			if filepath.IsAbs(packagePath) {
				continue
			}
			full, err := filepath.Abs(filepath.Join(baseDir, packagePath))
			if err != nil {
				return fmt.Errorf("destinations.npm.options.packagePath: %w", err)
			}
			options[key], err = json.Marshal(full)
			if err != nil {
				return err
			}
		}
		raw, err := json.Marshal(options)
		if err != nil {
			return err
		}
		t.Configuration, err = canonicaljson.Normalize(raw)
		if err != nil {
			return err
		}
	}
	return nil
}
