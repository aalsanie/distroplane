package planner

import (
	"errors"
	"math"
	"strings"
	"testing"

	"github.com/aalsanie/distroplane/internal/domain"
	"github.com/aalsanie/distroplane/internal/protocol"
)

func persistedDocument(t *testing.T) planDocument {
	t.Helper()
	builder := newPlanner(t,
		describe("1.0.0", protocol.CapabilityPlan, protocol.CapabilityApply, protocol.CapabilityReconcile),
		planResponse(`{"channel":"stable"}`),
		mustDigest(t, 'a'),
	)
	built, err := builder.Build(t.Context(), baseLoaded(t, baseConfig(`{"id":"primary","provider":{"name":"fake"},"configuration":{}}`)))
	if err != nil {
		t.Fatal(err)
	}
	return built.document
}

func TestPlanFromDocumentRejectsPersistedInvariantViolations(t *testing.T) {
	cases := map[string]struct {
		mutate func(*planDocument)
		want   string
	}{
		"schema":                {func(d *planDocument) { d.SchemaVersion = "2" }, "unsupported plan schema"},
		"protocol":              {func(d *planDocument) { d.ProtocolVersion = "2" }, "unsupported plan protocol"},
		"empty plan id":         {func(d *planDocument) { d.PlanID = "" }, "plan ID must not be empty"},
		"release id":            {func(d *planDocument) { d.Release.ID = " release" }, "release ID"},
		"digest":                {func(d *planDocument) { d.Release.Artifacts[0].Digest = "sha256:bad" }, "digest"},
		"artifact source":       {func(d *planDocument) { d.Release.Artifacts[0].Source = "" }, "artifact source"},
		"target id":             {func(d *planDocument) { d.Targets[0].ID = " target" }, "target ID"},
		"provider name":         {func(d *planDocument) { d.Targets[0].Provider.Name = " fake" }, "provider name"},
		"provider version":      {func(d *planDocument) { d.Targets[0].Provider.Version = "" }, "provider version"},
		"capability":            {func(d *planDocument) { d.Targets[0].RequiredCapabilities = []protocol.Capability{"future"} }, "invalid required capability"},
		"configuration":         {func(d *planDocument) { d.Targets[0].Configuration = nil }, "configuration"},
		"requirement":           {func(d *planDocument) { d.Targets[0].Requirements[0].Kind = "" }, "requirement kind"},
		"provider operation id": {func(d *planDocument) { d.Operations[0].ProviderOperationID = "" }, "provider operation ID"},
		"operation id":          {func(d *planDocument) { d.Operations[0].ID = " op" }, "operation ID"},
		"operation target":      {func(d *planDocument) { d.Operations[0].TargetID = " target" }, "target ID"},
		"operation provider":    {func(d *planDocument) { d.Operations[0].Provider.Name = "other" }, "provider does not match target"},
		"dependency":            {func(d *planDocument) { d.Operations[0].Dependencies = []string{" bad"} }, "operation ID"},
		"timeout":               {func(d *planDocument) { d.Operations[0].TimeoutMillis = math.MaxUint64 }, "timeout exceeds"},
		"payload":               {func(d *planDocument) { d.Operations[0].ProviderPayload = nil }, "payload"},
		"idempotency": {func(d *planDocument) {
			for i := range d.Operations {
				if d.Operations[i].SideEffecting {
					d.Operations[i].IdempotencyKey = "wrong"
					return
				}
			}
		}, "idempotency key does not match"},
		"non side effect key": {func(d *planDocument) {
			for i := range d.Operations {
				if !d.Operations[i].SideEffecting {
					d.Operations[i].IdempotencyKey = "unexpected"
					return
				}
			}
		}, "must not contain an idempotency key"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			document := persistedDocument(t)
			tc.mutate(&document)
			_, err := planFromDocument(document)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err=%v want substring %q", err, tc.want)
			}
		})
	}
}

func TestVerifyArtifactsFailureModes(t *testing.T) {
	if err := VerifyArtifacts(domain.Plan{}, nil); err == nil {
		t.Fatal("invalid plan accepted")
	}
	document := persistedDocument(t)
	loaded, err := planFromDocument(document)
	if err != nil {
		t.Fatal(err)
	}
	artifact := loaded.Plan().Release().Artifacts()[0]

	if err := VerifyArtifacts(loaded.Plan(), fakeHasher{err: errors.New("hash failed")}); err == nil || !strings.Contains(err.Error(), "hash failed") {
		t.Fatalf("err=%v", err)
	}
	if err := VerifyArtifacts(loaded.Plan(), fakeHasher{digest: artifact.Digest(), size: artifact.Size() + 1}); err == nil || !strings.Contains(err.Error(), "size changed") {
		t.Fatalf("err=%v", err)
	}
	other, _ := domain.NewSHA256Digest(strings.Repeat("b", 64))
	if err := VerifyArtifacts(loaded.Plan(), fakeHasher{digest: other, size: artifact.Size()}); err == nil || !strings.Contains(err.Error(), "digest changed") {
		t.Fatalf("err=%v", err)
	}
}
