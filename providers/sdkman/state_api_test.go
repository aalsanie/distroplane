package sdkman

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/aalsanie/distroplane/internal/protocol"
)

const (
	stateEmail    = "release-vendor@example.org"
	statePassword = "state-secret-password-123"
	stateJWT      = "eyJhbGciOiJub25lIn0.eyJzdWIiOiJ2ZW5kb3IifQ.signature"
)

type stateAPIFixture struct {
	mu             sync.Mutex
	loginStatus    int
	publishStatus  int
	lookups        int
	logins         int
	publishes      int
	observed       *stateVersion
	lastPublished  statePublishRequest
	lastBody       []byte
	badAuth        bool
	badPublishAuth bool
}

func (f *stateAPIFixture) handler(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/versions/"):
		f.mu.Lock()
		f.lookups++
		var observed *stateVersion
		if f.observed != nil {
			copy := *f.observed
			observed = &copy
		}
		f.mu.Unlock()
		if r.URL.Query().Get("platform") != "UNIVERSAL" || r.Header.Get("Authorization") != "" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if observed == nil {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(observed)
	case r.Method == http.MethodPost && r.URL.Path == "/login":
		body, _ := io.ReadAll(io.LimitReader(r.Body, 4096))
		var auth struct {
			Email    string `json:"email"`
			Password string `json:"password"`
		}
		_ = json.Unmarshal(body, &auth)
		f.mu.Lock()
		f.logins++
		status := f.loginStatus
		f.badAuth = f.badAuth || auth.Email != stateEmail || auth.Password != statePassword ||
			r.Header.Get("Consumer-Key") != "" || r.Header.Get("Authorization") != ""
		f.mu.Unlock()
		if status != 0 {
			w.WriteHeader(status)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"token":"`+stateJWT+`"}`)
	case r.Method == http.MethodPost && r.URL.Path == "/versions":
		body, _ := io.ReadAll(io.LimitReader(r.Body, 4096))
		var entry statePublishRequest
		_ = json.Unmarshal(body, &entry)
		f.mu.Lock()
		f.publishes++
		f.lastPublished = entry
		f.lastBody = body
		f.badPublishAuth = f.badPublishAuth || r.Header.Get("Authorization") != "Bearer "+stateJWT ||
			r.Header.Get("Consumer-Key") != "" || r.Header.Get("Consumer-Token") != ""
		status := f.publishStatus
		if status == 0 || status == http.StatusNoContent {
			f.observed = &stateVersion{Candidate: entry.Candidate, Version: entry.Version,
				URL: entry.URL, Platform: entry.Platform, SHA256: entry.SHA256, Distribution: entry.Distribution}
		}
		f.mu.Unlock()
		if status == 0 {
			status = http.StatusNoContent
		}
		w.WriteHeader(status)
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

func statePlan(t *testing.T, provider Provider, url string, mutate func(*configuration)) (operationPayload, protocol.PlanResponse) {
	t.Helper()
	result, e := provider.Plan(context.Background(), planRequest(url, func(c *configuration) {
		c.Authentication = authenticationConfig{
			Mode: AuthState, EmailCredential: "sdkman-email", PasswordCredential: "sdkman-password",
		}
		if mutate != nil {
			mutate(c)
		}
	}))
	if e != nil {
		t.Fatal(e)
	}
	var payload operationPayload
	if err := json.Unmarshal(result.Operations[0].ProviderPayload, &payload); err != nil {
		t.Fatal(err)
	}
	return payload, result
}

func stateProvider(server *httptest.Server) Provider {
	return Provider{Client: server.Client(), Getenv: func(name string) string {
		switch name {
		case VendorEmailEnvironment:
			return stateEmail
		case VendorPasswordEnvironment:
			return statePassword
		default:
			return ""
		}
	}}
}

func TestStateAPIJWTContractAndIdempotentPreflight(t *testing.T) {
	fixture := &stateAPIFixture{}
	server := httptest.NewServer(http.HandlerFunc(fixture.handler))
	defer server.Close()
	provider := stateProvider(server)
	payload, plan := statePlan(t, provider, server.URL, nil)
	if payload.Mode != AuthState || payload.StatePlatform != "UNIVERSAL" || payload.Platform != "UNIVERSAL" ||
		len(plan.Requirements) != 2 ||
		plan.Requirements[0].Name != "sdkman-email" || plan.Requirements[1].Name != "sdkman-password" ||
		!strings.Contains(string(plan.Requirements[0].Metadata), VendorEmailEnvironment) ||
		!strings.Contains(string(plan.Requirements[1].Metadata), VendorPasswordEnvironment) {
		t.Fatalf("state plan=%+v payload=%+v", plan.Requirements, payload)
	}
	if strings.Contains(string(plan.Operations[0].ProviderPayload), statePassword) ||
		strings.Contains(string(plan.Operations[0].ProviderPayload), stateEmail) {
		t.Fatal("state credentials in plan")
	}
	first, e := provider.Apply(context.Background(), applyRequest(payload))
	if e != nil || first.Result.State != protocol.ResultPublished {
		t.Fatalf("first apply=%+v err=%+v", first, e)
	}
	if !strings.Contains(string(first.Result.Evidence), "vendor-api-accepted") ||
		strings.Contains(string(first.Result.Evidence), statePassword) ||
		strings.Contains(string(first.Result.Evidence), stateJWT) {
		t.Fatalf("unsafe evidence=%s", first.Result.Evidence)
	}
	second, e := provider.Apply(context.Background(), applyRequest(payload))
	if e != nil || second.Result.State != protocol.ResultPublished ||
		second.Result.ProviderState != "observed" {
		t.Fatalf("idempotent apply=%+v err=%+v", second, e)
	}
	reconciled, e := provider.Reconcile(context.Background(), reconcileRequest(payload))
	if e != nil || reconciled.Result.State != protocol.ResultPublished {
		t.Fatalf("reconciled=%+v err=%+v", reconciled, e)
	}
	fixture.mu.Lock()
	defer fixture.mu.Unlock()
	if fixture.badAuth || fixture.badPublishAuth || fixture.logins != 1 || fixture.publishes != 1 ||
		fixture.lookups != 3 ||
		fixture.lastPublished.Candidate != "groovy" || fixture.lastPublished.Version != "4.0.28" ||
		fixture.lastPublished.Platform != "UNIVERSAL" ||
		fixture.lastPublished.SHA256 != strings.Repeat("a", 64) ||
		strings.Contains(string(fixture.lastBody), statePassword) ||
		strings.Contains(string(fixture.lastBody), stateJWT) ||
		strings.Contains(string(fixture.lastBody), "checksums") ||
		strings.Contains(string(fixture.lastBody), "vendor") {
		t.Fatalf("state API request mismatch login=%d published=%d lookups=%d body=%s", fixture.logins, fixture.publishes, fixture.lookups, fixture.lastBody)
	}
}

func TestStateAPIRejectsExistingConflictsWithoutMutation(t *testing.T) {
	fixture := &stateAPIFixture{observed: &stateVersion{
		Candidate: "groovy", Version: "4.0.28", Platform: "UNIVERSAL",
		URL: "https://attacker.example/other.zip", SHA256: strings.Repeat("a", 64),
	}}
	server := httptest.NewServer(http.HandlerFunc(fixture.handler))
	defer server.Close()
	provider := stateProvider(server)
	payload, _ := statePlan(t, provider, server.URL, nil)
	result, e := provider.Apply(context.Background(), applyRequest(payload))
	if e != nil || result.Result.State != protocol.ResultRejected {
		t.Fatalf("conflict=%+v err=%+v", result, e)
	}
	fixture.mu.Lock()
	defer fixture.mu.Unlock()
	if fixture.logins != 0 || fixture.publishes != 0 {
		t.Fatal("State API mutated an existing conflicting version")
	}
}

func TestStateAPIAuthenticationAndResponseFailuresAreSafe(t *testing.T) {
	for _, tc := range []struct {
		name          string
		loginStatus   int
		publishStatus int
		code          protocol.ErrorCode
		retryable     bool
		publishes     int
	}{
		{"bad login", http.StatusUnauthorized, 0, protocol.ErrorAuthentication, false, 0},
		{"login rate limit", http.StatusTooManyRequests, 0, protocol.ErrorTransientExternal, true, 0},
		{"login unavailable", http.StatusServiceUnavailable, 0, protocol.ErrorTransientExternal, true, 0},
		{"publish auth", 0, http.StatusUnauthorized, protocol.ErrorAuthentication, false, 1},
		{"publish unauthorized candidate", 0, http.StatusForbidden, protocol.ErrorAuthorization, false, 1},
		{"publish invalid", 0, http.StatusBadRequest, protocol.ErrorRejected, false, 1},
		{"publish uncertain", 0, http.StatusServiceUnavailable, protocol.ErrorAmbiguousOutcome, false, 1},
		{"publish rate limit", 0, http.StatusTooManyRequests, protocol.ErrorAmbiguousOutcome, false, 1},
		{"wrong success contract", 0, http.StatusCreated, protocol.ErrorAmbiguousOutcome, false, 1},
		{"publish conflict unobserved", 0, http.StatusConflict, protocol.ErrorAmbiguousOutcome, false, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := &stateAPIFixture{loginStatus: tc.loginStatus, publishStatus: tc.publishStatus}
			server := httptest.NewServer(http.HandlerFunc(f.handler))
			defer server.Close()
			provider := stateProvider(server)
			payload, _ := statePlan(t, provider, server.URL, nil)
			_, e := provider.Apply(context.Background(), applyRequest(payload))
			if e == nil || e.Code != tc.code || e.Retryable != tc.retryable ||
				strings.Contains(e.Message, statePassword) || strings.Contains(e.Message, stateJWT) ||
				strings.Contains(e.Message, stateEmail) {
				t.Fatalf("error classification=%+v want=%q retry=%t", e, tc.code, tc.retryable)
			}
			f.mu.Lock()
			defer f.mu.Unlock()
			if f.publishes != tc.publishes {
				t.Fatalf("unsafe publish count=%d want=%d", f.publishes, tc.publishes)
			}
		})
	}
}

func TestStateAPIPlanCanonicalPlatformAndRejectInvalidModes(t *testing.T) {
	provider := Provider{}
	for _, tc := range []struct{ name, platform, canonical string }{
		{"universal", "UNIVERSAL", "UNIVERSAL"},
		{"linux", "LINUX_64", "LINUX_X64"},
		{"mac", "MAC_OSX", "MAC_X64"},
		{"windows", "WINDOWS_64", "WINDOWS_X64"},
		{"new platform", "LINUX_ARM64", "LINUX_ARM64"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			payload, _ := statePlan(t, provider, "https://state.sdkman.io", func(c *configuration) { c.Platform = tc.platform })
			if payload.Platform != tc.canonical || payload.StatePlatform != tc.canonical {
				t.Fatalf("platform=%s state=%s", payload.Platform, payload.StatePlatform)
			}
		})
	}
	for name, mutate := range map[string]func(*configuration){
		"unknown mode":             func(c *configuration) { c.Authentication.Mode = "basic" },
		"mixed auth":               func(c *configuration) { c.Authentication.ConsumerKeyCredential = "old-key" },
		"duplicate auth":           func(c *configuration) { c.Authentication.PasswordCredential = c.Authentication.EmailCredential },
		"java":                     func(c *configuration) { c.Candidate = "java" },
		"unsupported distribution": func(c *configuration) { c.StateDistribution = "unknown" },
		"unsupported checksum":     func(c *configuration) { c.Checksums = map[string]string{"SHA-1": strings.Repeat("a", 40)} },
	} {
		t.Run(name, func(t *testing.T) {
			_, e := provider.Plan(context.Background(), planRequest("https://state.sdkman.io", func(c *configuration) {
				c.Authentication = authenticationConfig{Mode: AuthState, EmailCredential: "sdkman-email", PasswordCredential: "sdkman-password"}
				mutate(c)
			}))
			if e == nil || e.Code != protocol.ErrorConfiguration {
				t.Fatalf("invalid state config accepted: %+v", e)
			}
		})
	}
}

func TestStateAPIMissingCredentialsAndNoLeaks(t *testing.T) {
	f := &stateAPIFixture{}
	server := httptest.NewServer(http.HandlerFunc(f.handler))
	defer server.Close()
	provider := Provider{Client: server.Client(), Getenv: func(string) string { return "" }}
	payload, _ := statePlan(t, provider, server.URL, nil)
	_, e := provider.Apply(context.Background(), applyRequest(payload))
	if e == nil || e.Code != protocol.ErrorAuthentication {
		t.Fatalf("missing credentials=%+v", e)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.logins != 0 || f.publishes != 0 {
		t.Fatal("attempted API calls without credentials")
	}
}

func TestStateAPILostPublishResponseReconcilesWithoutDuplicate(t *testing.T) {
	var mu sync.Mutex
	var observed *stateVersion
	var publishes int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/versions/"):
			mu.Lock()
			result := observed
			mu.Unlock()
			if result == nil {
				w.WriteHeader(http.StatusNotFound)
			} else {
				_ = json.NewEncoder(w).Encode(result)
			}
		case r.Method == http.MethodPost && r.URL.Path == "/login":
			_, _ = io.WriteString(w, `{"token":"`+stateJWT+`"}`)
		case r.Method == http.MethodPost && r.URL.Path == "/versions":
			if r.Header.Get("Authorization") != "Bearer "+stateJWT {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			var publish statePublishRequest
			if err := json.NewDecoder(r.Body).Decode(&publish); err != nil {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			mu.Lock()
			publishes++
			observed = &stateVersion{
				Candidate: publish.Candidate, Version: publish.Version, Platform: publish.Platform,
				URL: publish.URL, SHA256: publish.SHA256, Distribution: publish.Distribution,
			}
			mu.Unlock()
			connection, _, err := w.(http.Hijacker).Hijack()
			if err == nil {
				_ = connection.Close() // Simulate a successful server-side write with lost response.
			}
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	provider := stateProvider(server)
	payload, _ := statePlan(t, provider, server.URL, nil)
	_, providerErr := provider.Apply(context.Background(), applyRequest(payload))
	if providerErr == nil || providerErr.Code != protocol.ErrorAmbiguousOutcome || providerErr.Retryable {
		t.Fatalf("lost response was not ambiguous: %+v", providerErr)
	}
	reconciled, providerErr := provider.Reconcile(context.Background(), reconcileRequest(payload))
	if providerErr != nil || reconciled.Result.State != protocol.ResultPublished {
		t.Fatalf("reconciliation failed: result=%+v err=%+v", reconciled, providerErr)
	}
	again, providerErr := provider.Apply(context.Background(), applyRequest(payload))
	if providerErr != nil || again.Result.State != protocol.ResultPublished {
		t.Fatalf("idempotent retry failed: result=%+v err=%+v", again, providerErr)
	}
	mu.Lock()
	defer mu.Unlock()
	if publishes != 1 {
		t.Fatalf("reconciliation accidentally published %d times", publishes)
	}
}

func TestStateAPIRedirectsDoNotForwardCredentials(t *testing.T) {
	var leaked int
	destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		leaked++
		w.WriteHeader(http.StatusOK)
	}))
	defer destination.Close()
	for _, redirectStage := range []string{"login", "publish"} {
		t.Run(redirectStage, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch {
				case r.Method == http.MethodGet:
					w.WriteHeader(http.StatusNotFound)
				case r.URL.Path == "/login" && redirectStage == "publish":
					_, _ = io.WriteString(w, `{"token":"`+stateJWT+`"}`)
				default:
					w.Header().Set("Location", destination.URL)
					w.WriteHeader(http.StatusTemporaryRedirect)
				}
			}))
			defer server.Close()
			provider := stateProvider(server)
			payload, _ := statePlan(t, provider, server.URL, nil)
			_, providerErr := provider.Apply(context.Background(), applyRequest(payload))
			if providerErr == nil || leaked != 0 {
				t.Fatalf("redirect leaked SDKMAN credentials: err=%+v requests=%d", providerErr, leaked)
			}
		})
	}
}
