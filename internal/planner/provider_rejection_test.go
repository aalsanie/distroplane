package planner

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestProviderResolutionRejectsExplicitInvalidFiles(t *testing.T) {
	r, _, shadow, ext := bundledResolver(t)
	for _, name := range []string{"absent", "directory", "nonexecutable"} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(shadow, name+ext)
			switch name {
			case "directory":
				if err := os.Mkdir(path, 0700); err != nil {
					t.Fatal(err)
				}
			case "nonexecutable":
				if runtime.GOOS == "windows" {
					t.Skip("POSIX executable permissions")
				}
				if err := os.WriteFile(path, []byte("not executable"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := r.Resolve("npm", path); err == nil {
				t.Fatalf("unsafe explicit executable %q accepted", path)
			}
		})
	}
}

func TestProviderResolutionRejectsUntrustworthyFilesystem(t *testing.T) {
	r, dir, shadow, ext := bundledResolver(t)
	binary := fixtureProvider(t, shadow, "npm", ext)
	bundled := fixtureProvider(t, dir, "npm", ext)
	absFailure := r
	absFailure.abs = func(string) (string, error) { return "", errors.New("absolute path unavailable") }
	if _, err := absFailure.Resolve("npm", binary); err == nil {
		t.Fatal("failed absolute-path resolution accepted")
	}
	statFailure := r
	statFailure.stat = func(string) (os.FileInfo, error) { return nil, errors.New("stat failed") }
	if _, err := statFailure.Resolve("npm", binary); err == nil {
		t.Fatal("failed file stat accepted")
	}
	linkFailure := r
	linkFailure.evalLinks = func(string) (string, error) { return "", errors.New("link failure") }
	if _, err := linkFailure.Resolve("npm", binary); err == nil {
		t.Fatal("unresolvable executable accepted")
	}
	lstatFailure := r
	lstatFailure.lstat = func(path string) (os.FileInfo, error) {
		if path == bundled {
			return nil, errors.New("untrusted provider")
		}
		return os.Lstat(path)
	}
	if _, err := lstatFailure.Resolve("npm", ""); err == nil {
		t.Fatal("uninspectable installed provider accepted")
	}
	absence := r
	absence.lookPath = func(string) (string, error) { return "", errors.New("PATH provider missing") }
	if err := os.Remove(bundled); err != nil {
		t.Fatal(err)
	}
	if _, err := absence.Resolve("npm", ""); err == nil {
		t.Fatal("missing provider accepted")
	}
}

func TestBundledProviderSymlinkedCLIRoot(t *testing.T) {
	r, dir, _, ext := bundledResolver(t)
	want := fixtureProvider(t, dir, "homebrew", ext)
	realCLI, err := r.executable()
	if err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(t.TempDir(), "distroplane"+ext)
	if err := os.Symlink(realCLI, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	r.executable = func() (string, error) { return link, nil }
	if got, err := r.Resolve("homebrew", ""); err != nil || got != want {
		t.Fatalf("symlinked CLI picked wrong provider: %q %v", got, err)
	}
}
