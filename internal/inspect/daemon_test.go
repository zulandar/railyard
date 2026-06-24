package inspect

import (
	"context"
	"encoding/json"
	"fmt"
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

	prs            []*github.PullRequest
	files          []*github.CommitFile
	fileContents   map[string]string // path -> content
	prState        string
	prMerged       bool
	labelsByPR     map[int]map[string]bool
	commentCount   int
	submittedPR    int
	submittedBody  string
	submittedEvent string
	ensuredLabels  bool
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

func (m *mockClient) SubmitReview(_ context.Context, number int, summary string, _ []InlineComment, event string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.submittedPR = number
	m.submittedBody = summary
	m.submittedEvent = event
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
	err      error
}

func (m *mockAI) RunPrompt(_ context.Context, _ string) (string, error) {
	if m.err != nil {
		return "", m.err
	}
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

// TestRunDaemon_SingleCycle tests a complete review cycle that processes one
// car/PR pair end-to-end: car is claimed, review is submitted, and labels are
// managed.
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
	// The review carries inline comments, so the verdict must request changes
	// (this is the signal the yardmaster reopens the car on).
	if client.submittedEvent != reviewEventRequestChanges {
		t.Errorf("expected REQUEST_CHANGES event, got %q", client.submittedEvent)
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

// maxIterationsErr returns an error that agentloop.IsMaxIterationsError will
// recognize as an iteration-cap hit. Used by tests that exercise the daemon's
// cap-hit bounding machinery.
func maxIterationsErr(iter int) error {
	return fmt.Errorf("inspect: native run prompt: agent did not finish within %d iterations", iter)
}

func TestHandleReviewError_TracksCapHits(t *testing.T) {
	client := newMockClient()
	opts := &DaemonOpts{}
	ctx := context.Background()
	prNum := 99

	// First cap hit: should not be terminal.
	term := handleReviewError(ctx, client, nil, opts, prNum, maxIterationsErr(16))
	if term {
		t.Error("first cap hit should not be terminal")
	}
	if opts.CapHitCounts[prNum] != 1 {
		t.Errorf("CapHitCounts[%d] = %d, want 1", prNum, opts.CapHitCounts[prNum])
	}

	// Second cap hit: still not terminal.
	term = handleReviewError(ctx, client, nil, opts, prNum, maxIterationsErr(16))
	if term {
		t.Error("second cap hit should not be terminal")
	}
	if opts.CapHitCounts[prNum] != 2 {
		t.Errorf("CapHitCounts[%d] = %d, want 2", prNum, opts.CapHitCounts[prNum])
	}

	// Third (final) cap hit: terminal, applies label.
	term = handleReviewError(ctx, client, nil, opts, prNum, maxIterationsErr(16))
	if !term {
		t.Error("third cap hit should be terminal")
	}
	if opts.CapHitCounts[prNum] != 3 {
		t.Errorf("CapHitCounts[%d] = %d, want 3", prNum, opts.CapHitCounts[prNum])
	}

	// Verify the terminal label was applied.
	has, err := client.PRHasLabel(ctx, prNum, inspectCapHitExhaustedLabel)
	if err != nil {
		t.Fatal(err)
	}
	if !has {
		t.Errorf("expected terminal label %q to be applied", inspectCapHitExhaustedLabel)
	}
}

func TestHandleReviewError_NonCapHitIgnored(t *testing.T) {
	client := newMockClient()
	opts := &DaemonOpts{}
	ctx := context.Background()
	prNum := 99

	// A non-cap-hit error (e.g., network) should not increment the counter.
	err := fmt.Errorf("network timeout")
	term := handleReviewError(ctx, client, nil, opts, prNum, err)
	if term {
		t.Error("non-cap-hit error should not be terminal")
	}
	if opts.CapHitCounts[prNum] != 0 {
		t.Errorf("CapHitCounts[%d] = %d, want 0 for non-cap-hit error", prNum, opts.CapHitCounts[prNum])
	}
}

func TestHandleReviewError_NilCapHitCounts(t *testing.T) {
	client := newMockClient()
	opts := &DaemonOpts{CapHitCounts: nil} // explicitly nil
	ctx := context.Background()
	prNum := 99

	term := handleReviewError(ctx, client, nil, opts, prNum, maxIterationsErr(16))
	if term {
		t.Error("first cap hit with nil map should not be terminal")
	}
	if opts.CapHitCounts[prNum] != 1 {
		t.Errorf("CapHitCounts[%d] = %d, want 1 after first cap hit", prNum, opts.CapHitCounts[prNum])
	}
}

func TestHandleReviewError_DifferentPRsTrackSeparately(t *testing.T) {
	client := newMockClient()
	opts := &DaemonOpts{}
	ctx := context.Background()

	// PR 1 hits cap twice (non-terminal).
	handleReviewError(ctx, client, nil, opts, 1, maxIterationsErr(16))
	handleReviewError(ctx, client, nil, opts, 1, maxIterationsErr(16))

	// PR 2 hits cap once (non-terminal).
	handleReviewError(ctx, client, nil, opts, 2, maxIterationsErr(16))

	if opts.CapHitCounts[1] != 2 {
		t.Errorf("CapHitCounts[1] = %d, want 2", opts.CapHitCounts[1])
	}
	if opts.CapHitCounts[2] != 1 {
		t.Errorf("CapHitCounts[2] = %d, want 1", opts.CapHitCounts[2])
	}

	// PR 1 third hit: terminal.
	term := handleReviewError(ctx, client, nil, opts, 1, maxIterationsErr(16))
	if !term {
		t.Error("PR 1 third hit should be terminal")
	}

	// PR 2 still at 1.
	if opts.CapHitCounts[2] != 1 {
		t.Errorf("CapHitCounts[2] = %d, want 1 (unaffected by PR 1)", opts.CapHitCounts[2])
	}
}

// scriptedAI returns a different (response, error) pair on each successive
// RunPrompt call, so tests can drive the parse-failure retry path.
type scriptedAI struct {
	calls []struct {
		resp string
		err  error
	}
	n int
}

func (s *scriptedAI) RunPrompt(_ context.Context, _ string) (string, error) {
	if s.n >= len(s.calls) {
		return "", fmt.Errorf("scriptedAI: unexpected call %d", s.n)
	}
	c := s.calls[s.n]
	s.n++
	return c.resp, c.err
}

// runSingleReviewCycle wires up a one-shot RunDaemon cycle against PR 42 / car-1
// with the given AI and starting cap-hit counts, returning the client/store so
// the caller can assert on side effects. prep, if non-nil, mutates the client
// before the cycle runs (e.g. to pre-seed labels).
func runSingleReviewCycle(t *testing.T, ai ReviewAI, opts DaemonOpts, prep func(*mockClient)) (*mockClient, *mockStore) {
	t.Helper()
	client := newMockClient()
	client.prs = []*github.PullRequest{
		{
			Number: github.Ptr(42),
			Head:   &github.PullRequestBranch{Ref: github.Ptr("ry/test-branch"), SHA: github.Ptr("abc123")},
			Title:  github.Ptr("Test PR"),
		},
	}
	client.files = []*github.CommitFile{
		{Filename: github.Ptr("main.go"), Patch: github.Ptr("@@ -1,3 +1,4 @@\n+new line"), Changes: github.Ptr(1)},
	}
	client.fileContents["main.go"] = "package main\n\nfunc main() {}\n"
	if prep != nil {
		prep(client)
	}

	store := &mockStore{cars: []models.Car{{ID: "car-1", Branch: "ry/test-branch", Track: "backend", Status: "pr_open"}}}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	opts.Config.Enabled = true
	opts.Config.MaxDiffLines = 10000
	opts.Config.HealthPort = 0
	if opts.Config.Labels.InProgress == "" {
		opts.Config.Labels = config.InspectLabelsConfig{
			InProgress: "inspect: in-progress",
			Reviewed:   "inspect: reviewed",
			ReReview:   "inspect: re-review",
		}
	}
	opts.Tracks = []config.TrackConfig{{Name: "backend", Language: "go"}}
	opts.ReplicaID = "test-replica"
	opts.PollInterval = 1 * time.Millisecond
	opts.AI = ai
	opts.BotLogin = "railyard-bot[bot]"
	opts.OnCycleEnd = func(cycle int) {
		if cycle >= 1 {
			cancel()
		}
	}

	if err := RunDaemon(ctx, client, store, opts); err != nil && err != context.Canceled {
		t.Fatalf("RunDaemon returned unexpected error: %v", err)
	}
	return client, store
}

// TestReviewOnePR_ResetsCapHitsOnSuccess verifies a successful review clears the
// per-PR cap-hit counter so stale partial counts don't accumulate across
// re-reviews of the same PR (railyard-1d0.8).
func TestReviewOnePR_ResetsCapHitsOnSuccess(t *testing.T) {
	result := ReviewResult{Summary: "Looks good.", Severity: "info"}
	resultJSON, _ := json.Marshal(result)
	ai := &mockAI{response: string(resultJSON)}

	opts := DaemonOpts{CapHitCounts: map[int]int{42: 2}}
	_, _ = runSingleReviewCycle(t, ai, opts, nil)

	if got := opts.CapHitCounts[42]; got != 0 {
		t.Errorf("CapHitCounts[42] = %d after successful review, want 0 (reset)", got)
	}
}

// TestReviewOnePR_RetryCapHitIsTracked verifies that when the first prompt
// parses badly and the retry hits the iteration cap, the cap hit is counted via
// the cap-hit machinery instead of being silently posted as a fallback COMMENT
// review (railyard-1d0.6).
func TestReviewOnePR_RetryCapHitIsTracked(t *testing.T) {
	ai := &scriptedAI{calls: []struct {
		resp string
		err  error
	}{
		{resp: "not json at all", err: nil},   // first prompt: parse fails
		{resp: "", err: maxIterationsErr(30)}, // retry: iteration cap hit
	}}

	opts := DaemonOpts{CapHitCounts: map[int]int{}}
	client, _ := runSingleReviewCycle(t, ai, opts, nil)

	if got := opts.CapHitCounts[42]; got != 1 {
		t.Errorf("CapHitCounts[42] = %d, want 1 (retry-leg cap hit must be tracked)", got)
	}
	// A cap hit is not a parse failure: no fallback COMMENT review should be posted.
	client.mu.Lock()
	defer client.mu.Unlock()
	if client.submittedPR != 0 {
		t.Errorf("no review should be submitted on a retry cap hit, got PR %d event %q", client.submittedPR, client.submittedEvent)
	}
}

// TestReviewOnePR_ClearsRevisedLabel verifies the inspect daemon removes the
// yardmaster "revised" label when it reviews, signalling that the latest pushed
// revision has been reviewed so a fresh verdict can drive a reopen without the
// stale-CHANGES_REQUESTED reopen loop (railyard-1d0.5).
func TestReviewOnePR_ClearsRevisedLabel(t *testing.T) {
	result := ReviewResult{Summary: "Looks good.", Severity: "info"}
	resultJSON, _ := json.Marshal(result)
	ai := &mockAI{response: string(resultJSON)}

	opts := DaemonOpts{RevisedLabel: "railyard: revised"}
	client, _ := runSingleReviewCycle(t, ai, opts, func(c *mockClient) {
		c.labelsByPR[42] = map[string]bool{"railyard: revised": true}
	})

	has, err := client.PRHasLabel(context.Background(), 42, "railyard: revised")
	if err != nil {
		t.Fatal(err)
	}
	if has {
		t.Error("expected the revised label to be removed after a review")
	}
}

func TestReviewCycle_SkipsCapHitExhaustedPR(t *testing.T) {
	ai := &mockAI{response: "{}"} // won't be called because PR gets skipped

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
	// Pre-set the terminal cap-hit-exhausted label.
	client.labelsByPR[42] = map[string]bool{
		inspectCapHitExhaustedLabel: true,
	}

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

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	opts := DaemonOpts{
		Config: config.InspectConfig{
			Enabled:         true,
			PollIntervalSec: 1,
			MaxDiffLines:    10000,
			HealthPort:      0,
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

	// Car should NOT have been claimed because the PR was skipped.
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.claimedCar == "car-1" {
		t.Error("car should not have been claimed for a cap-hit-exhausted PR")
	}
	// No review should have been submitted.
	client.mu.Lock()
	defer client.mu.Unlock()
	if client.submittedPR != 0 {
		t.Errorf("no review should be submitted for cap-hit-exhausted PR, got PR %d", client.submittedPR)
	}
}
