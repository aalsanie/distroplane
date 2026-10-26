package main

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestReleaseArchiveNativeExecutables(t *testing.T) {
	root := os.Getenv("DISTROPLANE_RELEASE_SMOKE_DIR")
	if root == "" {
		t.Skip("set DISTROPLANE_RELEASE_SMOKE_DIR to test built release")
	}
	version := os.Getenv("VERSION")
	if version == "" {
		t.Fatal("VERSION is required to verify release identity")
	}
	osName, arch := runtime.GOOS, runtime.GOARCH
	archive := archiveName(version, osName, arch)
	b, err := os.ReadFile(filepath.Join(root, archive))
	if err != nil {
		t.Fatal(err)
	}
	dest := t.TempDir()
	extract := func(name string, mode os.FileMode, reader io.Reader, size int64) {
		t.Helper()
		if size < 0 || size > maxExecutableSize {
			t.Fatalf("invalid entry size: %s", name)
		}
		known := name == "PACKAGE-METADATA.json"
		for _, exe := range executables {
			known = known || innerName(exe, osName) == name
		}
		if !known {
			t.Fatalf("unexpected path: %s", name)
		}
		path := filepath.Join(dest, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(io.LimitReader(reader, size+1))
		if err != nil || int64(len(data)) != size {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(path, data, mode); err != nil {
			t.Fatal(err)
		}
	}
	if osName == "windows" {
		z, err := zip.NewReader(strings.NewReader(string(b)), int64(len(b)))
		if err != nil {
			t.Fatal(err)
		}
		for _, f := range z.File {
			r, err := f.Open()
			if err != nil {
				t.Fatal(err)
			}
			extract(f.Name, f.Mode().Perm(), r, int64(f.UncompressedSize64))
			if err := r.Close(); err != nil {
				t.Fatal(err)
			}
		}
	} else {
		g, err := gzip.NewReader(strings.NewReader(string(b)))
		if err != nil {
			t.Fatal(err)
		}
		defer g.Close()
		r := tar.NewReader(g)
		for {
			h, err := r.Next()
			if err == io.EOF {
				break
			}
			if err != nil {
				t.Fatal(err)
			}
			extract(h.Name, os.FileMode(h.Mode), r, h.Size)
		}
	}
	metadata, err := os.ReadFile(filepath.Join(dest, "PACKAGE-METADATA.json"))
	if err != nil {
		t.Fatal(err)
	}
	var pkg packageRecord
	if err := strictJSON(metadata, &pkg); err != nil {
		t.Fatal(err)
	}
	if pkg.Version != version || pkg.OS != osName || pkg.Arch != arch || len(pkg.Files) != len(executables) {
		t.Fatalf("archive identity does not match: %+v", pkg)
	}
	ext := ""
	if osName == "windows" {
		ext = ".exe"
	}
	cli := exec.Command(filepath.Join(dest, "bin", "distroplane"+ext), "version", "--json")
	out, err := cli.CombinedOutput()
	if err != nil {
		t.Fatalf("CLI did not execute: %v: %s", err, out)
	}
	var identity struct {
		Version string `json:"version"`
		Commit  string `json:"commit"`
	}
	if err := json.Unmarshal(out, &identity); err != nil {
		t.Fatal(err)
	}
	if identity.Version != version || identity.Commit != pkg.Commit {
		t.Fatalf("CLI identity mismatch: %+v", identity)
	}
	for _, name := range executables[1:] {
		p := exec.Command(filepath.Join(dest, "libexec", name+ext))
		p.Stdin = strings.NewReader(`{"protocolVersion":"1","requestId":"archive-smoke","operation":"describe","payload":{}}`)
		output, err := p.CombinedOutput()
		if err != nil {
			t.Fatalf("%s did not start: %v: %s", name, err, output)
		}
		var reply struct {
			Status          string `json:"status"`
			ProtocolVersion string `json:"protocolVersion"`
			Payload         struct {
				Provider struct {
					Name    string `json:"name"`
					Version string `json:"version"`
				} `json:"provider"`
			} `json:"payload"`
		}
		if err := json.Unmarshal(output, &reply); err != nil {
			t.Fatalf("%s invalid response: %v: %s", name, err, output)
		}
		if reply.Status != "ok" || reply.ProtocolVersion != "1" ||
			reply.Payload.Provider.Name != strings.TrimPrefix(name, "distroplane-provider-") ||
			reply.Payload.Provider.Version != version {
			t.Fatalf("%s incorrect identity: %s", name, fmt.Sprintf("%+v", reply))
		}
	}

	// Verify extracted CLI discovery takes precedence over a shadowed PATH.
	artifact := filepath.Join(dest, "candidate.zip")
	if err := os.WriteFile(artifact, []byte("artifact bytes"), 0600); err != nil {
		t.Fatal(err)
	}
	cfg := map[string]any{
		"schemaVersion": "1",
		"release": map[string]any{
			"id": "archive-provider-discovery",
			"artifacts": []map[string]any{{"name": "app", "source": artifact}},
		},
		"targets": []map[string]any{{
			"id": "sdkman",
			"provider": map[string]any{"name": "sdkman"},
			"configuration": map[string]any{
				"artifact": "app", "candidate": "example", "version": "1.2.3",
				"url": "https://example.invalid/candidate.zip",
				"authentication": map[string]any{
					"mode": "legacy", "consumerKeyCredential": "sdkman-key",
					"consumerTokenCredential": "sdkman-token",
				},
			},
		}},
	}
	config, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(dest, "test-config.json")
	if err := os.WriteFile(configPath, config, 0600); err != nil {
		t.Fatal(err)
	}
	shadow := t.TempDir()
	if err := os.WriteFile(filepath.Join(shadow, "distroplane-provider-sdkman"+ext), []byte("PATH shadow"), 0755); err != nil {
		t.Fatal(err)
	}
	plan := exec.Command(filepath.Join(dest, "bin", "distroplane"+ext),
		"plan", "--config", configPath, "--state-dir", filepath.Join(dest, "state"), "--json")
	plan.Env = append(os.Environ(), "PATH="+shadow+string(os.PathListSeparator)+os.Getenv("PATH"))
	planned, err := plan.CombinedOutput()
	if err != nil {
		t.Fatalf("bundled SDKMAN provider was not discovered: %v: %s", err, planned)
	}
	var output struct {
		PlanID string `json:"planId"`
		Operations int `json:"operations"`
	}
	if err := json.Unmarshal(planned, &output); err != nil || output.PlanID == "" || output.Operations != 1 {
		t.Fatalf("bundled provider plan invalid: %+v %v: %s", output, err, planned)
	}
}
