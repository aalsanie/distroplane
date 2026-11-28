package runstate

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestCatalogClaimProcessCrash(t *testing.T) {
	if os.Getenv("DISTROPLANE_CLAIM_HELPER") == "1" {
		s := Store{Root: os.Getenv("DISTROPLANE_CLAIM_ROOT")}
		if err := s.ClaimRun(os.Getenv("DISTROPLANE_CLAIM_PLAN"), os.Getenv("DISTROPLANE_CLAIM_JOURNAL"), "run-abrupt"); err != nil {
			os.Exit(24)
		}
		// Exit without deferred cleanup and before the journal is created.
		os.Exit(0)
	}
	root := filepath.Join(t.TempDir(), "project")
	store, plan, _ := fixturePlan(t, root, "test bytes")
	journal := filepath.Join(root, ".distroplane", "runs", "crashed.journal")
	child := exec.Command(os.Args[0], "-test.run=^TestCatalogClaimProcessCrash$")
	child.Env = append(os.Environ(),
		"DISTROPLANE_CLAIM_HELPER=1",
		"DISTROPLANE_CLAIM_ROOT="+store.Root,
		"DISTROPLANE_CLAIM_PLAN="+plan,
		"DISTROPLANE_CLAIM_JOURNAL="+journal)
	if out, err := child.CombinedOutput(); err != nil {
		t.Fatalf("process reservation failed: %v: %s", err, out)
	}
	inventory, err := store.Inventory()
	if err != nil || len(inventory) != 1 || inventory[0].State != "RESERVED" || inventory[0].RunID != "run-abrupt" {
		t.Fatalf("crash lost durable claim: %+v err=%v", inventory, err)
	}
	if err := store.ClaimRun(plan, journal, "run-abrupt"); err == nil {
		t.Fatal("crashed reservation without a journal was restarted")
	}
	if _, err := os.Stat(journal); !os.IsNotExist(err) {
		t.Fatalf("crashed reservation wrote unexpected journal: %v", err)
	}
}

func TestCatalogAmbiguousPlansFailClosed(t *testing.T) {
	root := filepath.Join(t.TempDir(), "project")
	store, path1, id1 := fixturePlan(t, root, "first artifact")
	if _, err := store.RegisterPlan(path1); err != nil {
		t.Fatal(err)
	}
	_, path2, id2 := fixturePlan(t, root, "updated artifact")
	if _, err := store.RegisterPlan(path2); err != nil {
		t.Fatal(err)
	}
	if id1 == id2 {
		t.Fatal("changing artifact did not create new intent")
	}
	records, err := store.Inventory()
	if err != nil || len(records) != 2 {
		t.Fatalf("inventory=%+v err=%v", records, err)
	}
	_, err = store.Select("release-1", "")
	if !errors.Is(err, ErrAmbiguous) || !strings.Contains(err.Error(), string(id1)) || !strings.Contains(err.Error(), string(id2)) {
		t.Fatalf("ambiguous plan chosen or no candidates displayed: %v", err)
	}
}

func TestCatalogClaimDirectorySyncFailurePreservesReservation(t *testing.T) {
	for _, stage := range []string{"backup", "primary"} {
		t.Run(stage, func(t *testing.T) {
			store, plan, id := fixturePlan(t, filepath.Join(t.TempDir(), "project"), "data")
			planPath, err := canonicalFile(plan, true)
			if err != nil {
				t.Fatal(err)
			}
			journalPath, err := canonicalFile(filepath.Join(store.Root, "original.journal"), false)
			if err != nil {
				t.Fatal(err)
			}
			claim := entry{SchemaVersion: schemaVersion, PlanID: string(id), PlanPath: planPath, RunID: "run-original", JournalPath: journalPath}
			dir := filepath.Join(store.Root, "catalog", "runs")
			name := key(string(id)) + ".backup"
			if stage == "primary" {
				if created, err := install(dir, name, claim); !created || err != nil {
					t.Fatalf("backup claim: %t %v", created, err)
				}
				name = key(string(id))
			}
			failure := errors.New("catalog directory sync failed")
			failSync := func(string) error { return failure }
			if created, err := installWith(dir, name, claim, failSync); !created || !errors.Is(err, failure) {
				t.Fatalf("installed but uncertain claim: %t %v", created, err)
			}
			if created, err := installWith(dir, name, claim, failSync); created || !errors.Is(err, failure) {
				t.Fatalf("identical claim retry acknowledged without sync: %t %v", created, err)
			}
			records, err := store.Inventory()
			if err != nil || len(records) != 1 || records[0].State != "RESERVED" || records[0].RunID != claim.RunID {
				t.Fatalf("uncertain claim lost reservation: %+v %v", records, err)
			}
			alternate := filepath.Join(store.Root, "alternate.journal")
			if err := store.ClaimRun(planPath, alternate, "run-alternate"); !errors.Is(err, ErrConflict) {
				t.Fatalf("alternate run accepted: %v", err)
			}
			if _, err := os.Stat(alternate); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("unexpected alternate journal: %v", err)
			}
			if err := store.ClaimRun(planPath, journalPath, "run-original"); err == nil {
				t.Fatal("uncertain claim with missing journal was restarted")
			}
			startJournal(t, journalPath, "run-original", id)
			if err := store.ClaimRun(planPath, journalPath, "run-original"); err != nil {
				t.Fatalf("original durable journal not recovered: %v", err)
			}
			for _, name := range []string{key(string(id)), key(string(id)) + ".backup"} {
				got, err := readEntry(filepath.Join(dir, name))
				if err != nil || got != claim {
					t.Fatalf("claim changed: %+v %v", got, err)
				}
			}
		})
	}
}
