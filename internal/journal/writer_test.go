package journal

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func TestWriterAppendDurabilityAndLocking(t *testing.T) {
	path := filepath.Join(t.TempDir(), "run.journal")
	w, err := OpenWriter(path, runID())
	if err != nil {
		t.Fatal(err)
	}
	w.clock = func() time.Time { return time.Unix(100, 0) }
	if _, err := OpenWriter(path, runID()); !errors.Is(err, ErrWriterLocked) {
		t.Fatalf("second writer err=%v", err)
	}
	first, err := w.Append(Entry{RunID: runID(), Type: EventRunStarted})
	if err != nil {
		t.Fatal(err)
	}
	if first.Sequence != 1 || !first.ObservedAt.Equal(time.Unix(100, 0).UTC()) {
		t.Fatalf("first=%+v", first)
	}
	second, err := w.Append(Entry{RunID: runID(), Type: EventAttemptStarted, OperationID: opID("op-a"), TargetID: targetID("target-a"), Payload: Payload{Attempt: 1}})
	if err != nil {
		t.Fatal(err)
	}
	if second.Sequence != 2 {
		t.Fatalf("sequence=%d", second.Sequence)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path + ".lock"); err != nil {
		t.Fatalf("persistent lock file missing: %v", err)
	}
	reopened, err := OpenWriter(path, runID())
	if err != nil {
		t.Fatalf("reopen after close: %v", err)
	}
	if err := reopened.Close(); err != nil {
		t.Fatal(err)
	}
	read, err := readPath(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(read.Events) != 2 || read.TruncatedTail {
		t.Fatalf("read=%+v", read)
	}
	if _, err := w.Append(Entry{RunID: runID(), Type: EventAttemptStarted}); !errors.Is(err, ErrWriterClosed) {
		t.Fatalf("closed append err=%v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestWriterRecoversTruncatedTailAndResumesSequence(t *testing.T) {
	path := filepath.Join(t.TempDir(), "run.journal")
	w, err := OpenWriter(path, runID())
	if err != nil {
		t.Fatal(err)
	}
	_, _ = w.Append(Entry{RunID: runID(), Type: EventRunStarted})
	_, _ = w.Append(Entry{RunID: runID(), Type: EventAttemptStarted, OperationID: opID("op-a"), TargetID: targetID("target-a"), Payload: Payload{Attempt: 1}})
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	original, _ := os.ReadFile(path)
	partial := mustFrame(t, dispatched(3, "op-a", "target-a", 1))
	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = file.Write(partial[:len(partial)/2])
	_ = file.Close()
	w, err = OpenWriter(path, runID())
	if err != nil {
		t.Fatal(err)
	}
	third, err := w.Append(Entry{RunID: runID(), Type: EventSideEffectDispatched, OperationID: opID("op-a"), TargetID: targetID("target-a"), Payload: Payload{Attempt: 1}})
	if err != nil {
		t.Fatal(err)
	}
	if third.Sequence != 3 {
		t.Fatalf("sequence=%d", third.Sequence)
	}
	_ = w.Close()
	read, err := readPath(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(read.Events) != 3 || read.TruncatedTail {
		t.Fatalf("read=%+v", read)
	}
	final, _ := os.ReadFile(path)
	if len(final) <= len(original) {
		t.Fatal("recovered journal did not append")
	}
}

func TestWriterRejectsInvalidLifecycle(t *testing.T) {
	var nilWriter *Writer
	if _, err := nilWriter.Append(Entry{}); !errors.Is(err, ErrWriterClosed) {
		t.Fatalf("nil writer err=%v", err)
	}
	if _, err := OpenWriter("", runID()); err == nil {
		t.Fatal("empty path accepted")
	}
	if _, err := OpenWriter(filepath.Join(t.TempDir(), "x"), ""); err == nil {
		t.Fatal("invalid run accepted")
	}
	path := filepath.Join(t.TempDir(), "run.journal")
	w, err := OpenWriter(path, runID())
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	if _, err := w.Append(Entry{RunID: runID(), Type: EventAttemptStarted, OperationID: opID("op-a"), TargetID: targetID("target-a"), Payload: Payload{Attempt: 1}}); err == nil {
		t.Fatal("non-start first event accepted")
	}
	other := runID()
	other = "run-2"
	if _, err := w.Append(Entry{RunID: other, Type: EventRunStarted}); err == nil {
		t.Fatal("wrong run accepted")
	}
	if _, err := w.Append(Entry{RunID: runID(), Type: EventRunStarted}); err != nil {
		t.Fatal(err)
	}
	if _, err := w.Append(Entry{RunID: runID(), Type: EventRunStarted}); err == nil {
		t.Fatal("duplicate run start accepted")
	}
	if _, err := w.Append(Entry{RunID: runID(), Type: EventAttemptStarted, OperationID: opID("op-a"), TargetID: targetID("target-a")}); err == nil {
		t.Fatal("invalid entry accepted")
	}
}

func TestOpenWriterRejectsRunMismatchAndCorruption(t *testing.T) {
	path := filepath.Join(t.TempDir(), "run.journal")
	w, _ := OpenWriter(path, runID())
	_, _ = w.Append(Entry{RunID: runID(), Type: EventRunStarted})
	_ = w.Close()
	if _, err := OpenWriter(path, "run-2"); err == nil {
		t.Fatal("run mismatch accepted")
	}
	data, _ := os.ReadFile(path)
	data[0] = 'X'
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenWriter(path, runID()); err == nil {
		t.Fatal("corrupt journal accepted")
	}
}

type failingFile struct {
	bytes.Buffer
	writeLimit int
	writeErr   error
	syncErr    error
	closed     bool
}

func (f *failingFile) Write(p []byte) (int, error) {
	if f.writeLimit >= 0 && len(p) > f.writeLimit {
		n, _ := f.Buffer.Write(p[:f.writeLimit])
		return n, f.writeErr
	}
	return f.Buffer.Write(p)
}
func (f *failingFile) Sync() error  { return f.syncErr }
func (f *failingFile) Close() error { f.closed = true; return nil }

func TestWriterPoisonsAfterPartialWriteOrSyncFailure(t *testing.T) {
	for _, tc := range []struct {
		name string
		file *failingFile
	}{
		{"partial", &failingFile{writeLimit: 5, writeErr: io.ErrUnexpectedEOF}},
		{"sync", &failingFile{writeLimit: -1, syncErr: errors.New("sync")}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := &Writer{file: tc.file, runID: runID(), next: 1, clock: time.Now}
			if _, err := w.Append(Entry{RunID: runID(), Type: EventRunStarted}); err == nil {
				t.Fatal("failure not surfaced")
			}
			if _, err := w.Append(Entry{RunID: runID(), Type: EventRunStarted}); !errors.Is(err, ErrWriterPoisoned) {
				t.Fatalf("poison err=%v", err)
			}
			if err := w.Close(); err != nil {
				t.Fatal(err)
			}
			if !tc.file.closed {
				t.Fatal("file not closed")
			}
		})
	}
}

func TestWriteFullHandlesShortWrites(t *testing.T) {
	var out bytes.Buffer
	writer := shortWriter{dst: &out, max: 2}
	if err := writeFull(writer, []byte("abcdef")); err != nil {
		t.Fatal(err)
	}
	if out.String() != "abcdef" {
		t.Fatalf("out=%q", out.String())
	}
	if err := writeFull(zeroWriter{}, []byte("x")); !errors.Is(err, io.ErrShortWrite) {
		t.Fatalf("err=%v", err)
	}
}

type shortWriter struct {
	dst *bytes.Buffer
	max int
}

func (w shortWriter) Write(p []byte) (int, error) {
	if len(p) > w.max {
		p = p[:w.max]
	}
	return w.dst.Write(p)
}

type zeroWriter struct{}

func (zeroWriter) Write([]byte) (int, error) { return 0, nil }

func TestWriterFilesystemErrorPaths(t *testing.T) {
	root := t.TempDir()
	missing := filepath.Join(root, "missing")
	result, err := readPath(missing)
	if err != nil || len(result.Events) != 0 {
		t.Fatalf("missing read result=%+v err=%v", result, err)
	}
	blocker := filepath.Join(root, "blocker")
	if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenWriter(filepath.Join(blocker, "child", "run.journal"), runID()); err == nil {
		t.Fatal("mkdir failure not surfaced")
	}
	lock := filepath.Join(root, "manual.lock")
	held, err := acquireLock(lock)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := acquireLock(lock); !errors.Is(err, ErrWriterLocked) {
		t.Fatalf("second lock err=%v", err)
	}
	if err := releaseLock(held); err != nil {
		t.Fatal(err)
	}
	if _, err := acquireLock(filepath.Join(blocker, "lock")); err == nil {
		t.Fatal("lock path error not surfaced")
	}
	if runtime.GOOS != "windows" {
		if err := syncDirectory(root); err != nil {
			t.Fatal(err)
		}
		if err := syncDirectory(filepath.Join(root, "absent")); err == nil {
			t.Fatal("missing directory accepted")
		}
	}
}

type closeErrorFile struct{ closeErr error }

func (*closeErrorFile) Write(p []byte) (int, error) { return len(p), nil }
func (*closeErrorFile) Sync() error                 { return nil }
func (f *closeErrorFile) Close() error              { return f.closeErr }

func TestWriterCloseErrorPaths(t *testing.T) {
	w := &Writer{file: &closeErrorFile{closeErr: errors.New("close")}}
	if err := w.Close(); err == nil || err.Error() != "close" {
		t.Fatalf("close err=%v", err)
	}
	if err := releaseLock(nil); err != nil {
		t.Fatalf("nil lock release: %v", err)
	}
}

func TestWriterLockReleasedAfterForcedProcessExit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "run.journal")
	cmd := exec.Command(os.Args[0], "-test.run=^TestWriterLockHelperProcess$")
	cmd.Env = append(os.Environ(),
		"DISTROPLANE_JOURNAL_LOCK_HELPER=1",
		"DISTROPLANE_JOURNAL_LOCK_PATH="+path,
	)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	scanner := bufio.NewScanner(stdout)
	if !scanner.Scan() || scanner.Text() != "locked" {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		t.Fatalf("helper did not acquire lock: stdout=%q stderr=%q", scanner.Text(), stderr.String())
	}
	if err := cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = cmd.Wait()

	w, err := OpenWriter(path, runID())
	if err != nil {
		t.Fatalf("reopen after forced process exit: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestWriterLockHelperProcess(t *testing.T) {
	if os.Getenv("DISTROPLANE_JOURNAL_LOCK_HELPER") != "1" {
		return
	}
	path := os.Getenv("DISTROPLANE_JOURNAL_LOCK_PATH")
	w, err := OpenWriter(path, runID())
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	fmt.Fprintln(os.Stdout, "locked")
	for {
		time.Sleep(time.Hour)
	}
}
