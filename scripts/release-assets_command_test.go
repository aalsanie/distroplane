package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestReleaseAssetCommandReturnCodes(t *testing.T) {
	root := t.TempDir()
	environment := map[string]string{
		"OUT_DIR":    root,
		"VERSION":    "1.0.0",
		"COMMIT":     "0123456789ab",
		"BUILD_DATE": "2026-10-25T00:00:00Z",
		"GO_VERSION": "go1.27.3",
	}
	env := func(key string) string { return environment[key] }
	for _, args := range [][]string{nil, {"unknown"}, {"build", "extra"}} {
		if code, err := releaseAssetsCommand(args, env); code != 2 || err == nil {
			t.Fatalf("usage accepted %v: code=%d error=%v", args, code, err)
		}
	}
	if code, err := releaseAssetsCommand([]string{"verify"}, env); code != 1 || err == nil {
		t.Fatalf("missing assets accepted: code=%d error=%v", code, err)
	}
	if code, err := releaseAssetsCommand([]string{"build"}, env); code != 1 || err == nil {
		t.Fatalf("missing binaries accepted: code=%d error=%v", code, err)
	}
	seedExecutables(t, root)
	// Packaging fixtures are not compiled Go executables: the production command
	// must reject them even when filenames and declared versions are correct.
	if code, err := releaseAssetsCommand([]string{"build"}, env); code != 1 || err == nil {
		t.Fatalf("non-Go binaries accepted: code=%d error=%v", code, err)
	}
	if err := build(root, environment["VERSION"], environment["COMMIT"], environment["BUILD_DATE"], environment["GO_VERSION"]); err != nil {
		t.Fatal(err)
	}
	if code, err := releaseAssetsCommand([]string{"verify"}, env); code != 1 || err == nil {
		t.Fatalf("non-Go binaries passed production verification: code=%d error=%v", code, err)
	}
	// An obsolete compiler is rejected before any asset is assembled.
	environment["GO_VERSION"] = "go1.27.1"
	if code, err := releaseAssetsCommand([]string{"build"}, env); code != 1 || err == nil {
		t.Fatalf("outdated toolchain accepted: code=%d error=%v", code, err)
	}
	environment["GO_VERSION"] = "go1.27.3"
	// An absent OUT_DIR selects dist; it must never discover a different project.
	envNoRoot := func(key string) string {
		if key == "OUT_DIR" {
			return ""
		}
		return environment[key]
	}
	if _, err := os.Stat(filepath.Join(root, "RELEASE-METADATA.json")); err != nil {
		t.Fatal(err)
	}
	if code, err := releaseAssetsCommand([]string{"verify"}, envNoRoot); code != 1 || err == nil {
		t.Fatalf("empty OUT_DIR ignored: code=%d error=%v", code, err)
	}
}
