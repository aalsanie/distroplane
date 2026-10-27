package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aalsanie/distroplane/internal/domain"
	"github.com/aalsanie/distroplane/internal/journal"
)

func TestCLIUntrustedProviderRequiresExplicitCredentialSourceAndResumesBlockedRun(t *testing.T) {
	dir := t.TempDir()
	binary := copyExecutable(t, buildFakeProvider(t), filepath.Join(dir, executableName("distroplane-provider-npm")))
	if err := os.MkdirAll(filepath.Join(dir, "dist"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "dist", "app.tgz"), []byte("fixture-package"), 0o600); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(dir, "distroplane.json")
	rawConfig := `{"schemaVersion":"2","release":{"id":"release","artifacts":{"app":{"source":"dist/app.tgz"}}},"destinations":{"npm":{"artifact":"app","executable":` + jsonString(binary) + `,"options":{"packagePath":"dist/app.tgz","registry":"https://registry.npmjs.org/","authentication":{"mode":"token","credential":"npm-publish"}}}}}`
	if err := os.WriteFile(configPath, []byte(rawConfig), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("NPM_TOKEN", "ambient-token-must-not-go-to-custom-provider")
	var out, diagnostic bytes.Buffer
	if code := execute([]string{"plan", "--config", configPath, "--json"}, &out, &diagnostic); code != exitOK {
		t.Fatalf("plan code=%d stdout=%s stderr=%s", code, out.String(), diagnostic.String())
	}
	var planned planOutput
	if err := json.Unmarshal(out.Bytes(), &planned); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "run.journal")
	args := []string{"apply", "--config", configPath, "--plan", planned.Path, "--journal", path, "--run", "release-run", "--json"}
	out.Reset()
	diagnostic.Reset()
	if code := execute(args, &out, &diagnostic); code != exitPending {
		t.Fatalf("untrusted provider auto-received token: code=%d stdout=%s stderr=%s", code, out.String(), diagnostic.String())
	}
	var initial stateOutput
	if err := json.Unmarshal(out.Bytes(), &initial); err != nil {
		t.Fatal(err)
	}
	if len(initial.Targets) != 1 || initial.Targets[0].State != string(domain.StateBlocked) {
		t.Fatalf("expected blocked before dispatch: %+v", initial)
	}
	before, err := readJournal(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range before.Events {
		if event.Type == journal.EventSideEffectDispatched {
			t.Fatal("blocked publication was dispatched")
		}
	}
	const secret = "config-source-token-canary"
	t.Setenv("MY_NPM_TOKEN", secret)
	withSource := strings.Replace(rawConfig, `"destinations":`, `"credentialSources":{"npm-publish":"MY_NPM_TOKEN"},"destinations":`, 1)
	if err := os.WriteFile(configPath, []byte(withSource), 0o600); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	diagnostic.Reset()
	if code := execute(args, &out, &diagnostic); code != exitOK {
		t.Fatalf("resumed code=%d stdout=%s stderr=%s", code, out.String(), diagnostic.String())
	}
	var finished stateOutput
	if err := json.Unmarshal(out.Bytes(), &finished); err != nil {
		t.Fatal(err)
	}
	if !finished.Completed || finished.RunID != initial.RunID || finished.PlanID != initial.PlanID {
		t.Fatalf("run identity changed: %+v", finished)
	}
	rawJournal, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	rawPlan, err := os.ReadFile(planned.Path)
	if err != nil {
		t.Fatal(err)
	}
	for _, data := range [][]byte{rawJournal, rawPlan, out.Bytes(), diagnostic.Bytes()} {
		if strings.Contains(string(data), secret) || strings.Contains(string(data), "ambient-token-must-not-go-to-custom-provider") {
			t.Fatal("credential material persisted or printed")
		}
	}
	events, err := readJournal(path)
	if err != nil {
		t.Fatal(err)
	}
	dispatches := 0
	for _, event := range events.Events {
		if event.Type == journal.EventSideEffectDispatched {
			dispatches++
		}
	}
	if dispatches != 1 {
		t.Fatalf("dispatch count=%d, want one", dispatches)
	}
}
