package npm

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aalsanie/distroplane/internal/protocol"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) { return f(request) }

func response(status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}
}

func TestValidationCoverage(t *testing.T) {
	provider := Provider{}
	description, providerErr := provider.Describe(context.Background(), protocol.DescribeRequest{})
	if providerErr != nil || description.Provider.Version != DefaultVersion {
		t.Fatalf("description=%+v err=%v", description, providerErr)
	}

	for _, raw := range []json.RawMessage{
		json.RawMessage(`{`),
		json.RawMessage(`{} trailing`),
		json.RawMessage(`{"artifact":"a"}`),
		json.RawMessage(`{"artifact":"a","packagePath":"/x","package":"p","version":"1.0.0","registry":"https://r/","tag":"latest","authentication":"bad","sha256":"sha256:x","size":1}`),
	} {
		if _, err := parsePayload(raw); err == nil {
			t.Fatalf("invalid payload accepted: %s", raw)
		}
	}
	validPayload := operationPayload{Artifact: "a", PackagePath: filepath.Join(t.TempDir(), "a.tgz"), Package: "p", Version: "1.0.0", Registry: "https://r/", Tag: "latest", Authentication: "token", SHA256: "sha256:" + strings.Repeat("a", 64), Size: 1}
	raw, _ := json.Marshal(validPayload)
	if _, err := parsePayload(raw); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		raw      string
		allow    bool
		wantFail bool
	}{
		{"", false, true},
		{"ftp://registry.example", false, true},
		{"http://registry.example", false, true},
		{"https://user@registry.example", false, true},
		{"https://registry.example/path?q=1", false, true},
		{"https://registry.example/path#x", false, true},
		{"https://registry.example/root", false, false},
		{"http://registry.example/root", true, false},
	} {
		value, err := normalizeRegistry(tc.raw, tc.allow)
		if tc.wantFail && err == nil {
			t.Fatalf("registry accepted: %q -> %q", tc.raw, value)
		}
		if !tc.wantFail && (err != nil || !strings.HasSuffix(value, "/")) {
			t.Fatalf("registry=%q err=%v", value, err)
		}
	}

	for _, value := range []string{"", " credential", "credential ", "bad\nref"} {
		if validCredentialRef(value) {
			t.Fatalf("credential ref accepted: %q", value)
		}
	}
	if !validCredentialRef("credential.ref") {
		t.Fatal("valid credential ref rejected")
	}

	t.Setenv("DISTROPLANE_NPM_TEST", "value")
	if provider.getenv("DISTROPLANE_NPM_TEST") != "value" || provider.client() != http.DefaultClient {
		t.Fatal("provider defaults mismatch")
	}
}

func TestPackageFailureCoverage(t *testing.T) {
	if _, err := loadPackage(filepath.Join(t.TempDir(), "missing.tgz"), "sha256:"+strings.Repeat("a", 64), 1); err == nil {
		t.Fatal("missing package accepted")
	}
	file := filepath.Join(t.TempDir(), "package.tgz")
	if err := os.WriteFile(file, []byte("abc"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadPackage(file, "sha256:"+strings.Repeat("a", 64), -1); err == nil {
		t.Fatal("negative size accepted")
	}
	if _, err := loadPackage(file, "sha256:"+strings.Repeat("a", 64), 2); err == nil || !strings.Contains(err.Error(), "size changed") {
		t.Fatalf("err=%v", err)
	}
	if _, err := loadPackage(file, "sha256:"+strings.Repeat("a", 64), 3); err == nil || !strings.Contains(err.Error(), "digest changed") {
		t.Fatalf("err=%v", err)
	}
	if _, _, _, err := packageManifest([]byte("not gzip")); err == nil {
		t.Fatal("invalid gzip accepted")
	}
	if _, _, _, err := packageManifest(gzipBytes(t, []byte("not tar"))); err == nil {
		t.Fatal("invalid tar accepted")
	}
	if _, _, _, err := packageManifest(tarball(t, "other.txt", []byte("x"), 1)); err == nil || !strings.Contains(err.Error(), "does not contain") {
		t.Fatalf("err=%v", err)
	}
	if _, _, _, err := packageManifest(tarball(t, "package/package.json", []byte("{"), 1)); err == nil || !strings.Contains(err.Error(), "invalid package.json") {
		t.Fatalf("err=%v", err)
	}
	if _, _, _, err := packageManifest(tarball(t, "package/package.json", []byte(`{"name":"Bad","version":"1.0.0"}`), int64(len(`{"name":"Bad","version":"1.0.0"}`)))); err == nil || !strings.Contains(err.Error(), "invalid name") {
		t.Fatalf("err=%v", err)
	}
	if _, _, _, err := packageManifest(tarball(t, "package/package.json", []byte(`{"name":"widget","version":"bad"}`), int64(len(`{"name":"widget","version":"bad"}`)))); err == nil || !strings.Contains(err.Error(), "semantic version") {
		t.Fatalf("err=%v", err)
	}
	if _, _, _, err := packageManifest(tarballHeaderOnly(t, "package/package.json", maxManifestBytes+1)); err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("err=%v", err)
	}
}

func TestPrepareExecutionFailures(t *testing.T) {
	provider := Provider{}
	if _, _, providerErr := provider.prepareExecution(json.RawMessage(`{}`)); providerErr == nil {
		t.Fatal("incomplete execution payload accepted")
	}
	archive, artifact := packageFixture(t, "widget", "1.0.0")
	payload := operationPayload{Artifact: "package", PackagePath: archive, Package: "other", Version: "1.0.0", Registry: "https://registry.example/", Tag: "latest", Authentication: "token", SHA256: artifact.Digest, Size: artifact.Size}
	raw, _ := json.Marshal(payload)
	if _, _, providerErr := provider.prepareExecution(raw); providerErr == nil || !strings.Contains(providerErr.Message, "identity changed") {
		t.Fatalf("err=%v", providerErr)
	}
	payload.Package = "widget"
	payload.SHA256 = "sha256:" + strings.Repeat("b", 64)
	raw, _ = json.Marshal(payload)
	if _, _, providerErr := provider.prepareExecution(raw); providerErr == nil || !strings.Contains(providerErr.Message, "digest changed") {
		t.Fatalf("err=%v", providerErr)
	}
}

func TestHTTPStatusAndTransportCoverage(t *testing.T) {
	payload := operationPayload{Package: "widget", Registry: "https://registry.example/", Version: "1.0.0", Authentication: "token"}
	for status, code := range map[int]protocol.ErrorCode{
		http.StatusUnauthorized:        protocol.ErrorAuthentication,
		http.StatusForbidden:           protocol.ErrorAuthorization,
		http.StatusTooManyRequests:     protocol.ErrorTransientExternal,
		http.StatusRequestTimeout:      protocol.ErrorTransientExternal,
		http.StatusInternalServerError: protocol.ErrorTransientExternal,
		http.StatusBadRequest:          protocol.ErrorPermanentExternal,
	} {
		providerErr := statusError("operation", status)
		if providerErr == nil || providerErr.Code != code {
			t.Fatalf("status=%d err=%v", status, providerErr)
		}
	}

	provider := Provider{Client: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return nil, errors.New("network")
	})}}
	if _, providerErr := provider.exchangeOIDC(context.Background(), payload, "oidc"); providerErr == nil || providerErr.Code != protocol.ErrorTransientExternal {
		t.Fatalf("exchange err=%v", providerErr)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, providerErr := provider.exchangeOIDC(ctx, payload, "oidc"); providerErr == nil || providerErr.Code != protocol.ErrorCancelled {
		t.Fatalf("exchange cancel err=%v", providerErr)
	}

	for _, tc := range []struct {
		status int
		body   string
		code   protocol.ErrorCode
	}{
		{http.StatusUnauthorized, `{}`, protocol.ErrorAuthentication},
		{http.StatusInternalServerError, `{}`, protocol.ErrorTransientExternal},
		{http.StatusCreated, `{}`, protocol.ErrorPermanentExternal},
		{http.StatusCreated, `{"token":""}`, protocol.ErrorPermanentExternal},
	} {
		provider = Provider{Client: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			return response(tc.status, tc.body), nil
		})}}
		if _, providerErr := provider.exchangeOIDC(context.Background(), payload, "oidc"); providerErr == nil || providerErr.Code != tc.code {
			t.Fatalf("tc=%+v err=%v", tc, providerErr)
		}
	}

	provider = Provider{Client: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return response(http.StatusNotFound, `{}`), nil
	})}}
	if observed, providerErr := provider.lookup(context.Background(), payload, "token"); providerErr != nil || observed.exists {
		t.Fatalf("observed=%+v err=%v", observed, providerErr)
	}
	for _, status := range []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusTooManyRequests, http.StatusInternalServerError, http.StatusBadRequest} {
		provider = Provider{Client: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			return response(status, `{}`), nil
		})}}
		if _, providerErr := provider.lookup(context.Background(), payload, "token"); providerErr == nil {
			t.Fatalf("lookup status %d accepted", status)
		}
	}
}

func TestPublishHTTPCoverage(t *testing.T) {
	archive, artifact := packageFixture(t, "widget", "1.0.0")
	packageData, err := loadPackage(archive, artifact.Digest, artifact.Size)
	if err != nil {
		t.Fatal(err)
	}
	payload := operationPayload{Package: "widget", Registry: "https://registry.example/", Version: "1.0.0", Tag: "latest", Authentication: "token"}

	for _, tc := range []struct {
		status int
		code   protocol.ErrorCode
	}{
		{http.StatusUnauthorized, protocol.ErrorAuthentication},
		{http.StatusForbidden, protocol.ErrorAuthorization},
		{http.StatusInternalServerError, protocol.ErrorAmbiguousOutcome},
		{http.StatusRequestTimeout, protocol.ErrorAmbiguousOutcome},
		{http.StatusTooManyRequests, protocol.ErrorAmbiguousOutcome},
		{http.StatusBadRequest, protocol.ErrorPermanentExternal},
	} {
		provider := Provider{Client: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			return response(tc.status, `{}`), nil
		})}}
		_, providerErr := provider.publish(context.Background(), payload, packageData, "token")
		if providerErr == nil || providerErr.Code != tc.code {
			t.Fatalf("status=%d err=%v", tc.status, providerErr)
		}
	}

	provider := Provider{Client: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return nil, errors.New("lost response")
	})}}
	if _, providerErr := provider.publish(context.Background(), payload, packageData, "token"); providerErr == nil || providerErr.Code != protocol.ErrorAmbiguousOutcome {
		t.Fatalf("err=%v", providerErr)
	}

	calls := 0
	provider = Provider{Client: &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		calls++
		if calls == 1 {
			return response(http.StatusConflict, `{}`), nil
		}
		return response(http.StatusOK, `{"versions":{"1.0.0":{"dist":{"integrity":"`+packageData.integrity+`","shasum":"`+packageData.shasum+`"}}}}`), nil
	})}}
	if observed, providerErr := provider.publish(context.Background(), payload, packageData, "token"); providerErr != nil || !observed.exists || observed.integrity != packageData.integrity {
		t.Fatalf("same-intent conflict observed=%+v err=%v", observed, providerErr)
	}

	calls = 0
	provider = Provider{Client: &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		calls++
		if calls == 1 {
			return response(http.StatusConflict, `{}`), nil
		}
		return response(http.StatusOK, `{"versions":{"1.0.0":{"dist":{"integrity":"other","shasum":"other"}}}}`), nil
	})}}
	if observed, providerErr := provider.publish(context.Background(), payload, packageData, "token"); providerErr != nil || !observed.exists || observed.integrity != "other" {
		t.Fatalf("conflict observed=%+v err=%v", observed, providerErr)
	}

	calls = 0
	provider = Provider{Client: &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		calls++
		if calls == 1 {
			return response(http.StatusConflict, `{}`), nil
		}
		return nil, errors.New("lookup failed")
	})}}
	if _, providerErr := provider.publish(context.Background(), payload, packageData, "token"); providerErr == nil || providerErr.Code != protocol.ErrorAmbiguousOutcome {
		t.Fatalf("conflict reconcile err=%v", providerErr)
	}
}

func TestApplyAndReconcileValidationCoverage(t *testing.T) {
	provider := Provider{}
	if _, providerErr := provider.Apply(context.Background(), protocol.ApplyRequest{}); providerErr == nil || providerErr.Code != protocol.ErrorProtocol {
		t.Fatalf("apply err=%v", providerErr)
	}
	if _, providerErr := provider.Reconcile(context.Background(), protocol.ReconcileRequest{}); providerErr == nil || providerErr.Code != protocol.ErrorProtocol {
		t.Fatalf("reconcile err=%v", providerErr)
	}
	request := applyRequest(json.RawMessage(`{}`))
	if _, providerErr := provider.Apply(context.Background(), request); providerErr == nil || providerErr.Code != protocol.ErrorConfiguration {
		t.Fatalf("apply err=%v", providerErr)
	}
	reconcile := reconcileRequest(json.RawMessage(`{}`))
	if _, providerErr := provider.Reconcile(context.Background(), reconcile); providerErr == nil || providerErr.Code != protocol.ErrorConfiguration {
		t.Fatalf("reconcile err=%v", providerErr)
	}
}

func gzipBytes(t *testing.T, payload []byte) []byte {
	t.Helper()
	var buffer bytes.Buffer
	writer := gzip.NewWriter(&buffer)
	if _, err := writer.Write(payload); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

func tarball(t *testing.T, name string, payload []byte, size int64) []byte {
	t.Helper()
	var buffer bytes.Buffer
	gz := gzip.NewWriter(&buffer)
	writer := tar.NewWriter(gz)
	if err := writer.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: size}); err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Write(payload); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

func tarballHeaderOnly(t *testing.T, name string, size int64) []byte {
	t.Helper()
	var buffer bytes.Buffer
	gz := gzip.NewWriter(&buffer)
	writer := tar.NewWriter(gz)
	if err := writer.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: size}); err != nil {
		t.Fatal(err)
	}
	_ = writer.Close()
	_ = gz.Close()
	return buffer.Bytes()
}
