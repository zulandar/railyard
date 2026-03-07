package bull

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/google/go-github/v68/github"
	"github.com/zulandar/railyard/internal/config"
	"github.com/zulandar/railyard/internal/models"
)

// --- Mock implementations ---

type mockTriageClient struct {
	comments      []mockComment
	labelsAdded   []mockLabel
	labelsRemoved []mockLabel
}

type mockComment struct {
	Number int
	Body   string
}

type mockLabel struct {
	Number int
	Label  string
}

func (m *mockTriageClient) AddComment(_ context.Context, number int, body string) error {
	m.comments = append(m.comments, mockComment{Number: number, Body: body})
	return nil
}

func (m *mockTriageClient) AddLabel(_ context.Context, number int, label string) error {
	m.labelsAdded = append(m.labelsAdded, mockLabel{Number: number, Label: label})
	return nil
}

func (m *mockTriageClient) RemoveLabel(_ context.Context, number int, label string) error {
	m.labelsRemoved = append(m.labelsRemoved, mockLabel{Number: number, Label: label})
	return nil
}

type mockTriageAI struct {
	response string
	err      error
	called   bool
}

func (m *mockTriageAI) RunPrompt(_ context.Context, _ string) (string, error) {
	m.called = true
	return m.response, m.err
}

type mockTriageStore struct {
	createdCars    []CarCreateOpts
	recordedIssues []models.BullIssue
	carIDToReturn  string
	createErr      error
	recordErr      error
}

func (m *mockTriageStore) CreateCar(_ context.Context, opts CarCreateOpts) (string, error) {
	m.createdCars = append(m.createdCars, opts)
	if m.createErr != nil {
		return "", m.createErr
	}
	return m.carIDToReturn, nil
}

func (m *mockTriageStore) RecordTriagedIssue(_ context.Context, issue models.BullIssue) error {
	m.recordedIssues = append(m.recordedIssues, issue)
	return m.recordErr
}

// --- Helper functions ---

func makeAIResponse(classification string, extra map[string]interface{}) string {
	m := map[string]interface{}{
		"classification": classification,
		"priority":       2,
		"track":          "backend",
		"title":          "Test Title",
		"description":    "Test description",
		"acceptance":     "Test acceptance",
	}
	for k, v := range extra {
		m[k] = v
	}
	b, _ := json.Marshal(m)
	return string(b)
}

func makeIssue(number int, title, body string) *github.Issue {
	return &github.Issue{
		Number: github.Ptr(number),
		Title:  github.Ptr(title),
		Body:   github.Ptr(body),
		User:   &github.User{Login: github.Ptr("testuser")},
	}
}

func defaultOpts(client *mockTriageClient, ai *mockTriageAI, store *mockTriageStore) TriageOpts {
	return TriageOpts{
		Client: client,
		AI:     ai,
		Store:  store,
		Config: config.BullConfig{
			Enabled:    true,
			TriageMode: "standard",
			Comments: config.BullCommentsConfig{
				Enabled:         true,
				AnswerQuestions: true,
			},
			Labels: config.BullLabelsConfig{
				UnderReview: "bull:under-review",
				Ignore:      "bull:ignore",
			},
		},
		Tracks:       []string{"backend", "frontend"},
		IgnoreLabel:  "bull:ignore",
		Tracked:      nil,
		CodeContext:  "",
		BranchPrefix: "ry/bull",
	}
}

// --- Tests ---

func TestExecuteTriage_FilteredByHeuristic(t *testing.T) {
	client := &mockTriageClient{}
	ai := &mockTriageAI{response: makeAIResponse("bug", nil)}
	store := &mockTriageStore{carIDToReturn: "CAR-001"}

	// Short body triggers heuristic filter rejection
	issue := makeIssue(42, "short", "x")
	opts := defaultOpts(client, ai, store)

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
	if ai.called {
		t.Fatal("AI should not have been called when issue is filtered")
	}
	if len(store.createdCars) > 0 {
		t.Fatal("no car should be created when issue is filtered")
	}
}

func TestExecuteTriage_BugClassification(t *testing.T) {
	client := &mockTriageClient{}
	ai := &mockTriageAI{response: makeAIResponse("bug", nil)}
	store := &mockTriageStore{carIDToReturn: "CAR-BUG"}

	issue := makeIssue(10, "App crashes on login", "The application crashes when I try to log in with valid credentials on the latest version")
	opts := defaultOpts(client, ai, store)

	outcome, err := ExecuteTriage(context.Background(), issue, opts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if outcome.Action != "created_car" {
		t.Fatalf("expected action 'created_car', got %q", outcome.Action)
	}
	if outcome.CarID != "CAR-BUG" {
		t.Fatalf("expected CarID 'CAR-BUG', got %q", outcome.CarID)
	}
	if outcome.Classification != "bug" {
		t.Fatalf("expected classification 'bug', got %q", outcome.Classification)
	}

	// Verify car was created
	if len(store.createdCars) != 1 {
		t.Fatalf("expected 1 car created, got %d", len(store.createdCars))
	}
	if store.createdCars[0].SourceIssue != 10 {
		t.Errorf("expected SourceIssue 10, got %d", store.createdCars[0].SourceIssue)
	}
	if store.createdCars[0].Type != "bug" {
		t.Errorf("expected Type 'bug', got %q", store.createdCars[0].Type)
	}

	// Verify bull_issue recorded
	if len(store.recordedIssues) != 1 {
		t.Fatalf("expected 1 recorded issue, got %d", len(store.recordedIssues))
	}
	if store.recordedIssues[0].IssueNumber != 10 {
		t.Errorf("expected IssueNumber 10, got %d", store.recordedIssues[0].IssueNumber)
	}
	if store.recordedIssues[0].CarID != "CAR-BUG" {
		t.Errorf("expected CarID 'CAR-BUG', got %q", store.recordedIssues[0].CarID)
	}

	// Verify under_review label applied
	found := false
	for _, l := range client.labelsAdded {
		if l.Label == "bull:under-review" && l.Number == 10 {
			found = true
		}
	}
	if !found {
		t.Error("expected under_review label to be applied")
	}
}

func TestExecuteTriage_TaskClassification(t *testing.T) {
	client := &mockTriageClient{}
	ai := &mockTriageAI{response: makeAIResponse("task", nil)}
	store := &mockTriageStore{carIDToReturn: "CAR-TASK"}

	issue := makeIssue(11, "Add pagination to API", "We need to add pagination support to the API endpoint for listing users in the admin dashboard")
	opts := defaultOpts(client, ai, store)

	outcome, err := ExecuteTriage(context.Background(), issue, opts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if outcome.Action != "created_car" {
		t.Fatalf("expected action 'created_car', got %q", outcome.Action)
	}
	if outcome.CarID != "CAR-TASK" {
		t.Fatalf("expected CarID 'CAR-TASK', got %q", outcome.CarID)
	}
	if outcome.Classification != "task" {
		t.Fatalf("expected classification 'task', got %q", outcome.Classification)
	}
	if len(store.createdCars) != 1 {
		t.Fatalf("expected 1 car created, got %d", len(store.createdCars))
	}
	if store.createdCars[0].Type != "task" {
		t.Errorf("expected Type 'task', got %q", store.createdCars[0].Type)
	}
}

func TestExecuteTriage_QuestionClassification(t *testing.T) {
	client := &mockTriageClient{}
	ai := &mockTriageAI{response: makeAIResponse("question", map[string]interface{}{
		"description": "The answer to your question is: use the /api/v2 endpoint.",
	})}
	store := &mockTriageStore{}

	issue := makeIssue(12, "How do I use the API?", "I cannot figure out how to authenticate with the API. Can someone help me understand the process?")
	opts := defaultOpts(client, ai, store)

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

	// No car should be created
	if len(store.createdCars) > 0 {
		t.Fatal("no car should be created for a question")
	}

	// Comment should be posted
	if len(client.comments) != 1 {
		t.Fatalf("expected 1 comment, got %d", len(client.comments))
	}
	if client.comments[0].Number != 12 {
		t.Errorf("expected comment on issue 12, got %d", client.comments[0].Number)
	}

	// Ignore label should be applied
	found := false
	for _, l := range client.labelsAdded {
		if l.Label == "bull:ignore" && l.Number == 12 {
			found = true
		}
	}
	if !found {
		t.Error("expected ignore label to be applied")
	}
}

func TestExecuteTriage_QuestionCommentsDisabled(t *testing.T) {
	client := &mockTriageClient{}
	ai := &mockTriageAI{response: makeAIResponse("question", nil)}
	store := &mockTriageStore{}

	issue := makeIssue(13, "How do I deploy?", "I need help understanding the deployment process for this application and its dependencies")
	opts := defaultOpts(client, ai, store)
	opts.Config.Comments.Enabled = false

	outcome, err := ExecuteTriage(context.Background(), issue, opts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if outcome.Action != "answered" {
		t.Fatalf("expected action 'answered', got %q", outcome.Action)
	}

	// No comment should be posted
	if len(client.comments) > 0 {
		t.Fatal("no comment should be posted when comments are disabled")
	}

	// Ignore label should still be applied
	found := false
	for _, l := range client.labelsAdded {
		if l.Label == "bull:ignore" && l.Number == 13 {
			found = true
		}
	}
	if !found {
		t.Error("expected ignore label to be applied even when comments are disabled")
	}
}

func TestExecuteTriage_RejectClassification(t *testing.T) {
	client := &mockTriageClient{}
	ai := &mockTriageAI{response: makeAIResponse("reject", map[string]interface{}{
		"reject_reason": "This is spam.",
	})}
	store := &mockTriageStore{}

	issue := makeIssue(14, "Buy cheap sunglasses", "Visit our website for amazing deals on sunglasses and other accessories at low prices")
	opts := defaultOpts(client, ai, store)

	outcome, err := ExecuteTriage(context.Background(), issue, opts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if outcome.Action != "rejected" {
		t.Fatalf("expected action 'rejected', got %q", outcome.Action)
	}
	if outcome.Classification != "reject" {
		t.Fatalf("expected classification 'reject', got %q", outcome.Classification)
	}

	// No car should be created
	if len(store.createdCars) > 0 {
		t.Fatal("no car should be created for a rejection")
	}

	// Comment should be posted with reject reason
	if len(client.comments) != 1 {
		t.Fatalf("expected 1 comment, got %d", len(client.comments))
	}
	if !strings.Contains(client.comments[0].Body, "This is spam.") {
		t.Errorf("expected comment to contain reject reason, got %q", client.comments[0].Body)
	}

	// Ignore label should be applied
	found := false
	for _, l := range client.labelsAdded {
		if l.Label == "bull:ignore" && l.Number == 14 {
			found = true
		}
	}
	if !found {
		t.Error("expected ignore label to be applied")
	}
}

func TestExecuteTriage_RejectWithCustomTemplate(t *testing.T) {
	client := &mockTriageClient{}
	ai := &mockTriageAI{response: makeAIResponse("reject", map[string]interface{}{
		"reject_reason": "Out of scope.",
	})}
	store := &mockTriageStore{}

	issue := makeIssue(15, "Feature request for another project", "Please add dark mode to a completely different project than this repository")
	opts := defaultOpts(client, ai, store)
	opts.Config.Comments.RejectTemplate = "Closed by Bull: {{.Reason}}"

	outcome, err := ExecuteTriage(context.Background(), issue, opts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if outcome.Action != "rejected" {
		t.Fatalf("expected action 'rejected', got %q", outcome.Action)
	}

	if len(client.comments) != 1 {
		t.Fatalf("expected 1 comment, got %d", len(client.comments))
	}
	if !strings.Contains(client.comments[0].Body, "Out of scope.") {
		t.Errorf("expected comment to contain reject reason, got %q", client.comments[0].Body)
	}
	if !strings.Contains(client.comments[0].Body, "Closed by Bull:") {
		t.Errorf("expected comment to use custom template, got %q", client.comments[0].Body)
	}
}

func TestExecuteTriage_AIUnparseableResponse(t *testing.T) {
	client := &mockTriageClient{}
	ai := &mockTriageAI{response: "this is not valid json at all"}
	store := &mockTriageStore{}

	issue := makeIssue(16, "Valid issue title for testing", "This issue body is long enough to pass the heuristic filter for the triage system")
	opts := defaultOpts(client, ai, store)

	_, err := ExecuteTriage(context.Background(), issue, opts)
	if err == nil {
		t.Fatal("expected error for unparseable AI response")
	}
}

func TestExecuteTriage_FullModeDesignNotes(t *testing.T) {
	client := &mockTriageClient{}
	ai := &mockTriageAI{response: makeAIResponse("bug", map[string]interface{}{
		"design_notes": "Use a circuit breaker pattern for resilience.",
	})}
	store := &mockTriageStore{carIDToReturn: "CAR-FULL"}

	issue := makeIssue(17, "Service times out under load", "The service consistently times out when handling more than 100 concurrent requests in production")
	opts := defaultOpts(client, ai, store)
	opts.Config.TriageMode = "full"

	outcome, err := ExecuteTriage(context.Background(), issue, opts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if outcome.Action != "created_car" {
		t.Fatalf("expected action 'created_car', got %q", outcome.Action)
	}

	if len(store.createdCars) != 1 {
		t.Fatalf("expected 1 car created, got %d", len(store.createdCars))
	}
	if store.createdCars[0].DesignNotes != "Use a circuit breaker pattern for resilience." {
		t.Errorf("expected DesignNotes to be set, got %q", store.createdCars[0].DesignNotes)
	}
}

// Verify AI error propagation
func TestExecuteTriage_AIError(t *testing.T) {
	client := &mockTriageClient{}
	ai := &mockTriageAI{err: fmt.Errorf("AI service unavailable")}
	store := &mockTriageStore{}

	issue := makeIssue(18, "Some valid issue for AI testing", "This body is long enough to pass the heuristic filter and reach the AI step in triage")
	opts := defaultOpts(client, ai, store)

	_, err := ExecuteTriage(context.Background(), issue, opts)
	if err == nil {
		t.Fatal("expected error when AI fails")
	}
	if !strings.Contains(err.Error(), "AI service unavailable") {
		t.Errorf("expected error to contain AI error message, got %q", err.Error())
	}
}
