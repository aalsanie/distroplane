package providerretention

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/aalsanie/distroplane/internal/config"
	"github.com/aalsanie/distroplane/internal/domain"
	"github.com/aalsanie/distroplane/internal/planner"
	"github.com/aalsanie/distroplane/internal/protocol"
)

type retentionClient struct{}

func (retentionClient) Describe(context.Context, planner.Endpoint) (protocol.DescribeResponse, error) {
	return protocol.DescribeResponse{
		Provider:         protocol.ProviderIdentity{Name: "fake", Version: "1.2.3"},
		ProtocolVersions: []string{protocol.Version},
		Capabilities:     []protocol.Capability{protocol.CapabilityPlan, protocol.CapabilityApply, protocol.CapabilityReconcile},
	}, nil
}

func (retentionClient) Plan(context.Context, planner.Endpoint, protocol.PlanRequest) (protocol.PlanResponse, error) {
	return protocol.PlanResponse{Operations: []protocol.PlannedOperation{{
		ID: "publish", Kind: "publish", ProviderPayload: json.RawMessage(`{}`),
		SideEffecting: true,
	}}}, nil
}

func retentionFixture(t *testing.T) (config.Loaded, planner.DistributionPlan, domain.ProviderRef, string) {
	t.Helper()
	dir := t.TempDir()
	binary := filepath.Join(dir, "provider-original")
	if err := os.WriteFile(binary, []byte("provider-build-A"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "artifact.zip"), []byte("test archive"), 0600); err != nil {
		t.Fatal(err)
	}
	cfgPath := filepath.Join(dir, "distroplane.json")
	payload := fmt.Sprintf(`{"schemaVersion":"1","release":{"id":"release","artifacts":[{"name":"artifact","source":"artifact.zip"}]},"targets":[{"id":"publish","provider":{"name":"fake","executable":%q},"configuration":{}}]}`, binary)
	if err := os.WriteFile(cfgPath, []byte(payload), 0600); err != nil {
		t.Fatal(err)
	}
	loaded, err := config.Load(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	builder, err := planner.New(retentionClient{}, planner.Options{})
	if err != nil {
		t.Fatal(err)
	}
	plan, err := builder.Build(context.Background(), loaded)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, ".distroplane"), 0700); err != nil {
		t.Fatal(err)
	}
	ref := plan.Plan().Targets()[0].Provider()
	return loaded, plan, ref, binary
}

func TestRetainPreservesProviderAcrossUpgradeAndReinstall(t *testing.T) {
	loaded, plan, ref, binary := retentionFixture(t)
	if err := Retain(loaded, plan); err != nil {
		t.Fatal(err)
	}
	cacheRoot := filepath.Join(loaded.BaseDir, ".distroplane")
	path, trusted, err := Resolve(cacheRoot, ref, plan.ProviderDigests()[ref])
	if err != nil || trusted || path == binary {
		t.Fatalf("retained binding changed identity: path=%s trusted=%t err=%v", path, trusted, err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, []byte("provider-build-A")) {
		t.Fatalf("unexpected retained content %q", before)
	}
	if err := os.WriteFile(binary, []byte("provider-build-B"), 0700); err != nil {
		t.Fatal(err)
	}
	retained, _, err := Resolve(cacheRoot, ref, plan.ProviderDigests()[ref])
	if err != nil || retained != path {
		t.Fatalf("install upgrade changed immutable binding: %s %v", retained, err)
	}
	if err := os.Remove(binary); err != nil {
		t.Fatal(err)
	}
	if _, _, err := Resolve(cacheRoot, ref, plan.ProviderDigests()[ref]); err != nil {
		t.Fatalf("uninstall destroyed recovery: %v", err)
	}
	if err := Retain(loaded, plan); err == nil {
		t.Fatal("retention silently used a missing installed binary")
	}
	if _, _, err := Resolve(cacheRoot, ref, plan.ProviderDigests()[ref]); err != nil {
		t.Fatalf("failed re-retain lost preserved bytes: %v", err)
	}
}

func TestRetainIdempotenceAndVerifiedTrustedReceipt(t *testing.T) {
	loaded, plan, ref, source := retentionFixture(t)
	cacheRoot := filepath.Join(loaded.BaseDir, ".distroplane")
	original := sourceBinding{path: source, digest: plan.ProviderDigests()[ref], trusted: true}
	if err := retainOne(cacheRoot, ref, original); err != nil {
		t.Fatal(err)
	}
	if err := retainOne(cacheRoot, ref, original); err != nil {
		t.Fatalf("same immutable provider could not be retained twice: %v", err)
	}
	_, trusted, err := Resolve(cacheRoot, ref, original.digest)
	if err != nil || !trusted {
		t.Fatalf("trusted installed receipt not honored: %t %v", trusted, err)
	}
	original.trusted = false
	if err := retainOne(cacheRoot, ref, original); err == nil {
		t.Fatal("changed provenance overwrote immutable receipt")
	}
}

func TestRetainRejectsChangedSourceBeforePersistingPlan(t *testing.T) {
	loaded, plan, _, binary := retentionFixture(t)
	if err := os.WriteFile(binary, []byte("replaced after planning"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := Retain(loaded, plan); err == nil || !strings.Contains(err.Error(), "replaced") {
		t.Fatalf("changed provider accepted: %v", err)
	}
	if _, err := os.Stat(filepath.Join(loaded.BaseDir, ".distroplane", "providers")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("bad provider created persistent cache: %v", err)
	}
}

func TestRetainRejectsChangedConfigAndUnsafeRetentionRoot(t *testing.T) {
	loaded, plan, _, _ := retentionFixture(t)
	loaded.Config.Targets[0].Provider.Name = "other"
	if err := Retain(loaded, plan); err == nil || !strings.Contains(err.Error(), "no matching") {
		t.Fatalf("provider mismatch accepted: %v", err)
	}
	loaded, plan, _, _ = retentionFixture(t)
	root := filepath.Join(loaded.BaseDir, ".distroplane")
	if err := os.Remove(root); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(root, []byte("unsafe"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := Retain(loaded, plan); err == nil {
		t.Fatal("non-directory state root accepted")
	}
}

func TestResolveRejectsTamperedAndMissingProviderSnapshots(t *testing.T) {
	for _, mode := range []string{"missing", "tamper", "symlink", "manifest-missing", "manifest-tamper", "manifest-symlink", "receipt-private"} {
		t.Run(mode, func(t *testing.T) {
			loaded, plan, ref, _ := retentionFixture(t)
			if err := Retain(loaded, plan); err != nil {
				t.Fatal(err)
			}
			root := filepath.Join(loaded.BaseDir, ".distroplane")
			dir, executable, receipt, err := retentionPaths(root, ref.Name(), plan.ProviderDigests()[ref])
			if err != nil {
				t.Fatal(err)
			}
			switch mode {
			case "missing":
				err = os.Remove(executable)
			case "tamper":
				err = os.Chmod(executable, 0700)
				if err == nil {
					err = os.WriteFile(executable, []byte("malicious bytes"), 0700)
				}
			case "symlink":
				err = os.Remove(executable)
				if err == nil {
					err = os.Symlink(receipt, executable)
				}
			case "manifest-missing":
				err = os.Remove(receipt)
			case "manifest-tamper":
				err = os.WriteFile(receipt, []byte(`{"schemaVersion":"1","name":"fake","version":"other","digest":"bad","trusted":true}`), 0600)
			case "manifest-symlink":
				err = os.Remove(receipt)
				if err == nil {
					err = os.Symlink(executable, receipt)
				}
			case "receipt-private":
				if runtime.GOOS == "windows" {
					t.Skip("POSIX private permissions")
				}
				err = os.Chmod(receipt, 0644)
			}
			if err != nil {
				if mode == "symlink" || mode == "manifest-symlink" {
					t.Skipf("symlink unavailable: %v", err)
				}
				t.Fatal(err)
			}
			if _, _, err := Resolve(root, ref, plan.ProviderDigests()[ref]); err == nil {
				t.Fatalf("unsafe provider %s accepted from %s", mode, dir)
			}
		})
	}
}

func TestRetainRejectsCorruptCachedProviderAndNeverReplaces(t *testing.T) {
	loaded, plan, ref, _ := retentionFixture(t)
	if err := Retain(loaded, plan); err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(loaded.BaseDir, ".distroplane")
	_, path, _, _ := retentionPaths(root, ref.Name(), plan.ProviderDigests()[ref])
	if err := os.Chmod(path, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("corrupt snapshot"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := Retain(loaded, plan); err == nil {
		t.Fatal("tampered cache was replaced silently")
	}
	actual, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(actual, []byte("corrupt snapshot")) {
		t.Fatalf("tampered bytes unexpectedly overwritten: %v %q", err, actual)
	}
}

func TestRetentionPathValidationAndPermissions(t *testing.T) {
	loaded, plan, ref, _ := retentionFixture(t)
	root := filepath.Join(loaded.BaseDir, ".distroplane")
	digest := plan.ProviderDigests()[ref]
	for _, tc := range []struct {
		root   string
		name   domain.ProviderName
		digest domain.Digest
	}{
		{"", ref.Name(), digest},
		{filepath.Join(t.TempDir(), "wrong"), ref.Name(), digest},
		{root, domain.ProviderName("../unsafe"), digest},
		{root, domain.ProviderName(".hidden"), digest},
		{root, ref.Name(), domain.Digest{}},
	} {
		if _, _, _, err := retentionPaths(tc.root, tc.name, tc.digest); err == nil {
			t.Fatalf("invalid retention identity accepted: %q %q", tc.root, tc.name)
		}
	}
	for _, name := range []string{"", "..", ".hidden", "x/../y", "name\\evil", strings.Repeat("a", 129)} {
		if safeName(name) {
			t.Fatalf("unsafe provider name accepted %q", name)
		}
	}
	for _, name := range []string{"npm", "homebrew", "x-y", "x_y", "x.y"} {
		if !safeName(name) {
			t.Fatalf("valid provider name rejected %q", name)
		}
	}
	if err := privateDirs(root, filepath.Join(root, "providers", "fake", "digest"), false); err == nil {
		t.Fatal("missing retention hierarchy accepted")
	}
	if err := privateDirs(root, filepath.Join(root, "providers", "fake", "digest"), true); err != nil {
		t.Fatal(err)
	}
	if err := privateDirs(root, filepath.Join(root, "providers", "fake", "digest"), false); err != nil {
		t.Fatal(err)
	}
}

func TestSnapshotDigestAndExclusiveReceiptFailures(t *testing.T) {
	loaded, plan, ref, source := retentionFixture(t)
	root := filepath.Join(loaded.BaseDir, ".distroplane")
	dir, path, metadata, err := retentionPaths(root, ref.Name(), plan.ProviderDigests()[ref])
	if err != nil {
		t.Fatal(err)
	}
	if err := privateDirs(root, dir, true); err != nil {
		t.Fatal(err)
	}
	wrong, _ := domain.NewSHA256Digest(strings.Repeat("a", 64))
	if err := snapshot(source, path, wrong); err == nil {
		t.Fatal("incorrect source digest accepted")
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("invalid copy persisted: %v", err)
	}
	if err := snapshot(source, path, plan.ProviderDigests()[ref]); err != nil {
		t.Fatal(err)
	}
	if err := snapshot(source, path, plan.ProviderDigests()[ref]); err != nil {
		t.Fatalf("identical concurrent snapshot rejected: %v", err)
	}
	if err := immutableWrite(metadata, []byte("receipt-1"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := immutableWrite(metadata, []byte("receipt-1"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := immutableWrite(metadata, []byte("receipt-2"), 0600); err == nil {
		t.Fatal("receipt overwrite accepted")
	}
}

type failingReader struct{}

func (failingReader) Read([]byte) (int, error) { return 0, errors.New("reader crashed") }

func TestRetentionFailsOnIOErrorsAndSpecialSources(t *testing.T) {
	loaded, plan, ref, source := retentionFixture(t)
	root := filepath.Join(loaded.BaseDir, ".distroplane")
	dir, executable, receipt, err := retentionPaths(root, ref.Name(), plan.ProviderDigests()[ref])
	if err != nil {
		t.Fatal(err)
	}
	if err := privateDirs(root, dir, true); err != nil {
		t.Fatal(err)
	}
	if err := writeCopiedExecutable(failingReader{}, executable, plan.ProviderDigests()[ref]); err == nil {
		t.Fatal("failed read accepted")
	}
	if err := os.Remove(source); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(source, 0700); err != nil {
		t.Fatal(err)
	}
	if err := snapshot(source, executable, plan.ProviderDigests()[ref]); err == nil {
		t.Fatal("directory source accepted")
	}
	if err := immutableWrite(receipt, []byte("test"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(receipt); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(receipt, 0700); err != nil {
		t.Fatal(err)
	}
	if err := immutableWrite(receipt, []byte("test"), 0600); err == nil {
		t.Fatal("directory receipt accepted")
	}
}

func TestProviderRetentionSymlinkAndPublicDirectoryRejection(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permissions and symlinks")
	}
	loaded, plan, ref, _ := retentionFixture(t)
	root := filepath.Join(loaded.BaseDir, ".distroplane")
	dir, _, _, err := retentionPaths(root, ref.Name(), plan.ProviderDigests()[ref])
	if err != nil {
		t.Fatal(err)
	}
	if err := privateDirs(root, dir, true); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(filepath.Join(root, "providers"), 0777); err != nil {
		t.Fatal(err)
	}
	if err := privateDirs(root, dir, false); err == nil {
		t.Fatal("public cache accepted")
	}
	if err := os.Chmod(filepath.Join(root, "providers"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(dir); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(loaded.BaseDir, dir); err != nil {
		t.Fatal(err)
	}
	if err := privateDirs(root, dir, false); err == nil {
		t.Fatal("linked cache accepted")
	}
}

func TestRetainRejectsUnapprovedProviderBinarySource(t *testing.T) {
	loaded, plan, ref, source := retentionFixture(t)
	if err := os.Remove(source); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(loaded.BaseDir, "artifact.zip"), source); err != nil {
		if runtime.GOOS == "windows" {
			t.Skipf("symlink privileges unavailable: %v", err)
		}
		t.Fatal(err)
	}
	if err := retainOne(filepath.Join(loaded.BaseDir, ".distroplane"), ref, sourceBinding{path: source, digest: plan.ProviderDigests()[ref]}); err == nil {
		t.Fatal("symlinked provider source retained")
	}
}

var _ io.Reader = failingReader{}
