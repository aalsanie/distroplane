package sdkman

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestStateOperationPayloadIdentityAndAuthenticationValidation(t *testing.T) {
	checksum := strings.Repeat("a", 64)
	base := operationPayload{
		Mode: AuthState, Artifact: "release", Candidate: "sample", Version: "1.0",
		URL: "https://example.test/archive.zip", API: "https://vendor.example.test",
		StateAPI: "https://state.example.test", Platform: "LINUX_X64",
		StatePlatform: "LINUX_X64", ArtifactDigest: "sha256:" + checksum,
		ArtifactSize: 123, Checksums: map[string]string{"SHA-256": checksum},
	}
	parse := func(p operationPayload) error {
		raw, err := json.Marshal(p)
		if err != nil {
			return err
		}
		_, err = parsePayload(raw)
		return err
	}
	if err := parse(base); err != nil {
		t.Fatalf("valid state API payload rejected: %v", err)
	}
	for name, change := range map[string]func(*operationPayload){
		"mode":                   func(p *operationPayload) { p.Mode = "future" },
		"java":                   func(p *operationPayload) { p.Candidate = "java" },
		"distribution":           func(p *operationPayload) { p.StateDistribution = "WRONG" },
		"non-canonical-platform": func(p *operationPayload) { p.Platform = "LINUX_64"; p.StatePlatform = "LINUX_X64" },
		"platform-binding":       func(p *operationPayload) { p.StatePlatform = "MAC_X64" },
		"unsupported-checksum":   func(p *operationPayload) { p.Checksums["SHA-1"] = strings.Repeat("c", 40) },
		"checksum-binding":       func(p *operationPayload) { p.Checksums["SHA-256"] = strings.Repeat("b", 64) },
		"artifact-digest":        func(p *operationPayload) { p.ArtifactDigest = "md5:" + checksum },
		"missing-artifact":       func(p *operationPayload) { p.Artifact = "" },
		"negative-size":          func(p *operationPayload) { p.ArtifactSize = -1 },
	} {
		t.Run(name, func(t *testing.T) {
			p := base
			p.Checksums = make(map[string]string, len(base.Checksums))
			for k, v := range base.Checksums {
				p.Checksums[k] = v
			}
			change(&p)
			if err := parse(p); err == nil {
				t.Fatal("unsafe SDKMAN publication payload accepted")
			}
		})
	}
	for _, v := range []string{"LINUX_64", "LINUX_32", "MAC_OSX", "WINDOWS_64", "LINUX_X64"} {
		if canonicalPlatform(v) == "" {
			t.Fatal("valid platform mapped to empty coordinate")
		}
	}
	if !validStateDistributionOptional("") || !validStateDistribution("CORRETTO") ||
		validStateDistribution("UNKNOWN_VENDOR") {
		t.Fatal("SDKMAN distribution validation mismatch")
	}
}

func TestStateLoginCancellationAndRequestConstruction(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"token":"token"}`))
	}))
	defer srv.Close()
	p := Provider{Client: srv.Client()}
	payload := operationPayload{StateAPI: srv.URL}
	for _, timeout := range []bool{false, true} {
		var ctx context.Context
		if timeout {
			var cancel context.CancelFunc
			ctx, cancel = context.WithDeadline(context.Background(), time.Unix(0, 0))
			defer cancel()
		} else {
			var cancel context.CancelFunc
			ctx, cancel = context.WithCancel(context.Background())
			cancel()
		}
		if _, err := p.login(ctx, payload, "email", "secret"); err == nil {
			t.Fatal("cancelled SDKMAN login succeeded")
		}
		if _, err := p.lookup(ctx, operationPayload{StateAPI: srv.URL, Candidate: "sample", Version: "1.0"}); err == nil {
			t.Fatal("cancelled SDKMAN lookup succeeded")
		}
	}
	bad := operationPayload{
		StateAPI: "http://bad\x00endpoint",
		API:      "http://bad\x00endpoint",
	}
	if _, err := p.login(context.Background(), bad, "email", "secret"); err == nil {
		t.Fatal("invalid login request built")
	}
	if _, err := p.lookup(context.Background(), bad); err == nil {
		t.Fatal("invalid lookup request built")
	}
	if _, err := p.publishState(context.Background(), bad, "token"); err == nil {
		t.Fatal("invalid state publish request built")
	}
	if _, err := p.publish(context.Background(), bad, "key", "token"); err == nil {
		t.Fatal("invalid legacy publish request built")
	}
}
