package winget

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

type pullRequestObservation struct {
	exists  bool
	number  int64
	url     string
	state   string
	headSHA string
}

type githubPullRequest struct {
	Number   int64   `json:"number"`
	HTMLURL  string  `json:"html_url"`
	State    string  `json:"state"`
	MergedAt *string `json:"merged_at"`
	Head     struct {
		Ref string `json:"ref"`
		SHA string `json:"sha"`
	} `json:"head"`
	Base struct {
		Ref string `json:"ref"`
	} `json:"base"`
}

type validationObservation struct {
	state       string
	failedCheck string
}

type checkRunsResponse struct {
	CheckRuns []struct {
		Name       string  `json:"name"`
		Status     string  `json:"status"`
		Conclusion *string `json:"conclusion"`
	} `json:"check_runs"`
}


const maxDestinationManifestBytes = 1 << 20

type destinationObservation struct {
	state  string
	commit string
}

const (
	destinationExact    = "exact"
	destinationMissing  = "missing"
	destinationConflict = "conflict"
)

func (p Provider) observeDestination(ctx context.Context, payload operationPayload, token string) (destinationObservation, *protocol.ProviderError) {
	commit, providerErr := p.resolveDestinationCommit(ctx, payload, token)
	if providerErr != nil {
		return destinationObservation{}, providerErr
	}
	state := destinationExact
	for _, file := range payload.Files {
		content, exists, providerErr := p.readDestinationFile(ctx, payload, file.Path, commit, token)
		if providerErr != nil {
			return destinationObservation{}, providerErr
		}
		if !exists {
			if state != destinationConflict {
				state = destinationMissing
			}
			continue
		}
		if string(content) != file.Content {
			state = destinationConflict
		}
	}
	return destinationObservation{state: state, commit: commit}, nil
}

func (p Provider) resolveDestinationCommit(ctx context.Context, payload operationPayload, token string) (string, *protocol.ProviderError) {
	endpoint := repositoryEndpoint(payload.PullRequest) + "/commits/" + url.PathEscape(payload.Branch)
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return "", providerError(protocol.ErrorProviderInternal, "build WinGet destination commit lookup", false)
	}
	setGitHubHeaders(request, token)
	response, err := p.client().Do(request)
	if err != nil {
		return "", classifyGitHubReadError(ctx)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 64<<10))
		return "", classifyGitHubStatus(response.StatusCode, false)
	}
	var value struct {
		SHA string `json:"sha"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&value); err != nil || strings.TrimSpace(value.SHA) == "" {
		return "", providerError(protocol.ErrorPermanentExternal, "WinGet destination commit API returned malformed response", false)
	}
	return strings.TrimSpace(value.SHA), nil
}

func (p Provider) readDestinationFile(ctx context.Context, payload operationPayload, filePath, commit, token string) ([]byte, bool, *protocol.ProviderError) {
	endpoint := destinationContentEndpoint(payload.PullRequest, filePath, commit)
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, false, providerError(protocol.ErrorProviderInternal, "build WinGet destination manifest lookup", false)
	}
	setGitHubHeaders(request, token)
	request.Header.Set("Accept", "application/vnd.github.raw+json")
	response, err := p.client().Do(request)
	if err != nil {
		return nil, false, classifyGitHubReadError(ctx)
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusNotFound {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 64<<10))
		return nil, false, nil
	}
	if response.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 64<<10))
		return nil, false, classifyGitHubStatus(response.StatusCode, false)
	}
	content, err := io.ReadAll(io.LimitReader(response.Body, maxDestinationManifestBytes+1))
	if err != nil {
		return nil, false, classifyGitHubReadError(ctx)
	}
	if len(content) > maxDestinationManifestBytes {
		return nil, false, providerError(protocol.ErrorPermanentExternal, "WinGet destination manifest exceeds response limit", false)
	}
	return content, true, nil
}

func (p Provider) lookupPullRequest(ctx context.Context, payload operationPayload, token string) (pullRequestObservation, *protocol.ProviderError) {
	endpoint := pullRequestEndpoint(payload.PullRequest) + "?" + url.Values{
		"state": []string{"all"},
		"head":  []string{payload.PullRequest.HeadOwner + ":" + payload.UpdateBranch},
		"base":  []string{payload.Branch},
	}.Encode()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return pullRequestObservation{}, providerError(protocol.ErrorProviderInternal, "build WinGet pull request lookup", false)
	}
	setGitHubHeaders(request, token)
	response, err := p.client().Do(request)
	if err != nil {
		return pullRequestObservation{}, classifyGitHubReadError(ctx)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 64<<10))
		return pullRequestObservation{}, classifyGitHubStatus(response.StatusCode, false)
	}
	var values []githubPullRequest
	if err := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&values); err != nil {
		return pullRequestObservation{}, providerError(protocol.ErrorPermanentExternal, "WinGet pull request API returned malformed response", false)
	}
	for _, value := range values {
		if value.Head.Ref != "" && value.Head.Ref != payload.UpdateBranch {
			continue
		}
		if value.Base.Ref != "" && value.Base.Ref != payload.Branch {
			continue
		}
		return observePullRequest(value), nil
	}
	return pullRequestObservation{}, nil
}

func (p Provider) lookupPriorPullRequest(ctx context.Context, payload operationPayload, previous *protocol.DistributionResult, token string) (pullRequestObservation, *protocol.ProviderError) {
	number, ok := priorPullRequestNumber(payload, previous)
	if !ok {
		return pullRequestObservation{}, nil
	}
	endpoint := pullRequestEndpoint(payload.PullRequest) + "/" + fmt.Sprintf("%d", number)
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return pullRequestObservation{}, providerError(protocol.ErrorProviderInternal, "build WinGet prior pull request lookup", false)
	}
	setGitHubHeaders(request, token)
	response, err := p.client().Do(request)
	if err != nil {
		return pullRequestObservation{}, classifyGitHubReadError(ctx)
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusNotFound {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 64<<10))
		return pullRequestObservation{}, nil
	}
	if response.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 64<<10))
		return pullRequestObservation{}, classifyGitHubStatus(response.StatusCode, false)
	}
	var value githubPullRequest
	if err := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&value); err != nil || value.Number <= 0 || strings.TrimSpace(value.HTMLURL) == "" {
		return pullRequestObservation{}, providerError(protocol.ErrorPermanentExternal, "WinGet pull request API returned malformed response", false)
	}
	if value.Number != number || (value.Head.Ref != "" && value.Head.Ref != payload.UpdateBranch) || (value.Base.Ref != "" && value.Base.Ref != payload.Branch) {
		return pullRequestObservation{}, nil
	}
	return observePullRequest(value), nil
}

func priorPullRequestNumber(payload operationPayload, previous *protocol.DistributionResult) (int64, bool) {
	if previous == nil || len(previous.Evidence) == 0 {
		return 0, false
	}
	var prior evidence
	if err := json.Unmarshal(previous.Evidence, &prior); err != nil {
		return 0, false
	}
	if prior.PullRequestNumber <= 0 ||
		prior.Repository != payload.PullRequest.Repository ||
		prior.BaseBranch != payload.Branch ||
		prior.UpdateBranch != payload.UpdateBranch ||
		prior.ManifestTreeSHA != payload.TreeSHA256 {
		return 0, false
	}
	return prior.PullRequestNumber, true
}

func (p Provider) submitPullRequest(ctx context.Context, payload operationPayload, token string) (pullRequestObservation, *protocol.ProviderError) {
	head := payload.PullRequest.HeadOwner + ":" + payload.UpdateBranch
	body, _ := json.Marshal(map[string]string{
		"title": payload.PullRequest.Title,
		"head":  head,
		"base":  payload.Branch,
		"body":  payload.PullRequest.Body,
	})
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, pullRequestEndpoint(payload.PullRequest), bytes.NewReader(body))
	if err != nil {
		return pullRequestObservation{}, providerError(protocol.ErrorProviderInternal, "build WinGet pull request submission", false)
	}
	setGitHubHeaders(request, token)
	request.Header.Set("Content-Type", "application/json")
	response, err := p.client().Do(request)
	if err != nil {
		if errors.Is(ctx.Err(), context.Canceled) {
			return pullRequestObservation{}, providerError(protocol.ErrorCancelled, "WinGet pull request submission was cancelled", false)
		}
		return pullRequestObservation{}, providerError(protocol.ErrorAmbiguousOutcome, "WinGet pull request submission outcome is unknown; reconcile before retry", false)
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusCreated {
		return decodePullRequest(response.Body)
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 64<<10))
	if response.StatusCode == http.StatusUnprocessableEntity {
		existing, lookupErr := p.lookupPullRequest(ctx, payload, token)
		if lookupErr == nil && existing.exists {
			return existing, nil
		}
		return pullRequestObservation{}, providerError(protocol.ErrorRejected, "WinGet pull request submission was rejected", false)
	}
	return pullRequestObservation{}, classifyGitHubStatus(response.StatusCode, true)
}

func (p Provider) lookupValidation(ctx context.Context, payload operationPayload, pr pullRequestObservation, token string) (validationObservation, *protocol.ProviderError) {
	if strings.TrimSpace(pr.headSHA) == "" {
		return validationObservation{state: "pending"}, nil
	}
	endpoint := repositoryEndpoint(payload.PullRequest) + "/commits/" + url.PathEscape(pr.headSHA) + "/check-runs?per_page=100"
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return validationObservation{}, providerError(protocol.ErrorProviderInternal, "build WinGet validation lookup", false)
	}
	setGitHubHeaders(request, token)
	response, err := p.client().Do(request)
	if err != nil {
		return validationObservation{}, classifyGitHubReadError(ctx)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 64<<10))
		return validationObservation{}, classifyGitHubStatus(response.StatusCode, false)
	}
	var value checkRunsResponse
	if err := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&value); err != nil {
		return validationObservation{}, providerError(protocol.ErrorPermanentExternal, "WinGet validation API returned malformed response", false)
	}
	if len(value.CheckRuns) == 0 {
		return validationObservation{state: "pending"}, nil
	}
	pending := false
	for _, check := range value.CheckRuns {
		if check.Status != "completed" || check.Conclusion == nil {
			pending = true
			continue
		}
		switch *check.Conclusion {
		case "success", "neutral", "skipped":
		case "failure", "cancelled", "timed_out", "action_required", "stale":
			return validationObservation{state: "failed", failedCheck: check.Name}, nil
		default:
			pending = true
		}
	}
	if pending {
		return validationObservation{state: "pending"}, nil
	}
	return validationObservation{state: "passed"}, nil
}

func decodePullRequest(reader io.Reader) (pullRequestObservation, *protocol.ProviderError) {
	var value githubPullRequest
	if err := json.NewDecoder(io.LimitReader(reader, 1<<20)).Decode(&value); err != nil || value.Number <= 0 || strings.TrimSpace(value.HTMLURL) == "" {
		return pullRequestObservation{}, providerError(protocol.ErrorPermanentExternal, "WinGet pull request API returned malformed response", false)
	}
	return observePullRequest(value), nil
}

func observePullRequest(value githubPullRequest) pullRequestObservation {
	state := value.State
	if value.MergedAt != nil {
		state = "merged"
	}
	if state == "" {
		state = "open"
	}
	return pullRequestObservation{exists: true, number: value.Number, url: value.HTMLURL, state: state, headSHA: value.Head.SHA}
}

func pullRequestEndpoint(cfg pullRequestConfig) string {
	return repositoryEndpoint(cfg) + "/pulls"
}

func destinationContentEndpoint(cfg pullRequestConfig, filePath, commit string) string {
	parts := strings.Split(filePath, "/")
	for i := range parts {
		parts[i] = url.PathEscape(parts[i])
	}
	return repositoryEndpoint(cfg) + "/contents/" + strings.Join(parts, "/") + "?" + url.Values{"ref": []string{commit}}.Encode()
}

func repositoryEndpoint(cfg pullRequestConfig) string {
	owner, name, _ := strings.Cut(cfg.Repository, "/")
	return cfg.API + "/repos/" + url.PathEscape(owner) + "/" + url.PathEscape(name)
}

func setGitHubHeaders(request *http.Request, token string) {
	request.Header.Set("Accept", "application/vnd.github+json")
	request.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
}

func classifyGitHubReadError(ctx context.Context) *protocol.ProviderError {
	if errors.Is(ctx.Err(), context.Canceled) {
		return providerError(protocol.ErrorCancelled, "WinGet repository lookup was cancelled", false)
	}
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return providerError(protocol.ErrorTimeout, "WinGet repository lookup timed out", true)
	}
	return providerError(protocol.ErrorTransientExternal, "WinGet repository API is unavailable", true)
}

func classifyGitHubStatus(status int, write bool) *protocol.ProviderError {
	switch status {
	case http.StatusUnauthorized:
		return providerError(protocol.ErrorAuthentication, "WinGet repository API rejected authentication", false)
	case http.StatusForbidden:
		return providerError(protocol.ErrorAuthorization, "WinGet repository API rejected authorization", false)
	case http.StatusTooManyRequests, http.StatusRequestTimeout:
		if write {
			return providerError(protocol.ErrorAmbiguousOutcome, "WinGet submission outcome is unknown; reconcile before retry", false)
		}
		return providerError(protocol.ErrorTransientExternal, "WinGet repository API is temporarily unavailable", true)
	default:
		if status >= 500 {
			if write {
				return providerError(protocol.ErrorAmbiguousOutcome, "WinGet submission outcome is unknown; reconcile before retry", false)
			}
			return providerError(protocol.ErrorTransientExternal, "WinGet repository API is temporarily unavailable", true)
		}
		return providerError(protocol.ErrorPermanentExternal, fmt.Sprintf("WinGet repository API failed with HTTP %d", status), false)
	}
}
