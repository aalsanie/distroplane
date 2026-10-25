package winget

import (
	"archive/zip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/aalsanie/distroplane/internal/protocol"
)

// WinGet archive installers need explicit nested installer metadata. Restrict
// support to layouts that we can verify against the actual release artifact.
// Never guess the nested installer or silently submit invalid manifests.
func validateNestedInstaller(installer installerConfig) error {
	if installer.Type != "zip" {
		if installer.ArchivePath != "" || installer.NestedInstallerType != "" || len(installer.NestedInstallerFiles) != 0 {
			return fmt.Errorf("nested installer metadata is only valid for zip installers")
		}
		return nil
	}
	if installer.ArchivePath == "" || !filepath.IsAbs(installer.ArchivePath) {
		return fmt.Errorf("zip installer requires an absolute local archivePath")
	}
	switch installer.NestedInstallerType {
	case "portable", "exe", "msi":
	default:
		return fmt.Errorf("unsupported zip nestedInstallerType %q (supported: portable, exe, msi)", installer.NestedInstallerType)
	}
	if len(installer.NestedInstallerFiles) == 0 || len(installer.NestedInstallerFiles) > 16 {
		return fmt.Errorf("zip requires between one and 16 nestedInstallerFiles")
	}
	if installer.NestedInstallerType != "portable" && len(installer.NestedInstallerFiles) != 1 {
		return fmt.Errorf("only portable zip layouts may specify multiple nested files")
	}
	seenPaths := make(map[string]struct{}, len(installer.NestedInstallerFiles))
	seenAliases := make(map[string]struct{}, len(installer.NestedInstallerFiles))
	for _, nested := range installer.NestedInstallerFiles {
		if err := validateArchiveMemberPath(nested.RelativeFilePath); err != nil {
			return fmt.Errorf("invalid nested installer path: %w", err)
		}
		ext := strings.ToLower(path.Ext(nested.RelativeFilePath))
		want := "." + installer.NestedInstallerType
		if installer.NestedInstallerType == "portable" {
			want = ".exe"
		}
		if ext != want {
			return fmt.Errorf("nested %s installer must use %s file", installer.NestedInstallerType, want)
		}
		key := strings.ToLower(nested.RelativeFilePath)
		if _, exists := seenPaths[key]; exists {
			return fmt.Errorf("duplicate nested installer path %q", nested.RelativeFilePath)
		}
		seenPaths[key] = struct{}{}
		if nested.PortableCommandAlias != "" {
			if installer.NestedInstallerType != "portable" {
				return fmt.Errorf("PortableCommandAlias requires nestedInstallerType portable")
			}
			if !validPortableAlias(nested.PortableCommandAlias) {
				return fmt.Errorf("invalid portable command alias")
			}
			key := strings.ToLower(nested.PortableCommandAlias)
			if _, exists := seenAliases[key]; exists {
				return fmt.Errorf("duplicate portable command alias %q", nested.PortableCommandAlias)
			}
			seenAliases[key] = struct{}{}
		}
	}
	return nil
}

// Every path component must be unambiguous on Windows and safe to extract.
// In particular reject absolute/drive/UNC paths, traversal, ADS, reserved
// device names, and case-insensitive collisions in the archive.
func validateArchiveMemberPath(value string) error {
	if value == "" || len(value) > 512 || strings.HasPrefix(value, "/") ||
		strings.Contains(value, "\\") || strings.ContainsAny(value, ":*?\"<>|\x00\r\n") {
		return fmt.Errorf("unsafe Windows archive path")
	}
	if path.Clean(value) != value {
		return fmt.Errorf("archive path must be normalized without traversal")
	}
	for _, component := range strings.Split(value, "/") {
		if component == "" || component == "." || component == ".." ||
			strings.HasSuffix(component, " ") || strings.HasSuffix(component, ".") ||
			strings.ContainsAny(component, "\t") || reservedWindowsName(component) {
			return fmt.Errorf("unsafe Windows archive path component")
		}
	}
	return nil
}

func reservedWindowsName(value string) bool {
	base := strings.ToUpper(strings.SplitN(value, ".", 2)[0])
	switch base {
	case "CON", "PRN", "AUX", "NUL":
		return true
	}
	if len(base) == 4 && (strings.HasPrefix(base, "COM") || strings.HasPrefix(base, "LPT")) {
		return base[3] >= '1' && base[3] <= '9'
	}
	return false
}

func validPortableAlias(alias string) bool {
	if alias == "" || len(alias) > 40 || reservedWindowsName(alias) {
		return false
	}
	for i, r := range alias {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') {
			continue
		}
		if i > 0 && ((r >= '0' && r <= '9') || r == '-' || r == '_' || r == '.') {
			continue
		}
		return false
	}
	return !strings.HasSuffix(alias, ".")
}

func validateZIPArchive(installer installerConfig, artifact protocol.Artifact) error {
	if installer.Type != "zip" {
		return nil
	}
	file, err := os.Open(installer.ArchivePath)
	if err != nil {
		return fmt.Errorf("open zip installer archive: %w", err)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return fmt.Errorf("zip installer archive must be a regular file")
	}
	if info.Size() != artifact.Size {
		return fmt.Errorf("zip installer archive size differs from release artifact")
	}
	digest := sha256.New()
	if _, err := io.Copy(digest, file); err != nil {
		return fmt.Errorf("hash zip installer archive: %w", err)
	}
	if "sha256:"+hex.EncodeToString(digest.Sum(nil)) != artifact.Digest {
		return fmt.Errorf("zip installer archive digest differs from release artifact")
	}
	archive, err := zip.NewReader(file, info.Size())
	if err != nil {
		return fmt.Errorf("invalid zip installer archive: %w", err)
	}
	if len(archive.File) == 0 || len(archive.File) > 8192 {
		return fmt.Errorf("unsupported zip archive entry count")
	}
	archiveFiles := make(map[string]struct{}, len(archive.File))
	for _, entry := range archive.File {
		name := strings.TrimSuffix(entry.Name, "/")
		if err := validateArchiveMemberPath(name); err != nil {
			return fmt.Errorf("unsafe zip archive member %q: %w", entry.Name, err)
		}
		if entry.Flags&1 != 0 || (!entry.FileInfo().IsDir() && !entry.Mode().IsRegular()) {
			return fmt.Errorf("unsupported encrypted or special zip member %q", entry.Name)
		}
		key := strings.ToLower(name)
		if _, duplicate := archiveFiles[key]; duplicate {
			return fmt.Errorf("case-insensitive duplicate zip member %q", entry.Name)
		}
		archiveFiles[key] = struct{}{}
	}
	for _, selected := range installer.NestedInstallerFiles {
		found := false
		for _, entry := range archive.File {
			if entry.Name == selected.RelativeFilePath && !entry.FileInfo().IsDir() {
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("nested installer %q not found in zip archive", selected.RelativeFilePath)
		}
	}
	return nil
}
