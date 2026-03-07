package bull

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/go-github/v68/github"
	"github.com/zulandar/railyard/internal/config"
	"github.com/zulandar/railyard/internal/models"
)

// integrationTriageConfig returns a config used across integration tests,
// with consistent label names and comments enabled.
func integrationTriageConfig() config.BullConfig {
	return config.BullConfig{
		Enabled:    true,
		TriageMode: "standard",
		Labels: config.BullLabelsConfig{
			UnderReview: "bull: under review",
			InProgress:  "bull: in progress",
			FixMerged:   "bull: fix merged",
			Ignore:      "bull: ignore",
		},
		Comments: config.BullCommentsConfig{
			Enabled:         true,
			AnswerQuestions: true,
		},
	}
}

// integrationTriageOpts builds TriageOpts wired to the given mocks with the
// standard integration config.
func integrationTriageOpts(
	client *mockTriageClient,
	ai *mockTriageAI,
	store *mockTriageStore,
	tracked []ExistingIssue,
) TriageOpts {
	cfg := integrationTriageConfig()
	return TriageOpts{
		Client:       client,
		AI:           ai,
		Store:        store,
		Config:       cfg,
		Tracks:       []string{"backend", "frontend"},
		IgnoreLabel:  cfg.Labels.Ignore,
		Tracked:      tracked,
		CodeContext:  "",
		BranchPrefix: "ry/bull",
	}
}

// ---------------------------------------------------------------------------
// Scenario 1: Bug issue -> car created
// Issue passes heuristic filter, AI classifies as "bug", car created with
// correct fields, under_review label applied, bull_issue recorded.
// ---------------------------------------------------------------------------

func TestIntegration_BugIssueCreatesCarWithCorrectFields(t *testing.T) {
	client := &mockTriageClient{}
	ai := &mockTriageAI{response: makeAIResponse("bug", map[string]interface{}{
		"title":       "Login crash on submit",
		"description": "The login form crashes when the user submits valid credentials.",
		"priority":    1,
		"track":       "backend",
		"acceptance":  "Login succeeds without crash.",
	})}
	store := &mockTriageStore{carIDToReturn: "CAR-INT-BUG"}

	issue := makeIssue(100, "Login form crashes on submit",
		"When I fill in my credentials and hit submit the whole app crashes with a stack trace. This happens every time on the latest version.")

	opts := integrationTriageOpts(client, ai, store, nil)

	// --- Execute full pipeline ---
	outcome, err := ExecuteTriage(context.Background(), issue, opts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Outcome assertions
	if outcome.Action != "created_car" {
		t.Fatalf("expected action 'created_car', got %q", outcome.Action)
	}
	if outcome.Classification != "bug" {
		t.Fatalf("expected classification 'bug', got %q", outcome.Classification)
	}
	if outcome.CarID != "CAR-INT-BUG" {
		t.Fatalf("expected CarID 'CAR-INT-BUG', got %q", outcome.CarID)
	}

	// Car fields
	if len(store.createdCars) != 1 {
		t.Fatalf("expected 1 car created, got %d", len(store.createdCars))
	}
	car := store.createdCars[0]
	if car.Type != "bug" {
		t.Errorf("expected car Type 'bug', got %q", car.Type)
	}
	if car.SourceIssue != 100 {
		t.Errorf("expected SourceIssue 100, got %d", car.SourceIssue)
	}
	if car.Track != "backend" {
		t.Errorf("expected Track 'backend', got %q", car.Track)
	}
	if car.Priority != 1 {
		t.Errorf("expected Priority 1, got %d", car.Priority)
	}
	if car.Title != "Login crash on submit" {
		t.Errorf("expected Title from AI response, got %q", car.Title)
	}

	// Recorded bull_issue
	if len(store.recordedIssues) != 1 {
		t.Fatalf("expected 1 recorded issue, got %d", len(store.recordedIssues))
	}
	if store.recordedIssues[0].CarID != "CAR-INT-BUG" {
		t.Errorf("expected recorded CarID 'CAR-INT-BUG', got %q", store.recordedIssues[0].CarID)
	}
	if store.recordedIssues[0].IssueNumber != 100 {
		t.Errorf("expected recorded IssueNumber 100, got %d", store.recordedIssues[0].IssueNumber)
	}

	// Label: under review
	found := false
	for _, l := range client.labelsAdded {
		if l.Number == 100 && l.Label == "bull: under review" {
			found = true
		}
	}
	if !found {
		t.Error("expected 'bull: under review' label to be applied to issue #100")
	}
}

// ---------------------------------------------------------------------------
// Scenario 2: Feature request -> task car
// ---------------------------------------------------------------------------

func TestIntegration_FeatureRequestCreatesTaskCar(t *testing.T) {
	client := &mockTriageClient{}
	ai := &mockTriageAI{response: makeAIResponse("task", map[string]interface{}{
		"title":       "Add CSV export",
		"description": "Implement a CSV export feature for user data.",
		"priority":    3,
		"track":       "frontend",
		"acceptance":  "Users can export their data as CSV.",
	})}
	store := &mockTriageStore{carIDToReturn: "CAR-INT-TASK"}

	issue := makeIssue(101, "Please add CSV export for user data",
		"It would be great to have a button to export all user data as a CSV file from the dashboard settings page.")

	opts := integrationTriageOpts(client, ai, store, nil)

	outcome, err := ExecuteTriage(context.Background(), issue, opts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if outcome.Action != "created_car" {
		t.Fatalf("expected action 'created_car', got %q", outcome.Action)
	}
	if outcome.Classification != "task" {
		t.Fatalf("expected classification 'task', got %q", outcome.Classification)
	}
	if outcome.CarID != "CAR-INT-TASK" {
		t.Fatalf("expected CarID 'CAR-INT-TASK', got %q", outcome.CarID)
	}

	if len(store.createdCars) != 1 {
		t.Fatalf("expected 1 car, got %d", len(store.createdCars))
	}
	if store.createdCars[0].Type != "task" {
		t.Errorf("expected car Type 'task', got %q", store.createdCars[0].Type)
	}
	if store.createdCars[0].Track != "frontend" {
		t.Errorf("expected Track 'frontend', got %q", store.createdCars[0].Track)
	}

	// Under-review label
	found := false
	for _, l := range client.labelsAdded {
		if l.Number == 101 && l.Label == "bull: under review" {
			found = true
		}
	}
	if !found {
		t.Error("expected 'bull: under review' label on issue #101")
	}
}

// ---------------------------------------------------------------------------
// Scenario 3: Question -> answer posted, ignore label applied
// ---------------------------------------------------------------------------

func TestIntegration_QuestionPostsAnswerAndIgnoreLabel(t *testing.T) {
	client := &mockTriageClient{}
	ai := &mockTriageAI{response: makeAIResponse("question", map[string]interface{}{
		"description": "You can authenticate by passing a Bearer token in the Authorization header.",
	})}
	store := &mockTriageStore{}

	issue := makeIssue(102, "How do I authenticate with the API?",
		"I have been trying to call the API but keep getting 401 errors. What credentials do I need to use?")

	opts := integrationTriageOpts(client, ai, store, nil)

	outcome, err := ExecuteTriage(context.Background(), issue, opts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if outcome.Action != "answered" {
		t.Fatalf("expected action 'answered', got %q", outcome.Action)
	}
	if outcome.Classification != "question" {
		t.Fatalf("expected classification 'question', got %q", outcome.Classification)
	}

	// No car created
	if len(store.createdCars) > 0 {
		t.Fatal("no car should be created for a question")
	}

	// Comment posted with answer
	if len(client.comments) != 1 {
		t.Fatalf("expected 1 comment, got %d", len(client.comments))
	}
	if client.comments[0].Number != 102 {
		t.Errorf("expected comment on issue #102, got #%d", client.comments[0].Number)
	}
	if !strings.Contains(client.comments[0].Body, "Bearer token") {
		t.Errorf("expected comment to contain answer text, got %q", client.comments[0].Body)
	}

	// Ignore label applied
	foundIgnore := false
	for _, l := range client.labelsAdded {
		if l.Number == 102 && l.Label == "bull: ignore" {
			foundIgnore = true
		}
	}
	if !foundIgnore {
		t.Error("expected 'bull: ignore' label on issue #102")
	}
}

// ---------------------------------------------------------------------------
// Scenario 4: Spam -> heuristic reject (empty body filtered before AI)
// ---------------------------------------------------------------------------

func TestIntegration_SpamHeuristicRejectBeforeAI(t *testing.T) {
	client := &mockTriageClient{}
	ai := &mockTriageAI{response: makeAIResponse("bug", nil)} // should never be called
	store := &mockTriageStore{carIDToReturn: "SHOULD-NOT-EXIST"}

	// Empty body triggers heuristic filter (< 20 chars)
	issue := makeIssue(103, "Buy cheap stuff", "")

	opts := integrationTriageOpts(client, ai, store, nil)

	outcome, err := ExecuteTriage(context.Background(), issue, opts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if outcome.Action != "filtered" {
		t.Fatalf("expected action 'filtered', got %q", outcome.Action)
	}
	if outcome.FilterReason == "" {
		t.Fatal("expected FilterReason to be set")
	}

	// AI must not have been called
	if ai.called {
		t.Error("AI should not be called when heuristic filter rejects the issue")
	}

	// No car created
	if len(store.createdCars) > 0 {
		t.Error("no car should be created when issue is filtered")
	}

	// No labels or comments
	if len(client.labelsAdded) > 0 {
		t.Error("no labels should be added when issue is filtered")
	}
	if len(client.comments) > 0 {
		t.Error("no comments should be posted when issue is filtered")
	}
}

// ---------------------------------------------------------------------------
// Scenario 5: Issue with ignore label -> skipped by FilterIssue
// ---------------------------------------------------------------------------

func TestIntegration_IgnoreLabelSkipped(t *testing.T) {
	client := &mockTriageClient{}
	ai := &mockTriageAI{response: makeAIResponse("bug", nil)}
	store := &mockTriageStore{carIDToReturn: "SHOULD-NOT-EXIST"}

	issue := &github.Issue{
		Number: github.Ptr(104),
		Title:  github.Ptr("Already handled issue"),
		Body:   github.Ptr("This issue has a sufficiently long body to pass length checks"),
		User:   &github.User{Login: github.Ptr("testuser")},
		Labels: []*github.Label{
			{Name: github.Ptr("bull: ignore")},
		},
	}

	opts := integrationTriageOpts(client, ai, store, nil)

	outcome, err := ExecuteTriage(context.Background(), issue, opts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if outcome.Action != "filtered" {
		t.Fatalf("expected action 'filtered', got %q", outcome.Action)
	}
	if !strings.Contains(outcome.FilterReason, "ignore label") {
		t.Errorf("expected filter reason to mention ignore label, got %q", outcome.FilterReason)
	}

	if ai.called {
		t.Error("AI should not be called for issues with ignore label")
	}
	if len(store.createdCars) > 0 {
		t.Error("no car should be created for ignored issues")
	}
}

// ---------------------------------------------------------------------------
// Scenario 6: Duplicate issue -> rejected by heuristic filter
// ---------------------------------------------------------------------------

func TestIntegration_DuplicateIssueRejected(t *testing.T) {
	client := &mockTriageClient{}
	ai := &mockTriageAI{response: makeAIResponse("bug", nil)}
	store := &mockTriageStore{carIDToReturn: "SHOULD-NOT-EXIST"}

	issue := makeIssue(105, "Database connection timeout",
		"The database connection keeps timing out when under heavy load in the production environment.")

	tracked := []ExistingIssue{
		{IssueNumber: 50, Title: "database connection timeout"},
	}
	opts := integrationTriageOpts(client, ai, store, tracked)

	outcome, err := ExecuteTriage(context.Background(), issue, opts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if outcome.Action != "filtered" {
		t.Fatalf("expected action 'filtered', got %q", outcome.Action)
	}
	if !strings.Contains(outcome.FilterReason, "duplicate") {
		t.Errorf("expected filter reason to mention duplicate, got %q", outcome.FilterReason)
	}

	if ai.called {
		t.Error("AI should not be called for duplicate issues")
	}
	if len(store.createdCars) > 0 {
		t.Error("no car should be created for duplicate issues")
	}
}

// ---------------------------------------------------------------------------
// Scenario 7: Car status open -> in_progress -> InProgress label applied
// ---------------------------------------------------------------------------

func TestIntegration_CarStatusOpenToInProgressUpdatesLabel(t *testing.T) {
	client := &mockSyncClient{}
	store := &mockSyncStore{
		issues: []models.BullIssue{
			{ID: 1, IssueNumber: 200, CarID: "car-200", LastKnownStatus: "open"},
		},
		carStatuses: map[string]string{"car-200": "in_progress"},
	}
	cfg := integrationTriageConfig()

	err := SyncCarStatuses(context.Background(), client, store, cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// InProgress label added
	if !hasLabel(client.addedLabels, 200, "bull: in progress") {
		t.Error("expected 'bull: in progress' label to be added to issue #200")
	}

	// Status updated in store
	if len(store.updatedIssues) != 1 {
		t.Fatalf("expected 1 status update, got %d", len(store.updatedIssues))
	}
	if store.updatedIssues[0].issueID != 1 {
		t.Errorf("expected issueID 1, got %d", store.updatedIssues[0].issueID)
	}
	if store.updatedIssues[0].newStatus != "in_progress" {
		t.Errorf("expected newStatus 'in_progress', got %q", store.updatedIssues[0].newStatus)
	}
}

// ---------------------------------------------------------------------------
// Scenario 8: Car merged -> FixMerged label applied with comment
// ---------------------------------------------------------------------------

func TestIntegration_CarMergedAppliesFixMergedLabelWithComment(t *testing.T) {
	client := &mockSyncClient{}
	store := &mockSyncStore{
		issues: []models.BullIssue{
			{ID: 2, IssueNumber: 201, CarID: "car-201", LastKnownStatus: "open"},
		},
		carStatuses: map[string]string{"car-201": "merged"},
	}
	cfg := integrationTriageConfig()

	err := SyncCarStatuses(context.Background(), client, store, cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// FixMerged label added
	if !hasLabel(client.addedLabels, 201, "bull: fix merged") {
		t.Error("expected 'bull: fix merged' label to be added to issue #201")
	}

	// InProgress label removed
	if !hasLabel(client.removedLabels, 201, "bull: in progress") {
		t.Error("expected 'bull: in progress' label to be removed from issue #201")
	}

	// Comment posted (comments enabled)
	if !hasComment(client.comments, 201) {
		t.Error("expected a merge notification comment on issue #201")
	}
	if len(client.comments) != 1 {
		t.Fatalf("expected 1 comment, got %d", len(client.comments))
	}
	if !strings.Contains(client.comments[0].body, "merged") {
		t.Errorf("expected comment to mention 'merged', got %q", client.comments[0].body)
	}

	// Status updated
	if len(store.updatedIssues) != 1 {
		t.Fatalf("expected 1 status update, got %d", len(store.updatedIssues))
	}
	if store.updatedIssues[0].newStatus != "merged" {
		t.Errorf("expected newStatus 'merged', got %q", store.updatedIssues[0].newStatus)
	}
}

// ---------------------------------------------------------------------------
// Scenario 9: Car cancelled -> all labels removed with comment
// ---------------------------------------------------------------------------

func TestIntegration_CarCancelledRemovesAllLabelsWithComment(t *testing.T) {
	client := &mockSyncClient{}
	store := &mockSyncStore{
		issues: []models.BullIssue{
			{ID: 3, IssueNumber: 202, CarID: "car-202", LastKnownStatus: "open"},
		},
		carStatuses: map[string]string{"car-202": "cancelled"},
	}
	cfg := integrationTriageConfig()

	err := SyncCarStatuses(context.Background(), client, store, cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// All 4 bull labels should be removed
	if len(client.removedLabels) != 4 {
		t.Fatalf("expected 4 label removals for cancelled, got %d", len(client.removedLabels))
	}
	expectedRemoved := []string{"bull: under review", "bull: in progress", "bull: fix merged", "bull: ignore"}
	for _, expected := range expectedRemoved {
		if !hasLabel(client.removedLabels, 202, expected) {
			t.Errorf("expected label %q to be removed from issue #202", expected)
		}
	}

	// No labels should be added
	if len(client.addedLabels) != 0 {
		t.Errorf("expected 0 labels added for cancelled, got %d", len(client.addedLabels))
	}

	// Comment posted
	if !hasComment(client.comments, 202) {
		t.Error("expected a cancellation comment on issue #202")
	}
	if len(client.comments) != 1 {
		t.Fatalf("expected 1 comment, got %d", len(client.comments))
	}
	if !strings.Contains(client.comments[0].body, "cancelled") {
		t.Errorf("expected comment to mention 'cancelled', got %q", client.comments[0].body)
	}

	// Status updated
	if len(store.updatedIssues) != 1 {
		t.Fatalf("expected 1 status update, got %d", len(store.updatedIssues))
	}
	if store.updatedIssues[0].newStatus != "cancelled" {
		t.Errorf("expected newStatus 'cancelled', got %q", store.updatedIssues[0].newStatus)
	}
}

// ---------------------------------------------------------------------------
// Scenario 10: New release -> fix-merged issues closed
// ---------------------------------------------------------------------------

func TestIntegration_NewReleaseClosesFixMergedIssues(t *testing.T) {
	now := time.Now()
	releaseTime := now.Add(-1 * time.Hour)

	client := &mockReleaseClient{
		releases: []*github.RepositoryRelease{
			makeRelease("v3.0.0", releaseTime),
		},
	}
	store := &mockReleaseStore{
		lastReleaseCheck: now.Add(-24 * time.Hour),
		mergedIssues: []models.BullIssue{
			{ID: 10, IssueNumber: 300, CarID: "car-300", LastKnownStatus: "merged"},
			{ID: 11, IssueNumber: 301, CarID: "car-301", LastKnownStatus: "merged"},
		},
	}
	cfg := integrationTriageConfig()

	err := SyncReleases(context.Background(), client, store, cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Both issues closed
	if len(client.closedIssues) != 2 {
		t.Fatalf("expected 2 closed issues, got %d", len(client.closedIssues))
	}
	closedNumbers := map[int]bool{}
	for _, ci := range client.closedIssues {
		closedNumbers[ci.number] = true
		// Comment should reference the release tag
		if !strings.Contains(ci.comment, "v3.0.0") {
			t.Errorf("expected close comment to mention release tag v3.0.0, got %q", ci.comment)
		}
	}
	if !closedNumbers[300] {
		t.Error("expected issue #300 to be closed")
	}
	if !closedNumbers[301] {
		t.Error("expected issue #301 to be closed")
	}

	// fix-merged label removed from both
	if len(client.removedLabels) != 2 {
		t.Fatalf("expected 2 label removals, got %d", len(client.removedLabels))
	}
	for _, rl := range client.removedLabels {
		if rl.label != "bull: fix merged" {
			t.Errorf("expected 'bull: fix merged' label removal, got %q", rl.label)
		}
	}

	// Status updated to "released"
	if len(store.updatedIssues) != 2 {
		t.Fatalf("expected 2 status updates, got %d", len(store.updatedIssues))
	}
	for _, ui := range store.updatedIssues {
		if ui.newStatus != "released" {
			t.Errorf("expected status 'released', got %q", ui.newStatus)
		}
	}

	// Last release check time saved
	if store.savedCheckTime == nil {
		t.Fatal("expected last release check time to be saved")
	}
	if !store.savedCheckTime.Equal(releaseTime) {
		t.Errorf("expected saved check time %v, got %v", releaseTime, *store.savedCheckTime)
	}
}
