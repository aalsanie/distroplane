package main

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReleaseRejectsInvalidBuildInputs(t *testing.T) {
	for _, tc := range []struct {
		version, commit, date, goVersion string
	}{
		{"../version", "0123456789ab", "2026-10-25T00:00:00Z", "go1.27.1"},
		{"1.0.0", "wrong-commit", "2026-10-25T00:00:00Z", "go1.27.1"},
		{"1.0.0", "0123456789ab", "not-a-date", "go1.27.1"},
		{"1.0.0", "0123456789ab", "2026-10-25T00:00:00Z", "invalid-toolchain"},
	} {
		if err := build(t.TempDir(), tc.version, tc.commit, tc.date, tc.goVersion); err == nil {
			t.Fatalf("invalid build identity accepted: %+v", tc)
		}
	}
	if err := build(t.TempDir(), "1.0.0", "0123456789ab", "2026-10-25T00:00:00Z", "go1.27.1"); err == nil {
		t.Fatal("build with missing executables succeeded")
	}
}

func TestReleaseVerificationRejectsInvalidMetadata(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*releaseRecord)
	}{
		{"unsupported schema", func(r *releaseRecord) { r.SchemaVersion = "1" }},
		{"CGO mismatch", func(r *releaseRecord) { r.CGOEnabled = true }},
		{"invalid date", func(r *releaseRecord) { r.BuildDate = "invalid" }},
		{"missing asset", func(r *releaseRecord) { r.Assets = r.Assets[:len(r.Assets)-1] }},
		{"duplicate asset", func(r *releaseRecord) { r.Assets[1] = r.Assets[0] }},
		{"wrong digest", func(r *releaseRecord) { r.Assets[0].SHA256 = strings.Repeat("0", 64) }},
		{"wrong size", func(r *releaseRecord) { r.Assets[0].Size++ }},
		{"unknown asset", func(r *releaseRecord) { r.Assets[0].Name = "unexpected-asset" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := prepareRelease(t)
			path := filepath.Join(root, "RELEASE-METADATA.json")
			b, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			var r releaseRecord
			if err := json.Unmarshal(b, &r); err != nil {
				t.Fatal(err)
			}
			tc.change(&r)
			raw, err := jsonFile(r)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, raw, 0600); err != nil {
				t.Fatal(err)
			}
			if err := verify(root); err == nil {
				t.Fatal("invalid release metadata accepted")
			}
		})
	}
}

func TestReleaseVerificationRejectsMissingManifestsAndWrongEnvironment(t *testing.T) {
	root := prepareRelease(t)
	t.Setenv("VERSION", "2.0.0")
	if err := verify(root); err == nil {
		t.Fatal("mismatched release version accepted")
	}
	t.Setenv("VERSION", "")
	t.Setenv("COMMIT", "ffffffffffff")
	if err := verify(root); err == nil {
		t.Fatal("mismatched source commit accepted")
	}
	t.Setenv("COMMIT", "")
	if err := os.Remove(filepath.Join(root, "SHA256SUMS")); err != nil {
		t.Fatal(err)
	}
	if err := verify(root); err == nil {
		t.Fatal("missing checksum manifest accepted")
	}
}

func TestReleaseArchiveRejectsUnexpectedEntriesAndByteSubstitution(t *testing.T) {
	root := prepareRelease(t)
	want, err := archiveContents(root, "1.0.0", "0123456789ab", "2026-10-25T00:00:00Z", "go1.27.1", "windows", "amd64")
	if err != nil {
		t.Fatal(err)
	}
	good, err := makeArchive(want, "windows")
	if err != nil {
		t.Fatal(err)
	}
	wrong := append([]packedFile(nil), want...)
	wrong[0].data = []byte("different")
	if err := verifyArchive(good, "windows", wrong); err == nil {
		t.Fatal("substituted archive content accepted")
	}
	wrong = append([]packedFile(nil), want...)
	wrong[1].mode = 0644
	if err := verifyArchive(good, "windows", wrong); err == nil {
		t.Fatal("unexpected archive mode accepted")
	}
	if err := verifyArchive([]byte("not-a-zip"), "windows", want); err == nil {
		t.Fatal("invalid ZIP accepted")
	}

	var out bytes.Buffer
	z := zip.NewWriter(&out)
	h := &zip.FileHeader{Name: "../../escape", Method: zip.Store}
	h.SetMode(0755)
	w, err := z.CreateHeader(h)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte("bad")); err != nil {
		t.Fatal(err)
	}
	if err := z.Close(); err != nil {
		t.Fatal(err)
	}
	if err := verifyArchive(out.Bytes(), "windows", want); err == nil {
		t.Fatal("archive with traversing path accepted")
	}
	tarBytes, err := makeArchive(want, "linux")
	if err != nil {
		t.Fatal(err)
	}
	if err := verifyArchive(tarBytes, "linux", want); err != nil {
		t.Fatal(err)
	}
	wrong = append([]packedFile(nil), want...)
	wrong[0].name = "../../escape"
	if err := verifyArchive(tarBytes, "linux", wrong); err == nil {
		t.Fatal("unsafe TAR path accepted")
	}
	if err := verifyArchive([]byte("not-gzip"), "linux", want); err == nil {
		t.Fatal("invalid tar gzip accepted")
	}
	if err := verifyArchive(tarBytes[:20], "linux", want); err == nil {
		t.Fatal("truncated tar accepted")
	}
}

func TestReleaseFileAndJSONReadersFailClosed(t *testing.T) {
	root := t.TempDir()
	p := filepath.Join(root, "file")
	if _, err := readRegular(filepath.Join(root, "missing"), 50); err == nil {
		t.Fatal("missing file accepted")
	}
	if _, err := readRegular(root, 50); err == nil {
		t.Fatal("directory accepted")
	}
	if err := os.WriteFile(p, []byte("contents"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := readRegular(p, 1); err == nil {
		t.Fatal("oversized file accepted")
	}
	if b, err := readRegular(p, 50); err != nil || string(b) != "contents" {
		t.Fatalf("valid file: %s / %v", b, err)
	}
	link := filepath.Join(root, "symlink")
	if err := os.Symlink(p, link); err == nil {
		if _, err := readRegular(link, 50); err == nil {
			t.Fatal("symlink accepted")
		}
	}
	if err := writeAtomic(filepath.Join(root, "missing-parent", "asset"), []byte("value")); err == nil {
		t.Fatal("unwritable destination accepted")
	}
	if err := writeAtomic(p, []byte("replacement")); err != nil {
		t.Fatal(err)
	}
	for _, body := range []string{
		"{invalid",
		`{"schemaVersion":"1","unexpected":true}`,
		`{"schemaVersion":"1"}{"schemaVersion":"2"}`,
	} {
		var target struct {
			SchemaVersion string `json:"schemaVersion"`
		}
		if err := strictJSON([]byte(body), &target); err == nil {
			t.Fatalf("invalid JSON accepted: %s", body)
		}
	}
}

func TestReleasePackagingEntrypointAndOutputFailures(t *testing.T) {
	root := t.TempDir()
	seedExecutables(t, root)
	t.Setenv("VERSION", "1.0.0")
	t.Setenv("COMMIT", "0123456789ab")
	t.Setenv("BUILD_DATE", "2026-10-25T00:00:00Z")
	t.Setenv("GO_VERSION", "go1.27.1")
	t.Setenv("OUT_DIR", root)
	original := os.Args
	defer func() { os.Args = original }()
	os.Args = []string{"release-assets", "build"}
	main()
	os.Args = []string{"release-assets", "verify"}
	main()

	for _, blocked := range []string{
		archiveName("1.0.0", "linux", "amd64"),
		archiveName("1.0.0", "windows", "amd64"),
		"RELEASE-METADATA.json",
		"SHA256SUMS",
	} {
		t.Run(blocked, func(t *testing.T) {
			other := t.TempDir()
			seedExecutables(t, other)
			if err := os.Mkdir(filepath.Join(other, blocked), 0700); err != nil {
				t.Fatal(err)
			}
			if err := build(other, "1.0.0", "0123456789ab", "2026-10-25T00:00:00Z", "go1.27.1"); err == nil {
				t.Fatal("invalid output destination accepted")
			}
		})
	}
	if err := writeAtomic(root, []byte("bad")); err == nil {
		t.Fatal("directory used as output file")
	}
	if _, err := jsonFile(make(chan string)); err == nil {
		t.Fatal("unsupported JSON type accepted")
	}
}

func TestReleaseArchiveVerifierRejectsRepublishedContents(t *testing.T) {
	root := prepareRelease(t)
	want, err := archiveContents(root, "1.0.0", "0123456789ab", "2026-10-25T00:00:00Z", "go1.27.1", "windows", "amd64")
	if err != nil {
		t.Fatal(err)
	}
	for _, change := range []struct {
		name string
		mutate func([]packedFile)
	}{
		{"unsafe path", func(p []packedFile) { p[1].name = "../escape" }},
		{"wrong executable mode", func(p []packedFile) { p[1].mode = 0644 }},
		{"changed bytes of equal size", func(p []packedFile) {
			p[1].data = bytes.Repeat([]byte("X"), len(p[1].data))
		}},
	} {
		t.Run(change.name, func(t *testing.T) {
			mutated := append([]packedFile(nil), want...)
			change.mutate(mutated)
			archive, err := makeArchive(mutated, "windows")
			if err != nil { t.Fatal(err) }
			if err := verifyArchive(archive, "windows", want); err == nil {
				t.Fatal("modified archive accepted")
			}
		})
	}
	for _, tc := range []struct {
		name string
		files []packedFile
	}{
		{"missing tar entry", want[:len(want)-1]},
		{"unexpected tar entry", append(append([]packedFile(nil), want...), packedFile{name: "extra", data: []byte("x"), mode: 0644})},
	} {
		t.Run(tc.name, func(t *testing.T) {
			archive, err := makeArchive(tc.files, "linux")
			if err != nil { t.Fatal(err) }
			if err := verifyArchive(archive, "linux", want); err == nil {
				t.Fatal("invalid TAR archive accepted")
			}
		})
	}
}
