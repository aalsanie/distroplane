package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aalsanie/distroplane/internal/planner"
	"github.com/aalsanie/distroplane/internal/providerretention"
)

func snapshotFixture(t *testing.T) (string, string, publishOutput) {
	t.Helper()
	dir, cfg := publishFixture(t, `{"mode":"waiting_external","reconcileState":"published"}`)
	var stdout, stderr bytes.Buffer
	if code := runPublishContext(context.Background(), []string{"--config", cfg, "--yes", "--json"}, nil, &stdout, &stderr); code != exitPending {
		t.Fatalf("pending publication code=%d stderr=%s stdout=%s", code, stderr.String(), stdout.String())
	}
	var published publishOutput
	if err := json.Unmarshal(stdout.Bytes(), &published); err != nil {
		t.Fatal(err)
	}
	return dir, cfg, published
}

func retainedFixturePath(t *testing.T, dir string, published publishOutput) string {
	t.Helper()
	plan, err := planner.Load(published.Plan.Path)
	if err != nil {
		t.Fatal(err)
	}
	targets := plan.Plan().Targets()
	if len(targets) != 1 {
		t.Fatalf("unexpected targets: %d", len(targets))
	}
	ref := targets[0].Provider()
	cache, _, err := providerretention.Resolve(filepath.Join(dir, ".distroplane"), ref, plan.ProviderDigests()[ref])
	if err != nil {
		t.Fatal(err)
	}
	return cache
}

func TestResumeAfterProviderUpgradeUsesOriginalImmutableBytes(t *testing.T) {
	dir, cfg, published := snapshotFixture(t)
	cache := retainedFixturePath(t, dir, published)
	original, err := os.ReadFile(cache)
	if err != nil {
		t.Fatal(err)
	}
	installed := filepath.Join(dir, executableName("distroplane-provider-fake"))
	if err := os.Remove(installed); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(installed, []byte("upgraded incompatible executable"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(installed); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	if code := runResumeContext(context.Background(), []string{"--config", cfg, "--reconcile-only", "--json"}, &stdout, &stderr); code != exitOK {
		t.Fatalf("resume after uninstall code=%d stderr=%s stdout=%s", code, stderr.String(), stdout.String())
	}
	var completed stateOutput
	if err := json.Unmarshal(stdout.Bytes(), &completed); err != nil || !completed.Completed || completed.RunID != published.Run.RunID || completed.PlanID != published.Plan.PlanID {
		t.Fatalf("resume altered release identity %+v %v", completed, err)
	}
	retained, err := os.ReadFile(cache)
	if err != nil || !bytes.Equal(original, retained) {
		t.Fatalf("immutable provider changed: %v", err)
	}
}

func TestMissingRetainedProviderBlocksResumeBeforeChangingJournal(t *testing.T) {
	dir, cfg, published := snapshotFixture(t)
	cache := retainedFixturePath(t, dir, published)
	if err := os.Remove(cache); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(published.JournalPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, flags := range [][]string{{"--config", cfg, "--json"}, {"--config", cfg, "--reconcile-only", "--json"}} {
		var stdout, stderr bytes.Buffer
		if code := runResumeContext(context.Background(), flags, &stdout, &stderr); code != exitOperational {
			t.Fatalf("missing exact provider fallback accepted: code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
		}
		if !strings.Contains(stderr.String(), "provider_binding_failed") || !strings.Contains(stderr.String(), "restore the exact provider binary") {
			t.Fatalf("missing actionable provider recovery: %s", stderr.String())
		}
		after, err := os.ReadFile(published.JournalPath)
		if err != nil || !bytes.Equal(before, after) {
			t.Fatalf("failed recovery changed original journal: %v", err)
		}
	}
}

func TestTamperedRetainedProviderNeverFallsBackToInstalledBinary(t *testing.T) {
	dir, cfg, published := snapshotFixture(t)
	cache := retainedFixturePath(t, dir, published)
	if err := os.Chmod(cache, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cache, []byte("modified retained binary"), 0700); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(published.JournalPath)
	if err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	if code := runResumeContext(context.Background(), []string{"--config", cfg, "--reconcile-only", "--json"}, &stdout, &stderr); code != exitOperational {
		t.Fatalf("tampered retained binary accepted: %d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stderr.String(), "provider_binding_failed") {
		t.Fatalf("missing retention diagnostic: %s", stderr.String())
	}
	after, err := os.ReadFile(published.JournalPath)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatalf("journal altered: %v", err)
	}
}

func TestMissingProviderAtPlanningNeverWritesPlan(t *testing.T) {
	dir, cfg := publishFixture(t, `{"mode":"published"}`)
	binary := filepath.Join(dir, executableName("distroplane-provider-fake"))
	if err := os.Remove(binary); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	if code := runPlanContext(context.Background(), []string{"--config", cfg, "--json"}, &stdout, &stderr); code != exitOperational {
		t.Fatalf("missing provider executable was accepted: %d, %s", code, stderr.String())
	}
	if _, err := os.Stat(filepath.Join(dir, ".distroplane", "plans")); err == nil {
		entries, readErr := os.ReadDir(filepath.Join(dir, ".distroplane", "plans"))
		if readErr != nil || len(entries) != 0 {
			t.Fatalf("missing provider persisted plan: %v %+v", readErr, entries)
		}
	}
}
