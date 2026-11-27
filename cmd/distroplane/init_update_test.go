package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/aalsanie/distroplane/internal/frontend/official"
	"github.com/aalsanie/distroplane/internal/journal"
	"github.com/aalsanie/distroplane/internal/runstate"
)

func writeUpdateNPMFixture(t *testing.T, path, name, version string) {
	t.Helper()
	data, err := json.Marshal(map[string]string{"name": name, "version": version})
	if err != nil {
		t.Fatal(err)
	}
	writeUpdateTar(t, path, "package/package.json", tar.TypeReg, data)
}

func writeUpdateTar(t *testing.T, path, member string, kind byte, data []byte) {
	t.Helper()
	var buffer bytes.Buffer
	gz := gzip.NewWriter(&buffer)
	archive := tar.NewWriter(gz)
	header := &tar.Header{Name: member, Mode: 0600, Typeflag: kind, Size: int64(len(data))}
	if err := archive.WriteHeader(header); err != nil {
		t.Fatal(err)
	}
	if _, err := archive.Write(data); err != nil {
		t.Fatal(err)
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, buffer.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
}

func updateTestRead(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func updateTestWrite(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
}

func updateTestCall(t *testing.T, want int, input string, interactive bool, args ...string) (string, string) {
	t.Helper()
	var out, diagnostic bytes.Buffer
	code := runInitContext(context.Background(), append([]string{"--update"}, args...), strings.NewReader(input), interactive, &out, &diagnostic)
	if code != want {
		t.Fatalf("update %v: code=%d want=%d stdout=%s stderr=%s", args, code, want, out.String(), diagnostic.String())
	}
	return out.String(), diagnostic.String()
}

func exerciseSecondRelease(t *testing.T, configPath, originalArtifact, variant string, original []byte) {
	t.Helper()
	provider, _, _ := strings.Cut(variant, ":")
	previousPath := originalArtifact
	previous := original
	for _, version := range []string{"1.2.4", "1.2.5"} {
		release := "build-" + version
		next := filepath.Join(filepath.Dir(originalArtifact), "next-"+version+filepath.Ext(originalArtifact))
		if provider == "npm" {
			writeUpdateNPMFixture(t, next, "sample", version)
		} else {
			updateTestWrite(t, next, []byte(version))
		}
		oldBytes := updateTestRead(t, previousPath)
		args := []string{"--output", configPath, "--release", release, "--artifact", next, "--non-interactive", "--json"}
		url := "https://example.invalid/" + filepath.Base(next)
		if provider != "npm" {
			args = append(args, optionFlags(provider+".version="+version, provider+".url="+url)...)
		}
		out, _ := updateTestCall(t, exitOK, "", false, args...)
		var result struct {
			BackupPath string         `json:"backupPath"`
			Release    string         `json:"release"`
			Changes    []updateChange `json:"changes"`
		}
		if err := json.Unmarshal([]byte(out), &result); err != nil {
			t.Fatal(err)
		}
		if result.Release != release || len(result.Changes) != 1 || result.Changes[0].SHA256 == "" {
			t.Fatalf("missing update summary: %s", out)
		}
		if !bytes.Equal(updateTestRead(t, result.BackupPath), previous) || !bytes.Equal(updateTestRead(t, previousPath), oldBytes) {
			t.Fatal("update changed historical config or artifact bytes")
		}
		loaded, err := official.Load(configPath)
		if err != nil || loaded.Config.Release.ID != release {
			t.Fatalf("updated config: %+v %v", loaded.Config, err)
		}
		after := updateTestRead(t, configPath)
		beforeDoc, oldDest, err := decodeUpdate(previous)
		if err != nil {
			t.Fatal(err)
		}
		afterDoc, newDest, err := decodeUpdate(after)
		if err != nil {
			t.Fatal(err)
		}
		old := oldDest[0]
		new := newDest[0]
		if old.id != new.id || old.variant != new.variant || old.artifact != new.artifact ||
			!bytes.Equal(beforeDoc.Sources, afterDoc.Sources) {
			t.Fatal("destination identity or credential sources changed")
		}
		source := updateString(afterDoc.Release.Artifacts[new.artifact], "source")
		if filepath.Join(filepath.Dir(configPath), filepath.FromSlash(source)) != next {
			t.Fatalf("wrong artifact path %s", source)
		}
		switch provider {
		case "npm":
			if result.Changes[0].NewVersion != "sample@"+version || updateString(new.options, "packagePath") != source {
				t.Fatal("npm package metadata or path not reflected")
			}
			new.options["packagePath"] = old.options["packagePath"]
		default:
			if updateString(new.version, "version") != version || updateString(new.download, "url") != url {
				t.Fatal("destination version or URL not updated")
			}
			new.version["version"] = old.version["version"]
			new.download["url"] = old.download["url"]
			if variant == "winget:zip" {
				if updateString(new.download, "archivePath") != source {
					t.Fatal("ZIP archive path not updated")
				}
				new.download["archivePath"] = old.download["archivePath"]
			}
		}
		if !reflect.DeepEqual(old.entry, new.entry) {
			t.Fatalf("unrelated destination metadata changed: old=%v new=%v", old.entry, new.entry)
		}
		previous, previousPath = after, next
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(configPath), ".distroplane")); !os.IsNotExist(err) {
		t.Fatal("configuration update created execution state")
	}
}

func updateSDKMANFixture(t *testing.T) (string, string, string) {
	t.Helper()
	dir := t.TempDir()
	old := filepath.Join(dir, "old.zip")
	next := filepath.Join(dir, "new.zip")
	updateTestWrite(t, old, []byte("first"))
	updateTestWrite(t, next, []byte("second"))
	path := filepath.Join(dir, "distroplane.json")
	args := append(initArgs(dir, "sdkman"), "--artifact", old, "--credential", "sdkman-vendor-email=UPDATE_TEST_EMAIL", "--credential", "sdkman-vendor-password=UPDATE_TEST_PASSWORD")
	args = append(args, optionFlags("sdkman.candidate=sample", "sdkman.version=1.2.3", "sdkman.url=https://example.invalid/old.zip", "sdkman.platform=UNIVERSAL")...)
	invokeInit(t, exitOK, "", false, args...)
	return path, old, next
}

func TestInitUpdateInteractiveReviewAndRefusals(t *testing.T) {
	path, old, next := updateSDKMANFixture(t)
	before := updateTestRead(t, path)
	base := []string{"--output", path}
	for _, tc := range []struct {
		name  string
		input string
		want  int
	}{
		{"deny", "v2\n" + next + "\n2.0\nhttps://example.invalid/new.zip\nno\n", exitInvalid},
		{"EOF", "v2\n" + next + "\n2.0\nhttps://example.invalid/new.zip\n", exitInvalid},
		{"incomplete", "v2\n", exitInvalid},
	} {
		t.Run(tc.name, func(t *testing.T) {
			updateTestCall(t, tc.want, tc.input, true, base...)
			if !bytes.Equal(before, updateTestRead(t, path)) {
				t.Fatal("unapproved update wrote configuration")
			}
		})
	}
	input := "v1.2.3\nv2\n999\n" + old + "\n" + next + "\n1.2.3\n2.0\nhttp://invalid.example\nhttps://example.invalid/new.zip\nupdate v2\n"
	out, diagnostic := updateTestCall(t, exitOK, input, true, base...)
	if !strings.Contains(out, "backup ") || !strings.Contains(diagnostic, "Destinations unchanged") ||
		!strings.Contains(diagnostic, "Invalid answer") || !strings.Contains(diagnostic, "1.2.3 -> 2.0") {
		t.Fatalf("review or field correction absent: %s %s", out, diagnostic)
	}
	loaded, err := official.Load(path)
	if err != nil || loaded.Config.Release.ID != "v2" || loaded.Config.CredentialSources["sdkman-vendor-email"] != "UPDATE_TEST_EMAIL" {
		t.Fatalf("interactive update lost configuration: %+v %v", loaded.Config, err)
	}
	if string(updateTestRead(t, old)) != "first" {
		t.Fatal("old artifact changed")
	}
}

func TestInitUpdateScriptedInvalidInputsLeaveConfigUntouched(t *testing.T) {
	path, old, next := updateSDKMANFixture(t)
	original := updateTestRead(t, path)
	base := []string{"--output", path, "--json"}
	for _, tc := range []struct {
		name string
		args []string
		want int
	}{
		{"force", []string{"--force"}, exitUsage},
		{"destination", []string{"--destination", "sdkman"}, exitUsage},
		{"credential", []string{"--credential", "x=ENV"}, exitUsage},
		{"missing release", []string{"--artifact", next}, exitInvalid},
		{"same release", []string{"--release", "v1.2.3", "--artifact", next}, exitInvalid},
		{"missing artifact", []string{"--release", "v2"}, exitInvalid},
		{"old artifact", []string{"--release", "v2", "--artifact", old}, exitInvalid},
		{"absent artifact", []string{"--release", "v2", "--artifact", next + ".missing"}, exitInvalid},
		{"wrong scope", []string{"--release", "v2", "--artifact", "npm=" + next}, exitInvalid},
		{"duplicate scope", []string{"--release", "v2", "--artifact", "sdkman=" + next, "--artifact", "sdkman=" + next}, exitInvalid},
		{"empty scope", []string{"--release", "v2", "--artifact", "sdkman="}, exitInvalid},
		{"config as artifact", []string{"--release", "v2", "--artifact", path}, exitInvalid},
		{"multiple shared", []string{"--release", "v2", "--artifact", next, "--artifact", old}, exitInvalid},
		{"missing version", []string{"--release", "v2", "--artifact", next}, exitInvalid},
		{"missing URL", []string{"--release", "v2", "--artifact", next, "--set", "sdkman.version=2.0"}, exitInvalid},
		{"same version", []string{"--release", "v2", "--artifact", next, "--set", "sdkman.version=1.2.3"}, exitInvalid},
		{"unsafe URL", []string{"--release", "v2", "--artifact", next, "--set", "sdkman.version=2.0", "--set", "sdkman.url=https://user:SECRET@example.invalid/new.zip"}, exitInvalid},
		{"malformed field", []string{"--release", "v2", "--set", "invalid"}, exitUsage},
		{"empty field", []string{"--release", "v2", "--set", "sdkman.version="}, exitUsage},
		{"duplicate field", []string{"--release", "v2", "--set", "sdkman.version=2", "--set", "sdkman.version=3"}, exitUsage},
		{"destination change", []string{"--release", "v2", "--artifact", next, "--set", "sdkman.version=2", "--set", "sdkman.url=https://example.invalid/new.zip", "--set", "sdkman.candidate=other"}, exitUsage},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, diagnostic := updateTestCall(t, tc.want, "", false, append(append([]string{}, base...), tc.args...)...)
			if strings.Contains(diagnostic, "SECRET") || !bytes.Equal(original, updateTestRead(t, path)) {
				t.Fatal("invalid update leaked input or wrote configuration")
			}
		})
	}
	updateTestCall(t, exitUsage, "", false, "--output", "", "--json")
	updateTestCall(t, exitInvalid, "", false, "--output", path+".missing", "--json")
	updateTestCall(t, exitInvalid, "", false, "--output", filepath.Dir(path), "--json")
	oversized := filepath.Join(filepath.Dir(path), "oversized.json")
	updateTestWrite(t, oversized, make([]byte, 5<<20))
	updateTestCall(t, exitInvalid, "", false, "--output", oversized, "--json")
}

func TestInitUpdateUnsupportedConfigRefusesWithoutMutation(t *testing.T) {
	path, _, _ := updateSDKMANFixture(t)
	original := updateTestRead(t, path)
	for _, tc := range []struct {
		name   string
		change func(map[string]any, map[string]any, map[string]any)
	}{
		{"array", func(root, destinations, entry map[string]any) {
			entry["id"] = "one"
			destinations["sdkman"] = []any{entry}
		}},
		{"multiple array", func(root, destinations, entry map[string]any) {
			other := map[string]any{"id": "two", "artifact": entry["artifact"], "options": entry["options"]}
			entry["id"] = "one"
			destinations["sdkman"] = []any{entry, other}
		}},
		{"executable", func(root, destinations, entry map[string]any) { entry["executable"] = "custom-provider" }},
		{"checksums", func(root, destinations, entry map[string]any) {
			updateObject(entry, "options")["checksums"] = map[string]string{"SHA-256": strings.Repeat("a", 64)}
		}},
		{"private download query", func(root, destinations, entry map[string]any) {
			updateObject(entry, "options")["url"] = "https://example.invalid/old.zip?token=do-not-display"
		}},
		{"unreferenced artifact", func(root, destinations, entry map[string]any) {
			updateObject(updateObject(root, "release"), "artifacts")["extra"] = map[string]string{"source": "extra.zip"}
		}},
		{"custom provider", func(root, destinations, entry map[string]any) {
			destinations["custom"] = entry
			delete(destinations, "sdkman")
		}},
		{"case variant", func(root, destinations, entry map[string]any) {
			root["SchemaVersion"] = root["schemaVersion"]
			delete(root, "schemaVersion")
		}},
		{"artifact case", func(root, destinations, entry map[string]any) {
			artifact := updateObject(updateObject(updateObject(root, "release"), "artifacts"), "release")
			artifact["Source"] = artifact["source"]
			delete(artifact, "source")
		}},
		{"entry case", func(root, destinations, entry map[string]any) {
			entry["Artifact"] = entry["artifact"]
			delete(entry, "artifact")
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var root map[string]any
			if err := json.Unmarshal(original, &root); err != nil {
				t.Fatal(err)
			}
			destinations := updateObject(root, "destinations")
			tc.change(root, destinations, updateObject(destinations, "sdkman"))
			raw, err := json.Marshal(root)
			if err != nil {
				t.Fatal(err)
			}
			updateTestWrite(t, path, raw)
			_, diagnostic := updateTestCall(t, exitInvalid, "", false, "--output", path, "--json")
			if strings.Contains(diagnostic, "do-not-display") {
				t.Fatal("unsupported config exposed a URL credential")
			}
			if !bytes.Equal(raw, updateTestRead(t, path)) {
				t.Fatal("unsupported config changed")
			}
		})
	}
	for _, raw := range []string{
		"{",
		`{"schemaVersion":"1","release":{"id":"v1","artifacts":[{"name":"a","source":"old.zip"}]},"targets":[{"id":"custom","provider":{"name":"custom"},"configuration":{}}]}`,
	} {
		updateTestWrite(t, path, []byte(raw))
		updateTestCall(t, exitInvalid, "", false, "--output", path, "--json")
		if string(updateTestRead(t, path)) != raw {
			t.Fatal("unsupported config changed")
		}
	}
}

func TestInitUpdatePreservesMultiDestinationMetadataAndSharedArtifact(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "distroplane.json")
	old := filepath.Join(dir, "old.tgz")
	next := filepath.Join(dir, "new.tgz")
	writeUpdateNPMFixture(t, old, "@scope/sample", "1.0.0")
	writeUpdateNPMFixture(t, next, "@scope/sample", "2.0.0")
	raw := `{"schemaVersion":"2","release":{"id":"first","artifacts":{"payload":{"source":"old.tgz","mediaType":"application/gzip"}}},"credentialSources":{"npm-auth":"TEAM_NPM_TOKEN","tap-auth":"TEAM_TAP_TOKEN"},"destinations":{"npm":{"id":"public-registry","artifact":"payload","options":{"packagePath":"old.tgz","registry":"https://registry.npmjs.org/","tag":"next","access":"public","authentication":{"mode":"token","credential":"npm-auth"}}},"homebrew":{"id":"custom-tap","artifact":"payload","options":{"repository":"https://github.com/example/tap.git","branch":"stable","path":"Formula/sample.rb","mode":"pull-request","updateBranch":"release-update","manifest":{"type":"formula","name":"Sample","description":"CLI","homepage":"https://example.invalid/","url":"https://example.invalid/old.tgz","version":"1.0.0","binary":"sample","license":"Apache-2.0"},"authentication":{"credential":"tap-auth"},"commit":{"authorName":"Release Bot","authorEmail":"bot@example.invalid"},"pullRequest":{"repository":"example/tap","headOwner":"example"}}}}}`
	updateTestWrite(t, path, []byte(raw))
	base := []string{"--output", path, "--release", "second", "--json"}
	fields := optionFlags("homebrew.version=2.0.0", "homebrew.url=https://example.invalid/new.tgz")
	other := filepath.Join(dir, "other.tgz")
	writeUpdateNPMFixture(t, other, "@scope/sample", "2.0.0")
	updateTestCall(t, exitInvalid, "", false, append(append(append([]string{}, base...), "--artifact", "npm="+next, "--artifact", "homebrew="+other), fields...)...)
	if string(updateTestRead(t, path)) != raw {
		t.Fatal("shared artifact association was split")
	}
	out, _ := updateTestCall(t, exitOK, "", false, append(append(append([]string{}, base...), "--artifact", "npm="+next), fields...)...)
	if !strings.Contains(out, "public-registry") || !strings.Contains(out, "custom-tap") {
		t.Fatal("destination identity absent from review")
	}
	before, oldDest, err := decodeUpdate([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	after, newDest, err := decodeUpdate(updateTestRead(t, path))
	if err != nil {
		t.Fatal(err)
	}
	if len(newDest) != 2 || len(after.Release.Artifacts) != 1 || !bytes.Equal(before.Sources, after.Sources) ||
		updateString(after.Release.Artifacts["payload"], "mediaType") != "application/gzip" {
		t.Fatal("shared metadata changed")
	}
	for i, old := range oldDest {
		next := newDest[i]
		if old.variant == "npm" {
			next.options["packagePath"] = old.options["packagePath"]
		} else {
			next.version["version"] = old.version["version"]
			next.download["url"] = old.download["url"]
		}
		if !reflect.DeepEqual(old.entry, next.entry) {
			t.Fatal("unrelated destination metadata changed")
		}
	}
}

func TestInitUpdateNPMIdentityAndInvalidTarballs(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "distroplane.json")
	old := filepath.Join(dir, "old.tgz")
	next := filepath.Join(dir, "next.tgz")
	writeUpdateNPMFixture(t, old, "sample", "1.0.0")
	invokeInit(t, exitOK, "", false, append(initArgs(dir, "npm"), "--artifact", old)...)
	original := updateTestRead(t, path)
	args := []string{"--output", path, "--release", "v2", "--artifact", next, "--json"}
	for _, tc := range []struct{ name, version string }{{"other", "2.0.0"}, {"sample", "1.0.0"}} {
		writeUpdateNPMFixture(t, next, tc.name, tc.version)
		updateTestCall(t, exitInvalid, "", false, args...)
	}
	for _, tc := range []struct {
		member string
		data   []byte
	}{
		{"package/README", []byte("nothing")},
		{"package/package.json", []byte("{")},
		{"package/package.json", []byte(`{"name":"sample"}`)},
		{"package/package.json", []byte(`{"name":"sample","name":"other","version":"2.0.0"}`)},
		{"package/package.json", make([]byte, maxUpdateManifestBytes+1)},
	} {
		writeUpdateTar(t, next, tc.member, tar.TypeReg, tc.data)
		updateTestCall(t, exitInvalid, "", false, args...)
	}
	writeUpdateTar(t, next, "package/package.json", tar.TypeDir, nil)
	updateTestCall(t, exitInvalid, "", false, args...)
	updateTestWrite(t, next, []byte("not gzip"))
	updateTestCall(t, exitInvalid, "", false, args...)
	if _, _, err := inspectUpdateNPM(next + ".missing"); err == nil {
		t.Fatal("missing npm archive accepted")
	}
	writeUpdateNPMFixture(t, next, "sample", "2.0.0")
	if err := os.Remove(old); err != nil {
		t.Fatal(err)
	}
	updateTestCall(t, exitInvalid, "", false, args...)
	if !bytes.Equal(original, updateTestRead(t, path)) {
		t.Fatal("invalid npm update wrote config")
	}
}

func TestInitUpdateAtomicBackupAndConcurrentChange(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	original := []byte("original")
	data := []byte("next")
	updateTestWrite(t, path, original)
	sum := sha256.Sum256(original)
	backup := path + ".bak." + hex.EncodeToString(sum[:])
	updateTestWrite(t, backup, []byte("existing backup"))
	if _, err := writeUpdatedConfig(context.Background(), path, original, data); err == nil {
		t.Fatal("overwrote existing backup")
	}
	if string(updateTestRead(t, backup)) != "existing backup" || !bytes.Equal(updateTestRead(t, path), original) {
		t.Fatal("backup conflict mutated files")
	}
	if err := os.Remove(backup); err != nil {
		t.Fatal(err)
	}
	if _, err := writeUpdatedConfig(context.Background(), path, []byte("stale"), data); err == nil {
		t.Fatal("stale configuration overwritten")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := writeUpdatedConfig(ctx, path, original, data); err == nil {
		t.Fatal("cancelled update wrote config")
	}
	if !bytes.Equal(updateTestRead(t, path), original) {
		t.Fatal("cancelled update changed configuration")
	}
	results := make(chan error, 2)
	for _, candidate := range [][]byte{data, []byte("alternative")} {
		go func(candidate []byte) {
			_, err := writeUpdatedConfig(context.Background(), path, original, candidate)
			results <- err
		}(candidate)
	}
	successes := 0
	for range 2 {
		if err := <-results; err == nil {
			successes++
		}
	}
	if successes != 1 || !bytes.Equal(updateTestRead(t, backup), original) {
		t.Fatal("concurrent updates did not preserve one original backup")
	}
	if matches, err := filepath.Glob(filepath.Join(dir, ".distroplane-*")); err != nil || len(matches) != 0 {
		t.Fatalf("temporary files retained: %v %v", matches, err)
	}
	if _, err := writeUpdatedConfig(context.Background(), filepath.Join(dir, "absent", "config"), original, data); err == nil {
		t.Fatal("missing parent accepted")
	}
}

func TestInitUpdateCancellationWhileAwaitingConfirmation(t *testing.T) {
	path, _, next := updateSDKMANFixture(t)
	original := updateTestRead(t, path)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	blocked := initBlockedInput{started: make(chan struct{}), release: make(chan struct{})}
	defer close(blocked.release)
	args := []string{"--update", "--output", path, "--release", "v2", "--artifact", next, "--set", "sdkman.version=2", "--set", "sdkman.url=https://example.invalid/new.zip"}
	result := make(chan int, 1)
	go func() {
		result <- runInitContext(ctx, args, blocked, true, io.Discard, io.Discard)
	}()
	select {
	case <-blocked.started:
	case <-time.After(5 * time.Second):
		t.Fatal("update did not reach confirmation")
	}
	cancel()
	select {
	case code := <-result:
		if code != exitOperational {
			t.Fatalf("cancellation returned %d", code)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("update remained blocked")
	}
	if !bytes.Equal(original, updateTestRead(t, path)) {
		t.Fatal("cancelled confirmation wrote configuration")
	}
}

func TestInitUpdateRetainsOriginalPendingRun(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "distroplane.json")
	old := filepath.Join(dir, "first.tgz")
	next := filepath.Join(dir, "next.tgz")
	writeUpdateNPMFixture(t, old, "sample", "1.0.0")
	writeUpdateNPMFixture(t, next, "sample", "2.0.0")
	copyExecutable(t, buildFakeProvider(t), filepath.Join(dir, executableName("distroplane-provider-npm")))
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("UPDATE_PENDING_TOKEN", "")
	invokeInit(t, exitOK, "", false, append(initArgs(dir, "npm"), "--artifact", old, "--credential", "npm-publish=UPDATE_PENDING_TOKEN")...)
	raw, _ := recoveryCall(t, exitPending, "publish", "--config", path, "--yes", "--json")
	var first publishOutput
	if err := json.Unmarshal(raw, &first); err != nil || first.Run == nil {
		t.Fatalf("missing initial pending run: %s %v", raw, err)
	}
	root := filepath.Join(dir, ".distroplane")
	snapshot := map[string][]byte{}
	if err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		data, err := os.ReadFile(path)
		if err == nil {
			snapshot[path] = data
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
	updateTestCall(t, exitInvalid, "", false, "--output", path, "--release", "v1.2.3", "--artifact", next, "--json")
	updateTestCall(t, exitOK, "", false, "--output", path, "--release", "second", "--artifact", next, "--json")
	updateTestCall(t, exitInvalid, "", false, "--output", path, "--release", "v1.2.3", "--artifact", old, "--json")
	updateTestCall(t, exitInvalid, "", false, "--output", path, "--release", "third", "--artifact", old, "--json")
	for path, data := range snapshot {
		if !bytes.Equal(data, updateTestRead(t, path)) {
			t.Fatalf("update modified historical state %s", path)
		}
	}
	records, err := (runstate.Store{Root: root}).Inventory()
	if err != nil || len(records) != 1 || records[0].RunID != first.Run.RunID {
		t.Fatalf("run catalog changed: %+v %v", records, err)
	}
	status, _ := recoveryCall(t, exitPending, "status", "--config", path, "--run", first.Run.RunID, "--json")
	var state stateOutput
	if err := json.Unmarshal(status, &state); err != nil || state.PlanID != first.Plan.PlanID {
		t.Fatal("original status selection changed")
	}
	recoveryCall(t, exitPending, "resume", "--config", path, "--run", first.Run.RunID, "--reconcile-only", "--json")
	t.Setenv("UPDATE_PENDING_TOKEN", "fake-credential")
	resumed, _ := recoveryCall(t, exitOK, "resume", "--config", path, "--run", first.Run.RunID, "--json")
	if err := json.Unmarshal(resumed, &state); err != nil || !state.Completed || state.RunID != first.Run.RunID || state.PlanID != first.Plan.PlanID {
		t.Fatalf("resume changed original identity: %s %v", resumed, err)
	}
	events, err := readJournal(first.JournalPath)
	if err != nil {
		t.Fatal(err)
	}
	dispatched := 0
	for _, event := range events.Events {
		if event.Type == journal.EventSideEffectDispatched {
			dispatched++
		}
	}
	if dispatched != 1 {
		t.Fatalf("original run dispatched %d times", dispatched)
	}
}
