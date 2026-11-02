package providerretention

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aalsanie/distroplane/internal/domain"
)

func TestRetainedProviderNamesAndPathsFailClosed(t *testing.T) {
	for _, name := range []string{"npm", "sdkman-v2", "homebrew_tap", "winget.2026"} {
		if !safeName(name) {
			t.Errorf("valid provider name rejected: %q", name)
		}
	}
	for _, name := range []string{"", ".hidden", "../escape", "bad/name", "bad name", "bad\n", strings.Repeat("a", 129)} {
		if safeName(name) {
			t.Errorf("unsafe provider name accepted: %q", name)
		}
	}
	digest, err := domain.NewSHA256Digest(strings.Repeat("a", 64))
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(t.TempDir(), ".distroplane")
	directory, executable, manifest, err := retentionPaths(root, domain.ProviderName("npm"), digest)
	if err != nil || !strings.HasPrefix(executable, directory) || !strings.HasPrefix(manifest, directory) {
		t.Fatalf("provider cache paths: %q %q %q err=%v", directory, executable, manifest, err)
	}
	for _, tc := range []struct {
		root   string
		name   domain.ProviderName
		digest domain.Digest
	}{
		{root, domain.ProviderName("bad/name"), digest},
		{root, domain.ProviderName("npm"), domain.Digest{}},
		{filepath.Join(t.TempDir(), "wrong"), domain.ProviderName("npm"), digest},
		{"", domain.ProviderName("npm"), digest},
	} {
		if _, _, _, err := retentionPaths(tc.root, tc.name, tc.digest); err == nil {
			t.Fatalf("unsafe cache accepted: %+v", tc)
		}
	}
	if _, _, err := Resolve(root, domain.ProviderRef{}, digest); err == nil {
		t.Fatal("unbound provider identity accepted")
	}
	if err := privateDirs(root, directory, false); err == nil {
		t.Fatal("missing provider cache accepted")
	}
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	unsafe := filepath.Join(root, "providers")
	if err := os.WriteFile(unsafe, []byte("wrong type"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := privateDirs(root, directory, true); err == nil {
		t.Fatal("non-directory provider cache accepted")
	}
}
