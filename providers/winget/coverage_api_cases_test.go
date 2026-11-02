package winget

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/aalsanie/distroplane/internal/protocol"
)

func TestGitHubValidationResponseMatrix(t *testing.T) {
	for _, tc := range []struct {
		name   string
		body   string
		status int
		want   string
		error  bool
	}{
		{"none", `{"check_runs":[]}`, 200, "pending", false},
		{"success", `{"check_runs":[{"name":"package","status":"completed","conclusion":"success"}]}`, 200, "passed", false},
		{"neutral", `{"check_runs":[{"name":"package","status":"completed","conclusion":"neutral"}]}`, 200, "passed", false},
		{"skipped", `{"check_runs":[{"name":"package","status":"completed","conclusion":"skipped"}]}`, 200, "passed", false},
		{"failed", `{"check_runs":[{"name":"package","status":"completed","conclusion":"failure"}]}`, 200, "failed", false},
		{"cancelled", `{"check_runs":[{"name":"package","status":"completed","conclusion":"cancelled"}]}`, 200, "failed", false},
		{"timed-out", `{"check_runs":[{"name":"package","status":"completed","conclusion":"timed_out"}]}`, 200, "failed", false},
		{"action", `{"check_runs":[{"name":"package","status":"completed","conclusion":"action_required"}]}`, 200, "failed", false},
		{"stale", `{"check_runs":[{"name":"package","status":"completed","conclusion":"stale"}]}`, 200, "failed", false},
		{"in-progress", `{"check_runs":[{"name":"package","status":"in_progress","conclusion":null}]}`, 200, "pending", false},
		{"unknown", `{"check_runs":[{"name":"package","status":"completed","conclusion":"future"}]}`, 200, "pending", false},
		{"mixture", `{"check_runs":[{"name":"ok","status":"completed","conclusion":"success"},{"name":"not-yet","status":"in_progress","conclusion":null}]}`, 200, "pending", false},
		{"bad-json", "{", 200, "", true},
		{"forbidden", "denied", http.StatusForbidden, "", true},
		{"rate", "denied", http.StatusTooManyRequests, "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if !strings.Contains(r.URL.Path, "/check-runs") {
					t.Errorf("wrong validation endpoint: %s", r.URL)
				}
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer server.Close()
			p := Provider{Client: server.Client()}
			payload := operationPayload{PullRequest: pullRequestConfig{API: server.URL, Repository: "owner/repo"}}
			result, err := p.lookupValidation(context.Background(), payload, pullRequestObservation{headSHA: "abc123"}, "token")
			if (err != nil) != tc.error || result.state != tc.want {
				t.Fatalf("result=%+v error=%v", result, err)
			}
			if tc.name == "failed" && result.failedCheck != "package" {
				t.Fatalf("failed check omitted: %+v", result)
			}
		})
	}
}

func TestGitHubPullRequestLookupFiltersAndIdempotentConflict(t *testing.T) {
	for _, tc := range []struct {
		name   string
		body   string
		state  string
		exists bool
	}{
		{"empty", `[]`, "", false},
		{"mismatched-head", `[{"number":1,"html_url":"https://example.test/1","head":{"ref":"other"},"base":{"ref":"main"}}]`, "", false},
		{"mismatched-base", `[{"number":1,"html_url":"https://example.test/1","head":{"ref":"update"},"base":{"ref":"other"}}]`, "", false},
		{"open", `[{"number":3,"html_url":"https://example.test/3","state":"open","head":{"ref":"update","sha":"commit"},"base":{"ref":"main"}}]`, "open", true},
		{"merged", `[{"number":3,"html_url":"https://example.test/3","state":"closed","merged_at":"2026-11-01T00:00:00Z","head":{"ref":"update"},"base":{"ref":"main"}}]`, "merged", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			posts := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodPost {
					posts++
					w.WriteHeader(http.StatusUnprocessableEntity)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(tc.body))
			}))
			defer server.Close()
			p := Provider{Client: server.Client()}
			payload := operationPayload{Branch: "main", UpdateBranch: "update",
				PullRequest: pullRequestConfig{API: server.URL, Repository: "owner/repo", HeadOwner: "owner"}}
			got, err := p.lookupPullRequest(context.Background(), payload, "token")
			if err != nil || got.exists != tc.exists || got.state != tc.state {
				t.Fatalf("lookup %+v err=%v", got, err)
			}
			if tc.exists {
				retried, err := p.submitPullRequest(context.Background(), payload, "token")
				if err != nil || !retried.exists || retried.number != got.number || posts != 1 {
					t.Fatalf("recovered 422: %+v err=%v posts=%d", retried, err, posts)
				}
			}
		})
	}
}

func TestPriorPullRequestEvidenceBindingDecisionMatrix(t *testing.T) {
	payload := operationPayload{Branch: "main", UpdateBranch: "update", TreeSHA256: strings.Repeat("a", 64),
		PullRequest: pullRequestConfig{Repository: "owner/repo"}}
	valid := evidence{Repository: "owner/repo", BaseBranch: "main", UpdateBranch: "update",
		ManifestTreeSHA: strings.Repeat("a", 64), PullRequestNumber: 17}
	b, _ := json.Marshal(valid)
	if n, ok := priorPullRequestNumber(payload, &protocol.DistributionResult{Evidence: b}); !ok || n != 17 {
		t.Fatalf("valid prior PR: %d %v", n, ok)
	}
	if _, ok := priorPullRequestNumber(payload, nil); ok {
		t.Fatal("missing prior evidence accepted")
	}
	if _, ok := priorPullRequestNumber(payload, &protocol.DistributionResult{Evidence: []byte("{")}); ok {
		t.Fatal("invalid prior evidence accepted")
	}
	for _, mutate := range []func(*evidence){
		func(e *evidence) { e.PullRequestNumber = 0 },
		func(e *evidence) { e.Repository = "other/repo" },
		func(e *evidence) { e.BaseBranch = "wrong" },
		func(e *evidence) { e.UpdateBranch = "wrong" },
		func(e *evidence) { e.ManifestTreeSHA = "wrong" },
	} {
		change := valid
		mutate(&change)
		raw, _ := json.Marshal(change)
		if _, ok := priorPullRequestNumber(payload, &protocol.DistributionResult{Evidence: raw}); ok {
			t.Fatalf("unbound prior evidence accepted: %+v", change)
		}
	}
}
