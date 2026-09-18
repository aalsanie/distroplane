package npm

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path"
	"strings"
)

const maxManifestBytes = 1 << 20

type packageArchive struct {
	data      []byte
	manifest  map[string]json.RawMessage
	name      string
	version   string
	sha256    string
	shasum    string
	integrity string
}

type attachment struct {
	ContentType string `json:"content_type"`
	Data        string `json:"data"`
	Length      int    `json:"length"`
}

type publishDocument struct {
	ID          string                     `json:"_id"`
	Name        string                     `json:"name"`
	Access      string                     `json:"access,omitempty"`
	DistTags    map[string]string          `json:"dist-tags"`
	Versions    map[string]json.RawMessage `json:"versions"`
	Attachments map[string]attachment      `json:"_attachments"`
}

type manifestDist struct {
	Integrity string `json:"integrity"`
	Shasum    string `json:"shasum"`
	Tarball   string `json:"tarball"`
}

func loadPackage(filename, expectedDigest string, expectedSize int64) (packageArchive, error) {
	file, err := os.Open(filename)
	if err != nil {
		return packageArchive{}, fmt.Errorf("open npm package: %w", err)
	}
	defer file.Close()
	if expectedSize < 0 {
		return packageArchive{}, fmt.Errorf("expected package size is invalid")
	}
	data, err := io.ReadAll(io.LimitReader(file, expectedSize+1))
	if err != nil {
		return packageArchive{}, fmt.Errorf("read npm package: %w", err)
	}
	if int64(len(data)) != expectedSize {
		return packageArchive{}, fmt.Errorf("npm package size changed: got %d, want %d", len(data), expectedSize)
	}
	sha256Sum := sha256.Sum256(data)
	sha256Value := "sha256:" + hex.EncodeToString(sha256Sum[:])
	if sha256Value != expectedDigest {
		return packageArchive{}, fmt.Errorf("npm package digest changed")
	}
	manifest, name, version, err := packageManifest(data)
	if err != nil {
		return packageArchive{}, err
	}
	sha1Sum := sha1.Sum(data)
	sha512Sum := sha512.Sum512(data)
	return packageArchive{
		data: data, manifest: manifest, name: name, version: version, sha256: sha256Value,
		shasum: hex.EncodeToString(sha1Sum[:]), integrity: "sha512-" + base64.StdEncoding.EncodeToString(sha512Sum[:]),
	}, nil
}

func packageManifest(data []byte) (map[string]json.RawMessage, string, string, error) {
	gz, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, "", "", fmt.Errorf("npm artifact is not a gzip tarball: %w", err)
	}
	defer gz.Close()
	tarReader := tar.NewReader(gz)
	for {
		header, err := tarReader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, "", "", fmt.Errorf("read npm package tarball: %w", err)
		}
		clean := path.Clean(strings.TrimPrefix(header.Name, "./"))
		if clean != "package/package.json" && clean != "package.json" {
			continue
		}
		if header.Size < 0 || header.Size > maxManifestBytes {
			return nil, "", "", fmt.Errorf("package.json exceeds %d bytes", maxManifestBytes)
		}
		raw, err := io.ReadAll(io.LimitReader(tarReader, maxManifestBytes+1))
		if err != nil {
			return nil, "", "", fmt.Errorf("read package.json: %w", err)
		}
		if len(raw) > maxManifestBytes {
			return nil, "", "", fmt.Errorf("package.json exceeds %d bytes", maxManifestBytes)
		}
		var manifest map[string]json.RawMessage
		if err := json.Unmarshal(raw, &manifest); err != nil {
			return nil, "", "", fmt.Errorf("invalid package.json: %w", err)
		}
		var name, version string
		if err := json.Unmarshal(manifest["name"], &name); err != nil || !validPackageName(name) {
			return nil, "", "", fmt.Errorf("package.json contains an invalid name")
		}
		if err := json.Unmarshal(manifest["version"], &version); err != nil || !semverPattern.MatchString(version) {
			return nil, "", "", fmt.Errorf("package.json contains an invalid semantic version")
		}
		return manifest, name, version, nil
	}
	return nil, "", "", fmt.Errorf("npm package does not contain package/package.json")
}

func validPackageName(value string) bool {
	if value == "" || len(value) > 214 || strings.ToLower(value) != value || strings.ContainsAny(value, " \\") {
		return false
	}
	if strings.HasPrefix(value, "@") {
		parts := strings.Split(value, "/")
		return len(parts) == 2 && len(parts[0]) > 1 && parts[1] != ""
	}
	return !strings.Contains(value, "/") && value != "." && value != ".."
}

func (p packageArchive) publishBody(payload operationPayload) []byte {
	manifest := make(map[string]json.RawMessage, len(p.manifest)+1)
	for key, value := range p.manifest {
		manifest[key] = append(json.RawMessage(nil), value...)
	}
	dist, _ := json.Marshal(manifestDist{Integrity: p.integrity, Shasum: p.shasum, Tarball: tarballURL(payload.Registry, p.name, p.version)})
	manifest["dist"] = dist
	encodedManifest, _ := json.Marshal(manifest)
	filename := packageBaseName(p.name) + "-" + p.version + ".tgz"
	document := publishDocument{
		ID: p.name, Name: p.name, Access: payload.Access,
		DistTags: map[string]string{payload.Tag: p.version},
		Versions: map[string]json.RawMessage{p.version: encodedManifest},
		Attachments: map[string]attachment{filename: {
			ContentType: "application/octet-stream", Data: base64.StdEncoding.EncodeToString(p.data), Length: len(p.data),
		}},
	}
	encoded, _ := json.Marshal(document)
	return encoded
}

func packageBaseName(name string) string {
	if index := strings.LastIndexByte(name, '/'); index >= 0 {
		return name[index+1:]
	}
	return name
}
