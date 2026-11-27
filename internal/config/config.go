package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/aalsanie/distroplane/internal/canonicaljson"
)

const (
	SchemaVersion  = "1"
	MaxConfigBytes = 4 << 20
)

type Config struct {
	SchemaVersion     string            `json:"schemaVersion"`
	Release           Release           `json:"release"`
	Targets           []Target          `json:"targets"`
	CredentialSources map[string]string `json:"-"`
}

type Release struct {
	ID        string     `json:"id"`
	Artifacts []Artifact `json:"artifacts"`
}

type Artifact struct {
	Name      string `json:"name"`
	Source    string `json:"source"`
	MediaType string `json:"mediaType,omitempty"`
}

type Target struct {
	ID            string          `json:"id"`
	Provider      Provider        `json:"provider"`
	Configuration json.RawMessage `json:"configuration"`
}

type Provider struct {
	Name       string `json:"name"`
	Executable string `json:"executable,omitempty"`
}

type Loaded struct {
	Config  Config
	BaseDir string
}

type OptionNormalizer func(provider string, raw json.RawMessage, artifact, field string) (json.RawMessage, error)

func Load(path string) (Loaded, error) {
	return LoadWithOptions(path, nil)
}

func LoadWithOptions(path string, normalize OptionNormalizer) (Loaded, error) {
	if strings.TrimSpace(path) == "" {
		return Loaded{}, fmt.Errorf("config path must not be empty")
	}
	file, err := os.Open(path)
	if err != nil {
		return Loaded{}, err
	}
	defer file.Close()
	cfg, err := DecodeWithOptions(file, normalize)
	if err != nil {
		return Loaded{}, fmt.Errorf("config %q: %w", path, err)
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return Loaded{}, err
	}
	return Loaded{Config: cfg, BaseDir: filepath.Dir(absolute)}, nil
}

func Decode(reader io.Reader) (Config, error) {
	return DecodeWithOptions(reader, nil)
}

func DecodeWithOptions(reader io.Reader, normalize OptionNormalizer) (Config, error) {
	if reader == nil {
		return Config{}, fmt.Errorf("config reader must not be nil")
	}
	data, err := io.ReadAll(io.LimitReader(reader, MaxConfigBytes+1))
	if err != nil {
		return Config{}, err
	}
	if len(data) > MaxConfigBytes {
		return Config{}, fmt.Errorf("config exceeds %d bytes", MaxConfigBytes)
	}
	normalized, err := canonicaljson.Normalize(data)
	if err != nil {
		return Config{}, fmt.Errorf("decode config: %w", err)
	}
	var header struct {
		SchemaVersion string `json:"schemaVersion"`
	}
	if err := json.Unmarshal(normalized, &header); err != nil {
		return Config{}, fmt.Errorf("decode config: %w", err)
	}
	if header.SchemaVersion == SchemaVersionV2 {
		if normalize == nil {
			return Config{}, fmt.Errorf("config v2 requires an option normalizer")
		}
		return decodeV2(normalized, normalize)
	}
	decoder := json.NewDecoder(bytes.NewReader(normalized))
	decoder.DisallowUnknownFields()
	var cfg Config
	if err := decoder.Decode(&cfg); err != nil {
		return Config{}, fmt.Errorf("decode config: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return Config{}, fmt.Errorf("decode config: trailing data")
	}
	if err := cfg.normalizeAndValidate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func (c *Config) normalizeAndValidate() error {
	if c.SchemaVersion != SchemaVersion && c.SchemaVersion != SchemaVersionV2 {
		return fmt.Errorf("unsupported config schema version %q", c.SchemaVersion)
	}
	if err := ValidateText("release ID", c.Release.ID, false); err != nil {
		return err
	}
	if len(c.Release.Artifacts) == 0 {
		return fmt.Errorf("release must contain at least one artifact")
	}
	artifactNames := make(map[string]struct{}, len(c.Release.Artifacts))
	for i := range c.Release.Artifacts {
		artifact := &c.Release.Artifacts[i]
		if err := ValidateText("artifact name", artifact.Name, false); err != nil {
			return err
		}
		if err := ValidateText("artifact source", artifact.Source, false); err != nil {
			return err
		}
		if err := ValidateText("artifact media type", artifact.MediaType, true); err != nil {
			return err
		}
		if _, exists := artifactNames[artifact.Name]; exists {
			return fmt.Errorf("duplicate artifact name %q", artifact.Name)
		}
		artifactNames[artifact.Name] = struct{}{}
	}
	if len(c.Targets) == 0 {
		return fmt.Errorf("config must contain at least one target")
	}
	targetIDs := make(map[string]struct{}, len(c.Targets))
	for i := range c.Targets {
		target := &c.Targets[i]
		if err := ValidateText("target ID", target.ID, false); err != nil {
			return err
		}
		if _, exists := targetIDs[target.ID]; exists {
			return fmt.Errorf("duplicate target ID %q", target.ID)
		}
		targetIDs[target.ID] = struct{}{}
		if err := ValidateText("provider name", target.Provider.Name, false); err != nil {
			return err
		}
		if err := ValidateText("provider executable", target.Provider.Executable, true); err != nil {
			return err
		}
		configuration, err := canonicaljson.Normalize(target.Configuration)
		if err != nil {
			return fmt.Errorf("target %q configuration: %w", target.ID, err)
		}
		target.Configuration = configuration
	}
	return nil
}

func ValidateText(name, value string, optional bool) error {
	if value == "" {
		if optional {
			return nil
		}
		return fmt.Errorf("%s must not be empty", name)
	}
	if !utf8.ValidString(value) {
		return fmt.Errorf("%s must be valid UTF-8", name)
	}
	if strings.TrimSpace(value) != value {
		return fmt.Errorf("%s must not have surrounding whitespace", name)
	}
	for _, r := range value {
		if unicode.IsControl(r) {
			return fmt.Errorf("%s must not contain control characters", name)
		}
	}
	return nil
}
