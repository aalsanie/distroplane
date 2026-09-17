package planner

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/aalsanie/distroplane/internal/domain"
	"github.com/aalsanie/distroplane/internal/protocol"
)

func TestFileHasher(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "artifact")
	if err := os.WriteFile(path, []byte("abc"), 0o600); err != nil {
		t.Fatal(err)
	}
	d, size, err := (FileHasher{}).Hash(path)
	if err != nil {
		t.Fatal(err)
	}
	if size != 3 || d.String() != "sha256:ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad" {
		t.Fatalf("digest=%s size=%d", d.String(), size)
	}
	if _, _, err := (FileHasher{}).Hash(dir); err == nil {
		t.Fatal("directory accepted")
	}
	if _, _, err := (FileHasher{}).Hash(filepath.Join(dir, "missing")); err == nil {
		t.Fatal("missing accepted")
	}
	m := FileHasher{afterRead: func() { _ = os.WriteFile(path, []byte("abcd"), 0o600) }}
	if _, _, err := m.Hash(path); err == nil {
		t.Fatal("mutation not detected")
	}
}

func TestExecutableResolver(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "provider")
	if err := os.WriteFile(file, []byte("x"), 0o700); err != nil {
		t.Fatal(err)
	}
	r := executableResolver{lookPath: func(string) (string, error) { return file, nil }, abs: filepath.Abs, stat: os.Stat}
	if got, err := r.Resolve("fake", ""); err != nil || got == "" {
		t.Fatalf("got=%q err=%v", got, err)
	}
	if _, err := r.Resolve("fake", dir); err == nil {
		t.Fatal("directory accepted")
	}
	if _, err := r.Resolve("", file); err == nil {
		t.Fatal("blank name accepted")
	}
	r.lookPath = func(string) (string, error) { return "", errors.New("missing") }
	if _, err := r.Resolve("missing", ""); err == nil {
		t.Fatal("lookPath error ignored")
	}
	r = executableResolver{lookPath: func(string) (string, error) { return file, nil }, abs: func(string) (string, error) { return "", errors.New("abs") }, stat: os.Stat}
	if _, err := r.Resolve("fake", file); err == nil {
		t.Fatal("abs error ignored")
	}
	r = executableResolver{lookPath: func(string) (string, error) { return file, nil }, abs: filepath.Abs, stat: func(string) (os.FileInfo, error) { return nil, errors.New("stat") }}
	if _, err := r.Resolve("fake", file); err == nil {
		t.Fatal("stat error ignored")
	}
}

func TestStore(t *testing.T) {
	d := mustDigest(t, 'f')
	p := newPlanner(t, describe("1", protocol.CapabilityPlan, protocol.CapabilityApply, protocol.CapabilityReconcile), planResponse(`{}`), d)
	loaded := baseLoaded(t, baseConfig(`{"id":"a","provider":{"name":"fake"},"configuration":{}}`))
	plan, err := p.Build(context.Background(), loaded)
	if err != nil {
		t.Fatal(err)
	}
	store := Store{Root: t.TempDir()}
	path, err := store.Save(plan)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	want, _ := plan.Bytes()
	if string(data) != string(want) {
		t.Fatal("persisted bytes differ")
	}
	if _, err := store.Save(plan); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("tampered"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Save(plan); err == nil {
		t.Fatal("tampered plan accepted")
	}
	if _, err := (Store{}).Save(plan); err == nil {
		t.Fatal("empty root accepted")
	}
	if _, err := store.Save(DistributionPlan{}); err == nil {
		t.Fatal("zero plan accepted")
	}
}

func TestStoreErrors(t *testing.T) {
	d := mustDigest(t, '8')
	p := newPlanner(t, describe("1", protocol.CapabilityPlan), protocol.PlanResponse{}, d)
	loaded := baseLoaded(t, baseConfig(`{"id":"a","provider":{"name":"fake"},"configuration":{}}`))
	plan, err := p.Build(context.Background(), loaded)
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	blocker := filepath.Join(root, "blocker")
	if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := (Store{Root: filepath.Join(blocker, "child")}).Save(plan); err == nil {
		t.Fatal("mkdir error not surfaced")
	}
	if _, err := existingMatches(root, []byte("x")); err == nil {
		t.Fatal("directory read accepted")
	}
	if runtime.GOOS != "windows" {
		if err := syncDirectory(filepath.Join(root, "missing")); err == nil {
			t.Fatal("missing dir sync accepted")
		}
	}
	bad := plan
	bad.document.Targets[0].Configuration = json.RawMessage(`{`)
	if _, err := (Store{Root: root}).Save(bad); err == nil {
		t.Fatal("marshal error not surfaced")
	}
}

func TestPlannerSupportFunctions(t *testing.T) {
	ta, _ := domain.NewTargetID("a")
	tb, _ := domain.NewTargetID("b")
	a1, _ := namespacedOperationID(ta, "publish")
	a2, _ := namespacedOperationID(ta, "publish")
	b, _ := namespacedOperationID(tb, "publish")
	if a1 != a2 || a1 == b {
		t.Fatal("operation IDs not deterministic or namespaced")
	}
	pid, _ := domain.NewPlanID("sha256-" + strings.Repeat("a", 64))
	d1, d2 := mustDigest(t, '1'), mustDigest(t, '2')
	x1, _ := domain.NewArtifact("a", "/a", d1, 1, "")
	x2, _ := domain.NewArtifact("b", "/b", d2, 1, "")
	k1 := deriveIdempotencyKey(pid, ta, a1, []domain.Artifact{x1, x2})
	k2 := deriveIdempotencyKey(pid, ta, a1, []domain.Artifact{x2, x1})
	if k1 != k2 || !strings.HasPrefix(k1, "sha256:") {
		t.Fatal("idempotency key unstable")
	}
	reqs, err := canonicalRequirements([]protocol.Requirement{{Kind: "b", Name: "a", Metadata: json.RawMessage(`{"z":1,"a":2}`)}, {Kind: "a", Name: "z", Metadata: json.RawMessage(`{"x":1}`)}, {Kind: "a", Name: "a", Metadata: json.RawMessage(`{"x":2}`)}, {Kind: "a", Name: "a", Metadata: json.RawMessage(`{"x":1}`)}})
	if err != nil || len(reqs) != 4 || reqs[0].Kind != "a" {
		t.Fatalf("reqs=%+v err=%v", reqs, err)
	}
	if _, err := canonicalRequirements([]protocol.Requirement{{Kind: "", Name: "x"}}); err == nil {
		t.Fatal("invalid requirement accepted")
	}
	if _, err := canonicalRequirement(protocol.Requirement{Kind: "x", Name: "y", Metadata: json.RawMessage(`{"a":1,"a":2}`)}); err == nil {
		t.Fatal("duplicate metadata accepted")
	}
}

func TestSemanticAndOperationValidation(t *testing.T) {
	_, err := semanticPlanID(semanticPlan{
		SchemaVersion:   "1",
		ProtocolVersion: "1",
		Release: semanticRelease{ID: "r", Artifacts: []semanticArtifact{{
			Name: "a", Digest: "sha256:" + strings.Repeat("a", 64), Size: 1,
		}}},
		Targets: []targetDocument{{
			ID: "t", Provider: providerDocument{Name: "p", Version: "1"},
			Configuration: json.RawMessage(`{`), RequiredCapabilities: []protocol.Capability{protocol.CapabilityPlan},
		}},
	})
	if err == nil {
		t.Fatal("invalid semantic JSON accepted")
	}
	target, _ := domain.NewTargetID("t")
	name, _ := domain.NewProviderName("p")
	version, _ := domain.NewProviderVersion("1")
	ref, _ := domain.NewProviderRef(name, version)
	if _, err := buildOperationDrafts(target, ref, []protocol.PlannedOperation{{
		ID: "x", Kind: "x", Dependencies: []string{"missing"}, ProviderPayload: json.RawMessage(`{}`),
	}}); err == nil {
		t.Fatal("unknown dependency accepted")
	}
	drafts, err := buildOperationDrafts(target, ref, []protocol.PlannedOperation{{
		ID: "x", Kind: "x", TimeoutMillis: 123, ProviderPayload: json.RawMessage(`{}`),
	}})
	if err != nil {
		t.Fatal(err)
	}
	if drafts[0].timeout != 123*time.Millisecond {
		t.Fatalf("timeout=%v", drafts[0].timeout)
	}
}

func TestNewDefaults(t *testing.T) {
	c := fakeClient{describe: func(context.Context, Endpoint) (protocol.DescribeResponse, error) {
		return protocol.DescribeResponse{}, nil
	}, plan: func(context.Context, Endpoint, protocol.PlanRequest) (protocol.PlanResponse, error) {
		return protocol.PlanResponse{}, nil
	}}
	p, err := New(c, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if p.resolver == nil || p.hasher == nil {
		t.Fatal("defaults missing")
	}
}
