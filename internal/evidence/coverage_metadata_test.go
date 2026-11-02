package evidence

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/aalsanie/distroplane/internal/domain"
	"github.com/aalsanie/distroplane/internal/journal"
)

func TestObservationLifecycleUsesRecordedEventTimestamps(t *testing.T) {
	origin := time.Date(2026, time.November, 1, 0, 0, 0, 0, time.UTC)
	operation := domain.OperationID("publish")
	events := []journal.Event{
		{Type: journal.EventRunStarted, ObservedAt: origin},
		{Type: journal.EventAttemptStarted, OperationID: operation, ObservedAt: origin.Add(time.Second)},
		{Type: journal.EventOperationPublished, OperationID: operation, ObservedAt: origin.Add(2 * time.Second)},
		{Type: journal.EventRunCancelled, ObservedAt: origin.Add(3 * time.Second)},
		{Type: journal.EventRunCompleted, ObservedAt: origin.Add(4 * time.Second)},
	}
	metadata := deriveObservations(events)
	if !metadata.startedAt.Equal(origin) || !metadata.observedAt.Equal(origin.Add(4*time.Second)) {
		t.Fatalf("run timestamps: %+v", metadata)
	}
	if metadata.cancelledAt == nil || !metadata.cancelledAt.Equal(origin.Add(3*time.Second)) ||
		metadata.completedAt == nil || !metadata.completedAt.Equal(origin.Add(4*time.Second)) {
		t.Fatalf("lifecycle event timestamps: %+v", metadata)
	}
	if !metadata.operations[operation].Equal(origin.Add(2 * time.Second)) {
		t.Fatalf("operation observation not monotonic: %+v", metadata.operations)
	}
}

func TestExternalReferenceCollectionHandlesNestedValuesAndInvalidURLs(t *testing.T) {
	raw := json.RawMessage(`["https://example.test/a","https://example.test/a",{"nested":["ftp://example.test/file","not-a-url","http://example.test/b",1,true,null]},{"password":"https://user:secret@example.test/private"}]`)
	got := externalReferences(raw)
	want := []string{"http://example.test/b", "https://example.test/a"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unsafe or duplicate references: got=%v want=%v", got, want)
	}
	values := make(map[string]struct{})
	collectReferences(map[string]any{
		"list":  []any{"https://example.test/c", 123, nil},
		"other": map[string]any{"url": "https://example.test/d", "hidden": "relative"},
	}, values)
	if len(values) != 2 {
		t.Fatalf("unexpected reference set: %+v", values)
	}
}
