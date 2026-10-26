package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestReleaseAssetCommandReturnCodes(t *testing.T) {
	root := t.TempDir()
	environment := map[string]string{
		"OUT_DIR": root,
		"VERSION": "1.0.0",
		"COMMIT": "0123456789ab",
		"BUILD_DATE": "2026-10-25T00:00:00Z",
		"GO_VERSION": "go1.27.1",
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
	if code, err := releaseAssetsCommand([]string{"build"}, env); code != 0 || err != nil {
		t.Fatalf("valid build rejected: code=%d error=%v", code, err)
	}
	if code, err := releaseAssetsCommand([]string{"verify"}, env); code != 0 || err != nil {
		t.Fatalf("valid verification rejected: code=%d error=%v", code, err)
	}
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
