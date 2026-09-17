package domain

import (
	"fmt"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

type Artifact struct {
	name      string
	source    string
	digest    Digest
	size      int64
	mediaType string
}

func NewArtifact(name, source string, digest Digest, size int64, mediaType string) (Artifact, error) {
	if err := validateText("artifact name", name, false); err != nil {
		return Artifact{}, err
	}
	if err := validateText("artifact source", source, false); err != nil {
		return Artifact{}, err
	}
	if !digest.Valid() {
		return Artifact{}, fmt.Errorf("artifact digest is invalid")
	}
	if size < 0 {
		return Artifact{}, fmt.Errorf("artifact size must not be negative")
	}
	if err := validateText("artifact media type", mediaType, true); err != nil {
		return Artifact{}, err
	}
	return Artifact{name: name, source: source, digest: digest, size: size, mediaType: mediaType}, nil
}

func (a Artifact) Name() string      { return a.name }
func (a Artifact) Source() string    { return a.source }
func (a Artifact) Digest() Digest    { return a.digest }
func (a Artifact) Size() int64       { return a.size }
func (a Artifact) MediaType() string { return a.mediaType }

type Release struct {
	id        ReleaseID
	artifacts []Artifact
}

func NewRelease(id ReleaseID, artifacts []Artifact) (Release, error) {
	if !id.Valid() {
		return Release{}, fmt.Errorf("release ID is invalid")
	}
	if len(artifacts) == 0 {
		return Release{}, fmt.Errorf("release must contain at least one artifact")
	}
	names := make(map[string]struct{}, len(artifacts))
	for _, artifact := range artifacts {
		if artifact.name == "" || !artifact.digest.Valid() {
			return Release{}, fmt.Errorf("release contains invalid artifact")
		}
		if _, exists := names[artifact.name]; exists {
			return Release{}, fmt.Errorf("duplicate artifact name %q", artifact.name)
		}
		names[artifact.name] = struct{}{}
	}
	return Release{id: id, artifacts: append([]Artifact(nil), artifacts...)}, nil
}

func (r Release) ID() ReleaseID         { return r.id }
func (r Release) Artifacts() []Artifact { return append([]Artifact(nil), r.artifacts...) }

type ProviderRef struct {
	name    ProviderName
	version ProviderVersion
}

func NewProviderRef(name ProviderName, version ProviderVersion) (ProviderRef, error) {
	if !name.Valid() {
		return ProviderRef{}, fmt.Errorf("provider name is invalid")
	}
	if !version.Valid() {
		return ProviderRef{}, fmt.Errorf("provider version is invalid")
	}
	return ProviderRef{name: name, version: version}, nil
}

func (p ProviderRef) Name() ProviderName       { return p.name }
func (p ProviderRef) Version() ProviderVersion { return p.version }
func (p ProviderRef) Valid() bool              { return p.name.Valid() && p.version.Valid() }

type Target struct {
	id            TargetID
	provider      ProviderRef
	configuration JSONValue
}

func NewTarget(id TargetID, provider ProviderRef, configuration JSONValue) (Target, error) {
	if !id.Valid() {
		return Target{}, fmt.Errorf("target ID is invalid")
	}
	if !provider.Valid() {
		return Target{}, fmt.Errorf("target provider is invalid")
	}
	if !configuration.Valid() {
		return Target{}, fmt.Errorf("target configuration is invalid")
	}
	return Target{id: id, provider: provider, configuration: configuration}, nil
}

func (t Target) ID() TargetID             { return t.id }
func (t Target) Provider() ProviderRef    { return t.provider }
func (t Target) Configuration() JSONValue { return JSONValue{raw: t.configuration.Bytes()} }

type Operation struct {
	id              OperationID
	targetID        TargetID
	provider        ProviderRef
	kind            string
	dependencies    []OperationID
	sideEffecting   bool
	idempotencyKey  string
	timeout         time.Duration
	providerPayload JSONValue
}

func NewOperation(id OperationID, targetID TargetID, provider ProviderRef, kind string, dependencies []OperationID, sideEffecting bool, idempotencyKey string, timeout time.Duration, providerPayload JSONValue) (Operation, error) {
	if !id.Valid() {
		return Operation{}, fmt.Errorf("operation ID is invalid")
	}
	if !targetID.Valid() {
		return Operation{}, fmt.Errorf("operation target ID is invalid")
	}
	if !provider.Valid() {
		return Operation{}, fmt.Errorf("operation provider is invalid")
	}
	if err := validateText("operation kind", kind, false); err != nil {
		return Operation{}, err
	}
	if timeout < 0 {
		return Operation{}, fmt.Errorf("operation timeout must not be negative")
	}
	if !providerPayload.Valid() {
		return Operation{}, fmt.Errorf("operation provider payload is invalid")
	}
	if sideEffecting {
		if err := validateText("idempotency key", idempotencyKey, false); err != nil {
			return Operation{}, err
		}
	} else if idempotencyKey != "" {
		if err := validateText("idempotency key", idempotencyKey, false); err != nil {
			return Operation{}, err
		}
	}
	seen := make(map[OperationID]struct{}, len(dependencies))
	for _, dependency := range dependencies {
		if !dependency.Valid() {
			return Operation{}, fmt.Errorf("operation dependency is invalid")
		}
		if dependency == id {
			return Operation{}, fmt.Errorf("operation cannot depend on itself")
		}
		if _, exists := seen[dependency]; exists {
			return Operation{}, fmt.Errorf("duplicate operation dependency %q", dependency)
		}
		seen[dependency] = struct{}{}
	}
	return Operation{id: id, targetID: targetID, provider: provider, kind: kind, dependencies: append([]OperationID(nil), dependencies...), sideEffecting: sideEffecting, idempotencyKey: idempotencyKey, timeout: timeout, providerPayload: providerPayload}, nil
}

func (o Operation) ID() OperationID             { return o.id }
func (o Operation) TargetID() TargetID          { return o.targetID }
func (o Operation) Provider() ProviderRef       { return o.provider }
func (o Operation) Kind() string                { return o.kind }
func (o Operation) Dependencies() []OperationID { return append([]OperationID(nil), o.dependencies...) }
func (o Operation) SideEffecting() bool         { return o.sideEffecting }
func (o Operation) IdempotencyKey() string      { return o.idempotencyKey }
func (o Operation) Timeout() time.Duration      { return o.timeout }
func (o Operation) ProviderPayload() JSONValue  { return JSONValue{raw: o.providerPayload.Bytes()} }

func validateText(name, value string, optional bool) error {
	if value == "" {
		if optional {
			return nil
		}
		return fmt.Errorf("%s must not be empty", name)
	}
	if !utf8.ValidString(value) {
		return fmt.Errorf("%s must be valid UTF-8", name)
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
