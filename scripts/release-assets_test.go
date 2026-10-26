package main

import (
	"archive/zip"
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func seedExecutables(t *testing.T, root string) {
	t.Helper()
	for _, p := range platforms {
		for _, e := range executables {
			name := assetName(e, "1.0.0", p.os, p.arch)
			if err := os.WriteFile(filepath.Join(root, name), []byte("signed fixture: "+name), 0755); err != nil {
				t.Fatal(err)
			}
		}
	}
}
func prepareRelease(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	seedExecutables(t, root)
	if err := build(root, "1.0.0", "0123456789ab", "2026-10-25T00:00:00Z", "go1.27.1"); err != nil {
		t.Fatal(err)
	}
	return root
}
func TestReleaseArchiveReproducibleAndComplete(t *testing.T) {
	root := prepareRelease(t)
	before := map[string]string{}
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 38 {
		t.Fatalf("expected 30 executables, 6 archives, two manifests, found %d", len(entries))
	}
	for _, e := range entries {
		b, err := os.ReadFile(filepath.Join(root, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		before[e.Name()] = hash(b)
	}
	if err := build(root, "1.0.0", "0123456789ab", "2026-10-25T00:00:00Z", "go1.27.1"); err != nil {
		t.Fatal(err)
	}
	for name, want := range before {
		b, err := os.ReadFile(filepath.Join(root, name))
		if err != nil || hash(b) != want {
			t.Fatalf("%s did not reproduce: %v", name, err)
		}
	}
	if err := verify(root); err != nil {
		t.Fatal(err)
	}
	archive, err := os.ReadFile(filepath.Join(root, archiveName("1.0.0", "windows", "amd64")))
	if err != nil {
		t.Fatal(err)
	}
	z, err := zip.NewReader(bytes.NewReader(archive), int64(len(archive)))
	if err != nil {
		t.Fatal(err)
	}
	if len(z.File) != 6 {
		t.Fatalf("expected 5 executables + package metadata: %d", len(z.File))
	}
	for _, f := range z.File {
		if strings.HasPrefix(f.Name, "../") || f.Mode().Perm() == 0 {
			t.Fatalf("unsafe packaged mode/path: %s", f.Name)
		}
	}
}
func TestReleaseVerificationRejectsTampering(t *testing.T) {
	for _, tc := range []struct {
		name, filename string
		mutate         func([]byte) []byte
	}{
		{"altered binary", "distroplane_1.0.0_linux_amd64", func(b []byte) []byte { return append(b, 'x') }},
		{"altered archive", "distroplane_1.0.0_windows_amd64.zip", func(b []byte) []byte { return append(b, 'x') }},
		{"altered checksum", "SHA256SUMS", func(b []byte) []byte {
			if b[0] == '0' {
				b[0] = '1'
			} else {
				b[0] = '0'
			}
			return b
		}},
		{"altered metadata", "RELEASE-METADATA.json", func(b []byte) []byte { return append(b, 'x') }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := prepareRelease(t)
			path := filepath.Join(root, tc.filename)
			b, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, tc.mutate(b), 0600); err != nil {
				t.Fatal(err)
			}
			if err := verify(root); err == nil {
				t.Fatal("tampering passed validation")
			}
		})
	}
}
func TestReleaseVerificationRejectsMissingAndUnexpectedAssets(t *testing.T) {
	root := prepareRelease(t)
	if err := os.WriteFile(filepath.Join(root, "distroplane-provider-fake_1.0.0_linux_amd64"), []byte("fake"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := verify(root); err == nil {
		t.Fatal("unexpected fake provider accepted")
	}
	if err := os.Remove(filepath.Join(root, "distroplane-provider-fake_1.0.0_linux_amd64")); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(root, "distroplane-provider-winget_1.0.0_darwin_arm64")); err != nil {
		t.Fatal(err)
	}
	if err := verify(root); err == nil {
		t.Fatal("missing binary accepted")
	}
}
func TestReleaseVerificationRejectsSymlinkAndBadIdentity(t *testing.T) {
	root := t.TempDir()
	seedExecutables(t, root)
	target := filepath.Join(root, "distroplane_1.0.0_linux_amd64")
	if err := os.Remove(target); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "distroplane_1.0.0_darwin_amd64"), target); err != nil {
		t.Skipf("host does not support symlinks: %v", err)
	}
	if err := build(root, "1.0.0", "0123456789ab", "2026-10-25T00:00:00Z", "go1.27.1"); err == nil {
		t.Fatal("symlink executable accepted")
	}
	if err := validIdentity("../1.0.0", "0123456789ab", "2026-10-25T00:00:00Z", "go1.27.1"); err == nil {
		t.Fatal("unsafe version accepted")
	}
}
