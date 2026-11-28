package runstate

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

func withExclusiveCatalogFile(t *testing.T, path string, check func()) {
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

func TestWindowsCatalogSharingViolationsPreserveOriginalRun(t *testing.T) {
	store, plan, id := fixturePlan(t, filepath.Join(t.TempDir(), "project"), "release bytes")
	journalPath := filepath.Join(store.Root, "original.journal")
	if err := store.ClaimRun(plan, journalPath, "run-original"); err != nil {
		t.Fatal(err)
	}
	startJournal(t, journalPath, "run-original", id)
	original, err := os.ReadFile(journalPath)
	if err != nil {
		t.Fatal(err)
	}
	planEntry := filepath.Join(store.Root, "catalog", "plans", key(string(id)))
	primary := filepath.Join(store.Root, "catalog", "runs", key(string(id)))
	backup := primary + ".backup"
	alternate := filepath.Join(store.Root, "alternate.journal")
	for _, path := range []string{plan, planEntry, journalPath} {
		t.Run(filepath.Base(path), func(t *testing.T) {
			withExclusiveCatalogFile(t, path, func() {
				if _, err := store.Inventory(); err == nil {
					t.Fatal("inventory ignored sharing violation")
				}
				if err := store.ClaimRun(plan, alternate, "run-alternate"); err == nil {
					t.Fatal("sharing violation authorized an alternate run")
				}
			})
		})
	}
	withExclusiveCatalogFile(t, primary, func() {
		records, err := store.Inventory()
		if err != nil || len(records) != 1 || records[0].RunID != "run-original" {
			t.Fatalf("valid backup did not preserve original run: %+v %v", records, err)
		}
		withExclusiveCatalogFile(t, backup, func() {
			if _, err := store.Inventory(); err == nil {
				t.Fatal("both inaccessible claims were accepted")
			}
			if err := store.ClaimRun(plan, alternate, "run-alternate"); err == nil {
				t.Fatal("inaccessible claims allowed a replacement run")
			}
		})
	})
	if _, err := os.Stat(alternate); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("unexpected alternate journal: %v", err)
	}
	if err := store.ClaimRun(plan, journalPath, "run-original"); err != nil {
		t.Fatalf("original run did not recover: %v", err)
	}
	if data, err := os.ReadFile(journalPath); err != nil || string(data) != string(original) {
		t.Fatalf("original journal changed: %v", err)
	}
}
