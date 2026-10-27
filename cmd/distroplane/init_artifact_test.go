package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInitRejectsMismatchedArtifactTypeBeforeWriting(t *testing.T) {
	cases := []struct {
		destination string
		artifact string
	}{
		{"npm", "tool.zip"},
		{"homebrew:jvm", "tool.tgz"},
		{"winget:msi", "tool.zip"},
		{"winget:zip", "tool.msi"},
	}
	for _, tc := range cases {
		t.Run(tc.destination, func(t *testing.T) {
			dir := t.TempDir()
			artifact := filepath.Join(dir, tc.artifact)
			if err := os.WriteFile(artifact, []byte("bytes"), 0600); err != nil {
				t.Fatal(err)
			}
			configPath := filepath.Join(dir, "distroplane.json")
			args := []string{"--destination", tc.destination, "--release", "v1", "--artifact", artifact, "--output", configPath, "--json"}
			var stdout, stderr bytes.Buffer
			if code := runInitContext(context.Background(), args, nil, false, &stdout, &stderr); code != exitInvalid {
				t.Fatalf("mismatched archive accepted: code=%d stderr=%s", code, stderr.String())
			}
			if !strings.Contains(stderr.String(), "invalid_artifact") {
				t.Fatalf("no actionable artifact error: %s", stderr.String())
			}
			if _, err := os.Stat(configPath); !os.IsNotExist(err) {
				t.Fatalf("mismatched artifact wrote config: %v", err)
			}
		})
	}
}

func TestInitArtifactKindPreservesOtherProviderFormats(t *testing.T) {
	for _, tc := range []struct{
		destination string
		source string
	}{
		{"npm", "release.tgz"},
		{"npm", "release.tar.gz"},
		{"sdkman", "release.zip"},
		{"sdkman", "release.tgz"},
		{"homebrew:formula", "release.tar.gz"},
		{"homebrew:cask", "release.zip"},
		{"homebrew:jvm", "release.zip"},
		{"winget:msi", "installer.msi"},
		{"winget:zip", "installer.zip"},
	} {
		if err := validateInitArtifactKind(tc.destination, tc.source); err != nil {
			t.Fatalf("%s with %s: %v", tc.destination, tc.source, err)
		}
	}
}
