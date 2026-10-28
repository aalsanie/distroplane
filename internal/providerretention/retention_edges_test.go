package providerretention

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/aalsanie/distroplane/internal/domain"
	"github.com/aalsanie/distroplane/internal/planner"
)

func TestRetainRecoversInterruptedReceiptCreation(t *testing.T) {
	loaded, plan, ref, source := retentionFixture(t)
	root := filepath.Join(loaded.BaseDir, ".distroplane")
	s := sourceBinding{path: source, digest: plan.ProviderDigests()[ref]}
	if err := retainOne(root, ref, s); err != nil {
		t.Fatal(err)
	}
	_, executable, receipt, err := retentionPaths(root, ref.Name(), s.digest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(receipt); err != nil {
		t.Fatal(err)
	}
	if err := retainOne(root, ref, s); err != nil {
		t.Fatalf("incomplete receipt recovery: %v", err)
	}
	if _, _, err := Resolve(root, ref, s.digest); err != nil {
		t.Fatalf("retained provider inaccessible after repair: %v", err)
	}
	if _, err := os.Stat(executable); err != nil {
		t.Fatal(err)
	}
}

func TestRetainedProviderReceiptRejectsMalformedAndConflictingMetadata(t *testing.T) {
	for _, tc := range []struct {
		name    string
		receipt string
	}{
		{"invalid JSON", "{"},
		{"truncated", ""},
		{"unknown fields", `{"schemaVersion":"1","name":"fake","version":"1.2.3","digest":"bad","trusted":false,"injected":true}`},
		{"wrong version", `{"schemaVersion":"1","name":"fake","version":"upgraded","digest":"bad","trusted":false}`},
		{"wrong name", `{"schemaVersion":"1","name":"malicious","version":"1.2.3","digest":"bad","trusted":false}`},
		{"trailing document", `{"schemaVersion":"1","name":"fake","version":"1.2.3","digest":"bad","trusted":false} {}`},
		{"wrong schema", `{"schemaVersion":"2","name":"fake","version":"1.2.3","digest":"bad","trusted":false}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			loaded, plan, ref, _ := retentionFixture(t)
			if err := Retain(loaded, plan); err != nil {
				t.Fatal(err)
			}
			root := filepath.Join(loaded.BaseDir, ".distroplane")
			_, _, receipt, err := retentionPaths(root, ref.Name(), plan.ProviderDigests()[ref])
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(receipt, []byte(tc.receipt), 0600); err != nil {
				t.Fatal(err)
			}
			if _, _, err := Resolve(root, ref, plan.ProviderDigests()[ref]); err == nil {
				t.Fatalf("unsafe receipt %q accepted", tc.name)
			}
		})
	}
}

func TestRetainedProviderRejectsCrossVersionBinding(t *testing.T) {
	loaded, plan, ref, source := retentionFixture(t)
	root := filepath.Join(loaded.BaseDir, ".distroplane")
	digest := plan.ProviderDigests()[ref]
	if err := retainOne(root, ref, sourceBinding{path: source, digest: digest}); err != nil {
		t.Fatal(err)
	}
	name := ref.Name()
	otherVersion, err := domain.NewProviderVersion("9.9.9")
	if err != nil {
		t.Fatal(err)
	}
	otherRef, err := domain.NewProviderRef(name, otherVersion)
	if err != nil {
		t.Fatal(err)
	}
	if err := retainOne(root, otherRef, sourceBinding{path: source, digest: digest}); err == nil {
		t.Fatal("provider cache accepted a new version with the same pinned digest")
	}
	if _, _, err := Resolve(root, otherRef, digest); err == nil {
		t.Fatal("provider lookup ignored pinned version")
	}
}

func TestRetainFailsOnMissingConfiguredProviderAndNoRoot(t *testing.T) {
	loaded, plan, _, _ := retentionFixture(t)
	loaded.Config.Targets[0].Provider.Executable = filepath.Join(loaded.BaseDir, "deleted-provider")
	if err := Retain(loaded, plan); err == nil {
		t.Fatal("missing configured provider accepted")
	}
	loaded, plan, _, _ = retentionFixture(t)
	if err := os.Remove(filepath.Join(loaded.BaseDir, ".distroplane")); err != nil {
		t.Fatal(err)
	}
	if err := Retain(loaded, plan); err == nil {
		t.Fatal("missing project state directory silently created")
	}
}

func TestRetainedExecutableRejectsModesAndChangedBytes(t *testing.T) {
	loaded, plan, ref, _ := retentionFixture(t)
	if err := Retain(loaded, plan); err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(loaded.BaseDir, ".distroplane")
	_, executable, _, err := retentionPaths(root, ref.Name(), plan.ProviderDigests()[ref])
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" {
		for _, mode := range []os.FileMode{0600, 0777} {
			if err := os.Chmod(executable, mode); err != nil {
				t.Fatal(err)
			}
			info, err := os.Lstat(executable)
			if err != nil {
				t.Fatal(err)
			}
			if err := verifyExecutable(executable, info, plan.ProviderDigests()[ref]); err == nil {
				t.Fatalf("unsafe executable mode %o allowed", mode)
			}
		}
	}
	if err := os.Chmod(executable, 0700); err != nil {
		t.Fatal(err)
	}
	info, err := os.Lstat(executable)
	if err != nil {
		t.Fatal(err)
	}
	wrong, _ := domain.NewSHA256Digest(strings.Repeat("f", 64))
	if err := verifyExecutable(executable, info, wrong); err == nil {
		t.Fatal("wrong executable digest accepted")
	}
	if err := verifyExecutable(filepath.Dir(executable), info, plan.ProviderDigests()[ref]); err == nil {
		t.Fatal("directory hashed as executable")
	}
}

func TestSnapshotRefusesUnwritableDestinationAndUnsafeSources(t *testing.T) {
	loaded, plan, ref, source := retentionFixture(t)
	root := filepath.Join(loaded.BaseDir, ".distroplane")
	dir, executable, receipt, err := retentionPaths(root, ref.Name(), plan.ProviderDigests()[ref])
	if err != nil {
		t.Fatal(err)
	}
	if err := privateDirs(root, dir, true); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(executable, 0700); err != nil {
		t.Fatal(err)
	}
	if err := snapshot(source, executable, plan.ProviderDigests()[ref]); err == nil {
		t.Fatal("snapshot could overwrite directory")
	}
	if err := os.Remove(executable); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(receipt, 0700); err != nil {
		t.Fatal(err)
	}
	if err := immutableWrite(receipt, []byte("{}"), 0600); err == nil {
		t.Fatal("receipt overwrote directory")
	}
	if err := os.Remove(receipt); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(source, receipt); err == nil {
		if err := immutableWrite(receipt, []byte("{}"), 0600); err == nil {
			t.Fatal("symlinked receipt modified")
		}
	}
	if err := snapshot(filepath.Join(loaded.BaseDir, "not-found"), executable, plan.ProviderDigests()[ref]); err == nil {
		t.Fatal("nonexistent source accepted")
	}
}

func TestRetentionSafelyRejectsInvalidInstallPaths(t *testing.T) {
	loaded, plan, ref, source := retentionFixture(t)
	root := filepath.Join(loaded.BaseDir, ".distroplane")
	digest := plan.ProviderDigests()[ref]
	dir, executable, receipt, err := retentionPaths(root, ref.Name(), digest)
	if err != nil {
		t.Fatal(err)
	}
	if err := privateDirs(root, dir, true); err != nil {
		t.Fatal(err)
	}
	invalidParent := filepath.Join(dir, "missing", "provider")
	if err := writeCopiedExecutable(bytes.NewBufferString("provider-build-A"), invalidParent, digest); err == nil {
		t.Fatal("missing destination parent silently created")
	}
	if err := immutableWrite(filepath.Join(dir, "no-such-parent", "receipt"), []byte("{}"), 0600); err == nil {
		t.Fatal("missing metadata directory accepted")
	}
	if err := immutableWrite(receipt, []byte("valid"), 0600); err != nil {
		t.Fatal(err)
	}
	malformed := []byte("malformed")
	if err := os.WriteFile(receipt, malformed, 0600); err != nil {
		t.Fatal(err)
	}
	if err := retainOne(root, ref, sourceBinding{path: source, digest: digest}); err == nil {
		t.Fatal("conflicting metadata receipt silently replaced")
	}
	if _, err := os.Stat(executable); err != nil {
		t.Fatal(err)
	}
}

func TestRetainedSnapshotFailurePreservesNoExecutable(t *testing.T) {
	loaded, plan, ref, _ := retentionFixture(t)
	root := filepath.Join(loaded.BaseDir, ".distroplane")
	digest := plan.ProviderDigests()[ref]
	dir, executable, _, err := retentionPaths(root, ref.Name(), digest)
	if err != nil {
		t.Fatal(err)
	}
	if err := privateDirs(root, dir, true); err != nil {
		t.Fatal(err)
	}
	wrongDigest, _ := domain.NewSHA256Digest(strings.Repeat("0", 64))
	if err := writeCopiedExecutable(bytes.NewBufferString("provider-build-A"), executable, wrongDigest); err == nil {
		t.Fatal("wrong digest was persisted")
	}
	if _, err := os.Lstat(executable); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("incorrect snapshot left cache file: %v", err)
	}
}

func TestPrivateRetentionHierarchyRejectsSymlinkedRoots(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks require privileges on Windows")
	}
	dir := t.TempDir()
	realRoot := filepath.Join(dir, "real")
	if err := os.Mkdir(realRoot, 0700); err != nil {
		t.Fatal(err)
	}
	linkedRoot := filepath.Join(dir, ".distroplane")
	if err := os.Symlink(realRoot, linkedRoot); err != nil {
		t.Fatal(err)
	}
	if err := privateDirs(linkedRoot, filepath.Join(linkedRoot, "providers", "fake", "digest"), true); err == nil {
		t.Fatal("linked state root accepted")
	}
}

func TestRetainSameProviderAcrossTargetsStillPinsOnce(t *testing.T) {
	loaded, _, _, source := retentionFixture(t)
	copied := loaded.Config.Targets[0]
	copied.ID = "second"
	loaded.Config.Targets = append(loaded.Config.Targets, copied)
	builder, err := planner.New(retentionClient{}, planner.Options{})
	if err != nil {
		t.Fatal(err)
	}
	plan, err := builder.Build(context.Background(), loaded)
	if err != nil {
		t.Fatal(err)
	}
	if err := Retain(loaded, plan); err != nil {
		t.Fatalf("shared provider not retained: %v", err)
	}
	digests := plan.ProviderDigests()
	if len(digests) != 1 {
		t.Fatalf("multiple digests for same provider: %d", len(digests))
	}
	for ref, digest := range digests {
		cached, _, err := Resolve(filepath.Join(loaded.BaseDir, ".distroplane"), ref, digest)
		if err != nil || cached == source {
			t.Fatalf("unexpected shared snapshot: %q %v", cached, err)
		}
	}
}

func TestRetainedReceiptStableJSON(t *testing.T) {
	loaded, plan, ref, _ := retentionFixture(t)
	if err := Retain(loaded, plan); err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(loaded.BaseDir, ".distroplane")
	_, _, receiptPath, err := retentionPaths(root, ref.Name(), plan.ProviderDigests()[ref])
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(receiptPath)
	if err != nil {
		t.Fatal(err)
	}
	var stored receipt
	if err := json.Unmarshal(data, &stored); err != nil {
		t.Fatal(err)
	}
	if stored.Name != string(ref.Name()) || stored.Version != string(ref.Version()) || stored.Trusted {
		t.Fatalf("wrong receipt content: %+v", stored)
	}
	if err := immutableWrite(receiptPath, data, 0600); err != nil {
		t.Fatalf("idempotent receipt rejected: %v", err)
	}
	if err := immutableWrite(receiptPath, []byte(fmt.Sprintf("%q", "different")), 0600); err == nil {
		t.Fatal("receipt content altered")
	}
}
