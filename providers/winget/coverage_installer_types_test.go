package winget

import (
	"path/filepath"
	"testing"
)

func TestInstallerTypeArchitectureAndNestedZIPContracts(t *testing.T) {
	for _, architecture := range []string{"x86", "x64", "arm", "arm64", "neutral"} {
		for _, kind := range []string{"exe", "msi", "msix", "inno", "nullsoft", "wix", "burn", "portable"} {
			t.Run(architecture+"/"+kind, func(t *testing.T) {
				cfg := installerConfig{Architecture: architecture, Type: kind, URL: "https://example.test/release.exe"}
				if err := validateInstaller(cfg); err != nil {
					t.Fatalf("supported installer rejected: %v", err)
				}
			})
		}
	}
	for _, nested := range []struct{ kind, path string }{
		{"portable", "archive/tool.exe"}, {"exe", "archive/install.exe"}, {"msi", "archive/install.msi"},
	} {
		t.Run("zip/"+nested.kind, func(t *testing.T) {
			cfg := installerConfig{
				Architecture: "x64", Type: "zip", URL: "https://example.test/release.zip",
				ArchivePath: filepath.Join(t.TempDir(), "release.zip"), NestedInstallerType: nested.kind,
				NestedInstallerFiles: []nestedInstallerFile{{RelativeFilePath: nested.path}},
			}
			if err := validateInstaller(cfg); err != nil {
				t.Fatalf("supported ZIP layout rejected: %v", err)
			}
		})
	}
	for _, architecture := range []string{"riscv64", "", "X64"} {
		if err := validateInstaller(installerConfig{Architecture: architecture, Type: "exe", URL: "https://example.test/x"}); err == nil {
			t.Errorf("unrecognized architecture accepted: %q", architecture)
		}
	}
	for _, kind := range []string{"script", "unknown", ""} {
		if err := validateInstaller(installerConfig{Architecture: "x64", Type: kind, URL: "https://example.test/x"}); err == nil {
			t.Errorf("unrecognized installer type accepted: %q", kind)
		}
	}
}
