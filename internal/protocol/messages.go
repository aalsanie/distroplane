package protocol

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
)

type Capability string

const (
	CapabilityPlan      Capability = "plan"
	CapabilityApply     Capability = "apply"
	CapabilityReconcile Capability = "reconcile"
)

func (c Capability) Valid() bool {
	return c == CapabilityPlan || c == CapabilityApply || c == CapabilityReconcile
}

type ProviderIdentity struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

func (p ProviderIdentity) Validate() error {
	if err := validateText("provider name", p.Name, 256, false); err != nil {
		return err
	}
	return validateText("provider version", p.Version, 256, false)
}

type DescribeRequest struct{}

type DescribeResponse struct {
	Provider         ProviderIdentity `json:"provider"`
	ProtocolVersions []string         `json:"protocolVersions"`
	Capabilities     []Capability     `json:"capabilities"`
	Requirements     []Requirement    `json:"requirements,omitempty"`
}

func (r DescribeResponse) Validate() error {
	if err := r.Provider.Validate(); err != nil {
		return err
	}
	if len(r.ProtocolVersions) == 0 {
		return fmt.Errorf("describe response must contain protocol versions")
	}
	versions := make(map[string]struct{}, len(r.ProtocolVersions))
	for _, version := range r.ProtocolVersions {
		if err := validateVersion(version); err != nil {
			return err
		}
		if _, exists := versions[version]; exists {
			return fmt.Errorf("duplicate protocol version %q", version)
		}
		versions[version] = struct{}{}
	}
	capabilities := make(map[Capability]struct{}, len(r.Capabilities))
	for _, capability := range r.Capabilities {
		if !capability.Valid() {
			return fmt.Errorf("invalid capability %q", capability)
		}
		if _, exists := capabilities[capability]; exists {
			return fmt.Errorf("duplicate capability %q", capability)
		}
		capabilities[capability] = struct{}{}
	}
	for _, requirement := range r.Requirements {
		if err := requirement.Validate(); err != nil {
			return err
		}
	}
	return nil
}

type Artifact struct {
	Name      string `json:"name"`
	Digest    string `json:"digest"`
	Size      int64  `json:"size"`
	MediaType string `json:"mediaType,omitempty"`
}

func (a Artifact) Validate() error {
	if err := validateText("artifact name", a.Name, 1024, false); err != nil {
		return err
	}
	if err := validateSHA256(a.Digest); err != nil {
		return err
	}
	if a.Size < 0 {
		return fmt.Errorf("artifact size must not be negative")
	}
	return validateText("artifact media type", a.MediaType, 1024, true)
}

type Release struct {
	ID        string     `json:"id"`
	Artifacts []Artifact `json:"artifacts"`
}

func (r Release) Validate() error {
	if err := validateText("release ID", r.ID, 256, false); err != nil {
		return err
	}
	if len(r.Artifacts) == 0 {
		return fmt.Errorf("release must contain at least one artifact")
	}
	names := make(map[string]struct{}, len(r.Artifacts))
	for _, artifact := range r.Artifacts {
		if err := artifact.Validate(); err != nil {
			return err
		}
		if _, exists := names[artifact.Name]; exists {
			return fmt.Errorf("duplicate artifact name %q", artifact.Name)
		}
		names[artifact.Name] = struct{}{}
	}
	return nil
}

type Target struct {
	ID            string          `json:"id"`
	Configuration json.RawMessage `json:"configuration"`
}

func (t Target) Validate() error {
	if err := validateText("target ID", t.ID, 256, false); err != nil {
		return err
	}
	if len(t.Configuration) == 0 || !json.Valid(t.Configuration) {
		return fmt.Errorf("target configuration must be valid JSON")
	}
	return nil
}

type Requirement struct {
	Kind     string          `json:"kind"`
	Name     string          `json:"name"`
	Metadata json.RawMessage `json:"metadata,omitempty"`
}

func (r Requirement) Validate() error {
	if err := validateText("requirement kind", r.Kind, 128, false); err != nil {
		return err
	}
	if err := validateText("requirement name", r.Name, 256, false); err != nil {
		return err
	}
	if len(r.Metadata) != 0 && !json.Valid(r.Metadata) {
		return fmt.Errorf("requirement metadata must be valid JSON")
	}
	return nil
}

type PlanRequest struct {
	Release Release `json:"release"`
	Target  Target  `json:"target"`
}

func (r PlanRequest) Validate() error {
	if err := r.Release.Validate(); err != nil {
		return err
	}
	return r.Target.Validate()
}

type PlannedOperation struct {
	ID              string          `json:"id"`
	Kind            string          `json:"kind"`
	Dependencies    []string        `json:"dependencies,omitempty"`
	SideEffecting   bool            `json:"sideEffecting"`
	TimeoutMillis   uint64          `json:"timeoutMillis,omitempty"`
	ProviderPayload json.RawMessage `json:"providerPayload"`
}

func (o PlannedOperation) Validate() error {
	if err := validateText("operation ID", o.ID, 256, false); err != nil {
		return err
	}
	if err := validateText("operation kind", o.Kind, 256, false); err != nil {
		return err
	}
	if len(o.ProviderPayload) == 0 || !json.Valid(o.ProviderPayload) {
		return fmt.Errorf("operation provider payload must be valid JSON")
	}
	seen := make(map[string]struct{}, len(o.Dependencies))
	for _, dependency := range o.Dependencies {
		if err := validateText("operation dependency", dependency, 256, false); err != nil {
			return err
		}
		if dependency == o.ID {
			return fmt.Errorf("operation cannot depend on itself")
		}
		if _, exists := seen[dependency]; exists {
			return fmt.Errorf("duplicate operation dependency %q", dependency)
		}
		seen[dependency] = struct{}{}
	}
	return nil
}

type PlanResponse struct {
	Operations   []PlannedOperation `json:"operations"`
	Requirements []Requirement      `json:"requirements,omitempty"`
}

func (r PlanResponse) Validate() error {
	operations := make(map[string]PlannedOperation, len(r.Operations))
	for _, operation := range r.Operations {
		if err := operation.Validate(); err != nil {
			return err
		}
		if _, exists := operations[operation.ID]; exists {
			return fmt.Errorf("duplicate operation ID %q", operation.ID)
		}
		operations[operation.ID] = operation
	}
	for _, operation := range r.Operations {
		for _, dependency := range operation.Dependencies {
			if _, exists := operations[dependency]; !exists {
				return fmt.Errorf("operation %q references unknown dependency %q", operation.ID, dependency)
			}
		}
	}
	if err := validateOperationGraph(operations); err != nil {
		return err
	}
	for _, requirement := range r.Requirements {
		if err := requirement.Validate(); err != nil {
			return err
		}
	}
	return nil
}

type ApplyRequest struct {
	PlanID          string          `json:"planId"`
	TargetID        string          `json:"targetId"`
	OperationID     string          `json:"operationId"`
	IdempotencyKey  string          `json:"idempotencyKey"`
	Attempt         uint32          `json:"attempt"`
	ProviderPayload json.RawMessage `json:"providerPayload"`
}

func (r ApplyRequest) Validate() error {
	return validateExecutionRequest(r.PlanID, r.TargetID, r.OperationID, r.IdempotencyKey, r.Attempt, r.ProviderPayload)
}

type ReconcileRequest struct {
	PlanID          string              `json:"planId"`
	TargetID        string              `json:"targetId"`
	OperationID     string              `json:"operationId"`
	IdempotencyKey  string              `json:"idempotencyKey"`
	Attempt         uint32              `json:"attempt"`
	ProviderPayload json.RawMessage     `json:"providerPayload"`
	Previous        *DistributionResult `json:"previous,omitempty"`
}

func (r ReconcileRequest) Validate() error {
	if err := validateExecutionRequest(r.PlanID, r.TargetID, r.OperationID, r.IdempotencyKey, r.Attempt, r.ProviderPayload); err != nil {
		return err
	}
	if r.Previous != nil {
		return r.Previous.Validate()
	}
	return nil
}

type ResultState string

const (
	ResultWaitingExternal ResultState = "WAITING_EXTERNAL"
	ResultPublished       ResultState = "PUBLISHED"
	ResultRejected        ResultState = "REJECTED"
)

func (s ResultState) Valid() bool {
	return s == ResultWaitingExternal || s == ResultPublished || s == ResultRejected
}

type DistributionResult struct {
	State         ResultState     `json:"state"`
	ProviderState string          `json:"providerState,omitempty"`
	Evidence      json.RawMessage `json:"evidence"`
}

func (r DistributionResult) Validate() error {
	if !r.State.Valid() {
		return fmt.Errorf("invalid result state %q", r.State)
	}
	if err := validateText("provider state", r.ProviderState, 1024, true); err != nil {
		return err
	}
	if len(r.Evidence) == 0 || !json.Valid(r.Evidence) {
		return fmt.Errorf("result evidence must be valid JSON")
	}
	return nil
}

type ApplyResponse struct {
	Result DistributionResult `json:"result"`
}

func (r ApplyResponse) Validate() error { return r.Result.Validate() }

type ReconcileResponse struct {
	Result DistributionResult `json:"result"`
}

func (r ReconcileResponse) Validate() error { return r.Result.Validate() }

func validateExecutionRequest(planID, targetID, operationID, idempotencyKey string, attempt uint32, payload json.RawMessage) error {
	fields := []struct {
		name  string
		value string
	}{{"plan ID", planID}, {"target ID", targetID}, {"operation ID", operationID}, {"idempotency key", idempotencyKey}}
	for _, field := range fields {
		if err := validateText(field.name, field.value, 512, false); err != nil {
			return err
		}
	}
	if attempt == 0 {
		return fmt.Errorf("attempt must be greater than zero")
	}
	if len(payload) == 0 || !json.Valid(payload) {
		return fmt.Errorf("provider payload must be valid JSON")
	}
	return nil
}

func validateSHA256(value string) error {
	encoded, ok := strings.CutPrefix(value, "sha256:")
	if !ok || len(encoded) != 64 || encoded != strings.ToLower(encoded) {
		return fmt.Errorf("artifact digest must use sha256:<lowercase hex>")
	}
	if _, err := hex.DecodeString(encoded); err != nil {
		return fmt.Errorf("artifact digest must use sha256:<lowercase hex>: %w", err)
	}
	return nil
}

func validateOperationGraph(operations map[string]PlannedOperation) error {
	indegree := make(map[string]int, len(operations))
	dependents := make(map[string][]string, len(operations))
	queue := make([]string, 0, len(operations))
	for id, operation := range operations {
		indegree[id] = len(operation.Dependencies)
		if len(operation.Dependencies) == 0 {
			queue = append(queue, id)
		}
		for _, dependency := range operation.Dependencies {
			dependents[dependency] = append(dependents[dependency], id)
		}
	}
	processed := 0
	for len(queue) > 0 {
		id := queue[len(queue)-1]
		queue = queue[:len(queue)-1]
		processed++
		for _, dependent := range dependents[id] {
			indegree[dependent]--
			if indegree[dependent] == 0 {
				queue = append(queue, dependent)
			}
		}
	}
	if processed != len(operations) {
		return fmt.Errorf("operation dependency graph contains a cycle")
	}
	return nil
}
