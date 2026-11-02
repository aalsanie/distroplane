package sdkman

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestVendorAPIHTTPStatusDecisionMatrix(t *testing.T) {
	ctx := context.Background()
	cases := []struct {
		name     string
		code     int
		accepted bool
	}{
		{"created", http.StatusCreated, true},
		{"accepted", http.StatusAccepted, true},
		{"conflict", http.StatusConflict, true},
		{"unauthorized", http.StatusUnauthorized, false},
		{"forbidden", http.StatusForbidden, false},
		{"invalid", http.StatusBadRequest, false},
		{"unprocessable", http.StatusUnprocessableEntity, false},
		{"timeout", http.StatusRequestTimeout, false},
		{"rate-limited", http.StatusTooManyRequests, false},
		{"unavailable", http.StatusServiceUnavailable, false},
		{"permanent-error", http.StatusNotFound, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/release" || r.Header.Get("Consumer-Key") != "key" {
					t.Errorf("unexpected vendor API request: %s", r.URL)
				}
				w.WriteHeader(tc.code)
			}))
			defer srv.Close()
			status, err := (Provider{}).publish(ctx, operationPayload{
				API: srv.URL, Candidate: "sample", Version: "1.0.0",
			}, "key", "token")
			if tc.accepted != (err == nil) || (tc.accepted && status != tc.code) {
				t.Fatalf("HTTP %d: status=%d err=%v", tc.code, status, err)
			}
		})
	}
}

func TestStateAPIHTTPStatusDecisionMatrix(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		code     int
		accepted bool
	}{
		{http.StatusNoContent, true},
		{http.StatusConflict, true},
		{http.StatusUnauthorized, false},
		{http.StatusForbidden, false},
		{http.StatusBadRequest, false},
		{http.StatusUnprocessableEntity, false},
		{http.StatusRequestTimeout, false},
		{http.StatusTooManyRequests, false},
		{http.StatusInternalServerError, false},
		{http.StatusAccepted, false},
		{http.StatusNotFound, false},
	} {
		t.Run(http.StatusText(tc.code), func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/versions" || r.Header.Get("Authorization") != "Bearer token" {
					t.Errorf("unexpected state API request: %s", r.URL)
				}
				w.WriteHeader(tc.code)
			}))
			defer srv.Close()
			status, err := (Provider{}).publishState(ctx, operationPayload{
				StateAPI: srv.URL, Candidate: "sample", Version: "1.0.0",
			}, "token")
			if tc.accepted != (err == nil) || (tc.accepted && status != tc.code) {
				t.Fatalf("HTTP %d: status=%d err=%v", tc.code, status, err)
			}
		})
	}
}

func TestStateLookupConflictAndRecoveryObservations(t *testing.T) {
	payload := operationPayload{
		Mode: AuthState, Candidate: "sample", Version: "1.0.0",
		StatePlatform: "linuxx64", URL: "https://example.test/release.zip",
		Checksums: map[string]string{"SHA-256": strings.Repeat("a", 64)},
	}
	cases := []struct {
		name   string
		status int
		body   string
		exists bool
		error  bool
	}{
		{"absent", http.StatusNotFound, "", false, false},
		{"rate-limited", http.StatusTooManyRequests, "", false, true},
		{"request-timeout", http.StatusRequestTimeout, "", false, true},
		{"server-error", http.StatusBadGateway, "", false, true},
		{"forbidden", http.StatusForbidden, "", false, true},
		{"malformed", http.StatusOK, "{", false, true},
		{"wrong-candidate", http.StatusOK, `{"candidate":"wrong","version":"1.0.0","url":"https://example.test/release.zip","platform":"linuxx64"}`, false, true},
		{"wrong-platform", http.StatusOK, `{"candidate":"sample","version":"1.0.0","url":"https://example.test/release.zip","platform":"other"}`, false, true},
		{"empty-url", http.StatusOK, `{"candidate":"sample","version":"1.0.0","url":"   ","platform":"linuxx64"}`, false, true},
		{"observed", http.StatusOK, `{"candidate":"sample","version":"1.0.0","url":"https://example.test/release.zip","platform":"linuxx64"}`, true, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if !strings.Contains(r.URL.Path, "/versions/sample/1.0.0") {
					t.Errorf("unexpected lookup: %s", r.URL)
				}
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer srv.Close()
			request := payload
			request.StateAPI = srv.URL
			observed, err := (Provider{}).lookup(context.Background(), request)
			if (err != nil) != tc.error || observed.exists != tc.exists {
				t.Fatalf("lookup: %+v err=%v", observed, err)
			}
		})
	}
	observed := observedVersion{
		exists: true, candidate: payload.Candidate, version: payload.Version,
		url: payload.URL, sha256: payload.Checksums["SHA-256"],
	}
	if result := resultForObserved(payload, observed); result.ProviderState != "observed" {
		t.Fatalf("matching observation rejected: %+v", result)
	}
	for name, modify := range map[string]func(*operationPayload, *observedVersion){
		"url":      func(_ *operationPayload, o *observedVersion) { o.url = "https://example.test/different" },
		"checksum": func(_ *operationPayload, o *observedVersion) { o.sha256 = strings.Repeat("b", 64) },
		"distribution": func(p *operationPayload, o *observedVersion) {
			p.StateDistribution = "vendor"
			o.distribution = "other"
		},
		"unexpected distribution": func(_ *operationPayload, o *observedVersion) { o.distribution = "vendor" },
	} {
		t.Run(name, func(t *testing.T) {
			p, o := payload, observed
			modify(&p, &o)
			if result := resultForObserved(p, o); result.ProviderState != "conflict" {
				t.Fatalf("conflict was accepted: %+v", result)
			}
		})
	}
	withoutChecksum := observed
	withoutChecksum.sha256 = ""
	vendor := payload
	vendor.Vendor = "vendor"
	if result := resultForObserved(vendor, withoutChecksum); result.ProviderState != "observed" {
		t.Fatalf("weaker vendor observation rejected: %+v", result)
	}
}

func TestStateLoginAuthenticationFailureMatrix(t *testing.T) {
	for _, tc := range []struct {
		name  string
		code  int
		body  string
		valid bool
	}{
		{"ok", http.StatusOK, `{"token":"valid.token"}`, true},
		{"forbidden", http.StatusForbidden, "", false},
		{"unauthorized", http.StatusUnauthorized, "", false},
		{"rate-limited", http.StatusTooManyRequests, "", false},
		{"request-timeout", http.StatusRequestTimeout, "", false},
		{"server-error", http.StatusInternalServerError, "", false},
		{"permanent", http.StatusNotFound, "", false},
		{"empty", http.StatusOK, `{"token":""}`, false},
		{"malformed", http.StatusOK, `{"token":`, false},
		{"trailing-data", http.StatusOK, `{"token":"valid"} {}`, false},
		{"whitespace-token", http.StatusOK, `{"token":"bad token"}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/login" || r.Method != http.MethodPost {
					t.Errorf("unexpected login request: %s %s", r.Method, r.URL)
				}
				w.WriteHeader(tc.code)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer srv.Close()
			token, err := (Provider{}).login(context.Background(), operationPayload{StateAPI: srv.URL}, "email@example.test", "secret")
			if tc.valid != (err == nil) {
				t.Fatalf("token=%q err=%v", token, err)
			}
			if tc.valid && token == "" {
				t.Fatal("login did not yield a bearer token")
			}
		})
	}
	for _, token := range []string{"", "bad token", "x\n", "\x7f", strings.Repeat("a", 16385)} {
		if validBearer(token) {
			t.Errorf("invalid token accepted: %q", token[:min(20, len(token))])
		}
	}
	if !validBearer("header.payload.signature") {
		t.Fatal("valid printable bearer token rejected")
	}
}
