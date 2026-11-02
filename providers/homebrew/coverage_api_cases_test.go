package homebrew

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHomebrewPullRequestLookupIdentityAndMergeMatrix(t *testing.T) {
	for _, tc := range []struct {
		name   string
		body   string
		exists bool
		state  string
	}{
		{"empty", `[]`, false, ""},
		{"wrong-head", `[{"number":1,"html_url":"https://example.test/1","head":{"ref":"wrong"},"base":{"ref":"main"}}]`, false, ""},
		{"wrong-base", `[{"number":1,"html_url":"https://example.test/1","head":{"ref":"update"},"base":{"ref":"wrong"}}]`, false, ""},
		{"open", `[{"number":1,"html_url":"https://example.test/1","state":"open","head":{"ref":"update"},"base":{"ref":"main"}}]`, true, "open"},
		{"merged", `[{"number":1,"html_url":"https://example.test/1","state":"closed","merged_at":"2026-11-01T00:00:00Z","head":{"ref":"update"},"base":{"ref":"main"}}]`, true, "merged"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if !strings.Contains(r.URL.Path, "/pulls") {
					t.Errorf("unexpected path: %s", r.URL)
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(tc.body))
			}))
			defer srv.Close()
			payload := operationPayload{Mode: "pull-request", Branch: "main", UpdateBranch: "update",
				PullRequest: pullRequestConfig{API: srv.URL, Repository: "owner/repo"}}
			got, err := (Provider{Client: srv.Client()}).lookupPullRequest(context.Background(), payload, "token")
			if err != nil || got.exists != tc.exists || got.state != tc.state {
				t.Fatalf("lookup %+v error=%v", got, err)
			}
		})
	}
}

func TestHomebrewPullRequestRecoverRejectedDuplicate(t *testing.T) {
	for _, lookup := range []struct {
		name, body string
		recovered  bool
	}{
		{"existing", `[{"number":4,"html_url":"https://example.test/pr/4","state":"open","head":{"ref":"update"},"base":{"ref":"main"}}]`, true},
		{"absent", `[]`, false},
		{"bad-response", `{`, false},
	} {
		t.Run(lookup.name, func(t *testing.T) {
			posts := 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodPost {
					posts++
					w.WriteHeader(http.StatusUnprocessableEntity)
					return
				}
				_, _ = w.Write([]byte(lookup.body))
			}))
			defer srv.Close()
			payload := operationPayload{Mode: "pull-request", Branch: "main", UpdateBranch: "update",
				Path: "Formula/Example.rb", PullRequest: pullRequestConfig{API: srv.URL, Repository: "owner/repo"}}
			got, err := (Provider{Client: srv.Client()}).submitPullRequest(context.Background(), payload, "token")
			if (err == nil) != lookup.recovered || got.exists != lookup.recovered || posts != 1 {
				t.Fatalf("recovered=%+v err=%v posts=%d", got, err, posts)
			}
		})
	}
}

func TestHomebrewMalformedPRAndMissingRepositoryValidation(t *testing.T) {
	p := Provider{}
	if _, err := p.lookupPullRequest(context.Background(), operationPayload{Mode: "pull-request", PullRequest: pullRequestConfig{Repository: "bad"}}, ""); err == nil {
		t.Fatal("malformed repository accepted")
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("{"))
	}))
	defer srv.Close()
	payload := operationPayload{Mode: "pull-request", PullRequest: pullRequestConfig{Repository: "owner/repo", API: srv.URL}}
	if _, err := (Provider{Client: srv.Client()}).lookupPullRequest(context.Background(), payload, ""); err == nil {
		t.Fatal("malformed PR list accepted")
	}
	for _, raw := range []string{`{}`, `{"number":0,"html_url":"https://example.test"}`, `{"number":1}`, `{`} {
		if got, err := decodePullRequest(strings.NewReader(raw)); err == nil {
			t.Fatalf("invalid PR accepted: %+v", got)
		}
	}
	for _, raw := range []string{
		`{"number":1,"html_url":"https://example.test/pr"}`,
		`{"number":1,"html_url":"https://example.test/pr","state":"closed"}`,
	} {
		if got, err := decodePullRequest(strings.NewReader(raw)); err != nil || !got.exists {
			t.Fatalf("valid PR rejected: %+v err=%v", got, err)
		}
	}
}
