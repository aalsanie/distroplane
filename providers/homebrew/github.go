package homebrew

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
	exists bool
	number int64
	url    string
	state  string
}

type githubPullRequest struct {
	Number   int64   `json:"number"`
	HTMLURL  string  `json:"html_url"`
	State    string  `json:"state"`
	MergedAt *string `json:"merged_at"`
	Head     struct {
		Ref string `json:"ref"`
	} `json:"head"`
	Base struct {
		Ref string `json:"ref"`
	} `json:"base"`
}

func (p Provider) lookupPullRequest(ctx context.Context, payload operationPayload, token string) (pullRequestObservation, *protocol.ProviderError) {
	if payload.Mode != "pull-request" {
		return pullRequestObservation{}, nil
	}
	owner, _, ok := strings.Cut(payload.PullRequest.Repository, "/")
	if !ok {
		return pullRequestObservation{}, providerError(protocol.ErrorConfiguration, "pull request repository is invalid", false)
	}
	endpoint := pullRequestEndpoint(payload.PullRequest) + "?" + url.Values{
		"state": []string{"all"},
		"head":  []string{owner + ":" + payload.UpdateBranch},
		"base":  []string{payload.Branch},
	}.Encode()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return pullRequestObservation{}, providerError(protocol.ErrorProviderInternal, "build Homebrew pull request lookup", false)
	}
	setGitHubHeaders(request, token)
	response, err := p.client().Do(request)
	if err != nil {
		return pullRequestObservation{}, classifyPullRequestReadError(ctx, err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 64<<10))
		return pullRequestObservation{}, classifyPullRequestStatus(response.StatusCode, false)
	}
	var values []githubPullRequest
	decoder := json.NewDecoder(io.LimitReader(response.Body, 1<<20))
	if err := decoder.Decode(&values); err != nil {
		return pullRequestObservation{}, providerError(protocol.ErrorPermanentExternal, "pull request API returned malformed response", false)
	}
	for _, value := range values {
		if value.Head.Ref != "" && value.Head.Ref != payload.UpdateBranch {
			continue
		}
		if value.Base.Ref != "" && value.Base.Ref != payload.Branch {
			continue
		}
		state := value.State
		if value.MergedAt != nil {
			state = "merged"
		}
		return pullRequestObservation{exists: true, number: value.Number, url: value.HTMLURL, state: state}, nil
	}
	return pullRequestObservation{}, nil
}

func (p Provider) submitPullRequest(ctx context.Context, payload operationPayload, token string) (pullRequestObservation, *protocol.ProviderError) {
	title := payload.PullRequest.Title
	if title == "" {
		title = "Update " + payload.Path
	}
	body, _ := json.Marshal(map[string]string{
		"title": title,
		"head":  payload.UpdateBranch,
		"base":  payload.Branch,
		"body":  payload.PullRequest.Body,
	})
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, pullRequestEndpoint(payload.PullRequest), bytes.NewReader(body))
	if err != nil {
		return pullRequestObservation{}, providerError(protocol.ErrorProviderInternal, "build Homebrew pull request submission", false)
	}
	setGitHubHeaders(request, token)
	request.Header.Set("Content-Type", "application/json")
	response, err := p.client().Do(request)
	if err != nil {
		if errors.Is(ctx.Err(), context.Canceled) {
			return pullRequestObservation{}, providerError(protocol.ErrorCancelled, "pull request submission was cancelled", false)
		}
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return pullRequestObservation{}, providerError(protocol.ErrorAmbiguousOutcome, "pull request submission outcome is unknown; reconcile before retry", false)
		}
		return pullRequestObservation{}, providerError(protocol.ErrorAmbiguousOutcome, "pull request submission outcome is unknown; reconcile before retry", false)
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
		return pullRequestObservation{}, providerError(protocol.ErrorRejected, "pull request submission was rejected", false)
	}
	return pullRequestObservation{}, classifyPullRequestStatus(response.StatusCode, true)
}

func decodePullRequest(reader io.Reader) (pullRequestObservation, *protocol.ProviderError) {
	var value githubPullRequest
	decoder := json.NewDecoder(io.LimitReader(reader, 1<<20))
	if err := decoder.Decode(&value); err != nil || value.Number <= 0 || strings.TrimSpace(value.HTMLURL) == "" {
		return pullRequestObservation{}, providerError(protocol.ErrorPermanentExternal, "pull request API returned malformed response", false)
	}
	state := value.State
	if state == "" {
		state = "open"
	}
	return pullRequestObservation{exists: true, number: value.Number, url: value.HTMLURL, state: state}, nil
}

func pullRequestEndpoint(cfg pullRequestConfig) string {
	owner, name, _ := strings.Cut(cfg.Repository, "/")
	return cfg.API + "/repos/" + url.PathEscape(owner) + "/" + url.PathEscape(name) + "/pulls"
}

func setGitHubHeaders(request *http.Request, token string) {
	request.Header.Set("Accept", "application/vnd.github+json")
	request.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
}

func classifyPullRequestReadError(ctx context.Context, err error) *protocol.ProviderError {
	if errors.Is(ctx.Err(), context.Canceled) {
		return providerError(protocol.ErrorCancelled, "pull request lookup was cancelled", false)
	}
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return providerError(protocol.ErrorTimeout, "pull request lookup timed out", true)
	}
	_ = err
	return providerError(protocol.ErrorTransientExternal, "pull request API is unavailable", true)
}

func classifyPullRequestStatus(status int, write bool) *protocol.ProviderError {
	switch status {
	case http.StatusUnauthorized:
		return providerError(protocol.ErrorAuthentication, "pull request API rejected authentication", false)
	case http.StatusForbidden:
		return providerError(protocol.ErrorAuthorization, "pull request API rejected authorization", false)
	case http.StatusTooManyRequests, http.StatusRequestTimeout:
		if write {
			return providerError(protocol.ErrorAmbiguousOutcome, "pull request submission outcome is unknown; reconcile before retry", false)
		}
		return providerError(protocol.ErrorTransientExternal, "pull request API is temporarily unavailable", true)
	default:
		if status >= 500 {
			if write {
				return providerError(protocol.ErrorAmbiguousOutcome, "pull request submission outcome is unknown; reconcile before retry", false)
			}
			return providerError(protocol.ErrorTransientExternal, "pull request API is temporarily unavailable", true)
		}
		return providerError(protocol.ErrorPermanentExternal, fmt.Sprintf("pull request API failed with HTTP %d", status), false)
	}
}
