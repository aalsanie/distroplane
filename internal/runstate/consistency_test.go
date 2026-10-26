package runstate

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestCatalogIgnoredTemporaryEntriesAndReleaseOrdering(t *testing.T) {
	root := filepath.Join(t.TempDir(), "project")
	store, _, _ := fixturePlanWithRelease(t, root, "a", "release-z")
	_, _, _ = fixturePlanWithRelease(t, root, "b", "release-a")
	for _, area := range []string{"plans", "runs"} {
		path := filepath.Join(store.Root, "catalog", area, ".entry-abandoned")
		if err := os.WriteFile(path, []byte("uncommitted"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	records, err := store.Inventory()
	if err != nil || len(records) != 2 {
		t.Fatalf("inventory failed: %+v, %v", records, err)
	}
	if records[0].ReleaseID != "release-a" || records[1].ReleaseID != "release-z" {
		t.Fatalf("release selection is not deterministic: %+v", records)
	}
}

func TestCatalogMissingAndCorruptPlanClaims(t *testing.T) {
	t.Run("missing plan", func(t *testing.T) {
		store, _, _ := fixturePlan(t, filepath.Join(t.TempDir(), "project"), "data")
		if _, err := store.RegisterPlan(filepath.Join(store.Root, "missing-plan.json")); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("missing plan accepted: %v", err)
		}
	})
	t.Run("invalid plan for claim", func(t *testing.T) {
		store, plan, _ := fixturePlan(t, filepath.Join(t.TempDir(), "project"), "data")
		if err := os.WriteFile(plan, []byte("invalid json"), 0600); err != nil {
			t.Fatal(err)
		}
		if err := store.ClaimRun(plan, filepath.Join(store.Root, "run.journal"), "run-one"); err == nil {
			t.Fatal("corrupt saved plan accepted by run claim")
		}
	})
	t.Run("corrupted existing primary claim", func(t *testing.T) {
		store, plan, id := fixturePlan(t, filepath.Join(t.TempDir(), "project"), "data")
		path := filepath.Join(store.Root, "catalog", "runs", key(string(id)))
		if err := os.WriteFile(path, []byte("{bad"), 0600); err != nil {
			t.Fatal(err)
		}
		if err := store.ClaimRun(plan, filepath.Join(store.Root, "journal"), "run-one"); err == nil {
			t.Fatal("corrupt preexisting claim overwritten")
		}
	})
	t.Run("invalid catalog selection", func(t *testing.T) {
		store, _, _ := fixturePlan(t, filepath.Join(t.TempDir(), "project"), "data")
		if err := os.WriteFile(filepath.Join(store.Root, "catalog", "plans", "unexpected"), []byte("invalid"), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := store.Select("release-1", ""); err == nil {
			t.Fatal("selection ignored corrupt catalog index")
		}
	})
}

func TestCatalogRejectsPlanAliasAndRebinding(t *testing.T) {
	t.Run("changed absolute plan path", func(t *testing.T) {
		root := filepath.Join(t.TempDir(), "project")
		store, _, firstID := fixturePlanWithRelease(t, root, "original", "one")
		_, replacement, _ := fixturePlanWithRelease(t, root, "changed", "two")
		index := filepath.Join(store.Root, "catalog", "plans", key(string(firstID)))
		e, err := readEntry(index)
		if err != nil {
			t.Fatal(err)
		}
		e.PlanPath = replacement
		raw, err := json.Marshal(e)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(index, raw, 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := store.Inventory(); err == nil {
			t.Fatal("different plan reused under previous identity")
		}
	})
	t.Run("symlinked plan directory", func(t *testing.T) {
		store, plan, id := fixturePlan(t, filepath.Join(t.TempDir(), "project"), "artifact")
		alias := filepath.Join(filepath.Dir(store.Root), "plans-alias")
		if err := os.Symlink(filepath.Dir(plan), alias); err != nil {
			t.Skipf("directory symlink unavailable: %v", err)
		}
		index := filepath.Join(store.Root, "catalog", "plans", key(string(id)))
		e, err := readEntry(index)
		if err != nil {
			t.Fatal(err)
		}
		e.PlanPath = filepath.Join(alias, filepath.Base(plan))
		raw, err := json.Marshal(e)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(index, raw, 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := store.Inventory(); err == nil {
			t.Fatal("symlink alias treated as canonical plan path")
		}
	})
}

func TestCatalogJournalReadFailures(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permissions")
	}
	store, plan, id := fixturePlan(t, filepath.Join(t.TempDir(), "project"), "data")
	path := filepath.Join(store.Root, "journal.bin")
	if err := store.ClaimRun(plan, path, "run-one"); err != nil {
		t.Fatal(err)
	}
	startJournal(t, path, "run-one", id)
	if err := os.Chmod(path, 0000); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(path, 0600)
	if _, err := store.Inventory(); err == nil {
		t.Fatal("unreadable journal was silently ignored")
	}
}

func TestCatalogUnreadableIndexAndDirectory(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permissions")
	}
	t.Run("index read failure", func(t *testing.T) {
		store, _, id := fixturePlan(t, filepath.Join(t.TempDir(), "project"), "data")
		path := filepath.Join(store.Root, "catalog", "plans", key(string(id)))
		if err := os.Chmod(path, 0000); err != nil {
			t.Fatal(err)
		}
		defer os.Chmod(path, 0600)
		if _, err := store.Inventory(); err == nil {
			t.Fatal("unreadable plan index was accepted")
		}
	})
	t.Run("run directory read failure", func(t *testing.T) {
		store, _, _ := fixturePlan(t, filepath.Join(t.TempDir(), "project"), "data")
		path := filepath.Join(store.Root, "catalog", "runs")
		if err := os.Chmod(path, 0300); err != nil {
			t.Fatal(err)
		}
		defer os.Chmod(path, 0700)
		if _, err := store.Inventory(); err == nil {
			t.Fatal("inaccessible run directory was accepted")
		}
	})
	t.Run("no permission to create root", func(t *testing.T) {
		project := t.TempDir()
		if err := os.Chmod(project, 0500); err != nil {
			t.Fatal(err)
		}
		defer os.Chmod(project, 0700)
		if err := (Store{Root: filepath.Join(project, ".distroplane")}).Ensure(); err == nil {
			t.Fatal("unwritable project accepted catalog creation")
		}
	})
}

func TestCatalogBadJournalPathAndPlanIndex(t *testing.T) {
	store, plan, id := fixturePlan(t, filepath.Join(t.TempDir(), "project"), "bytes")
	runPath := filepath.Join(store.Root, "run.journal")
	if err := store.ClaimRun(plan, runPath, "run-one"); err != nil {
		t.Fatal(err)
	}
	startJournal(t, runPath, "run-one", id)
	if err := store.ClaimRun(plan, runPath, "run-one"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Select("release-1", "run-one"); err != nil {
		t.Fatal(err)
	}
}
