package journal

import (
	"bytes"
	"errors"
	"testing"
	"time"
)

func TestWriterDiskFullPoisonsWriter(t *testing.T) {
	diskFull := errors.New("disk full")
	file := &failingFile{writeLimit: 8, writeErr: diskFull}
	writer := &Writer{file: file, runID: runID(), next: 1, clock: time.Now}

	if _, err := writer.Append(Entry{RunID: runID(), Type: EventRunStarted}); !errors.Is(err, diskFull) {
		t.Fatalf("append err=%v", err)
	}
	if _, err := writer.Append(Entry{RunID: runID(), Type: EventRunStarted}); !errors.Is(err, ErrWriterPoisoned) {
		t.Fatalf("poisoned writer err=%v", err)
	}
}

func TestJournalSequenceRemainsAuthoritativeUnderClockSkew(t *testing.T) {
	times := []time.Time{
		time.Unix(300, 0).UTC(),
		time.Unix(100, 0).UTC(),
		time.Unix(200, 0).UTC(),
	}
	index := 0
	file := &failingFile{writeLimit: -1}
	writer := &Writer{
		file: file, runID: runID(), next: 1,
		clock: func() time.Time {
			value := times[index]
			index++
			return value
		},
	}
	entries := []Entry{
		{RunID: runID(), Type: EventRunStarted},
		{RunID: runID(), Type: EventOperationReady, OperationID: opID("op-a"), TargetID: targetID("target-a")},
		{RunID: runID(), Type: EventAttemptStarted, OperationID: opID("op-a"), TargetID: targetID("target-a"), Payload: Payload{Attempt: 1, AttemptReason: AttemptReasonInitial}},
	}
	for _, entry := range entries {
		if _, err := writer.Append(entry); err != nil {
			t.Fatal(err)
		}
	}
	read, err := Read(bytes.NewReader(file.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	if len(read.Events) != len(entries) {
		t.Fatalf("events=%d", len(read.Events))
	}
	if !read.Events[1].ObservedAt.Before(read.Events[0].ObservedAt) {
		t.Fatal("test did not create backwards clock movement")
	}
	for i, event := range read.Events {
		if event.Sequence != uint64(i+1) {
			t.Fatalf("sequence[%d]=%d", i, event.Sequence)
		}
	}
	if _, err := Reduce(testPlan(t), read.Events); err != nil {
		t.Fatalf("clock skew changed replay semantics: %v", err)
	}
}
