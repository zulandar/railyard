package bull

import (
	"context"
	"testing"
	"time"

	"github.com/google/go-github/v68/github"
	"github.com/zulandar/railyard/internal/config"
	"github.com/zulandar/railyard/internal/models"
)

// ---------- Mock ReleaseClient ----------

type mockReleaseClient struct {
	releases     []*github.RepositoryRelease
	closedIssues []struct {
		number  int
		comment string
	}
	removedLabels []struct {
		number int
		label  string
	}
}

func (m *mockReleaseClient) ListReleases(ctx context.Context, since time.Time) ([]*github.RepositoryRelease, error) {
	var filtered []*github.RepositoryRelease
	for _, r := range m.releases {
		if r.CreatedAt != nil && r.CreatedAt.Time.After(since) {
			filtered = append(filtered, r)
		}
	}
	return filtered, nil
}

func (m *mockReleaseClient) CloseIssue(ctx context.Context, number int, comment string) error {
	m.closedIssues = append(m.closedIssues, struct {
		number  int
		comment string
	}{number, comment})
	return nil
}

func (m *mockReleaseClient) RemoveLabel(ctx context.Context, number int, label string) error {
	m.removedLabels = append(m.removedLabels, struct {
		number int
		label  string
	}{number, label})
	return nil
}

// ---------- Mock ReleaseStore ----------

type mockReleaseStore struct {
	mergedIssues     []models.BullIssue
	lastReleaseCheck time.Time
	updatedIssues    []struct {
		issueID   uint
		newStatus string
	}
	savedCheckTime *time.Time
}

func (m *mockReleaseStore) GetMergedIssues(ctx context.Context) ([]models.BullIssue, error) {
	return m.mergedIssues, nil
}

func (m *mockReleaseStore) GetLastReleaseCheck(ctx context.Context) (time.Time, error) {
	return m.lastReleaseCheck, nil
}

func (m *mockReleaseStore) SetLastReleaseCheck(ctx context.Context, t time.Time) error {
	m.savedCheckTime = &t
	return nil
}

func (m *mockReleaseStore) UpdateIssueStatus(ctx context.Context, issueID uint, newStatus string) error {
	m.updatedIssues = append(m.updatedIssues, struct {
		issueID   uint
		newStatus string
	}{issueID, newStatus})
	return nil
}

// ---------- Helpers ----------

func releaseBullConfig() config.BullConfig {
	return config.BullConfig{
		Enabled: true,
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

func makeRelease(tag string, createdAt time.Time) *github.RepositoryRelease {
	return &github.RepositoryRelease{
		TagName:   github.Ptr(tag),
		HTMLURL:   github.Ptr("https://github.com/owner/repo/releases/tag/" + tag),
		CreatedAt: &github.Timestamp{Time: createdAt},
	}
}

// ---------- Tests ----------

func TestSyncReleases_NoNewReleases(t *testing.T) {
	now := time.Now()
	client := &mockReleaseClient{
		releases: []*github.RepositoryRelease{
			makeRelease("v0.9.0", now.Add(-48*time.Hour)),
		},
	}
	store := &mockReleaseStore{
		lastReleaseCheck: now.Add(-24 * time.Hour),
		mergedIssues: []models.BullIssue{
			{ID: 1, IssueNumber: 10, CarID: "car-1", LastKnownStatus: "merged"},
		},
	}

	err := SyncReleases(context.Background(), client, store, releaseBullConfig())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(client.closedIssues) != 0 {
		t.Errorf("expected 0 closed issues, got %d", len(client.closedIssues))
	}
	if store.savedCheckTime != nil {
		t.Error("should not update last check time when no new releases")
	}
}

func TestSyncReleases_ClosesFixMergedIssues(t *testing.T) {
	now := time.Now()
	client := &mockReleaseClient{
		releases: []*github.RepositoryRelease{
			makeRelease("v1.0.0", now.Add(-1*time.Hour)),
		},
	}
	store := &mockReleaseStore{
		lastReleaseCheck: now.Add(-24 * time.Hour),
		mergedIssues: []models.BullIssue{
			{ID: 1, IssueNumber: 10, CarID: "car-1", LastKnownStatus: "merged"},
			{ID: 2, IssueNumber: 20, CarID: "car-2", LastKnownStatus: "merged"},
		},
	}
	cfg := releaseBullConfig()

	err := SyncReleases(context.Background(), client, store, cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(client.closedIssues) != 2 {
		t.Fatalf("expected 2 closed issues, got %d", len(client.closedIssues))
	}

	// Verify comment contains release link
	for _, ci := range client.closedIssues {
		if ci.comment == "" {
			t.Errorf("expected non-empty comment for issue #%d", ci.number)
		}
	}

	// Verify fix-merged label removed
	if len(client.removedLabels) != 2 {
		t.Fatalf("expected 2 label removals, got %d", len(client.removedLabels))
	}
	for _, rl := range client.removedLabels {
		if rl.label != "bull: fix merged" {
			t.Errorf("expected fix-merged label removal, got %q", rl.label)
		}
	}

	// Verify status updated to "released"
	if len(store.updatedIssues) != 2 {
		t.Fatalf("expected 2 status updates, got %d", len(store.updatedIssues))
	}
	for _, ui := range store.updatedIssues {
		if ui.newStatus != "released" {
			t.Errorf("expected status 'released', got %q", ui.newStatus)
		}
	}
}

func TestSyncReleases_UpdatesLastCheckTime(t *testing.T) {
	now := time.Now()
	releaseTime := now.Add(-1 * time.Hour)
	client := &mockReleaseClient{
		releases: []*github.RepositoryRelease{
			makeRelease("v1.0.0", releaseTime),
		},
	}
	store := &mockReleaseStore{
		lastReleaseCheck: now.Add(-24 * time.Hour),
		mergedIssues:     []models.BullIssue{},
	}

	err := SyncReleases(context.Background(), client, store, releaseBullConfig())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if store.savedCheckTime == nil {
		t.Fatal("expected last check time to be updated")
	}
	if !store.savedCheckTime.Equal(releaseTime) {
		t.Errorf("expected saved time %v, got %v", releaseTime, *store.savedCheckTime)
	}
}

func TestSyncReleases_UsesLatestReleaseTime(t *testing.T) {
	now := time.Now()
	older := now.Add(-2 * time.Hour)
	newer := now.Add(-1 * time.Hour)
	client := &mockReleaseClient{
		releases: []*github.RepositoryRelease{
			makeRelease("v1.0.0", older),
			makeRelease("v1.1.0", newer),
		},
	}
	store := &mockReleaseStore{
		lastReleaseCheck: now.Add(-24 * time.Hour),
		mergedIssues:     []models.BullIssue{},
	}

	err := SyncReleases(context.Background(), client, store, releaseBullConfig())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if store.savedCheckTime == nil {
		t.Fatal("expected last check time to be updated")
	}
	if !store.savedCheckTime.Equal(newer) {
		t.Errorf("expected saved time %v (latest release), got %v", newer, *store.savedCheckTime)
	}
}

func TestSyncReleases_NoMergedIssues(t *testing.T) {
	now := time.Now()
	client := &mockReleaseClient{
		releases: []*github.RepositoryRelease{
			makeRelease("v1.0.0", now.Add(-1*time.Hour)),
		},
	}
	store := &mockReleaseStore{
		lastReleaseCheck: now.Add(-24 * time.Hour),
		mergedIssues:     []models.BullIssue{},
	}

	err := SyncReleases(context.Background(), client, store, releaseBullConfig())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(client.closedIssues) != 0 {
		t.Errorf("expected 0 closed issues, got %d", len(client.closedIssues))
	}
	// Should still update last check time
	if store.savedCheckTime == nil {
		t.Fatal("expected last check time to be updated even with no merged issues")
	}
}

func TestSyncReleases_FirstRunZeroTime(t *testing.T) {
	now := time.Now()
	client := &mockReleaseClient{
		releases: []*github.RepositoryRelease{
			makeRelease("v1.0.0", now.Add(-1*time.Hour)),
		},
	}
	store := &mockReleaseStore{
		lastReleaseCheck: time.Time{}, // zero value = first run
		mergedIssues: []models.BullIssue{
			{ID: 1, IssueNumber: 10, CarID: "car-1", LastKnownStatus: "merged"},
		},
	}

	err := SyncReleases(context.Background(), client, store, releaseBullConfig())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(client.closedIssues) != 1 {
		t.Fatalf("expected 1 closed issue on first run, got %d", len(client.closedIssues))
	}
	if store.savedCheckTime == nil {
		t.Fatal("expected last check time to be set on first run")
	}
}

func TestSyncReleases_CommentContainsReleaseTag(t *testing.T) {
	now := time.Now()
	client := &mockReleaseClient{
		releases: []*github.RepositoryRelease{
			makeRelease("v2.0.0", now.Add(-1*time.Hour)),
		},
	}
	store := &mockReleaseStore{
		lastReleaseCheck: now.Add(-24 * time.Hour),
		mergedIssues: []models.BullIssue{
			{ID: 1, IssueNumber: 10, CarID: "car-1", LastKnownStatus: "merged"},
		},
	}

	err := SyncReleases(context.Background(), client, store, releaseBullConfig())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(client.closedIssues) != 1 {
		t.Fatalf("expected 1 closed issue, got %d", len(client.closedIssues))
	}

	comment := client.closedIssues[0].comment
	if !containsStr(comment, "v2.0.0") {
		t.Errorf("comment should mention release tag v2.0.0, got %q", comment)
	}
}

func TestSyncReleases_IdempotentReRun(t *testing.T) {
	now := time.Now()
	releaseTime := now.Add(-1 * time.Hour)
	client := &mockReleaseClient{
		releases: []*github.RepositoryRelease{
			makeRelease("v1.0.0", releaseTime),
		},
	}
	store := &mockReleaseStore{
		lastReleaseCheck: now.Add(-24 * time.Hour),
		mergedIssues: []models.BullIssue{
			{ID: 1, IssueNumber: 10, CarID: "car-1", LastKnownStatus: "merged"},
		},
	}
	cfg := releaseBullConfig()

	// First run
	err := SyncReleases(context.Background(), client, store, cfg)
	if err != nil {
		t.Fatalf("first run: unexpected error: %v", err)
	}
	if len(client.closedIssues) != 1 {
		t.Fatalf("first run: expected 1 closed issue, got %d", len(client.closedIssues))
	}

	// Second run: update lastReleaseCheck to saved time, clear merged issues
	// (they'd be "released" now, not "merged")
	store.lastReleaseCheck = *store.savedCheckTime
	store.mergedIssues = nil
	client.closedIssues = nil
	client.removedLabels = nil

	err = SyncReleases(context.Background(), client, store, cfg)
	if err != nil {
		t.Fatalf("second run: unexpected error: %v", err)
	}
	if len(client.closedIssues) != 0 {
		t.Errorf("second run: expected 0 closed issues, got %d", len(client.closedIssues))
	}
}

func containsStr(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && contains(s, substr))
}

func contains(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
