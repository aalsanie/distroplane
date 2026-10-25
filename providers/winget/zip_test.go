package winget

import (
	"archive/zip"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aalsanie/distroplane/internal/protocol"
)

func zipArtifact(t *testing.T, members map[string][]byte) (string, protocol.Artifact) {
	t.Helper()
	fileName := filepath.Join(t.TempDir(), "portable.zip")
	file, err := os.Create(fileName)
	if err != nil {
		t.Fatal(err)
	}
	archive := zip.NewWriter(file)
	for name, content := range members {
		writer, err := archive.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := writer.Write(content); err != nil {
			t.Fatal(err)
		}
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(fileName)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(raw)
	return fileName, protocol.Artifact{
		Name: "installer", Digest: fmt.Sprintf("sha256:%x", sum), Size: int64(len(raw)),
	}
}

func zipInstaller(path string, nestedType string, entries ...nestedInstallerFile) installerConfig {
	return installerConfig{
		Architecture: "x64", Type: "zip", URL: "https://example.test/portable.zip",
		ArchivePath: path, NestedInstallerType: nestedType, NestedInstallerFiles: entries,
	}
}

func TestZIPPortableManifestContainsSchemaRequiredNestedFields(t *testing.T) {
	archive, artifact := zipArtifact(t, map[string][]byte{
		"bin/tool.exe": []byte("MZ-test-executable"),
		"bin/helper.exe": []byte("MZ-helper"),
		"README.md": []byte("portable distribution"),
	})
	cfg := configuration{
		ManifestRoot: "manifests", ManifestVersion: defaultManifestVersion,
		Package: packageConfig{
			Identifier: "Example.Tool", Version: "1.2.3", DefaultLocale: "en-US",
			Publisher: "Example", Name: "Tool", License: "MIT", ShortDescription: "Example executable",
		},
		Installer: zipInstaller(archive, "portable",
			nestedInstallerFile{RelativeFilePath: "bin/tool.exe", PortableCommandAlias: "example-tool"},
			nestedInstallerFile{RelativeFilePath: "bin/helper.exe", PortableCommandAlias: "example-helper"}),
	}
	if err := validateInstaller(cfg.Installer); err != nil {
		t.Fatal(err)
	}
	if err := validateZIPArchive(cfg.Installer, artifact); err != nil {
		t.Fatal(err)
	}
	files, err := renderManifestSet(cfg, artifact)
	if err != nil || len(files) != 3 {
		t.Fatalf("files=%v err=%v", files, err)
	}
	var installerManifest string
	for _, file := range files {
		if strings.HasSuffix(file.Path, ".installer.yaml") {
			installerManifest = file.Content
		}
	}
	for _, section := range []string{
		"InstallerType: zip", "NestedInstallerType: portable", "NestedInstallerFiles:",
		"RelativeFilePath: \"bin/tool.exe\"", "PortableCommandAlias: \"example-tool\"",
		"RelativeFilePath: \"bin/helper.exe\"", "PortableCommandAlias: \"example-helper\"",
		"InstallerSha256: "+strings.ToUpper(strings.TrimPrefix(artifact.Digest, "sha256:")),
	} {
		if !strings.Contains(installerManifest, section) {
			t.Fatalf("missing %q from installer manifest:\n%s", section, installerManifest)
		}
	}
	if strings.Contains(installerManifest, archive) || strings.Contains(installerManifest, "ArchivePath") {
		t.Fatal("local archive path leaked into manifest")
	}
	if manifestTreeHash(files) != manifestTreeHash([]manifestFile{files[2], files[0], files[1]}) {
		t.Fatal("ZIP manifest hash depends on file order")
	}
	encoded, _ := json.Marshal(cfg.Installer)
	var roundTripped installerConfig
	if err := json.Unmarshal(encoded, &roundTripped); err != nil || len(roundTripped.NestedInstallerFiles) != 2 {
		t.Fatalf("serialized config failed: %v", err)
	}
}

func TestZIPMSIAndEXESingleNestedLayouts(t *testing.T) {
	for _, kind := range []string{"exe", "msi"} {
		t.Run(kind, func(t *testing.T) {
			name := "setup." + kind
			archive, artifact := zipArtifact(t, map[string][]byte{name: []byte("installer fixture")})
			installer := zipInstaller(archive, kind, nestedInstallerFile{RelativeFilePath: name})
			if err := validateInstaller(installer); err != nil {
				t.Fatal(err)
			}
			if err := validateZIPArchive(installer, artifact); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestZIPFailClosedOnInvalidPathsAliasesAndLayouts(t *testing.T) {
	archive, _ := zipArtifact(t, map[string][]byte{"bin/tool.exe": []byte("MZ")})
	valid := zipInstaller(archive, "portable", nestedInstallerFile{RelativeFilePath: "bin/tool.exe", PortableCommandAlias: "tool"})
	for name, change := range map[string]func(*installerConfig){
		"missing archive": func(i *installerConfig) { i.ArchivePath = "" },
		"relative archive": func(i *installerConfig) { i.ArchivePath = "relative.zip" },
		"missing nested type": func(i *installerConfig) { i.NestedInstallerType = "" },
		"unsupported nested type": func(i *installerConfig) { i.NestedInstallerType = "msix" },
		"missing nested files": func(i *installerConfig) { i.NestedInstallerFiles = nil },
		"too many": func(i *installerConfig) { i.NestedInstallerFiles = make([]nestedInstallerFile, 17) },
		"multiple exe": func(i *installerConfig) {
			i.NestedInstallerType = "exe"
			i.NestedInstallerFiles = append(i.NestedInstallerFiles, nestedInstallerFile{RelativeFilePath: "other.exe"})
		},
		"alias nonportable": func(i *installerConfig) { i.NestedInstallerType = "exe" },
		"wrong extension": func(i *installerConfig) { i.NestedInstallerFiles[0].RelativeFilePath = "bin/tool.cmd" },
		"unsafe traversal": func(i *installerConfig) { i.NestedInstallerFiles[0].RelativeFilePath = "../tool.exe" },
		"unsafe embedded traversal": func(i *installerConfig) { i.NestedInstallerFiles[0].RelativeFilePath = "a/../tool.exe" },
		"absolute": func(i *installerConfig) { i.NestedInstallerFiles[0].RelativeFilePath = "/tool.exe" },
		"drive": func(i *installerConfig) { i.NestedInstallerFiles[0].RelativeFilePath = "C:/tool.exe" },
		"backslash": func(i *installerConfig) { i.NestedInstallerFiles[0].RelativeFilePath = `bin\tool.exe` },
		"ads": func(i *installerConfig) { i.NestedInstallerFiles[0].RelativeFilePath = "tool.exe:evil.exe" },
		"device": func(i *installerConfig) { i.NestedInstallerFiles[0].RelativeFilePath = "CON/tool.exe" },
		"invalid alias": func(i *installerConfig) { i.NestedInstallerFiles[0].PortableCommandAlias = "../bad" },
		"device alias": func(i *installerConfig) { i.NestedInstallerFiles[0].PortableCommandAlias = "NUL" },
		"long alias": func(i *installerConfig) { i.NestedInstallerFiles[0].PortableCommandAlias = strings.Repeat("a", 41) },
		"duplicate path": func(i *installerConfig) {
			i.NestedInstallerFiles = append(i.NestedInstallerFiles, nestedInstallerFile{RelativeFilePath: "BIN/TOOL.EXE", PortableCommandAlias: "two"})
		},
		"duplicate alias": func(i *installerConfig) {
			i.NestedInstallerFiles = append(i.NestedInstallerFiles, nestedInstallerFile{RelativeFilePath: "other.exe", PortableCommandAlias: "TOOL"})
		},
	} {
		t.Run(name, func(t *testing.T) {
			cfg := valid
			cfg.NestedInstallerFiles = append([]nestedInstallerFile(nil), valid.NestedInstallerFiles...)
			change(&cfg)
			if err := validateInstaller(cfg); err == nil {
				t.Fatal("invalid ZIP layout accepted")
			}
		})
	}
	nonZIP := valid
	nonZIP.Type = "msi"
	if err := validateInstaller(nonZIP); err == nil {
		t.Fatal("nested data silently ignored for non-ZIP installer")
	}
}

func TestZIPArchiveRejectsMissingChangedOrUnsafeMembers(t *testing.T) {
	for name, members := range map[string]map[string][]byte{
		"missing": {"other.exe": []byte("MZ")},
		"traversal": {"../tool.exe": []byte("MZ"), "bin/tool.exe": []byte("MZ")},
		"drive": {"C:/tool.exe": []byte("MZ"), "bin/tool.exe": []byte("MZ")},
		"duplicate": {"bin/tool.exe": []byte("MZ"), "BIN/TOOL.EXE": []byte("MZ")},
		"reserved device": {"AUX.exe": []byte("MZ"), "bin/tool.exe": []byte("MZ")},
	} {
		t.Run(name, func(t *testing.T) {
			archive, artifact := zipArtifact(t, members)
			installer := zipInstaller(archive, "portable", nestedInstallerFile{RelativeFilePath: "bin/tool.exe"})
			if err := validateZIPArchive(installer, artifact); err == nil {
				t.Fatal("unsafe ZIP archive accepted")
			}
		})
	}
	archive, artifact := zipArtifact(t, map[string][]byte{"bin/tool.exe": []byte("MZ")})
	installer := zipInstaller(archive, "portable", nestedInstallerFile{RelativeFilePath: "bin/tool.exe"})
	changed := artifact
	changed.Size++
	if err := validateZIPArchive(installer, changed); err == nil {
		t.Fatal("changed ZIP size accepted")
	}
	changed = artifact
	changed.Digest = "sha256:"+strings.Repeat("f", 64)
	if err := validateZIPArchive(installer, changed); err == nil {
		t.Fatal("changed ZIP digest accepted")
	}
	installer.ArchivePath = filepath.Join(t.TempDir(), "missing.zip")
	if err := validateZIPArchive(installer, artifact); err == nil {
		t.Fatal("missing ZIP artifact accepted")
	}
}
