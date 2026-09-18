package evidence

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/aalsanie/distroplane/internal/canonicaljson"
	"github.com/aalsanie/distroplane/internal/domain"
	"github.com/aalsanie/distroplane/internal/journal"
)

const SchemaVersion = "1"

var (
	assignmentSecretPattern = regexp.MustCompile(`(?i)(authorization|token|secret|password|passwd|api[_-]?key|client[_-]?secret|signature)=([^&\s]+)`)
	authorizationPattern    = regexp.MustCompile(`(?i)\b(bearer|basic)\s+[A-Za-z0-9+/=_\-.]+`)
)

type Bundle struct {
	SchemaVersion   string                 `json:"schemaVersion"`
	PlanID          string                 `json:"planId"`
	ProtocolVersion string                 `json:"protocolVersion"`
	Run             RunRecord              `json:"run"`
	Release         ReleaseRecord          `json:"release"`
	Journal         JournalRecord          `json:"journal"`
	Targets         []TargetRecord         `json:"targets"`
	Attestations    []AttestationReference `json:"attestations,omitempty"`
}

type RunRecord struct {
	ID        string `json:"id"`
	Completed bool   `json:"completed"`
	Cancelled bool   `json:"cancelled"`
}

type ReleaseRecord struct {
	ID        string           `json:"id"`
	Artifacts []ArtifactRecord `json:"artifacts"`
}

type ArtifactRecord struct {
	Name      string `json:"name"`
	Digest    string `json:"digest"`
	Size      int64  `json:"size"`
	MediaType string `json:"mediaType,omitempty"`
}

type JournalRecord struct {
	Reference     string `json:"reference"`
	Digest        string `json:"digest"`
	Bytes         int64  `json:"bytes"`
	Events        int    `json:"events"`
	TruncatedTail bool   `json:"truncatedTail,omitempty"`
	SchemaVersion uint16 `json:"schemaVersion"`
}

type ProviderRecord struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

type TargetRecord struct {
	ID                 string            `json:"id"`
	Provider           ProviderRecord    `json:"provider"`
	State              string            `json:"state"`
	ReconcileRequired  bool              `json:"reconcileRequired,omitempty"`
	Ambiguous          bool              `json:"ambiguous,omitempty"`
	ExternalReferences []string          `json:"externalReferences,omitempty"`
	Operations         []OperationRecord `json:"operations"`
}

type OperationRecord struct {
	ID                string          `json:"id"`
	Kind              string          `json:"kind"`
	State             string          `json:"state"`
	Attempt           uint32          `json:"attempt,omitempty"`
	ReconcileRequired bool            `json:"reconcileRequired,omitempty"`
	Ambiguous         bool            `json:"ambiguous,omitempty"`
	Retryable         bool            `json:"retryable,omitempty"`
	ProviderState     string          `json:"providerState,omitempty"`
	Evidence          json.RawMessage `json:"evidence,omitempty"`
	ErrorCode         string          `json:"errorCode,omitempty"`
}

type AttestationReference struct {
	Name   string `json:"name"`
	URI    string `json:"uri"`
	Digest string `json:"digest,omitempty"`
}

func Build(plan domain.Plan, journalBytes []byte, reference string, attestations []AttestationReference) (Bundle, error) {
	if !plan.ID().Valid() {
		return Bundle{}, fmt.Errorf("plan is invalid")
	}
	if err := validateText("journal reference", reference, false, 2048); err != nil {
		return Bundle{}, err
	}
	if len(journalBytes) == 0 {
		return Bundle{}, fmt.Errorf("journal is empty")
	}
	read, err := journal.Read(bytes.NewReader(journalBytes))
	if err != nil {
		return Bundle{}, fmt.Errorf("read journal: %w", err)
	}
	if len(read.Events) == 0 || read.ValidBytes <= 0 || read.ValidBytes > int64(len(journalBytes)) {
		return Bundle{}, fmt.Errorf("journal contains no durable events")
	}
	state, err := journal.Reduce(plan, read.Events)
	if err != nil {
		return Bundle{}, fmt.Errorf("reduce journal: %w", err)
	}
	normalizedAttestations, err := normalizeAttestations(attestations)
	if err != nil {
		return Bundle{}, err
	}

	release := plan.Release()
	artifacts := make([]ArtifactRecord, 0, len(release.Artifacts()))
	for _, artifact := range release.Artifacts() {
		artifacts = append(artifacts, ArtifactRecord{
			Name: artifact.Name(), Digest: artifact.Digest().String(), Size: artifact.Size(), MediaType: artifact.MediaType(),
		})
	}
	sort.Slice(artifacts, func(i, j int) bool { return artifacts[i].Name < artifacts[j].Name })

	targetByID := make(map[domain.TargetID]domain.Target, len(plan.Targets()))
	for _, target := range plan.Targets() {
		targetByID[target.ID()] = target
	}
	operationByID := make(map[domain.OperationID]domain.Operation, len(plan.Operations()))
	for _, operation := range plan.Operations() {
		operationByID[operation.ID()] = operation
	}
	operationsByTarget := make(map[domain.TargetID][]OperationRecord, len(plan.Targets()))
	referencesByTarget := make(map[domain.TargetID]map[string]struct{}, len(plan.Targets()))
	for _, operationState := range state.Operations() {
		operation, ok := operationByID[operationState.ID]
		if !ok {
			return Bundle{}, fmt.Errorf("derived state references unknown operation %q", operationState.ID)
		}
		var normalizedEvidence json.RawMessage
		if len(operationState.Evidence) != 0 {
			normalizedEvidence, err = normalizeEvidence(operationState.Evidence)
			if err != nil {
				return Bundle{}, fmt.Errorf("operation %q evidence: %w", operationState.ID, err)
			}
			for _, reference := range externalReferences(normalizedEvidence) {
				set := referencesByTarget[operationState.TargetID]
				if set == nil {
					set = make(map[string]struct{})
					referencesByTarget[operationState.TargetID] = set
				}
				set[reference] = struct{}{}
			}
		}
		operationsByTarget[operationState.TargetID] = append(operationsByTarget[operationState.TargetID], OperationRecord{
			ID: string(operationState.ID), Kind: operation.Kind(), State: string(operationState.State), Attempt: operationState.Attempt,
			ReconcileRequired: operationState.ReconcileRequired, Ambiguous: operationState.Ambiguous, Retryable: operationState.Retryable,
			ProviderState: operationState.ProviderState, Evidence: normalizedEvidence, ErrorCode: operationState.ErrorCode,
		})
	}
	for targetID := range operationsByTarget {
		sort.Slice(operationsByTarget[targetID], func(i, j int) bool {
			return operationsByTarget[targetID][i].ID < operationsByTarget[targetID][j].ID
		})
	}

	targets := make([]TargetRecord, 0, len(plan.Targets()))
	for _, targetState := range state.Targets() {
		target, ok := targetByID[targetState.ID]
		if !ok {
			return Bundle{}, fmt.Errorf("derived state references unknown target %q", targetState.ID)
		}
		references := make([]string, 0, len(referencesByTarget[targetState.ID]))
		for reference := range referencesByTarget[targetState.ID] {
			references = append(references, reference)
		}
		sort.Strings(references)
		provider := target.Provider()
		operations := operationsByTarget[targetState.ID]
		if operations == nil {
			operations = []OperationRecord{}
		}
		targets = append(targets, TargetRecord{
			ID:       string(targetState.ID),
			Provider: ProviderRecord{Name: string(provider.Name()), Version: string(provider.Version())},
			State:    string(targetState.State), ReconcileRequired: targetState.ReconcileRequired, Ambiguous: targetState.Ambiguous,
			ExternalReferences: references, Operations: operations,
		})
	}
	sort.Slice(targets, func(i, j int) bool { return targets[i].ID < targets[j].ID })

	digest := sha256.Sum256(journalBytes[:read.ValidBytes])
	return Bundle{
		SchemaVersion:   SchemaVersion,
		PlanID:          string(plan.ID()),
		ProtocolVersion: plan.ProtocolVersion(),
		Run:             RunRecord{ID: string(state.RunID), Completed: state.Completed, Cancelled: state.Cancelled},
		Release:         ReleaseRecord{ID: string(release.ID()), Artifacts: artifacts},
		Journal: JournalRecord{
			Reference: reference, Digest: "sha256:" + hex.EncodeToString(digest[:]), Bytes: read.ValidBytes,
			Events: len(read.Events), TruncatedTail: read.TruncatedTail, SchemaVersion: journal.SchemaVersion,
		},
		Targets:      targets,
		Attestations: normalizedAttestations,
	}, nil
}

func Marshal(bundle Bundle) ([]byte, error) {
	if bundle.SchemaVersion != SchemaVersion {
		return nil, fmt.Errorf("unsupported evidence schema version %q", bundle.SchemaVersion)
	}
	raw, err := json.Marshal(bundle)
	if err != nil {
		return nil, err
	}
	return canonicaljson.Normalize(raw)
}

func Digest(raw []byte) string {
	sum := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func normalizeAttestations(values []AttestationReference) ([]AttestationReference, error) {
	result := make([]AttestationReference, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		if err := validateText("attestation name", value.Name, false, 256); err != nil {
			return nil, err
		}
		uri, err := sanitizeURI(value.URI)
		if err != nil {
			return nil, fmt.Errorf("attestation %q URI: %w", value.Name, err)
		}
		if value.Digest != "" {
			if _, err := domain.ParseDigest(value.Digest); err != nil {
				return nil, fmt.Errorf("attestation %q digest: %w", value.Name, err)
			}
		}
		normalized := AttestationReference{Name: value.Name, URI: uri, Digest: value.Digest}
		key := normalized.Name + "\x00" + normalized.URI + "\x00" + normalized.Digest
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, normalized)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Name != result[j].Name {
			return result[i].Name < result[j].Name
		}
		if result[i].URI != result[j].URI {
			return result[i].URI < result[j].URI
		}
		return result[i].Digest < result[j].Digest
	})
	return result, nil
}

func normalizeEvidence(raw json.RawMessage) (json.RawMessage, error) {
	normalized, err := canonicaljson.Normalize(raw)
	if err != nil {
		return nil, err
	}
	decoder := json.NewDecoder(bytes.NewReader(normalized))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	value = sanitizeValue(value)
	sanitized, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	return canonicaljson.Normalize(sanitized)
}

func sanitizeValue(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		result := make(map[string]any, len(typed))
		for key, child := range typed {
			if secretKey(key) {
				result[key] = "[REDACTED]"
				continue
			}
			result[key] = sanitizeValue(child)
		}
		return result
	case []any:
		result := make([]any, len(typed))
		for i, child := range typed {
			result[i] = sanitizeValue(child)
		}
		return result
	case string:
		return sanitizeText(typed)
	default:
		return value
	}
}

func sanitizeText(value string) string {
	value = assignmentSecretPattern.ReplaceAllString(value, "$1=[REDACTED]")
	value = authorizationPattern.ReplaceAllString(value, "$1 [REDACTED]")
	if sanitized, err := sanitizeURI(value); err == nil {
		return sanitized
	}
	return value
}

func sanitizeURI(value string) (string, error) {
	parsed, err := url.Parse(value)
	if err != nil || parsed.Scheme == "" {
		return "", fmt.Errorf("must be an absolute URI")
	}
	if parsed.User != nil {
		parsed.User = url.User("[REDACTED]")
	}
	query := parsed.Query()
	changed := false
	for key := range query {
		if secretKey(key) {
			query.Set(key, "[REDACTED]")
			changed = true
		}
	}
	if changed {
		parsed.RawQuery = query.Encode()
	}
	return parsed.String(), nil
}

func externalReferences(raw json.RawMessage) []string {
	var value any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&value); err != nil {
		return nil
	}
	set := make(map[string]struct{})
	collectReferences(value, set)
	result := make([]string, 0, len(set))
	for reference := range set {
		result = append(result, reference)
	}
	sort.Strings(result)
	return result
}

func collectReferences(value any, result map[string]struct{}) {
	switch typed := value.(type) {
	case map[string]any:
		for _, child := range typed {
			collectReferences(child, result)
		}
	case []any:
		for _, child := range typed {
			collectReferences(child, result)
		}
	case string:
		parsed, err := url.Parse(typed)
		if err == nil && (parsed.Scheme == "https" || parsed.Scheme == "http") && parsed.Host != "" && parsed.User == nil {
			result[parsed.String()] = struct{}{}
		}
	}
}

func secretKey(key string) bool {
	var normalized strings.Builder
	for _, r := range strings.ToLower(key) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			normalized.WriteRune(r)
		}
	}
	switch normalized.String() {
	case "authorization", "credential", "credentials", "password", "passwd", "secret", "token",
		"accesstoken", "refreshtoken", "apikey", "clientsecret", "privatekey", "signature",
		"xamzcredential", "xamzsecuritytoken", "xamzsignature", "googleaccessid":
		return true
	default:
		return false
	}
}

func validateText(name, value string, optional bool, max int) error {
	if value == "" {
		if optional {
			return nil
		}
		return fmt.Errorf("%s must not be empty", name)
	}
	if !utf8.ValidString(value) {
		return fmt.Errorf("%s must be valid UTF-8", name)
	}
	if len(value) > max {
		return fmt.Errorf("%s exceeds %d bytes", name, max)
	}
	if strings.TrimSpace(value) != value {
		return fmt.Errorf("%s must not have surrounding whitespace", name)
	}
	for _, r := range value {
		if unicode.IsControl(r) {
			return fmt.Errorf("%s must not contain control characters", name)
		}
	}
	return nil
}
