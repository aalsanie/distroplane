package main

import (
	"debug/buildinfo"
	"errors"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"
	"testing"
)

func compiledFixture(executable, osName, arch string) *debug.BuildInfo {
	return &debug.BuildInfo{
		GoVersion: releaseGoToolchain,
		Path:      "github.com/aalsanie/distroplane/cmd/" + executable,
		Main:      debug.Module{Path: "github.com/aalsanie/distroplane"},
		Settings: []debug.BuildSetting{
			{Key: "GOOS", Value: osName},
			{Key: "GOARCH", Value: arch},
			{Key: "CGO_ENABLED", Value: "0"},
			{Key: "-trimpath", Value: "true"},
		},
	}
}

func TestExecutableBuildInfoRequiresPinnedToolchainAndTarget(t *testing.T) {
	const executable, osName, arch = "distroplane-provider-npm", "linux", "amd64"
	if err := validateExecutableBuildInfo(compiledFixture(executable, osName, arch), executable, osName, arch); err != nil {
		t.Fatalf("valid Go build info rejected: %v", err)
	}
	cases := []struct {
		name   string
		mutate func(*debug.BuildInfo)
	}{
		{"old compiler", func(b *debug.BuildInfo) { b.GoVersion = "go1.27.1" }},
		{"newer unreviewed compiler", func(b *debug.BuildInfo) { b.GoVersion = "go1.27.4" }},
		{"foreign command", func(b *debug.BuildInfo) { b.Path = "github.com/example/other/cmd/distroplane-provider-npm" }},
		{"foreign module", func(b *debug.BuildInfo) { b.Main.Path = "github.com/example/other" }},
		{"incorrect operating system", func(b *debug.BuildInfo) { b.Settings[0].Value = "windows" }},
		{"incorrect architecture", func(b *debug.BuildInfo) { b.Settings[1].Value = "arm64" }},
		{"cgo enabled", func(b *debug.BuildInfo) { b.Settings[2].Value = "1" }},
		{"not reproducibly trimmed", func(b *debug.BuildInfo) { b.Settings[3].Value = "false" }},
		{"missing settings", func(b *debug.BuildInfo) { b.Settings = nil }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			info := compiledFixture(executable, osName, arch)
			tc.mutate(info)
			if err := validateExecutableBuildInfo(info, executable, osName, arch); err == nil {
				t.Fatal("invalid compiled executable accepted")
			}
		})
	}
	if err := validateExecutableBuildInfo(nil, executable, osName, arch); err == nil {
		t.Fatal("missing build info accepted")
	}
}

func TestVerifyAllReleaseExecutableToolchains(t *testing.T) {
	const version = "1.0.0"
	root := t.TempDir()
	fixtures := make(map[string]*debug.BuildInfo)
	for _, target := range platforms {
		for _, executable := range executables {
			name := assetName(executable, version, target.os, target.arch)
			fixtures[filepath.Join(root, name)] = compiledFixture(executable, target.os, target.arch)
		}
	}
	seen := 0
	read := func(path string) (*debug.BuildInfo, error) {
		seen++
		info, ok := fixtures[path]
		if !ok {
			return nil, errors.New("missing build info")
		}
		return info, nil
	}
	if err := verifyExecutableToolchains(root, version, releaseGoToolchain, read); err != nil {
		t.Fatal(err)
	}
	if seen != len(platforms)*len(executables) {
		t.Fatalf("inspected %d executables; expected %d", seen, len(platforms)*len(executables))
	}
	if err := verifyExecutableToolchains(root, version, "go1.27.1", read); err == nil || !strings.Contains(err.Error(), "required") {
		t.Fatalf("outdated declared toolchain accepted: %v", err)
	}
	first := assetName(executables[0], version, platforms[0].os, platforms[0].arch)
	key := filepath.Join(root, first)
	original := fixtures[key]
	delete(fixtures, key)
	if err := verifyExecutableToolchains(root, version, releaseGoToolchain, read); err == nil || !strings.Contains(err.Error(), first) {
		t.Fatalf("missing executable information accepted: %v", err)
	}
	fixtures[key] = original
	fixtures[key] = compiledFixture(executables[0], "windows", platforms[0].arch)
	if err := verifyExecutableToolchains(root, version, releaseGoToolchain, read); err == nil || !strings.Contains(err.Error(), first) {
		t.Fatalf("cross-target executable accepted: %v", err)
	}
}

func TestBuildInfoReaderRejectsNonGoExecutable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "not-a-go-executable")
	if err := os.WriteFile(path, []byte("not a compiled Go executable"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := buildinfo.ReadFile(path); err == nil {
		t.Fatal("malformed Go build info accepted")
	}
}
