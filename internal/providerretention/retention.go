package providerretention

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"github.com/aalsanie/distroplane/internal/config"
	"github.com/aalsanie/distroplane/internal/domain"
	"github.com/aalsanie/distroplane/internal/planner"
)

const schemaVersion = "1"

type receipt struct {
	SchemaVersion string `json:"schemaVersion"`
	Name          string `json:"name"`
	Version       string `json:"version"`
	Digest        string `json:"digest"`
	Trusted       bool   `json:"trusted"`
}

type sourceBinding struct {
	path    string
	digest  domain.Digest
	trusted bool
}

// Retain stores executable bytes in project-owned, content-addressed private
// storage before a plan is registered. Installed files remain installer-owned;
// release history and pinned executables are never subject to installer GC.
func Retain(loaded config.Loaded, plan planner.DistributionPlan) error {
	if err := plan.VerifyProviderDigests(); err != nil {
		return err
	}
	configuration := make(map[string]config.Target, len(loaded.Config.Targets))
	for _, target := range loaded.Config.Targets {
		configuration[target.ID] = target
	}
	digests := plan.ProviderDigests()
	sources := make(map[domain.ProviderRef]sourceBinding, len(digests))
	for _, target := range plan.Plan().Targets() {
		cfg, ok := configuration[string(target.ID())]
		if !ok || cfg.Provider.Name != string(target.Provider().Name()) {
			return fmt.Errorf("planned target %q has no matching configured provider", target.ID())
		}
		source, err := planner.ResolveExecutable(loaded.BaseDir, cfg.Provider.Name, cfg.Provider.Executable)
		if err != nil {
			return fmt.Errorf("resolve provider %q: %w", cfg.Provider.Name, err)
		}
		digest := digests[target.Provider()]
		if !digest.Valid() {
			return fmt.Errorf("planned provider %q is missing its digest", target.Provider().Name())
		}
		actual, _, err := (planner.FileHasher{}).Hash(source)
		if err != nil {
			return fmt.Errorf("hash provider %q: %w", cfg.Provider.Name, err)
		}
		if actual != digest {
			return fmt.Errorf("provider %q was replaced since plan creation; no plan persisted", cfg.Provider.Name)
		}
		trusted := cfg.Provider.Executable == "" && planner.IsTrustedInstalledProvider(cfg.Provider.Name, source)
		if existing, seen := sources[target.Provider()]; seen {
			if existing.digest != digest {
				return fmt.Errorf("conflicting provider digests for %q", cfg.Provider.Name)
			}
			if existing.trusted && !trusted {
				existing.trusted = false
				sources[target.Provider()] = existing
			}
		} else {
			sources[target.Provider()] = sourceBinding{path: source, digest: digest, trusted: trusted}
		}
	}
	refs := make([]domain.ProviderRef, 0, len(sources))
	for ref := range sources {
		refs = append(refs, ref)
	}
	sort.Slice(refs, func(i, j int) bool {
		if refs[i].Name() != refs[j].Name() {
			return refs[i].Name() < refs[j].Name()
		}
		return refs[i].Version() < refs[j].Version()
	})
	root := filepath.Join(loaded.BaseDir, ".distroplane")
	for _, ref := range refs {
		binding := sources[ref]
		if err := retainOne(root, ref, binding); err != nil {
			return err
		}
	}
	return nil
}

func retainOne(root string, provider domain.ProviderRef, source sourceBinding) error {
	dir, executable, manifest, err := retentionPaths(root, provider.Name(), source.digest)
	if err != nil {
		return err
	}
	if err := privateDirs(root, dir, true); err != nil {
		return err
	}
	if info, err := os.Lstat(executable); err == nil {
		if err := verifyExecutable(executable, info, source.digest); err != nil {
			return err
		}
	} else if errors.Is(err, os.ErrNotExist) {
		if err := snapshot(source.path, executable, source.digest); err != nil {
			return err
		}
	} else {
		return err
	}
	expected := receipt{
		SchemaVersion: schemaVersion,
		Name:          string(provider.Name()),
		Version:       string(provider.Version()),
		Digest:        source.digest.String(),
		Trusted:       source.trusted,
	}
	data, err := json.Marshal(expected)
	if err != nil {
		return err
	}
	if err := immutableWrite(manifest, append(data, '\n'), 0600); err != nil {
		return fmt.Errorf("retain provider metadata: %w", err)
	}
	return nil
}

// Resolve locates only the exact provider bytes retained for the original
// plan. Never fall back to a new install, PATH, or unverified network download.
func Resolve(root string, provider domain.ProviderRef, digest domain.Digest) (string, bool, error) {
	dir, executable, manifest, err := retentionPaths(root, provider.Name(), digest)
	if err != nil {
		return "", false, err
	}
	repair := fmt.Sprintf("restore the exact provider binary with digest %s to %q from a trusted backup; do not replan or reset the journal", digest, executable)
	if err := privateDirs(root, dir, false); err != nil {
		return "", false, fmt.Errorf("retained provider %q@%q unavailable: %v; %s", provider.Name(), provider.Version(), err, repair)
	}
	info, err := os.Lstat(executable)
	if err != nil {
		return "", false, fmt.Errorf("retained provider %q unavailable: %v; %s", provider.Name(), err, repair)
	}
	if err := verifyExecutable(executable, info, digest); err != nil {
		return "", false, fmt.Errorf("retained provider %q: %v; %s", provider.Name(), err, repair)
	}
	info, err = os.Lstat(manifest)
	if err != nil || !info.Mode().IsRegular() || (runtime.GOOS != "windows" && info.Mode().Perm()&0077 != 0) {
		return "", false, fmt.Errorf("retained provider receipt unavailable or unsafe; %s", repair)
	}
	data, err := os.ReadFile(manifest)
	if err != nil {
		return "", false, err
	}
	var found receipt
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&found); err != nil {
		return "", false, fmt.Errorf("invalid retained provider receipt: %w", err)
	}
	if found.SchemaVersion != schemaVersion || found.Name != string(provider.Name()) ||
		found.Version != string(provider.Version()) || found.Digest != digest.String() {
		return "", false, fmt.Errorf("retained provider receipt disagrees with immutable plan; %s", repair)
	}
	return executable, found.Trusted, nil
}

func retentionPaths(root string, name domain.ProviderName, digest domain.Digest) (string, string, string, error) {
	if !digest.Valid() || !safeName(string(name)) {
		return "", "", "", fmt.Errorf("invalid provider identity or digest")
	}
	if strings.TrimSpace(root) == "" || filepath.Base(filepath.Clean(root)) != ".distroplane" {
		return "", "", "", fmt.Errorf("provider retention must use a project .distroplane directory")
	}
	root, err := filepath.Abs(root)
	if err != nil {
		return "", "", "", err
	}
	dir := filepath.Join(root, "providers", string(name), digest.Hex())
	filename := "distroplane-provider-" + string(name)
	if runtime.GOOS == "windows" {
		filename += ".exe"
	}
	return dir, filepath.Join(dir, filename), filepath.Join(dir, "binding.json"), nil
}

func safeName(value string) bool {
	if value == "" || len(value) > 128 {
		return false
	}
	for i, r := range value {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' {
			continue
		}
		if i != 0 && (r == '.' || r == '_' || r == '-') {
			continue
		}
		return false
	}
	return value != "." && value != ".."
}

func privateDirs(root, dir string, create bool) error {
	root, err := filepath.Abs(root)
	if err != nil {
		return err
	}
	paths := []string{root, filepath.Join(root, "providers"), filepath.Dir(dir), dir}
	for i, path := range paths {
		info, err := os.Lstat(path)
		if errors.Is(err, os.ErrNotExist) && create && i > 0 {
			if err := os.Mkdir(path, 0700); err != nil && !errors.Is(err, os.ErrExist) {
				return err
			}
			info, err = os.Lstat(path)
		}
		if err != nil {
			return err
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("unsafe provider retention directory %q", path)
		}
		if i > 0 && runtime.GOOS != "windows" && info.Mode().Perm()&0077 != 0 {
			return fmt.Errorf("provider retention directory %q is not private", path)
		}
	}
	return nil
}

func verifyExecutable(path string, info os.FileInfo, expected domain.Digest) error {
	if !info.Mode().IsRegular() {
		return fmt.Errorf("cached provider %q must be a regular file, not a symlink", path)
	}
	if runtime.GOOS != "windows" && (info.Mode().Perm()&0111 == 0 || info.Mode().Perm()&0022 != 0) {
		return fmt.Errorf("cached provider %q must be executable and not group/world writable", path)
	}
	digest, _, err := (planner.FileHasher{}).Hash(path)
	if err != nil {
		return fmt.Errorf("verify cached provider: %w", err)
	}
	if digest != expected {
		return fmt.Errorf("cached provider digest does not match the immutable plan")
	}
	return nil
}

func snapshot(source, target string, expected domain.Digest) error {
	info, err := os.Lstat(source)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("provider source must be a regular file")
	}
	in, err := os.Open(source)
	if err != nil {
		return err
	}
	defer in.Close()
	return writeCopiedExecutable(in, target, expected)
}

func writeCopiedExecutable(source io.Reader, target string, expected domain.Digest) error {
	dir := filepath.Dir(target)
	tmp, err := os.CreateTemp(dir, ".provider-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := io.Copy(tmp, source); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(0500); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	info, err := os.Lstat(tmp.Name())
	if err != nil {
		return err
	}
	if err := verifyExecutable(tmp.Name(), info, expected); err != nil {
		return err
	}
	if err := os.Link(tmp.Name(), target); err != nil {
		if existing, statErr := os.Lstat(target); statErr == nil {
			return verifyExecutable(target, existing, expected)
		}
		return fmt.Errorf("install immutable provider snapshot: %w", err)
	}
	return nil
}

func immutableWrite(path string, data []byte, permissions os.FileMode) error {
	if existing, err := os.Lstat(path); err == nil {
		if !existing.Mode().IsRegular() {
			return fmt.Errorf("existing provider receipt is not regular")
		}
		actual, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if !bytes.Equal(actual, data) {
			return fmt.Errorf("immutable provider receipt conflicts with saved binding")
		}
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	temp, err := os.CreateTemp(filepath.Dir(path), ".receipt-*")
	if err != nil {
		return err
	}
	defer os.Remove(temp.Name())
	if err := temp.Chmod(permissions); err != nil {
		temp.Close()
		return err
	}
	if _, err := temp.Write(data); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Sync(); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	if err := os.Link(temp.Name(), path); err != nil {
		actual, readErr := os.ReadFile(path)
		if readErr == nil && bytes.Equal(actual, data) {
			return nil
		}
		return fmt.Errorf("persist immutable provider receipt: %w", err)
	}
	return nil
}
