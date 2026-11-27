package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
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
		if !prompt {
			switch len(candidates) {
			case 0:
				return nil, fmt.Errorf("no recent build artifacts in target/, build/, or dist/; specify --artifact")
			case 1:
				unscoped = candidates[0]
			default:
				return nil, fmt.Errorf("%d build artifacts found; choose with --artifact provider=PATH or --artifact PATH (candidates: %s)", len(candidates), strings.Join(candidates, ", "))
			}
		} else {
			for _, variant := range destinations {
				provider, _, _ := strings.Cut(variant, ":")
				if explicit[provider] != "" {
					continue
				}
				compatible := make([]string, 0, len(candidates))
				for _, candidate := range candidates {
					if validateInitArtifactKind(variant, candidate) == nil {
						compatible = append(compatible, candidate)
					}
				}
				if len(compatible) == 1 && len(candidates) == 1 {
					explicit[provider] = compatible[0]
					continue
				}
				if len(compatible) > 0 {
					fmt.Fprintf(stderr, "Artifact candidates for %s:\n", variant)
					for i, candidate := range compatible {
						info, err := os.Lstat(filepath.Join(baseDir, candidate))
						if err != nil || !info.Mode().IsRegular() {
							return nil, fmt.Errorf("artifact candidates changed; rerun init or specify --artifact")
						}
						fmt.Fprintf(stderr, "  %d. %s (%d bytes)\n", i+1, safeDisplay(candidate), info.Size())
					}
				}
				var choice string
				_, err := promptValidated(reader, stderr, "artifact path for "+variant+" (or candidate number)", func(value string) error {
					if index, err := strconv.Atoi(value); err == nil {
						if index < 1 || index > len(compatible) {
							return fmt.Errorf("choose a listed candidate number or enter a path")
						}
						value = compatible[index-1]
					}
					if err := validateInitArtifactKind(variant, value); err != nil {
						return err
					}
					path := value
					if !filepath.IsAbs(path) {
						path = filepath.Join(baseDir, path)
					}
					info, err := os.Lstat(path)
					if err != nil || !info.Mode().IsRegular() || info.Size() == 0 || !initSafeDescendant(baseDir, path) {
						return fmt.Errorf("choose an existing nonempty regular artifact without symbolic links")
					}
					choice = value
					return nil
				})
				if err != nil {
					return nil, err
				}
				explicit[provider] = choice
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
