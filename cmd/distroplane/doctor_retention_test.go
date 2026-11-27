package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/aalsanie/distroplane/internal/config"
	"github.com/aalsanie/distroplane/internal/planner"
	"github.com/aalsanie/distroplane/internal/providerretention"
	"github.com/aalsanie/distroplane/internal/runstate"
)

type doctorFileSnapshot struct {
	mode   os.FileMode
	size   int64
	digest [32]byte
	denied bool
}

func snapshotDoctorFiles(t *testing.T, root string) map[string]doctorFileSnapshot {
	t.Helper()
	result := make(map[string]doctorFileSnapshot)
	err := filepath.WalkDir(root, func(path string, _ os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := os.Lstat(path)
		if err != nil {
			return err
		}
		value := doctorFileSnapshot{mode: info.Mode()}
		if info.Mode().IsRegular() {
			value.size = info.Size()
			data, err := os.ReadFile(path)
			if errors.Is(err, os.ErrPermission) {
				value.denied = true
			} else if err != nil {
				return err
			} else {
				value.digest = sha256.Sum256(data)
			}
		} else if info.Mode()&os.ModeSymlink != 0 {
			link, err := os.Readlink(path)
			if err != nil {
				return err
			}
			value.digest = sha256.Sum256([]byte(link))
		}
		result[path] = value
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func compareDoctorSnapshots(before, after map[string]doctorFileSnapshot) error {
	for path, previous := range before {
		current, ok := after[path]
		if !ok {
			return fmt.Errorf("removed %q", path)
		}
		if previous != current {
			return fmt.Errorf("changed %q: before=%+v after=%+v", path, previous, current)
		}
	}
	for path := range after {
		if _, ok := before[path]; !ok {
			return fmt.Errorf("created %q", path)
		}
	}
	return nil
}

func assertDoctorReadOnly(t *testing.T, root, cfg string, want int, check, severity string, flags ...string) doctorOutput {
	t.Helper()
	before := snapshotDoctorFiles(t, root)
	args := append([]string{"--config", cfg}, flags...)
	report := doctorRun(t, want, append(append([]string(nil), args...), "--json")...)
	if report.Healthy != (want == exitOK) || !hasDoctorCode(report, check, severity) {
		t.Fatalf("missing %s/%s or inconsistent health: %+v", check, severity, report)
	}
	var stdout, stderr bytes.Buffer
	if code := runDoctorContext(context.Background(), args, &stdout, &stderr); code != want || stderr.Len() != 0 {
		t.Fatalf("human report code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	for _, finding := range report.Checks {
		if finding.Severity != doctorFail && finding.Severity != doctorWarn && finding.Code != "PROVIDER_DIGEST_MATCH" && finding.Code != "PLAN_SKIPPED" {
			continue
		}
		if !strings.Contains(stdout.String(), finding.Severity+" "+finding.Code+" ") {
			t.Fatalf("human report omitted %s/%s: %s", finding.Code, finding.Severity, stdout.String())
		}
	}
	if err := compareDoctorSnapshots(before, snapshotDoctorFiles(t, root)); err != nil {
		t.Fatalf("doctor mutated persistent project state: %v", err)
	}
	return report
}

func doctorProbeExecutable(t *testing.T, marker, arch string) string {
	t.Helper()
	dir := t.TempDir()
	source := filepath.Join(dir, "main.go")
	program := fmt.Sprintf("package main\nimport \"os\"\nfunc main() { if err := os.WriteFile(%q, []byte(\"executed\"), 0600); err != nil { os.Exit(2) } }\n", marker)
	if err := os.WriteFile(source, []byte(program), 0600); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, executableName("probe"))
	command := exec.Command("go", "build", "-o", path, source)
	command.Env = append(os.Environ(), "GOARCH="+arch, "CGO_ENABLED=0")
	if out, err := command.CombinedOutput(); err != nil {
		t.Fatalf("build diagnostic executable: %v\n%s", err, out)
	}
	return path
}

func requireNoDoctorExecution(t *testing.T, root string, report doctorOutput) {
	t.Helper()
	if hasDoctorCode(report, "PROVIDER_DIGEST_MATCH", doctorPass) || hasDoctorCode(report, "PROVIDER_VERSION", doctorPass) {
		t.Fatalf("unverified provider accepted: %+v", report)
	}
	if _, err := os.Stat(filepath.Join(root, "executed")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("untrusted provider executed or marker inaccessible: %v", err)
	}
}

func TestDoctorVerifiesActualRetainedProviderBytes(t *testing.T) {
	root, cfg, published := snapshotFixture(t)
	cache := retainedFixturePath(t, root, published)
	installed := filepath.Join(root, executableName("distroplane-provider-fake"))
	original, err := os.ReadFile(cache)
	if err != nil {
		t.Fatal(err)
	}
	canary := doctorProbeExecutable(t, filepath.Join(root, "executed"), runtime.GOARCH)
	payload, err := os.ReadFile(canary)
	if err != nil {
		t.Fatal(err)
	}
	replace := func(path string, data []byte) {
		t.Helper()
		if err := os.Chmod(path, 0700); err != nil && !errors.Is(err, os.ErrNotExist) {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, data, 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(path, 0700); err != nil {
			t.Fatal(err)
		}
	}
	assertDoctorReadOnly(t, root, cfg, exitOK, "PROVIDER_DIGEST_MATCH", doctorPass)
	replace(cache, payload)
	for _, args := range [][]string{nil, {"--plan", published.Plan.Path}} {
		report := assertDoctorReadOnly(t, root, cfg, exitInvalid, "PROVIDER_DIGEST_CHANGED", doctorFail, args...)
		requireNoDoctorExecution(t, root, report)
	}
	replace(installed, payload)
	report := assertDoctorReadOnly(t, root, cfg, exitInvalid, "PROVIDER_DIGEST_CHANGED", doctorFail)
	requireNoDoctorExecution(t, root, report)
	replace(cache, original)
	report = assertDoctorReadOnly(t, root, cfg, exitOK, "PROVIDER_DIGEST_MATCH", doctorPass)
	if !hasDoctorCode(report, "PROVIDER_VERSION", doctorPass) {
		t.Fatalf("retained provider not described: %+v", report)
	}
	if _, err := os.Stat(filepath.Join(root, "executed")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("installed upgrade was executed: %v", err)
	}
	if err := os.Remove(installed); err != nil {
		t.Fatal(err)
	}
	assertDoctorReadOnly(t, root, cfg, exitOK, "PROVIDER_DIGEST_MATCH", doctorPass)
	configBytes, err := os.ReadFile(cfg)
	if err != nil {
		t.Fatal(err)
	}
	newPath := filepath.Join(root, "missing-installation", executableName("distroplane-provider-fake"))
	changed := bytes.Replace(configBytes, []byte(jsonString(installed)), []byte(jsonString(newPath)), 1)
	if bytes.Equal(configBytes, changed) {
		t.Fatal("fixture executable path not replaced")
	}
	if err := os.WriteFile(cfg, changed, 0600); err != nil {
		t.Fatal(err)
	}
	assertDoctorReadOnly(t, root, cfg, exitOK, "PROVIDER_DIGEST_MATCH", doctorPass, "--plan", published.Plan.Path)
	if err := os.Remove(cache); err != nil {
		t.Fatal(err)
	}
	report = assertDoctorReadOnly(t, root, cfg, exitInvalid, "PROVIDER_NOT_FOUND", doctorFail)
	requireNoDoctorExecution(t, root, report)
	if err := os.Mkdir(cache, 0700); err != nil {
		t.Fatal(err)
	}
	report = assertDoctorReadOnly(t, root, cfg, exitInvalid, "PROVIDER_UNSAFE_PATH", doctorFail)
	requireNoDoctorExecution(t, root, report)
	if err := os.Remove(cache); err != nil {
		t.Fatal(err)
	}
	replace(cache, original)
	receipt := filepath.Join(filepath.Dir(cache), "binding.json")
	receiptBytes, err := os.ReadFile(receipt)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(receipt, []byte("{}"), 0600); err != nil {
		t.Fatal(err)
	}
	report = assertDoctorReadOnly(t, root, cfg, exitInvalid, "PROVIDER_DIGEST_UNVERIFIED", doctorFail)
	requireNoDoctorExecution(t, root, report)
	if err := os.WriteFile(receipt, receiptBytes, 0600); err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" {
		if err := os.Chmod(cache, 0600); err != nil {
			t.Fatal(err)
		}
		report = assertDoctorReadOnly(t, root, cfg, exitInvalid, "PROVIDER_UNSAFE_PATH", doctorFail)
		requireNoDoctorExecution(t, root, report)
		if err := os.Chmod(cache, 0100); err != nil {
			t.Fatal(err)
		}
		if _, err := os.ReadFile(cache); errors.Is(err, os.ErrPermission) {
			report = assertDoctorReadOnly(t, root, cfg, exitInvalid, "PROVIDER_HASH_FAILED", doctorFail)
			requireNoDoctorExecution(t, root, report)
		}
		if err := os.Chmod(cache, 0700); err != nil {
			t.Fatal(err)
		}
	}
	assertDoctorReadOnly(t, root, cfg, exitOK, "PROVIDER_DIGEST_MATCH", doctorPass)
}

func TestDoctorRetainedProviderErrorClassification(t *testing.T) {
	for _, tc := range []struct {
		err  error
		code string
	}{
		{fmt.Errorf("wrapped: %w", providerretention.ErrDigestMismatch), "PROVIDER_DIGEST_CHANGED"},
		{&os.PathError{Op: "open", Path: "provider", Err: os.ErrNotExist}, "PROVIDER_NOT_FOUND"},
		{&os.PathError{Op: "read", Path: "provider", Err: os.ErrPermission}, "PROVIDER_HASH_FAILED"},
		{providerretention.ErrUnsafePath, "PROVIDER_UNSAFE_PATH"},
		{providerretention.ErrInvalidBinding, "PROVIDER_DIGEST_UNVERIFIED"},
		{errors.New("I/O error"), "PROVIDER_HASH_FAILED"},
	} {
		t.Run(tc.code, func(t *testing.T) {
			var report doctorChecks
			doctorRetainedProviderFailure(&report, tc.err)
			if report.exit() != exitInvalid || !hasDoctorCode(report.output, tc.code, doctorFail) {
				t.Fatalf("incorrect classification for %v: %+v", tc.err, report)
			}
		})
	}
}

func TestDoctorRejectsAmbiguousAndMismatchedPlans(t *testing.T) {
	root, cfg, published := snapshotFixture(t)
	original, err := os.ReadFile(cfg)
	if err != nil {
		t.Fatal(err)
	}
	writeConfig := func(data []byte) {
		t.Helper()
		if err := os.WriteFile(cfg, data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	otherRelease := bytes.ReplaceAll(original, []byte("release-publish"), []byte("release-other"))
	writeConfig(otherRelease)
	report := assertDoctorReadOnly(t, root, cfg, exitInvalid, "PLAN_CONFIG_MISMATCH", doctorFail, "--plan", published.Plan.Path)
	requireNoDoctorExecution(t, root, report)
	assertDoctorReadOnly(t, root, cfg, exitOK, "PLAN_SKIPPED", doctorSkip)
	writeConfig(bytes.Replace(original, []byte("waiting_external"), []byte("published"), 1))
	var stdout, stderr bytes.Buffer
	if code := runPlanContext(context.Background(), []string{"--config", cfg, "--json"}, &stdout, &stderr); code != exitOK {
		t.Fatalf("second plan: %d %s %s", code, stdout.String(), stderr.String())
	}
	writeConfig(otherRelease)
	assertDoctorReadOnly(t, root, cfg, exitOK, "PLAN_SKIPPED", doctorSkip)
	writeConfig(original)
	report = assertDoctorReadOnly(t, root, cfg, exitInvalid, "PLAN_AMBIGUOUS", doctorFail)
	requireNoDoctorExecution(t, root, report)
	assertDoctorReadOnly(t, root, cfg, exitOK, "PROVIDER_DIGEST_MATCH", doctorPass, "--plan", published.Plan.Path)
	for _, tc := range []struct{ old, replacement string }{
		{"\"id\":\"primary\"", "\"id\":\"different\""},
		{"\"name\":\"fake\"", "\"name\":\"other\""},
		{"\"mode\":\"waiting_external\"", "\"mode\":\"published\""},
		{"\"source\":\"app.bin\"", "\"source\":\"changed.bin\""},
		{"\"name\":\"app\"", "\"name\":\"other-artifact\""},
		{"\"source\":\"app.bin\"", "\"source\":\"app.bin\",\"mediaType\":\"application/octet-stream\""},
	} {
		changed := bytes.Replace(original, []byte(tc.old), []byte(tc.replacement), 1)
		if bytes.Equal(original, changed) {
			t.Fatalf("fixture did not contain %s", tc.old)
		}
		writeConfig(changed)
		report = assertDoctorReadOnly(t, root, cfg, exitInvalid, "PLAN_CONFIG_MISMATCH", doctorFail, "--plan", published.Plan.Path)
		requireNoDoctorExecution(t, root, report)
	}
	writeConfig(original)
}

func TestDoctorDoesNotLaunchWrongArchitecture(t *testing.T) {
	root, cfg := publishFixture(t, "{\"mode\":\"published\"}")
	otherArch := "arm64"
	if runtime.GOARCH == otherArch {
		otherArch = "amd64"
	}
	probe := doctorProbeExecutable(t, filepath.Join(root, "executed"), otherArch)
	loaded, err := config.Load(cfg)
	if err != nil {
		t.Fatal(err)
	}
	path := loaded.Config.Targets[0].Provider.Executable
	copyExecutable(t, probe, path)
	report := assertDoctorReadOnly(t, root, cfg, exitInvalid, "ARCH_MISMATCH", doctorFail)
	requireNoDoctorExecution(t, root, report)
	digest, _, err := (planner.FileHasher{}).Hash(path)
	if err != nil {
		t.Fatal(err)
	}
	planPath, _ := writeDoctorMetadataPlan(t, root, "1.0.0", digest.String())
	plan, err := planner.Load(planPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, ".distroplane"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := providerretention.Retain(loaded, plan); err != nil {
		t.Fatal(err)
	}
	report = assertDoctorReadOnly(t, root, cfg, exitInvalid, "ARCH_MISMATCH", doctorFail, "--plan", planPath)
	if !hasDoctorCode(report, "PROVIDER_DIGEST_MATCH", doctorPass) || hasDoctorCode(report, "PROVIDER_VERSION", doctorPass) {
		t.Fatalf("digest and architecture checks conflated: %+v", report)
	}
	if _, err := os.Stat(filepath.Join(root, "executed")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("incompatible retained provider was launched: %v", err)
	}
}

func writeDoctorMetadataPlan(t *testing.T, root, version, providerDigest string) (string, string) {
	t.Helper()
	artifact := filepath.Join(root, "app.bin")
	digest, size, err := (planner.FileHasher{}).Hash(artifact)
	if err != nil {
		t.Fatal(err)
	}
	binding := ""
	if providerDigest != "" {
		binding = fmt.Sprintf(",\"digest\":%q", providerDigest)
	}
	target := fmt.Sprintf("{\"id\":\"primary\",\"provider\":{\"name\":\"fake\",\"version\":%q%s},\"configuration\":{\"mode\":\"published\"},\"requiredCapabilities\":[\"plan\"]}", version, binding)
	semanticRelease := fmt.Sprintf("{\"id\":\"release-publish\",\"artifacts\":[{\"name\":\"app\",\"digest\":%q,\"size\":%d}]}", digest.String(), size)
	semantic := fmt.Sprintf("{\"schemaVersion\":\"1\",\"protocolVersion\":\"1\",\"release\":%s,\"targets\":[%s],\"operations\":[]}", semanticRelease, target)
	id := fmt.Sprintf("sha256-%x", sha256.Sum256([]byte(semantic)))
	persistedRelease := fmt.Sprintf("{\"id\":\"release-publish\",\"artifacts\":[{\"name\":\"app\",\"source\":%s,\"digest\":%q,\"size\":%d}]}", jsonString(artifact), digest.String(), size)
	data := fmt.Sprintf("{\"schemaVersion\":\"1\",\"protocolVersion\":\"1\",\"planId\":%q,\"release\":%s,\"targets\":[%s],\"operations\":[]}", id, persistedRelease, target)
	path := filepath.Join(root, "metadata-plan.json")
	if err := os.WriteFile(path, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	return path, data
}

func TestDoctorRejectsPlanWithoutDigestBinding(t *testing.T) {
	root, cfg := publishFixture(t, "{\"mode\":\"published\"}")
	path, data := writeDoctorMetadataPlan(t, root, "1.0.0", "")
	plan, err := planner.Load(path)
	if err != nil {
		t.Fatalf("legacy fixture must be a valid plan: %v", err)
	}
	if err := plan.VerifyProviderDigests(); !errors.Is(err, planner.ErrProviderDigestBinding) {
		t.Fatalf("legacy fixture unexpectedly digest-bound: %v", err)
	}
	report := assertDoctorReadOnly(t, root, cfg, exitInvalid, "PROVIDER_DIGEST_UNVERIFIED", doctorFail, "--plan", path)
	requireNoDoctorExecution(t, root, report)

	stateDir := filepath.Join(root, ".distroplane")
	if err := (runstate.Store{Root: stateDir}).Ensure(); err != nil {
		t.Fatal(err)
	}
	canonical, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatal(err)
	}
	entry, err := json.Marshal(map[string]string{
		"schemaVersion": "1",
		"planId":        string(plan.ID()),
		"planPath":      canonical,
	})
	if err != nil {
		t.Fatal(err)
	}
	catalogPath := filepath.Join(stateDir, "catalog", "plans", fmt.Sprintf("%x.json", sha256.Sum256([]byte(plan.ID()))))
	if err := os.WriteFile(catalogPath, entry, 0600); err != nil {
		t.Fatal(err)
	}
	report = assertDoctorReadOnly(t, root, cfg, exitInvalid, "PROVIDER_DIGEST_UNVERIFIED", doctorFail)
	requireNoDoctorExecution(t, root, report)
	if err := os.Remove(catalogPath); err != nil {
		t.Fatal(err)
	}

	unsupported := strings.Replace(data, "\"version\":\"1.0.0\"", "\"version\":\"1.0.0\",\"digest\":\"sha512:unsupported\"", 1)
	if unsupported == data {
		t.Fatal("unsupported digest fixture was not constructed")
	}
	if err := os.WriteFile(path, []byte(unsupported), 0600); err != nil {
		t.Fatal(err)
	}
	report = assertDoctorReadOnly(t, root, cfg, exitInvalid, "PROVIDER_DIGEST_UNVERIFIED", doctorFail, "--plan", path)
	requireNoDoctorExecution(t, root, report)
}

func TestDoctorChecksSavedProviderVersion(t *testing.T) {
	root, cfg := publishFixture(t, "{\"mode\":\"published\"}")
	loaded, err := config.Load(cfg)
	if err != nil {
		t.Fatal(err)
	}
	digest, _, err := (planner.FileHasher{}).Hash(loaded.Config.Targets[0].Provider.Executable)
	if err != nil {
		t.Fatal(err)
	}
	path, _ := writeDoctorMetadataPlan(t, root, "9.9.9", digest.String())
	plan, err := planner.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, ".distroplane"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := providerretention.Retain(loaded, plan); err != nil {
		t.Fatal(err)
	}
	report := assertDoctorReadOnly(t, root, cfg, exitInvalid, "VERSION_MISMATCH", doctorFail, "--plan", path)
	if !hasDoctorCode(report, "PROVIDER_DIGEST_MATCH", doctorPass) || hasDoctorCode(report, "PROVIDER_VERSION", doctorPass) {
		t.Fatalf("digest match hid a provider version mismatch: %+v", report)
	}
}

func TestDoctorSnapshotDetectsPersistentChanges(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "state.json")
	if err := os.WriteFile(path, []byte("original"), 0600); err != nil {
		t.Fatal(err)
	}
	before := snapshotDoctorFiles(t, root)
	stamp := time.Now().Add(-time.Hour)
	for _, name := range []string{path, root} {
		if err := os.Chtimes(name, stamp, stamp); err != nil {
			t.Fatal(err)
		}
	}
	if err := compareDoctorSnapshots(before, snapshotDoctorFiles(t, root)); err != nil {
		t.Fatalf("filesystem timestamp change affected state comparison: %v", err)
	}
	assertChanged := func(kind, name string) {
		t.Helper()
		err := compareDoctorSnapshots(before, snapshotDoctorFiles(t, root))
		if err == nil || !strings.Contains(err.Error(), kind+" "+fmt.Sprintf("%q", name)) {
			t.Fatalf("expected %s for %q, got %v", kind, name, err)
		}
	}
	if err := os.WriteFile(path, []byte("modified"), 0600); err != nil {
		t.Fatal(err)
	}
	assertChanged("changed", path)
	if err := os.WriteFile(path, []byte("original"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0400); err != nil {
		t.Fatal(err)
	}
	assertChanged("changed", path)
	if err := os.Chmod(path, 0600); err != nil {
		t.Fatal(err)
	}
	extra := filepath.Join(root, "unexpected")
	if err := os.Mkdir(extra, 0700); err != nil {
		t.Fatal(err)
	}
	assertChanged("created", extra)
	if err := os.Remove(extra); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	assertChanged("removed", path)
	if err := os.Mkdir(path, 0700); err != nil {
		t.Fatal(err)
	}
	assertChanged("changed", path)
}
