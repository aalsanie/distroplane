package evidence

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/aalsanie/distroplane/internal/journal"
)

func TestObservationEventsCoverEveryResultVariant(t *testing.T) {
	observed := []journal.EventType{
		journal.EventOperationReady, journal.EventAttemptStarted,
		journal.EventSideEffectDispatched, journal.EventOperationWaitingExternal,
		journal.EventOperationPublished, journal.EventOperationRejected,
		journal.EventOperationFailed, journal.EventOperationResult,
		journal.EventOperationCancelled, journal.EventOutcomeAmbiguous,
		journal.EventReconcileResult,
	}
	for _, kind := range observed {
		if !operationObservationEvent(kind) {
			t.Errorf("%s must advance the operation observation", kind)
		}
	}
	for _, kind := range []journal.EventType{journal.EventRunStarted, journal.EventRunCompleted,
		journal.EventLeaseAcquired, journal.EventProviderProcessStarted} {
		if operationObservationEvent(kind) {
			t.Errorf("%s is not an operation observation", kind)
		}
	}
}

func TestEvidenceSanitizerOutcomeMatrix(t *testing.T) {
	records := []struct {
		raw    json.RawMessage
		secret string
	}{
		{json.RawMessage(`{"nested":{"access_token":"secret","safe":"https://example.test/download"},"items":[null,17,true]}`), "secret"},
		{json.RawMessage(`["https://example.test/a",["https://example.test/b",false],null]`), ""},
		{json.RawMessage(`{"authorization":"Bearer private","other":"token=private"}`), "private"},
	}
	for _, record := range records {
		clean, err := normalizeEvidence(record.raw)
		if err != nil {
			t.Fatal(err)
		}
		if record.secret != "" && strings.Contains(string(clean), record.secret) {
			t.Fatalf("secret leaked from %s: %s", record.raw, clean)
		}
		_ = externalReferences(clean)
	}
	if got := sanitizeValue(nil); got != nil {
		t.Fatalf("nil changed: %v", got)
	}
	if got := sanitizeValue(true); got != true {
		t.Fatalf("boolean changed: %v", got)
	}
	if got := sanitizeValue(json.Number("7")); got != json.Number("7") {
		t.Fatalf("number changed: %v", got)
	}
	if got := sanitizeText("ordinary"); got != "ordinary" {
		t.Fatalf("ordinary string changed: %q", got)
	}
	if _, err := sanitizeURI("://bad"); err == nil {
		t.Fatal("invalid URI accepted")
	}
	if uri, err := sanitizeURI("https://example.test/path?x=1"); err != nil || uri != "https://example.test/path?x=1" {
		t.Fatalf("ordinary URI changed: %q: %v", uri, err)
	}
	for _, s := range []string{string([]byte{0xff}), "abc\x00def", "x\n", " x", "a  "} {
		if err := validateText("field", s, false, 30); err == nil {
			t.Errorf("invalid field accepted: %q", s)
		}
	}
}

func TestEvidenceAttestationSortAndRejectionBranches(t *testing.T) {
	refs := []AttestationReference{
		{Name: "z", URI: "https://example.test/z"},
		{Name: "a", URI: "https://example.test/2"},
		{Name: "a", URI: "https://example.test/1"},
		{Name: "a", URI: "https://example.test/1"},
	}
	got, err := normalizeAttestations(refs)
	if err != nil || len(got) != 3 {
		t.Fatalf("attestations: %+v err=%v", got, err)
	}
	if got[0].Name != "a" || got[0].URI != "https://example.test/1" || got[1].URI != "https://example.test/2" || got[2].Name != "z" {
		t.Fatalf("incorrect attestation order: %+v", got)
	}
	for _, bad := range []AttestationReference{
		{Name: "", URI: "https://example.test/1"},
		{Name: "bad\n", URI: "https://example.test/1"},
		{Name: "ok", URI: "relative"},
		{Name: "ok", URI: "https://example.test/1", Digest: "invalid"},
	} {
		if _, err := normalizeAttestations([]AttestationReference{bad}); err == nil {
			t.Errorf("invalid attestation accepted: %+v", bad)
		}
	}
}
