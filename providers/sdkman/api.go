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
	if state.Candidate != payload.Candidate || state.Version != payload.Version || strings.TrimSpace(state.URL) == "" {
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
	if observed.url != payload.URL || (observed.sha256 != "" && observed.sha256 != expectedSHA) || (payload.StateDistribution != "" && observed.distribution != "" && observed.distribution != payload.StateDistribution) {
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
