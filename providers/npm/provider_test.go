package npm

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/aalsanie/distroplane/internal/protocol"
)

const (
	testToken = "npm-secret-token-canary"
	testOIDC  = "oidc-secret-token-canary"
	exchanged = "short-lived-exchange-token"
)

type registryFixture struct {
	mu          sync.Mutex
	t           *testing.T
	token       string
	oidc        string
	mode        string
	versions    map[string]manifestDist
	puts        int
	exchanges   int
	lastPublish publishDocument
}

func newRegistry(t *testing.T, mode string) (*registryFixture, *httptest.Server) {
	t.Helper()
	fixture := &registryFixture{t: t, token: testToken, oidc: testOIDC, mode: mode, versions: map[string]manifestDist{}}
	server := httptest.NewServer(http.HandlerFunc(fixture.serveHTTP))
	return fixture, server
}

func (f *registryFixture) serveHTTP(w http.ResponseWriter, r *http.Request) {
	if strings.Contains(r.URL.EscapedPath(), "/-/npm/v1/oidc/token/exchange/package/") {
		f.mu.Lock()
		f.exchanges++
		f.mu.Unlock()
		if r.Method != http.MethodPost || r.Header.Get("Authorization") != "Bearer "+f.oidc {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = io.WriteString(w, `{"token_type":"oidc","token":"`+exchanged+`"}`)
		return
	}

	expected := f.token
	if f.mode == "trusted" {
		expected = exchanged
	}
	if r.Header.Get("Authorization") != "Bearer "+expected {
		_, _ = io.WriteString(w, `{"error":"bad auth `+testToken+`"}`)
		w.WriteHeader(http.StatusUnauthorized)
		return
	}

	switch r.Method {
	case http.MethodGet:
		f.handleGet(w)
	case http.MethodPut:
		f.handlePut(w, r)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (f *registryFixture) handleGet(w http.ResponseWriter) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.mode == "malformed" {
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "{")
		return
	}
	if len(f.versions) == 0 {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	document := packument{Versions: make(map[string]struct {
		Dist manifestDist `json:"dist"`
	}, len(f.versions))}
	for version, dist := range f.versions {
		document.Versions[version] = struct {
			Dist manifestDist `json:"dist"`
		}{Dist: dist}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(document)
}

func (f *registryFixture) handlePut(w http.ResponseWriter, r *http.Request) {
	var document publishDocument
	if err := json.NewDecoder(r.Body).Decode(&document); err != nil {
		f.t.Errorf("decode publish body: %v", err)
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	f.mu.Lock()
	f.puts++
	f.lastPublish = document
	f.mu.Unlock()
	if f.mode == "reject-auth" {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(w, testToken)
		return
	}
	if f.mode == "conflict" {
		w.WriteHeader(http.StatusConflict)
		return
	}
	for version, raw := range document.Versions {
		var manifest map[string]json.RawMessage
		if err := json.Unmarshal(raw, &manifest); err != nil {
			f.t.Errorf("decode manifest: %v", err)
			continue
		}
		var dist manifestDist
		if err := json.Unmarshal(manifest["dist"], &dist); err != nil {
			f.t.Errorf("decode dist: %v", err)
			continue
		}
		f.mu.Lock()
		f.versions[version] = dist
		f.mu.Unlock()
	}
	if f.mode == "ambiguous" {
		hijacker, ok := w.(http.Hijacker)
		if !ok {
			f.t.Error("response writer does not support hijacking")
			return
		}
		connection, _, err := hijacker.Hijack()
		if err != nil {
			f.t.Errorf("hijack: %v", err)
			return
		}
		_ = connection.Close()
		return
	}
	w.WriteHeader(http.StatusCreated)
	_, _ = io.WriteString(w, `{"ok":true}`)
}

func TestDescribeAndPlan(t *testing.T) {
	archive, artifact := packageFixture(t, "@scope/widget", "1.2.3")
	_, server := newRegistry(t, "normal")
	defer server.Close()
	provider := Provider{Version: "1.2.0"}
	description, providerErr := provider.Describe(context.Background(), protocol.DescribeRequest{})
	if providerErr != nil || description.Provider.Name != Name || description.Provider.Version != "1.2.0" || len(description.Capabilities) != 3 {
		t.Fatalf("description=%+v err=%v", description, providerErr)
	}
	response, providerErr := provider.Plan(context.Background(), planRequest(t, archive, artifact, server.URL, "token"))
	if providerErr != nil {
		t.Fatal(providerErr)
	}
	if len(response.Operations) != 1 || response.Operations[0].ID != "publish" || !response.Operations[0].SideEffecting || response.Operations[0].TimeoutMillis != defaultOperationTimeout {
		t.Fatalf("operations=%+v", response.Operations)
	}
	if len(response.Requirements) != 1 || response.Requirements[0].Name != "publish" || !strings.Contains(string(response.Requirements[0].Metadata), TokenEnvironment) {
		t.Fatalf("requirements=%+v", response.Requirements)
	}
	var payload operationPayload
	if err := json.Unmarshal(response.Operations[0].ProviderPayload, &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Package != "@scope/widget" || payload.Version != "1.2.3" || payload.Tag != defaultTag || payload.PackagePath != archive {
		t.Fatalf("payload=%+v", payload)
	}
}

func TestPlanRejectsInvalidConfigurationAndPackage(t *testing.T) {
	archive, artifact := packageFixture(t, "widget", "1.0.0")
	_, server := newRegistry(t, "normal")
	defer server.Close()
	valid := configurationJSON(archive, server.URL, "token")
	var relative configuration
	if err := json.Unmarshal(valid, &relative); err != nil {
		t.Fatal(err)
	}
	relative.PackagePath = "relative.tgz"
	relativeRaw, _ := json.Marshal(relative)
	cases := map[string]json.RawMessage{
		"unknown field":       json.RawMessage(strings.Replace(string(valid), `"artifact":`, `"unknown":true,"artifact":`, 1)),
		"relative path":       relativeRaw,
		"missing artifact":    json.RawMessage(strings.Replace(string(valid), `"artifact":"package"`, `"artifact":"other"`, 1)),
		"insecure disallowed": json.RawMessage(strings.Replace(string(valid), `,"allowInsecureHTTP":true`, "", 1)),
		"bad tag":             json.RawMessage(strings.Replace(string(valid), `"tag":"latest"`, `"tag":"1.0.0"`, 1)),
		"bad access":          json.RawMessage(strings.Replace(string(valid), `"access":"public"`, `"access":"other"`, 1)),
		"bad auth":            json.RawMessage(strings.Replace(string(valid), `"mode":"token"`, `"mode":"other"`, 1)),
		"bad credential":      json.RawMessage(strings.Replace(string(valid), `"credential":"publish"`, `"credential":" bad "`, 1)),
	}
	provider := Provider{}
	for name, raw := range cases {
		t.Run(name, func(t *testing.T) {
			request := protocol.PlanRequest{Release: protocol.Release{ID: "release", Artifacts: []protocol.Artifact{artifact}}, Target: protocol.Target{ID: "target", Configuration: raw}}
			if _, providerErr := provider.Plan(context.Background(), request); providerErr == nil || providerErr.Code != protocol.ErrorConfiguration {
				t.Fatalf("err=%v", providerErr)
			}
		})
	}

	badDigest := artifact
	badDigest.Digest = "sha256:" + strings.Repeat("b", 64)
	request := planRequest(t, archive, badDigest, server.URL, "token")
	if _, providerErr := provider.Plan(context.Background(), request); providerErr == nil || !strings.Contains(providerErr.Message, "digest changed") {
		t.Fatalf("err=%v", providerErr)
	}
	invalidRequest := request
	invalidRequest.Release.ID = ""
	if _, providerErr := provider.Plan(context.Background(), invalidRequest); providerErr == nil || providerErr.Code != protocol.ErrorProtocol {
		t.Fatalf("err=%v", providerErr)
	}
}

func TestApplyPublishAlreadyPublishedAndReconcile(t *testing.T) {
	archive, artifact := packageFixture(t, "widget", "2.0.0")
	registry, server := newRegistry(t, "normal")
	defer server.Close()
	provider := Provider{Getenv: env(map[string]string{TokenEnvironment: testToken})}
	payload := plannedPayload(t, provider, archive, artifact, server.URL, "token")
	request := applyRequest(payload)
	response, providerErr := provider.Apply(context.Background(), request)
	if providerErr != nil {
		t.Fatal(providerErr)
	}
	assertPublished(t, response.Result, "widget", "2.0.0")
	registry.mu.Lock()
	puts := registry.puts
	publish := registry.lastPublish
	registry.mu.Unlock()
	if puts != 1 || publish.Name != "widget" || publish.DistTags[defaultTag] != "2.0.0" || len(publish.Attachments) != 1 {
		t.Fatalf("puts=%d publish=%+v", puts, publish)
	}

	response, providerErr = provider.Apply(context.Background(), request)
	if providerErr != nil || response.Result.State != protocol.ResultPublished {
		t.Fatalf("response=%+v err=%v", response, providerErr)
	}
	registry.mu.Lock()
	puts = registry.puts
	registry.mu.Unlock()
	if puts != 1 {
		t.Fatalf("idempotent apply published again: puts=%d", puts)
	}

	reconciled, providerErr := provider.Reconcile(context.Background(), reconcileRequest(payload))
	if providerErr != nil || reconciled.Result.State != protocol.ResultPublished {
		t.Fatalf("reconcile=%+v err=%v", reconciled, providerErr)
	}
}

func TestApplyAuthenticationConflictAndSecretRedaction(t *testing.T) {
	archive, artifact := packageFixture(t, "widget", "3.0.0")
	_, server := newRegistry(t, "normal")
	defer server.Close()
	provider := Provider{Getenv: env(nil)}
	payload := plannedPayload(t, Provider{}, archive, artifact, server.URL, "token")
	if _, providerErr := provider.Apply(context.Background(), applyRequest(payload)); providerErr == nil || providerErr.Code != protocol.ErrorAuthentication || strings.Contains(providerErr.Message, testToken) {
		t.Fatalf("err=%v", providerErr)
	}

	rejectRegistry, rejectServer := newRegistry(t, "reject-auth")
	defer rejectServer.Close()
	provider = Provider{Getenv: env(map[string]string{TokenEnvironment: testToken})}
	payload = plannedPayload(t, Provider{}, archive, artifact, rejectServer.URL, "token")
	if _, providerErr := provider.Apply(context.Background(), applyRequest(payload)); providerErr == nil || providerErr.Code != protocol.ErrorAuthentication || strings.Contains(providerErr.Message, testToken) {
		t.Fatalf("err=%v", providerErr)
	}
	_ = rejectRegistry

	conflictRegistry, conflictServer := newRegistry(t, "normal")
	defer conflictServer.Close()
	payload = plannedPayload(t, Provider{}, archive, artifact, conflictServer.URL, "token")
	conflictRegistry.versions["3.0.0"] = manifestDist{Integrity: "sha512-other", Shasum: "other"}
	response, providerErr := provider.Apply(context.Background(), applyRequest(payload))
	if providerErr != nil || response.Result.State != protocol.ResultRejected || response.Result.ProviderState != "conflict" {
		t.Fatalf("response=%+v err=%v", response, providerErr)
	}
	conflictRegistry.mu.Lock()
	puts := conflictRegistry.puts
	conflictRegistry.mu.Unlock()
	if puts != 0 {
		t.Fatalf("conflicting version was republished: puts=%d", puts)
	}
}

func TestAmbiguousPublishReconcilesAfterConnectionLoss(t *testing.T) {
	archive, artifact := packageFixture(t, "widget", "4.0.0")
	_, server := newRegistry(t, "ambiguous")
	defer server.Close()
	provider := Provider{Getenv: env(map[string]string{TokenEnvironment: testToken})}
	payload := plannedPayload(t, Provider{}, archive, artifact, server.URL, "token")
	if _, providerErr := provider.Apply(context.Background(), applyRequest(payload)); providerErr == nil || providerErr.Code != protocol.ErrorAmbiguousOutcome || providerErr.Retryable {
		t.Fatalf("err=%v", providerErr)
	}
	response, providerErr := provider.Reconcile(context.Background(), reconcileRequest(payload))
	if providerErr != nil || response.Result.State != protocol.ResultPublished {
		t.Fatalf("response=%+v err=%v", response, providerErr)
	}
}

func TestRegistryFailureModesAndAbsentReconcile(t *testing.T) {
	archive, artifact := packageFixture(t, "widget", "5.0.0")
	_, malformedServer := newRegistry(t, "malformed")
	defer malformedServer.Close()
	provider := Provider{Getenv: env(map[string]string{TokenEnvironment: testToken})}
	payload := plannedPayload(t, Provider{}, archive, artifact, malformedServer.URL, "token")
	if _, providerErr := provider.Reconcile(context.Background(), reconcileRequest(payload)); providerErr == nil || providerErr.Code != protocol.ErrorPermanentExternal {
		t.Fatalf("err=%v", providerErr)
	}

	_, absentServer := newRegistry(t, "normal")
	payload = plannedPayload(t, Provider{}, archive, artifact, absentServer.URL, "token")
	response, providerErr := provider.Reconcile(context.Background(), reconcileRequest(payload))
	if providerErr != nil || response.Result.State != protocol.ResultWaitingExternal || response.Result.ProviderState != "absent" {
		t.Fatalf("response=%+v err=%v", response, providerErr)
	}
	absentServer.Close()
	if _, providerErr := provider.Reconcile(context.Background(), reconcileRequest(payload)); providerErr == nil || providerErr.Code != protocol.ErrorTransientExternal || !providerErr.Retryable {
		t.Fatalf("err=%v", providerErr)
	}
}

func TestTrustedPublishingExchangesOIDCToken(t *testing.T) {
	archive, artifact := packageFixture(t, "@scope/widget", "6.0.0")
	registry, server := newRegistry(t, "trusted")
	defer server.Close()
	provider := Provider{Getenv: env(map[string]string{OIDCTokenEnvironment: testOIDC})}
	payload := plannedPayload(t, Provider{}, archive, artifact, server.URL, "trusted-publishing")
	response, providerErr := provider.Apply(context.Background(), applyRequest(payload))
	if providerErr != nil || response.Result.State != protocol.ResultPublished {
		t.Fatalf("response=%+v err=%v", response, providerErr)
	}
	registry.mu.Lock()
	exchanges, puts := registry.exchanges, registry.puts
	registry.mu.Unlock()
	if exchanges != 1 || puts != 1 {
		t.Fatalf("exchanges=%d puts=%d", exchanges, puts)
	}
	if strings.Contains(string(response.Result.Evidence), testOIDC) || strings.Contains(string(response.Result.Evidence), exchanged) {
		t.Fatal("trusted publishing credential leaked to evidence")
	}
}

func TestPackageValidationHelpers(t *testing.T) {
	for _, value := range []string{"widget", "@scope/widget", "a-b.c_d"} {
		if !validPackageName(value) {
			t.Fatalf("valid package name rejected: %q", value)
		}
	}
	for _, value := range []string{"", "Widget", "@scope", "a/b", ".", "..", "bad name"} {
		if validPackageName(value) {
			t.Fatalf("invalid package name accepted: %q", value)
		}
	}
	for _, value := range []string{"latest", "next", "beta-1"} {
		if !validTag(value) {
			t.Fatalf("valid tag rejected: %q", value)
		}
	}
	for _, value := range []string{"", "1.2.3", "bad/tag", "bad tag"} {
		if validTag(value) {
			t.Fatalf("invalid tag accepted: %q", value)
		}
	}
	if escapedPackageName("@scope/widget") != "@scope%2Fwidget" {
		t.Fatalf("escaped=%q", escapedPackageName("@scope/widget"))
	}
	if packageBaseName("@scope/widget") != "widget" {
		t.Fatal("scoped package basename mismatch")
	}
}

func planRequest(t *testing.T, archive string, artifact protocol.Artifact, registry, mode string) protocol.PlanRequest {
	t.Helper()
	return protocol.PlanRequest{
		Release: protocol.Release{ID: "release", Artifacts: []protocol.Artifact{artifact}},
		Target:  protocol.Target{ID: "target", Configuration: configurationJSON(archive, registry, mode)},
	}
}

func configurationJSON(archive, registry, mode string) json.RawMessage {
	value := configuration{
		Artifact: "package", PackagePath: archive, Registry: registry, Tag: defaultTag, Access: "public", AllowInsecureHTTP: true,
		Authentication: authenticationConfig{Mode: mode, Credential: "publish"},
	}
	raw, _ := json.Marshal(value)
	return raw
}

func plannedPayload(t *testing.T, provider Provider, archive string, artifact protocol.Artifact, registry, mode string) json.RawMessage {
	t.Helper()
	response, providerErr := provider.Plan(context.Background(), planRequest(t, archive, artifact, registry, mode))
	if providerErr != nil {
		t.Fatal(providerErr)
	}
	return response.Operations[0].ProviderPayload
}

func applyRequest(payload json.RawMessage) protocol.ApplyRequest {
	return protocol.ApplyRequest{PlanID: "plan", TargetID: "target", OperationID: "operation", IdempotencyKey: "key", Attempt: 1, ProviderPayload: payload}
}

func reconcileRequest(payload json.RawMessage) protocol.ReconcileRequest {
	return protocol.ReconcileRequest{PlanID: "plan", TargetID: "target", OperationID: "operation", IdempotencyKey: "key", Attempt: 1, ProviderPayload: payload}
}

func env(values map[string]string) func(string) string {
	return func(name string) string { return values[name] }
}

func packageFixture(t *testing.T, name, version string) (string, protocol.Artifact) {
	t.Helper()
	filename := filepath.Join(t.TempDir(), "package.tgz")
	file, err := os.Create(filename)
	if err != nil {
		t.Fatal(err)
	}
	gz := gzip.NewWriter(file)
	tarWriter := tar.NewWriter(gz)
	manifest, _ := json.Marshal(map[string]any{"name": name, "version": version, "description": "fixture"})
	for path, data := range map[string][]byte{"package/package.json": manifest, "package/index.js": []byte("module.exports = true\n")} {
		if err := tarWriter.WriteHeader(&tar.Header{Name: path, Mode: 0o644, Size: int64(len(data))}); err != nil {
			t.Fatal(err)
		}
		if _, err := tarWriter.Write(data); err != nil {
			t.Fatal(err)
		}
	}
	if err := tarWriter.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filename)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(data)
	return filename, protocol.Artifact{Name: "package", Digest: "sha256:" + hex.EncodeToString(digest[:]), Size: int64(len(data)), MediaType: "application/gzip"}
}

func assertPublished(t *testing.T, result protocol.DistributionResult, name, version string) {
	t.Helper()
	if result.State != protocol.ResultPublished || result.ProviderState != "published" {
		t.Fatalf("result=%+v", result)
	}
	var value evidence
	if err := json.Unmarshal(result.Evidence, &value); err != nil {
		t.Fatal(err)
	}
	if value.Package != name || value.Version != version || value.Integrity == "" || value.Shasum == "" || value.SHA256 == "" || value.PublicationState != "published" {
		t.Fatalf("evidence=%+v", value)
	}
}

func TestPublishBodyAttachmentMatchesArchive(t *testing.T) {
	archive, artifact := packageFixture(t, "widget", "7.0.0")
	packageData, err := loadPackage(archive, artifact.Digest, artifact.Size)
	if err != nil {
		t.Fatal(err)
	}
	body := packageData.publishBody(operationPayload{Registry: "https://registry.example/", Tag: "latest", Access: "public"})
	var document publishDocument
	if err := json.Unmarshal(body, &document); err != nil {
		t.Fatal(err)
	}
	attachment := document.Attachments["widget-7.0.0.tgz"]
	decoded, err := base64.StdEncoding.DecodeString(attachment.Data)
	if err != nil {
		t.Fatal(err)
	}
	original, _ := os.ReadFile(archive)
	if string(decoded) != string(original) || attachment.Length != len(original) || attachment.ContentType != "application/octet-stream" {
		t.Fatal("publish attachment does not match package archive")
	}
}

func TestCancelledLookup(t *testing.T) {
	archive, artifact := packageFixture(t, "widget", "8.0.0")
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := "http://" + listener.Addr().String()
	_ = listener.Close()
	provider := Provider{Getenv: env(map[string]string{TokenEnvironment: testToken})}
	payload := plannedPayload(t, Provider{}, archive, artifact, address, "token")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, providerErr := provider.Reconcile(ctx, reconcileRequest(payload))
	if providerErr == nil || (providerErr.Code != protocol.ErrorCancelled && providerErr.Code != protocol.ErrorTransientExternal) {
		t.Fatalf("err=%v", providerErr)
	}
}
