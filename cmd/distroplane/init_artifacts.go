package main

import (
	"bufio"
	"fmt"
	"io"
	"path/filepath"
	"strings"
)

// initArtifactPaths resolves explicit provider=PATH selections first. A single
// unscoped --artifact is a deliberate shared choice; missing paths are detected
// only when the project has one unambiguous recent build candidate.
func initArtifactPaths(destinations, raw []string, baseDir string, prompt bool, reader *bufio.Reader, stderr io.Writer) (map[string]string, error) {
	explicit := make(map[string]string)
	unscoped := ""
	selected := make(map[string]bool, len(destinations))
	for _, variant := range destinations {
		provider, _, _ := strings.Cut(variant, ":")
		selected[provider] = true
	}
	for _, value := range raw {
		key, path, scoped := strings.Cut(value, "=")
		if scoped && selected[key] {
			if path == "" || explicit[key] != "" {
				return nil, fmt.Errorf("duplicate or empty --artifact mapping for %s", key)
			}
			explicit[key] = path
			continue
		}
		if scoped && (key == "npm" || key == "sdkman" || key == "homebrew" || key == "winget") {
			return nil, fmt.Errorf("artifact mapping for destination %q was not selected", key)
		}
		if unscoped != "" {
			return nil, fmt.Errorf("multiple shared --artifact paths; use provider=PATH mappings")
		}
		unscoped = value
	}
	missing := false
	for provider := range selected {
		if explicit[provider] == "" && unscoped == "" {
			missing = true
		}
	}
	if missing {
		candidates, err := discoverArtifacts(baseDir)
		if err != nil {
			return nil, err
		}
		switch len(candidates) {
		case 0:
			if !prompt {
				return nil, fmt.Errorf("no recent build artifacts in target/, build/, or dist/; specify --artifact")
			}
		case 1:
			unscoped = candidates[0]
		default:
			if !prompt {
				return nil, fmt.Errorf("%d build artifacts found; choose with --artifact provider=PATH or --artifact PATH (candidates: %s)", len(candidates), strings.Join(candidates, ", "))
			}
			fmt.Fprintf(stderr, "Artifact candidates: %s\n", strings.Join(candidates, ", "))
		}
		if unscoped == "" && prompt {
			unscoped, err = promptInit(reader, stderr, "artifact path")
			if err != nil {
				return nil, err
			}
		}
	}
	out := make(map[string]string, len(selected))
	for provider := range selected {
		path := explicit[provider]
		if path == "" {
			path = unscoped
		}
		if path == "" {
			return nil, fmt.Errorf("missing --artifact %s=PATH", provider)
		}
		if !filepath.IsAbs(path) {
			out[provider] = filepath.Join(baseDir, path)
		} else {
			out[provider] = path
		}
	}
	return out, nil
}
