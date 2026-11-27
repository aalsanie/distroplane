package config

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/aalsanie/distroplane/internal/credentials"
	"github.com/aalsanie/distroplane/internal/domain"
)

const SchemaVersionV2 = "2"

type configV2 struct {
	SchemaVersion     string                     `json:"schemaVersion"`
	Release           releaseV2                  `json:"release"`
	Destinations      map[string]json.RawMessage `json:"destinations"`
	CredentialSources json.RawMessage            `json:"credentialSources,omitempty"`
}
type releaseV2 struct {
	ID        string                     `json:"id"`
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

func decodeV2(raw []byte, normalize OptionNormalizer) (Config, error) {
	var input configV2
	if err := strictV2JSON(raw, &input); err != nil {
		return Config{}, fmt.Errorf("config v2: %w", err)
	}
	if err := ValidateText("release.id", input.Release.ID, false); err != nil {
		return Config{}, err
	}
	if len(input.Release.Artifacts) == 0 {
		return Config{}, fmt.Errorf("release.artifacts: expected at least one artifact")
	}
	if len(input.Destinations) == 0 {
		return Config{}, fmt.Errorf("destinations: expected at least one provider")
	}
	out := Config{SchemaVersion: SchemaVersionV2, Release: Release{ID: input.Release.ID}}
	if len(input.CredentialSources) != 0 {
		if input.CredentialSources[0] != '{' {
			return Config{}, fmt.Errorf("credentialSources: expected object of environment variable names")
		}
		var sources map[string]string
		if err := json.Unmarshal(input.CredentialSources, &sources); err != nil {
			return Config{}, fmt.Errorf("credentialSources: expected object of strings")
		}
		for ref, source := range sources {
			if _, err := domain.NewCredentialRef(ref); err != nil {
				return Config{}, fmt.Errorf("credentialSources: invalid credential reference")
			}
			if !credentials.ValidEnvironmentName(source) {
				return Config{}, fmt.Errorf("credentialSources.%s: invalid environment variable name", ref)
			}
		}
		out.CredentialSources = sources
	}
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
		if err := ValidateText(path, name, false); err != nil {
			return Config{}, err
		}
		if err := ValidateLocalPath(path+".source", a.Source); err != nil {
			return Config{}, err
		}
		if err := ValidateText(path+".mediaType", a.MediaType, true); err != nil {
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
			if err := ValidateText(field+".id", id, false); err != nil {
				return Config{}, err
			}
			if seen[id] {
				return Config{}, fmt.Errorf("%s.id: duplicate destination ID", field)
			}
			seen[id] = true
			if err := ValidateText(field+".artifact", d.Artifact, false); err != nil {
				return Config{}, err
			}
			if _, ok := input.Release.Artifacts[d.Artifact]; !ok {
				return Config{}, fmt.Errorf("%s.artifact: undefined artifact reference", field)
			}
			if d.Executable != "" {
				if err := ValidateLocalPath(field+".executable", d.Executable); err != nil {
					return Config{}, err
				}
			}
			options, err := normalize(provider, d.Options, d.Artifact, field+".options")
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

func ValidateLocalPath(field, value string) error {
	if err := ValidateText(field, value, false); err != nil {
		return err
	}
	if strings.HasPrefix(value, "~") || strings.Contains(value, "$"+"{") || strings.Contains(value, "$(") || strings.Contains(value, "%{") {
		return fmt.Errorf("%s: use a literal filesystem path without interpolation", field)
	}
	return nil
}
