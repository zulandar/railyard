package inspect

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/go-github/v68/github"
)

func newTestGitHubClient(t *testing.T, srv *httptest.Server) *GitHubClient {
	t.Helper()
	client := github.NewClient(nil)
	client, _ = client.WithEnterpriseURLs(srv.URL+"/", srv.URL+"/upload/")
	return &GitHubClient{
		client:             client,
		owner:              "testowner",
		repo:               "testrepo",
		rateLimitThreshold: 100,
	}
}

func TestListReviewablePRs_FiltersDrafts(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v3/repos/testowner/testrepo/pulls", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("expected GET, got %s", r.Method)
		}
		if r.URL.Query().Get("state") != "open" {
			t.Errorf("expected state=open, got %s", r.URL.Query().Get("state"))
		}
		prs := []*github.PullRequest{
			{
				Number: github.Ptr(1),
				Title:  github.Ptr("Real PR"),
				Draft:  github.Ptr(false),
			},
			{
				Number: github.Ptr(2),
				Title:  github.Ptr("Draft PR"),
				Draft:  github.Ptr(true),
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(prs)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	gc := newTestGitHubClient(t, srv)
	prs, err := gc.ListReviewablePRs(context.Background())
	if err != nil {
		t.Fatalf("ListReviewablePRs() error: %v", err)
	}
	if len(prs) != 1 {
		t.Fatalf("expected 1 PR, got %d", len(prs))
	}
	if prs[0].GetNumber() != 1 {
		t.Errorf("expected PR #1, got #%d", prs[0].GetNumber())
	}
}

func TestGetPRDiff(t *testing.T) {
	expectedDiff := "diff --git a/file.go b/file.go\n--- a/file.go\n+++ b/file.go\n@@ -1,3 +1,4 @@\n package main\n+// added\n"
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v3/repos/testowner/testrepo/pulls/42", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("expected GET, got %s", r.Method)
		}
		accept := r.Header.Get("Accept")
		if accept != "application/vnd.github.v3.diff" {
			t.Errorf("expected diff Accept header, got %s", accept)
		}
		w.Header().Set("Content-Type", "text/plain")
		w.Write([]byte(expectedDiff))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	gc := newTestGitHubClient(t, srv)
	diff, err := gc.GetPRDiff(context.Background(), 42)
	if err != nil {
		t.Fatalf("GetPRDiff() error: %v", err)
	}
	if diff != expectedDiff {
		t.Errorf("diff mismatch:\ngot:  %q\nwant: %q", diff, expectedDiff)
	}
}

func TestSubmitReview(t *testing.T) {
	var called bool
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v3/repos/testowner/testrepo/pulls/10/reviews", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		called = true

		var body map[string]interface{}
		json.NewDecoder(r.Body).Decode(&body)

		if body["event"] != "COMMENT" {
			t.Errorf("expected COMMENT event, got %v", body["event"])
		}
		if body["body"] == nil || body["body"] == "" {
			t.Errorf("expected non-empty body")
		}

		review := &github.PullRequestReview{
			ID: github.Ptr(int64(1)),
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(review)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	gc := newTestGitHubClient(t, srv)
	comments := []InlineComment{
		{Path: "main.go", Line: 10, Side: "RIGHT", Body: "Consider renaming this."},
	}
	err := gc.SubmitReview(context.Background(), 10, "Looks mostly good.", comments)
	if err != nil {
		t.Fatalf("SubmitReview() error: %v", err)
	}
	if !called {
		t.Error("expected review endpoint to be called")
	}
}

func TestGetPRState(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v3/repos/testowner/testrepo/pulls/7", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("expected GET, got %s", r.Method)
		}
		pr := &github.PullRequest{
			Number: github.Ptr(7),
			State:  github.Ptr("closed"),
			Merged: github.Ptr(true),
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(pr)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	gc := newTestGitHubClient(t, srv)
	state, merged, err := gc.GetPRState(context.Background(), 7)
	if err != nil {
		t.Fatalf("GetPRState() error: %v", err)
	}
	if state != "closed" {
		t.Errorf("expected state=closed, got %s", state)
	}
	if !merged {
		t.Error("expected merged=true")
	}
}
