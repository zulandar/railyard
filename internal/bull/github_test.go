package bull

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/bradleyfalzon/ghinstallation/v2"
	"github.com/google/go-github/v68/github"
	"github.com/zulandar/railyard/internal/config"
	"golang.org/x/oauth2"
)

// newTestClient creates a GitHubClient backed by the given httptest.Server.
func newTestClient(t *testing.T, srv *httptest.Server) *GitHubClient {
	t.Helper()
	c := github.NewClient(srv.Client())
	c.BaseURL, _ = c.BaseURL.Parse(srv.URL + "/")
	return &GitHubClient{
		client:             c,
		owner:              "testowner",
		repo:               "testrepo",
		rateLimitThreshold: 100,
	}
}

// setRateLimitHeaders sets generous rate limit headers on a response.
func setRateLimitHeaders(w http.ResponseWriter) {
	w.Header().Set("X-RateLimit-Remaining", "500")
	w.Header().Set("X-RateLimit-Reset", strconv.FormatInt(time.Now().Add(time.Hour).Unix(), 10))
}

// ---------- NewClient ----------

func TestNewClient_PAT(t *testing.T) {
	cfg := config.BullConfig{GitHubToken: "fake-token"}
	gc, err := NewClient("owner", "repo", cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gc == nil {
		t.Fatal("expected non-nil GitHubClient")
	}
	if gc.owner != "owner" {
		t.Errorf("owner = %q, want %q", gc.owner, "owner")
	}
	if gc.repo != "repo" {
		t.Errorf("repo = %q, want %q", gc.repo, "repo")
	}
	if gc.rateLimitThreshold != 100 {
		t.Errorf("rateLimitThreshold = %d, want 100", gc.rateLimitThreshold)
	}
	if gc.client == nil {
		t.Error("expected non-nil underlying github.Client")
	}
	// Verify the underlying transport is oauth2.
	transport := gc.client.Client().Transport
	if _, ok := transport.(*oauth2.Transport); !ok {
		t.Errorf("expected oauth2.Transport, got %s", reflect.TypeOf(transport))
	}
}

func TestNewClient_AppCredentials(t *testing.T) {
	// Generate a real RSA key so ghinstallation can parse it.
	keyFile := generateRSAKeyFile(t)

	cfg := config.BullConfig{
		AppID:          12345,
		InstallationID: 67890,
		PrivateKeyPath: keyFile,
	}
	gc, err := NewClient("owner", "repo", cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gc == nil {
		t.Fatal("expected non-nil GitHubClient")
	}
	if gc.owner != "owner" {
		t.Errorf("owner = %q, want %q", gc.owner, "owner")
	}
	if gc.repo != "repo" {
		t.Errorf("repo = %q, want %q", gc.repo, "repo")
	}
	// Verify the underlying transport is ghinstallation.
	transport := gc.client.Client().Transport
	if _, ok := transport.(*ghinstallation.Transport); !ok {
		t.Errorf("expected *ghinstallation.Transport, got %s", reflect.TypeOf(transport))
	}
}

func TestNewClient_AppCredentials_BadKeyPath(t *testing.T) {
	cfg := config.BullConfig{
		AppID:          12345,
		InstallationID: 67890,
		PrivateKeyPath: "/nonexistent/key.pem",
	}
	_, err := NewClient("owner", "repo", cfg)
	if err == nil {
		t.Fatal("expected error for bad key path")
	}
	if !strings.Contains(err.Error(), "github app auth") {
		t.Errorf("error %q should contain 'github app auth'", err.Error())
	}
}

// generateRSAKeyFile generates a temporary RSA private key PEM file for use in tests.
func generateRSAKeyFile(t *testing.T) string {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate RSA key: %v", err)
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(key),
	})
	path := t.TempDir() + "/test.pem"
	if err := os.WriteFile(path, keyPEM, 0600); err != nil {
		t.Fatalf("write key file: %v", err)
	}
	return path
}

// ---------- ListNewIssues ----------

func TestListNewIssues_Success(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/testowner/testrepo/issues", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		since := r.URL.Query().Get("since")
		if since == "" {
			t.Error("expected 'since' query parameter")
		}
		state := r.URL.Query().Get("state")
		if state != "open" {
			t.Errorf("state = %q, want %q", state, "open")
		}
		setRateLimitHeaders(w)
		w.Header().Set("Content-Type", "application/json")
		issues := []github.Issue{
			{Number: github.Ptr(1), Title: github.Ptr("first issue")},
			{Number: github.Ptr(2), Title: github.Ptr("second issue")},
		}
		json.NewEncoder(w).Encode(issues)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	gc := newTestClient(t, srv)
	since := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	issues, err := gc.ListNewIssues(context.Background(), since)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(issues) != 2 {
		t.Fatalf("got %d issues, want 2", len(issues))
	}
	if issues[0].GetTitle() != "first issue" {
		t.Errorf("issue[0] title = %q, want %q", issues[0].GetTitle(), "first issue")
	}
}

// ---------- GetIssue ----------

func TestGetIssue_Success(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/testowner/testrepo/issues/42", func(w http.ResponseWriter, r *http.Request) {
		setRateLimitHeaders(w)
		w.Header().Set("Content-Type", "application/json")
		issue := github.Issue{Number: github.Ptr(42), Title: github.Ptr("test issue")}
		json.NewEncoder(w).Encode(issue)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	gc := newTestClient(t, srv)
	issue, err := gc.GetIssue(context.Background(), 42)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if issue.GetNumber() != 42 {
		t.Errorf("number = %d, want 42", issue.GetNumber())
	}
}

func TestGetIssue_NotFound(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/testowner/testrepo/issues/999", func(w http.ResponseWriter, r *http.Request) {
		setRateLimitHeaders(w)
		w.WriteHeader(http.StatusNotFound)
		fmt.Fprintf(w, `{"message":"Not Found"}`)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	gc := newTestClient(t, srv)
	_, err := gc.GetIssue(context.Background(), 999)
	if err == nil {
		t.Fatal("expected error for 404")
	}
	if !strings.Contains(err.Error(), "bull:") {
		t.Errorf("error %q should contain 'bull:' prefix", err.Error())
	}
}

// ---------- AddLabel ----------

func TestAddLabel_Success(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/testowner/testrepo/issues/10/labels", func(w http.ResponseWriter, r *http.Request) {
		setRateLimitHeaders(w)
		w.Header().Set("Content-Type", "application/json")
		labels := []github.Label{{Name: github.Ptr("bug")}}
		json.NewEncoder(w).Encode(labels)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	gc := newTestClient(t, srv)
	err := gc.AddLabel(context.Background(), 10, "bug")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// ---------- RemoveLabel ----------

func TestRemoveLabel_Success(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/testowner/testrepo/issues/10/labels/bug", func(w http.ResponseWriter, r *http.Request) {
		setRateLimitHeaders(w)
		w.WriteHeader(http.StatusNoContent)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	gc := newTestClient(t, srv)
	err := gc.RemoveLabel(context.Background(), 10, "bug")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// ---------- AddComment ----------

func TestAddComment_Success(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/testowner/testrepo/issues/5/comments", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Body string `json:"body"`
		}
		json.NewDecoder(r.Body).Decode(&body)
		if body.Body != "hello world" {
			t.Errorf("comment body = %q, want %q", body.Body, "hello world")
		}
		setRateLimitHeaders(w)
		w.Header().Set("Content-Type", "application/json")
		comment := github.IssueComment{ID: github.Ptr(int64(1)), Body: github.Ptr("hello world")}
		json.NewEncoder(w).Encode(comment)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	gc := newTestClient(t, srv)
	err := gc.AddComment(context.Background(), 5, "hello world")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// ---------- CloseIssue ----------

func TestCloseIssue_Success(t *testing.T) {
	commentCalled := false
	editCalled := false

	mux := http.NewServeMux()
	mux.HandleFunc("/repos/testowner/testrepo/issues/7/comments", func(w http.ResponseWriter, r *http.Request) {
		commentCalled = true
		setRateLimitHeaders(w)
		w.Header().Set("Content-Type", "application/json")
		comment := github.IssueComment{ID: github.Ptr(int64(1))}
		json.NewEncoder(w).Encode(comment)
	})
	mux.HandleFunc("/repos/testowner/testrepo/issues/7", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPatch {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		editCalled = true
		var body struct {
			State string `json:"state"`
		}
		json.NewDecoder(r.Body).Decode(&body)
		if body.State != "closed" {
			t.Errorf("state = %q, want %q", body.State, "closed")
		}
		setRateLimitHeaders(w)
		w.Header().Set("Content-Type", "application/json")
		issue := github.Issue{Number: github.Ptr(7), State: github.Ptr("closed")}
		json.NewEncoder(w).Encode(issue)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	gc := newTestClient(t, srv)
	err := gc.CloseIssue(context.Background(), 7, "closing this")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !commentCalled {
		t.Error("expected comment to be created")
	}
	if !editCalled {
		t.Error("expected issue to be edited (closed)")
	}
}

// ---------- ListReleases ----------

func TestListReleases_Success(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/testowner/testrepo/releases", func(w http.ResponseWriter, r *http.Request) {
		setRateLimitHeaders(w)
		w.Header().Set("Content-Type", "application/json")
		now := time.Now()
		old := now.Add(-48 * time.Hour)
		releases := []github.RepositoryRelease{
			{ID: github.Ptr(int64(1)), TagName: github.Ptr("v1.0.0"), CreatedAt: &github.Timestamp{Time: now}},
			{ID: github.Ptr(int64(2)), TagName: github.Ptr("v0.9.0"), CreatedAt: &github.Timestamp{Time: old}},
		}
		json.NewEncoder(w).Encode(releases)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	gc := newTestClient(t, srv)
	since := time.Now().Add(-24 * time.Hour)
	releases, err := gc.ListReleases(context.Background(), since)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(releases) != 1 {
		t.Fatalf("got %d releases, want 1", len(releases))
	}
	if releases[0].GetTagName() != "v1.0.0" {
		t.Errorf("tag = %q, want %q", releases[0].GetTagName(), "v1.0.0")
	}
}

// ---------- Error handling ----------

func TestGetIssue_ServerError(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/testowner/testrepo/issues/1", func(w http.ResponseWriter, r *http.Request) {
		setRateLimitHeaders(w)
		w.WriteHeader(http.StatusInternalServerError)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	gc := newTestClient(t, srv)
	_, err := gc.GetIssue(context.Background(), 1)
	if err == nil {
		t.Fatal("expected error for 500")
	}
}

// ---------- Rate limit ----------

func TestRateLimitBackoff_BelowThreshold(t *testing.T) {
	// Use a reset time far enough in the future to avoid races between
	// handler execution and client-side time.Until() calculation.
	resetTime := time.Now().Add(2 * time.Second).Unix()

	mux := http.NewServeMux()
	mux.HandleFunc("/repos/testowner/testrepo/issues/1", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-RateLimit-Remaining", "10")
		w.Header().Set("X-RateLimit-Reset", strconv.FormatInt(resetTime, 10))
		issue := github.Issue{Number: github.Ptr(1), Title: github.Ptr("test")}
		json.NewEncoder(w).Encode(issue)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	gc := newTestClient(t, srv)
	gc.rateLimitThreshold = 100

	start := time.Now()
	_, err := gc.GetIssue(context.Background(), 1)
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Should have waited until reset time due to rate limit backoff.
	if elapsed < 500*time.Millisecond {
		t.Errorf("expected rate limit backoff wait, but elapsed = %v", elapsed)
	}
}

func TestRateLimitBackoff_403Response(t *testing.T) {
	callCount := 0
	// Use a reset time far enough in the past so that after our sleep,
	// go-github's internal rate limiter allows the retry request.
	// The trick: set reset to now (the handler sleeps have already happened
	// by the time the retry fires). We use time.Now() at test start.
	resetAt := time.Now().Add(500 * time.Millisecond)

	mux := http.NewServeMux()
	mux.HandleFunc("/repos/testowner/testrepo/issues/1", func(w http.ResponseWriter, r *http.Request) {
		callCount++
		if callCount == 1 {
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("X-RateLimit-Remaining", "0")
			w.Header().Set("X-RateLimit-Reset", strconv.FormatInt(resetAt.Unix(), 10))
			w.WriteHeader(http.StatusForbidden)
			fmt.Fprintf(w, `{"message":"API rate limit exceeded"}`)
			return
		}
		setRateLimitHeaders(w)
		w.Header().Set("Content-Type", "application/json")
		issue := github.Issue{Number: github.Ptr(1), Title: github.Ptr("test")}
		json.NewEncoder(w).Encode(issue)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	gc := newTestClient(t, srv)
	_, err := gc.GetIssue(context.Background(), 1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if callCount != 2 {
		t.Errorf("callCount = %d, want 2 (one 403, one success)", callCount)
	}
}
