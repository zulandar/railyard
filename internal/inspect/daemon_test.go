package inspect

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/google/go-github/v68/github"
	"github.com/zulandar/railyard/internal/config"
	"github.com/zulandar/railyard/internal/models"
)

// --- Mock Client ---

type mockClient struct {
	mu sync.Mutex

	prs   []*github.PullRequest
	files []*github.CommitFile
	fileContents  map[string]string // path -> content
	prState       string
	prMerged      bool
	labelsByPR    map[int]map[string]bool
	commentCount  int
	submittedPR   int
	submittedBody string
	ensuredLabels bool
}

func newMockClient() *mockClient {
	return &mockClient{
		fileContents: make(map[string]string),
		labelsByPR:   make(map[int]map[string]bool),
		prState:      "open",
	}
}

func (m *mockClient) ListReviewablePRs(_ context.Context) ([]*github.PullRequest, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.prs, nil
}

func (m *mockClient) ListPRFiles(_ context.Context, _ int) ([]*github.CommitFile, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.files, nil
}

func (m *mockClient) GetFileContent(_ context.Context, path, _ string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	content, ok := m.fileContents[path]
	if !ok {
		return "", nil
	}
	return content, nil
}

func (m *mockClient) GetPRState(_ context.Context, _ int) (string, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.prState, m.prMerged, nil
}

func (m *mockClient) SubmitReview(_ context.Context, number int, summary string, _ []InlineComment) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.submittedPR = number
	m.submittedBody = summary
	return nil
}

func (m *mockClient) DismissReviewsByBot(_ context.Context, _ int, _ string) error {
	return nil
}

func (m *mockClient) AddLabel(_ context.Context, number int, label string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.labelsByPR[number] == nil {
		m.labelsByPR[number] = make(map[string]bool)
	}
	m.labelsByPR[number][label] = true
	return nil
}

func (m *mockClient) RemoveLabel(_ context.Context, number int, label string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.labelsByPR[number] != nil {
		delete(m.labelsByPR[number], label)
	}
	return nil
}

func (m *mockClient) PRHasLabel(_ context.Context, number int, label string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.labelsByPR[number] == nil {
		return false, nil
	}
	return m.labelsByPR[number][label], nil
}

func (m *mockClient) CountTotalComments(_ context.Context, _ int, _ string) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.commentCount, nil
}

func (m *mockClient) EnsureLabels(_ context.Context, _ config.InspectLabelsConfig) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ensuredLabels = true
	return nil
}

// --- Mock Store ---

type mockStore struct {
	mu   sync.Mutex
	cars []models.Car

	claimedCar string
	released   bool
}

func (m *mockStore) ListPROpenCars(_ context.Context) ([]models.Car, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.cars, nil
}

func (m *mockStore) ClaimForReview(_ context.Context, carID, _ string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.claimedCar = carID
	return true, nil
}

func (m *mockStore) ReleaseReview(_ context.Context, _ string, _ map[string]interface{}) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.released = true
	return nil
}

func (m *mockStore) UpdateCarStatus(_ context.Context, _ string, _ string) error {
	return nil
}

// --- Mock AI ---

type mockAI struct {
	response string
}

func (m *mockAI) RunPrompt(_ context.Context, _ string) (string, error) {
	return m.response, nil
}

// --- Tests ---

func TestStart_MissingConfig(t *testing.T) {
	err := Start(context.Background(), StartOpts{})
	if err == nil {
		t.Fatal("expected error for nil config")
	}
	if err.Error() != "inspect: config is required" {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestStart_NotEnabled(t *testing.T) {
	cfg := &config.Config{
		Inspect: config.InspectConfig{Enabled: false},
	}
	err := Start(context.Background(), StartOpts{Config: cfg})
	if err == nil {
		t.Fatal("expected error for disabled inspect")
	}
	if err.Error() != "inspect: not enabled in config" {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRunDaemon_SingleCycle(t *testing.T) {
	// Build a valid AI response.
	result := ReviewResult{
		Summary:  "Looks good overall.",
		Comments: []ReviewComment{{Path: "main.go", Line: 10, Side: "RIGHT", Body: "Nice."}},
		Severity: "info",
	}
	resultJSON, _ := json.Marshal(result)

	client := newMockClient()
	client.prs = []*github.PullRequest{
		{
			Number: github.Ptr(42),
			Head: &github.PullRequestBranch{
				Ref: github.Ptr("ry/test-branch"),
				SHA: github.Ptr("abc123"),
			},
			Title: github.Ptr("Test PR"),
		},
	}
	client.files = []*github.CommitFile{
		{
			Filename: github.Ptr("main.go"),
			Patch:    github.Ptr("@@ -1,3 +1,4 @@\n+new line"),
			Changes:  github.Ptr(1),
		},
	}
	client.fileContents["main.go"] = "package main\n\nfunc main() {}\n"

	store := &mockStore{
		cars: []models.Car{
			{
				ID:     "car-1",
				Branch: "ry/test-branch",
				Track:  "backend",
				Status: "pr_open",
			},
		},
	}

	ai := &mockAI{response: string(resultJSON)}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	opts := DaemonOpts{
		Config: config.InspectConfig{
			Enabled:         true,
			PollIntervalSec: 1,
			MaxDiffLines:    10000,
			HealthPort:      0, // disabled in test
			Labels: config.InspectLabelsConfig{
				InProgress: "inspect: in-progress",
				Reviewed:   "inspect: reviewed",
				ReReview:   "inspect: re-review",
			},
		},
		Tracks: []config.TrackConfig{
			{Name: "backend", Language: "go"},
		},
		ReplicaID:    "test-replica",
		PollInterval: 1 * time.Millisecond,
		AI:           ai,
		BotLogin:     "railyard-bot[bot]",
		OnCycleEnd: func(cycle int) {
			if cycle >= 1 {
				cancel()
			}
		},
	}

	err := RunDaemon(ctx, client, store, opts)
	if err != nil && err != context.Canceled {
		t.Fatalf("RunDaemon returned unexpected error: %v", err)
	}

	// Verify the review was submitted for PR #42.
	client.mu.Lock()
	defer client.mu.Unlock()
	if client.submittedPR != 42 {
		t.Errorf("expected review submitted for PR 42, got %d", client.submittedPR)
	}

	// Verify the car was claimed.
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.claimedCar != "car-1" {
		t.Errorf("expected car-1 to be claimed, got %q", store.claimedCar)
	}
	if !store.released {
		t.Error("expected car to be released after review")
	}
}
