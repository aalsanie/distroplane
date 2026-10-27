package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"path/filepath"
	"strings"
	"testing"

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

func TestPublishAcceptsExactApprovalAtEOF(t *testing.T) {
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
	if code := runPublishContext(context.Background(), []string{"--config", path}, approval, &out, &diagnostic); code != exitOK {
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
