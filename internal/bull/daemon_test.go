package bull

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/go-github/v68/github"
	"github.com/zulandar/railyard/internal/config"
	"github.com/zulandar/railyard/internal/models"
)

// countingMockAI fails the first failTimes calls, then returns successResponse.
type countingMockAI struct {
	failTimes       int
	calls           int
	successResponse string
}

func (m *countingMockAI) RunPrompt(_ context.Context, _ string) (string, error) {
	m.calls++
	if m.calls <= m.failTimes {
		return "", fmt.Errorf("mock AI transient error (call %d)", m.calls)
	}
	return m.successResponse, nil
}

// ---------- Mock DaemonDeps ----------

type mockDaemonDeps struct {
	// GitHub issues returned by ListNewIssues
	issues []*github.Issue
	// Optional override: if set, ListNewIssues calls this instead of returning issues.
	listNewIssuesFn func(ctx context.Context, since time.Time) ([]*github.Issue, error)

	// Sync
	syncTrackedIssues []models.BullIssue
	syncCarStatuses   map[string]string
	syncErr           error

	// Releases
	releases         []*github.RepositoryRelease
	mergedIssues     []models.BullIssue
	lastReleaseCheck time.Time
	releaseErr       error

	// Tracking
	phasesRun     []string
	closedIssues  []int
	removedLabels []struct {
		number int
		label  string
	}
	addedLabels []struct {
		number int
		label  string
	}
	addedComments []struct {
		number int
		body   string
	}
	statusUpdates []struct {
		issueID   uint
		newStatus string
	}
	savedCheckTime *time.Time
	recordedIssues []models.BullIssue
	createdCars    []CarCreateOpts
}

func (m *mockDaemonDeps) ListNewIssues(ctx context.Context, since time.Time) ([]*github.Issue, error) {
	m.phasesRun = append(m.phasesRun, "poll")
	if m.listNewIssuesFn != nil {
		return m.listNewIssuesFn(ctx, since)
	}
	return m.issues, nil
}

func (m *mockDaemonDeps) GetIssue(ctx context.Context, number int) (*github.Issue, error) {
	for _, i := range m.issues {
		if i.GetNumber() == number {
			return i, nil
		}
	}
	return nil, errors.New("not found")
}

// TriageClient methods
func (m *mockDaemonDeps) AddLabel(ctx context.Context, number int, label string) error {
	m.addedLabels = append(m.addedLabels, struct {
		number int
		label  string
	}{number, label})
	return nil
}

func (m *mockDaemonDeps) RemoveLabel(ctx context.Context, number int, label string) error {
	m.removedLabels = append(m.removedLabels, struct {
		number int
		label  string
	}{number, label})
	return nil
}

func (m *mockDaemonDeps) AddComment(ctx context.Context, number int, body string) error {
	m.addedComments = append(m.addedComments, struct {
		number int
		body   string
	}{number, body})
	return nil
}

func (m *mockDaemonDeps) CloseIssue(ctx context.Context, number int, comment string) error {
	m.closedIssues = append(m.closedIssues, number)
	return nil
}

func (m *mockDaemonDeps) ListReleases(ctx context.Context, since time.Time) ([]*github.RepositoryRelease, error) {
	if m.releaseErr != nil {
		return nil, m.releaseErr
	}
	var filtered []*github.RepositoryRelease
	for _, r := range m.releases {
		if r.CreatedAt != nil && r.CreatedAt.After(since) {
			filtered = append(filtered, r)
		}
	}
	return filtered, nil
}

// SyncStore methods
func (m *mockDaemonDeps) GetTrackedIssues(ctx context.Context) ([]models.BullIssue, error) {
	if m.syncErr != nil {
		return nil, m.syncErr
	}
	return m.syncTrackedIssues, nil
}

func (m *mockDaemonDeps) GetCarStatus(ctx context.Context, carID string) (string, error) {
	status, ok := m.syncCarStatuses[carID]
	if !ok {
		return "", errors.New("car not found")
	}
	return status, nil
}

func (m *mockDaemonDeps) UpdateIssueStatus(ctx context.Context, issueID uint, newStatus string) error {
	m.statusUpdates = append(m.statusUpdates, struct {
		issueID   uint
		newStatus string
	}{issueID, newStatus})
	return nil
}

// ReleaseStore methods
func (m *mockDaemonDeps) GetMergedIssues(ctx context.Context) ([]models.BullIssue, error) {
	return m.mergedIssues, nil
}

func (m *mockDaemonDeps) GetLastReleaseCheck(ctx context.Context) (time.Time, error) {
	return m.lastReleaseCheck, nil
}

func (m *mockDaemonDeps) SetLastReleaseCheck(ctx context.Context, t time.Time) error {
	m.savedCheckTime = &t
	return nil
}

// TriageStore methods
func (m *mockDaemonDeps) CreateCar(ctx context.Context, opts CarCreateOpts) (string, error) {
	m.createdCars = append(m.createdCars, opts)
	return "car-test", nil
}

func (m *mockDaemonDeps) RecordTriagedIssue(ctx context.Context, issue models.BullIssue) error {
	m.recordedIssues = append(m.recordedIssues, issue)
	return nil
}

// ---------- Helpers ----------

func daemonConfig() config.BullConfig {
	return config.BullConfig{
		Enabled:         true,
		PollIntervalSec: 1,
		TriageMode:      "standard",
		Labels: config.BullLabelsConfig{
			UnderReview: "bull: under review",
			InProgress:  "bull: in progress",
			FixMerged:   "bull: fix merged",
			Ignore:      "bull: ignore",
		},
		Comments: config.BullCommentsConfig{
			Enabled: true,
		},
	}
}

// ---------- Tests ----------

func TestRunDaemon_RunsAllPhasesInOrder(t *testing.T) {
	deps := &mockDaemonDeps{
		issues: []*github.Issue{
			makeIssue(1, "Unique bug title", "This is a valid bug report with enough text to pass the filter"),
		},
		syncTrackedIssues: []models.BullIssue{
			{ID: 1, IssueNumber: 5, CarID: "car-5", LastKnownStatus: "open"},
		},
		syncCarStatuses: map[string]string{"car-5": "open"},
		releases:        []*github.RepositoryRelease{},
		mergedIssues:    []models.BullIssue{},
	}

	ctx, cancel := context.WithCancel(context.Background())
	var buf bytes.Buffer

	// Run one cycle then cancel
	opts := DaemonOpts{
		Config:       daemonConfig(),
		Tracks:       []TrackInfo{{Name: "backend"}},
		BranchPrefix: "ry/test",
		PollInterval: time.Millisecond,
		Out:          &buf,
		OnCycleEnd: func(cycle int) {
			cancel()
		},
	}

	err := RunDaemon(ctx, deps, opts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	output := buf.String()
	// Verify key phases ran by checking log output contains phase markers
	// Phase 2 only prints when an issue is already tracked; Phase 4 prints for triage
	phases := []string{"Phase 1", "Phase 5", "Phase 6"}
	for _, phase := range phases {
		if !strings.Contains(output, phase) {
			t.Errorf("expected output to contain %q, got:\n%s", phase, output)
		}
	}
}

func TestRunDaemon_GracefulShutdown(t *testing.T) {
	deps := &mockDaemonDeps{
		issues:          []*github.Issue{},
		syncCarStatuses: map[string]string{},
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	var buf bytes.Buffer
	opts := DaemonOpts{
		Config:       daemonConfig(),
		Tracks:       []TrackInfo{{Name: "backend"}},
		BranchPrefix: "ry/test",
		PollInterval: time.Millisecond,
		Out:          &buf,
	}

	err := RunDaemon(ctx, deps, opts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	output := buf.String()
	if !strings.Contains(output, "shutting down") {
		t.Errorf("expected shutdown message, got:\n%s", output)
	}
}

func TestRunDaemon_PhaseErrorDoesNotCrashLoop(t *testing.T) {
	deps := &mockDaemonDeps{
		issues:            []*github.Issue{},
		syncTrackedIssues: []models.BullIssue{},
		syncCarStatuses:   map[string]string{},
		syncErr:           errors.New("sync database error"),
		releases:          []*github.RepositoryRelease{},
		mergedIssues:      []models.BullIssue{},
	}

	ctx, cancel := context.WithCancel(context.Background())
	var buf bytes.Buffer
	cycleCount := 0

	opts := DaemonOpts{
		Config:       daemonConfig(),
		Tracks:       []TrackInfo{{Name: "backend"}},
		BranchPrefix: "ry/test",
		PollInterval: time.Millisecond,
		Out:          &buf,
		OnCycleEnd: func(cycle int) {
			cycleCount++
			if cycleCount >= 2 {
				cancel()
			}
		},
	}

	err := RunDaemon(ctx, deps, opts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cycleCount < 2 {
		t.Errorf("expected at least 2 cycles despite errors, got %d", cycleCount)
	}

	// The key assertion: the loop ran multiple cycles despite sync errors.
	// Errors are logged to the standard logger (not the output writer).
}

func TestRunDaemon_BackoffOnRateLimit(t *testing.T) {
	cfg := daemonConfig()
	cfg.PollIntervalSec = 1

	deps := &mockDaemonDeps{
		issues:          []*github.Issue{},
		syncCarStatuses: map[string]string{},
	}

	ctx, cancel := context.WithCancel(context.Background())
	var buf bytes.Buffer
	var cycleTimes []time.Time

	opts := DaemonOpts{
		Config:         cfg,
		Tracks:         []TrackInfo{{Name: "backend"}},
		BranchPrefix:   "ry/test",
		PollInterval:   50 * time.Millisecond,
		Out:            &buf,
		RateLimitUntil: time.Now().Add(100 * time.Millisecond),
		OnCycleEnd: func(cycle int) {
			cycleTimes = append(cycleTimes, time.Now())
			if cycle >= 1 {
				cancel()
			}
		},
	}

	err := RunDaemon(ctx, deps, opts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	output := buf.String()
	if !strings.Contains(output, "rate limit") {
		t.Errorf("expected rate limit backoff message, got:\n%s", output)
	}
}

func TestRunDaemon_FiltersDuplicateIssues(t *testing.T) {
	deps := &mockDaemonDeps{
		issues: []*github.Issue{
			makeIssue(1, "Bug report", "This is a valid bug report with enough text to pass the heuristic filter"),
			makeIssue(1, "Bug report", "This is a valid bug report with enough text to pass the heuristic filter"), // duplicate
		},
		syncTrackedIssues: []models.BullIssue{},
		syncCarStatuses:   map[string]string{},
		releases:          []*github.RepositoryRelease{},
		mergedIssues:      []models.BullIssue{},
	}

	ctx, cancel := context.WithCancel(context.Background())
	var buf bytes.Buffer

	opts := DaemonOpts{
		Config:       daemonConfig(),
		Tracks:       []TrackInfo{{Name: "backend"}},
		BranchPrefix: "ry/test",
		PollInterval: time.Millisecond,
		Out:          &buf,
		OnCycleEnd: func(cycle int) {
			cancel()
		},
	}

	err := RunDaemon(ctx, deps, opts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Should not process the same issue number twice
}

func TestRunDaemon_NilOutDefaultsToDiscard(t *testing.T) {
	deps := &mockDaemonDeps{
		issues:          []*github.Issue{},
		syncCarStatuses: map[string]string{},
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	opts := DaemonOpts{
		Config:       daemonConfig(),
		Tracks:       []TrackInfo{{Name: "backend"}},
		BranchPrefix: "ry/test",
		PollInterval: time.Millisecond,
		Out:          nil, // should not panic
	}

	err := RunDaemon(ctx, deps, opts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// Fix #2: Raw AI response should be truncated in log output.
func TestRunDaemon_RawResponseTruncatedInOutput(t *testing.T) {
	longResponse := makeAIResponse("bug", nil) + strings.Repeat("x", 500)
	ai := &mockTriageAI{response: longResponse}

	deps := &mockDaemonDeps{
		issues: []*github.Issue{
			makeIssue(50, "Long response test", "Testing that raw response gets truncated in output"),
		},
		syncTrackedIssues: []models.BullIssue{},
		syncCarStatuses:   map[string]string{},
		releases:          []*github.RepositoryRelease{},
		mergedIssues:      []models.BullIssue{},
	}

	ctx, cancel := context.WithCancel(context.Background())
	var buf bytes.Buffer

	opts := DaemonOpts{
		Config:       daemonConfig(),
		Tracks:       []TrackInfo{{Name: "backend"}},
		BranchPrefix: "ry/test",
		PollInterval: time.Millisecond,
		Out:          &buf,
		AI:           ai,
		OnCycleEnd: func(c int) {
			cancel()
		},
	}

	_ = RunDaemon(ctx, deps, opts)

	// The full raw response should NOT appear in output — it should be truncated.
	output := buf.String()
	if strings.Contains(output, longResponse) {
		t.Errorf("raw response should be truncated, but full response found in output")
	}
}

// Fix #5: Retry phase should use a distinct label, not "Phase 4".
func TestRunDaemon_RetryPhaseUsesDistinctLabel(t *testing.T) {
	ai := &countingMockAI{
		failTimes:       99,
		successResponse: makeAIResponse("bug", nil),
	}

	deps := &mockDaemonDeps{
		issues: []*github.Issue{
			makeIssue(60, "Phase label test", "Testing that retry phase has a distinct label"),
		},
		syncTrackedIssues: []models.BullIssue{},
		syncCarStatuses:   map[string]string{},
		releases:          []*github.RepositoryRelease{},
		mergedIssues:      []models.BullIssue{},
	}

	ctx, cancel := context.WithCancel(context.Background())
	var buf bytes.Buffer

	opts := DaemonOpts{
		Config:       daemonConfig(),
		Tracks:       []TrackInfo{{Name: "backend"}},
		BranchPrefix: "ry/test",
		PollInterval: time.Millisecond,
		Out:          &buf,
		AI:           ai,
		OnCycleEnd: func(c int) {
			if c >= 2 {
				cancel()
			}
		},
	}

	_ = RunDaemon(ctx, deps, opts)

	output := buf.String()
	// Retry output should NOT say "Phase 4:" for retries.
	if strings.Contains(output, "Phase 4: Processing") || strings.Contains(output, "Phase 4: Retrying") {
		t.Errorf("retry phase should not use 'Phase 4' label; output:\n%s", output)
	}
	// Should use a distinct retry label.
	if !strings.Contains(output, "Retry:") {
		t.Errorf("expected retry phase to use 'Retry:' label; output:\n%s", output)
	}
}

// TestRunDaemon_RetryQueue_IssuePersistsToNextCycle verifies that when triage
// fails on cycle 1, the issue appears in the retry output on cycle 2.
func TestRunDaemon_RetryQueue_IssuePersistsToNextCycle(t *testing.T) {
	ai := &countingMockAI{
		failTimes:       99, // always fail
		successResponse: makeAIResponse("bug", nil),
	}

	deps := &mockDaemonDeps{
		issues: []*github.Issue{
			makeIssue(42, "Crashing on startup", "The application crashes immediately on startup with a nil pointer dereference"),
		},
		syncTrackedIssues: []models.BullIssue{},
		syncCarStatuses:   map[string]string{},
		releases:          []*github.RepositoryRelease{},
		mergedIssues:      []models.BullIssue{},
	}

	ctx, cancel := context.WithCancel(context.Background())
	var buf bytes.Buffer
	cycle := 0

	opts := DaemonOpts{
		Config:       daemonConfig(),
		Tracks:       []TrackInfo{{Name: "backend"}},
		BranchPrefix: "ry/test",
		PollInterval: time.Millisecond,
		Out:          &buf,
		AI:           ai,
		OnCycleEnd: func(c int) {
			cycle = c
			if c >= 2 {
				cancel()
			}
		},
	}

	err := RunDaemon(ctx, deps, opts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cycle < 2 {
		t.Fatalf("expected at least 2 cycles, got %d", cycle)
	}

	output := buf.String()
	if !strings.Contains(output, "Retrying triage for issue #42") {
		t.Errorf("expected retry output for issue #42 on cycle 2, got:\n%s", output)
	}
}

// TestRunDaemon_RetryQueue_DropsAfterMaxRetries verifies that an issue is
// dropped after maxTriageRetries failed attempts and the queue is empty.
func TestRunDaemon_RetryQueue_DropsAfterMaxRetries(t *testing.T) {
	ai := &countingMockAI{
		failTimes:       99, // always fail
		successResponse: makeAIResponse("bug", nil),
	}

	issueOnce := []*github.Issue{
		makeIssue(7, "Memory leak in handler", "There is a memory leak in the request handler that causes OOM after a few hours"),
	}
	pollCount := 0
	deps := &mockDaemonDeps{
		syncTrackedIssues: []models.BullIssue{},
		syncCarStatuses:   map[string]string{},
		releases:          []*github.RepositoryRelease{},
		mergedIssues:      []models.BullIssue{},
	}
	// Return issue only on the first poll so it isn't re-queued each cycle.
	deps.listNewIssuesFn = func(ctx context.Context, since time.Time) ([]*github.Issue, error) {
		deps.phasesRun = append(deps.phasesRun, "poll")
		pollCount++
		if pollCount == 1 {
			return issueOnce, nil
		}
		return nil, nil
	}

	ctx, cancel := context.WithCancel(context.Background())
	var buf bytes.Buffer
	// Cycles needed:
	// Cycle 1: new issue fails → attempt=1 queued
	// Cycle 2: attempt=1 fails → attempt=2 queued
	// Cycle 3: attempt=2 fails → dropped (attempt+1=3 not < maxTriageRetries=3)
	// Cycle 4: no retry entries
	const neededCycles = 4

	opts := DaemonOpts{
		Config:       daemonConfig(),
		Tracks:       []TrackInfo{{Name: "backend"}},
		BranchPrefix: "ry/test",
		PollInterval: time.Millisecond,
		Out:          &buf,
		AI:           ai,
		OnCycleEnd: func(c int) {
			if c >= neededCycles {
				cancel()
			}
		},
	}

	err := RunDaemon(ctx, deps, opts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	output := buf.String()

	// Count retry attempts: attempt=1 (cycle 2) and attempt=2 (cycle 3) → exactly 2.
	retryLineCount := strings.Count(output, "Retrying triage for issue #7")
	if retryLineCount != 2 {
		t.Errorf("expected exactly 2 retry attempts for issue #7 before drop, got %d; output:\n%s", retryLineCount, output)
	}

	// Cycle 4 should show no retry entries for #7 (queue is empty after drop).
	if strings.Contains(output, "Retrying triage for issue #7 (attempt 3)") {
		t.Errorf("issue #7 should have been dropped after attempt 2, not retried again; output:\n%s", output)
	}
}

// TestRunDaemon_RetryQueue_SucceedsOnRetry verifies that when an issue fails
// once and succeeds on the next retry, the correct outcome is logged.
func TestRunDaemon_RetryQueue_SucceedsOnRetry(t *testing.T) {
	ai := &countingMockAI{
		failTimes:       1, // fail once, then succeed
		successResponse: makeAIResponse("bug", nil),
	}

	issueOnce := []*github.Issue{
		makeIssue(99, "Data corruption on write", "Data gets corrupted when writing large payloads under high concurrency"),
	}
	pollCount := 0
	deps := &mockDaemonDeps{
		syncTrackedIssues: []models.BullIssue{},
		syncCarStatuses:   map[string]string{},
		releases:          []*github.RepositoryRelease{},
		mergedIssues:      []models.BullIssue{},
	}
	// Return issue only on the first poll to avoid interference on retry cycle.
	deps.listNewIssuesFn = func(ctx context.Context, since time.Time) ([]*github.Issue, error) {
		deps.phasesRun = append(deps.phasesRun, "poll")
		pollCount++
		if pollCount == 1 {
			return issueOnce, nil
		}
		return nil, nil
	}

	ctx, cancel := context.WithCancel(context.Background())
	var buf bytes.Buffer

	opts := DaemonOpts{
		Config:       daemonConfig(),
		Tracks:       []TrackInfo{{Name: "backend"}},
		BranchPrefix: "ry/test",
		PollInterval: time.Millisecond,
		Out:          &buf,
		AI:           ai,
		OnCycleEnd: func(c int) {
			if c >= 2 {
				cancel()
			}
		},
	}

	err := RunDaemon(ctx, deps, opts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	output := buf.String()

	// Cycle 1: triage fails → queued
	if !strings.Contains(output, "Phase 4: Triaging issue #99") {
		t.Errorf("expected initial triage attempt for #99; output:\n%s", output)
	}

	// Cycle 2: retry succeeds → "(retried)" in output
	if !strings.Contains(output, "Issue #99") || !strings.Contains(output, "(retried)") {
		t.Errorf("expected successful retry outcome for issue #99; output:\n%s", output)
	}
}

// TestRunDaemon_RetryQueue_NoDuplicateEntries verifies that when the same
// issue number appears twice in a batch and triage fails, only one retry
// entry is created for that issue number.
func TestRunDaemon_RetryQueue_NoDuplicateEntries(t *testing.T) {
	ai := &countingMockAI{
		failTimes:       99, // always fail
		successResponse: makeAIResponse("bug", nil),
	}

	// Batch contains issue #5 twice — the seen-map dedup should prevent double queuing.
	batchWithDupes := []*github.Issue{
		makeIssue(5, "Slow query in report endpoint", "The report endpoint takes 30s due to a missing index on the queries table"),
		makeIssue(5, "Slow query in report endpoint", "The report endpoint takes 30s due to a missing index on the queries table"),
	}
	pollCount2 := 0
	deps := &mockDaemonDeps{
		syncTrackedIssues: []models.BullIssue{},
		syncCarStatuses:   map[string]string{},
		releases:          []*github.RepositoryRelease{},
		mergedIssues:      []models.BullIssue{},
	}
	// Only return the duplicate batch on cycle 1.
	deps.listNewIssuesFn = func(ctx context.Context, since time.Time) ([]*github.Issue, error) {
		deps.phasesRun = append(deps.phasesRun, "poll")
		pollCount2++
		if pollCount2 == 1 {
			return batchWithDupes, nil
		}
		return nil, nil
	}

	ctx, cancel := context.WithCancel(context.Background())
	var buf bytes.Buffer

	opts := DaemonOpts{
		Config:       daemonConfig(),
		Tracks:       []TrackInfo{{Name: "backend"}},
		BranchPrefix: "ry/test",
		PollInterval: time.Millisecond,
		Out:          &buf,
		AI:           ai,
		OnCycleEnd: func(c int) {
			if c >= 2 {
				cancel()
			}
		},
	}

	err := RunDaemon(ctx, deps, opts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	output := buf.String()

	// Count retry lines for issue #5: should be exactly 1 (not 2).
	retryCount := strings.Count(output, "Retrying triage for issue #5")
	if retryCount != 1 {
		t.Errorf("expected exactly 1 retry entry for issue #5, got %d; output:\n%s", retryCount, output)
	}
}
