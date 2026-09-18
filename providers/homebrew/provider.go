package homebrew

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"regexp"
	"strings"

	"github.com/aalsanie/distroplane/internal/protocol"
)

const (
	Name                    = "homebrew"
	DefaultVersion          = "0.0.0-dev"
	TokenEnvironment        = "DISTROPLANE_HOMEBREW_TOKEN"
	defaultBranch           = "main"
	defaultOperationTimeout = 120000
)

var rubyClassPattern = regexp.MustCompile(`^[A-Z][A-Za-z0-9]*$`)
var caskTokenPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9+._-]*$`)

type Provider struct {
	Version string
	Client  *http.Client
	Getenv  func(string) string
	Git     GitRunner
}

type configuration struct {
	Artifact       string               `json:"artifact"`
	Repository     string               `json:"repository"`
	Branch         string               `json:"branch,omitempty"`
	Path           string               `json:"path"`
	Mode           string               `json:"mode"`
	UpdateBranch   string               `json:"updateBranch,omitempty"`
	Manifest       manifestConfig       `json:"manifest"`
	Commit         commitConfig         `json:"commit,omitempty"`
	Authentication authenticationConfig `json:"authentication,omitempty"`
	PullRequest    pullRequestConfig    `json:"pullRequest,omitempty"`
}

type authenticationConfig struct {
	Credential string `json:"credential,omitempty"`
}

type commitConfig struct {
	Message     string `json:"message,omitempty"`
	AuthorName  string `json:"authorName,omitempty"`
	AuthorEmail string `json:"authorEmail,omitempty"`
}

type pullRequestConfig struct {
	API               string `json:"api,omitempty"`
	Repository        string `json:"repository,omitempty"`
	Title             string `json:"title,omitempty"`
	Body              string `json:"body,omitempty"`
	AllowInsecureHTTP bool   `json:"allowInsecureHTTP,omitempty"`
}

type manifestConfig struct {
	Type          string `json:"type"`
	Name          string `json:"name"`
	Description   string `json:"description"`
	Homepage      string `json:"homepage"`
	License       string `json:"license,omitempty"`
	URL           string `json:"url"`
	Version       string `json:"version"`
	Binary        string `json:"binary,omitempty"`
	InstallKind   string `json:"installKind,omitempty"`
	InstallSource string `json:"installSource,omitempty"`
}

type operationPayload struct {
	Repository     string            `json:"repository"`
	Branch         string            `json:"branch"`
	Path           string            `json:"path"`
	Mode           string            `json:"mode"`
	UpdateBranch   string            `json:"updateBranch,omitempty"`
	Content        string            `json:"content"`
	ContentSHA256  string            `json:"contentSha256"`
	Commit         commitConfig      `json:"commit"`
	Authenticated  bool              `json:"authenticated"`
	PullRequest    pullRequestConfig `json:"pullRequest,omitempty"`
	Artifact       string            `json:"artifact"`
	ArtifactDigest string            `json:"artifactDigest"`
	ArtifactSize   int64             `json:"artifactSize"`
}

type evidence struct {
	Repository        string `json:"repository"`
	Branch            string `json:"branch"`
	Path              string `json:"path"`
	Mode              string `json:"mode"`
	Commit            string `json:"commit,omitempty"`
	ContentSHA256     string `json:"contentSha256"`
	PublicationState  string `json:"publicationState"`
	PullRequestURL    string `json:"pullRequestUrl,omitempty"`
	PullRequestNumber int64  `json:"pullRequestNumber,omitempty"`
	PullRequestState  string `json:"pullRequestState,omitempty"`
}

func (p Provider) Describe(context.Context, protocol.DescribeRequest) (protocol.DescribeResponse, *protocol.ProviderError) {
	version := strings.TrimSpace(p.Version)
	if version == "" {
		version = DefaultVersion
	}
	return protocol.DescribeResponse{
		Provider:         protocol.ProviderIdentity{Name: Name, Version: version},
		ProtocolVersions: []string{protocol.Version},
		Capabilities:     []protocol.Capability{protocol.CapabilityPlan, protocol.CapabilityApply, protocol.CapabilityReconcile},
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
	content, err := renderManifest(cfg.Manifest, artifact)
	if err != nil {
		return protocol.PlanResponse{}, providerError(protocol.ErrorConfiguration, err.Error(), false)
	}
	contentSum := sha256.Sum256([]byte(content))
	if cfg.Branch == "" {
		cfg.Branch = defaultBranch
	}
	if cfg.Commit.Message == "" {
		cfg.Commit.Message = "Update Homebrew manifest for " + request.Release.ID
	}
	if cfg.Commit.AuthorName == "" {
		cfg.Commit.AuthorName = "Distroplane"
	}
	if cfg.Commit.AuthorEmail == "" {
		cfg.Commit.AuthorEmail = "distroplane@localhost"
	}
	if cfg.Mode == "pull-request" && cfg.UpdateBranch == "" {
		cfg.UpdateBranch = deterministicUpdateBranch(request.Release.ID, contentSum[:])
	}
	payload := operationPayload{
		Repository: cfg.Repository, Branch: cfg.Branch, Path: cfg.Path, Mode: cfg.Mode, UpdateBranch: cfg.UpdateBranch,
		Content: content, ContentSHA256: hex.EncodeToString(contentSum[:]), Commit: cfg.Commit,
		Authenticated: cfg.Authentication.Credential != "", PullRequest: cfg.PullRequest,
		Artifact: cfg.Artifact, ArtifactDigest: artifact.Digest, ArtifactSize: artifact.Size,
	}
	raw, _ := json.Marshal(payload)
	requirements := []protocol.Requirement{{Kind: "executable", Name: "git", Metadata: json.RawMessage(`{"minimumVersion":"2"}`)}}
	if cfg.Authentication.Credential != "" {
		metadata, _ := json.Marshal(map[string]string{"environment": TokenEnvironment})
		requirements = append(requirements, protocol.Requirement{Kind: "credential", Name: cfg.Authentication.Credential, Metadata: metadata})
	}
	return protocol.PlanResponse{
		Operations:   []protocol.PlannedOperation{{ID: "publish", Kind: "publish", SideEffecting: true, TimeoutMillis: defaultOperationTimeout, ProviderPayload: raw}},
		Requirements: requirements,
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
	token, providerErr := p.token(payload.Authenticated)
	if providerErr != nil {
		return protocol.ApplyResponse{}, providerErr
	}
	result, providerErr := p.apply(ctx, payload, token)
	if providerErr != nil {
		return protocol.ApplyResponse{}, providerErr
	}
	return protocol.ApplyResponse{Result: result}, nil
}

func (p Provider) Reconcile(ctx context.Context, request protocol.ReconcileRequest) (protocol.ReconcileResponse, *protocol.ProviderError) {
	if err := request.Validate(); err != nil {
		return protocol.ReconcileResponse{}, providerError(protocol.ErrorProtocol, err.Error(), false)
	}
	payload, err := parsePayload(request.ProviderPayload)
	if err != nil {
		return protocol.ReconcileResponse{}, providerError(protocol.ErrorConfiguration, err.Error(), false)
	}
	token, providerErr := p.token(payload.Authenticated)
	if providerErr != nil {
		return protocol.ReconcileResponse{}, providerErr
	}
	result, providerErr := p.reconcile(ctx, payload, token)
	if providerErr != nil {
		return protocol.ReconcileResponse{}, providerErr
	}
	return protocol.ReconcileResponse{Result: result}, nil
}

func parseConfiguration(raw json.RawMessage) (configuration, error) {
	var cfg configuration
	if err := decodeStrict(raw, &cfg); err != nil {
		return configuration{}, fmt.Errorf("invalid Homebrew configuration: %w", err)
	}
	if strings.TrimSpace(cfg.Artifact) == "" {
		return configuration{}, fmt.Errorf("artifact must not be empty")
	}
	if err := validateRepository(cfg.Repository); err != nil {
		return configuration{}, err
	}
	if cfg.Branch != "" && !validBranch(cfg.Branch) {
		return configuration{}, fmt.Errorf("branch is invalid")
	}
	repoPath, err := normalizeRepoPath(cfg.Path)
	if err != nil {
		return configuration{}, err
	}
	cfg.Path = repoPath
	if cfg.Mode != "direct" && cfg.Mode != "pull-request" {
		return configuration{}, fmt.Errorf("mode must be direct or pull-request")
	}
	if cfg.UpdateBranch != "" && !validBranch(cfg.UpdateBranch) {
		return configuration{}, fmt.Errorf("updateBranch is invalid")
	}
	if cfg.Mode == "direct" && cfg.UpdateBranch != "" {
		return configuration{}, fmt.Errorf("updateBranch is only valid in pull-request mode")
	}
	if cfg.Authentication.Credential != "" && !validCredentialRef(cfg.Authentication.Credential) {
		return configuration{}, fmt.Errorf("authentication credential reference is invalid")
	}
	if err := validateCommit(cfg.Commit); err != nil {
		return configuration{}, err
	}
	if cfg.Mode == "pull-request" {
		if cfg.Authentication.Credential == "" {
			return configuration{}, fmt.Errorf("pull-request mode requires an authentication credential")
		}
		if err := validatePullRequest(&cfg.PullRequest); err != nil {
			return configuration{}, err
		}
	} else if cfg.PullRequest != (pullRequestConfig{}) {
		return configuration{}, fmt.Errorf("pullRequest is only valid in pull-request mode")
	}
	if err := validateManifest(cfg.Manifest); err != nil {
		return configuration{}, err
	}
	return cfg, nil
}

func parsePayload(raw json.RawMessage) (operationPayload, error) {
	var payload operationPayload
	if err := decodeStrict(raw, &payload); err != nil {
		return operationPayload{}, fmt.Errorf("invalid Homebrew operation payload: %w", err)
	}
	if payload.Repository == "" || payload.Branch == "" || payload.Path == "" || payload.Content == "" || payload.ContentSHA256 == "" || payload.Artifact == "" || payload.ArtifactDigest == "" || payload.ArtifactSize < 0 {
		return operationPayload{}, fmt.Errorf("Homebrew operation payload is incomplete")
	}
	if payload.Mode != "direct" && payload.Mode != "pull-request" {
		return operationPayload{}, fmt.Errorf("Homebrew operation mode is invalid")
	}
	if payload.Mode == "pull-request" && payload.UpdateBranch == "" {
		return operationPayload{}, fmt.Errorf("Homebrew pull-request operation lacks update branch")
	}
	sum := sha256.Sum256([]byte(payload.Content))
	if hex.EncodeToString(sum[:]) != payload.ContentSHA256 {
		return operationPayload{}, fmt.Errorf("Homebrew operation content digest does not match content")
	}
	if _, err := normalizeRepoPath(payload.Path); err != nil {
		return operationPayload{}, err
	}
	if !validBranch(payload.Branch) || (payload.UpdateBranch != "" && !validBranch(payload.UpdateBranch)) {
		return operationPayload{}, fmt.Errorf("Homebrew operation branch is invalid")
	}
	return payload, nil
}

func renderManifest(cfg manifestConfig, artifact protocol.Artifact) (string, error) {
	digest, ok := strings.CutPrefix(artifact.Digest, "sha256:")
	if !ok {
		return "", fmt.Errorf("artifact digest must be SHA-256")
	}
	switch cfg.Type {
	case "formula":
		return renderFormula(cfg, digest), nil
	case "cask":
		return renderCask(cfg, digest), nil
	default:
		return "", fmt.Errorf("manifest type must be formula or cask")
	}
}

func renderFormula(cfg manifestConfig, digest string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "class %s < Formula\n", cfg.Name)
	fmt.Fprintf(&b, "  desc %s\n", rubyQuote(cfg.Description))
	fmt.Fprintf(&b, "  homepage %s\n", rubyQuote(cfg.Homepage))
	fmt.Fprintf(&b, "  url %s\n", rubyQuote(cfg.URL))
	fmt.Fprintf(&b, "  version %s\n", rubyQuote(cfg.Version))
	fmt.Fprintf(&b, "  sha256 %s\n", rubyQuote(digest))
	if cfg.License != "" {
		fmt.Fprintf(&b, "  license %s\n", rubyQuote(cfg.License))
	}
	b.WriteString("\n  def install\n")
	fmt.Fprintf(&b, "    bin.install %s\n", rubyQuote(cfg.Binary))
	b.WriteString("  end\nend\n")
	return b.String()
}

func renderCask(cfg manifestConfig, digest string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "cask %s do\n", rubyQuote(cfg.Name))
	fmt.Fprintf(&b, "  version %s\n", rubyQuote(cfg.Version))
	fmt.Fprintf(&b, "  sha256 %s\n", rubyQuote(digest))
	fmt.Fprintf(&b, "  url %s\n", rubyQuote(cfg.URL))
	fmt.Fprintf(&b, "  name %s\n", rubyQuote(cfg.Description))
	fmt.Fprintf(&b, "  desc %s\n", rubyQuote(cfg.Description))
	fmt.Fprintf(&b, "  homepage %s\n", rubyQuote(cfg.Homepage))
	fmt.Fprintf(&b, "  %s %s\n", cfg.InstallKind, rubyQuote(cfg.InstallSource))
	b.WriteString("end\n")
	return b.String()
}

func validateManifest(cfg manifestConfig) error {
	if cfg.Type != "formula" && cfg.Type != "cask" {
		return fmt.Errorf("manifest type must be formula or cask")
	}
	for name, value := range map[string]string{"description": cfg.Description, "homepage": cfg.Homepage, "url": cfg.URL, "version": cfg.Version} {
		if strings.TrimSpace(value) == "" || strings.ContainsAny(value, "\x00\r\n") {
			return fmt.Errorf("manifest %s is invalid", name)
		}
	}
	if _, err := url.ParseRequestURI(cfg.Homepage); err != nil {
		return fmt.Errorf("manifest homepage is invalid")
	}
	parsedURL, err := url.Parse(cfg.URL)
	if err != nil || (parsedURL.Scheme != "https" && parsedURL.Scheme != "http") || parsedURL.Host == "" || parsedURL.User != nil {
		return fmt.Errorf("manifest url must be an absolute HTTP(S) URL without user info")
	}
	if cfg.Type == "formula" {
		if !rubyClassPattern.MatchString(cfg.Name) || strings.TrimSpace(cfg.Binary) == "" || strings.ContainsAny(cfg.Binary, "\x00\r\n/") {
			return fmt.Errorf("formula name or binary is invalid")
		}
		if cfg.InstallKind != "" || cfg.InstallSource != "" {
			return fmt.Errorf("cask install fields are not valid for formula manifests")
		}
		return nil
	}
	if !caskTokenPattern.MatchString(cfg.Name) {
		return fmt.Errorf("cask token is invalid")
	}
	switch cfg.InstallKind {
	case "app", "pkg", "binary":
	default:
		return fmt.Errorf("cask installKind must be app, pkg, or binary")
	}
	if strings.TrimSpace(cfg.InstallSource) == "" || strings.ContainsAny(cfg.InstallSource, "\x00\r\n") {
		return fmt.Errorf("cask installSource is invalid")
	}
	if cfg.Binary != "" || cfg.License != "" {
		return fmt.Errorf("formula-only fields are not valid for cask manifests")
	}
	return nil
}

func validateRepository(value string) error {
	value = strings.TrimSpace(value)
	if value == "" || strings.HasPrefix(value, "-") || strings.ContainsAny(value, "\x00\r\n") {
		return fmt.Errorf("repository is invalid")
	}
	if strings.Contains(value, "://") {
		parsed, err := url.Parse(value)
		if err != nil || parsed.Scheme == "" {
			return fmt.Errorf("repository URL is invalid")
		}
		if parsed.User != nil {
			return fmt.Errorf("repository URL must not contain credentials")
		}
	}
	return nil
}

func validatePullRequest(cfg *pullRequestConfig) error {
	if strings.TrimSpace(cfg.Repository) == "" || !strings.Contains(cfg.Repository, "/") || strings.ContainsAny(cfg.Repository, "\x00\r\n ") {
		return fmt.Errorf("pullRequest repository must be owner/name")
	}
	if cfg.API == "" {
		cfg.API = "https://api.github.com"
	}
	parsed, err := url.Parse(cfg.API)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "https" && parsed.Scheme != "http") || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return fmt.Errorf("pullRequest api is invalid")
	}
	if parsed.Scheme == "http" && !cfg.AllowInsecureHTTP {
		return fmt.Errorf("pullRequest HTTP api requires allowInsecureHTTP")
	}
	cfg.API = strings.TrimRight(parsed.String(), "/")
	if strings.ContainsAny(cfg.Title, "\x00\r\n") || strings.ContainsAny(cfg.Body, "\x00") {
		return fmt.Errorf("pullRequest title or body is invalid")
	}
	return nil
}

func validateCommit(cfg commitConfig) error {
	for name, value := range map[string]string{"message": cfg.Message, "authorName": cfg.AuthorName, "authorEmail": cfg.AuthorEmail} {
		if strings.ContainsAny(value, "\x00\r\n") {
			return fmt.Errorf("commit %s is invalid", name)
		}
	}
	if cfg.AuthorEmail != "" && (!strings.Contains(cfg.AuthorEmail, "@") || strings.ContainsAny(cfg.AuthorEmail, " <>")) {
		return fmt.Errorf("commit authorEmail is invalid")
	}
	return nil
}

func normalizeRepoPath(value string) (string, error) {
	if value == "" || strings.Contains(value, "\\") || strings.ContainsAny(value, "\x00\r\n") || strings.HasPrefix(value, "/") {
		return "", fmt.Errorf("repository path is invalid")
	}
	cleaned := path.Clean(value)
	if cleaned == "." || cleaned == ".." || strings.HasPrefix(cleaned, "../") {
		return "", fmt.Errorf("repository path escapes the repository")
	}
	return cleaned, nil
}

func validBranch(value string) bool {
	if value == "" || strings.HasPrefix(value, "-") || strings.HasPrefix(value, ".") || strings.HasSuffix(value, ".") || strings.HasSuffix(value, "/") || strings.Contains(value, "..") || strings.Contains(value, "@{") || strings.HasSuffix(value, ".lock") {
		return false
	}
	return !strings.ContainsAny(value, " ~^:?*[\\\x00\r\n\t")
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

func deterministicUpdateBranch(releaseID string, digest []byte) string {
	slug := strings.Builder{}
	for _, r := range strings.ToLower(releaseID) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			slug.WriteRune(r)
		} else {
			slug.WriteByte('-')
		}
	}
	value := strings.Trim(slug.String(), "-_")
	if value == "" {
		value = "release"
	}
	if len(value) > 48 {
		value = value[:48]
	}
	return "distroplane/" + value + "-" + hex.EncodeToString(digest[:6])
}

func rubyQuote(value string) string {
	value = strings.ReplaceAll(value, "\\", "\\\\")
	value = strings.ReplaceAll(value, `"`, `\"`)
	value = strings.ReplaceAll(value, `#{`, `\#{`)
	return `"` + value + `"`
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

func (p Provider) token(required bool) (string, *protocol.ProviderError) {
	if !required {
		return "", nil
	}
	getenv := p.Getenv
	if getenv == nil {
		getenv = os.Getenv
	}
	token := strings.TrimSpace(getenv(TokenEnvironment))
	if token == "" {
		return "", providerError(protocol.ErrorAuthentication, "required Homebrew repository credential is unavailable", false)
	}
	return token, nil
}

func (p Provider) client() *http.Client {
	if p.Client != nil {
		return p.Client
	}
	return http.DefaultClient
}

func providerError(code protocol.ErrorCode, message string, retryable bool) *protocol.ProviderError {
	value := protocol.NewProviderError(code, message, retryable)
	return &value
}
