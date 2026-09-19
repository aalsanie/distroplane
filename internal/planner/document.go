package planner

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"github.com/aalsanie/distroplane/internal/canonicaljson"
	"github.com/aalsanie/distroplane/internal/domain"
	"github.com/aalsanie/distroplane/internal/protocol"
)

const PlanSchemaVersion = "1"

type providerDocument struct {
	Name    string `json:"name"`
	Version string `json:"version"`
	Digest  string `json:"digest,omitempty"`
}

type artifactDocument struct {
	Name      string `json:"name"`
	Source    string `json:"source,omitempty"`
	Digest    string `json:"digest"`
	Size      int64  `json:"size"`
	MediaType string `json:"mediaType,omitempty"`
}

type releaseDocument struct {
	ID        string             `json:"id"`
	Artifacts []artifactDocument `json:"artifacts"`
}

type requirementDocument struct {
	Kind     string          `json:"kind"`
	Name     string          `json:"name"`
	Metadata json.RawMessage `json:"metadata,omitempty"`
}

type targetDocument struct {
	ID                   string                `json:"id"`
	Provider             providerDocument      `json:"provider"`
	Configuration        json.RawMessage       `json:"configuration"`
	RequiredCapabilities []protocol.Capability `json:"requiredCapabilities"`
	Requirements         []requirementDocument `json:"requirements,omitempty"`
}

type operationDocument struct {
	ID                  string           `json:"id"`
	ProviderOperationID string           `json:"providerOperationId"`
	TargetID            string           `json:"targetId"`
	Provider            providerDocument `json:"provider"`
	Kind                string           `json:"kind"`
	Dependencies        []string         `json:"dependencies,omitempty"`
	SideEffecting       bool             `json:"sideEffecting"`
	IdempotencyKey      string           `json:"idempotencyKey,omitempty"`
	TimeoutMillis       uint64           `json:"timeoutMillis,omitempty"`
	ProviderPayload     json.RawMessage  `json:"providerPayload"`
}

type planDocument struct {
	SchemaVersion   string              `json:"schemaVersion"`
	ProtocolVersion string              `json:"protocolVersion"`
	PlanID          string              `json:"planId"`
	Release         releaseDocument     `json:"release"`
	Targets         []targetDocument    `json:"targets"`
	Operations      []operationDocument `json:"operations"`
}

type semanticOperation struct {
	ID                  string           `json:"id"`
	ProviderOperationID string           `json:"providerOperationId"`
	TargetID            string           `json:"targetId"`
	Provider            providerDocument `json:"provider"`
	Kind                string           `json:"kind"`
	Dependencies        []string         `json:"dependencies,omitempty"`
	SideEffecting       bool             `json:"sideEffecting"`
	TimeoutMillis       uint64           `json:"timeoutMillis,omitempty"`
	ProviderPayload     json.RawMessage  `json:"providerPayload"`
}

type semanticArtifact struct {
	Name      string `json:"name"`
	Digest    string `json:"digest"`
	Size      int64  `json:"size"`
	MediaType string `json:"mediaType,omitempty"`
}

type semanticRelease struct {
	ID        string             `json:"id"`
	Artifacts []semanticArtifact `json:"artifacts"`
}

type semanticPlan struct {
	SchemaVersion   string              `json:"schemaVersion"`
	ProtocolVersion string              `json:"protocolVersion"`
	Release         semanticRelease     `json:"release"`
	Targets         []targetDocument    `json:"targets"`
	Operations      []semanticOperation `json:"operations"`
}

type operationDraft struct {
	globalID            domain.OperationID
	providerOperationID string
	targetID            domain.TargetID
	provider            domain.ProviderRef
	providerDigest      domain.Digest
	kind                string
	dependencies        []domain.OperationID
	sideEffecting       bool
	timeout             time.Duration
	timeoutMillis       uint64
	providerPayload     domain.JSONValue
}

type DistributionPlan struct {
	plan     domain.Plan
	document planDocument
}

func (p DistributionPlan) Plan() domain.Plan { return p.plan }

func (p DistributionPlan) ID() domain.PlanID { return p.plan.ID() }

func (p DistributionPlan) ProviderDigests() map[domain.ProviderRef]domain.Digest {
	result := make(map[domain.ProviderRef]domain.Digest)
	for _, target := range p.document.Targets {
		if target.Provider.Digest == "" {
			continue
		}
		ref, err := providerRefFromDocument(target.Provider)
		if err != nil {
			continue
		}
		digest, err := domain.ParseDigest(target.Provider.Digest)
		if err != nil {
			continue
		}
		result[ref] = digest
	}
	return result
}

func (p DistributionPlan) Bytes() ([]byte, error) {
	return json.Marshal(p.document)
}

func semanticPlanID(value semanticPlan) (domain.PlanID, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return domain.NewPlanID("sha256-" + hex.EncodeToString(sum[:]))
}

func deriveIdempotencyKey(planID domain.PlanID, targetID domain.TargetID, operationID domain.OperationID, artifacts []domain.Artifact) string {
	digests := make([]string, len(artifacts))
	for i, artifact := range artifacts {
		digests[i] = artifact.Digest().String()
	}
	sort.Strings(digests)
	material := struct {
		Version     string   `json:"version"`
		PlanID      string   `json:"planId"`
		TargetID    string   `json:"targetId"`
		OperationID string   `json:"operationId"`
		Artifacts   []string `json:"artifacts"`
	}{
		Version: "1", PlanID: string(planID), TargetID: string(targetID), OperationID: string(operationID), Artifacts: digests,
	}
	data, _ := json.Marshal(material)
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func canonicalRequirement(requirement protocol.Requirement) (requirementDocument, error) {
	var metadata json.RawMessage
	if len(requirement.Metadata) != 0 {
		normalized, err := canonicaljson.Normalize(requirement.Metadata)
		if err != nil {
			return requirementDocument{}, err
		}
		metadata = normalized
	}
	return requirementDocument{Kind: requirement.Kind, Name: requirement.Name, Metadata: metadata}, nil
}

func canonicalRequirements(values []protocol.Requirement) ([]requirementDocument, error) {
	result := make([]requirementDocument, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		if err := value.Validate(); err != nil {
			return nil, err
		}
		normalized, err := canonicalRequirement(value)
		if err != nil {
			return nil, err
		}
		keyBytes, _ := json.Marshal(normalized)
		key := string(keyBytes)
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, normalized)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Kind != result[j].Kind {
			return result[i].Kind < result[j].Kind
		}
		if result[i].Name != result[j].Name {
			return result[i].Name < result[j].Name
		}
		left, _ := json.Marshal(result[i].Metadata)
		right, _ := json.Marshal(result[j].Metadata)
		return string(left) < string(right)
	})
	return result, nil
}

func requiredCapabilities(operations []protocol.PlannedOperation) []protocol.Capability {
	required := []protocol.Capability{protocol.CapabilityPlan}
	if len(operations) != 0 {
		required = append(required, protocol.CapabilityApply)
	}
	for _, operation := range operations {
		if operation.SideEffecting {
			required = append(required, protocol.CapabilityReconcile)
			break
		}
	}
	return required
}

func ensureCapabilities(advertised, required []protocol.Capability) error {
	set := make(map[protocol.Capability]struct{}, len(advertised))
	for _, capability := range advertised {
		set[capability] = struct{}{}
	}
	for _, capability := range required {
		if _, exists := set[capability]; !exists {
			return fmt.Errorf("provider is missing required capability %q", capability)
		}
	}
	return nil
}
