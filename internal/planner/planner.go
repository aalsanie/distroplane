package planner

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"path/filepath"
	"sort"
	"time"

	"github.com/aalsanie/distroplane/internal/canonicaljson"
	"github.com/aalsanie/distroplane/internal/config"
	"github.com/aalsanie/distroplane/internal/domain"
	"github.com/aalsanie/distroplane/internal/protocol"
)

type Planner struct {
	client   ProviderClient
	resolver ProviderResolver
	hasher   ArtifactHasher
}

type Options struct {
	Resolver ProviderResolver
	Hasher   ArtifactHasher
}

func New(client ProviderClient, options Options) (*Planner, error) {
	if client == nil {
		return nil, fmt.Errorf("provider client must not be nil")
	}
	resolver := options.Resolver
	if resolver == nil {
		resolver = newExecutableResolver()
	}
	hasher := options.Hasher
	if hasher == nil {
		hasher = FileHasher{}
	}
	return &Planner{client: client, resolver: resolver, hasher: hasher}, nil
}

func (p *Planner) Build(ctx context.Context, loaded config.Loaded) (DistributionPlan, error) {
	if ctx == nil {
		return DistributionPlan{}, fmt.Errorf("context must not be nil")
	}
	if p == nil || p.client == nil || p.resolver == nil || p.hasher == nil {
		return DistributionPlan{}, fmt.Errorf("planner is not initialized")
	}

	release, protocolRelease, err := p.buildRelease(loaded)
	if err != nil {
		return DistributionPlan{}, err
	}

	targetConfigs := append([]config.Target(nil), loaded.Config.Targets...)
	sort.Slice(targetConfigs, func(i, j int) bool { return targetConfigs[i].ID < targetConfigs[j].ID })

	targets := make([]domain.Target, 0, len(targetConfigs))
	targetDocs := make([]targetDocument, 0, len(targetConfigs))
	drafts := make([]operationDraft, 0)
	describeCache := make(map[Endpoint]protocol.DescribeResponse)
	providerDigestCache := make(map[Endpoint]domain.Digest)
	providerDigests := make(map[domain.ProviderRef]domain.Digest)

	for _, targetConfig := range targetConfigs {
		endpoint, err := p.resolveEndpoint(targetConfig)
		if err != nil {
			return DistributionPlan{}, fmt.Errorf("target %q: %w", targetConfig.ID, err)
		}
		describe, ok := describeCache[endpoint]
		if !ok {
			describe, err = p.client.Describe(ctx, endpoint)
			if err != nil {
				return DistributionPlan{}, fmt.Errorf("describe provider %q: %w", targetConfig.Provider.Name, err)
			}
			if err := validateDescription(targetConfig.Provider.Name, describe); err != nil {
				return DistributionPlan{}, fmt.Errorf("describe provider %q: %w", targetConfig.Provider.Name, err)
			}
			describeCache[endpoint] = describe
		}

		targetID, err := domain.NewTargetID(targetConfig.ID)
		if err != nil {
			return DistributionPlan{}, err
		}
		providerName, err := domain.NewProviderName(describe.Provider.Name)
		if err != nil {
			return DistributionPlan{}, err
		}
		providerVersion, err := domain.NewProviderVersion(describe.Provider.Version)
		if err != nil {
			return DistributionPlan{}, err
		}
		providerRef, err := domain.NewProviderRef(providerName, providerVersion)
		if err != nil {
			return DistributionPlan{}, err
		}
		providerDigest, ok := providerDigestCache[endpoint]
		if !ok {
			providerDigest, _, err = p.hasher.Hash(endpoint.Executable)
			if err != nil {
				return DistributionPlan{}, fmt.Errorf("hash provider %q: %w", targetConfig.Provider.Name, err)
			}
			providerDigestCache[endpoint] = providerDigest
		}
		if existing, exists := providerDigests[providerRef]; exists && existing != providerDigest {
			return DistributionPlan{}, fmt.Errorf("provider %q@%q resolves to multiple executable digests", providerRef.Name(), providerRef.Version())
		}
		providerDigests[providerRef] = providerDigest
		configuration, err := domain.NewJSONValue(targetConfig.Configuration)
		if err != nil {
			return DistributionPlan{}, err
		}
		target, err := domain.NewTarget(targetID, providerRef, configuration)
		if err != nil {
			return DistributionPlan{}, err
		}

		response, err := p.client.Plan(ctx, endpoint, protocol.PlanRequest{
			Release: protocolRelease,
			Target:  protocol.Target{ID: targetConfig.ID, Configuration: append(json.RawMessage(nil), targetConfig.Configuration...)},
		})
		if err != nil {
			return DistributionPlan{}, fmt.Errorf("plan target %q: %w", targetConfig.ID, err)
		}
		if err := response.Validate(); err != nil {
			return DistributionPlan{}, fmt.Errorf("plan target %q: %w", targetConfig.ID, err)
		}

		required := requiredCapabilities(response.Operations)
		if err := ensureCapabilities(describe.Capabilities, required); err != nil {
			return DistributionPlan{}, fmt.Errorf("target %q: %w", targetConfig.ID, err)
		}
		requirements, err := canonicalRequirements(append(append([]protocol.Requirement(nil), describe.Requirements...), response.Requirements...))
		if err != nil {
			return DistributionPlan{}, fmt.Errorf("target %q requirements: %w", targetConfig.ID, err)
		}
		domainRequirements, err := buildDomainRequirements(requirements)
		if err != nil {
			return DistributionPlan{}, fmt.Errorf("target %q requirements: %w", targetConfig.ID, err)
		}
		target, err = domain.NewTargetWithRequirements(targetID, providerRef, configuration, domainRequirements)
		if err != nil {
			return DistributionPlan{}, err
		}

		targets = append(targets, target)
		targetDocs = append(targetDocs, targetDocument{
			ID:                   targetConfig.ID,
			Provider:             providerDocument{Name: describe.Provider.Name, Version: describe.Provider.Version, Digest: providerDigest.String()},
			Configuration:        append(json.RawMessage(nil), targetConfig.Configuration...),
			RequiredCapabilities: append([]protocol.Capability(nil), required...),
			Requirements:         requirements,
		})

		targetDrafts, err := buildOperationDrafts(targetID, providerRef, providerDigest, response.Operations)
		if err != nil {
			return DistributionPlan{}, fmt.Errorf("target %q operations: %w", targetConfig.ID, err)
		}
		drafts = append(drafts, targetDrafts...)
	}

	sort.Slice(targets, func(i, j int) bool { return string(targets[i].ID()) < string(targets[j].ID()) })
	sort.Slice(targetDocs, func(i, j int) bool { return targetDocs[i].ID < targetDocs[j].ID })
	sort.Slice(drafts, func(i, j int) bool { return string(drafts[i].globalID) < string(drafts[j].globalID) })

	semantic := semanticPlan{
		SchemaVersion:   PlanSchemaVersion,
		ProtocolVersion: protocol.Version,
		Release:         semanticReleaseFromDomain(release),
		Targets:         targetDocs,
		Operations:      semanticOperations(drafts),
	}
	planID, err := semanticPlanID(semantic)
	if err != nil {
		return DistributionPlan{}, err
	}

	operations := make([]domain.Operation, 0, len(drafts))
	operationDocs := make([]operationDocument, 0, len(drafts))
	for _, draft := range drafts {
		key := ""
		if draft.sideEffecting {
			key = deriveIdempotencyKey(planID, draft.targetID, draft.globalID, release.Artifacts())
		}
		operation, err := domain.NewOperation(
			draft.globalID,
			draft.targetID,
			draft.provider,
			draft.kind,
			draft.dependencies,
			draft.sideEffecting,
			key,
			draft.timeout,
			draft.providerPayload,
		)
		if err != nil {
			return DistributionPlan{}, err
		}
		operations = append(operations, operation)
		operationDocs = append(operationDocs, operationDocument{
			ID:                  string(draft.globalID),
			ProviderOperationID: draft.providerOperationID,
			TargetID:            string(draft.targetID),
			Provider:            providerDocument{Name: string(draft.provider.Name()), Version: string(draft.provider.Version()), Digest: draft.providerDigest.String()},
			Kind:                draft.kind,
			Dependencies:        operationIDsToStrings(draft.dependencies),
			SideEffecting:       draft.sideEffecting,
			IdempotencyKey:      key,
			TimeoutMillis:       draft.timeoutMillis,
			ProviderPayload:     append(json.RawMessage(nil), draft.providerPayload.Bytes()...),
		})
	}

	plan, err := domain.NewPlan(planID, PlanSchemaVersion, protocol.Version, release, targets, operations)
	if err != nil {
		return DistributionPlan{}, err
	}

	return DistributionPlan{
		plan: plan,
		document: planDocument{
			SchemaVersion:   PlanSchemaVersion,
			ProtocolVersion: protocol.Version,
			PlanID:          string(planID),
			Release:         releaseDocumentFromDomain(release),
			Targets:         targetDocs,
			Operations:      operationDocs,
		},
	}, nil
}

func (p *Planner) buildRelease(loaded config.Loaded) (domain.Release, protocol.Release, error) {
	releaseID, err := domain.NewReleaseID(loaded.Config.Release.ID)
	if err != nil {
		return domain.Release{}, protocol.Release{}, err
	}
	artifactConfigs := append([]config.Artifact(nil), loaded.Config.Release.Artifacts...)
	sort.Slice(artifactConfigs, func(i, j int) bool { return artifactConfigs[i].Name < artifactConfigs[j].Name })

	artifacts := make([]domain.Artifact, 0, len(artifactConfigs))
	protocolArtifacts := make([]protocol.Artifact, 0, len(artifactConfigs))
	for _, artifactConfig := range artifactConfigs {
		source := artifactConfig.Source
		if !filepath.IsAbs(source) {
			source = filepath.Join(loaded.BaseDir, source)
		}
		absolute, err := filepath.Abs(source)
		if err != nil {
			return domain.Release{}, protocol.Release{}, err
		}
		absolute = filepath.Clean(absolute)
		digest, size, err := p.hasher.Hash(absolute)
		if err != nil {
			return domain.Release{}, protocol.Release{}, fmt.Errorf("hash artifact %q: %w", artifactConfig.Name, err)
		}
		artifact, err := domain.NewArtifact(artifactConfig.Name, absolute, digest, size, artifactConfig.MediaType)
		if err != nil {
			return domain.Release{}, protocol.Release{}, err
		}
		artifacts = append(artifacts, artifact)
		protocolArtifacts = append(protocolArtifacts, protocol.Artifact{
			Name: artifact.Name(), Digest: artifact.Digest().String(), Size: artifact.Size(), MediaType: artifact.MediaType(),
		})
	}
	release, err := domain.NewRelease(releaseID, artifacts)
	if err != nil {
		return domain.Release{}, protocol.Release{}, err
	}
	return release, protocol.Release{ID: string(releaseID), Artifacts: protocolArtifacts}, nil
}

func (p *Planner) resolveEndpoint(target config.Target) (Endpoint, error) {
	executable, err := p.resolver.Resolve(target.Provider.Name, target.Provider.Executable)
	if err != nil {
		return Endpoint{}, err
	}
	return Endpoint{Name: target.Provider.Name, Executable: executable}, nil
}

func validateDescription(configuredName string, response protocol.DescribeResponse) error {
	if err := response.Validate(); err != nil {
		return err
	}
	if response.Provider.Name != configuredName {
		return fmt.Errorf("provider reported name %q, expected %q", response.Provider.Name, configuredName)
	}
	supportsVersion := false
	for _, version := range response.ProtocolVersions {
		if version == protocol.Version {
			supportsVersion = true
			break
		}
	}
	if !supportsVersion {
		return fmt.Errorf("provider does not support protocol version %q", protocol.Version)
	}
	return ensureCapabilities(response.Capabilities, []protocol.Capability{protocol.CapabilityPlan})
}

func buildOperationDrafts(targetID domain.TargetID, provider domain.ProviderRef, providerDigest domain.Digest, operations []protocol.PlannedOperation) ([]operationDraft, error) {
	localToGlobal := make(map[string]domain.OperationID, len(operations))
	for _, operation := range operations {
		global, err := namespacedOperationID(targetID, operation.ID)
		if err != nil {
			return nil, err
		}
		localToGlobal[operation.ID] = global
	}

	drafts := make([]operationDraft, 0, len(operations))
	for _, operation := range operations {
		payload, err := canonicaljson.Normalize(operation.ProviderPayload)
		if err != nil {
			return nil, fmt.Errorf("operation %q payload: %w", operation.ID, err)
		}
		jsonValue, err := domain.NewJSONValue(payload)
		if err != nil {
			return nil, err
		}
		dependencies := make([]domain.OperationID, 0, len(operation.Dependencies))
		for _, dependency := range operation.Dependencies {
			global, exists := localToGlobal[dependency]
			if !exists {
				return nil, fmt.Errorf("operation %q references unknown dependency %q", operation.ID, dependency)
			}
			dependencies = append(dependencies, global)
		}
		sort.Slice(dependencies, func(i, j int) bool { return string(dependencies[i]) < string(dependencies[j]) })
		if operation.TimeoutMillis > uint64(math.MaxInt64/int64(time.Millisecond)) {
			return nil, fmt.Errorf("operation %q timeout exceeds supported duration", operation.ID)
		}
		drafts = append(drafts, operationDraft{
			globalID:            localToGlobal[operation.ID],
			providerOperationID: operation.ID,
			targetID:            targetID,
			provider:            provider,
			providerDigest:      providerDigest,
			kind:                operation.Kind,
			dependencies:        dependencies,
			sideEffecting:       operation.SideEffecting,
			timeout:             time.Duration(operation.TimeoutMillis) * time.Millisecond,
			timeoutMillis:       operation.TimeoutMillis,
			providerPayload:     jsonValue,
		})
	}
	sort.Slice(drafts, func(i, j int) bool { return string(drafts[i].globalID) < string(drafts[j].globalID) })
	return drafts, nil
}

func namespacedOperationID(targetID domain.TargetID, providerOperationID string) (domain.OperationID, error) {
	material := string(targetID) + "\x00" + providerOperationID
	sum := sha256.Sum256([]byte(material))
	return domain.NewOperationID("op-" + hex.EncodeToString(sum[:]))
}

func semanticReleaseFromDomain(release domain.Release) semanticRelease {
	artifacts := release.Artifacts()
	result := make([]semanticArtifact, len(artifacts))
	for i, artifact := range artifacts {
		result[i] = semanticArtifact{
			Name: artifact.Name(), Digest: artifact.Digest().String(), Size: artifact.Size(), MediaType: artifact.MediaType(),
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Name < result[j].Name })
	return semanticRelease{ID: string(release.ID()), Artifacts: result}
}

func releaseDocumentFromDomain(release domain.Release) releaseDocument {
	artifacts := release.Artifacts()
	result := make([]artifactDocument, len(artifacts))
	for i, artifact := range artifacts {
		result[i] = artifactDocument{
			Name: artifact.Name(), Source: artifact.Source(), Digest: artifact.Digest().String(), Size: artifact.Size(), MediaType: artifact.MediaType(),
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Name < result[j].Name })
	return releaseDocument{ID: string(release.ID()), Artifacts: result}
}

func semanticOperations(drafts []operationDraft) []semanticOperation {
	result := make([]semanticOperation, len(drafts))
	for i, draft := range drafts {
		result[i] = semanticOperation{
			ID:                  string(draft.globalID),
			ProviderOperationID: draft.providerOperationID,
			TargetID:            string(draft.targetID),
			Provider:            providerDocument{Name: string(draft.provider.Name()), Version: string(draft.provider.Version()), Digest: draft.providerDigest.String()},
			Kind:                draft.kind,
			Dependencies:        operationIDsToStrings(draft.dependencies),
			SideEffecting:       draft.sideEffecting,
			TimeoutMillis:       draft.timeoutMillis,
			ProviderPayload:     append(json.RawMessage(nil), draft.providerPayload.Bytes()...),
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	return result
}

func operationIDsToStrings(values []domain.OperationID) []string {
	if len(values) == 0 {
		return nil
	}
	result := make([]string, len(values))
	for i, value := range values {
		result[i] = string(value)
	}
	sort.Strings(result)
	return result
}

func buildDomainRequirements(values []requirementDocument) ([]domain.Requirement, error) {
	result := make([]domain.Requirement, 0, len(values))
	for _, requirement := range values {
		value, err := domain.NewRequirement(requirement.Kind, requirement.Name, requirement.Metadata)
		if err != nil {
			return nil, err
		}
		result = append(result, value)
	}
	return result, nil
}
