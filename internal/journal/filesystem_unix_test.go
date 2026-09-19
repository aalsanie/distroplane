//go:build linux || darwin

package journal

import (
	"os"
	"path/filepath"
	"testing"
)

func TestOpenWriterRejectsReadOnlyDirectory(t *testing.T) {
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })

	if _, err := OpenWriter(filepath.Join(dir, "run.journal"), runID()); err == nil {
		t.Fatal("read-only directory accepted")
	}
}
