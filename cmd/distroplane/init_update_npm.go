package main

import (
	"archive/tar"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path"
	"strings"

	"github.com/aalsanie/distroplane/internal/canonicaljson"
	"github.com/aalsanie/distroplane/internal/config"
)

const maxUpdateManifestBytes = 1 << 20
const maxUpdateTarBytes = 128 << 20

func inspectUpdateNPM(filename string) (string, string, error) {
	file, err := os.Open(filename)
	if err != nil {
		return "", "", err
	}
	defer file.Close()
	gz, err := gzip.NewReader(file)
	if err != nil {
		return "", "", fmt.Errorf("npm artifact is not a gzip tarball")
	}
	defer gz.Close()
	reader := tar.NewReader(io.LimitReader(gz, maxUpdateTarBytes))
	for {
		header, err := reader.Next()
		if err != nil {
			return "", "", fmt.Errorf("npm package.json not found within the bounded archive scan")
		}
		name := path.Clean(strings.TrimPrefix(header.Name, "./"))
		if name != "package/package.json" && name != "package.json" {
			continue
		}
		if header.Typeflag != tar.TypeReg || header.Size > maxUpdateManifestBytes {
			return "", "", fmt.Errorf("npm package.json must be a bounded regular file")
		}
		raw, err := io.ReadAll(io.LimitReader(reader, maxUpdateManifestBytes+1))
		if err != nil || len(raw) > maxUpdateManifestBytes {
			return "", "", fmt.Errorf("npm package.json cannot be read")
		}
		raw, err = canonicaljson.Normalize(raw)
		if err != nil {
			return "", "", fmt.Errorf("npm package.json is invalid")
		}
		var manifest struct {
			Name    string `json:"name"`
			Version string `json:"version"`
		}
		if err := json.Unmarshal(raw, &manifest); err != nil ||
			config.ValidateText("package.json name", manifest.Name, false) != nil ||
			config.ValidateText("package.json version", manifest.Version, false) != nil {
			return "", "", fmt.Errorf("npm package.json requires a name and version")
		}
		return manifest.Name, manifest.Version, nil
	}
}
