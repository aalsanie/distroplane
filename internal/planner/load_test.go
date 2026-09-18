package planner

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aalsanie/distroplane/internal/protocol"
)

func TestDecodePlanRoundTripAndTamperDetection(t *testing.T) {
	planner := newPlanner(t,
		describe("1.0.0", protocol.CapabilityPlan, protocol.CapabilityApply, protocol.CapabilityReconcile),
		planResponse(`{"channel":"stable"}`),
		mustDigest(t, 'a'),
	)
	built, err := planner.Build(t.Context(), baseLoaded(t, baseConfig(`{"id":"primary","provider":{"name":"fake"},"configuration":{}}`)))
	if err != nil {
		t.Fatal(err)
	}
	raw, err := built.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := Decode(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	if loaded.ID() != built.ID() {
		t.Fatalf("id=%q want %q", loaded.ID(), built.ID())
	}
	reencoded, err := loaded.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(reencoded, raw) {
		t.Fatalf("round trip changed plan\n%s\n%s", raw, reencoded)
	}

	tampered := bytes.Replace(raw, []byte(`"kind":"publish"`), []byte(`"kind":"tampered"`), 1)
	if _, err := Decode(bytes.NewReader(tampered)); err == nil || !strings.Contains(err.Error(), "does not match plan content") {
		t.Fatalf("tamper err=%v", err)
	}
	unknown := bytes.Replace(raw, []byte(`"schemaVersion":"1"`), []byte(`"schemaVersion":"1","unexpected":true`), 1)
	if _, err := Decode(bytes.NewReader(unknown)); err == nil {
		t.Fatal("unknown plan field accepted")
	}
	if _, err := Decode(nil); err == nil {
		t.Fatal("nil reader accepted")
	}
	if _, err := Decode(strings.NewReader(strings.Repeat(" ", MaxPlanBytes+1))); err == nil {
		t.Fatal("oversized plan accepted")
	}
}

func TestLoadAndVerifyArtifacts(t *testing.T) {
	dir := t.TempDir()
	artifactPath := filepath.Join(dir, "app")
	if err := os.WriteFile(artifactPath, []byte("abc"), 0o600); err != nil {
		t.Fatal(err)
	}
	digest, size, err := FileHasher{}.Hash(artifactPath)
	if err != nil {
		t.Fatal(err)
	}
	cfg := baseLoaded(t, baseConfig(`{"id":"primary","provider":{"name":"fake"},"configuration":{}}`))
	cfg.BaseDir = dir
	cfg.Config.Release.Artifacts[0].Source = "app"
	planner := newPlanner(t,
		describe("1.0.0", protocol.CapabilityPlan, protocol.CapabilityApply, protocol.CapabilityReconcile),
		planResponse(`{"channel":"stable"}`), digest,
	)
	planner.hasher = fakeHasher{digest: digest, size: size}
	built, err := planner.Build(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := built.Bytes()
	path := filepath.Join(dir, "plan.json")
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := VerifyArtifacts(loaded.Plan(), nil); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(artifactPath, []byte("abd"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := VerifyArtifacts(loaded.Plan(), nil); err == nil || !strings.Contains(err.Error(), "digest changed") {
		t.Fatalf("err=%v", err)
	}
	if _, err := Load(""); err == nil {
		t.Fatal("empty plan path accepted")
	}
	if _, err := Load(filepath.Join(dir, "missing")); err == nil {
		t.Fatal("missing plan accepted")
	}
}

func TestResolveExecutableRelativeToBaseDir(t *testing.T) {
	dir := t.TempDir()
	name := "provider"
	if os.PathSeparator == '\\' {
		name += ".exe"
	}
	path := filepath.Join(dir, "bin", name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("x"), 0o700); err != nil {
		t.Fatal(err)
	}
	resolved, err := ResolveExecutable(dir, "fake", filepath.Join("bin", name))
	if err != nil {
		t.Fatal(err)
	}
	absolute, _ := filepath.Abs(path)
	if resolved != filepath.Clean(absolute) {
		t.Fatalf("resolved=%q want %q", resolved, absolute)
	}
	if _, err := ResolveExecutable("", "fake", filepath.Join("bin", name)); err == nil {
		t.Fatal("missing base directory accepted")
	}
}
