package sdkman

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/aalsanie/distroplane/internal/protocol"
)

const (
	testKey   = "sdkman-consumer-key-secret"
	testToken = "sdkman-consumer-token-secret"
)

type apiFixture struct {
	mu           sync.Mutex
	releaseMode  string
	stateMode    string
	releases     int
	stateLookups int
	lastRelease  releaseRequest
	state        *stateVersion
}

func newAPI(t *testing.T, releaseMode, stateMode string) (*apiFixture, *httptest.Server) {
	t.Helper()
	fixture := &apiFixture{releaseMode: releaseMode, stateMode: stateMode}
	server := httptest.NewServer(http.HandlerFunc(fixture.serveHTTP))
	return fixture, server
}

func (f *apiFixture) serveHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/release" {
		f.handleRelease(w, r)
		return
	}
	if strings.HasPrefix(r.URL.Path, "/versions/") {
		f.handleState(w, r)
		return
	}
	w.WriteHeader(http.StatusNotFound)
}

func (f *apiFixture) handleRelease(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if f.releaseMode == "auth" || r.Header.Get("Consumer-Key") != testKey || r.Header.Get("Consumer-Token") != testToken {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	if f.releaseMode == "forbidden" {
		w.WriteHeader(http.StatusForbidden)
		return
	}
	var release releaseRequest
	if err := json.NewDecoder(r.Body).Decode(&release); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	f.mu.Lock()
	f.releases++
	f.lastRelease = release
	f.mu.Unlock()

	switch f.releaseMode {
	case "validation":
		w.WriteHeader(http.StatusBadRequest)
		return
	case "rejected":
		w.WriteHeader(http.StatusUnprocessableEntity)
		return
	case "permanent":
		w.WriteHeader(http.StatusGone)
		return
	case "transient":
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	case "rate":
		w.WriteHeader(http.StatusTooManyRequests)
		return
	case "conflict":
		w.WriteHeader(http.StatusConflict)
		return
	}

	state := &stateVersion{
		Candidate: release.Candidate,
		Version:   release.Version,
		URL:       release.URL,
		SHA256:    release.Checksums["SHA-256"],
	}
	f.mu.Lock()
	f.state = state
	f.mu.Unlock()
	if f.releaseMode == "ambiguous" {
		hijacker, ok := w.(http.Hijacker)
		if !ok {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		connection, _, err := hijacker.Hijack()
		if err == nil {
			_ = connection.Close()
		}
		return
	}
	w.WriteHeader(http.StatusCreated)
	_, _ = io.WriteString(w, `{"status":"released"}`)
}

func (f *apiFixture) handleState(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	f.mu.Lock()
	f.stateLookups++
	mode := f.stateMode
	var state *stateVersion
	if f.state != nil {
		copy := *f.state
		state = &copy
	}
	f.mu.Unlock()
	if r.URL.Query().Get("platform") == "" {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	switch mode {
	case "malformed":
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "{")
		return
	case "transient":
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	case "rate":
		w.WriteHeader(http.StatusTooManyRequests)
		return
	case "bad-request":
		w.WriteHeader(http.StatusBadRequest)
		return
	case "absent":
		w.WriteHeader(http.StatusNotFound)
		return
	}
	if state == nil {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(state)
}

func artifactFixture() protocol.Artifact {
	return protocol.Artifact{Name: "archive", Digest: "sha256:" + strings.Repeat("a", 64), Size: 123}
}

func configurationJSON(serverURL string, mutate func(*configuration)) json.RawMessage {
	cfg := configuration{
		Artifact: "archive", Candidate: "groovy", Version: "4.0.28", URL: "https://downloads.example/groovy.zip",
		API: serverURL, StateAPI: serverURL, AllowInsecureHTTP: true,
		Authentication: authenticationConfig{ConsumerKeyCredential: "sdkman-key", ConsumerTokenCredential: "sdkman-token"},
	}
	if mutate != nil {
		mutate(&cfg)
	}
	raw, _ := json.Marshal(cfg)
	return raw
}

func planRequest(serverURL string, mutate func(*configuration)) protocol.PlanRequest {
	return protocol.PlanRequest{
		Release: protocol.Release{ID: "release", Artifacts: []protocol.Artifact{artifactFixture()}},
		Target:  protocol.Target{ID: "sdkman", Configuration: configurationJSON(serverURL, mutate)},
	}
}

func providerFor(server *httptest.Server) Provider {
	return Provider{Client: server.Client(), Getenv: func(name string) string {
		switch name {
		case ConsumerKeyEnvironment:
			return testKey
		case ConsumerTokenEnvironment:
			return testToken
		default:
			return ""
		}
	}}
}

func plannedPayload(t *testing.T, provider Provider, serverURL string, mutate func(*configuration)) operationPayload {
	t.Helper()
	response, providerErr := provider.Plan(context.Background(), planRequest(serverURL, mutate))
	if providerErr != nil {
		t.Fatal(providerErr)
	}
	if len(response.Operations) != 1 {
		t.Fatalf("operations=%+v", response.Operations)
	}
	var payload operationPayload
	if err := json.Unmarshal(response.Operations[0].ProviderPayload, &payload); err != nil {
		t.Fatal(err)
	}
	return payload
}

func applyRequest(payload operationPayload) protocol.ApplyRequest {
	raw, _ := json.Marshal(payload)
	return protocol.ApplyRequest{PlanID: "plan", TargetID: "target", OperationID: "publish", IdempotencyKey: "key", Attempt: 1, ProviderPayload: raw}
}

func reconcileRequest(payload operationPayload) protocol.ReconcileRequest {
	raw, _ := json.Marshal(payload)
	return protocol.ReconcileRequest{PlanID: "plan", TargetID: "target", OperationID: "publish", IdempotencyKey: "key", Attempt: 1, ProviderPayload: raw}
}

func TestDescribeAndPlanAreProviderLocal(t *testing.T) {
	fixture, server := newAPI(t, "normal", "normal")
	defer server.Close()
	provider := Provider{Version: "1.2.0"}
	description, providerErr := provider.Describe(context.Background(), protocol.DescribeRequest{})
	if providerErr != nil || description.Provider.Name != Name || description.Provider.Version != "1.2.0" || len(description.Capabilities) != 3 {
		t.Fatalf("description=%+v err=%v", description, providerErr)
	}
	response, providerErr := provider.Plan(context.Background(), planRequest(server.URL, nil))
	if providerErr != nil {
		t.Fatal(providerErr)
	}
	if len(response.Operations) != 1 || !response.Operations[0].SideEffecting || response.Operations[0].TimeoutMillis != defaultOperationTimeout {
		t.Fatalf("operations=%+v", response.Operations)
	}
	if len(response.Requirements) != 2 || response.Requirements[0].Name != "sdkman-key" || response.Requirements[1].Name != "sdkman-token" {
		t.Fatalf("requirements=%+v", response.Requirements)
	}
	if !strings.Contains(string(response.Requirements[0].Metadata), ConsumerKeyEnvironment) || !strings.Contains(string(response.Requirements[1].Metadata), ConsumerTokenEnvironment) {
		t.Fatalf("requirements=%+v", response.Requirements)
	}
	var payload operationPayload
	if err := json.Unmarshal(response.Operations[0].ProviderPayload, &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Platform != "UNIVERSAL" || payload.StatePlatform != "universal" || payload.Checksums["SHA-256"] != strings.Repeat("a", 64) {
		t.Fatalf("payload=%+v", payload)
	}
	fixture.mu.Lock()
	calls := fixture.releases + fixture.stateLookups
	fixture.mu.Unlock()
	if calls != 0 {
		t.Fatalf("plan made network calls=%d", calls)
	}
}

func TestPlanSupportsVendorPlatformAndAdditionalChecksums(t *testing.T) {
	_, server := newAPI(t, "normal", "normal")
	defer server.Close()
	response, providerErr := (Provider{}).Plan(context.Background(), planRequest(server.URL, func(cfg *configuration) {
		cfg.Vendor = "zulu"
		cfg.Platform = "LINUX_64"
		cfg.StateDistribution = "ZULU"
		cfg.Checksums = map[string]string{"MD5": strings.Repeat("B", 32)}
	}))
	if providerErr != nil {
		t.Fatal(providerErr)
	}
	var payload operationPayload
	_ = json.Unmarshal(response.Operations[0].ProviderPayload, &payload)
	if payload.Vendor != "zulu" || payload.StatePlatform != "linuxx64" || payload.StateDistribution != "ZULU" || payload.Checksums["MD5"] != strings.Repeat("b", 32) {
		t.Fatalf("payload=%+v", payload)
	}
}

func TestPlanRejectsInvalidConfiguration(t *testing.T) {
	_, server := newAPI(t, "normal", "normal")
	defer server.Close()
	cases := map[string]func(*configuration){
		"artifact":           func(c *configuration) { c.Artifact = "" },
		"candidate":          func(c *configuration) { c.Candidate = "bad candidate" },
		"version":            func(c *configuration) { c.Version = "" },
		"download":           func(c *configuration) { c.URL = "ftp://example/file" },
		"platform":           func(c *configuration) { c.Platform = "SOLARIS_64" },
		"vendor":             func(c *configuration) { c.Vendor = "bad/vendor" },
		"distribution":       func(c *configuration) { c.StateDistribution = "bad dist" },
		"api":                func(c *configuration) { c.API = "ftp://example" },
		"state api":          func(c *configuration) { c.StateAPI = "https://user@example" },
		"checksum algorithm": func(c *configuration) { c.Checksums = map[string]string{"CRC32": "12345678"} },
		"checksum length":    func(c *configuration) { c.Checksums = map[string]string{"MD5": "abc"} },
		"key credential":     func(c *configuration) { c.Authentication.ConsumerKeyCredential = " bad " },
		"same credential": func(c *configuration) {
			c.Authentication.ConsumerTokenCredential = c.Authentication.ConsumerKeyCredential
		},
	}
	provider := Provider{}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			if _, providerErr := provider.Plan(context.Background(), planRequest(server.URL, mutate)); providerErr == nil || providerErr.Code != protocol.ErrorConfiguration {
				t.Fatalf("err=%v", providerErr)
			}
		})
	}

	request := planRequest(server.URL, func(c *configuration) { c.Checksums = map[string]string{"SHA-256": strings.Repeat("b", 64)} })
	if _, providerErr := provider.Plan(context.Background(), request); providerErr == nil || !strings.Contains(providerErr.Message, "does not match") {
		t.Fatalf("err=%v", providerErr)
	}
	request = planRequest(server.URL, nil)
	request.Release.Artifacts[0].Name = "other"
	if _, providerErr := provider.Plan(context.Background(), request); providerErr == nil || !strings.Contains(providerErr.Message, "not found") {
		t.Fatalf("err=%v", providerErr)
	}
	request = planRequest(server.URL, nil)
	request.Release.ID = ""
	if _, providerErr := provider.Plan(context.Background(), request); providerErr == nil || providerErr.Code != protocol.ErrorProtocol {
		t.Fatalf("err=%v", providerErr)
	}
	raw := configurationJSON(server.URL, nil)
	raw = json.RawMessage(strings.Replace(string(raw), `"artifact":`, `"unknown":true,"artifact":`, 1))
	request = planRequest(server.URL, nil)
	request.Target.Configuration = raw
	if _, providerErr := provider.Plan(context.Background(), request); providerErr == nil || providerErr.Code != protocol.ErrorConfiguration {
		t.Fatalf("err=%v", providerErr)
	}
}

func TestApplyPublishesAndReconcileObservesIdentity(t *testing.T) {
	fixture, server := newAPI(t, "normal", "normal")
	defer server.Close()
	provider := providerFor(server)
	payload := plannedPayload(t, provider, server.URL, nil)
	response, providerErr := provider.Apply(context.Background(), applyRequest(payload))
	if providerErr != nil {
		t.Fatal(providerErr)
	}
	if response.Result.State != protocol.ResultPublished || response.Result.ProviderState != "accepted" || !strings.Contains(string(response.Result.Evidence), "vendor-api-accepted") {
		t.Fatalf("response=%+v", response)
	}
	if strings.Contains(string(response.Result.Evidence), testKey) || strings.Contains(string(response.Result.Evidence), testToken) {
		t.Fatal("credential leaked into evidence")
	}
	fixture.mu.Lock()
	last := fixture.lastRelease
	fixture.mu.Unlock()
	if last.Candidate != "groovy" || last.Version != "4.0.28" || last.Platform != "" || last.Checksums["SHA-256"] != strings.Repeat("a", 64) {
		t.Fatalf("release=%+v", last)
	}
	reconciled, providerErr := provider.Reconcile(context.Background(), reconcileRequest(payload))
	if providerErr != nil {
		t.Fatal(providerErr)
	}
	if reconciled.Result.State != protocol.ResultPublished || reconciled.Result.ProviderState != "observed" || !strings.Contains(string(reconciled.Result.Evidence), "state-url-and-sha256") {
		t.Fatalf("reconciled=%+v", reconciled)
	}
}

func TestApplyFailureClassification(t *testing.T) {
	cases := []struct {
		mode      string
		code      protocol.ErrorCode
		retryable bool
	}{
		{"auth", protocol.ErrorAuthentication, false},
		{"forbidden", protocol.ErrorAuthorization, false},
		{"validation", protocol.ErrorRejected, false},
		{"rejected", protocol.ErrorRejected, false},
		{"permanent", protocol.ErrorPermanentExternal, false},
		{"transient", protocol.ErrorAmbiguousOutcome, false},
		{"rate", protocol.ErrorAmbiguousOutcome, false},
	}
	for _, tc := range cases {
		t.Run(tc.mode, func(t *testing.T) {
			_, server := newAPI(t, tc.mode, "normal")
			defer server.Close()
			provider := providerFor(server)
			payload := plannedPayload(t, provider, server.URL, nil)
			_, providerErr := provider.Apply(context.Background(), applyRequest(payload))
			if providerErr == nil || providerErr.Code != tc.code || providerErr.Retryable != tc.retryable {
				t.Fatalf("err=%+v", providerErr)
			}
		})
	}
}

func TestApplyRequiresBothCredentials(t *testing.T) {
	_, server := newAPI(t, "normal", "normal")
	defer server.Close()
	provider := Provider{Client: server.Client(), Getenv: func(name string) string {
		if name == ConsumerKeyEnvironment {
			return testKey
		}
		return ""
	}}
	payload := plannedPayload(t, provider, server.URL, nil)
	_, providerErr := provider.Apply(context.Background(), applyRequest(payload))
	if providerErr == nil || providerErr.Code != protocol.ErrorAuthentication || strings.Contains(providerErr.Message, testKey) {
		t.Fatalf("err=%+v", providerErr)
	}
}

func TestAmbiguousPublishReconcilesAfterAcceptance(t *testing.T) {
	_, server := newAPI(t, "ambiguous", "normal")
	defer server.Close()
	provider := providerFor(server)
	payload := plannedPayload(t, provider, server.URL, nil)
	_, providerErr := provider.Apply(context.Background(), applyRequest(payload))
	if providerErr == nil || providerErr.Code != protocol.ErrorAmbiguousOutcome || providerErr.Retryable {
		t.Fatalf("err=%+v", providerErr)
	}
	response, providerErr := provider.Reconcile(context.Background(), reconcileRequest(payload))
	if providerErr != nil || response.Result.State != protocol.ResultPublished {
		t.Fatalf("response=%+v err=%v", response, providerErr)
	}
}

func TestConflictIsReconciled(t *testing.T) {
	fixture, server := newAPI(t, "conflict", "normal")
	defer server.Close()
	provider := providerFor(server)
	payload := plannedPayload(t, provider, server.URL, nil)
	fixture.mu.Lock()
	fixture.state = &stateVersion{Candidate: payload.Candidate, Version: payload.Version, URL: payload.URL, SHA256: payload.Checksums["SHA-256"]}
	fixture.mu.Unlock()
	response, providerErr := provider.Apply(context.Background(), applyRequest(payload))
	if providerErr != nil || response.Result.State != protocol.ResultPublished || response.Result.ProviderState != "observed" {
		t.Fatalf("response=%+v err=%v", response, providerErr)
	}

	fixture.mu.Lock()
	fixture.state = &stateVersion{Candidate: payload.Candidate, Version: payload.Version, URL: "https://other.example/file", SHA256: payload.Checksums["SHA-256"]}
	fixture.mu.Unlock()
	response, providerErr = provider.Apply(context.Background(), applyRequest(payload))
	if providerErr != nil || response.Result.State != protocol.ResultRejected || response.Result.ProviderState != "conflict" {
		t.Fatalf("response=%+v err=%v", response, providerErr)
	}

	fixture.mu.Lock()
	fixture.state = nil
	fixture.mu.Unlock()
	_, providerErr = provider.Apply(context.Background(), applyRequest(payload))
	if providerErr == nil || providerErr.Code != protocol.ErrorAmbiguousOutcome {
		t.Fatalf("err=%+v", providerErr)
	}
}

func TestReconcileAbsentLimitedAndConflictingState(t *testing.T) {
	fixture, server := newAPI(t, "normal", "absent")
	defer server.Close()
	provider := providerFor(server)
	payload := plannedPayload(t, provider, server.URL, func(cfg *configuration) {
		cfg.Vendor = "zulu"
		cfg.Platform = "LINUX_64"
	})
	response, providerErr := provider.Reconcile(context.Background(), reconcileRequest(payload))
	if providerErr != nil || response.Result.State != protocol.ResultWaitingExternal || response.Result.ProviderState != "absent" {
		t.Fatalf("response=%+v err=%v", response, providerErr)
	}

	fixture.mu.Lock()
	fixture.stateMode = "normal"
	fixture.state = &stateVersion{Candidate: payload.Candidate, Version: payload.Version, URL: payload.URL}
	fixture.mu.Unlock()
	response, providerErr = provider.Reconcile(context.Background(), reconcileRequest(payload))
	if providerErr != nil || response.Result.State != protocol.ResultPublished || !strings.Contains(string(response.Result.Evidence), "state-url-only") || !strings.Contains(string(response.Result.Evidence), "stateDistribution") {
		t.Fatalf("response=%+v err=%v", response, providerErr)
	}

	fixture.mu.Lock()
	fixture.state = &stateVersion{Candidate: payload.Candidate, Version: payload.Version, URL: payload.URL, SHA256: strings.Repeat("b", 64)}
	fixture.mu.Unlock()
	response, providerErr = provider.Reconcile(context.Background(), reconcileRequest(payload))
	if providerErr != nil || response.Result.State != protocol.ResultRejected {
		t.Fatalf("response=%+v err=%v", response, providerErr)
	}
}

func TestReconcileFailureClassification(t *testing.T) {
	cases := []struct {
		mode      string
		code      protocol.ErrorCode
		retryable bool
	}{
		{"malformed", protocol.ErrorPermanentExternal, false},
		{"transient", protocol.ErrorTransientExternal, true},
		{"rate", protocol.ErrorTransientExternal, true},
		{"bad-request", protocol.ErrorPermanentExternal, false},
	}
	for _, tc := range cases {
		t.Run(tc.mode, func(t *testing.T) {
			_, server := newAPI(t, "normal", tc.mode)
			defer server.Close()
			provider := providerFor(server)
			payload := plannedPayload(t, provider, server.URL, nil)
			_, providerErr := provider.Reconcile(context.Background(), reconcileRequest(payload))
			if providerErr == nil || providerErr.Code != tc.code || providerErr.Retryable != tc.retryable {
				t.Fatalf("err=%+v", providerErr)
			}
		})
	}
}

func TestExecutionRequestAndPayloadValidation(t *testing.T) {
	provider := Provider{}
	if _, providerErr := provider.Apply(context.Background(), protocol.ApplyRequest{}); providerErr == nil || providerErr.Code != protocol.ErrorProtocol {
		t.Fatalf("err=%v", providerErr)
	}
	if _, providerErr := provider.Reconcile(context.Background(), protocol.ReconcileRequest{}); providerErr == nil || providerErr.Code != protocol.ErrorProtocol {
		t.Fatalf("err=%v", providerErr)
	}
	raw, _ := json.Marshal(operationPayload{Artifact: "archive"})
	request := protocol.ApplyRequest{PlanID: "p", TargetID: "t", OperationID: "o", IdempotencyKey: "k", Attempt: 1, ProviderPayload: raw}
	if _, providerErr := provider.Apply(context.Background(), request); providerErr == nil || providerErr.Code != protocol.ErrorConfiguration {
		t.Fatalf("err=%v", providerErr)
	}
}

func TestTransportFailureIsAmbiguous(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return nil, &net.DNSError{Err: "offline", Name: "vendors.example"}
	})}
	provider := Provider{Client: client, Getenv: func(name string) string {
		if name == ConsumerKeyEnvironment {
			return testKey
		}
		return testToken
	}}
	payload := operationPayload{
		Artifact: "archive", Candidate: "groovy", Version: "1.0.0", URL: "https://downloads.example/a.zip", Platform: "UNIVERSAL", StatePlatform: "universal",
		Checksums: map[string]string{"SHA-256": strings.Repeat("a", 64)}, API: "https://vendors.example", StateAPI: "https://state.example",
		ArtifactDigest: "sha256:" + strings.Repeat("a", 64), ArtifactSize: 1,
	}
	_, providerErr := provider.Apply(context.Background(), applyRequest(payload))
	if providerErr == nil || providerErr.Code != protocol.ErrorAmbiguousOutcome {
		t.Fatalf("err=%+v", providerErr)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) { return f(request) }
