package npm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/aalsanie/distroplane/internal/protocol"
)

const (
	Name                    = "npm"
	DefaultVersion          = "0.0.0-dev"
	TokenEnvironment        = "DISTROPLANE_NPM_TOKEN"
	OIDCTokenEnvironment    = "DISTROPLANE_NPM_ID_TOKEN"
	defaultTag              = "latest"
	defaultOperationTimeout = 120000
)

var semverPattern = regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(?:-([0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*))?(?:\+([0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*))?$`)

type Provider struct {
	Version string
	Client  *http.Client
	Getenv  func(string) string
}

type configuration struct {
	Artifact          string               `json:"artifact"`
	PackagePath       string               `json:"packagePath"`
	Registry          string               `json:"registry"`
	Tag               string               `json:"tag,omitempty"`
	Access            string               `json:"access,omitempty"`
	AllowInsecureHTTP bool                 `json:"allowInsecureHTTP,omitempty"`
	Authentication    authenticationConfig `json:"authentication"`
}

type authenticationConfig struct {
	Mode       string `json:"mode"`
	Credential string `json:"credential"`
}

type operationPayload struct {
	Artifact       string `json:"artifact"`
	PackagePath    string `json:"packagePath"`
	Package        string `json:"package"`
	Version        string `json:"version"`
	Registry       string `json:"registry"`
	Tag            string `json:"tag"`
	Access         string `json:"access,omitempty"`
	Authentication string `json:"authentication"`
	SHA256         string `json:"sha256"`
	Size           int64  `json:"size"`
}

type evidence struct {
	Package           string `json:"package"`
	Version           string `json:"version"`
	Registry          string `json:"registry"`
	PackageURL        string `json:"packageUrl"`
	Integrity         string `json:"integrity"`
	Shasum            string `json:"shasum"`
	SHA256            string `json:"sha256"`
	PublicationState  string `json:"publicationState"`
	ObservedIntegrity string `json:"observedIntegrity,omitempty"`
	ObservedShasum    string `json:"observedShasum,omitempty"`
}

func (p Provider) Describe(context.Context, protocol.DescribeRequest) (protocol.DescribeResponse, *protocol.ProviderError) {
	version := p.Version
	if version == "" {
		version = DefaultVersion
	}
	return protocol.DescribeResponse{
		Provider:         protocol.ProviderIdentity{Name: Name, Version: version},
		ProtocolVersions: []string{protocol.Version},
		Capabilities: []protocol.Capability{
			protocol.CapabilityPlan,
			protocol.CapabilityApply,
			protocol.CapabilityReconcile,
		},
	}, nil
}

func (p Provider) Plan(_ context.Context, request protocol.PlanRequest) (protocol.PlanResponse, *protocol.ProviderError) {
	if err := request.Validate(); err != nil {
		return protocol.PlanResponse{}, providerError(protocol.ErrorProtocol, err.Error(), false)
	}
	cfg, err := parseConfiguration(request.Target.Configuration)
	if err != nil {
		return protocol.PlanResponse{}, providerError(protocol.ErrorConfiguration, err.Error(), false)
	}
	artifact, err := selectArtifact(request.Release.Artifacts, cfg.Artifact)
	if err != nil {
		return protocol.PlanResponse{}, providerError(protocol.ErrorConfiguration, err.Error(), false)
	}
	packageData, err := loadPackage(cfg.PackagePath, artifact.Digest, artifact.Size)
	if err != nil {
		return protocol.PlanResponse{}, providerError(protocol.ErrorConfiguration, err.Error(), false)
	}
	payload := operationPayload{
		Artifact: cfg.Artifact, PackagePath: cfg.PackagePath, Package: packageData.name, Version: packageData.version,
		Registry: cfg.Registry, Tag: cfg.Tag, Access: cfg.Access, Authentication: cfg.Authentication.Mode,
		SHA256: artifact.Digest, Size: artifact.Size,
	}
	encoded, _ := json.Marshal(payload)
	metadata, _ := json.Marshal(map[string]string{"environment": credentialEnvironment(cfg.Authentication.Mode)})
	return protocol.PlanResponse{
		Operations: []protocol.PlannedOperation{{
			ID: "publish", Kind: "publish", SideEffecting: true, TimeoutMillis: defaultOperationTimeout, ProviderPayload: encoded,
		}},
		Requirements: []protocol.Requirement{{Kind: "credential", Name: cfg.Authentication.Credential, Metadata: metadata}},
	}, nil
}

func (p Provider) Apply(ctx context.Context, request protocol.ApplyRequest) (protocol.ApplyResponse, *protocol.ProviderError) {
	if err := request.Validate(); err != nil {
		return protocol.ApplyResponse{}, providerError(protocol.ErrorProtocol, err.Error(), false)
	}
	payload, packageData, providerErr := p.prepareExecution(request.ProviderPayload)
	if providerErr != nil {
		return protocol.ApplyResponse{}, providerErr
	}
	token, providerErr := p.authenticationToken(ctx, payload)
	if providerErr != nil {
		return protocol.ApplyResponse{}, providerErr
	}
	observed, providerErr := p.lookup(ctx, payload, token)
	if providerErr != nil {
		return protocol.ApplyResponse{}, providerErr
	}
	if observed.exists {
		result := resultForObserved(payload, packageData, observed)
		return protocol.ApplyResponse{Result: result}, nil
	}
	observed, providerErr = p.publish(ctx, payload, packageData, token)
	if providerErr != nil {
		return protocol.ApplyResponse{}, providerErr
	}
	return protocol.ApplyResponse{Result: resultForObserved(payload, packageData, observed)}, nil
}

func (p Provider) Reconcile(ctx context.Context, request protocol.ReconcileRequest) (protocol.ReconcileResponse, *protocol.ProviderError) {
	if err := request.Validate(); err != nil {
		return protocol.ReconcileResponse{}, providerError(protocol.ErrorProtocol, err.Error(), false)
	}
	payload, packageData, providerErr := p.prepareExecution(request.ProviderPayload)
	if providerErr != nil {
		return protocol.ReconcileResponse{}, providerErr
	}
	token, providerErr := p.authenticationToken(ctx, payload)
	if providerErr != nil {
		return protocol.ReconcileResponse{}, providerErr
	}
	observed, providerErr := p.lookup(ctx, payload, token)
	if providerErr != nil {
		return protocol.ReconcileResponse{}, providerErr
	}
	if !observed.exists {
		result := distributionResult(protocol.ResultWaitingExternal, "absent", payload, packageData, observed)
		return protocol.ReconcileResponse{Result: result}, nil
	}
	return protocol.ReconcileResponse{Result: resultForObserved(payload, packageData, observed)}, nil
}

func (p Provider) prepareExecution(raw json.RawMessage) (operationPayload, packageArchive, *protocol.ProviderError) {
	payload, err := parsePayload(raw)
	if err != nil {
		return operationPayload{}, packageArchive{}, providerError(protocol.ErrorConfiguration, err.Error(), false)
	}
	packageData, err := loadPackage(payload.PackagePath, payload.SHA256, payload.Size)
	if err != nil {
		return operationPayload{}, packageArchive{}, providerError(protocol.ErrorConfiguration, err.Error(), false)
	}
	if packageData.name != payload.Package || packageData.version != payload.Version {
		return operationPayload{}, packageArchive{}, providerError(protocol.ErrorConfiguration, "package identity changed since planning", false)
	}
	return payload, packageData, nil
}

func parseConfiguration(raw json.RawMessage) (configuration, error) {
	var cfg configuration
	if err := decodeStrict(raw, &cfg); err != nil {
		return configuration{}, fmt.Errorf("invalid npm configuration: %w", err)
	}
	if strings.TrimSpace(cfg.Artifact) == "" {
		return configuration{}, fmt.Errorf("artifact must not be empty")
	}
	if cfg.PackagePath == "" || !filepath.IsAbs(cfg.PackagePath) {
		return configuration{}, fmt.Errorf("packagePath must be an absolute path")
	}
	registry, err := normalizeRegistry(cfg.Registry, cfg.AllowInsecureHTTP)
	if err != nil {
		return configuration{}, err
	}
	cfg.Registry = registry
	if cfg.Tag == "" {
		cfg.Tag = defaultTag
	}
	if !validTag(cfg.Tag) {
		return configuration{}, fmt.Errorf("invalid npm dist-tag %q", cfg.Tag)
	}
	if cfg.Access != "" && cfg.Access != "public" && cfg.Access != "restricted" {
		return configuration{}, fmt.Errorf("access must be public or restricted")
	}
	if cfg.Authentication.Mode != "token" && cfg.Authentication.Mode != "trusted-publishing" {
		return configuration{}, fmt.Errorf("authentication mode must be token or trusted-publishing")
	}
	if !validCredentialRef(cfg.Authentication.Credential) {
		return configuration{}, fmt.Errorf("authentication credential reference is invalid")
	}
	return cfg, nil
}

func parsePayload(raw json.RawMessage) (operationPayload, error) {
	var payload operationPayload
	if err := decodeStrict(raw, &payload); err != nil {
		return operationPayload{}, fmt.Errorf("invalid npm operation payload: %w", err)
	}
	if payload.Artifact == "" || payload.PackagePath == "" || !filepath.IsAbs(payload.PackagePath) || payload.Package == "" || payload.Version == "" || payload.Registry == "" || payload.Tag == "" || payload.SHA256 == "" || payload.Size < 0 {
		return operationPayload{}, fmt.Errorf("npm operation payload is incomplete")
	}
	if payload.Authentication != "token" && payload.Authentication != "trusted-publishing" {
		return operationPayload{}, fmt.Errorf("npm operation authentication is invalid")
	}
	return payload, nil
}

func decodeStrict(raw []byte, destination any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return fmt.Errorf("trailing JSON data")
		}
		return err
	}
	return nil
}

func selectArtifact(artifacts []protocol.Artifact, name string) (protocol.Artifact, error) {
	for _, artifact := range artifacts {
		if artifact.Name == name {
			return artifact, nil
		}
	}
	return protocol.Artifact{}, fmt.Errorf("release artifact %q was not found", name)
}

func normalizeRegistry(raw string, allowInsecure bool) (string, error) {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return "", fmt.Errorf("registry must be an absolute HTTP(S) URL")
	}
	if parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", fmt.Errorf("registry URL must not contain user info, query, or fragment")
	}
	if parsed.Scheme != "https" && parsed.Scheme != "http" {
		return "", fmt.Errorf("registry URL must use HTTPS")
	}
	if parsed.Scheme == "http" && !allowInsecure {
		return "", fmt.Errorf("HTTP registry requires allowInsecureHTTP")
	}
	parsed.Path = strings.TrimRight(parsed.Path, "/") + "/"
	return parsed.String(), nil
}

func validTag(value string) bool {
	if value == "" || len(value) > 214 || semverPattern.MatchString(value) {
		return false
	}
	for _, r := range value {
		if r <= ' ' || r == '/' || r == '\\' {
			return false
		}
	}
	return true
}

func validCredentialRef(value string) bool {
	if value == "" || len(value) > 256 || strings.TrimSpace(value) != value {
		return false
	}
	for _, r := range value {
		if r < 0x20 || r == 0x7f || r == ' ' || r == '\t' || r == '\n' || r == '\r' {
			return false
		}
	}
	return true
}

func credentialEnvironment(mode string) string {
	if mode == "trusted-publishing" {
		return OIDCTokenEnvironment
	}
	return TokenEnvironment
}

func (p Provider) client() *http.Client {
	if p.Client != nil {
		return p.Client
	}
	return http.DefaultClient
}

func (p Provider) getenv(name string) string {
	if p.Getenv != nil {
		return p.Getenv(name)
	}
	return os.Getenv(name)
}

func providerError(code protocol.ErrorCode, message string, retryable bool) *protocol.ProviderError {
	value := protocol.NewProviderError(code, message, retryable)
	return &value
}
