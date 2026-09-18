package journal

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"sync"
	"time"

	"github.com/aalsanie/distroplane/internal/domain"
)

type durableFile interface {
	io.Writer
	Sync() error
	Close() error
}

type Writer struct {
	mu       sync.Mutex
	file     durableFile
	path     string
	lockPath string
	runID    domain.RunID
	next     uint64
	clock    func() time.Time
	events   []Event
	poisoned bool
	closed   bool
}

func OpenWriter(path string, runID domain.RunID) (*Writer, error) {
	if path == "" {
		return nil, fmt.Errorf("journal path must not be empty")
	}
	if !runID.Valid() {
		return nil, fmt.Errorf("run ID is invalid")
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(absolute), 0o755); err != nil {
		return nil, err
	}
	lockPath := absolute + ".lock"
	if err := acquireLock(lockPath); err != nil {
		return nil, err
	}
	keepLock := false
	defer func() {
		if !keepLock {
			_ = os.Remove(lockPath)
		}
	}()

	result, err := readPath(absolute)
	if err != nil {
		return nil, err
	}
	if len(result.Events) != 0 && result.Events[0].RunID != runID {
		return nil, fmt.Errorf("journal belongs to run %q", result.Events[0].RunID)
	}
	_, statErr := os.Stat(absolute)
	created := errors.Is(statErr, os.ErrNotExist)
	if statErr != nil && !created {
		return nil, statErr
	}
	file, err := os.OpenFile(absolute, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if created {
		if err := syncDirectory(filepath.Dir(absolute)); err != nil {
			_ = file.Close()
			return nil, err
		}
	}
	if result.TruncatedTail {
		if err := file.Truncate(result.ValidBytes); err != nil {
			_ = file.Close()
			return nil, err
		}
		if err := file.Sync(); err != nil {
			_ = file.Close()
			return nil, err
		}
	}
	if _, err := file.Seek(0, io.SeekEnd); err != nil {
		_ = file.Close()
		return nil, err
	}
	keepLock = true
	return &Writer{file: file, path: absolute, lockPath: lockPath, runID: runID, next: uint64(len(result.Events)) + 1, clock: time.Now, events: cloneEvents(result.Events)}, nil
}

func acquireLock(path string) error {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			return ErrWriterLocked
		}
		return err
	}
	ok := false
	defer func() {
		_ = file.Close()
		if !ok {
			_ = os.Remove(path)
		}
	}()
	if _, err := file.WriteString(strconv.Itoa(os.Getpid()) + "\n"); err != nil {
		return err
	}
	if err := file.Sync(); err != nil {
		return err
	}
	ok = true
	return nil
}

func readPath(path string) (ReadResult, error) {
	file, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return ReadResult{}, nil
		}
		return ReadResult{}, err
	}
	defer file.Close()
	return Read(file)
}

func (w *Writer) Append(entry Entry) (Event, error) {
	if w == nil {
		return Event{}, ErrWriterClosed
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.file == nil {
		return Event{}, ErrWriterClosed
	}
	if w.closed {
		return Event{}, ErrWriterClosed
	}
	if w.poisoned {
		return Event{}, ErrWriterPoisoned
	}
	if entry.RunID != w.runID {
		return Event{}, fmt.Errorf("entry run ID %q does not match writer run %q", entry.RunID, w.runID)
	}
	if w.next == 1 && entry.Type != EventRunStarted {
		return Event{}, fmt.Errorf("first journal event must be %q", EventRunStarted)
	}
	if w.next > 1 && entry.Type == EventRunStarted {
		return Event{}, fmt.Errorf("run-started event already exists")
	}
	if err := entry.validate(); err != nil {
		return Event{}, err
	}
	event := Event{
		SchemaVersion: SchemaVersion,
		Sequence:      w.next,
		RunID:         entry.RunID,
		Type:          entry.Type,
		OperationID:   entry.OperationID,
		TargetID:      entry.TargetID,
		ObservedAt:    w.clock().UTC(),
		Payload:       entry.Payload,
	}
	frame, err := encodeFrame(event)
	if err != nil {
		return Event{}, err
	}
	if err := writeFull(w.file, frame); err != nil {
		w.poisoned = true
		return Event{}, err
	}
	if err := w.file.Sync(); err != nil {
		w.poisoned = true
		return Event{}, err
	}
	w.events = append(w.events, cloneEvent(event))
	w.next++
	return cloneEvent(event), nil
}

func (w *Writer) Events() []Event {
	if w == nil {
		return nil
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	return cloneEvents(w.events)
}

func writeFull(writer io.Writer, data []byte) error {
	for len(data) != 0 {
		n, err := writer.Write(data)
		if n > 0 {
			data = data[n:]
		}
		if err != nil {
			return err
		}
		if n == 0 {
			return io.ErrShortWrite
		}
	}
	return nil
}

func (w *Writer) Close() error {
	if w == nil {
		return nil
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return nil
	}
	w.closed = true
	var first error
	if w.file != nil {
		if err := w.file.Close(); err != nil {
			first = err
		}
	}
	if w.lockPath != "" {
		if err := os.Remove(w.lockPath); err != nil && !errors.Is(err, os.ErrNotExist) && first == nil {
			first = err
		}
	}
	return first
}

func syncDirectory(path string) error {
	if runtime.GOOS == "windows" {
		return nil
	}
	directory, err := os.Open(path)
	if err != nil {
		return err
	}
	defer directory.Close()
	return directory.Sync()
}
