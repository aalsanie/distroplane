package winget

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
	"sort"
	"strconv"
	"strings"

	"github.com/aalsanie/distroplane/internal/protocol"
)

const (
	Name                    = "winget"
	DefaultVersion          = "0.0.0-dev"
	TokenEnvironment        = "DISTROPLANE_WINGET_TOKEN"
	defaultBranch           = "master"
	defaultManifestRoot     = "manifests"
	defaultManifestVersion  = "1.12.0"
	defaultOperationTimeout = 120000
)

var (
	identifierPattern      = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9+_-]*(\.[A-Za-z0-9][A-Za-z0-9+_-]*)+$`)
	localePattern          = regexp.MustCompile(`^[A-Za-z]{2,3}(?:-[A-Za-z0-9]{2,8})*$`)
	manifestVersionPattern = regexp.MustCompile(`^1\.[0-9]+\.[0-9]+$`)
)

type Provider struct {
	Version string
	Client  *http.Client
	Getenv  func(string) string
	Git     GitRunner
}

type configuration struct {
	Artifact        string               `json:"artifact"`
	Repository      string               `json:"repository"`
	Branch          string               `json:"branch,omitempty"`
	UpdateBranch    string               `json:"updateBranch,omitempty"`
	ManifestRoot    string               `json:"manifestRoot,omitempty"`
	ManifestVersion string               `json:"manifestVersion,omitempty"`
	Package         packageConfig        `json:"package"`
	Installer       installerConfig      `json:"installer"`
	Commit          commitConfig         `json:"commit,omitempty"`
	Authentication  authenticationConfig `json:"authentication"`
	PullRequest     pullRequestConfig    `json:"pullRequest"`
}

type authenticationConfig struct {
	Credential string `json:"credential"`
}

type packageConfig struct {
	Identifier       string `json:"identifier"`
	Version          string `json:"version"`
	DefaultLocale    string `json:"defaultLocale,omitempty"`
	Publisher        string `json:"publisher"`
	Name             string `json:"name"`
	License          string `json:"license"`
	ShortDescription string `json:"shortDescription"`
	PackageURL       string `json:"packageUrl,omitempty"`
	PublisherURL     string `json:"publisherUrl,omitempty"`
	ReleaseNotesURL  string `json:"releaseNotesUrl,omitempty"`
}

type installerConfig struct {
	Architecture string `json:"architecture"`
	Type         string `json:"type"`
	URL          string `json:"url"`
}

type commitConfig struct {
	Message     string `json:"message,omitempty"`
	AuthorName  string `json:"authorName,omitempty"`
	AuthorEmail string `json:"authorEmail,omitempty"`
}

type pullRequestConfig struct {
	API               string `json:"api,omitempty"`
	Repository        string `json:"repository"`
	HeadOwner         string `json:"headOwner,omitempty"`
	Title             string `json:"title,omitempty"`
	Body              string `json:"body,omitempty"`
	AllowInsecureHTTP bool   `json:"allowInsecureHTTP,omitempty"`
}

type manifestFile struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

type operationPayload struct {
	Repository      string            `json:"repository"`
	Branch          string            `json:"branch"`
	UpdateBranch    string            `json:"updateBranch"`
	Files           []manifestFile    `json:"files"`
	TreeSHA256      string            `json:"treeSha256"`
	Commit          commitConfig      `json:"commit"`
	PullRequest     pullRequestConfig `json:"pullRequest"`
	Artifact        string            `json:"artifact"`
	ArtifactDigest  string            `json:"artifactDigest"`
	ArtifactSize    int64             `json:"artifactSize"`
	PackageID       string            `json:"packageId"`
	PackageVersion  string            `json:"packageVersion"`
	ManifestVersion string            `json:"manifestVersion"`
}

type evidence struct {
	Repository        string `json:"repository"`
	BaseBranch        string `json:"baseBranch"`
	UpdateBranch      string `json:"updateBranch"`
	ManifestTreeSHA   string `json:"manifestTreeSha256"`
	Commit            string `json:"commit,omitempty"`
	DestinationCommit string `json:"destinationCommit,omitempty"`
	PackageID         string `json:"packageId"`
	PackageVersion    string `json:"packageVersion"`
	PublicationState  string `json:"publicationState"`
	PullRequestURL    string `json:"pullRequestUrl,omitempty"`
	PullRequestNumber int64  `json:"pullRequestNumber,omitempty"`
	PullRequestState  string `json:"pullRequestState,omitempty"`
	ValidationState   string `json:"validationState,omitempty"`
	ValidationCheck   string `json:"validationCheck,omitempty"`
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
	files, err := renderManifestSet(cfg, artifact)
	if err != nil {
		return protocol.PlanResponse{}, providerError(protocol.ErrorConfiguration, err.Error(), false)
	}
	tree := manifestTreeHash(files)
	if cfg.UpdateBranch == "" {
		cfg.UpdateBranch = deterministicUpdateBranch(request.Release.ID, tree)
	}
	if cfg.Commit.Message == "" {
		cfg.Commit.Message = "Add " + cfg.Package.Identifier + " " + cfg.Package.Version
	}
	if cfg.Commit.AuthorName == "" {
		cfg.Commit.AuthorName = "Distroplane"
	}
	if cfg.Commit.AuthorEmail == "" {
		cfg.Commit.AuthorEmail = "distroplane@localhost"
	}
	if cfg.PullRequest.Title == "" {
		cfg.PullRequest.Title = "New version: " + cfg.Package.Identifier + " version " + cfg.Package.Version
	}
	payload := operationPayload{
		Repository: cfg.Repository, Branch: cfg.Branch, UpdateBranch: cfg.UpdateBranch,
		Files: files, TreeSHA256: tree, Commit: cfg.Commit, PullRequest: cfg.PullRequest,
		Artifact: cfg.Artifact, ArtifactDigest: artifact.Digest, ArtifactSize: artifact.Size,
		PackageID: cfg.Package.Identifier, PackageVersion: cfg.Package.Version, ManifestVersion: cfg.ManifestVersion,
	}
	raw, _ := json.Marshal(payload)
	credentialMetadata, _ := json.Marshal(map[string]string{"environment": TokenEnvironment})
	return protocol.PlanResponse{
		Operations: []protocol.PlannedOperation{{
			ID: "submit", Kind: "submit", SideEffecting: true, TimeoutMillis: defaultOperationTimeout, ProviderPayload: raw,
		}},
		Requirements: []protocol.Requirement{
			{Kind: "executable", Name: "git", Metadata: json.RawMessage(`{"minimumVersion":"2"}`)},
			{Kind: "credential", Name: cfg.Authentication.Credential, Metadata: credentialMetadata},
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
	token, providerErr := p.token()
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
	token, providerErr := p.token()
	if providerErr != nil {
		return protocol.ReconcileResponse{}, providerErr
	}
	result, providerErr := p.reconcile(ctx, payload, request.Previous, token)
	if providerErr != nil {
		return protocol.ReconcileResponse{}, providerErr
	}
	return protocol.ReconcileResponse{Result: result}, nil
}

func parseConfiguration(raw json.RawMessage) (configuration, error) {
	var cfg configuration
	if err := decodeStrict(raw, &cfg); err != nil {
		return configuration{}, fmt.Errorf("invalid WinGet configuration: %w", err)
	}
	if strings.TrimSpace(cfg.Artifact) == "" {
		return configuration{}, fmt.Errorf("artifact must not be empty")
	}
	if err := validateRepository(cfg.Repository); err != nil {
		return configuration{}, err
	}
	if cfg.Branch == "" {
		cfg.Branch = defaultBranch
	}
	if !validBranch(cfg.Branch) {
		return configuration{}, fmt.Errorf("branch is invalid")
	}
	if cfg.UpdateBranch != "" && !validBranch(cfg.UpdateBranch) {
		return configuration{}, fmt.Errorf("updateBranch is invalid")
	}
	if cfg.ManifestRoot == "" {
		cfg.ManifestRoot = defaultManifestRoot
	}
	root, err := normalizeRepoPath(cfg.ManifestRoot)
	if err != nil {
		return configuration{}, fmt.Errorf("manifestRoot is invalid: %w", err)
	}
	cfg.ManifestRoot = root
	if cfg.ManifestVersion == "" {
		cfg.ManifestVersion = defaultManifestVersion
	}
	if !manifestVersionPattern.MatchString(cfg.ManifestVersion) {
		return configuration{}, fmt.Errorf("manifestVersion is invalid")
	}
	if cfg.Package.DefaultLocale == "" {
		cfg.Package.DefaultLocale = "en-US"
	}
	if err := validatePackage(cfg.Package); err != nil {
		return configuration{}, err
	}
	if err := validateInstaller(cfg.Installer); err != nil {
		return configuration{}, err
	}
	if !validCredentialRef(cfg.Authentication.Credential) {
		return configuration{}, fmt.Errorf("authentication credential reference is invalid")
	}
	if err := validateCommit(cfg.Commit); err != nil {
		return configuration{}, err
	}
	if err := validatePullRequest(&cfg.PullRequest); err != nil {
		return configuration{}, err
	}
	return cfg, nil
}

func parsePayload(raw json.RawMessage) (operationPayload, error) {
	var payload operationPayload
	if err := decodeStrict(raw, &payload); err != nil {
		return operationPayload{}, fmt.Errorf("invalid WinGet operation payload: %w", err)
	}
	if payload.Repository == "" || payload.Branch == "" || payload.UpdateBranch == "" || len(payload.Files) != 3 ||
		payload.TreeSHA256 == "" || payload.Artifact == "" || payload.ArtifactDigest == "" || payload.ArtifactSize < 0 ||
		payload.PackageID == "" || payload.PackageVersion == "" || payload.ManifestVersion == "" {
		return operationPayload{}, fmt.Errorf("WinGet operation payload is incomplete")
	}
	if !validBranch(payload.Branch) || !validBranch(payload.UpdateBranch) {
		return operationPayload{}, fmt.Errorf("WinGet operation branch is invalid")
	}
	if !manifestVersionPattern.MatchString(payload.ManifestVersion) {
		return operationPayload{}, fmt.Errorf("WinGet operation manifest version is invalid")
	}
	if err := validatePullRequest(&payload.PullRequest); err != nil {
		return operationPayload{}, err
	}
	seen := make(map[string]struct{}, len(payload.Files))
	for _, file := range payload.Files {
		normalized, err := normalizeRepoPath(file.Path)
		if err != nil || normalized != file.Path || file.Content == "" {
			return operationPayload{}, fmt.Errorf("WinGet manifest file is invalid")
		}
		if _, exists := seen[file.Path]; exists {
			return operationPayload{}, fmt.Errorf("duplicate WinGet manifest path")
		}
		seen[file.Path] = struct{}{}
	}
	if manifestTreeHash(payload.Files) != payload.TreeSHA256 {
		return operationPayload{}, fmt.Errorf("WinGet manifest tree digest does not match files")
	}
	digest, ok := strings.CutPrefix(payload.ArtifactDigest, "sha256:")
	if !ok || len(digest) != 64 {
		return operationPayload{}, fmt.Errorf("WinGet artifact digest is invalid")
	}
	if _, err := hex.DecodeString(digest); err != nil {
		return operationPayload{}, fmt.Errorf("WinGet artifact digest is invalid")
	}
	return payload, nil
}

func validatePackage(cfg packageConfig) error {
	if !identifierPattern.MatchString(cfg.Identifier) {
		return fmt.Errorf("package identifier is invalid")
	}
	if !validSimple(cfg.Version, 128) {
		return fmt.Errorf("package version is invalid")
	}
	if cfg.DefaultLocale == "" {
		cfg.DefaultLocale = "en-US"
	}
	if !localePattern.MatchString(cfg.DefaultLocale) {
		return fmt.Errorf("default locale is invalid")
	}
	for name, value := range map[string]string{
		"publisher": cfg.Publisher, "name": cfg.Name, "license": cfg.License, "shortDescription": cfg.ShortDescription,
	} {
		if !validMetadata(value, 512) {
			return fmt.Errorf("package %s is invalid", name)
		}
	}
	for name, value := range map[string]string{
		"packageUrl": cfg.PackageURL, "publisherUrl": cfg.PublisherURL, "releaseNotesUrl": cfg.ReleaseNotesURL,
	} {
		if value != "" {
			if _, err := normalizeHTTPURL(value, false); err != nil {
				return fmt.Errorf("package %s is invalid", name)
			}
		}
	}
	return nil
}

func validateInstaller(cfg installerConfig) error {
	switch cfg.Architecture {
	case "x86", "x64", "arm", "arm64", "neutral":
	default:
		return fmt.Errorf("installer architecture is invalid")
	}
	switch cfg.Type {
	case "exe", "msi", "msix", "inno", "nullsoft", "wix", "burn", "portable", "zip":
	default:
		return fmt.Errorf("installer type is invalid")
	}
	if _, err := normalizeHTTPURL(cfg.URL, false); err != nil {
		return fmt.Errorf("installer URL is invalid")
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
		if err != nil || parsed.Scheme == "" || parsed.User != nil {
			return fmt.Errorf("repository URL is invalid")
		}
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

func validatePullRequest(cfg *pullRequestConfig) error {
	if strings.TrimSpace(cfg.Repository) == "" || !strings.Contains(cfg.Repository, "/") || strings.ContainsAny(cfg.Repository, "\x00\r\n ") {
		return fmt.Errorf("pullRequest repository must be owner/name")
	}
	owner, _, _ := strings.Cut(cfg.Repository, "/")
	if cfg.HeadOwner == "" {
		cfg.HeadOwner = owner
	}
	if !validSimple(cfg.HeadOwner, 128) {
		return fmt.Errorf("pullRequest headOwner is invalid")
	}
	if cfg.API == "" {
		cfg.API = "https://api.github.com"
	}
	normalized, err := normalizeHTTPURL(cfg.API, cfg.AllowInsecureHTTP)
	if err != nil {
		return fmt.Errorf("pullRequest api is invalid")
	}
	parsed, _ := url.Parse(normalized)
	if parsed.RawQuery != "" || parsed.Fragment != "" {
		return fmt.Errorf("pullRequest api is invalid")
	}
	cfg.API = strings.TrimRight(normalized, "/")
	if strings.ContainsAny(cfg.Title, "\x00\r\n") || strings.Contains(cfg.Body, "\x00") {
		return fmt.Errorf("pullRequest title or body is invalid")
	}
	return nil
}

func normalizeHTTPURL(value string, allowHTTP bool) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(value))
	if err != nil || parsed.Host == "" || (parsed.Scheme != "https" && parsed.Scheme != "http") || parsed.User != nil {
		return "", fmt.Errorf("must be an absolute HTTP(S) URL without user info")
	}
	if parsed.Scheme == "http" && !allowHTTP {
		return "", fmt.Errorf("HTTP is not allowed")
	}
	return parsed.String(), nil
}

func renderManifestSet(cfg configuration, artifact protocol.Artifact) ([]manifestFile, error) {
	digest, ok := strings.CutPrefix(artifact.Digest, "sha256:")
	if !ok || len(digest) != 64 {
		return nil, fmt.Errorf("artifact digest must be SHA-256")
	}
	locale := cfg.Package.DefaultLocale
	if locale == "" {
		locale = "en-US"
	}
	base, err := manifestDirectory(cfg.ManifestRoot, cfg.Package.Identifier, cfg.Package.Version)
	if err != nil {
		return nil, err
	}
	id := cfg.Package.Identifier
	files := []manifestFile{
		{Path: path.Join(base, id+".yaml"), Content: renderVersionManifest(cfg, locale)},
		{Path: path.Join(base, id+".installer.yaml"), Content: renderInstallerManifest(cfg, strings.ToUpper(digest))},
		{Path: path.Join(base, id+".locale."+locale+".yaml"), Content: renderLocaleManifest(cfg, locale)},
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	return files, nil
}

func manifestDirectory(root, identifier, version string) (string, error) {
	parts := strings.Split(identifier, ".")
	if len(parts) < 2 {
		return "", fmt.Errorf("package identifier is invalid")
	}
	items := []string{root, strings.ToLower(string(identifier[0]))}
	items = append(items, parts...)
	items = append(items, version)
	value := path.Join(items...)
	return normalizeRepoPath(value)
}

func renderVersionManifest(cfg configuration, locale string) string {
	v := cfg.ManifestVersion
	return schemaHeader("version", v) +
		"PackageIdentifier: " + yamlQuote(cfg.Package.Identifier) + "\n" +
		"PackageVersion: " + yamlQuote(cfg.Package.Version) + "\n" +
		"DefaultLocale: " + yamlQuote(locale) + "\n" +
		"ManifestType: version\n" +
		"ManifestVersion: " + v + "\n"
}

func renderInstallerManifest(cfg configuration, digest string) string {
	v := cfg.ManifestVersion
	return schemaHeader("installer", v) +
		"PackageIdentifier: " + yamlQuote(cfg.Package.Identifier) + "\n" +
		"PackageVersion: " + yamlQuote(cfg.Package.Version) + "\n" +
		"InstallerType: " + cfg.Installer.Type + "\n" +
		"Installers:\n" +
		"  - Architecture: " + cfg.Installer.Architecture + "\n" +
		"    InstallerUrl: " + yamlQuote(cfg.Installer.URL) + "\n" +
		"    InstallerSha256: " + digest + "\n" +
		"ManifestType: installer\n" +
		"ManifestVersion: " + v + "\n"
}

func renderLocaleManifest(cfg configuration, locale string) string {
	v := cfg.ManifestVersion
	var b strings.Builder
	b.WriteString(schemaHeader("defaultLocale", v))
	fmt.Fprintf(&b, "PackageIdentifier: %s\n", yamlQuote(cfg.Package.Identifier))
	fmt.Fprintf(&b, "PackageVersion: %s\n", yamlQuote(cfg.Package.Version))
	fmt.Fprintf(&b, "PackageLocale: %s\n", yamlQuote(locale))
	fmt.Fprintf(&b, "Publisher: %s\n", yamlQuote(cfg.Package.Publisher))
	if cfg.Package.PublisherURL != "" {
		fmt.Fprintf(&b, "PublisherUrl: %s\n", yamlQuote(cfg.Package.PublisherURL))
	}
	fmt.Fprintf(&b, "PackageName: %s\n", yamlQuote(cfg.Package.Name))
	if cfg.Package.PackageURL != "" {
		fmt.Fprintf(&b, "PackageUrl: %s\n", yamlQuote(cfg.Package.PackageURL))
	}
	fmt.Fprintf(&b, "License: %s\n", yamlQuote(cfg.Package.License))
	fmt.Fprintf(&b, "ShortDescription: %s\n", yamlQuote(cfg.Package.ShortDescription))
	if cfg.Package.ReleaseNotesURL != "" {
		fmt.Fprintf(&b, "ReleaseNotesUrl: %s\n", yamlQuote(cfg.Package.ReleaseNotesURL))
	}
	fmt.Fprintf(&b, "ManifestType: defaultLocale\nManifestVersion: %s\n", v)
	return b.String()
}

func schemaHeader(kind, version string) string {
	return "# yaml-language-server: $schema=https://aka.ms/winget-manifest." + kind + "." + version + ".schema.json\n"
}

func yamlQuote(value string) string {
	return strconv.Quote(value)
}

func manifestTreeHash(files []manifestFile) string {
	copyFiles := append([]manifestFile(nil), files...)
	sort.Slice(copyFiles, func(i, j int) bool { return copyFiles[i].Path < copyFiles[j].Path })
	hash := sha256.New()
	for _, file := range copyFiles {
		_, _ = hash.Write([]byte(file.Path))
		_, _ = hash.Write([]byte{0})
		_, _ = hash.Write([]byte(file.Content))
		_, _ = hash.Write([]byte{0})
	}
	return hex.EncodeToString(hash.Sum(nil))
}

func deterministicUpdateBranch(releaseID, tree string) string {
	var slug strings.Builder
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
	suffix := tree
	if len(suffix) > 12 {
		suffix = suffix[:12]
	}
	return "distroplane/" + value + "-" + suffix
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
	if value == "" || strings.HasPrefix(value, "-") || strings.HasPrefix(value, ".") || strings.HasSuffix(value, ".") ||
		strings.HasSuffix(value, "/") || strings.Contains(value, "..") || strings.Contains(value, "@{") || strings.HasSuffix(value, ".lock") {
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

func validMetadata(value string, max int) bool {
	return value != "" && len(value) <= max && strings.TrimSpace(value) == value && !strings.ContainsAny(value, "\x00\r\n")
}

func validSimple(value string, max int) bool {
	return validMetadata(value, max) && !strings.ContainsAny(value, "/\\")
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

func (p Provider) token() (string, *protocol.ProviderError) {
	getenv := p.Getenv
	if getenv == nil {
		getenv = os.Getenv
	}
	token := strings.TrimSpace(getenv(TokenEnvironment))
	if token == "" {
		return "", providerError(protocol.ErrorAuthentication, "required WinGet submission credential is unavailable", false)
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
