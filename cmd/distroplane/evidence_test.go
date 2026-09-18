package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aalsanie/distroplane/internal/domain"
	evidencepkg "github.com/aalsanie/distroplane/internal/evidence"
)

func TestCLIEvidenceExport(t *testing.T) {
	dir := t.TempDir()
	providerBinary := buildFakeProvider(t)
	alpha := copyExecutable(t, providerBinary, filepath.Join(dir, executableName("distroplane-provider-alpha")))
	artifact := filepath.Join(dir, "app.bin")
	if err := os.WriteFile(artifact, []byte("artifact"), 0o600); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(dir, "distroplane.json")
	rawConfig := `{"schemaVersion":"1","release":{"id":"v1","artifacts":[{"name":"app","source":"app.bin"}]},"targets":[{"id":"alpha","provider":{"name":"alpha","executable":` + jsonString(alpha) + `},"configuration":{"mode":"published"}}]}`
	if err := os.WriteFile(configPath, []byte(rawConfig), 0o600); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	if code := execute([]string{"plan", "--config", configPath, "--json"}, &stdout, &stderr); code != exitOK {
		t.Fatalf("plan code=%d stderr=%s", code, stderr.String())
	}
	var planned planOutput
	if err := json.Unmarshal(stdout.Bytes(), &planned); err != nil {
		t.Fatal(err)
	}
	journalPath := filepath.Join(dir, "run.journal")
	stdout.Reset()
	stderr.Reset()
	if code := execute([]string{
		"apply", "--config", configPath, "--plan", planned.Path, "--journal", journalPath, "--run", "run-evidence-cli", "--json",
	}, &stdout, &stderr); code != exitOK {
		t.Fatalf("apply code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}

	const attestationSecret = "attestation-secret"
	outputPath := filepath.Join(dir, "artifacts", "release-evidence.json")
	stdout.Reset()
	stderr.Reset()
	if code := execute([]string{
		"evidence", "--plan", planned.Path, "--journal", journalPath, "--output", outputPath,
		"--journal-reference", "ci/run.journal",
		"--attestation", "workflow=https://example.test/attestation?token=" + attestationSecret,
		"--json",
	}, &stdout, &stderr); code != exitOK {
		t.Fatalf("evidence code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	var summary evidenceOutput
	if err := json.Unmarshal(stdout.Bytes(), &summary); err != nil {
		t.Fatal(err)
	}
	if summary.Path != outputPath || summary.PlanID != planned.PlanID || summary.RunID != "run-evidence-cli" ||
		!strings.HasPrefix(summary.Digest, "sha256:") {
		t.Fatalf("summary=%+v", summary)
	}
	raw, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatal(err)
	}
	if summary.Digest != evidencepkg.Digest(raw) || bytes.Contains(raw, []byte(attestationSecret)) ||
		bytes.Contains(raw, []byte(artifact)) {
		t.Fatalf("evidence=%s", raw)
	}
	var bundle evidencepkg.Bundle
	if err := json.Unmarshal(raw, &bundle); err != nil {
		t.Fatal(err)
	}
	if bundle.SchemaVersion != evidencepkg.SchemaVersion || bundle.Journal.Reference != "ci/run.journal" ||
		bundle.Run.ID != "run-evidence-cli" || len(bundle.Targets) != 1 ||
		bundle.Targets[0].State != string(domain.StatePublished) || bundle.Targets[0].Provider.Version != "1.0.0" {
		t.Fatalf("bundle=%+v", bundle)
	}
	if len(bundle.Attestations) != 1 || strings.Contains(bundle.Attestations[0].URI, attestationSecret) {
		t.Fatalf("attestations=%+v", bundle.Attestations)
	}

	stdout.Reset()
	stderr.Reset()
	if code := execute([]string{
		"evidence", "--plan", planned.Path, "--journal", journalPath,
		"--journal-reference", "ci/run.journal",
		"--attestation", "workflow=https://example.test/attestation?token=" + attestationSecret,
	}, &stdout, &stderr); code != exitOK {
		t.Fatalf("stdout evidence code=%d stderr=%s", code, stderr.String())
	}
	if !bytes.Equal(bytes.TrimSpace(stdout.Bytes()), raw) {
		t.Fatalf("stdout evidence differs\nstdout=%s\nfile=%s", stdout.Bytes(), raw)
	}
}

func TestEvidenceCommandErrorsAndHelpers(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := execute([]string{"evidence"}, &stdout, &stderr); code != exitUsage {
		t.Fatalf("missing args code=%d", code)
	}
	stdout.Reset()
	stderr.Reset()
	if code := execute([]string{"evidence", "--plan", "missing.plan", "--journal", "missing.journal", "--json"}, &stdout, &stderr); code != exitInvalid || !json.Valid(stderr.Bytes()) {
		t.Fatalf("missing plan code=%d stderr=%s", code, stderr.String())
	}
	flags := attestationFlags{}
	if err := flags.Set("bad"); err == nil {
		t.Fatal("invalid attestation accepted")
	}
	if err := flags.Set("build=urn:example:build"); err != nil || flags.String() != "" || len(flags) != 1 {
		t.Fatalf("flags=%+v err=%v", flags, err)
	}
	if err := writeEvidenceFile("", []byte("{}")); err == nil {
		t.Fatal("empty output path accepted")
	}
	dir := t.TempDir()
	parentFile := filepath.Join(dir, "parent")
	if err := os.WriteFile(parentFile, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := writeEvidenceFile(filepath.Join(parentFile, "evidence.json"), []byte("{}")); err == nil {
		t.Fatal("non-directory parent accepted")
	}
}
