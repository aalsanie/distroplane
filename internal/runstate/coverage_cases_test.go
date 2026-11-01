package runstate

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCatalogPathResolutionAndImmutableFileSafety(t *testing.T) {
	project := t.TempDir()
	for _, root := range []string{"", " ", filepath.Join(project, "other")} {
		if err := (Store{Root: root}).Ensure(); err == nil {
			t.Fatalf("unsafe catalog root accepted: %q", root)
		}
	}
	root := filepath.Join(project, ".distroplane")
	if err := (Store{Root: root}).Ensure(); err != nil {
		t.Fatal(err)
	}
	catalog, err := (Store{Root: root}).directory(false)
	if err != nil || !strings.HasSuffix(catalog, "catalog") {
		t.Fatalf("catalog lookup: %q: %v", catalog, err)
	}
	if _, err := canonicalFile("", false); err == nil {
		t.Fatal("empty path accepted")
	}
	nested := filepath.Join(project, "missing", "nested", "journal")
	canonical, err := canonicalFile(nested, false)
	if err != nil || canonical != nested {
		t.Fatalf("future journal path: %q: %v", canonical, err)
	}
	if _, err := canonicalFile(nested, true); err == nil {
		t.Fatal("nonexistent journal accepted as present")
	}
	if _, err := canonicalFile(project, true); err == nil {
		t.Fatal("directory accepted as journal")
	}
	path := filepath.Join(project, "journal")
	if err := os.WriteFile(path, []byte("journal"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got, err := canonicalFile(path, true); err != nil || got != path {
		t.Fatalf("existing journal: %q: %v", got, err)
	}
	link := filepath.Join(project, "journal-link")
	if err := os.Symlink(path, link); err == nil {
		if _, err := canonicalFile(link, true); err == nil {
			t.Fatal("journal symlink accepted")
		}
	}
}
