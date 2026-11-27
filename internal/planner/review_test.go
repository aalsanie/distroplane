package planner

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/aalsanie/distroplane/internal/protocol"
)

func TestReviewFieldsBindPlanIdentityAndSurviveRoundTrip(t *testing.T) {
	loaded := baseLoaded(t, baseConfig(`{"id":"primary","provider":{"name":"fake"},"configuration":{}}`))
	response := planResponse(`{"channel":"stable"}`)
	response.ReviewFields = protocol.ReviewFields{{Label: "Destination", Value: "production"}, {Label: "Version", Value: "1.2.3"}}
	desc := describe("1", protocol.CapabilityPlan, protocol.CapabilityApply, protocol.CapabilityReconcile)
	digest := mustDigest(t, 'a')
	build := func(response protocol.PlanResponse) DistributionPlan {
		t.Helper()
		p, err := newPlanner(t, desc, response, digest).Build(context.Background(), loaded)
		if err != nil {
			t.Fatal(err)
		}
		return p
	}
	first := build(response)
	if first.Plan().SchemaVersion() != "2" || first.ID() != build(response).ID() {
		t.Fatal("review plan identity not deterministic")
	}
	raw, err := first.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	restored, err := Decode(bytes.NewReader(raw))
	if err != nil || restored.ID() != first.ID() {
		t.Fatalf("review round trip: %v", err)
	}
	fields := restored.ReviewFields("primary")
	if len(fields) != 2 || fields[0] != response.ReviewFields[0] || fields[1] != response.ReviewFields[1] || restored.ReviewFields("unknown") != nil {
		t.Fatalf("review fields=%v", fields)
	}
	fields[0].Value = "other"
	if restored.ReviewFields("primary")[0].Value != "production" {
		t.Fatal("review fields are mutable")
	}
	for _, changed := range []protocol.ReviewFields{
		nil,
		{{Label: "Destination", Value: "staging"}, {Label: "Version", Value: "1.2.3"}},
		{{Label: "Destination", Value: "production"}, {Label: "Version", Value: "2.0.0"}},
		{response.ReviewFields[1], response.ReviewFields[0]},
	} {
		variant := response
		variant.ReviewFields = changed
		if build(variant).ID() == first.ID() {
			t.Fatal("changed review did not invalidate plan identity")
		}
	}
	for _, tampered := range [][]byte{
		bytes.Replace(raw, []byte(`"production"`), []byte(`"staging"`), 1),
		bytes.Replace(raw, []byte(`"production"`), []byte(`"\u202e"`), 1),
		bytes.Replace(raw, []byte(`"schemaVersion":"2"`), []byte(`"schemaVersion":"1"`), 1),
		bytes.Replace(raw, []byte(`"label":"Destination"`), []byte(`"label":"Destination","extra":true`), 1),
	} {
		if _, err := Decode(bytes.NewReader(tampered)); err == nil {
			t.Fatal("tampered review plan accepted")
		}
	}
	doc := first.document
	doc.Targets[0].ReviewFields = protocol.ReviewFields{{Label: "x", Value: "bad\nvalue"}}
	if _, err := planFromDocument(doc); err == nil {
		t.Fatal("invalid review fields accepted by plan loader")
	}
	response.ReviewFields = protocol.ReviewFields{{Label: "x", Value: "bad\nvalue"}}
	if _, err := newPlanner(t, desc, response, digest).Build(context.Background(), loaded); err == nil {
		t.Fatal("invalid review fields accepted by builder")
	}
}

func TestLegacyPlanIdentityIndependentFixture(t *testing.T) {
	const raw = `{"schemaVersion":"1","protocolVersion":"1","release":{"id":"legacy","artifacts":[{"name":"app","digest":"sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","size":3,"source":"/fixture/app"}]},"targets":[{"id":"primary","provider":{"name":"fake","version":"1","digest":"sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"},"configuration":{},"requiredCapabilities":["plan"]}],"operations":[],"planId":"sha256-b42b05777788653194bd2f06c765aff0112c997ff5a3cd3f68998297d1a66fea"}`
	p, err := Decode(strings.NewReader(raw))
	if err != nil || p.Plan().SchemaVersion() != "1" || string(p.ID()) != "sha256-b42b05777788653194bd2f06c765aff0112c997ff5a3cd3f68998297d1a66fea" || len(p.ReviewFields("primary")) != 0 {
		t.Fatalf("legacy plan changed: %s %v", p.ID(), err)
	}
	for _, value := range []string{`[]`, `[{"label":"Destination","value":"example"}]`, `null`} {
		changed := strings.Replace(raw, `"requiredCapabilities":["plan"]`, `"requiredCapabilities":["plan"],"reviewFields":`+value, 1)
		if _, err := Decode(strings.NewReader(changed)); err == nil {
			t.Fatal("schema 1 accepted reviewFields")
		}
	}
}
