package planner

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aalsanie/distroplane/internal/protocol"
)

func TestProviderDigestDocumentFailureBranches(t *testing.T) {
	digest := mustDigest(t, 'b')
	creator, loaded := providerDigestPlanner(t, digest)
	planned, err := creator.Build(context.Background(), loaded)
	if err != nil {
		t.Fatal(err)
	}
	t.Run("uninitialized", func(t *testing.T) {
		if err := (DistributionPlan{}).VerifyProviderDigests(); err == nil {
			t.Fatal("uninitialized plan was accepted")
		}
	})
	for _, tc := range []struct {
		name string
		digest string
		providerName string
	}{
		{"missing digest", "", "fake"},
		{"invalid digest", "invalid", "fake"},
		{"invalid provider", digest.String(), "bad name"},
		{"valid digest", digest.String(), "fake"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			value := planned
			value.document.Targets = append([]targetDocument(nil), planned.document.Targets...)
			value.document.Targets[0].Provider.Digest = tc.digest
			value.document.Targets[0].Provider.Name = tc.providerName
			got := value.ProviderDigests()
			if tc.name == "valid digest" && len(got) != 1 {
				t.Fatalf("valid binding missing: %+v", got)
			}
			if tc.name != "valid digest" && len(got) != 0 {
				t.Fatalf("invalid provider binding accepted: %+v", got)
			}
		})
	}
}

func TestRequirementCanonicalizationEmptyDedupAndInvalid(t *testing.T) {
	if got, err := canonicalRequirement(protocol.Requirement{Kind: "credential", Name: "name"}); err != nil || len(got.Metadata) != 0 {
		t.Fatalf("optional metadata: %+v err=%v", got, err)
	}
	one := protocol.Requirement{Kind: "credential", Name: "token", Metadata: json.RawMessage(`{"a":1,"b":2}`)}
	two := protocol.Requirement{Kind: "credential", Name: "token", Metadata: json.RawMessage(`{"b":2,"a":1}`)}
	result, err := canonicalRequirements([]protocol.Requirement{one, two, {Kind: "env", Name: "other"}})
	if err != nil || len(result) != 2 {
		t.Fatalf("canonical requirements: %+v err=%v", result, err)
	}
	for _, input := range []protocol.Requirement{
		{Kind: "", Name: "name"},
		{Kind: "credential", Name: ""},
		{Kind: "credential", Name: "name", Metadata: json.RawMessage(`{"x":`)},
	} {
		if _, err := canonicalRequirements([]protocol.Requirement{input}); err == nil {
			t.Errorf("invalid requirement accepted: %+v", input)
		}
	}
}

func TestFileHasherRejectsFilesystemMutations(t *testing.T) {
	if _, _, err := (FileHasher{}).Hash(filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Fatal("missing artifact accepted")
	}
	if _, _, err := (FileHasher{}).Hash(t.TempDir()); err == nil {
		t.Fatal("directory accepted as an artifact")
	}
	path := filepath.Join(t.TempDir(), "artifact")
	if err := os.WriteFile(path, []byte("payload"), 0o600); err != nil {
		t.Fatal(err)
	}
	hash, size, err := (FileHasher{}).Hash(path)
	if err != nil || size != 7 || !hash.Valid() {
		t.Fatalf("valid artifact: hash=%s size=%d err=%v", hash, size, err)
	}
	if _, _, err := (FileHasher{afterRead: func() {
		_ = os.WriteFile(path, []byte(strings.Repeat("changed", 2)), 0o600)
	}}).Hash(path); err == nil {
		t.Fatal("artifact mutation during hashing accepted")
	}
}
