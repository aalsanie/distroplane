package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aalsanie/distroplane/internal/domain"
	"github.com/aalsanie/distroplane/internal/journal"
	"github.com/aalsanie/distroplane/internal/planner"
)

func TestCLIProviderPhaseTimeouts(t *testing.T) {
	binaries := t.TempDir()
	cli := filepath.Join(binaries, executableName("distroplane"))
	provider := filepath.Join(binaries, executableName("distroplane-provider-fake"))
	for _, build := range []struct{ path, pkg string }{
		{cli, "."},
		{provider, "./testdata/timeout-provider"},
	} {
		if out, err := exec.Command("go", "build", "-o", build.path, build.pkg).CombinedOutput(); err != nil {
			t.Fatalf("build %s: %v\n%s", build.pkg, err, out)
		}
	}
	for _, phase := range []string{"cancel-planning", "describe", "plan", "apply-reconcile"} {
		t.Run(phase, func(t *testing.T) {
			if phase != "cancel-planning" {
				t.Parallel()
			}
			root := t.TempDir()
			cfg := filepath.Join(root, "distroplane.json")
			raw := "{\"schemaVersion\":\"1\",\"release\":{\"id\":\"release-timeout\",\"artifacts\":[{\"name\":\"app\",\"source\":\"app.bin\"}]},\"targets\":[{\"id\":\"primary\",\"provider\":{\"name\":\"fake\",\"executable\":" + jsonString(provider) + "},\"configuration\":{\"mode\":\"waiting_external\",\"reconcileState\":\"published\"}}]}"
			if err := os.WriteFile(cfg, []byte(raw), 0600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(root, "app.bin"), []byte("artifact"), 0600); err != nil {
				t.Fatal(err)
			}
			modePath := filepath.Join(root, "provider-mode")
			setMode := func(mode string) {
				t.Helper()
				if err := os.WriteFile(modePath, []byte(mode), 0600); err != nil {
					t.Fatal(err)
				}
			}
			run := func(limit time.Duration, want int, args ...string) (string, string) {
				t.Helper()
				ctx, cancel := context.WithTimeout(context.Background(), limit)
				defer cancel()
				command := exec.CommandContext(ctx, cli, append(args, "--config", cfg, "--json")...)
				command.Env = append(os.Environ(), "TMPDIR="+root, "TMP="+root, "TEMP="+root)
				command.WaitDelay = 3 * time.Second
				var stdout, stderr bytes.Buffer
				command.Stdout, command.Stderr = &stdout, &stderr
				err := command.Run()
				if ctx.Err() != nil {
					t.Fatalf("CLI exceeded watchdog %s: %v", limit, err)
				}
				code := 0
				if err != nil {
					var exitErr *exec.ExitError
					if !errors.As(err, &exitErr) {
						t.Fatal(err)
					}
					code = exitErr.ExitCode()
				}
				if code != want {
					t.Fatalf("code=%d want=%d stdout=%s stderr=%s", code, want, stdout.String(), stderr.String())
				}
				return stdout.String(), stderr.String()
			}
			calls := func() string {
				t.Helper()
				raw, err := os.ReadFile(filepath.Join(root, "provider-calls"))
				if err != nil {
					t.Fatal(err)
				}
				return string(raw)
			}
			if phase == "cancel-planning" {
				t.Setenv("TMPDIR", root)
				t.Setenv("TMP", root)
				t.Setenv("TEMP", root)
				setMode("plan")
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				var stdout, stderr bytes.Buffer
				done := make(chan int, 1)
				go func() {
					done <- executeContext(ctx, []string{"plan", "--config", cfg, "--json"}, &stdout, &stderr)
				}()
				deadline := time.NewTimer(15 * time.Second)
				defer deadline.Stop()
				ticker := time.NewTicker(10 * time.Millisecond)
				defer ticker.Stop()
				for {
					raw, _ := os.ReadFile(filepath.Join(root, "provider-calls"))
					if strings.Contains(string(raw), "plan\n") {
						break
					}
					select {
					case code := <-done:
						t.Fatalf("planning exited before cancellation: %d %s %s", code, stdout.String(), stderr.String())
					case <-deadline.C:
						cancel()
						select {
						case <-done:
						case <-time.After(8 * time.Second):
						}
						t.Fatal("planning provider never started")
					case <-ticker.C:
					}
				}
				cancel()
				select {
				case code := <-done:
					var failure errorOutput
					if code != exitOperational || stdout.Len() != 0 || json.Unmarshal(stderr.Bytes(), &failure) != nil ||
						failure.Error.Kind != "plan_failed" || !strings.Contains(failure.Error.Message, "context canceled") {
						t.Fatalf("cancelled planning: %d %s %s", code, stdout.String(), stderr.String())
					}
				case <-time.After(8 * time.Second):
					t.Fatal("planning cancellation did not terminate the provider")
				}
				if _, err := os.Stat(filepath.Join(root, ".distroplane")); !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("cancelled planning persisted release state: %v", err)
				}
				setMode("")
				out, diagnostics := run(30*time.Second, exitOK, "plan")
				var plan planOutput
				if diagnostics != "" || json.Unmarshal([]byte(out), &plan) != nil || plan.PlanID == "" {
					t.Fatalf("planning after cancellation: %s %s", out, diagnostics)
				}
				return
			}

			if phase == "describe" || phase == "plan" {
				setMode(phase)
				limit := 30 * time.Second
				if phase == "plan" {
					limit = 80 * time.Second
				}
				stdout, stderr := run(limit, exitOperational, "plan")
				var failure errorOutput
				if stdout != "" || json.Unmarshal([]byte(stderr), &failure) != nil || failure.Error.Kind != "plan_failed" ||
					!strings.Contains(failure.Error.Message, "context deadline exceeded") {
					t.Fatalf("unexpected timeout diagnostic: stdout=%s stderr=%s", stdout, stderr)
				}
				if !strings.Contains(calls(), phase+"\n") || strings.Contains(calls(), "apply\n") {
					t.Fatalf("wrong provider calls: %q", calls())
				}
				if _, err := os.Stat(filepath.Join(root, ".distroplane")); !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("timed-out planning persisted release state: %v", err)
				}
				setMode("")
				stdout, stderr = run(30*time.Second, exitOK, "plan")
				var plan planOutput
				if stderr != "" || json.Unmarshal([]byte(stdout), &plan) != nil || plan.PlanID == "" {
					t.Fatalf("planning after timeout failed: %s %s", stdout, stderr)
				}
				return
			}

			setMode("apply reconcile")
			stdout, stderr := run(45*time.Second, exitPending, "publish", "--yes")
			var result publishOutput
			if stderr != "" || json.Unmarshal([]byte(stdout), &result) != nil || result.Run == nil ||
				result.Run.Completed || len(result.Run.Operations) != 1 {
				t.Fatalf("unexpected publication outcome: %s %s", stdout, stderr)
			}
			operation := result.Run.Operations[0]
			if operation.Attempt != 1 || !operation.ReconcileRequired || !operation.Ambiguous || operation.ErrorCode != "TIMEOUT" {
				t.Fatalf("timeout was not retained as uncertain: %+v", operation)
			}
			persisted, err := planner.Load(result.Plan.Path)
			if err != nil {
				t.Fatal(err)
			}
			read, err := readJournal(result.JournalPath)
			if err != nil {
				t.Fatal(err)
			}
			dispatches, ambiguous, observations := 0, 0, 0
			for i, event := range read.Events {
				if _, err := journal.Reduce(persisted.Plan(), read.Events[:i+1]); err != nil {
					t.Fatalf("journal prefix %d: %v", i+1, err)
				}
				switch event.Type {
				case journal.EventSideEffectDispatched:
					dispatches++
				case journal.EventOutcomeAmbiguous:
					if event.Payload.ErrorCode == "TIMEOUT" {
						ambiguous++
					}
				case journal.EventReconcileResult:
					if event.Payload.ErrorCode == "TIMEOUT" {
						observations++
					}
				}
			}
			if dispatches != 1 || ambiguous != 1 || observations != 1 ||
				strings.Count(calls(), "apply\n") != 1 || strings.Count(calls(), "reconcile\n") != 1 {
				t.Fatalf("dispatch=%d ambiguous=%d observations=%d calls=%q", dispatches, ambiguous, observations, calls())
			}
			setMode("")
			stdout, stderr = run(30*time.Second, exitOK, "resume", "--reconcile-only")
			var recovered stateOutput
			if stderr != "" || json.Unmarshal([]byte(stdout), &recovered) != nil || !recovered.Completed ||
				recovered.RunID != result.Run.RunID || recovered.PlanID != result.Run.PlanID ||
				len(recovered.Operations) != 1 || recovered.Operations[0].Attempt != 1 ||
				recovered.Operations[0].State != string(domain.StatePublished) ||
				strings.Count(calls(), "apply\n") != 1 || strings.Count(calls(), "reconcile\n") != 2 {
				t.Fatalf("recovery did not preserve the original run: %s %s calls=%q", stdout, stderr, calls())
			}
		})
	}
}
