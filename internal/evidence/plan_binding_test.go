package evidence

import (
	"errors"
	"testing"

	"github.com/aalsanie/distroplane/internal/domain"
	"github.com/aalsanie/distroplane/internal/journal"
)

func TestBuildRejectsEvidenceForDifferentPlan(t *testing.T) {
	first := singlePublishedPlan(t)
	data := publishedJournal(t, first)
	otherID, _ := domain.NewPlanID("plan-other")
	second, err := domain.NewPlan(otherID, first.SchemaVersion(), first.ProtocolVersion(), first.Release(), first.Targets(), first.Operations())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Build(first, data, "run.journal", nil); err != nil {
		t.Fatalf("valid evidence failed: %v", err)
	}
	if _, err := Build(second, data, "run.journal", nil); !errors.Is(err, journal.ErrPlanMismatch) {
		t.Fatalf("cross-plan evidence accepted: %v", err)
	}
}
