package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestV2WinGetZIPArchivePathUsesArtifactBaseDir(t *testing.T) {
	root := t.TempDir()
	filename := filepath.Join(root, "distroplane.json")
	raw := `{"schemaVersion":"2","release":{"id":"release","artifacts":{"release":{"source":"dist/release.zip"}}},"destinations":{"winget":{"artifact":"release","options":{"repository":"https://github.com/example/winget-pkgs.git","authentication":{"credential":"winget-publish"},"package":{"identifier":"Example.Tool","version":"1.2.3","publisher":"Example","name":"Tool","license":"MIT","shortDescription":"Example"},"installer":{"architecture":"x64","type":"zip","url":"https://example.invalid/release.zip","archivePath":"dist/release.zip","nestedInstallerType":"portable","nestedInstallerFiles":[{"relativeFilePath":"bin/tool.exe","portableCommandAlias":"tool"}]},"pullRequest":{"repository":"microsoft/winget-pkgs"}}}}}`
	if err := os.WriteFile(filename, []byte(raw), 0600); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(filename)
	if err != nil { t.Fatal(err) }
	var options map[string]json.RawMessage
	if err := json.Unmarshal(loaded.Config.Targets[0].Configuration, &options); err != nil { t.Fatal(err) }
	var installer map[string]any
	if err := json.Unmarshal(options["installer"], &installer); err != nil { t.Fatal(err) }
	if installer["archivePath"] != filepath.Join(root, "dist", "release.zip") { t.Fatalf("ZIP source not absolute: %+v", installer) }
	var original map[string]any
	if err := json.Unmarshal([]byte(raw), &original); err != nil { t.Fatal(err) }
	got := original["destinations"].(map[string]any)["winget"].(map[string]any)["options"].(map[string]any)["installer"].(map[string]any)["archivePath"]
	if got != "dist/release.zip" { t.Fatalf("local input path changed: %s", got) }
}

func TestV2WinGetNonZIPDoesNotInventArchivePath(t *testing.T) {
	base := `{"schemaVersion":"2","release":{"id":"release","artifacts":{"release":{"source":"dist/release.msi"}}},"destinations":{"winget":{"artifact":"release","options":{"repository":"https://github.com/example/winget-pkgs.git","authentication":{"credential":"winget-publish"},"package":{"identifier":"Example.Tool","version":"1.2.3","publisher":"Example","name":"Tool","license":"MIT","shortDescription":"Example"},"installer":{"architecture":"x64","type":"msi","url":"https://example.invalid/release.msi"},"pullRequest":{"repository":"microsoft/winget-pkgs"}}}}}`
	path := filepath.Join(t.TempDir(), "distroplane.json")
	if err := os.WriteFile(path, []byte(base), 0600); err != nil { t.Fatal(err) }
	loaded, err := Load(path)
	if err != nil { t.Fatal(err) }
	var options map[string]any
	if err := json.Unmarshal(loaded.Config.Targets[0].Configuration, &options); err != nil { t.Fatal(err) }
	installer := options["installer"].(map[string]any)
	if _, ok := installer["archivePath"]; ok { t.Fatalf("invented archive: %v", installer) }
	if strings.TrimSpace(installer["url"].(string)) == "" { t.Fatal("lost remote URL") }
}
