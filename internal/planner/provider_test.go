package planner

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestExecutableResolverConvention(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "provider")
	if err := os.WriteFile(file, []byte("x"), 0o700); err != nil {
		t.Fatal(err)
	}

	lookedUp := ""
	r := executableResolver{
		lookPath: func(candidate string) (string, error) {
			lookedUp = candidate
			if candidate == providerExecutablePrefix+"fake" {
				return file, nil
			}
			return candidate, nil
		},
		abs:  filepath.Abs,
		stat: os.Stat,
	}
	if got, err := r.Resolve("fake", ""); err != nil || got == "" {
		t.Fatalf("got=%q err=%v", got, err)
	}
	if lookedUp != providerExecutablePrefix+"fake" {
		t.Fatalf("looked up %q", lookedUp)
	}

	lookedUp = ""
	if got, err := r.Resolve("fake", file); err != nil || got == "" {
		t.Fatalf("got=%q err=%v", got, err)
	}
	if lookedUp != file {
		t.Fatalf("configured executable lookup=%q", lookedUp)
	}

	r.lookPath = func(string) (string, error) { return "", errors.New("missing") }
	if _, err := r.Resolve("missing", ""); err == nil {
		t.Fatal("lookPath error ignored")
	}
}
