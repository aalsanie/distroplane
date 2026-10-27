package providerretention

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/aalsanie/distroplane/internal/domain"
)

type faultFile struct {
	*os.File
	fail string
}

func (f *faultFile) Write(data []byte) (int, error) {
	if f.fail == "write" {
		return 0, errors.New("injected write error")
	}
	return f.File.Write(data)
}
func (f *faultFile) Sync() error {
	if f.fail == "sync" {
		return errors.New("injected sync error")
	}
	return f.File.Sync()
}
func (f *faultFile) Chmod(mode os.FileMode) error {
	if f.fail == "chmod" {
		return errors.New("injected chmod error")
	}
	return f.File.Chmod(mode)
}
func (f *faultFile) Close() error {
	err := f.File.Close()
	if f.fail == "close" {
		return errors.New("injected close error")
	}
	return err
}
func (f *faultFile) Name() string {
	if f.fail == "stat" {
		return f.File.Name() + ".vanished"
	}
	return f.File.Name()
}

func injectedFactory(fail string) tempFactory {
	return func(dir, pattern string) (temporaryFile, error) {
		if fail == "create" {
			return nil, errors.New("injected temp creation error")
		}
		f, err := os.CreateTemp(dir, pattern)
		if err != nil {
			return nil, err
		}
		return &faultFile{File: f, fail: fail}, nil
	}
}

func TestProviderSnapshotHandlesInjectedFilesystemFailures(t *testing.T) {
	loaded, plan, ref, _ := retentionFixture(t)
	digest := plan.ProviderDigests()[ref]
	root := filepath.Join(loaded.BaseDir, ".distroplane")
	dir, target, _, err := retentionPaths(root, ref.Name(), digest)
	if err != nil { t.Fatal(err) }
	if err := privateDirs(root, dir, true); err != nil { t.Fatal(err) }
	for _, stage := range []string{"create", "write", "sync", "chmod", "close", "stat"} {
		t.Run(stage, func(t *testing.T) {
			if err := writeCopiedExecutableWith(bytes.NewBufferString("provider-build-A"), target, digest, injectedFactory(stage), os.Link); err == nil {
				t.Fatalf("injected %s error was lost", stage)
			}
			if _, err := os.Lstat(target); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("failed copy installed executable: %v", err)
			}
		})
	}
}

func TestProviderSnapshotLinkConflictsAreVerifiedWithoutOverwrite(t *testing.T) {
	loaded, plan, ref, _ := retentionFixture(t)
	digest := plan.ProviderDigests()[ref]
	root := filepath.Join(loaded.BaseDir, ".distroplane")
	dir, target, _, err := retentionPaths(root, ref.Name(), digest)
	if err != nil { t.Fatal(err) }
	if err := privateDirs(root, dir, true); err != nil { t.Fatal(err) }
	for _, tc := range []struct {
		name string
		existing string
		wantError bool
	}{
		{"identical concurrent copy", "provider-build-A", false},
		{"different concurrent copy", "provider-build-B", true},
		{"no destination", "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			os.Remove(target)
			if tc.existing != "" {
				if err := os.WriteFile(target, []byte(tc.existing), 0700); err != nil { t.Fatal(err) }
			}
			err := writeCopiedExecutableWith(bytes.NewBufferString("provider-build-A"), target, digest, defaultTempFactory, func(_, _ string) error {
				return os.ErrExist
			})
			if (err != nil) != tc.wantError {
				t.Fatalf("link collision result %v, wanted error %t", err, tc.wantError)
			}
		})
	}
}

func TestImmutableReceiptHandlesInjectedFilesystemFailures(t *testing.T) {
	dir := t.TempDir()
	for _, stage := range []string{"create", "write", "sync", "chmod", "close"} {
		t.Run(stage, func(t *testing.T) {
			target := filepath.Join(dir, "receipt-"+stage+".json")
			if err := immutableWriteWith(target, []byte("record"), 0600, injectedFactory(stage), os.Link); err == nil {
				t.Fatalf("injected %s failure was ignored", stage)
			}
			if _, err := os.Lstat(target); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("incomplete receipt was installed: %v", err)
			}
		})
	}
}

func TestImmutableReceiptLinkConflictsFailClosed(t *testing.T) {
	dir := t.TempDir()
	data := []byte("immutable receipt")
	for _, tc := range []struct {
		name string
		existing []byte
		wantError bool
	}{
		{"same bytes concurrent writer", data, false},
		{"conflicting concurrent writer", []byte("different"), true},
		{"no existing file", nil, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			target := filepath.Join(dir, strings.ReplaceAll(tc.name, " ", "_"))
			if tc.existing != nil {
				if err := os.WriteFile(target, tc.existing, 0600); err != nil { t.Fatal(err) }
			}
			err := immutableWriteWith(target, data, 0600, defaultTempFactory, func(_, _ string) error {
				return os.ErrExist
			})
			if (err != nil) != tc.wantError {
				t.Fatalf("receipt link conflict %v wantError=%t", err, tc.wantError)
			}
		})
	}
}

func TestImmutableReceiptAndSnapshotRejectInvalidPathInputs(t *testing.T) {
	dir := t.TempDir()
	digest, _ := domain.NewSHA256Digest(strings.Repeat("e", 64))
	if err := immutableWriteWith(string([]byte{'x', 0, 'y'}), []byte("receipt"), 0600, defaultTempFactory, os.Link); err == nil {
		t.Fatal("invalid receipt path accepted")
	}
	if err := immutableWriteWith(filepath.Join(dir, "not-a-directory", "binding.json"), []byte("receipt"), 0600, defaultTempFactory, os.Link); err == nil {
		t.Fatal("missing receipt parent accepted")
	}
	if err := writeCopiedExecutableWith(bytes.NewReader([]byte("not a provider")), filepath.Join(dir,"nonexisting","provider"), digest, defaultTempFactory, os.Link); err == nil {
		t.Fatal("missing snapshot directory accepted")
	}
}

func TestRetainedSnapshotPreservesExclusiveInstallation(t *testing.T) {
	loaded, plan, ref, _ := retentionFixture(t)
	root := filepath.Join(loaded.BaseDir, ".distroplane")
	dir, path, _, err := retentionPaths(root, ref.Name(), plan.ProviderDigests()[ref])
	if err != nil { t.Fatal(err) }
	if err := privateDirs(root, dir, true); err != nil { t.Fatal(err) }
	if err := os.WriteFile(path, []byte("preexisting executable"), 0700); err != nil { t.Fatal(err) }
	if err := writeCopiedExecutableWith(bytes.NewBufferString("provider-build-A"), path, plan.ProviderDigests()[ref], defaultTempFactory, os.Link); err == nil {
		t.Fatal("snapshot overwrote preexisting incorrect bytes")
	}
	data, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(data, []byte("preexisting executable")) {
		t.Fatalf("preexisting bytes changed: %v", err)
	}
}

func TestReceiptReadFailureAndProviderDirectorySafety(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("permissions are enforced differently on Windows")
	}
	loaded, plan, ref, _ := retentionFixture(t)
	if err := Retain(loaded, plan); err != nil { t.Fatal(err) }
	root := filepath.Join(loaded.BaseDir, ".distroplane")
	_, _, receipt, err := retentionPaths(root, ref.Name(), plan.ProviderDigests()[ref])
	if err != nil { t.Fatal(err) }
	if err := os.Chmod(receipt, 0000); err != nil { t.Fatal(err) }
	defer os.Chmod(receipt, 0600)
	if _, _, err := Resolve(root, ref, plan.ProviderDigests()[ref]); err == nil {
		t.Fatal("unreadable receipt was accepted")
	}
	if err := immutableWrite(receipt, []byte("irrelevant"), 0600); err == nil {
		t.Fatal("unreadable receipt was overwritten")
	}
}

var _ io.Writer = (*faultFile)(nil)
