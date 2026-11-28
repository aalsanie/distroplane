package providerretention

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

func withExclusiveProviderFile(t *testing.T, path string, check func()) {
	t.Helper()
	name, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		t.Fatal(err)
	}
	handle, err := syscall.CreateFile(name, syscall.GENERIC_READ, 0, nil, syscall.OPEN_EXISTING, syscall.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := syscall.CloseHandle(handle); err != nil {
			t.Error(err)
		}
	}()
	check()
}

func TestWindowsRetainedProviderSharingViolations(t *testing.T) {
	loaded, plan, ref, source := retentionFixture(t)
	root := filepath.Join(loaded.BaseDir, ".distroplane")
	digest := plan.ProviderDigests()[ref]
	if err := Retain(loaded, plan); err != nil {
		t.Fatal(err)
	}
	_, executable, manifest, err := retentionPaths(root, ref.Name(), digest)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Ext(executable) != ".exe" {
		t.Fatalf("retained provider is not a Windows executable path: %q", executable)
	}
	for _, path := range []string{source, executable, manifest} {
		t.Run(filepath.Base(path), func(t *testing.T) {
			before, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			withExclusiveProviderFile(t, path, func() {
				if err := Retain(loaded, plan); err == nil {
					t.Fatal("retention ignored a sharing violation")
				}
				if path != source {
					if _, _, err := Resolve(root, ref, digest); err == nil {
						t.Fatal("locked retained file was accepted")
					}
				}
			})
			after, err := os.ReadFile(path)
			if err != nil || string(after) != string(before) {
				t.Fatalf("sharing violation changed file: %v", err)
			}
			if got, trusted, err := Resolve(root, ref, digest); got != executable || trusted || err != nil {
				t.Fatalf("original retained provider not recovered: %q %t %v", got, trusted, err)
			}
		})
	}
}
