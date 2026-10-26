package runstate

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/aalsanie/distroplane/internal/domain"
	"github.com/aalsanie/distroplane/internal/journal"
)

func TestMissingCatalogCannotBeSilentlyRecreated(t *testing.T) {
	store, path, _ := fixturePlan(t, filepath.Join(t.TempDir(), "project"), "payload")
	if err := os.RemoveAll(filepath.Join(store.Root, "catalog")); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Inventory(); err == nil {
		t.Fatal("deleted catalog accepted as empty project")
	}
	if err := store.Ensure(); err == nil {
		t.Fatal("deleted catalog silently replaced")
	}
	if _, err := store.RegisterPlan(path); err == nil {
		t.Fatal("historical plan registered after catalog loss")
	}
}

func TestExistingPlansWithoutCatalogFailClosed(t *testing.T) {
	root := filepath.Join(t.TempDir(), ".distroplane")
	if err := os.MkdirAll(filepath.Join(root, "plans"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "plans", "saved.json"), []byte("saved intent"), 0600); err != nil {
		t.Fatal(err)
	}
	s := Store{Root: root}
	if err := s.Ensure(); err == nil {
		t.Fatal("existing plans silently assigned a new catalog")
	}
	if _, err := s.Inventory(); err == nil {
		t.Fatal("orphaned plans silently treated as empty")
	}
}

func TestCatalogRejectsUnsafeDirectoryAndUnexpectedFile(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Run("group writable", func(t *testing.T) {
			store, _, _ := fixturePlan(t, filepath.Join(t.TempDir(), "project"), "bytes")
			dir := filepath.Join(store.Root, "catalog", "runs")
			if err := os.Chmod(dir, 0777); err != nil { t.Fatal(err) }
			if _, err := store.Inventory(); err == nil {
				t.Fatal("group/world writable catalog accepted")
			}
		})
	}
	t.Run("unexpected run file", func(t *testing.T) {
		store, _, _ := fixturePlan(t, filepath.Join(t.TempDir(), "project"), "bytes")
		dir := filepath.Join(store.Root, "catalog", "runs")
		if err := os.WriteFile(filepath.Join(dir, "unexpected"), []byte("spoofed"), 0600); err != nil { t.Fatal(err) }
		if _, err := store.Inventory(); err == nil { t.Fatal("orphaned index file accepted") }
	})
	t.Run("unexpected plan file", func(t *testing.T) {
		store, _, _ := fixturePlan(t, filepath.Join(t.TempDir(), "project"), "bytes")
		dir := filepath.Join(store.Root, "catalog", "plans")
		if err := os.WriteFile(filepath.Join(dir, "extra"), []byte("{}"), 0600); err != nil { t.Fatal(err) }
		if _, err := store.Inventory(); err == nil { t.Fatal("unexpected plan index file accepted") }
	})
	t.Run("catalog symlink", func(t *testing.T) {
		store, _, _ := fixturePlan(t, filepath.Join(t.TempDir(), "project"), "bytes")
		dir := filepath.Join(store.Root, "catalog", "runs")
		other := t.TempDir()
		if err := os.Remove(dir); err != nil { t.Fatal(err) }
		if err := os.Symlink(other, dir); err != nil { t.Skipf("symlinks unavailable: %v", err) }
		if _, err := store.Inventory(); err == nil { t.Fatal("symlink catalog directory accepted") }
	})
	t.Run("invalid root", func(t *testing.T) {
		root := filepath.Join(t.TempDir(), ".distroplane")
		if err := os.WriteFile(root, []byte("not-directory"), 0600); err != nil { t.Fatal(err) }
		if err := (Store{Root: root}).Ensure(); err == nil { t.Fatal("catalog root is a file") }
	})
}

func TestRunClaimCopiesRemainConsistent(t *testing.T) {
	for _, tc := range []struct {
		name string
		mutate func(*testing.T, string, string)
		shouldRecover bool
	}{
		{"corrupted primary", func(t *testing.T, primary, backup string) {
			if err := os.WriteFile(primary, []byte("{bad"), 0600); err != nil { t.Fatal(err) }
		}, true},
		{"corrupted backup", func(t *testing.T, primary, backup string) {
			if err := os.WriteFile(backup, []byte("{bad"), 0600); err != nil { t.Fatal(err) }
		}, true},
		{"missing primary", func(t *testing.T, primary, backup string) {
			if err := os.Remove(primary); err != nil { t.Fatal(err) }
		}, true},
		{"mismatched claims", func(t *testing.T, primary, backup string) {
			value, err := readEntry(primary)
			if err != nil { t.Fatal(err) }
			value.JournalPath = filepath.Join(filepath.Dir(value.JournalPath), "other.journal")
			raw, err := json.Marshal(value)
			if err != nil { t.Fatal(err) }
			if err := os.WriteFile(primary, raw, 0600); err != nil { t.Fatal(err) }
		}, false},
		{"corrupted both copies", func(t *testing.T, primary, backup string) {
			if err := os.WriteFile(primary, []byte("{bad"), 0600); err != nil { t.Fatal(err) }
			if err := os.WriteFile(backup, []byte("{bad"), 0600); err != nil { t.Fatal(err) }
		}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store, plan, id := fixturePlan(t, filepath.Join(t.TempDir(), "project"), "payload")
			journalPath := filepath.Join(store.Root, "journal.bin")
			if err := store.ClaimRun(plan, journalPath, "run-one"); err != nil { t.Fatal(err) }
			startJournal(t, journalPath, "run-one", id)
			primary := filepath.Join(store.Root, "catalog", "runs", key(string(id)))
			backup := primary + ".backup"
			tc.mutate(t, primary, backup)
			records, err := store.Inventory()
			if tc.shouldRecover {
				if err != nil || len(records) != 1 || records[0].RunID != "run-one" {
					t.Fatalf("single-good-copy recovery: %+v %v", records, err)
				}
			} else if err == nil {
				t.Fatalf("conflicting/corrupt redundant claims accepted: %+v", records)
			}
			if b, err := os.ReadFile(journalPath); err != nil || len(b) == 0 {
				t.Fatalf("run claim corruption lost journal: %v", err)
			}
		})
	}
}

func TestInvalidExistingJournalsCannotSeedNewRun(t *testing.T) {
	for _, tc := range []struct {
		name string
		prepare func(*testing.T, string, domain.PlanID)
	}{
		{"empty", func(t *testing.T, path string, id domain.PlanID) {
			if err := os.WriteFile(path, nil, 0600); err != nil { t.Fatal(err) }
		}},
		{"wrong run", func(t *testing.T, path string, id domain.PlanID) {
			startJournal(t, path, "run-another", id)
		}},
		{"wrong plan", func(t *testing.T, path string, id domain.PlanID) {
			startJournal(t, path, "run-current", "other-plan")
		}},
		{"corrupted", func(t *testing.T, path string, id domain.PlanID) {
			if err := os.WriteFile(path, []byte("corrupt-journal"), 0600); err != nil { t.Fatal(err) }
		}},
		{"symlink", func(t *testing.T, path string, id domain.PlanID) {
			target := filepath.Join(filepath.Dir(path), "target.journal")
			startJournal(t, target, "run-current", id)
			if err := os.Symlink(target, path); err != nil { t.Skipf("symlink unavailable: %v", err) }
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store, plan, id := fixturePlan(t, filepath.Join(t.TempDir(), "project"), "payload")
			path := filepath.Join(store.Root, "existing.journal")
			tc.prepare(t, path, id)
			if err := store.ClaimRun(plan, path, "run-current"); err == nil {
				t.Fatal("invalid prior journal accepted before side effects")
			}
			records, err := store.Inventory()
			if err != nil || len(records) != 1 || records[0].State != "PLANNED" {
				t.Fatalf("failed claim unexpectedly created run: %+v %v", records, err)
			}
		})
	}
}

func TestCatalogRejectsChangedPlanAndOrphanRunEntries(t *testing.T) {
	t.Run("lost plan", func(t *testing.T) {
		store, path, _ := fixturePlan(t, filepath.Join(t.TempDir(), "project"), "payload")
		if err := os.Remove(path); err != nil { t.Fatal(err) }
		if _, err := store.Inventory(); err == nil { t.Fatal("missing saved plan accepted") }
	})
	t.Run("plan content corrupted", func(t *testing.T) {
		store, path, _ := fixturePlan(t, filepath.Join(t.TempDir(), "project"), "payload")
		if err := os.WriteFile(path, []byte("{invalid"), 0600); err != nil { t.Fatal(err) }
		if _, err := store.Inventory(); err == nil { t.Fatal("corrupted saved plan accepted") }
	})
	t.Run("orphan claim", func(t *testing.T) {
		store, _, _ := fixturePlan(t, filepath.Join(t.TempDir(), "project"), "payload")
		if err := os.WriteFile(filepath.Join(store.Root, "catalog", "runs", key("unrelated")), []byte("{}"), 0600); err != nil { t.Fatal(err) }
		if _, err := store.Inventory(); err == nil { t.Fatal("orphaned run entry accepted") }
	})
	t.Run("wrong catalog path", func(t *testing.T) {
		store, _, id := fixturePlan(t, filepath.Join(t.TempDir(), "project"), "payload")
		original := filepath.Join(store.Root, "catalog", "plans", key(string(id)))
		renamed := filepath.Join(store.Root, "catalog", "plans", key("renamed"))
		if err := os.Rename(original, renamed); err != nil { t.Fatal(err) }
		if _, err := store.Inventory(); err == nil { t.Fatal("renamed plan index accepted") }
	})
}

func TestCatalogRejectsCorruptOrMissingJournalDuringInventory(t *testing.T) {
	store, plan, id := fixturePlan(t, filepath.Join(t.TempDir(), "project"), "payload")
	journalPath := filepath.Join(store.Root, "registered.journal")
	if err := store.ClaimRun(plan, journalPath, "run-one"); err != nil { t.Fatal(err) }
	startJournal(t, journalPath, "run-one", id)
	if err := os.WriteFile(journalPath, []byte("broken"), 0600); err != nil { t.Fatal(err) }
	if _, err := store.Inventory(); err == nil { t.Fatal("corrupt recorded journal accepted") }
	if err := os.Remove(journalPath); err != nil { t.Fatal(err) }
	records, err := store.Inventory()
	if err != nil || len(records) != 1 || records[0].State != "RESERVED" {
		t.Fatalf("deleted journal not marked as reserved: %+v %v", records, err)
	}
	if err := store.ClaimRun(plan, journalPath, "run-one"); err == nil {
		t.Fatal("lost journal silently started another run")
	}
}

func TestRunCatalogInvalidInputs(t *testing.T) {
	store, plan, _ := fixturePlan(t, filepath.Join(t.TempDir(), "project"), "payload")
	if err := store.ClaimRun(plan, filepath.Join(store.Root, "other"), ""); err == nil {
		t.Fatal("invalid run identity accepted")
	}
	if err := store.ClaimRun(filepath.Join(store.Root, "missing-plan"), filepath.Join(store.Root, "other"), "run"); err == nil {
		t.Fatal("missing plan accepted")
	}
	if _, err := (Store{}).Inventory(); err == nil { t.Fatal("empty root accepted") }
	if _, err := (Store{}).RegisterPlan(plan); err == nil { t.Fatal("empty root accepted for registration") }
	if _, err := canonicalFile(filepath.Join(t.TempDir(), "missing", "missing", "journal"), false); err != nil {
		t.Fatalf("new journal parent cannot be created: %v", err)
	}
	unknown := filepath.Join(store.Root, "catalog", "plans", "strange.bak")
	if err := os.WriteFile(unknown, []byte("x"), 0600); err != nil { t.Fatal(err) }
	if _, err := store.Inventory(); err == nil { t.Fatal("unknown plan catalog extension accepted") }
	if !strings.Contains(ErrConflict.Error(), "run") { t.Fatal("lost conflict sentinel") }
	if !errors.Is(ErrAmbiguous, ErrAmbiguous) { t.Fatal("lost ambiguity sentinel") }
}
