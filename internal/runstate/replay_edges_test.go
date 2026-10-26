package runstate

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/aalsanie/distroplane/internal/domain"
	"github.com/aalsanie/distroplane/internal/journal"
	"github.com/aalsanie/distroplane/internal/planner"
)

func TestMissingCatalogIsEmptyOnlyForNewProject(t *testing.T) {
	root := filepath.Join(t.TempDir(), ".distroplane")
	records, err := (Store{Root: root}).Inventory()
	if err != nil || len(records) != 0 {
		t.Fatalf("new project inventory = %+v, %v", records, err)
	}
	if err := directorySync(filepath.Join(t.TempDir(), "missing")); err == nil && runtime.GOOS != "windows" {
		t.Fatal("syncing nonexistent directory must fail")
	}
}

func TestCatalogRejectsInvalidRunIdentityRecord(t *testing.T) {
	store, _, id := fixturePlan(t, filepath.Join(t.TempDir(), "project"), "artifact")
	record := entry{SchemaVersion: schemaVersion, PlanID: string(id), PlanPath: "/invalid/plan"}
	dir := filepath.Join(store.Root, "catalog", "runs")
	if _, err := install(dir, key(string(id))+".backup", record); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Inventory(); err == nil {
		t.Fatal("run claim without a run ID was considered valid")
	}
}

func TestCatalogDetectsDifferentPlanLocationInRunEntry(t *testing.T) {
	store, plan, id := fixturePlan(t, filepath.Join(t.TempDir(), "project"), "artifact")
	journalPath := filepath.Join(store.Root, "journal.bin")
	if err := store.ClaimRun(plan, journalPath, "run-one"); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{key(string(id)), key(string(id)) + ".backup"} {
		path := filepath.Join(store.Root, "catalog", "runs", name)
		e, err := readEntry(path)
		if err != nil {
			t.Fatal(err)
		}
		e.PlanPath = filepath.Join(store.Root, "another-plan.json")
		b, err := json.Marshal(e)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, b, 0600); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := store.Inventory(); err == nil {
		t.Fatal("run claim referencing a different plan path was accepted")
	}
}

func TestCatalogRejectsNonCanonicalJournalPaths(t *testing.T) {
	root := t.TempDir()
	journalDir := filepath.Join(root, "journals")
	if err := os.Mkdir(journalDir, 0700); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(root, "journal-alias")
	if err := os.Symlink(journalDir, alias); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	actual := filepath.Join(journalDir, "run.journal")
	startJournal(t, actual, domain.RunID("run-one"), domain.PlanID("plan-one"))
	e := entry{
		SchemaVersion: schemaVersion, PlanID: "plan-one", RunID: "run-one",
		JournalPath: filepath.Join(alias, "run.journal"), PlanPath: filepath.Join(root, "plan.json"),
	}
	if _, err := readJournal(e); err == nil {
		t.Fatal("canonical path change not detected")
	}
}

func TestCatalogRejectsUnrecognizedJournalOperations(t *testing.T) {
	store, plan, id := fixturePlan(t, filepath.Join(t.TempDir(), "project"), "artifact")
	journalPath := filepath.Join(store.Root, "journal.bin")
	if err := store.ClaimRun(plan, journalPath, "run-one"); err != nil {
		t.Fatal(err)
	}
	w, err := journal.OpenWriter(journalPath, "run-one")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range []journal.Entry{
		{RunID: "run-one", PlanID: id, Type: journal.EventRunStarted},
		{RunID: "run-one", Type: journal.EventOperationReady, OperationID: "unknown-operation", TargetID: "fake-target"},
	} {
		if _, err := w.Append(e); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Inventory(); err == nil {
		t.Fatal("invalid journal operation was accepted")
	}
}

func TestCatalogCompletedAndUncertainExecutionState(t *testing.T) {
	for _, tc := range []struct {
		name      string
		finish    bool
		wantState string
		reconcile bool
	}{
		{"published", true, "COMPLETED", false},
		{"ambiguous", false, "PENDING", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store, plan, id := fixturePlan(t, filepath.Join(t.TempDir(), "project"), "artifact")
			journalPath := filepath.Join(store.Root, "journal.bin")
			if err := store.ClaimRun(plan, journalPath, "run-one"); err != nil {
				t.Fatal(err)
			}
			w, err := journal.OpenWriter(journalPath, "run-one")
			if err != nil {
				t.Fatal(err)
			}
			appendEvent := func(e journal.Entry) {
				t.Helper()
				if _, err := w.Append(e); err != nil {
					t.Fatal(err)
				}
			}
			persisted, err := planner.Load(plan)
			if err != nil { t.Fatal(err) }
			operations := persisted.Plan().Operations()
			if len(operations) != 1 { t.Fatalf("unexpected operation count: %d", len(operations)) }
			op := operations[0].ID()
			target := operations[0].TargetID()
			appendEvent(journal.Entry{RunID: "run-one", PlanID: id, Type: journal.EventRunStarted})
			appendEvent(journal.Entry{RunID: "run-one", Type: journal.EventAttemptStarted, OperationID: op, TargetID: target, Payload: journal.Payload{Attempt: 1, AttemptReason: journal.AttemptReasonInitial}})
			appendEvent(journal.Entry{RunID: "run-one", Type: journal.EventProviderProcessStarted, OperationID: op, TargetID: target, Payload: journal.Payload{Attempt: 1}})
			appendEvent(journal.Entry{RunID: "run-one", Type: journal.EventSideEffectDispatched, OperationID: op, TargetID: target, Payload: journal.Payload{Attempt: 1}})
			if tc.finish {
				appendEvent(journal.Entry{RunID: "run-one", Type: journal.EventProviderResponseReceived, OperationID: op, TargetID: target, Payload: journal.Payload{Attempt: 1}})
				appendEvent(journal.Entry{RunID: "run-one", Type: journal.EventOperationResult, OperationID: op, TargetID: target, Payload: journal.Payload{Attempt: 1, State: domain.StatePublished, ResultCategory: journal.ResultCategoryPublished}})
				appendEvent(journal.Entry{RunID: "run-one", Type: journal.EventRunCompleted})
			} else {
				appendEvent(journal.Entry{RunID: "run-one", Type: journal.EventOutcomeAmbiguous, OperationID: op, TargetID: target, Payload: journal.Payload{Attempt: 1}})
			}
			if err := w.Close(); err != nil {
				t.Fatal(err)
			}
			records, err := store.Inventory()
			if err != nil || len(records) != 1 {
				t.Fatalf("replay failed: %+v %v", records, err)
			}
			if records[0].State != tc.wantState || records[0].Completed != tc.finish || records[0].ReconcileRequired != tc.reconcile {
				t.Fatalf("run replay state mismatch: %+v", records[0])
			}
		})
	}
}

func TestCatalogPermissionErrorsFailClosed(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX directory permissions only")
	}
	t.Run("unreadable project state", func(t *testing.T) {
		store, _, _ := fixturePlan(t, filepath.Join(t.TempDir(), "project"), "artifact")
		if err := os.Chmod(store.Root, 0000); err != nil {
			t.Fatal(err)
		}
		defer os.Chmod(store.Root, 0700)
		if _, err := store.Inventory(); err == nil {
			t.Fatal("inaccessible state root accepted")
		}
	})
	t.Run("unwritable catalog root", func(t *testing.T) {
		store, _, _ := fixturePlan(t, filepath.Join(t.TempDir(), "project"), "artifact")
		marker := filepath.Join(store.Root, ".catalog-required")
		if err := os.Remove(marker); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(store.Root, 0500); err != nil {
			t.Fatal(err)
		}
		defer os.Chmod(store.Root, 0700)
		if err := store.Ensure(); err == nil {
			t.Fatal("unwritable state root accepted for writes")
		}
	})
	t.Run("missing catalog under nonwritable root", func(t *testing.T) {
		store, _, _ := fixturePlan(t, filepath.Join(t.TempDir(), "project"), "artifact")
		if err := os.RemoveAll(filepath.Join(store.Root, "catalog")); err != nil {
			t.Fatal(err)
		}
		if err := os.Remove(filepath.Join(store.Root, ".catalog-required")); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(store.Root, 0500); err != nil {
			t.Fatal(err)
		}
		defer os.Chmod(store.Root, 0700)
		if err := store.Ensure(); err == nil {
			t.Fatal("catalog replaced despite inaccessible state root")
		}
	})
	t.Run("unreadable plans while catalog absent", func(t *testing.T) {
		store, _, _ := fixturePlan(t, filepath.Join(t.TempDir(), "project"), "artifact")
		if err := os.RemoveAll(filepath.Join(store.Root, "catalog")); err != nil {
			t.Fatal(err)
		}
		if err := os.Remove(filepath.Join(store.Root, ".catalog-required")); err != nil {
			t.Fatal(err)
		}
		plans := filepath.Join(store.Root, "plans")
		if err := os.Chmod(plans, 0000); err != nil {
			t.Fatal(err)
		}
		defer os.Chmod(plans, 0700)
		if err := store.Ensure(); err == nil {
			t.Fatal("unreadable historical plans ignored")
		}
	})
}

func TestRunstateErrorsPreserveIdentity(t *testing.T) {
	store, plan, id := fixturePlan(t, filepath.Join(t.TempDir(), "project"), "artifact")
	dir, err := store.directory(false)
	if err != nil {
		t.Fatal(err)
	}
	_, exists, err := store.claimEntry(dir, id)
	if err != nil || exists {
		t.Fatalf("unexpected claim: exists=%t err=%v", exists, err)
	}
	if _, err := store.Select("nonexistent", ""); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("wrong error classification: %v", err)
	}
	if err := store.ClaimRun(plan, filepath.Join(store.Root, "run.journal"), "run-one"); err != nil {
		t.Fatal(err)
	}
	if err := store.ClaimRun(plan, filepath.Join(store.Root, "other.journal"), "run-one"); !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate plan identity not blocked: %v", err)
	}
}
