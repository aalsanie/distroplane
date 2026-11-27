package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aalsanie/distroplane/internal/planner"
	"github.com/aalsanie/distroplane/internal/runstate"
)

type publishReaderFunc func([]byte) (int, error)

func (f publishReaderFunc) Read(p []byte) (int, error) {
	return f(p)
}

func TestPublishFailsClosedOnMissingAndUnreadableApproval(t *testing.T) {
	dir, path := publishFixture(t, `{"mode":"published"}`)
	var stdout, stderr bytes.Buffer
	args := []string{"--config", path}
	if code := runPublishContext(context.Background(), args, nil, &stdout, &stderr); code != exitUsage {
		t.Fatalf("missing reader code=%d stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "approval_required") {
		t.Fatalf("missing approval error: %s", stderr.String())
	}
	stdout.Reset()
	stderr.Reset()
	unreadable := publishReaderFunc(func([]byte) (int, error) {
		return 0, errors.New("reader unavailable")
	})
	if code := runPublishContext(context.Background(), args, unreadable, &stdout, &stderr); code != exitOperational {
		t.Fatalf("unreadable approval code=%d stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "approval_failed") {
		t.Fatalf("missing read failure: %s", stderr.String())
	}
	record, err := (runstate.Store{Root: filepath.Join(dir, ".distroplane")}).Select("release-publish", "")
	if err != nil || record.RunID != "" {
		t.Fatalf("failed approval reserved a run: %+v, %v", record, err)
	}
}

func TestPublishCancellationAfterApprovalNeverDispatches(t *testing.T) {
	dir, path := publishFixture(t, `{"mode":"published"}`)
	var out, stderr bytes.Buffer
	if code := runPublishContext(context.Background(), []string{"--config", path, "--dry-run", "--json"}, nil, &out, &stderr); code != exitOK {
		t.Fatalf("preview code=%d stderr=%s", code, stderr.String())
	}
	var preview publishOutput
	if err := json.Unmarshal(out.Bytes(), &preview); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	approval := strings.NewReader("publish " + preview.Plan.PlanID + "\n")
	reader := publishReaderFunc(func(p []byte) (int, error) {
		n, err := approval.Read(p)
		cancel()
		return n, err
	})
	out.Reset()
	stderr.Reset()
	if code := runPublishContext(ctx, []string{"--config", path}, reader, &out, &stderr); code != exitOperational {
		t.Fatalf("cancellation code=%d stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "cancelled") {
		t.Fatalf("cancellation not reported: %s", stderr.String())
	}
	record, err := (runstate.Store{Root: filepath.Join(dir, ".distroplane")}).Select("release-publish", "")
	if err != nil || record.RunID != "" {
		t.Fatalf("canceled confirmation claimed a run: %+v, %v", record, err)
	}
}

func TestPublishRefusesExactApprovalAtEOF(t *testing.T) {
	_, path := publishFixture(t, `{"mode":"published"}`)
	var out, diagnostic bytes.Buffer
	if code := runPublishContext(context.Background(), []string{"--config", path, "--dry-run", "--json"}, nil, &out, &diagnostic); code != exitOK {
		t.Fatalf("preview code=%d", code)
	}
	var preview publishOutput
	if err := json.Unmarshal(out.Bytes(), &preview); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	diagnostic.Reset()
	approval := strings.NewReader("publish " + preview.Plan.PlanID)
	if code := runPublishContext(context.Background(), []string{"--config", path}, approval, &out, &diagnostic); code != exitInvalid {
		t.Fatalf("EOF approval code=%d stderr=%s", code, diagnostic.String())
	}
}

func TestPublishReaderFailureDoesNotLeakInputText(t *testing.T) {
	_, path := publishFixture(t, `{"mode":"published"}`)
	const secret = "untrusted-reader-input-secret"
	reader := publishReaderFunc(func(p []byte) (int, error) {
		copy(p, secret)
		return 0, io.ErrUnexpectedEOF
	})
	var out, stderr bytes.Buffer
	code := runPublishContext(context.Background(), []string{"--config", path}, reader, &out, &stderr)
	if code != exitOperational || strings.Contains(out.String()+stderr.String(), secret) {
		t.Fatalf("reader error not safely handled: code=%d stderr=%s", code, stderr.String())
	}
}

func TestPublishApprovalRefusesChangesAfterPreview(t *testing.T) {
	for _, mutation := range []string{"artifact", "config", "plan source"} {
		t.Run(mutation, func(t *testing.T) {
			dir, path := publishFixture(t, `{"mode":"published"}`)
			raw, _ := recoveryCall(t, exitOK, "publish", "--config", path, "--dry-run", "--json")
			var preview publishOutput
			if err := json.Unmarshal(raw, &preview); err != nil {
				t.Fatal(err)
			}
			if len(preview.Review) != 1 || !preview.Review[0].Limited {
				t.Fatalf("summary-less provider not identified: %+v", preview.Review)
			}
			approval := "publish " + preview.Plan.PlanID + "\n"
			var out, diagnostic bytes.Buffer
			reader := publishReaderFunc(func(p []byte) (int, error) {
				switch mutation {
				case "artifact":
					updateTestWrite(t, filepath.Join(dir, "app.bin"), []byte("other bytes"))
				case "config":
					current := updateTestRead(t, path)
					updateTestWrite(t, path, bytes.Replace(current, []byte(`"mode":"published"`), []byte(`"mode":"waiting_external"`), 1))
				case "plan source":
					original := filepath.Join(dir, "app.bin")
					alternate := filepath.Join(dir, "alternate.bin")
					updateTestWrite(t, alternate, updateTestRead(t, original))
					current := updateTestRead(t, preview.Plan.Path)
					changed := bytes.Replace(current, []byte(jsonString(original)), []byte(jsonString(alternate)), 1)
					if bytes.Equal(current, changed) {
						t.Fatal("fixture source was not replaced")
					}
					updateTestWrite(t, preview.Plan.Path, changed)
					loaded, err := planner.Load(preview.Plan.Path)
					if err != nil || string(loaded.ID()) != preview.Plan.PlanID {
						t.Fatalf("source relocation should retain semantic identity: %v", err)
					}
				}
				return copy(p, approval), nil
			})
			if code := runPublishContext(context.Background(), []string{"--config", path}, reader, &out, &diagnostic); code != exitInvalid {
				t.Fatalf("mutated approval code=%d stdout=%s stderr=%s", code, out.String(), diagnostic.String())
			}
			want := "approval_mismatch"
			if mutation == "artifact" {
				want = "artifact_changed"
			}
			if !strings.Contains(diagnostic.String(), want) {
				t.Fatalf("wrong refusal: %s", diagnostic.String())
			}
			records, err := (runstate.Store{Root: filepath.Join(dir, ".distroplane")}).Inventory()
			if err != nil || len(records) != 1 || records[0].RunID != "" {
				t.Fatalf("refused preview claimed a run: %+v %v", records, err)
			}
		})
	}
}

func TestPublishCancellationWhileWaitingForApproval(t *testing.T) {
	dir, path := publishFixture(t, `{"mode":"published"}`)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	blocked := initBlockedInput{started: make(chan struct{}), release: make(chan struct{})}
	defer close(blocked.release)
	done := make(chan int, 1)
	var diagnostic bytes.Buffer
	go func() {
		done <- runPublishContext(ctx, []string{"--config", path}, blocked, io.Discard, &diagnostic)
	}()
	select {
	case <-blocked.started:
	case <-time.After(15 * time.Second):
		t.Fatal("approval prompt was not reached")
	}
	cancel()
	select {
	case code := <-done:
		if code != exitOperational || !strings.Contains(diagnostic.String(), "cancelled") {
			t.Fatalf("cancelled approval code=%d stderr=%s", code, diagnostic.String())
		}
	case <-time.After(5 * time.Second):
		t.Fatal("approval read remained blocked")
	}
	record, err := (runstate.Store{Root: filepath.Join(dir, ".distroplane")}).Select("release-publish", "")
	if err != nil || record.RunID != "" {
		t.Fatalf("cancelled approval reserved a run: %+v %v", record, err)
	}
}

func TestPublishPreviewShowsActualNPMIdentityWithoutDispatch(t *testing.T) {
	dir := t.TempDir()
	provider := filepath.Join(dir, executableName("distroplane-provider-npm"))
	if output, err := exec.Command("go", "build", "-o", provider, "../distroplane-provider-npm").CombinedOutput(); err != nil {
		t.Fatalf("build provider: %v %s", err, output)
	}
	archive := filepath.Join(dir, "release.tgz")
	writeUpdateNPMFixture(t, archive, "@scope/sample", "2.3.4")
	path := filepath.Join(dir, "distroplane.json")
	raw := `{"schemaVersion":"2","release":{"id":"build-42","artifacts":{"package":{"source":"release.tgz"}}},"destinations":{"npm":{"artifact":"package","executable":` + jsonString(provider) + `,"options":{"packagePath":"release.tgz","registry":"https://registry.example.invalid/","authentication":{"mode":"token","credential":"release-token"}}}}}`
	updateTestWrite(t, path, []byte(raw))
	t.Setenv("DISTROPLANE_NPM_TOKEN", "secret-preview-canary")
	var out, diagnostic bytes.Buffer
	args := []string{"--config", path, "--dry-run"}
	if code := runPublishContext(context.Background(), args, nil, &out, &diagnostic); code != exitOK {
		t.Fatalf("preview code=%d stderr=%s", code, diagnostic.String())
	}
	for _, want := range []string{"Release build-42", "Registry: https://registry.example.invalid/", "Package: @scope/sample", "Version: 2.3.4", "Action: Publish package version", "SHA"} {
		if want == "SHA" {
			want = "sha256:"
		}
		if !strings.Contains(diagnostic.String(), want) {
			t.Fatalf("preview lacks %q: %s", want, diagnostic.String())
		}
	}
	if strings.Contains(out.String()+diagnostic.String(), "secret-preview-canary") || !strings.Contains(out.String(), "no publication was authorized") {
		t.Fatal("preview leaked credential or implied publication")
	}
	result, _ := recoveryCall(t, exitOK, "publish", "--config", path, "--dry-run", "--json")
	var preview publishOutput
	if err := json.Unmarshal(result, &preview); err != nil || len(preview.Review) != 1 || preview.Review[0].Limited || len(preview.Review[0].Fields) == 0 {
		t.Fatalf("JSON preview missing review: %s %v", result, err)
	}
	plan, err := planner.Load(preview.Plan.Path)
	if err != nil || len(plan.ReviewFields("npm")) != len(preview.Review[0].Fields) {
		t.Fatalf("review not retained: %v", err)
	}
	for i, field := range plan.ReviewFields("npm") {
		if preview.Review[0].Fields[i] != field {
			t.Fatal("JSON review differs from saved plan")
		}
	}
	if _, err := os.Stat(filepath.Join(dir, ".distroplane", "runs", preview.Plan.PlanID+".journal")); !os.IsNotExist(err) {
		t.Fatalf("preview created a journal: %v", err)
	}
}
