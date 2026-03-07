package bull

import (
	"context"
	"fmt"
	"testing"

	"github.com/zulandar/railyard/internal/config"
	"github.com/zulandar/railyard/internal/models"
)

// ---------- Mock SyncClient ----------

type mockSyncClient struct {
	addedLabels   []struct{ number int; label string }
	removedLabels []struct{ number int; label string }
	comments      []struct{ number int; body string }
}

func (m *mockSyncClient) AddLabel(_ context.Context, number int, label string) error {
	m.addedLabels = append(m.addedLabels, struct{ number int; label string }{number, label})
	return nil
}

func (m *mockSyncClient) RemoveLabel(_ context.Context, number int, label string) error {
	m.removedLabels = append(m.removedLabels, struct{ number int; label string }{number, label})
	return nil
}

func (m *mockSyncClient) AddComment(_ context.Context, number int, body string) error {
	m.comments = append(m.comments, struct{ number int; body string }{number, body})
	return nil
}

// ---------- Mock SyncStore ----------

type mockSyncStore struct {
	issues        []models.BullIssue
	carStatuses   map[string]string
	updatedIssues []struct {
		issueID   uint
		newStatus string
	}
}

func (m *mockSyncStore) GetTrackedIssues(_ context.Context) ([]models.BullIssue, error) {
	return m.issues, nil
}

func (m *mockSyncStore) GetCarStatus(_ context.Context, carID string) (string, error) {
	status, ok := m.carStatuses[carID]
	if !ok {
		return "", fmt.Errorf("car %q not found", carID)
	}
	return status, nil
}

func (m *mockSyncStore) UpdateIssueStatus(_ context.Context, issueID uint, newStatus string) error {
	m.updatedIssues = append(m.updatedIssues, struct {
		issueID   uint
		newStatus string
	}{issueID, newStatus})
	return nil
}

// ---------- Helpers ----------

func testBullConfig(commentsEnabled bool) config.BullConfig {
	return config.BullConfig{
		Enabled: true,
		Labels: config.BullLabelsConfig{
			UnderReview: "bull: under review",
			InProgress:  "bull: in progress",
			FixMerged:   "bull: fix merged",
			Ignore:      "bull: ignore",
		},
		Comments: config.BullCommentsConfig{
			Enabled: commentsEnabled,
		},
	}
}

func hasLabel(labels []struct{ number int; label string }, number int, label string) bool {
	for _, l := range labels {
		if l.number == number && l.label == label {
			return true
		}
	}
	return false
}

func hasComment(comments []struct{ number int; body string }, number int) bool {
	for _, c := range comments {
		if c.number == number {
			return true
		}
	}
	return false
}

// ---------- Tests ----------

func TestSyncCarStatuses_NoChangeWhenStatusMatches(t *testing.T) {
	client := &mockSyncClient{}
	store := &mockSyncStore{
		issues: []models.BullIssue{
			{ID: 1, IssueNumber: 10, CarID: "car-1", LastKnownStatus: "open"},
		},
		carStatuses: map[string]string{"car-1": "open"},
	}
	cfg := testBullConfig(true)

	err := SyncCarStatuses(context.Background(), client, store, cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(client.addedLabels) != 0 {
		t.Errorf("expected 0 AddLabel calls, got %d", len(client.addedLabels))
	}
	if len(client.removedLabels) != 0 {
		t.Errorf("expected 0 RemoveLabel calls, got %d", len(client.removedLabels))
	}
	if len(client.comments) != 0 {
		t.Errorf("expected 0 AddComment calls, got %d", len(client.comments))
	}
	if len(store.updatedIssues) != 0 {
		t.Errorf("expected 0 UpdateIssueStatus calls, got %d", len(store.updatedIssues))
	}
}

func TestSyncCarStatuses_DraftToOpenTransition(t *testing.T) {
	client := &mockSyncClient{}
	store := &mockSyncStore{
		issues: []models.BullIssue{
			{ID: 1, IssueNumber: 10, CarID: "car-1", LastKnownStatus: "draft"},
		},
		carStatuses: map[string]string{"car-1": "open"},
	}
	cfg := testBullConfig(true)

	err := SyncCarStatuses(context.Background(), client, store, cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !hasLabel(client.addedLabels, 10, "bull: in progress") {
		t.Error("expected InProgress label to be added")
	}
	if !hasLabel(client.removedLabels, 10, "bull: under review") {
		t.Error("expected UnderReview label to be removed")
	}
	if len(store.updatedIssues) != 1 {
		t.Fatalf("expected 1 UpdateIssueStatus call, got %d", len(store.updatedIssues))
	}
	if store.updatedIssues[0].issueID != 1 || store.updatedIssues[0].newStatus != "open" {
		t.Errorf("unexpected update: %+v", store.updatedIssues[0])
	}
}

func TestSyncCarStatuses_OpenToMergedWithComments(t *testing.T) {
	client := &mockSyncClient{}
	store := &mockSyncStore{
		issues: []models.BullIssue{
			{ID: 2, IssueNumber: 20, CarID: "car-2", LastKnownStatus: "open"},
		},
		carStatuses: map[string]string{"car-2": "merged"},
	}
	cfg := testBullConfig(true)

	err := SyncCarStatuses(context.Background(), client, store, cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !hasLabel(client.addedLabels, 20, "bull: fix merged") {
		t.Error("expected FixMerged label to be added")
	}
	if !hasLabel(client.removedLabels, 20, "bull: in progress") {
		t.Error("expected InProgress label to be removed")
	}
	if !hasComment(client.comments, 20) {
		t.Error("expected a comment to be posted for merged transition")
	}
	if len(store.updatedIssues) != 1 {
		t.Fatalf("expected 1 UpdateIssueStatus call, got %d", len(store.updatedIssues))
	}
	if store.updatedIssues[0].newStatus != "merged" {
		t.Errorf("expected status update to 'merged', got %q", store.updatedIssues[0].newStatus)
	}
}

func TestSyncCarStatuses_OpenToMergedCommentsDisabled(t *testing.T) {
	client := &mockSyncClient{}
	store := &mockSyncStore{
		issues: []models.BullIssue{
			{ID: 2, IssueNumber: 20, CarID: "car-2", LastKnownStatus: "open"},
		},
		carStatuses: map[string]string{"car-2": "merged"},
	}
	cfg := testBullConfig(false)

	err := SyncCarStatuses(context.Background(), client, store, cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !hasLabel(client.addedLabels, 20, "bull: fix merged") {
		t.Error("expected FixMerged label to be added")
	}
	if !hasLabel(client.removedLabels, 20, "bull: in progress") {
		t.Error("expected InProgress label to be removed")
	}
	if len(client.comments) != 0 {
		t.Errorf("expected 0 comments when disabled, got %d", len(client.comments))
	}
}

func TestSyncCarStatuses_OpenToCancelledWithComments(t *testing.T) {
	client := &mockSyncClient{}
	store := &mockSyncStore{
		issues: []models.BullIssue{
			{ID: 3, IssueNumber: 30, CarID: "car-3", LastKnownStatus: "open"},
		},
		carStatuses: map[string]string{"car-3": "cancelled"},
	}
	cfg := testBullConfig(true)

	err := SyncCarStatuses(context.Background(), client, store, cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Cancelled should remove all bull labels (4 labels)
	if len(client.removedLabels) != 4 {
		t.Errorf("expected 4 RemoveLabel calls for cancelled, got %d", len(client.removedLabels))
	}
	if !hasComment(client.comments, 30) {
		t.Error("expected a cancellation comment to be posted")
	}
	if len(store.updatedIssues) != 1 {
		t.Fatalf("expected 1 UpdateIssueStatus call, got %d", len(store.updatedIssues))
	}
	if store.updatedIssues[0].newStatus != "cancelled" {
		t.Errorf("expected status update to 'cancelled', got %q", store.updatedIssues[0].newStatus)
	}
}

func TestSyncCarStatuses_CancelledCommentsDisabled(t *testing.T) {
	client := &mockSyncClient{}
	store := &mockSyncStore{
		issues: []models.BullIssue{
			{ID: 3, IssueNumber: 30, CarID: "car-3", LastKnownStatus: "open"},
		},
		carStatuses: map[string]string{"car-3": "cancelled"},
	}
	cfg := testBullConfig(false)

	err := SyncCarStatuses(context.Background(), client, store, cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(client.removedLabels) != 4 {
		t.Errorf("expected 4 RemoveLabel calls, got %d", len(client.removedLabels))
	}
	if len(client.comments) != 0 {
		t.Errorf("expected 0 comments when disabled, got %d", len(client.comments))
	}
}

func TestSyncCarStatuses_IdempotentOnRepeatedRuns(t *testing.T) {
	client := &mockSyncClient{}
	store := &mockSyncStore{
		issues: []models.BullIssue{
			{ID: 1, IssueNumber: 10, CarID: "car-1", LastKnownStatus: "merged"},
		},
		carStatuses: map[string]string{"car-1": "merged"},
	}
	cfg := testBullConfig(true)

	// First run: no change since status matches
	err := SyncCarStatuses(context.Background(), client, store, cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(client.addedLabels) != 0 {
		t.Errorf("run1: expected 0 AddLabel calls, got %d", len(client.addedLabels))
	}
	if len(client.removedLabels) != 0 {
		t.Errorf("run1: expected 0 RemoveLabel calls, got %d", len(client.removedLabels))
	}
	if len(client.comments) != 0 {
		t.Errorf("run1: expected 0 AddComment calls, got %d", len(client.comments))
	}

	// Second run: still no change
	err = SyncCarStatuses(context.Background(), client, store, cfg)
	if err != nil {
		t.Fatalf("unexpected error on second run: %v", err)
	}
	if len(client.addedLabels) != 0 {
		t.Errorf("run2: expected 0 AddLabel calls, got %d", len(client.addedLabels))
	}
}

func TestSyncCarStatuses_MultipleIssues(t *testing.T) {
	client := &mockSyncClient{}
	store := &mockSyncStore{
		issues: []models.BullIssue{
			{ID: 1, IssueNumber: 10, CarID: "car-1", LastKnownStatus: "draft"},
			{ID: 2, IssueNumber: 20, CarID: "car-2", LastKnownStatus: "open"},
			{ID: 3, IssueNumber: 30, CarID: "car-3", LastKnownStatus: "open"},
		},
		carStatuses: map[string]string{
			"car-1": "ready",     // draft→ready: apply InProgress, remove UnderReview
			"car-2": "merged",    // open→merged: apply FixMerged, remove InProgress, comment
			"car-3": "open",      // no change
		},
	}
	cfg := testBullConfig(true)

	err := SyncCarStatuses(context.Background(), client, store, cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// car-1 (issue 10): draft→ready → InProgress added, UnderReview removed
	if !hasLabel(client.addedLabels, 10, "bull: in progress") {
		t.Error("car-1: expected InProgress label added")
	}
	if !hasLabel(client.removedLabels, 10, "bull: under review") {
		t.Error("car-1: expected UnderReview label removed")
	}

	// car-2 (issue 20): open→merged → FixMerged added, InProgress removed, comment
	if !hasLabel(client.addedLabels, 20, "bull: fix merged") {
		t.Error("car-2: expected FixMerged label added")
	}
	if !hasLabel(client.removedLabels, 20, "bull: in progress") {
		t.Error("car-2: expected InProgress label removed")
	}
	if !hasComment(client.comments, 20) {
		t.Error("car-2: expected merge comment")
	}

	// car-3 (issue 30): no change
	if hasLabel(client.addedLabels, 30, "bull: in progress") {
		t.Error("car-3: should not have added any labels (no status change)")
	}

	// Should have 2 status updates (car-1 and car-2), not car-3
	if len(store.updatedIssues) != 2 {
		t.Errorf("expected 2 UpdateIssueStatus calls, got %d", len(store.updatedIssues))
	}
}

func TestSyncCarStatuses_SkipsIssueWithNoCarID(t *testing.T) {
	client := &mockSyncClient{}
	store := &mockSyncStore{
		issues: []models.BullIssue{
			{ID: 1, IssueNumber: 10, CarID: "", LastKnownStatus: ""},
		},
		carStatuses: map[string]string{},
	}
	cfg := testBullConfig(true)

	err := SyncCarStatuses(context.Background(), client, store, cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(client.addedLabels) != 0 {
		t.Errorf("expected 0 AddLabel calls, got %d", len(client.addedLabels))
	}
	if len(store.updatedIssues) != 0 {
		t.Errorf("expected 0 UpdateIssueStatus calls, got %d", len(store.updatedIssues))
	}
}

func TestSyncCarStatuses_InProgressStatusKeepsLabel(t *testing.T) {
	client := &mockSyncClient{}
	store := &mockSyncStore{
		issues: []models.BullIssue{
			{ID: 1, IssueNumber: 10, CarID: "car-1", LastKnownStatus: "open"},
		},
		carStatuses: map[string]string{"car-1": "in_progress"},
	}
	cfg := testBullConfig(true)

	err := SyncCarStatuses(context.Background(), client, store, cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !hasLabel(client.addedLabels, 10, "bull: in progress") {
		t.Error("expected InProgress label to be applied for in_progress status")
	}
	if len(store.updatedIssues) != 1 {
		t.Fatalf("expected 1 UpdateIssueStatus call, got %d", len(store.updatedIssues))
	}
	if store.updatedIssues[0].newStatus != "in_progress" {
		t.Errorf("expected status update to 'in_progress', got %q", store.updatedIssues[0].newStatus)
	}
}
