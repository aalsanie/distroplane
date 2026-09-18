package planner

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"time"

	"github.com/aalsanie/distroplane/internal/canonicaljson"
	"github.com/aalsanie/distroplane/internal/domain"
	"github.com/aalsanie/distroplane/internal/protocol"
)

const MaxPlanBytes = 16 << 20

func Load(path string) (DistributionPlan, error) {
	if path == "" {
		return DistributionPlan{}, fmt.Errorf("plan path must not be empty")
	}
	file, err := os.Open(path)
	if err != nil {
		return DistributionPlan{}, err
	}
	defer file.Close()
	return Decode(file)
}

func Decode(reader io.Reader) (DistributionPlan, error) {
	if reader == nil {
		return DistributionPlan{}, fmt.Errorf("plan reader must not be nil")
	}
	raw, err := io.ReadAll(io.LimitReader(reader, MaxPlanBytes+1))
	if err != nil {
		return DistributionPlan{}, err
	}
	if len(raw) > MaxPlanBytes {
		return DistributionPlan{}, fmt.Errorf("plan exceeds %d bytes", MaxPlanBytes)
	}
	normalized, err := canonicaljson.Normalize(raw)
	if err != nil {
		return DistributionPlan{}, fmt.Errorf("decode plan: %w", err)
	}
	decoder := json.NewDecoder(bytes.NewReader(normalized))
	decoder.DisallowUnknownFields()
	var document planDocument
	if err := decoder.Decode(&document); err != nil {
		return DistributionPlan{}, fmt.Errorf("decode plan: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return DistributionPlan{}, fmt.Errorf("decode plan: trailing data")
	}
	return planFromDocument(document)
}

func planFromDocument(document planDocument) (DistributionPlan, error) {
	if document.SchemaVersion != PlanSchemaVersion {
		return DistributionPlan{}, fmt.Errorf("unsupported plan schema version %q", document.SchemaVersion)
	}
	if document.ProtocolVersion != protocol.Version {
		return DistributionPlan{}, fmt.Errorf("unsupported plan protocol version %q", document.ProtocolVersion)
	}
	if document.PlanID == "" {
		return DistributionPlan{}, fmt.Errorf("plan ID must not be empty")
	}

	releaseID, err := domain.NewReleaseID(document.Release.ID)
	if err != nil {
		return DistributionPlan{}, err
	}
	artifacts := make([]domain.Artifact, 0, len(document.Release.Artifacts))
	for _, value := range document.Release.Artifacts {
		digest, err := domain.ParseDigest(value.Digest)
		if err != nil {
			return DistributionPlan{}, fmt.Errorf("artifact %q digest: %w", value.Name, err)
		}
		artifact, err := domain.NewArtifact(value.Name, value.Source, digest, value.Size, value.MediaType)
		if err != nil {
			return DistributionPlan{}, fmt.Errorf("artifact %q: %w", value.Name, err)
		}
		artifacts = append(artifacts, artifact)
	}
	release, err := domain.NewRelease(releaseID, artifacts)
	if err != nil {
		return DistributionPlan{}, err
	}

	targets := make([]domain.Target, 0, len(document.Targets))
	targetProviders := make(map[domain.TargetID]domain.ProviderRef, len(document.Targets))
	for _, value := range document.Targets {
		targetID, err := domain.NewTargetID(value.ID)
		if err != nil {
			return DistributionPlan{}, err
		}
		providerRef, err := providerRefFromDocument(value.Provider)
		if err != nil {
			return DistributionPlan{}, fmt.Errorf("target %q provider: %w", value.ID, err)
		}
		for _, capability := range value.RequiredCapabilities {
			if !capability.Valid() {
				return DistributionPlan{}, fmt.Errorf("target %q has invalid required capability %q", value.ID, capability)
			}
		}
		configuration, err := domain.NewJSONValue(value.Configuration)
		if err != nil {
			return DistributionPlan{}, fmt.Errorf("target %q configuration: %w", value.ID, err)
		}
		requirements, err := buildDomainRequirements(value.Requirements)
		if err != nil {
			return DistributionPlan{}, fmt.Errorf("target %q requirements: %w", value.ID, err)
		}
		target, err := domain.NewTargetWithRequirements(targetID, providerRef, configuration, requirements)
		if err != nil {
			return DistributionPlan{}, err
		}
		targets = append(targets, target)
		targetProviders[targetID] = providerRef
	}

	planID, err := domain.NewPlanID(document.PlanID)
	if err != nil {
		return DistributionPlan{}, err
	}
	operations := make([]domain.Operation, 0, len(document.Operations))
	for _, value := range document.Operations {
		if value.ProviderOperationID == "" {
			return DistributionPlan{}, fmt.Errorf("operation %q provider operation ID must not be empty", value.ID)
		}
		id, err := domain.NewOperationID(value.ID)
		if err != nil {
			return DistributionPlan{}, err
		}
		targetID, err := domain.NewTargetID(value.TargetID)
		if err != nil {
			return DistributionPlan{}, err
		}
		providerRef, err := providerRefFromDocument(value.Provider)
		if err != nil {
			return DistributionPlan{}, fmt.Errorf("operation %q provider: %w", value.ID, err)
		}
		if targetProvider, ok := targetProviders[targetID]; !ok || targetProvider != providerRef {
			return DistributionPlan{}, fmt.Errorf("operation %q provider does not match target", value.ID)
		}
		dependencies := make([]domain.OperationID, len(value.Dependencies))
		for i, dependency := range value.Dependencies {
			dependencies[i], err = domain.NewOperationID(dependency)
			if err != nil {
				return DistributionPlan{}, err
			}
		}
		if value.TimeoutMillis > uint64(math.MaxInt64/int64(time.Millisecond)) {
			return DistributionPlan{}, fmt.Errorf("operation %q timeout exceeds supported duration", value.ID)
		}
		payload, err := domain.NewJSONValue(value.ProviderPayload)
		if err != nil {
			return DistributionPlan{}, fmt.Errorf("operation %q payload: %w", value.ID, err)
		}
		expectedKey := ""
		if value.SideEffecting {
			expectedKey = deriveIdempotencyKey(planID, targetID, id, artifacts)
			if value.IdempotencyKey != expectedKey {
				return DistributionPlan{}, fmt.Errorf("operation %q idempotency key does not match plan identity", value.ID)
			}
		} else if value.IdempotencyKey != "" {
			return DistributionPlan{}, fmt.Errorf("operation %q must not contain an idempotency key", value.ID)
		}
		operation, err := domain.NewOperation(
			id, targetID, providerRef, value.Kind, dependencies, value.SideEffecting, expectedKey,
			time.Duration(value.TimeoutMillis)*time.Millisecond, payload,
		)
		if err != nil {
			return DistributionPlan{}, err
		}
		operations = append(operations, operation)
	}

	semantic := semanticPlan{
		SchemaVersion:   document.SchemaVersion,
		ProtocolVersion: document.ProtocolVersion,
		Release:         semanticReleaseFromDomain(release),
		Targets:         document.Targets,
		Operations:      semanticOperationsFromDocument(document.Operations),
	}
	expectedID, err := semanticPlanID(semantic)
	if err != nil {
		return DistributionPlan{}, err
	}
	if expectedID != planID {
		return DistributionPlan{}, fmt.Errorf("plan ID %q does not match plan content", planID)
	}
	plan, err := domain.NewPlan(planID, document.SchemaVersion, document.ProtocolVersion, release, targets, operations)
	if err != nil {
		return DistributionPlan{}, err
	}
	return DistributionPlan{plan: plan, document: document}, nil
}

func providerRefFromDocument(value providerDocument) (domain.ProviderRef, error) {
	name, err := domain.NewProviderName(value.Name)
	if err != nil {
		return domain.ProviderRef{}, err
	}
	version, err := domain.NewProviderVersion(value.Version)
	if err != nil {
		return domain.ProviderRef{}, err
	}
	return domain.NewProviderRef(name, version)
}

func semanticOperationsFromDocument(values []operationDocument) []semanticOperation {
	result := make([]semanticOperation, len(values))
	for i, value := range values {
		result[i] = semanticOperation{
			ID: value.ID, ProviderOperationID: value.ProviderOperationID, TargetID: value.TargetID, Provider: value.Provider,
			Kind: value.Kind, Dependencies: append([]string(nil), value.Dependencies...), SideEffecting: value.SideEffecting,
			TimeoutMillis: value.TimeoutMillis, ProviderPayload: append(json.RawMessage(nil), value.ProviderPayload...),
		}
	}
	return result
}

func VerifyArtifacts(plan domain.Plan, hasher ArtifactHasher) error {
	if !plan.ID().Valid() {
		return fmt.Errorf("plan is invalid")
	}
	if hasher == nil {
		hasher = FileHasher{}
	}
	for _, artifact := range plan.Release().Artifacts() {
		digest, size, err := hasher.Hash(artifact.Source())
		if err != nil {
			return fmt.Errorf("verify artifact %q: %w", artifact.Name(), err)
		}
		if size != artifact.Size() {
			return fmt.Errorf("artifact %q size changed: got %d, want %d", artifact.Name(), size, artifact.Size())
		}
		if digest != artifact.Digest() {
			return fmt.Errorf("artifact %q digest changed", artifact.Name())
		}
	}
	return nil
}
