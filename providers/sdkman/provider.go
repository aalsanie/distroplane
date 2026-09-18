package sdkman

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"

	"github.com/aalsanie/distroplane/internal/protocol"
)

const (
	Name                     = "sdkman"
	DefaultVersion           = "0.0.0-dev"
	ConsumerKeyEnvironment   = "DISTROPLANE_SDKMAN_CONSUMER_KEY"
	ConsumerTokenEnvironment = "DISTROPLANE_SDKMAN_CONSUMER_TOKEN"
	defaultAPI               = "https://vendors.sdkman.io"
	defaultStateAPI          = "https://state.sdkman.io"
	defaultOperationTimeout  = 120000
)

var platformStateIDs = map[string]string{
	"UNIVERSAL":     "universal",
	"LINUX_64":      "linuxx64",
	"LINUX_ARM64":   "linuxarm64",
	"LINUX_32":      "linuxx32",
	"LINUX_ARM32SF": "linuxarm32sf",
	"LINUX_ARM32HF": "linuxarm32hf",
	"MAC_OSX":       "darwinx64",
	"MAC_ARM64":     "darwinarm64",
	"WINDOWS_64":    "windowsx64",
}

var checksumHexLengths = map[string]int{
	"MD5": 32, "SHA-1": 40, "SHA-224": 56, "SHA-256": 64, "SHA-384": 96, "SHA-512": 128,
}

type Provider struct {
	Version string
	Client  *http.Client
	Getenv  func(string) string
}

type configuration struct {
	Artifact          string               `json:"artifact"`
	Candidate         string               `json:"candidate"`
	Version           string               `json:"version"`
	URL               string               `json:"url"`
	Vendor            string               `json:"vendor,omitempty"`
	Platform          string               `json:"platform,omitempty"`
	Checksums         map[string]string    `json:"checksums,omitempty"`
	API               string               `json:"api,omitempty"`
	StateAPI          string               `json:"stateApi,omitempty"`
	StateDistribution string               `json:"stateDistribution,omitempty"`
	AllowInsecureHTTP bool                 `json:"allowInsecureHTTP,omitempty"`
	Authentication    authenticationConfig `json:"authentication"`
}

type authenticationConfig struct {
	ConsumerKeyCredential   string `json:"consumerKeyCredential"`
	ConsumerTokenCredential string `json:"consumerTokenCredential"`
}

type operationPayload struct {
	Artifact          string            `json:"artifact"`
	Candidate         string            `json:"candidate"`
	Version           string            `json:"version"`
	URL               string            `json:"url"`
	Vendor            string            `json:"vendor,omitempty"`
	Platform          string            `json:"platform"`
	StatePlatform     string            `json:"statePlatform"`
	Checksums         map[string]string `json:"checksums"`
	API               string            `json:"api"`
	StateAPI          string            `json:"stateApi"`
	StateDistribution string            `json:"stateDistribution,omitempty"`
	ArtifactDigest    string            `json:"artifactDigest"`
	ArtifactSize      int64             `json:"artifactSize"`
}

type releaseRequest struct {
	Candidate string            `json:"candidate"`
	Version   string            `json:"version"`
	URL       string            `json:"url"`
	Vendor    string            `json:"vendor,omitempty"`
	Platform  string            `json:"platform,omitempty"`
	Checksums map[string]string `json:"checksums,omitempty"`
}

type evidence struct {
	Candidate            string `json:"candidate"`
	Version              string `json:"version"`
	Vendor               string `json:"vendor,omitempty"`
	Platform             string `json:"platform"`
	DownloadURL          string `json:"downloadUrl"`
	SHA256               string `json:"sha256"`
	PublicationState     string `json:"publicationState"`
	Verification         string `json:"verification"`
	StateEndpoint        string `json:"stateEndpoint,omitempty"`
	ObservedURL          string `json:"observedUrl,omitempty"`
	ObservedSHA256       string `json:"observedSha256,omitempty"`
	ObservedDistribution string `json:"observedDistribution,omitempty"`
	Limitation           string `json:"limitation,omitempty"`
}

func (p Provider) Describe(context.Context, protocol.DescribeRequest) (protocol.DescribeResponse, *protocol.ProviderError) {
	version := strings.TrimSpace(p.Version)
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
	checksums, err := bindArtifactChecksum(cfg.Checksums, artifact.Digest)
	if err != nil {
		return protocol.PlanResponse{}, providerError(protocol.ErrorConfiguration, err.Error(), false)
	}
	payload := operationPayload{
		Artifact: cfg.Artifact, Candidate: cfg.Candidate, Version: cfg.Version, URL: cfg.URL,
		Vendor: cfg.Vendor, Platform: cfg.Platform, StatePlatform: platformStateIDs[cfg.Platform],
		Checksums: checksums, API: cfg.API, StateAPI: cfg.StateAPI, StateDistribution: cfg.StateDistribution,
		ArtifactDigest: artifact.Digest, ArtifactSize: artifact.Size,
	}
	encoded, _ := json.Marshal(payload)
	keyMetadata, _ := json.Marshal(map[string]string{"environment": ConsumerKeyEnvironment})
	tokenMetadata, _ := json.Marshal(map[string]string{"environment": ConsumerTokenEnvironment})
	return protocol.PlanResponse{
		Operations: []protocol.PlannedOperation{{
			ID: "publish", Kind: "publish", SideEffecting: true, TimeoutMillis: defaultOperationTimeout, ProviderPayload: encoded,
		}},
		Requirements: []protocol.Requirement{
			{Kind: "credential", Name: cfg.Authentication.ConsumerKeyCredential, Metadata: keyMetadata},
			{Kind: "credential", Name: cfg.Authentication.ConsumerTokenCredential, Metadata: tokenMetadata},
		},
	}, nil
}

func (p Provider) Apply(ctx context.Context, request protocol.ApplyRequest) (protocol.ApplyResponse, *protocol.ProviderError) {
	if err := request.Validate(); err != nil {
		return protocol.ApplyResponse{}, providerError(protocol.ErrorProtocol, err.Error(), false)
	}
	payload, err := parsePayload(request.ProviderPayload)
	if err != nil {
		return protocol.ApplyResponse{}, providerError(protocol.ErrorConfiguration, err.Error(), false)
	}
	key, token, providerErr := p.credentials()
	if providerErr != nil {
		return protocol.ApplyResponse{}, providerErr
	}
	status, providerErr := p.publish(ctx, payload, key, token)
	if providerErr != nil {
		return protocol.ApplyResponse{}, providerErr
	}
	if status == http.StatusConflict {
		observed, lookupErr := p.lookup(ctx, payload)
		if lookupErr != nil || !observed.exists {
			return protocol.ApplyResponse{}, providerError(protocol.ErrorAmbiguousOutcome, "SDKMAN release conflicted and could not be reconciled", false)
		}
		return protocol.ApplyResponse{Result: resultForObserved(payload, observed)}, nil
	}
	return protocol.ApplyResponse{Result: acceptedResult(payload)}, nil
}

func (p Provider) Reconcile(ctx context.Context, request protocol.ReconcileRequest) (protocol.ReconcileResponse, *protocol.ProviderError) {
	if err := request.Validate(); err != nil {
		return protocol.ReconcileResponse{}, providerError(protocol.ErrorProtocol, err.Error(), false)
	}
	payload, err := parsePayload(request.ProviderPayload)
	if err != nil {
		return protocol.ReconcileResponse{}, providerError(protocol.ErrorConfiguration, err.Error(), false)
	}
	observed, providerErr := p.lookup(ctx, payload)
	if providerErr != nil {
		return protocol.ReconcileResponse{}, providerErr
	}
	if !observed.exists {
		return protocol.ReconcileResponse{Result: absentResult(payload)}, nil
	}
	return protocol.ReconcileResponse{Result: resultForObserved(payload, observed)}, nil
}

func parseConfiguration(raw json.RawMessage) (configuration, error) {
	var cfg configuration
	if err := decodeStrict(raw, &cfg); err != nil {
		return configuration{}, fmt.Errorf("invalid SDKMAN configuration: %w", err)
	}
	if !validToken(cfg.Artifact, 1024) {
		return configuration{}, fmt.Errorf("artifact is invalid")
	}
	if !validToken(cfg.Candidate, 128) {
		return configuration{}, fmt.Errorf("candidate is invalid")
	}
	if !validToken(cfg.Version, 256) {
		return configuration{}, fmt.Errorf("version is invalid")
	}
	downloadURL, err := normalizeDownloadURL(cfg.URL, cfg.AllowInsecureHTTP)
	if err != nil {
		return configuration{}, err
	}
	cfg.URL = downloadURL
	if cfg.Platform == "" {
		cfg.Platform = "UNIVERSAL"
	}
	if _, ok := platformStateIDs[cfg.Platform]; !ok {
		return configuration{}, fmt.Errorf("unsupported SDKMAN platform %q", cfg.Platform)
	}
	if cfg.Vendor != "" && !validToken(cfg.Vendor, 128) {
		return configuration{}, fmt.Errorf("vendor is invalid")
	}
	if cfg.StateDistribution != "" && !validToken(cfg.StateDistribution, 128) {
		return configuration{}, fmt.Errorf("stateDistribution is invalid")
	}
	if cfg.API == "" {
		cfg.API = defaultAPI
	}
	if cfg.StateAPI == "" {
		cfg.StateAPI = defaultStateAPI
	}
	cfg.API, err = normalizeBaseURL(cfg.API, cfg.AllowInsecureHTTP, "api")
	if err != nil {
		return configuration{}, err
	}
	cfg.StateAPI, err = normalizeBaseURL(cfg.StateAPI, cfg.AllowInsecureHTTP, "stateApi")
	if err != nil {
		return configuration{}, err
	}
	if err := validateChecksums(cfg.Checksums); err != nil {
		return configuration{}, err
	}
	if !validCredentialRef(cfg.Authentication.ConsumerKeyCredential) || !validCredentialRef(cfg.Authentication.ConsumerTokenCredential) {
		return configuration{}, fmt.Errorf("SDKMAN credential reference is invalid")
	}
	if cfg.Authentication.ConsumerKeyCredential == cfg.Authentication.ConsumerTokenCredential {
		return configuration{}, fmt.Errorf("SDKMAN consumer key and token credentials must use different references")
	}
	return cfg, nil
}

func parsePayload(raw json.RawMessage) (operationPayload, error) {
	var payload operationPayload
	if err := decodeStrict(raw, &payload); err != nil {
		return operationPayload{}, fmt.Errorf("invalid SDKMAN operation payload: %w", err)
	}
	if !validToken(payload.Artifact, 1024) || !validToken(payload.Candidate, 128) || !validToken(payload.Version, 256) || payload.URL == "" || payload.API == "" || payload.StateAPI == "" || payload.ArtifactDigest == "" || payload.ArtifactSize < 0 {
		return operationPayload{}, fmt.Errorf("SDKMAN operation payload is incomplete")
	}
	statePlatform, ok := platformStateIDs[payload.Platform]
	if !ok || statePlatform != payload.StatePlatform {
		return operationPayload{}, fmt.Errorf("SDKMAN operation platform is invalid")
	}
	if err := validateChecksums(payload.Checksums); err != nil {
		return operationPayload{}, err
	}
	expected, ok := strings.CutPrefix(payload.ArtifactDigest, "sha256:")
	if !ok || payload.Checksums["SHA-256"] != expected {
		return operationPayload{}, fmt.Errorf("SDKMAN operation checksum is not bound to the release artifact")
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

func bindArtifactChecksum(configured map[string]string, digest string) (map[string]string, error) {
	hexDigest, ok := strings.CutPrefix(digest, "sha256:")
	if !ok {
		return nil, fmt.Errorf("release artifact digest is not SHA-256")
	}
	result := make(map[string]string, len(configured)+1)
	for algorithm, value := range configured {
		result[algorithm] = strings.ToLower(value)
	}
	if value, exists := result["SHA-256"]; exists && value != hexDigest {
		return nil, fmt.Errorf("configured SHA-256 checksum does not match release artifact")
	}
	result["SHA-256"] = hexDigest
	return result, nil
}

func validateChecksums(values map[string]string) error {
	for algorithm, value := range values {
		length, ok := checksumHexLengths[algorithm]
		if !ok {
			return fmt.Errorf("unsupported SDKMAN checksum algorithm %q", algorithm)
		}
		if len(value) != length {
			return fmt.Errorf("SDKMAN %s checksum must contain %d hexadecimal characters", algorithm, length)
		}
		if _, err := hex.DecodeString(value); err != nil {
			return fmt.Errorf("SDKMAN %s checksum must be hexadecimal", algorithm)
		}
	}
	return nil
}

func normalizeDownloadURL(raw string, allowInsecure bool) (string, error) {
	value, err := normalizeURL(raw, allowInsecure, false)
	if err != nil {
		return "", fmt.Errorf("download URL is invalid: %w", err)
	}
	return value, nil
}

func normalizeBaseURL(raw string, allowInsecure bool, name string) (string, error) {
	value, err := normalizeURL(raw, allowInsecure, true)
	if err != nil {
		return "", fmt.Errorf("%s URL is invalid: %w", name, err)
	}
	return strings.TrimRight(value, "/"), nil
}

func normalizeURL(raw string, allowInsecure, base bool) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return "", fmt.Errorf("must be an absolute HTTP(S) URL")
	}
	if parsed.Scheme != "https" && parsed.Scheme != "http" {
		return "", fmt.Errorf("must use HTTPS")
	}
	if parsed.Scheme == "http" && !allowInsecure {
		return "", fmt.Errorf("HTTP requires allowInsecureHTTP")
	}
	if parsed.User != nil || parsed.Fragment != "" || (base && parsed.RawQuery != "") {
		return "", fmt.Errorf("must not contain user info, fragment, or base query")
	}
	return parsed.String(), nil
}

func validToken(value string, max int) bool {
	if value == "" || len(value) > max || strings.TrimSpace(value) != value {
		return false
	}
	for _, r := range value {
		if r <= ' ' || r == 0x7f || r == '/' || r == '\\' {
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

func (p Provider) credentials() (string, string, *protocol.ProviderError) {
	key := strings.TrimSpace(p.getenv(ConsumerKeyEnvironment))
	token := strings.TrimSpace(p.getenv(ConsumerTokenEnvironment))
	if key == "" || token == "" {
		return "", "", providerError(protocol.ErrorAuthentication, "required SDKMAN vendor credentials are unavailable", false)
	}
	return key, token, nil
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
