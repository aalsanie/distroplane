package config

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"sort"
	"strings"

	"github.com/aalsanie/distroplane/internal/canonicaljson"
)

const SchemaVersionV2 = "2"

type configV2 struct {
	SchemaVersion string                     `json:"schemaVersion"`
	Release       releaseV2                  `json:"release"`
	Destinations  map[string]json.RawMessage `json:"destinations"`
}
type releaseV2 struct {
	ID        string                `json:"id"`
	Artifacts map[string]json.RawMessage `json:"artifacts"`
}
type artifactV2 struct {
	Source    string `json:"source"`
	MediaType string `json:"mediaType,omitempty"`
}
type destinationV2 struct {
	ID         string          `json:"id,omitempty"`
	Artifact   string          `json:"artifact"`
	Executable string          `json:"executable,omitempty"`
	Options    json.RawMessage `json:"options"`
}

func strictV2JSON(raw []byte, dst any) error {
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if err := d.Decode(dst); err != nil {
		return err
	}
	var extra any
	if err := d.Decode(&extra); err != io.EOF {
		return fmt.Errorf("trailing JSON data")
	}
	return nil
}

func decodeV2(raw []byte) (Config, error) {
	var input configV2
	if err := strictV2JSON(raw, &input); err != nil {
		return Config{}, fmt.Errorf("config v2: %w", err)
	}
	if err := validateText("release.id", input.Release.ID, false); err != nil {
		return Config{}, err
	}
	if len(input.Release.Artifacts) == 0 {
		return Config{}, fmt.Errorf("release.artifacts: expected at least one artifact")
	}
	if len(input.Destinations) == 0 {
		return Config{}, fmt.Errorf("destinations: expected at least one provider")
	}
	out := Config{SchemaVersion: SchemaVersionV2, Release: Release{ID: input.Release.ID}}
	names := make([]string, 0, len(input.Release.Artifacts))
	for name := range input.Release.Artifacts {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		var a artifactV2
		if err := strictV2JSON(input.Release.Artifacts[name], &a); err != nil {
			return Config{}, fmt.Errorf("release.artifacts.%s: %w", name, err)
		}
		path := "release.artifacts." + name
		if err := validateText(path, name, false); err != nil {
			return Config{}, err
		}
		if err := validLocalPath(path+".source", a.Source); err != nil {
			return Config{}, err
		}
		if err := validateText(path+".mediaType", a.MediaType, true); err != nil {
			return Config{}, err
		}
		out.Release.Artifacts = append(out.Release.Artifacts, Artifact{Name: name, Source: a.Source, MediaType: a.MediaType})
	}
	providers := make([]string, 0, len(input.Destinations))
	for name := range input.Destinations {
		providers = append(providers, name)
	}
	sort.Strings(providers)
	seen := map[string]bool{}
	for _, provider := range providers {
		if !supportedV2Provider(provider) {
			return Config{}, fmt.Errorf("destinations.%s: unsupported provider", provider)
		}
		raw := input.Destinations[provider]
		values := []json.RawMessage{raw}
		array := len(raw) > 0 && raw[0] == '['
		if array {
			if err := json.Unmarshal(raw, &values); err != nil || len(values) == 0 {
				return Config{}, fmt.Errorf("destinations.%s: expected nonempty destination array", provider)
			}
		}
		for i, item := range values {
			field := "destinations." + provider
			if array {
				field = fmt.Sprintf("%s[%d]", field, i)
			}
			var d destinationV2
			if err := strictV2JSON(item, &d); err != nil {
				return Config{}, fmt.Errorf("%s: %w", field, err)
			}
			id := d.ID
			if id == "" && !array {
				id = provider
			}
			if err := validateText(field+".id", id, false); err != nil {
				return Config{}, err
			}
			if seen[id] {
				return Config{}, fmt.Errorf("%s.id: duplicate destination ID", field)
			}
			seen[id] = true
			if err := validateText(field+".artifact", d.Artifact, false); err != nil {
				return Config{}, err
			}
			if _, ok := input.Release.Artifacts[d.Artifact]; !ok {
				return Config{}, fmt.Errorf("%s.artifact: undefined artifact reference", field)
			}
			if d.Executable != "" {
				if err := validLocalPath(field+".executable", d.Executable); err != nil {
					return Config{}, err
				}
			}
			options, err := normalizeV2Options(provider, d.Options, d.Artifact, field+".options")
			if err != nil {
				return Config{}, err
			}
			out.Targets = append(out.Targets, Target{ID: id, Provider: Provider{Name: provider, Executable: d.Executable}, Configuration: options})
		}
	}
	if err := out.normalizeAndValidate(); err != nil {
		return Config{}, err
	}
	return out, nil
}

func supportedV2Provider(name string) bool {
	switch name {
	case "npm", "sdkman", "homebrew", "winget":
		return true
	}
	return false
}
func validLocalPath(field, value string) error {
	if err := validateText(field, value, false); err != nil {
		return err
	}
	if strings.HasPrefix(value, "~") || strings.Contains(value, "$"+"{") || strings.Contains(value, "$(") || strings.Contains(value, "%{") {
		return fmt.Errorf("%s: use a literal filesystem path without interpolation", field)
	}
	return nil
}

func resolveV2Paths(cfg *Config, baseDir string) error {
	if cfg.SchemaVersion != SchemaVersionV2 {
		return nil
	}
	for i := range cfg.Targets {
		t := &cfg.Targets[i]
		if t.Provider.Name != "npm" {
			continue
		}
		var options map[string]json.RawMessage
		if err := json.Unmarshal(t.Configuration, &options); err != nil {
			return err
		}
		var packagePath string
		if err := json.Unmarshal(options["packagePath"], &packagePath); err != nil {
			return err
		}
		if filepath.IsAbs(packagePath) {
			continue
		}
		full, err := filepath.Abs(filepath.Join(baseDir, packagePath))
		if err != nil {
			return fmt.Errorf("destinations.npm.options.packagePath: %w", err)
		}
		options["packagePath"], err = json.Marshal(full)
		if err != nil {
			return err
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
