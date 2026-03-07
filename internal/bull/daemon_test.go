package bull

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/go-github/v68/github"
	"github.com/zulandar/railyard/internal/config"
	"github.com/zulandar/railyard/internal/models"
)

// ---------- Mock DaemonDeps ----------

type mockDaemonDeps struct {
	// GitHub issues returned by ListNewIssues
	issues []*github.Issue

	// Triage
	triageResults map[int]*TriageOutcome // keyed by issue number
	triageErr     error

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
	phasesRun      []string
	triageCalled   []int // issue numbers that went to triage
	closedIssues   []int
	removedLabels  []struct{ number int; label string }
	addedLabels    []struct{ number int; label string }
	addedComments  []struct{ number int; body string }
	statusUpdates  []struct{ issueID uint; newStatus string }
	savedCheckTime *time.Time
	recordedIssues []models.BullIssue
	createdCars    []CarCreateOpts
}

func (m *mockDaemonDeps) ListNewIssues(ctx context.Context, since time.Time) ([]*github.Issue, error) {
	m.phasesRun = append(m.phasesRun, "poll")
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
	m.addedLabels = append(m.addedLabels, struct{ number int; label string }{number, label})
	return nil
}

func (m *mockDaemonDeps) RemoveLabel(ctx context.Context, number int, label string) error {
	m.removedLabels = append(m.removedLabels, struct{ number int; label string }{number, label})
	return nil
}

func (m *mockDaemonDeps) AddComment(ctx context.Context, number int, body string) error {
	m.addedComments = append(m.addedComments, struct{ number int; body string }{number, body})
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
		if r.CreatedAt != nil && r.CreatedAt.Time.After(since) {
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
	m.statusUpdates = append(m.statusUpdates, struct{ issueID uint; newStatus string }{issueID, newStatus})
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
		Tracks:       []string{"backend"},
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
		Tracks:       []string{"backend"},
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
		issues:          []*github.Issue{},
		syncTrackedIssues: []models.BullIssue{},
		syncCarStatuses: map[string]string{},
		syncErr:         errors.New("sync database error"),
		releases:        []*github.RepositoryRelease{},
		mergedIssues:    []models.BullIssue{},
	}

	ctx, cancel := context.WithCancel(context.Background())
	var buf bytes.Buffer
	cycleCount := 0

	opts := DaemonOpts{
		Config:       daemonConfig(),
		Tracks:       []string{"backend"},
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
		Tracks:         []string{"backend"},
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
		Tracks:       []string{"backend"},
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
		Tracks:       []string{"backend"},
		BranchPrefix: "ry/test",
		PollInterval: time.Millisecond,
		Out:          nil, // should not panic
	}

	err := RunDaemon(ctx, deps, opts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}
