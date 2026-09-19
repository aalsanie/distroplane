package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/aalsanie/distroplane/internal/evidence"
	"github.com/aalsanie/distroplane/internal/planner"
)

type attestationFlags []evidence.AttestationReference

func (a *attestationFlags) String() string { return "" }

func (a *attestationFlags) Set(value string) error {
	name, uri, ok := strings.Cut(value, "=")
	if !ok || strings.TrimSpace(name) == "" || strings.TrimSpace(uri) == "" {
		return fmt.Errorf("attestation must use NAME=URI format")
	}
	*a = append(*a, evidence.AttestationReference{Name: name, URI: uri})
	return nil
}

type evidenceOutput struct {
	OutputSchemaVersion string `json:"outputSchemaVersion"`
	Path                string `json:"path"`
	Digest              string `json:"digest"`
	PlanID              string `json:"planId"`
	RunID               string `json:"runId"`
}

func runEvidence(args []string, stdout, stderr io.Writer) int {
	var planPath, journalPath, outputPath, journalReference string
	var jsonMode bool
	var attestations attestationFlags
	fs := commandFlags("evidence")
	fs.StringVar(&planPath, "plan", "", "")
	fs.StringVar(&journalPath, "journal", "", "")
	fs.StringVar(&outputPath, "output", "", "")
	fs.StringVar(&journalReference, "journal-reference", "", "")
	fs.BoolVar(&jsonMode, "json", false, "")
	fs.Var(&attestations, "attestation", "")
	if err := parseCommand(fs, args); err != nil {
		return writeCommandError(stderr, hasJSON(args), exitUsage, "usage", err)
	}
	if planPath == "" || journalPath == "" {
		return writeCommandError(stderr, jsonMode, exitUsage, "usage", fmt.Errorf("--plan and --journal are required"))
	}
	persisted, err := planner.Load(planPath)
	if err != nil {
		return writeCommandError(stderr, jsonMode, exitInvalid, "invalid_plan", err)
	}
	raw, err := os.ReadFile(journalPath)
	if err != nil {
		return writeCommandError(stderr, jsonMode, exitOperational, "journal_read_failed", err)
	}
	if journalReference == "" {
		journalReference = filepath.Base(journalPath)
	}
	bundle, err := evidence.Build(persisted.Plan(), raw, journalReference, []evidence.AttestationReference(attestations))
	if err != nil {
		return writeCommandError(stderr, jsonMode, exitInvalid, "evidence_failed", err)
	}
	data, err := evidence.Marshal(bundle)
	if err != nil {
		return writeCommandError(stderr, jsonMode, exitOperational, "evidence_encode_failed", err)
	}
	if outputPath == "" {
		if _, err := stdout.Write(append(data, '\n')); err != nil {
			return exitOperational
		}
		return exitOK
	}
	if err := writeEvidenceFile(outputPath, data); err != nil {
		return writeCommandError(stderr, jsonMode, exitOperational, "evidence_write_failed", err)
	}
	output := evidenceOutput{
		OutputSchemaVersion: outputSchemaVersion,
		Path:                outputPath, Digest: evidence.Digest(data), PlanID: bundle.PlanID, RunID: bundle.Run.ID,
	}
	if jsonMode {
		return writeJSON(stdout, output)
	}
	fmt.Fprintf(stdout, "evidence %s\ndigest %s\nplan %s\nrun %s\n", output.Path, output.Digest, output.PlanID, output.RunID)
	return exitOK
}

func writeEvidenceFile(path string, data []byte) error {
	if strings.TrimSpace(path) == "" {
		return fmt.Errorf("evidence output path must not be empty")
	}
	directory := filepath.Dir(path)
	if err := os.MkdirAll(directory, 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}
