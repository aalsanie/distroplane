package npm

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
	exists    bool
	integrity string
	shasum    string
}

type packument struct {
	Versions map[string]struct {
		Dist manifestDist `json:"dist"`
	} `json:"versions"`
}

type oidcExchange struct {
	Token string `json:"token"`
}

func (p Provider) authenticationToken(ctx context.Context, payload operationPayload) (string, *protocol.ProviderError) {
	environment := credentialEnvironment(payload.Authentication)
	credential := strings.TrimSpace(p.getenv(environment))
	if credential == "" {
		return "", providerError(protocol.ErrorAuthentication, "required npm credential is unavailable", false)
	}
	if payload.Authentication == "token" {
		return credential, nil
	}
	return p.exchangeOIDC(ctx, payload, credential)
}

func (p Provider) exchangeOIDC(ctx context.Context, payload operationPayload, idToken string) (string, *protocol.ProviderError) {
	endpoint := payload.Registry + "-/npm/v1/oidc/token/exchange/package/" + escapedPackageName(payload.Package)
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, nil)
	if err != nil {
		return "", providerError(protocol.ErrorProviderInternal, "build npm OIDC exchange request", false)
	}
	request.Header.Set("Authorization", "Bearer "+idToken)
	request.Header.Set("Accept", "application/json")
	response, err := p.client().Do(request)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(ctx.Err(), context.Canceled) {
			return "", providerError(protocol.ErrorCancelled, "npm OIDC exchange was cancelled", false)
		}
		return "", providerError(protocol.ErrorTransientExternal, "npm OIDC exchange failed", true)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusCreated {
		return "", statusError("npm OIDC exchange", response.StatusCode)
	}
	var value oidcExchange
	decoder := json.NewDecoder(io.LimitReader(response.Body, 64<<10))
	if err := decoder.Decode(&value); err != nil || strings.TrimSpace(value.Token) == "" {
		return "", providerError(protocol.ErrorPermanentExternal, "npm OIDC exchange returned an invalid response", false)
	}
	return value.Token, nil
}

func (p Provider) lookup(ctx context.Context, payload operationPayload, token string) (observedVersion, *protocol.ProviderError) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, packageURL(payload.Registry, payload.Package), nil)
	if err != nil {
		return observedVersion{}, providerError(protocol.ErrorProviderInternal, "build npm registry lookup request", false)
	}
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Accept", "application/json")
	response, err := p.client().Do(request)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(ctx.Err(), context.Canceled) {
			return observedVersion{}, providerError(protocol.ErrorCancelled, "npm registry lookup was cancelled", false)
		}
		return observedVersion{}, providerError(protocol.ErrorTransientExternal, "npm registry is unavailable", true)
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusNotFound {
		return observedVersion{}, nil
	}
	if response.StatusCode != http.StatusOK {
		return observedVersion{}, statusError("npm registry lookup", response.StatusCode)
	}
	var document packument
	decoder := json.NewDecoder(io.LimitReader(response.Body, 4<<20))
	if err := decoder.Decode(&document); err != nil {
		return observedVersion{}, providerError(protocol.ErrorPermanentExternal, "npm registry returned malformed package metadata", false)
	}
	version, exists := document.Versions[payload.Version]
	if !exists {
		return observedVersion{}, nil
	}
	return observedVersion{exists: true, integrity: version.Dist.Integrity, shasum: version.Dist.Shasum}, nil
}

func (p Provider) publish(ctx context.Context, payload operationPayload, packageData packageArchive, token string) (observedVersion, *protocol.ProviderError) {
	body := packageData.publishBody(payload)
	request, err := http.NewRequestWithContext(ctx, http.MethodPut, packageURL(payload.Registry, payload.Package), bytes.NewReader(body))
	if err != nil {
		return observedVersion{}, providerError(protocol.ErrorProviderInternal, "build npm publish request", false)
	}
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json")
	response, err := p.client().Do(request)
	if err != nil {
		return observedVersion{}, providerError(protocol.ErrorAmbiguousOutcome, "npm publish outcome is unknown; reconcile before retry", false)
	}
	defer response.Body.Close()
	switch response.StatusCode {
	case http.StatusOK, http.StatusCreated:
		return observedVersion{exists: true, integrity: packageData.integrity, shasum: packageData.shasum}, nil
	case http.StatusConflict:
		observed, reconcileErr := p.lookup(ctx, payload, token)
		if reconcileErr != nil || !observed.exists {
			return observedVersion{}, providerError(protocol.ErrorAmbiguousOutcome, "npm publish conflicted and could not be reconciled", false)
		}
		return observed, nil
	case http.StatusUnauthorized:
		return observedVersion{}, providerError(protocol.ErrorAuthentication, "npm registry rejected authentication", false)
	case http.StatusForbidden:
		return observedVersion{}, providerError(protocol.ErrorAuthorization, "npm registry rejected publication authorization", false)
	default:
		if response.StatusCode >= 500 || response.StatusCode == http.StatusRequestTimeout || response.StatusCode == http.StatusTooManyRequests {
			return observedVersion{}, providerError(protocol.ErrorAmbiguousOutcome, "npm publish outcome is unknown; reconcile before retry", false)
		}
		return observedVersion{}, providerError(protocol.ErrorPermanentExternal, fmt.Sprintf("npm registry rejected publication with HTTP %d", response.StatusCode), false)
	}
}

func resultForObserved(payload operationPayload, packageData packageArchive, observed observedVersion) protocol.DistributionResult {
	if observed.integrity == packageData.integrity && observed.shasum == packageData.shasum {
		return distributionResult(protocol.ResultPublished, "published", payload, packageData, observed)
	}
	return distributionResult(protocol.ResultRejected, "conflict", payload, packageData, observed)
}

func distributionResult(state protocol.ResultState, providerState string, payload operationPayload, packageData packageArchive, observed observedVersion) protocol.DistributionResult {
	publicationState := providerState
	value := evidence{
		Package: payload.Package, Version: payload.Version, Registry: payload.Registry,
		PackageURL: packageURL(payload.Registry, payload.Package), Integrity: packageData.integrity, Shasum: packageData.shasum,
		SHA256: packageData.sha256, PublicationState: publicationState,
		ObservedIntegrity: observed.integrity, ObservedShasum: observed.shasum,
	}
	encoded, _ := json.Marshal(value)
	return protocol.DistributionResult{State: state, ProviderState: providerState, Evidence: encoded}
}

func statusError(operation string, status int) *protocol.ProviderError {
	switch status {
	case http.StatusUnauthorized:
		return providerError(protocol.ErrorAuthentication, operation+" authentication failed", false)
	case http.StatusForbidden:
		return providerError(protocol.ErrorAuthorization, operation+" authorization failed", false)
	case http.StatusTooManyRequests, http.StatusRequestTimeout:
		return providerError(protocol.ErrorTransientExternal, operation+" was temporarily unavailable", true)
	default:
		if status >= 500 {
			return providerError(protocol.ErrorTransientExternal, operation+" failed temporarily", true)
		}
		return providerError(protocol.ErrorPermanentExternal, fmt.Sprintf("%s failed with HTTP %d", operation, status), false)
	}
}

func escapedPackageName(name string) string {
	return strings.ReplaceAll(url.PathEscape(name), "%40", "@")
}

func packageURL(registry, name string) string {
	return registry + escapedPackageName(name)
}

func tarballURL(registry, name, version string) string {
	return packageURL(registry, name) + "/-/" + packageBaseName(name) + "-" + version + ".tgz"
}
