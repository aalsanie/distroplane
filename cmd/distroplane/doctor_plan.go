package main

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"

	"github.com/aalsanie/distroplane/internal/config"
	"github.com/aalsanie/distroplane/internal/domain"
	"github.com/aalsanie/distroplane/internal/planner"
	"github.com/aalsanie/distroplane/internal/providerhost"
	"github.com/aalsanie/distroplane/internal/providerretention"
	"github.com/aalsanie/distroplane/internal/runstate"
)

func doctorPlanBindings(d *doctorChecks, loaded config.Loaded, planPath string) (map[string]providerhost.Binding, map[string]bool, bool) {
	records, err := (runstate.Store{Root: filepath.Join(loaded.BaseDir, ".distroplane")}).Inventory()
	if err != nil {
		if errors.Is(err, planner.ErrProviderDigestBinding) {
			d.add("PROVIDER_DIGEST_UNVERIFIED", doctorFail, "catalog", "saved plan lacks a supported executable digest binding", "Preserve the original run and use its compatible recovery tooling")
		} else {
			d.add("STATE_CORRUPT", doctorFail, "catalog", "saved plan or journal catalog verification failed", "Restore the original plan, provider binaries, and journal")
		}
		return nil, nil, false
	}
	var candidates []runstate.Record
	for _, record := range records {
		if record.ReleaseID == loaded.Config.Release.ID {
			candidates = append(candidates, record)
		}
		if record.State == "RESERVED" {
			d.add("RUN_RESERVED", doctorWarn, record.RunID, "a claimed run has no durable journal", "Investigate the run and preserve its original state; never create a replacement publication")
		}
		if record.ReconcileRequired {
			d.add("RECONCILE_REQUIRED", doctorWarn, record.RunID, "external reconciliation is required before retrying publication", "Use distroplane resume --reconcile-only for this exact run")
		}
	}
	if planPath == "" {
		if len(candidates) > 1 {
			d.add("PLAN_AMBIGUOUS", doctorFail, "catalog", "multiple saved plans for the configured release; provider bytes are unverified", "Use --plan PATH to select the exact immutable plan")
			return nil, nil, false
		}
		if len(candidates) == 1 {
			planPath = candidates[0].PlanPath
		}
	}
	if planPath == "" {
		d.add("PLAN_SKIPPED", doctorSkip, "plan", "no saved plan selected for digest comparison", "Use --plan PATH to verify an existing immutable plan")
		return nil, nil, true
	}
	info, err := os.Lstat(planPath)
	if err != nil || !info.Mode().IsRegular() {
		d.add("STATE_CORRUPT", doctorFail, "plan", "saved plan is missing, unreadable or not a regular file", "Restore the exact immutable plan")
		return nil, nil, false
	}
	persisted, err := planner.Load(planPath)
	if err != nil {
		if errors.Is(err, planner.ErrProviderDigestBinding) {
			d.add("PROVIDER_DIGEST_UNVERIFIED", doctorFail, "plan", "saved plan has an unsupported executable digest binding", "Preserve the original run and use its compatible recovery tooling")
		} else {
			d.add("STATE_CORRUPT", doctorFail, "plan", "saved plan cannot be verified", "Restore the exact immutable plan using compatible tooling")
		}
		return nil, nil, false
	}
	plan := persisted.Plan()
	if !doctorConfigMatchesPlan(loaded, plan) {
		d.add("PLAN_CONFIG_MISMATCH", doctorFail, "plan", "selected plan does not match the configured release, artifacts or destinations", "Select the original configuration and immutable plan")
		return nil, nil, false
	}
	if err := planner.VerifyArtifacts(plan, nil); err != nil {
		d.add("ARTIFACT_CHANGED", doctorFail, "plan", "artifact bytes differ from the saved plan", "Restore the exact published artifact bytes")
	} else {
		d.add("ARTIFACT_DIGEST_MATCH", doctorPass, "plan", "artifact digests match", "")
	}
	if err := persisted.VerifyProviderDigests(); err != nil {
		d.add("PROVIDER_DIGEST_UNVERIFIED", doctorFail, "plan", "saved plan lacks a supported executable digest binding", "Preserve the original run and use its compatible recovery tooling")
		return nil, nil, false
	}
	resolved, trusted, err := resolveExecutionBindings(loaded, plan, persisted.ProviderDigests())
	if err != nil {
		doctorRetainedProviderFailure(d, err)
		return nil, nil, false
	}
	byProvider := make(map[domain.ProviderRef]providerhost.Binding, len(resolved))
	for _, binding := range resolved {
		byProvider[binding.Provider] = binding
	}
	byTarget := make(map[string]providerhost.Binding, len(plan.Targets()))
	for _, target := range plan.Targets() {
		byTarget[string(target.ID())] = byProvider[target.Provider()]
	}
	return byTarget, trusted, true
}

func doctorConfigMatchesPlan(loaded config.Loaded, plan domain.Plan) bool {
	if loaded.Config.Release.ID != string(plan.Release().ID()) ||
		len(loaded.Config.Targets) != len(plan.Targets()) ||
		len(loaded.Config.Release.Artifacts) != len(plan.Release().Artifacts()) {
		return false
	}
	targets := make(map[string]config.Target, len(loaded.Config.Targets))
	for _, target := range loaded.Config.Targets {
		targets[target.ID] = target
	}
	for _, target := range plan.Targets() {
		configured, ok := targets[string(target.ID())]
		if !ok || configured.Provider.Name != string(target.Provider().Name()) ||
			!bytes.Equal(configured.Configuration, target.Configuration().Bytes()) {
			return false
		}
	}
	artifacts := make(map[string]config.Artifact, len(loaded.Config.Release.Artifacts))
	for _, artifact := range loaded.Config.Release.Artifacts {
		artifacts[artifact.Name] = artifact
	}
	for _, artifact := range plan.Release().Artifacts() {
		configured, ok := artifacts[artifact.Name()]
		source := configured.Source
		if !filepath.IsAbs(source) {
			source = filepath.Join(loaded.BaseDir, source)
		}
		if !ok || filepath.Clean(source) != filepath.Clean(artifact.Source()) || configured.MediaType != artifact.MediaType() {
			return false
		}
	}
	return true
}

func doctorRetainedProviderFailure(d *doctorChecks, err error) {
	code, message := "PROVIDER_HASH_FAILED", "retained provider bytes cannot be verified"
	switch {
	case errors.Is(err, providerretention.ErrDigestMismatch):
		code, message = "PROVIDER_DIGEST_CHANGED", "retained provider bytes differ from the immutable plan"
	case errors.Is(err, os.ErrNotExist):
		code, message = "PROVIDER_NOT_FOUND", "the exact retained provider or its receipt is missing"
	case errors.Is(err, os.ErrPermission):
		code, message = "PROVIDER_HASH_FAILED", "retained provider or receipt cannot be read"
	case errors.Is(err, providerretention.ErrUnsafePath):
		code, message = "PROVIDER_UNSAFE_PATH", "retained provider path or permissions are unsafe"
	case errors.Is(err, providerretention.ErrInvalidBinding):
		code, message = "PROVIDER_DIGEST_UNVERIFIED", "retained provider binding is invalid or unsupported"
	}
	d.add(code, doctorFail, "plan", message, "Restore the exact retained provider and receipt from a trusted backup; do not replan or reset the journal")
}
