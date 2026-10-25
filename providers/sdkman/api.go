package sdkman

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/aalsanie/distroplane/internal/protocol"
)

type observedVersion struct {
	exists       bool
	candidate    string
	version      string
	url          string
	sha256       string
	distribution string
}

type stateVersion struct {
	Candidate    string `json:"candidate"`
	Version      string `json:"version"`
	URL          string `json:"url"`
	SHA256       string `json:"sha256sum,omitempty"`
	Distribution string `json:"distribution,omitempty"`
	Platform     string `json:"platform,omitempty"`
}

// State API operations use the documented login + bearer JWT + /versions
// contract. JWTs are short-lived and never persisted in the plan, journal or
// evidence. A new token is obtained for every publish invocation.
type statePublishRequest struct {
	Candidate    string `json:"candidate"`
	Version      string `json:"version"`
	Platform     string `json:"platform"`
	URL          string `json:"url"`
	Distribution string `json:"distribution,omitempty"`
	MD5          string `json:"md5sum,omitempty"`
	SHA256       string `json:"sha256sum,omitempty"`
	SHA512       string `json:"sha512sum,omitempty"`
}

func (p Provider) noRedirectClient() *http.Client {
	// A redirect can forward secrets to an unexpected server. Never follow
	// redirects for requests carrying a vendor password or bearer credential.
	client := *p.client()
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &client
}

func (p Provider) login(ctx context.Context, payload operationPayload, email, password string) (string, *protocol.ProviderError) {
	body, _ := json.Marshal(struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}{Email: email, Password: password})
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, payload.StateAPI+"/login", bytes.NewReader(body))
	if err != nil {
		return "", providerError(protocol.ErrorProviderInternal, "build SDKMAN State API login request", false)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json")
	response, err := p.noRedirectClient().Do(request)
	if err != nil {
		if errors.Is(ctx.Err(), context.Canceled) {
			return "", providerError(protocol.ErrorCancelled, "SDKMAN State API login was cancelled", false)
		}
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return "", providerError(protocol.ErrorTimeout, "SDKMAN State API login timed out", true)
		}
		return "", providerError(protocol.ErrorTransientExternal, "SDKMAN State API login is unavailable", true)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 64<<10))
		switch response.StatusCode {
		case http.StatusUnauthorized, http.StatusForbidden:
			return "", providerError(protocol.ErrorAuthentication, "SDKMAN State API rejected vendor login", false)
		case http.StatusTooManyRequests, http.StatusRequestTimeout:
			return "", providerError(protocol.ErrorTransientExternal, "SDKMAN State API login is rate limited", true)
		default:
			if response.StatusCode >= 500 {
				return "", providerError(protocol.ErrorTransientExternal, "SDKMAN State API login failed temporarily", true)
			}
			return "", providerError(protocol.ErrorPermanentExternal, fmt.Sprintf("SDKMAN State API login failed with HTTP %d", response.StatusCode), false)
		}
	}
	var credentials struct {
		Token string `json:"token"`
	}
	decoder := json.NewDecoder(io.LimitReader(response.Body, 64<<10))
	if err := decoder.Decode(&credentials); err != nil || !validBearer(credentials.Token) {
		return "", providerError(protocol.ErrorPermanentExternal, "SDKMAN State API returned an invalid login response", false)
	}
	var remaining any
	if err := decoder.Decode(&remaining); !errors.Is(err, io.EOF) {
		return "", providerError(protocol.ErrorPermanentExternal, "SDKMAN State API returned an invalid login response", false)
	}
	return credentials.Token, nil
}

func validBearer(token string) bool {
	if token == "" || len(token) > 16384 {
		return false
	}
	for _, ch := range token {
		if ch <= 0x20 || ch >= 0x7f {
			return false
		}
	}
	return true
}

func (p Provider) publishState(ctx context.Context, payload operationPayload, token string) (int, *protocol.ProviderError) {
	body, _ := json.Marshal(statePublishRequest{
		Candidate: payload.Candidate, Version: payload.Version, Platform: payload.StatePlatform,
		URL: payload.URL, Distribution: payload.StateDistribution,
		MD5: payload.Checksums["MD5"], SHA256: payload.Checksums["SHA-256"], SHA512: payload.Checksums["SHA-512"],
	})
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, payload.StateAPI+"/versions", bytes.NewReader(body))
	if err != nil {
		return 0, providerError(protocol.ErrorProviderInternal, "build SDKMAN State API publish request", false)
	}
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json")
	response, err := p.noRedirectClient().Do(request)
	if err != nil {
		return 0, providerError(protocol.ErrorAmbiguousOutcome, "SDKMAN State API publication outcome is unknown; reconcile before retry", false)
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 64<<10))
	switch response.StatusCode {
	case http.StatusNoContent:
		return response.StatusCode, nil
	case http.StatusConflict:
		return response.StatusCode, nil
	case http.StatusUnauthorized:
		return 0, providerError(protocol.ErrorAuthentication, "SDKMAN State API rejected publication token", false)
	case http.StatusForbidden:
		return 0, providerError(protocol.ErrorAuthorization, "SDKMAN State API rejected publication authorization", false)
	case http.StatusBadRequest, http.StatusUnprocessableEntity:
		return 0, providerError(protocol.ErrorRejected, "SDKMAN State API rejected the version", false)
	default:
		if response.StatusCode == http.StatusRequestTimeout || response.StatusCode == http.StatusTooManyRequests ||
			response.StatusCode >= 500 || (response.StatusCode >= 200 && response.StatusCode < 300) {
			return 0, providerError(protocol.ErrorAmbiguousOutcome, "SDKMAN State API publication outcome is unknown; reconcile before retry", false)
		}
		return 0, providerError(protocol.ErrorPermanentExternal, fmt.Sprintf("SDKMAN State API failed with HTTP %d", response.StatusCode), false)
	}
}

func (p Provider) publish(ctx context.Context, payload operationPayload, key, token string) (int, *protocol.ProviderError) {
	body, _ := json.Marshal(releaseRequest{
		Candidate: payload.Candidate, Version: payload.Version, URL: payload.URL, Vendor: payload.Vendor,
		Platform: optionalPlatform(payload.Platform), Checksums: payload.Checksums,
	})
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, payload.API+"/release", bytes.NewReader(body))
	if err != nil {
		return 0, providerError(protocol.ErrorProviderInternal, "build SDKMAN release request", false)
	}
	request.Header.Set("Consumer-Key", key)
	request.Header.Set("Consumer-Token", token)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json")
	response, err := p.client().Do(request)
	if err != nil {
		return 0, providerError(protocol.ErrorAmbiguousOutcome, "SDKMAN release outcome is unknown; reconcile before retry", false)
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 64<<10))
	if response.StatusCode >= 200 && response.StatusCode < 300 {
		return response.StatusCode, nil
	}
	switch response.StatusCode {
	case http.StatusConflict:
		return response.StatusCode, nil
	case http.StatusUnauthorized:
		return 0, providerError(protocol.ErrorAuthentication, "SDKMAN vendor API rejected authentication", false)
	case http.StatusForbidden:
		return 0, providerError(protocol.ErrorAuthorization, "SDKMAN vendor API rejected authorization", false)
	case http.StatusBadRequest, http.StatusUnprocessableEntity:
		return 0, providerError(protocol.ErrorRejected, "SDKMAN vendor API rejected the release", false)
	case http.StatusRequestTimeout, http.StatusTooManyRequests:
		return 0, providerError(protocol.ErrorAmbiguousOutcome, "SDKMAN release outcome is unknown; reconcile before retry", false)
	default:
		if response.StatusCode >= 500 {
			return 0, providerError(protocol.ErrorAmbiguousOutcome, "SDKMAN release outcome is unknown; reconcile before retry", false)
		}
		return 0, providerError(protocol.ErrorPermanentExternal, fmt.Sprintf("SDKMAN vendor API failed with HTTP %d", response.StatusCode), false)
	}
}

func (p Provider) lookup(ctx context.Context, payload operationPayload) (observedVersion, *protocol.ProviderError) {
	endpoint := payload.StateAPI + "/versions/" + url.PathEscape(payload.Candidate) + "/" + url.PathEscape(payload.Version)
	query := url.Values{"platform": []string{payload.StatePlatform}}
	if payload.StateDistribution != "" {
		query.Set("distribution", payload.StateDistribution)
	}
	endpoint += "?" + query.Encode()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return observedVersion{}, providerError(protocol.ErrorProviderInternal, "build SDKMAN state request", false)
	}
	request.Header.Set("Accept", "application/json")
	response, err := p.client().Do(request)
	if err != nil {
		if errors.Is(ctx.Err(), context.Canceled) {
			return observedVersion{}, providerError(protocol.ErrorCancelled, "SDKMAN state lookup was cancelled", false)
		}
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return observedVersion{}, providerError(protocol.ErrorTimeout, "SDKMAN state lookup timed out", true)
		}
		return observedVersion{}, providerError(protocol.ErrorTransientExternal, "SDKMAN state API is unavailable", true)
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusNotFound {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 64<<10))
		return observedVersion{}, nil
	}
	if response.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 64<<10))
		return observedVersion{}, stateStatusError(response.StatusCode)
	}
	var state stateVersion
	decoder := json.NewDecoder(io.LimitReader(response.Body, 1<<20))
	if err := decoder.Decode(&state); err != nil {
		return observedVersion{}, providerError(protocol.ErrorPermanentExternal, "SDKMAN state API returned malformed version metadata", false)
	}
	if state.Candidate != payload.Candidate || state.Version != payload.Version || strings.TrimSpace(state.URL) == "" ||
		(payload.Mode == AuthState && state.Platform != payload.StatePlatform) {
		return observedVersion{}, providerError(protocol.ErrorPermanentExternal, "SDKMAN state API returned inconsistent version metadata", false)
	}
	return observedVersion{
		exists: true, candidate: state.Candidate, version: state.Version, url: state.URL,
		sha256: strings.ToLower(state.SHA256), distribution: state.Distribution,
	}, nil
}

func stateStatusError(status int) *protocol.ProviderError {
	switch status {
	case http.StatusTooManyRequests, http.StatusRequestTimeout:
		return providerError(protocol.ErrorTransientExternal, "SDKMAN state API is temporarily unavailable", true)
	default:
		if status >= 500 {
			return providerError(protocol.ErrorTransientExternal, "SDKMAN state API failed temporarily", true)
		}
		return providerError(protocol.ErrorPermanentExternal, fmt.Sprintf("SDKMAN state API failed with HTTP %d", status), false)
	}
}

func optionalPlatform(platform string) string {
	if platform == "UNIVERSAL" {
		return ""
	}
	return platform
}

func acceptedResult(payload operationPayload) protocol.DistributionResult {
	return distributionResult(protocol.ResultPublished, "accepted", payload, observedVersion{}, "vendor-api-accepted", "SDKMAN vendor API accepted the release; public state was not observed during apply")
}

func absentResult(payload operationPayload) protocol.DistributionResult {
	return distributionResult(protocol.ResultWaitingExternal, "absent", payload, observedVersion{}, "state-absent", "SDKMAN public state does not currently expose the planned release")
}

func resultForObserved(payload operationPayload, observed observedVersion) protocol.DistributionResult {
	expectedSHA := payload.Checksums["SHA-256"]
	if observed.url != payload.URL || (observed.sha256 != "" && observed.sha256 != expectedSHA) ||
		(payload.StateDistribution != "" && observed.distribution != payload.StateDistribution) ||
		(payload.Mode == AuthState && payload.StateDistribution == "" && observed.distribution != "") {
		return distributionResult(protocol.ResultRejected, "conflict", payload, observed, "state-conflict", "SDKMAN state exposes the same release coordinate with different publication identity")
	}
	limitation := ""
	verification := "state-url-and-sha256"
	if observed.sha256 == "" {
		verification = "state-url-only"
		limitation = "SDKMAN state did not expose SHA-256; publication identity is verified by candidate, version, platform query, and download URL only"
	}
	if payload.Vendor != "" && payload.StateDistribution == "" {
		if limitation != "" {
			limitation += "; "
		}
		limitation += "stateDistribution was not configured for a vendor-specific release, so state observation may not distinguish vendor distributions"
	}
	return distributionResult(protocol.ResultPublished, "observed", payload, observed, verification, limitation)
}

func distributionResult(state protocol.ResultState, providerState string, payload operationPayload, observed observedVersion, verification, limitation string) protocol.DistributionResult {
	value := evidence{
		Candidate: payload.Candidate, Version: payload.Version, Vendor: payload.Vendor, Platform: payload.Platform,
		DownloadURL: payload.URL, SHA256: payload.Checksums["SHA-256"], PublicationState: providerState,
		Verification: verification, StateEndpoint: payload.StateAPI, ObservedURL: observed.url,
		ObservedSHA256: observed.sha256, ObservedDistribution: observed.distribution, Limitation: limitation,
	}
	encoded, _ := json.Marshal(value)
	return protocol.DistributionResult{State: state, ProviderState: providerState, Evidence: encoded}
}
