package domain_test

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/aalsanie/distroplane/internal/domain"
)

const digestHex = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

func mustReleaseID(t *testing.T, value string) domain.ReleaseID {
	t.Helper()
	v, err := domain.NewReleaseID(value)
	if err != nil {
		t.Fatal(err)
	}
	return v
}
func mustTargetID(t *testing.T, value string) domain.TargetID {
	t.Helper()
	v, err := domain.NewTargetID(value)
	if err != nil {
		t.Fatal(err)
	}
	return v
}
func mustOperationID(t *testing.T, value string) domain.OperationID {
	t.Helper()
	v, err := domain.NewOperationID(value)
	if err != nil {
		t.Fatal(err)
	}
	return v
}
func mustPlanID(t *testing.T, value string) domain.PlanID {
	t.Helper()
	v, err := domain.NewPlanID(value)
	if err != nil {
		t.Fatal(err)
	}
	return v
}
func mustProvider(t *testing.T, name, version string) domain.ProviderRef {
	t.Helper()
	n, err := domain.NewProviderName(name)
	if err != nil {
		t.Fatal(err)
	}
	v, err := domain.NewProviderVersion(version)
	if err != nil {
		t.Fatal(err)
	}
	p, err := domain.NewProviderRef(n, v)
	if err != nil {
		t.Fatal(err)
	}
	return p
}
func mustJSON(t *testing.T, value string) domain.JSONValue {
	t.Helper()
	v, err := domain.NewJSONValue([]byte(value))
	if err != nil {
		t.Fatal(err)
	}
	return v
}
func mustDigest(t *testing.T) domain.Digest {
	t.Helper()
	v, err := domain.NewSHA256Digest(digestHex)
	if err != nil {
		t.Fatal(err)
	}
	return v
}
func mustRelease(t *testing.T) domain.Release {
	t.Helper()
	a, err := domain.NewArtifact("tool", "dist/tool", mustDigest(t), 12, "application/octet-stream")
	if err != nil {
		t.Fatal(err)
	}
	r, err := domain.NewRelease(mustReleaseID(t, "v1.0.0"), []domain.Artifact{a})
	if err != nil {
		t.Fatal(err)
	}
	return r
}
func mustTarget(t *testing.T, id string, provider domain.ProviderRef) domain.Target {
	t.Helper()
	v, err := domain.NewTarget(mustTargetID(t, id), provider, mustJSON(t, `{"channel":"stable"}`))
	if err != nil {
		t.Fatal(err)
	}
	return v
}
func mustOperation(t *testing.T, id, target string, provider domain.ProviderRef, deps []domain.OperationID) domain.Operation {
	t.Helper()
	v, err := domain.NewOperation(mustOperationID(t, id), mustTargetID(t, target), provider, "publish", deps, true, "key-"+id, time.Minute, mustJSON(t, `{}`))
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func TestIdentifiers(t *testing.T) {
	constructors := []struct {
		name  string
		valid func(string) bool
	}{
		{"release", func(s string) bool { v, e := domain.NewReleaseID(s); return e == nil && v.Valid() }},
		{"target", func(s string) bool { v, e := domain.NewTargetID(s); return e == nil && v.Valid() }},
		{"operation", func(s string) bool { v, e := domain.NewOperationID(s); return e == nil && v.Valid() }},
		{"plan", func(s string) bool { v, e := domain.NewPlanID(s); return e == nil && v.Valid() }},
		{"run", func(s string) bool { v, e := domain.NewRunID(s); return e == nil && v.Valid() }},
		{"provider-name", func(s string) bool { v, e := domain.NewProviderName(s); return e == nil && v.Valid() }},
		{"provider-version", func(s string) bool { v, e := domain.NewProviderVersion(s); return e == nil && v.Valid() }},
		{"credential", func(s string) bool { v, e := domain.NewCredentialRef(s); return e == nil && v.Valid() }},
	}
	invalid := []string{"", " leading", "trailing ", "line\nbreak", string([]byte{0xff}), strings.Repeat("x", 257)}
	for _, c := range constructors {
		t.Run(c.name+"/valid", func(t *testing.T) {
			if !c.valid("value-1") {
				t.Fatal("valid identifier rejected")
			}
		})
		for _, value := range invalid {
			t.Run(c.name+"/invalid", func(t *testing.T) {
				if c.valid(value) {
					t.Fatalf("invalid identifier accepted: %q", value)
				}
			})
		}
	}
	if n, err := domain.NewAttemptNumber(1); err != nil || !n.Valid() {
		t.Fatalf("valid attempt rejected: %v", err)
	}
	if n, err := domain.NewAttemptNumber(0); err == nil || n.Valid() {
		t.Fatal("zero attempt accepted")
	}
}

func TestDigest(t *testing.T) {
	d, err := domain.NewSHA256Digest(digestHex)
	if err != nil {
		t.Fatal(err)
	}
	if !d.Valid() || d.Algorithm() != "sha256" || d.Hex() != digestHex || d.String() != "sha256:"+digestHex {
		t.Fatal("unexpected digest")
	}
	zeroHex := strings.Repeat("0", 64)
	if z, err := domain.NewSHA256Digest(zeroHex); err != nil || !z.Valid() {
		t.Fatal("all-zero digest bytes must still be representable")
	}
	for _, value := range []string{"abc", strings.ToUpper(digestHex), strings.Repeat("z", 64)} {
		if _, err := domain.NewSHA256Digest(value); err == nil {
			t.Fatalf("accepted invalid digest %q", value)
		}
	}
	if _, err := domain.ParseDigest(digestHex); err == nil {
		t.Fatal("accepted digest without algorithm")
	}
	if _, err := domain.ParseDigest("sha512:" + digestHex); err == nil {
		t.Fatal("accepted unsupported algorithm")
	}
	parsed, err := domain.ParseDigest(d.String())
	if err != nil || parsed != d {
		t.Fatalf("parse failed: %v", err)
	}
	encoded, err := json.Marshal(d)
	if err != nil {
		t.Fatal(err)
	}
	var decoded domain.Digest
	if err := json.Unmarshal(encoded, &decoded); err != nil || decoded != d {
		t.Fatalf("JSON roundtrip failed: %v", err)
	}
	if _, err := json.Marshal(domain.Digest{}); err == nil {
		t.Fatal("zero digest marshaled")
	}
	if err := json.Unmarshal([]byte(`123`), &decoded); err == nil {
		t.Fatal("non-string digest accepted")
	}
	if err := json.Unmarshal([]byte(`"sha256:bad"`), &decoded); err == nil {
		t.Fatal("invalid digest accepted")
	}
}

func TestJSONValueIsOpaqueAndImmutable(t *testing.T) {
	raw := []byte(`{"x":[1,2]}`)
	value, err := domain.NewJSONValue(raw)
	if err != nil {
		t.Fatal(err)
	}
	raw[0] = '['
	if string(value.Bytes()) != `{"x":[1,2]}` {
		t.Fatal("input mutation leaked")
	}
	copyOut := value.Bytes()
	copyOut[0] = '['
	if string(value.Bytes()) != `{"x":[1,2]}` {
		t.Fatal("output mutation leaked")
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	var decoded domain.JSONValue
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
	if string(decoded.Bytes()) != `{"x":[1,2]}` {
		t.Fatal("roundtrip changed opaque JSON")
	}
	if _, err := domain.NewJSONValue(nil); err == nil {
		t.Fatal("empty JSON accepted")
	}
	if _, err := domain.NewJSONValue([]byte(`{`)); err == nil {
		t.Fatal("malformed JSON accepted")
	}
	if _, err := json.Marshal(domain.JSONValue{}); err == nil {
		t.Fatal("zero JSON value marshaled")
	}
	if err := json.Unmarshal([]byte(`{`), &decoded); err == nil {
		t.Fatal("malformed JSON unmarshaled")
	}
}

func TestArtifactAndRelease(t *testing.T) {
	d := mustDigest(t)
	a, err := domain.NewArtifact("tool", "dist/tool", d, 7, "")
	if err != nil {
		t.Fatal(err)
	}
	if a.Name() != "tool" || a.Source() != "dist/tool" || a.Digest() != d || a.Size() != 7 || a.MediaType() != "" {
		t.Fatal("artifact getters")
	}
	for _, tc := range []struct {
		name, source string
		digest       domain.Digest
		size         int64
		media        string
	}{
		{"", "x", d, 1, ""}, {"x", "", d, 1, ""}, {"x", "x", domain.Digest{}, 1, ""}, {"x", "x", d, -1, ""}, {" x", "x", d, 1, ""}, {"x", "x", d, 1, " bad"},
	} {
		if _, err := domain.NewArtifact(tc.name, tc.source, tc.digest, tc.size, tc.media); err == nil {
			t.Fatalf("invalid artifact accepted: %+v", tc)
		}
	}
	id := mustReleaseID(t, "r1")
	if _, err := domain.NewRelease(id, nil); err == nil {
		t.Fatal("empty release accepted")
	}
	if _, err := domain.NewRelease("", []domain.Artifact{a}); err == nil {
		t.Fatal("invalid release ID accepted")
	}
	if _, err := domain.NewRelease(id, []domain.Artifact{{}}); err == nil {
		t.Fatal("invalid artifact accepted")
	}
	if _, err := domain.NewRelease(id, []domain.Artifact{a, a}); err == nil {
		t.Fatal("duplicate artifact name accepted")
	}
	r, err := domain.NewRelease(id, []domain.Artifact{a})
	if err != nil {
		t.Fatal(err)
	}
	if r.ID() != id || len(r.Artifacts()) != 1 {
		t.Fatal("release getters")
	}
	arts := r.Artifacts()
	arts[0] = domain.Artifact{}
	if r.Artifacts()[0].Name() != "tool" {
		t.Fatal("artifact slice leaked")
	}
}

func TestProviderTargetAndOperation(t *testing.T) {
	pn, _ := domain.NewProviderName("provider")
	pv, _ := domain.NewProviderVersion("1")
	p, err := domain.NewProviderRef(pn, pv)
	if err != nil || !p.Valid() || p.Name() != pn || p.Version() != pv {
		t.Fatalf("provider: %v", err)
	}
	if _, err := domain.NewProviderRef("", pv); err == nil {
		t.Fatal("invalid provider name accepted")
	}
	if _, err := domain.NewProviderRef(pn, ""); err == nil {
		t.Fatal("invalid provider version accepted")
	}
	config := mustJSON(t, `{"x":1}`)
	targetID := mustTargetID(t, "t")
	target, err := domain.NewTarget(targetID, p, config)
	if err != nil {
		t.Fatal(err)
	}
	if target.ID() != targetID || target.Provider() != p || string(target.Configuration().Bytes()) != `{"x":1}` {
		t.Fatal("target getters")
	}
	if _, err := domain.NewTarget("", p, config); err == nil {
		t.Fatal("invalid target ID accepted")
	}
	if _, err := domain.NewTarget(targetID, domain.ProviderRef{}, config); err == nil {
		t.Fatal("invalid target provider accepted")
	}
	if _, err := domain.NewTarget(targetID, p, domain.JSONValue{}); err == nil {
		t.Fatal("invalid target config accepted")
	}

	opID := mustOperationID(t, "op")
	dep := mustOperationID(t, "dep")
	op, err := domain.NewOperation(opID, targetID, p, "publish", []domain.OperationID{dep}, true, "key", time.Second, mustJSON(t, `{}`))
	if err != nil {
		t.Fatal(err)
	}
	if op.ID() != opID || op.TargetID() != targetID || op.Provider() != p || op.Kind() != "publish" || !op.SideEffecting() || op.IdempotencyKey() != "key" || op.Timeout() != time.Second || string(op.ProviderPayload().Bytes()) != `{}` {
		t.Fatal("operation getters")
	}
	deps := op.Dependencies()
	deps[0] = opID
	if op.Dependencies()[0] != dep {
		t.Fatal("dependency slice leaked")
	}
	badCases := []struct {
		name     string
		id       domain.OperationID
		target   domain.TargetID
		provider domain.ProviderRef
		kind     string
		deps     []domain.OperationID
		side     bool
		key      string
		timeout  time.Duration
		payload  domain.JSONValue
	}{
		{"id", "", targetID, p, "publish", nil, false, "", 0, mustJSON(t, `{}`)},
		{"target", opID, "", p, "publish", nil, false, "", 0, mustJSON(t, `{}`)},
		{"provider", opID, targetID, domain.ProviderRef{}, "publish", nil, false, "", 0, mustJSON(t, `{}`)},
		{"kind", opID, targetID, p, "", nil, false, "", 0, mustJSON(t, `{}`)},
		{"timeout", opID, targetID, p, "publish", nil, false, "", -1, mustJSON(t, `{}`)},
		{"payload", opID, targetID, p, "publish", nil, false, "", 0, domain.JSONValue{}},
		{"key", opID, targetID, p, "publish", nil, true, "", 0, mustJSON(t, `{}`)},
		{"self", opID, targetID, p, "publish", []domain.OperationID{opID}, false, "", 0, mustJSON(t, `{}`)},
		{"baddep", opID, targetID, p, "publish", []domain.OperationID{""}, false, "", 0, mustJSON(t, `{}`)},
		{"dupdep", opID, targetID, p, "publish", []domain.OperationID{dep, dep}, false, "", 0, mustJSON(t, `{}`)},
	}
	for _, tc := range badCases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := domain.NewOperation(tc.id, tc.target, tc.provider, tc.kind, tc.deps, tc.side, tc.key, tc.timeout, tc.payload); err == nil {
				t.Fatal("invalid operation accepted")
			}
		})
	}
	if _, err := domain.NewOperation(opID, targetID, p, "publish", nil, false, " key", 0, mustJSON(t, `{}`)); err == nil {
		t.Fatal("invalid optional idempotency key accepted")
	}
}

func TestNormalizedStateAndEvidence(t *testing.T) {
	states := []domain.NormalizedState{domain.StatePlanned, domain.StateReady, domain.StateRunning, domain.StateWaitingExternal, domain.StatePublished, domain.StateRejected, domain.StateFailed, domain.StateCancelled}
	for _, state := range states {
		if !state.Valid() {
			t.Fatalf("state invalid: %s", state)
		}
		encoded, err := json.Marshal(state)
		if err != nil {
			t.Fatal(err)
		}
		var decoded domain.NormalizedState
		if err := json.Unmarshal(encoded, &decoded); err != nil || decoded != state {
			t.Fatalf("state roundtrip: %v", err)
		}
	}
	invalid := domain.NormalizedState("OTHER")
	if invalid.Valid() {
		t.Fatal("unknown state valid")
	}
	if _, err := json.Marshal(invalid); err == nil {
		t.Fatal("unknown state marshaled")
	}
	var state domain.NormalizedState
	if err := json.Unmarshal([]byte(`123`), &state); err == nil {
		t.Fatal("non-string state accepted")
	}
	if err := json.Unmarshal([]byte(`"OTHER"`), &state); err == nil {
		t.Fatal("unknown state accepted")
	}
	e, err := domain.NewDistributionEvidence(mustTargetID(t, "t"), domain.StatePublished, "visible", mustJSON(t, `{"url":"x"}`))
	if err != nil {
		t.Fatal(err)
	}
	if e.TargetID() != "t" || e.State() != domain.StatePublished || e.ProviderState() != "visible" || string(e.Data().Bytes()) != `{"url":"x"}` {
		t.Fatal("evidence getters")
	}
	if _, err := domain.NewDistributionEvidence("", domain.StatePublished, "", mustJSON(t, `{}`)); err == nil {
		t.Fatal("bad evidence target")
	}
	if _, err := domain.NewDistributionEvidence(mustTargetID(t, "t"), invalid, "", mustJSON(t, `{}`)); err == nil {
		t.Fatal("bad evidence state")
	}
	if _, err := domain.NewDistributionEvidence(mustTargetID(t, "t"), domain.StatePublished, " bad", mustJSON(t, `{}`)); err == nil {
		t.Fatal("bad provider state")
	}
	if _, err := domain.NewDistributionEvidence(mustTargetID(t, "t"), domain.StatePublished, "", domain.JSONValue{}); err == nil {
		t.Fatal("bad evidence data")
	}
}

func TestPlanValidation(t *testing.T) {
	provider := mustProvider(t, "provider", "1.0.0")
	t1 := mustTarget(t, "t1", provider)
	t2 := mustTarget(t, "t2", provider)
	a := mustOperation(t, "a", "t1", provider, nil)
	b := mustOperation(t, "b", "t1", provider, []domain.OperationID{a.ID()})
	c := mustOperation(t, "c", "t2", provider, nil)
	plan, err := domain.NewPlan(mustPlanID(t, "plan"), "1", "1", mustRelease(t), []domain.Target{t1, t2}, []domain.Operation{a, b, c})
	if err != nil {
		t.Fatal(err)
	}
	if plan.ID() != "plan" || plan.SchemaVersion() != "1" || plan.ProtocolVersion() != "1" || plan.Release().ID() != "v1.0.0" || len(plan.Targets()) != 2 || len(plan.Operations()) != 3 {
		t.Fatal("plan getters")
	}
	targets := plan.Targets()
	targets[0] = domain.Target{}
	if plan.Targets()[0].ID() != "t1" {
		t.Fatal("targets slice leaked")
	}
	ops := plan.Operations()
	ops[0] = domain.Operation{}
	if plan.Operations()[0].ID() != "a" {
		t.Fatal("operations slice leaked")
	}

	if _, err := domain.NewPlan("", "1", "1", mustRelease(t), []domain.Target{t1}, nil); err == nil {
		t.Fatal("invalid plan ID")
	}
	if _, err := domain.NewPlan(mustPlanID(t, "p"), "", "1", mustRelease(t), []domain.Target{t1}, nil); err == nil {
		t.Fatal("invalid schema")
	}
	if _, err := domain.NewPlan(mustPlanID(t, "p"), "1", "", mustRelease(t), []domain.Target{t1}, nil); err == nil {
		t.Fatal("invalid protocol")
	}
	if _, err := domain.NewPlan(mustPlanID(t, "p"), "1", "1", domain.Release{}, []domain.Target{t1}, nil); err == nil {
		t.Fatal("invalid release")
	}
	if _, err := domain.NewPlan(mustPlanID(t, "p"), "1", "1", mustRelease(t), nil, nil); err == nil {
		t.Fatal("empty targets")
	}
	if _, err := domain.NewPlan(mustPlanID(t, "p"), "1", "1", mustRelease(t), []domain.Target{t1, t1}, nil); err == nil {
		t.Fatal("duplicate target")
	}
	if _, err := domain.NewPlan(mustPlanID(t, "p"), "1", "1", mustRelease(t), []domain.Target{{}}, nil); err == nil {
		t.Fatal("invalid target")
	}
	if _, err := domain.NewPlan(mustPlanID(t, "p"), "1", "1", mustRelease(t), []domain.Target{t1}, []domain.Operation{a, a}); err == nil {
		t.Fatal("duplicate operation")
	}
	if _, err := domain.NewPlan(mustPlanID(t, "p"), "1", "1", mustRelease(t), []domain.Target{t1}, []domain.Operation{{}}); err == nil {
		t.Fatal("invalid operation")
	}
	unknownTarget := mustOperation(t, "x", "missing", provider, nil)
	if _, err := domain.NewPlan(mustPlanID(t, "p"), "1", "1", mustRelease(t), []domain.Target{t1}, []domain.Operation{unknownTarget}); err == nil {
		t.Fatal("unknown target accepted")
	}
	otherProvider := mustProvider(t, "other", "1")
	wrongProvider := mustOperation(t, "x", "t1", otherProvider, nil)
	if _, err := domain.NewPlan(mustPlanID(t, "p"), "1", "1", mustRelease(t), []domain.Target{t1}, []domain.Operation{wrongProvider}); err == nil {
		t.Fatal("provider mismatch accepted")
	}
	missingDep := mustOperation(t, "x", "t1", provider, []domain.OperationID{mustOperationID(t, "missing")})
	if _, err := domain.NewPlan(mustPlanID(t, "p"), "1", "1", mustRelease(t), []domain.Target{t1}, []domain.Operation{missingDep}); err == nil {
		t.Fatal("missing dependency accepted")
	}
	cycleA := mustOperation(t, "ca", "t1", provider, []domain.OperationID{mustOperationID(t, "cb")})
	cycleB := mustOperation(t, "cb", "t1", provider, []domain.OperationID{mustOperationID(t, "ca")})
	if _, err := domain.NewPlan(mustPlanID(t, "p"), "1", "1", mustRelease(t), []domain.Target{t1}, []domain.Operation{cycleA, cycleB}); err == nil {
		t.Fatal("cycle accepted")
	}
}

func FuzzParseDigest(f *testing.F) {
	f.Add("sha256:" + digestHex)
	f.Add("bad")
	f.Fuzz(func(t *testing.T, value string) { _, _ = domain.ParseDigest(value) })
}

func FuzzIdentifiers(f *testing.F) {
	f.Add("value")
	f.Add("")
	f.Fuzz(func(t *testing.T, value string) {
		_, _ = domain.NewReleaseID(value)
		_, _ = domain.NewTargetID(value)
		_, _ = domain.NewOperationID(value)
		_, _ = domain.NewPlanID(value)
		_, _ = domain.NewRunID(value)
		_, _ = domain.NewProviderName(value)
		_, _ = domain.NewProviderVersion(value)
		_, _ = domain.NewCredentialRef(value)
	})
}
