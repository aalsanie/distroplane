package credentials

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/aalsanie/distroplane/internal/domain"
)

type resolverFunc func(context.Context, domain.CredentialRef) (*Material, error)

func (f resolverFunc) Resolve(ctx context.Context, ref domain.CredentialRef) (*Material, error) {
	return f(ctx, ref)
}

func credentialRef(t testing.TB, value string) domain.CredentialRef {
	t.Helper()
	ref, err := domain.NewCredentialRef(value)
	if err != nil {
		t.Fatal(err)
	}
	return ref
}

func TestMaterialLifecycleAndFormatting(t *testing.T) {
	if _, err := newMaterial(nil); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("err=%v", err)
	}
	ref := credentialRef(t, "release")
	source := []byte("canary-material")
	resolver, err := NewStaticResolver(map[domain.CredentialRef][]byte{ref: source})
	if err != nil {
		t.Fatal(err)
	}
	source[0] = 'X'
	material, err := resolver.Resolve(context.Background(), ref)
	if err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(material) != "[REDACTED]" || fmt.Sprintf("%#v", material) != "credentials.Material{[REDACTED]}" {
		t.Fatalf("material formatting leaked: %v %#v", material, material)
	}
	if _, err := json.Marshal(material); err == nil {
		t.Fatal("credential material serialized")
	}
	first, err := material.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	if string(first) != "canary-material" {
		t.Fatalf("material=%q", first)
	}
	first[0] = 'X'
	second, err := material.Bytes()
	if err != nil || string(second) != "canary-material" {
		t.Fatalf("second=%q err=%v", second, err)
	}
	material.Release()
	material.Release()
	if _, err := material.Bytes(); !errors.Is(err, ErrReleased) {
		t.Fatalf("err=%v", err)
	}
	var nilMaterial *Material
	nilMaterial.Release()
	if _, err := nilMaterial.Bytes(); !errors.Is(err, ErrReleased) {
		t.Fatalf("nil err=%v", err)
	}
}

func TestStaticResolverValidationAndIsolation(t *testing.T) {
	ref := credentialRef(t, "release")
	if _, err := NewStaticResolver(map[domain.CredentialRef][]byte{"": []byte("x")}); err == nil {
		t.Fatal("invalid reference accepted")
	}
	if _, err := NewStaticResolver(map[domain.CredentialRef][]byte{ref: nil}); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("err=%v", err)
	}
	resolver, err := NewStaticResolver(map[domain.CredentialRef][]byte{ref: []byte("secret")})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := resolver.Resolve(context.Background(), ""); err == nil {
		t.Fatal("invalid reference accepted")
	}
	missing := credentialRef(t, "missing")
	if _, err := resolver.Resolve(context.Background(), missing); !errors.Is(err, ErrNotFound) || strings.Contains(err.Error(), "secret") {
		t.Fatalf("err=%v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := resolver.Resolve(ctx, ref); !errors.Is(err, context.Canceled) {
		t.Fatalf("err=%v", err)
	}
	if _, err := resolver.Resolve(nil, ref); err == nil {
		t.Fatal("nil context accepted")
	}
	var nilResolver *StaticResolver
	if _, err := nilResolver.Resolve(context.Background(), ref); err == nil {
		t.Fatal("nil resolver accepted")
	}
}

func TestEnvironmentResolver(t *testing.T) {
	ref := credentialRef(t, "release")
	if _, err := NewEnvironmentResolver(map[domain.CredentialRef]string{"": "TOKEN"}); err == nil {
		t.Fatal("invalid reference accepted")
	}
	if _, err := NewEnvironmentResolver(map[domain.CredentialRef]string{ref: "bad-name"}); err == nil {
		t.Fatal("invalid environment accepted")
	}
	resolver, err := NewEnvironmentResolver(map[domain.CredentialRef]string{ref: "DISTROPLANE_TEST_CREDENTIAL"})
	if err != nil {
		t.Fatal(err)
	}
	_ = os.Unsetenv("DISTROPLANE_TEST_CREDENTIAL")
	if _, err := resolver.Resolve(context.Background(), ref); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("err=%v", err)
	}
	t.Setenv("DISTROPLANE_TEST_CREDENTIAL", "environment-canary")
	material, err := resolver.Resolve(context.Background(), ref)
	if err != nil {
		t.Fatal(err)
	}
	value, _ := material.Bytes()
	material.Release()
	if string(value) != "environment-canary" {
		t.Fatalf("value=%q", value)
	}
	if _, err := resolver.Resolve(context.Background(), credentialRef(t, "other")); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err=%v", err)
	}
	var nilResolver *EnvironmentResolver
	if _, err := nilResolver.Resolve(context.Background(), ref); err == nil {
		t.Fatal("nil resolver accepted")
	}
	for _, value := range []string{"", "1TOKEN", "BAD-NAME", "BAD=NAME", "BAD\x00NAME"} {
		if validEnvironmentName(value) {
			t.Fatalf("accepted %q", value)
		}
	}
	for _, value := range []string{"TOKEN", "_TOKEN", "Token_123"} {
		if !validEnvironmentName(value) {
			t.Fatalf("rejected %q", value)
		}
	}
}

func TestChainResolution(t *testing.T) {
	ref := credentialRef(t, "release")
	first, _ := NewStaticResolver(nil)
	second, _ := NewStaticResolver(map[domain.CredentialRef][]byte{ref: []byte("chain-secret")})
	chain, err := NewChain(first, second)
	if err != nil {
		t.Fatal(err)
	}
	material, err := chain.Resolve(context.Background(), ref)
	if err != nil {
		t.Fatal(err)
	}
	material.Release()
	if _, err := chain.Resolve(context.Background(), credentialRef(t, "missing")); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err=%v", err)
	}
	stop, _ := NewChain(resolverFunc(func(context.Context, domain.CredentialRef) (*Material, error) {
		return nil, ErrUnavailable
	}), second)
	if _, err := stop.Resolve(context.Background(), ref); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("err=%v", err)
	}
	if _, err := NewChain(); err == nil {
		t.Fatal("empty chain accepted")
	}
	if _, err := NewChain(first, nil); err == nil {
		t.Fatal("nil resolver accepted")
	}
	var nilChain *Chain
	if _, err := nilChain.Resolve(context.Background(), ref); err == nil {
		t.Fatal("nil chain accepted")
	}
}

func TestParseRequirement(t *testing.T) {
	requirement, err := domain.NewRequirement("credential", "release", []byte(`{"environment":"RELEASE_TOKEN","scope":"write"}`))
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := ParseRequirement(requirement)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Ref != credentialRef(t, "release") || parsed.Environment != "RELEASE_TOKEN" {
		t.Fatalf("parsed=%+v", parsed)
	}
	other, _ := domain.NewRequirement("network", "registry", nil)
	if _, err := ParseRequirement(other); err == nil {
		t.Fatal("non-credential requirement accepted")
	}
	missing, _ := domain.NewRequirement("credential", "release", nil)
	if _, err := ParseRequirement(missing); err == nil {
		t.Fatal("missing metadata accepted")
	}
	bad, _ := domain.NewRequirement("credential", "release", []byte(`{"environment":"bad-name"}`))
	if _, err := ParseRequirement(bad); err == nil {
		t.Fatal("bad environment accepted")
	}
	wrongType, _ := domain.NewRequirement("credential", "release", []byte(`{"environment":1}`))
	if _, err := ParseRequirement(wrongType); err == nil {
		t.Fatal("invalid metadata type accepted")
	}
}
