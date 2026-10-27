package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aalsanie/distroplane/internal/domain"
	"github.com/aalsanie/distroplane/internal/journal"
	"github.com/aalsanie/distroplane/internal/runstate"
)

func publishFixture(t *testing.T, configuration string) (string, string) {
	t.Helper()
	dir := t.TempDir()
	provider := copyExecutable(t, buildFakeProvider(t), filepath.Join(dir, executableName("distroplane-provider-fake")))
	if err := os.WriteFile(filepath.Join(dir, "app.bin"), []byte("first-artifact"), 0o600); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "distroplane.json")
	raw := `{"schemaVersion":"1","release":{"id":"release-publish","artifacts":[{"name":"app","source":"app.bin"}]},"targets":[{"id":"primary","provider":{"name":"fake","executable":` + jsonString(provider) + `},"configuration":` + configuration + `}]}`
	if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir, path
}

func TestPublishExplicitApprovalAndNoDuplicateRun(t *testing.T) {
	dir, path := publishFixture(t, `{"mode":"published"}`)
	var stdout, stderr bytes.Buffer
	args := []string{"--config", path}
	if code := runPublishContext(context.Background(), args, strings.NewReader("no\n"), &stdout, &stderr); code != exitInvalid {
		t.Fatalf("unapproved publish code=%d stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "publish ") || !strings.Contains(stderr.String(), "approval_denied") {
		t.Fatalf("preview and approval prompt missing: %s", stderr.String())
	}
	store := runstate.Store{Root: filepath.Join(dir, ".distroplane")}
	records, err := store.Inventory()
	if err != nil || len(records) != 1 || records[0].RunID != "" {
		t.Fatalf("unapproved plan must not claim a run: %+v %v", records, err)
	}
	stdout.Reset()
	stderr.Reset()
	if code := runPublishContext(context.Background(), []string{"--config", path, "--yes", "--json"}, nil, &stdout, &stderr); code != exitOK {
		t.Fatalf("approved publish code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	var result publishOutput
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if !result.Approved || result.Run == nil || !result.Run.Completed || result.Run.RunID == "" || result.Run.PlanID != result.Plan.PlanID {
		t.Fatalf("invalid publication result: %+v", result)
	}
	if !strings.HasPrefix(result.JournalPath, filepath.Join(dir, ".distroplane", "runs")) {
		t.Fatalf("unexpected journal path: %q", result.JournalPath)
	}
	read, err := readJournal(result.JournalPath)
	if err != nil {
		t.Fatal(err)
	}
	dispatches := 0
	for _, event := range read.Events {
		if event.Type == journal.EventSideEffectDispatched {
			dispatches++
		}
	}
	if dispatches != 1 {
		t.Fatalf("dispatches=%d", dispatches)
	}
	stdout.Reset()
	stderr.Reset()
	if code := runPublishContext(context.Background(), []string{"--config", path, "--yes", "--json"}, nil, &stdout, &stderr); code != exitInvalid {
		t.Fatalf("second publish code=%d output=%s err=%s", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stderr.String(), "run_already_exists") {
		t.Fatalf("existing run not rejected: %s", stderr.String())
	}
	read, err = readJournal(result.JournalPath)
	if err != nil {
		t.Fatal(err)
	}
	dispatches = 0
	for _, event := range read.Events {
		if event.Type == journal.EventSideEffectDispatched {
			dispatches++
		}
	}
	if dispatches != 1 {
		t.Fatalf("duplicate publish dispatched=%d", dispatches)
	}
}

func TestPublishInteractivePlanConfirmation(t *testing.T) {
	_, path := publishFixture(t, `{"mode":"published"}`)
	var out, diagnostic bytes.Buffer
	if code := runPlanContext(context.Background(), []string{"--config", path, "--json"}, &out, &diagnostic); code != exitOK {
		t.Fatalf("plan: code=%d err=%s", code, diagnostic.String())
	}
	var plan planOutput
	if err := json.Unmarshal(out.Bytes(), &plan); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	diagnostic.Reset()
	approval := "publish " + plan.PlanID + "\n"
	if code := runPublishContext(context.Background(), []string{"--config", path}, strings.NewReader(approval), &out, &diagnostic); code != exitOK {
		t.Fatalf("interactive code=%d out=%s err=%s", code, out.String(), diagnostic.String())
	}
	if !strings.Contains(out.String(), "completed true") || !strings.Contains(diagnostic.String(), approval[:len(approval)-1]) {
		t.Fatalf("missing execution result or preview: out=%s err=%s", out.String(), diagnostic.String())
	}
}

func TestPublishPreviewAndConfirmationConstraints(t *testing.T) {
	dir, path := publishFixture(t, `{"mode":"published"}`)
	var out, diagnostic bytes.Buffer
	if code := runPublishContext(context.Background(), []string{"--config", path, "--json"}, nil, &out, &diagnostic); code != exitUsage {
		t.Fatalf("JSON execution without approval code=%d", code)
	}
	if _, err := os.Stat(filepath.Join(dir, ".distroplane")); !os.IsNotExist(err) {
		t.Fatalf("unapproved noninteractive execution created state: %v", err)
	}
	invalid := [][]string{
		{"--config", path, "--concurrency", "-1"},
		{"--config", path, "--dry-run", "--yes"},
		{"--config", path, "--dry-run", "--confirm-plan", "some-id"},
	}
	for _, args := range invalid {
		out.Reset()
		diagnostic.Reset()
		if code := runPublishContext(context.Background(), args, nil, &out, &diagnostic); code != exitUsage {
			t.Fatalf("args=%v code=%d stderr=%s", args, code, diagnostic.String())
		}
	}
	out.Reset()
	diagnostic.Reset()
	if code := runPublishContext(context.Background(), []string{"--config", path, "--dry-run", "--json"}, nil, &out, &diagnostic); code != exitOK {
		t.Fatalf("preview code=%d stderr=%s", code, diagnostic.String())
	}
	var preview publishOutput
	if err := json.Unmarshal(out.Bytes(), &preview); err != nil {
		t.Fatal(err)
	}
	if preview.Approved || preview.Run != nil || preview.Plan.PlanID == "" || preview.JournalPath != "" {
		t.Fatalf("unexpected dry-run: %+v", preview)
	}
	records, err := (runstate.Store{Root: filepath.Join(dir, ".distroplane")}).Inventory()
	if err != nil || len(records) != 1 || records[0].RunID != "" {
		t.Fatalf("dry-run claimed run: %+v %v", records, err)
	}
	out.Reset()
	diagnostic.Reset()
	if code := runPublishContext(context.Background(), []string{"--config", path, "--confirm-plan", "wrong", "--json"}, nil, &out, &diagnostic); code != exitInvalid {
		t.Fatalf("wrong ID code=%d", code)
	}
	if !strings.Contains(diagnostic.String(), "approval_mismatch") {
		t.Fatalf("wrong plan ID not rejected: %s", diagnostic.String())
	}
	out.Reset()
	diagnostic.Reset()
	if code := runPublishContext(context.Background(), []string{"--config", path, "--confirm-plan", preview.Plan.PlanID, "--json"}, nil, &out, &diagnostic); code != exitOK {
		t.Fatalf("confirmed ID code=%d stderr=%s", code, diagnostic.String())
	}
}

func TestPublishPendingDoesNotStartAnotherRun(t *testing.T) {
	_, path := publishFixture(t, `{"mode":"waiting_external","reconcileState":"published"}`)
	var out, diagnostic bytes.Buffer
	if code := runPublishContext(context.Background(), []string{"--config", path, "--yes", "--json"}, nil, &out, &diagnostic); code != exitPending {
		t.Fatalf("pending code=%d stdout=%s stderr=%s", code, out.String(), diagnostic.String())
	}
	var result publishOutput
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Run == nil || result.Run.Completed {
		t.Fatalf("pending run was reported complete: %+v", result)
	}
	out.Reset()
	diagnostic.Reset()
	if code := runPublishContext(context.Background(), []string{"--config", path, "--yes"}, nil, &out, &diagnostic); code != exitInvalid {
		t.Fatalf("pending run republished: code=%d", code)
	}
	if !strings.Contains(diagnostic.String(), "run_already_exists") {
		t.Fatalf("no pending run diagnostic: %s", diagnostic.String())
	}
}

func TestPublishCredentialMappingsKeepSecretsOutOfState(t *testing.T) {
	_, path := publishFixture(t, `{"mode":"published","credential":"release"}`)
	const secret = "publish-secret-regression-canary"
	t.Setenv("PUBLISH_CREDENTIAL_CANARY", secret)
	var out, diagnostic bytes.Buffer
	args := []string{"--config", path, "--yes", "--credential", "release=PUBLISH_CREDENTIAL_CANARY", "--json"}
	if code := runPublishContext(context.Background(), args, nil, &out, &diagnostic); code != exitOK {
		t.Fatalf("credential code=%d stdout=%s stderr=%s", code, out.String(), diagnostic.String())
	}
	var result publishOutput
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Run == nil || !result.Run.Completed {
		t.Fatalf("credential publication incomplete: %+v", result)
	}
	for _, name := range []string{result.JournalPath, result.Plan.Path} {
		raw, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Contains(raw, []byte(secret)) {
			t.Fatalf("credential persisted in %s", name)
		}
	}
	if strings.Contains(out.String(), secret) || strings.Contains(diagnostic.String(), secret) {
		t.Fatal("credential leaked to terminal output")
	}
}

func TestPublishDifferentPlanForSameReleaseFailsClosed(t *testing.T) {
	dir, path := publishFixture(t, `{"mode":"published"}`)
	var out, diagnostic bytes.Buffer
	if code := runPublishContext(context.Background(), []string{"--config", path, "--yes", "--json"}, nil, &out, &diagnostic); code != exitOK {
		t.Fatalf("initial code=%d err=%s", code, diagnostic.String())
	}
	if err := os.WriteFile(filepath.Join(dir, "app.bin"), []byte("new-artifact"), 0o600); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	diagnostic.Reset()
	if code := runPublishContext(context.Background(), []string{"--config", path, "--yes", "--json"}, nil, &out, &diagnostic); code != exitInvalid {
		t.Fatalf("different plan code=%d stderr=%s", code, diagnostic.String())
	}
	if !strings.Contains(diagnostic.String(), "release_conflict") {
		t.Fatalf("different plan not blocked: %s", diagnostic.String())
	}
	records, err := (runstate.Store{Root: filepath.Join(dir, ".distroplane")}).Inventory()
	if err != nil || len(records) != 1 {
		t.Fatalf("rejected release polluted catalog: records=%+v err=%v", records, err)
	}
}

func TestPublishCancelledBeforePlanning(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, path := publishFixture(t, `{"mode":"published"}`)
	var out, diagnostic bytes.Buffer
	if code := runPublishContext(ctx, []string{"--config", path, "--yes"}, nil, &out, &diagnostic); code != exitOperational {
		t.Fatalf("cancelled code=%d err=%s", code, diagnostic.String())
	}
	if !strings.Contains(diagnostic.String(), "cancelled") {
		t.Fatalf("missing cancellation diagnostic: %s", diagnostic.String())
	}
}

func TestPublishUnknownMappingFailsBeforeDispatch(t *testing.T) {
	_, path := publishFixture(t, `{"mode":"published"}`)
	var out, diagnostic bytes.Buffer
	if code := runPublishContext(context.Background(), []string{"--config", path, "--yes", "--credential", "missing=SOME_TOKEN", "--json"}, nil, &out, &diagnostic); code != exitOperational {
		t.Fatalf("invalid mapping code=%d stdout=%s stderr=%s", code, out.String(), diagnostic.String())
	}
	if !strings.Contains(diagnostic.String(), "provider_binding_failed") {
		t.Fatalf("missing binding diagnostic: %s", diagnostic.String())
	}
}

func TestPublishOutputUsesPlanAndJournalIdentity(t *testing.T) {
	_, path := publishFixture(t, `{"mode":"published"}`)
	var out, diagnostic bytes.Buffer
	if code := runPublishContext(context.Background(), []string{"--config", path, "--yes", "--json"}, nil, &out, &diagnostic); code != exitOK {
		t.Fatalf("code=%d stderr=%s", code, diagnostic.String())
	}
	var result publishOutput
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.OutputSchemaVersion != outputSchemaVersion || !domain.PlanID(result.Plan.PlanID).Valid() {
		t.Fatalf("invalid plan JSON: %+v", result)
	}
	if result.Run == nil || result.Run.PlanID != result.Plan.PlanID {
		t.Fatalf("plan/run identity mismatch: %+v", result)
	}
}

func TestPublishConflictingPreviewDoesNotPoisonCatalog(t *testing.T) {
	dir, path := publishFixture(t, `{"mode":"published"}`)
	var out, diagnostic bytes.Buffer
	previewArgs := []string{"--config", path, "--dry-run", "--json"}
	if code := runPublishContext(context.Background(), previewArgs, nil, &out, &diagnostic); code != exitOK {
		t.Fatalf("initial preview code=%d stderr=%s", code, diagnostic.String())
	}
	var first publishOutput
	if err := json.Unmarshal(out.Bytes(), &first); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "app.bin"), []byte("changed-artifact"), 0o600); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	diagnostic.Reset()
	if code := runPublishContext(context.Background(), previewArgs, nil, &out, &diagnostic); code != exitInvalid {
		t.Fatalf("changed preview code=%d stderr=%s", code, diagnostic.String())
	}
	if !strings.Contains(diagnostic.String(), "release_conflict") {
		t.Fatalf("missing plan-conflict diagnostic: %s", diagnostic.String())
	}
	records, err := (runstate.Store{Root: filepath.Join(dir, ".distroplane")}).Inventory()
	if err != nil || len(records) != 1 || records[0].PlanID != first.Plan.PlanID || records[0].RunID != "" {
		t.Fatalf("conflicting preview corrupted catalog: records=%+v err=%v", records, err)
	}
	if err := os.WriteFile(filepath.Join(dir, "app.bin"), []byte("first-artifact"), 0o600); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	diagnostic.Reset()
	if code := runPublishContext(context.Background(), []string{"--config", path, "--yes", "--json"}, nil, &out, &diagnostic); code != exitOK {
		t.Fatalf("original plan cannot be published: code=%d stderr=%s", code, diagnostic.String())
	}
	var completed publishOutput
	if err := json.Unmarshal(out.Bytes(), &completed); err != nil {
		t.Fatal(err)
	}
	if !completed.Approved || completed.Run == nil || !completed.Run.Completed || completed.Plan.PlanID != first.Plan.PlanID {
		t.Fatalf("original publication invalid: %+v", completed)
	}
}
