package bull

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/zulandar/railyard/internal/config"
)

// mockLabelClient implements LabelClient and tracks all calls made to it.
type mockLabelClient struct {
	existingLabels []string
	createdLabels  []struct{ name, color, description string }
	addedLabels    []struct{ number int; label string }
	removedLabels  []struct{ number int; label string }
	removeErr      error
}

func (m *mockLabelClient) CreateLabel(_ context.Context, name, color, description string) error {
	m.createdLabels = append(m.createdLabels, struct{ name, color, description string }{name, color, description})
	return nil
}

func (m *mockLabelClient) AddLabel(_ context.Context, number int, label string) error {
	m.addedLabels = append(m.addedLabels, struct{ number int; label string }{number, label})
	return nil
}

func (m *mockLabelClient) RemoveLabel(_ context.Context, number int, label string) error {
	if m.removeErr != nil {
		return m.removeErr
	}
	m.removedLabels = append(m.removedLabels, struct{ number int; label string }{number, label})
	return nil
}

func (m *mockLabelClient) ListLabels(_ context.Context) ([]string, error) {
	return m.existingLabels, nil
}

func testLabelsConfig() config.BullLabelsConfig {
	return config.BullLabelsConfig{
		UnderReview: "bull: under review",
		InProgress:  "bull: in progress",
		FixMerged:   "bull: fix merged",
		Ignore:      "bull: ignore",
	}
}

// ---------- EnsureLabels ----------

func TestEnsureLabels_CreatesAllMissing(t *testing.T) {
	mock := &mockLabelClient{existingLabels: []string{}}
	cfg := testLabelsConfig()

	err := EnsureLabels(context.Background(), mock, cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(mock.createdLabels) != 4 {
		t.Fatalf("got %d CreateLabel calls, want 4", len(mock.createdLabels))
	}

	created := map[string]bool{}
	for _, c := range mock.createdLabels {
		created[c.name] = true
	}
	for _, want := range []string{"bull: under review", "bull: in progress", "bull: fix merged", "bull: ignore"} {
		if !created[want] {
			t.Errorf("expected CreateLabel for %q", want)
		}
	}
}

func TestEnsureLabels_SkipsExisting(t *testing.T) {
	mock := &mockLabelClient{
		existingLabels: []string{"bull: under review", "bull: in progress"},
	}
	cfg := testLabelsConfig()

	err := EnsureLabels(context.Background(), mock, cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(mock.createdLabels) != 2 {
		t.Fatalf("got %d CreateLabel calls, want 2", len(mock.createdLabels))
	}

	created := map[string]bool{}
	for _, c := range mock.createdLabels {
		created[c.name] = true
	}
	if created["bull: under review"] {
		t.Error("should not have created 'bull: under review' — it already exists")
	}
	if created["bull: in progress"] {
		t.Error("should not have created 'bull: in progress' — it already exists")
	}
	if !created["bull: fix merged"] {
		t.Error("expected CreateLabel for 'bull: fix merged'")
	}
	if !created["bull: ignore"] {
		t.Error("expected CreateLabel for 'bull: ignore'")
	}
}

func TestEnsureLabels_FullyIdempotent(t *testing.T) {
	mock := &mockLabelClient{
		existingLabels: []string{"bull: under review", "bull: in progress", "bull: fix merged", "bull: ignore"},
	}
	cfg := testLabelsConfig()

	err := EnsureLabels(context.Background(), mock, cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(mock.createdLabels) != 0 {
		t.Errorf("got %d CreateLabel calls, want 0 (all labels exist)", len(mock.createdLabels))
	}
}

// ---------- ApplyLabel ----------

func TestApplyLabel_CallsAddLabel(t *testing.T) {
	mock := &mockLabelClient{}

	err := ApplyLabel(context.Background(), mock, 42, "bull: under review")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(mock.addedLabels) != 1 {
		t.Fatalf("got %d AddLabel calls, want 1", len(mock.addedLabels))
	}
	if mock.addedLabels[0].number != 42 {
		t.Errorf("AddLabel number = %d, want 42", mock.addedLabels[0].number)
	}
	if mock.addedLabels[0].label != "bull: under review" {
		t.Errorf("AddLabel label = %q, want %q", mock.addedLabels[0].label, "bull: under review")
	}
}

// ---------- RemoveBullLabel ----------

func TestRemoveBullLabel_CallsRemoveLabel(t *testing.T) {
	mock := &mockLabelClient{}

	err := RemoveBullLabel(context.Background(), mock, 10, "bull: in progress")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(mock.removedLabels) != 1 {
		t.Fatalf("got %d RemoveLabel calls, want 1", len(mock.removedLabels))
	}
	if mock.removedLabels[0].number != 10 {
		t.Errorf("RemoveLabel number = %d, want 10", mock.removedLabels[0].number)
	}
	if mock.removedLabels[0].label != "bull: in progress" {
		t.Errorf("RemoveLabel label = %q, want %q", mock.removedLabels[0].label, "bull: in progress")
	}
}

// httpError mimics a GitHub 404 error response for testing.
type httpError struct {
	statusCode int
	message    string
}

func (e *httpError) Error() string { return e.message }

// Is allows errors.Is to match by checking if target wraps an httpError with same status.
func (e *httpError) Is(target error) bool {
	var he *httpError
	if errors.As(target, &he) {
		return he.statusCode == e.statusCode
	}
	return false
}

func TestRemoveBullLabel_Swallows404(t *testing.T) {
	mock := &mockLabelClient{
		removeErr: &httpError{statusCode: http.StatusNotFound, message: "Not Found"},
	}

	err := RemoveBullLabel(context.Background(), mock, 10, "bull: in progress")
	if err != nil {
		t.Fatalf("expected nil error for 404, got: %v", err)
	}
}

func TestRemoveBullLabel_PropagatesOtherErrors(t *testing.T) {
	mock := &mockLabelClient{
		removeErr: errors.New("server error"),
	}

	err := RemoveBullLabel(context.Background(), mock, 10, "bull: in progress")
	if err == nil {
		t.Fatal("expected error to be propagated")
	}
}

// ---------- RemoveAllBullLabels ----------

func TestRemoveAllBullLabels_RemovesAll4(t *testing.T) {
	mock := &mockLabelClient{}
	cfg := testLabelsConfig()

	err := RemoveAllBullLabels(context.Background(), mock, 5, cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(mock.removedLabels) != 4 {
		t.Fatalf("got %d RemoveLabel calls, want 4", len(mock.removedLabels))
	}

	removed := map[string]bool{}
	for _, r := range mock.removedLabels {
		if r.number != 5 {
			t.Errorf("RemoveLabel number = %d, want 5", r.number)
		}
		removed[r.label] = true
	}
	for _, want := range []string{"bull: under review", "bull: in progress", "bull: fix merged", "bull: ignore"} {
		if !removed[want] {
			t.Errorf("expected RemoveLabel for %q", want)
		}
	}
}
