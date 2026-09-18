package sdkman

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/aalsanie/distroplane/internal/protocol"
)

func TestValidationHelperCoverage(t *testing.T) {
	provider := Provider{}
	description, providerErr := provider.Describe(context.Background(), protocol.DescribeRequest{})
	if providerErr != nil || description.Provider.Version != DefaultVersion {
		t.Fatalf("description=%+v err=%v", description, providerErr)
	}

	if _, err := parseConfiguration(json.RawMessage(`{`)); err == nil {
		t.Fatal("malformed configuration accepted")
	}
	if err := decodeStrict([]byte(`{} {}`), &map[string]any{}); err == nil {
		t.Fatal("trailing JSON accepted")
	}
	if err := decodeStrict([]byte(`{} x`), &map[string]any{}); err == nil {
		t.Fatal("invalid trailing JSON accepted")
	}

	for _, tc := range []struct {
		value         string
		allowInsecure bool
		base          bool
	}{
		{"", false, false},
		{"ftp://example.test/a", false, false},
		{"http://example.test/a", false, false},
		{"https://user@example.test/a", false, false},
		{"https://example.test/a#fragment", false, false},
		{"https://example.test/a?q=1", false, true},
	} {
		if _, err := normalizeURL(tc.value, tc.allowInsecure, tc.base); err == nil {
			t.Fatalf("invalid URL accepted: %q", tc.value)
		}
	}
	if value, err := normalizeBaseURL("http://example.test/root/", true, "test"); err != nil || value != "http://example.test/root" {
		t.Fatalf("value=%q err=%v", value, err)
	}
	if value, err := normalizeDownloadURL("https://example.test/a?q=1", false); err != nil || !strings.Contains(value, "?q=1") {
		t.Fatalf("value=%q err=%v", value, err)
	}

	if validToken("", 10) || validToken("bad value", 20) || validToken("a/b", 20) || validToken(strings.Repeat("a", 11), 10) || !validToken("good-token", 20) {
		t.Fatal("token validation mismatch")
	}
	if validCredentialRef("") || validCredentialRef(" bad") || validCredentialRef("bad ref") || validCredentialRef("bad\nref") || !validCredentialRef("credential.ref") {
		t.Fatal("credential validation mismatch")
	}

	if err := validateChecksums(map[string]string{"MD5": strings.Repeat("z", 32)}); err == nil {
		t.Fatal("non-hex checksum accepted")
	}
	if _, err := bindArtifactChecksum(nil, "md5:abc"); err == nil {
		t.Fatal("non-SHA artifact digest accepted")
	}
	checksums, err := bindArtifactChecksum(map[string]string{"SHA-1": strings.Repeat("A", 40)}, "sha256:"+strings.Repeat("b", 64))
	if err != nil || checksums["SHA-1"] != strings.Repeat("a", 40) || checksums["SHA-256"] != strings.Repeat("b", 64) {
		t.Fatalf("checksums=%v err=%v", checksums, err)
	}

	if optionalPlatform("UNIVERSAL") != "" || optionalPlatform("MAC_ARM64") != "MAC_ARM64" {
		t.Fatal("platform encoding mismatch")
	}
	if stateStatusError(http.StatusServiceUnavailable).Code != protocol.ErrorTransientExternal || stateStatusError(http.StatusTeapot).Code != protocol.ErrorPermanentExternal {
		t.Fatal("state status classification mismatch")
	}
}

func TestPayloadValidationCoverage(t *testing.T) {
	base := operationPayload{
		Artifact: "archive", Candidate: "groovy", Version: "1.0.0", URL: "https://downloads.example/a.zip",
		Platform: "UNIVERSAL", StatePlatform: "universal", Checksums: map[string]string{"SHA-256": strings.Repeat("a", 64)},
		API: "https://vendors.example", StateAPI: "https://state.example", ArtifactDigest: "sha256:" + strings.Repeat("a", 64), ArtifactSize: 1,
	}
	raw, _ := json.Marshal(base)
	if _, err := parsePayload(raw); err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*operationPayload){
		"incomplete": func(p *operationPayload) { p.Candidate = "" },
		"platform":   func(p *operationPayload) { p.StatePlatform = "wrong" },
		"checksum":   func(p *operationPayload) { p.Checksums["SHA-256"] = strings.Repeat("b", 64) },
		"algorithm":  func(p *operationPayload) { p.Checksums["BAD"] = "x" },
	} {
		t.Run(name, func(t *testing.T) {
			value := base
			value.Checksums = map[string]string{"SHA-256": strings.Repeat("a", 64)}
			mutate(&value)
			raw, _ := json.Marshal(value)
			if _, err := parsePayload(raw); err == nil {
				t.Fatal("invalid payload accepted")
			}
		})
	}
	if _, err := parsePayload(json.RawMessage(`{"unknown":true}`)); err == nil {
		t.Fatal("unknown payload accepted")
	}
}

func TestDefaultClientAndEnvironmentCredentials(t *testing.T) {
	t.Setenv(ConsumerKeyEnvironment, testKey)
	t.Setenv(ConsumerTokenEnvironment, testToken)
	provider := Provider{}
	key, token, providerErr := provider.credentials()
	if providerErr != nil || key != testKey || token != testToken || provider.client() != http.DefaultClient || provider.getenv(ConsumerKeyEnvironment) != testKey {
		t.Fatalf("key=%q token=%q err=%v", key, token, providerErr)
	}
	_ = os.Unsetenv(ConsumerTokenEnvironment)
	if _, _, providerErr := provider.credentials(); providerErr == nil {
		t.Fatal("missing token accepted")
	}
}

func TestReconcileCancellationTimeoutAndTransportErrors(t *testing.T) {
	payload := operationPayload{
		Artifact: "archive", Candidate: "groovy", Version: "1.0.0", URL: "https://downloads.example/a.zip",
		Platform: "UNIVERSAL", StatePlatform: "universal", Checksums: map[string]string{"SHA-256": strings.Repeat("a", 64)},
		API: "https://vendors.example", StateAPI: "https://state.example", ArtifactDigest: "sha256:" + strings.Repeat("a", 64), ArtifactSize: 1,
	}

	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	provider := Provider{Client: &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		return nil, request.Context().Err()
	})}}
	_, providerErr := provider.Reconcile(cancelled, reconcileRequest(payload))
	if providerErr == nil || providerErr.Code != protocol.ErrorCancelled {
		t.Fatalf("err=%+v", providerErr)
	}

	deadline, deadlineCancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer deadlineCancel()
	_, providerErr = provider.Reconcile(deadline, reconcileRequest(payload))
	if providerErr == nil || providerErr.Code != protocol.ErrorTimeout || !providerErr.Retryable {
		t.Fatalf("err=%+v", providerErr)
	}

	provider.Client = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return nil, errors.New("offline")
	})}
	_, providerErr = provider.Reconcile(context.Background(), reconcileRequest(payload))
	if providerErr == nil || providerErr.Code != protocol.ErrorTransientExternal || !providerErr.Retryable {
		t.Fatalf("err=%+v", providerErr)
	}
}

func TestMalformedRequestConstructionAndInconsistentState(t *testing.T) {
	provider := Provider{Client: http.DefaultClient, Getenv: func(name string) string {
		if name == ConsumerKeyEnvironment {
			return testKey
		}
		return testToken
	}}
	payload := operationPayload{
		Artifact: "archive", Candidate: "groovy", Version: "1.0.0", URL: "https://downloads.example/a.zip",
		Platform: "UNIVERSAL", StatePlatform: "universal", Checksums: map[string]string{"SHA-256": strings.Repeat("a", 64)},
		API: "%", StateAPI: "%", ArtifactDigest: "sha256:" + strings.Repeat("a", 64), ArtifactSize: 1,
	}
	if _, providerErr := provider.publish(context.Background(), payload, testKey, testToken); providerErr == nil || providerErr.Code != protocol.ErrorProviderInternal {
		t.Fatalf("err=%+v", providerErr)
	}
	if _, providerErr := provider.lookup(context.Background(), payload); providerErr == nil || providerErr.Code != protocol.ErrorProviderInternal {
		t.Fatalf("err=%+v", providerErr)
	}

	fixture, server := newAPI(t, "normal", "normal")
	defer server.Close()
	provider = providerFor(server)
	payload = plannedPayload(t, provider, server.URL, nil)
	fixture.mu.Lock()
	fixture.state = &stateVersion{Candidate: "other", Version: payload.Version, URL: payload.URL}
	fixture.mu.Unlock()
	_, providerErr := provider.Reconcile(context.Background(), reconcileRequest(payload))
	if providerErr == nil || providerErr.Code != protocol.ErrorPermanentExternal {
		t.Fatalf("err=%+v", providerErr)
	}
}

func TestStateDistributionIdentityCoverage(t *testing.T) {
	fixture, server := newAPI(t, "normal", "normal")
	defer server.Close()
	provider := providerFor(server)
	payload := plannedPayload(t, provider, server.URL, func(cfg *configuration) {
		cfg.Vendor = "zulu"
		cfg.Platform = "LINUX_64"
		cfg.StateDistribution = "ZULU"
	})
	fixture.mu.Lock()
	fixture.state = &stateVersion{Candidate: payload.Candidate, Version: payload.Version, URL: payload.URL, SHA256: payload.Checksums["SHA-256"], Distribution: "TEMURIN"}
	fixture.mu.Unlock()
	response, providerErr := provider.Reconcile(context.Background(), reconcileRequest(payload))
	if providerErr != nil || response.Result.State != protocol.ResultRejected {
		t.Fatalf("response=%+v err=%v", response, providerErr)
	}

	fixture.mu.Lock()
	fixture.state.Distribution = "ZULU"
	fixture.mu.Unlock()
	response, providerErr = provider.Reconcile(context.Background(), reconcileRequest(payload))
	if providerErr != nil || response.Result.State != protocol.ResultPublished || strings.Contains(string(response.Result.Evidence), "limitation") {
		t.Fatalf("response=%+v err=%v", response, providerErr)
	}
}
