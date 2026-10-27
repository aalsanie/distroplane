package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aalsanie/distroplane/internal/config"
)

func TestInitProviderScopedArtifactsCreateDistinctSources(t *testing.T) {
	dir, npm := createInitProject(t)
	winget := filepath.Join(dir, "dist", "installer.msi")
	if err := os.MkdirAll(filepath.Dir(winget), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(winget, []byte("msi-fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	args := append(initArgs(dir, "npm"),
		"--destination", "winget:msi",
		"--artifact", "npm="+npm,
		"--artifact", "winget="+winget)
	args = append(args, optionFlags(
		"winget.repository=https://github.com/example/winget-pkgs.git",
		"winget.identifier=Example.Sample",
		"winget.version=1.2.3",
		"winget.publisher=Example",
		"winget.name=Sample",
		"winget.license=MIT",
		"winget.description=Sample CLI",
		"winget.url=https://example.invalid/release.msi",
		"winget.architecture=x64",
		"winget.pr-repository=microsoft/winget-pkgs",
	)...)
	output, _ := invokeInit(t, exitOK, "", false, args...)
	if output.Artifacts["npm"].Path != npm || output.Artifacts["winget"].Path != winget {
		t.Fatalf("provider scoped artifact lost: %+v", output.Artifacts)
	}
	loaded, err := config.Load(output.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.Config.Release.Artifacts) != 2 || len(loaded.Config.Targets) != 2 {
		t.Fatalf("artifact references not distinct: %+v", loaded.Config)
	}
	refs := map[string]string{}
	for _, target := range loaded.Config.Targets {
		var options map[string]any
		if err := json.Unmarshal(target.Configuration, &options); err != nil {
			t.Fatal(err)
		}
		refs[target.Provider.Name] = options["artifact"].(string)
		if target.Provider.Name == "npm" {
			if options["packagePath"] != npm {
				t.Fatalf("npm source mismatch: %v", options)
			}
		}
	}
	if refs["npm"] != "npm" || refs["winget"] != "winget" {
		t.Fatalf("destination artifact aliasing: %+v", refs)
	}
	for _, source := range loaded.Config.Release.Artifacts {
		if source.Name != refs["npm"] && source.Name != refs["winget"] {
			t.Fatalf("unreferenced artifact: %+v", source)
		}
	}
}

func TestInitProviderScopedArtifactMustMatchDestination(t *testing.T) {
	dir, npm := createInitProject(t)
	for _, tc := range []struct {
		flags   []string
		message string
	}{
		{[]string{"--artifact", "winget=" + npm}, "was not selected"},
		{[]string{"--artifact", "npm=" + npm, "--artifact", "npm=" + npm}, "duplicate"},
		{[]string{"--artifact", npm, "--artifact", npm}, "multiple shared"},
		{[]string{"--artifact", "npm="}, "empty"},
	} {
		var out, errOut bytes.Buffer
		args := append(initArgs(dir, "npm"), tc.flags...)
		code := runInitContext(context.Background(), args, nil, false, &out, &errOut)
		if code != exitInvalid || !strings.Contains(errOut.String(), tc.message) {
			t.Fatalf("expected %q code=%d stderr=%s", tc.message, code, errOut.String())
		}
		if _, err := os.Stat(filepath.Join(dir, "distroplane.json")); !os.IsNotExist(err) {
			t.Fatalf("invalid artifact mapping wrote config: %v", err)
		}
	}
}

func TestInitSharedArtifactWithScopedOverride(t *testing.T) {
	dir, npm := createInitProject(t)
	second := filepath.Join(dir, "dist", "installer.msi")
	if err := os.MkdirAll(filepath.Dir(second), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(second, []byte("installer"), 0600); err != nil {
		t.Fatal(err)
	}
	selected, err := initArtifactPaths([]string{"npm", "winget:msi"},
		[]string{npm, "winget=" + second}, dir, false, nil, nil)
	if err != nil || selected["npm"] != npm || selected["winget"] != second {
		t.Fatalf("explicit scoped override lost: %+v %v", selected, err)
	}
}
