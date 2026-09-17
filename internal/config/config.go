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
	SchemaVersion string   `json:"schemaVersion"`
	Release       Release  `json:"release"`
	Targets       []Target `json:"targets"`
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

func Load(path string) (Loaded, error) {
	if strings.TrimSpace(path) == "" {
		return Loaded{}, fmt.Errorf("config path must not be empty")
	}
	file, err := os.Open(path)
	if err != nil {
		return Loaded{}, err
	}
	defer file.Close()
	cfg, err := Decode(file)
	if err != nil {
		return Loaded{}, err
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return Loaded{}, err
	}
	return Loaded{Config: cfg, BaseDir: filepath.Dir(absolute)}, nil
}

func Decode(reader io.Reader) (Config, error) {
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
	if c.SchemaVersion != SchemaVersion {
		return fmt.Errorf("unsupported config schema version %q", c.SchemaVersion)
	}
	if err := validateText("release ID", c.Release.ID, false); err != nil {
		return err
	}
	if len(c.Release.Artifacts) == 0 {
		return fmt.Errorf("release must contain at least one artifact")
	}
	artifactNames := make(map[string]struct{}, len(c.Release.Artifacts))
	for i := range c.Release.Artifacts {
		artifact := &c.Release.Artifacts[i]
		if err := validateText("artifact name", artifact.Name, false); err != nil {
			return err
		}
		if err := validateText("artifact source", artifact.Source, false); err != nil {
			return err
		}
		if err := validateText("artifact media type", artifact.MediaType, true); err != nil {
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
		if err := validateText("target ID", target.ID, false); err != nil {
			return err
		}
		if _, exists := targetIDs[target.ID]; exists {
			return fmt.Errorf("duplicate target ID %q", target.ID)
		}
		targetIDs[target.ID] = struct{}{}
		if err := validateText("provider name", target.Provider.Name, false); err != nil {
			return err
		}
		if err := validateText("provider executable", target.Provider.Executable, true); err != nil {
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

func validateText(name, value string, optional bool) error {
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
