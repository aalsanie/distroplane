package planner

import (
	"errors"
	"io"
	"strings"
	"testing"
)

type failingPlanReader struct{}

func (failingPlanReader) Read([]byte) (int, error) {
	return 0, errors.New("controlled plan storage error")
}

func TestPersistedPlanFailsClosedOnConflictingExecutableDigests(t *testing.T) {
	base := persistedDocument(t)
	conflicting := base
	conflicting.Targets = append([]targetDocument(nil), base.Targets...)
	second := base.Targets[0]
	second.ID = "secondary"
	second.Provider.Digest = "sha256:" + strings.Repeat("f", 64)
	conflicting.Targets = append(conflicting.Targets, second)
	if _, err := planFromDocument(conflicting); err == nil ||
		!strings.Contains(err.Error(), "inconsistent executable digests") {
		t.Fatalf("same provider with mismatched executable binding accepted: %v", err)
	}
	unbound := persistedDocument(t)
	unbound.Targets = append([]targetDocument(nil), unbound.Targets...)
	unbound.Operations = append([]operationDocument(nil), unbound.Operations...)
	unbound.Targets[0].Provider.Digest = ""
	for i := range unbound.Operations {
		unbound.Operations[i].Provider.Digest = ""
	}
	if _, err := planFromDocument(unbound); err == nil {
		t.Fatal("plan tampering to remove provider digest was accepted")
	}
}

func TestPersistedPlanDecoderRejectsFailedOrExcessiveInput(t *testing.T) {
	if _, err := Decode(failingPlanReader{}); err == nil {
		t.Fatal("I/O error was ignored while loading plan")
	}
	if _, err := Decode(strings.NewReader(strings.Repeat("x", MaxPlanBytes+1))); err == nil {
		t.Fatal("oversized plan was accepted")
	}
	if _, err := Decode(nil); err == nil {
		t.Fatal("nil plan reader was accepted")
	}
	if _, err := Decode(strings.NewReader("{")); err == nil {
		t.Fatal("truncated plan JSON was accepted")
	}
	var _ io.Reader = failingPlanReader{}
}
