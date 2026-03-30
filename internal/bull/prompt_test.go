package bull

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestBuildTriagePrompt_StandardMode(t *testing.T) {
	issue := IssueContext{
		Number: 42,
		Title:  "Login button broken",
		Body:   "The login button does not respond to clicks.",
		Labels: []string{"bug", "frontend"},
		Author: "alice",
	}
	tracks := []TrackInfo{{Name: "backend"}, {Name: "frontend"}}

	prompt := BuildTriagePrompt(issue, "standard", tracks, "")

	// Standard mode should include classification instructions
	if !strings.Contains(prompt, "bug") || !strings.Contains(prompt, "task") ||
		!strings.Contains(prompt, "question") || !strings.Contains(prompt, "reject") {
		t.Error("standard mode prompt should include classification options (bug, task, question, reject)")
	}

	// Should include priority instructions
	if !strings.Contains(prompt, "priority") || !strings.Contains(prompt, "0") {
		t.Error("standard mode prompt should include priority instructions")
	}

	// Should mention JSON output format
	if !strings.Contains(prompt, "JSON") || !strings.Contains(prompt, "json") {
		t.Error("standard mode prompt should specify JSON output format")
	}

	// Should include title, description, acceptance fields
	for _, field := range []string{"title", "description", "acceptance"} {
		if !strings.Contains(strings.ToLower(prompt), field) {
			t.Errorf("standard mode prompt should mention %q field", field)
		}
	}

	// Standard mode should NOT include design_notes or dependencies instructions
	if strings.Contains(prompt, "design_notes") {
		t.Error("standard mode prompt should not include design_notes instructions")
	}
	if strings.Contains(prompt, "dependencies") {
		t.Error("standard mode prompt should not include dependencies instructions")
	}
}

func TestBuildTriagePrompt_FullMode(t *testing.T) {
	issue := IssueContext{
		Number: 7,
		Title:  "Add auth system",
		Body:   "We need JWT-based authentication.",
		Labels: []string{"feature"},
		Author: "bob",
	}
	tracks := []TrackInfo{{Name: "backend"}, {Name: "frontend"}}

	prompt := BuildTriagePrompt(issue, "full", tracks, "")

	if !strings.Contains(prompt, "design_notes") {
		t.Error("full mode prompt should include design_notes instructions")
	}
	if !strings.Contains(prompt, "dependencies") {
		t.Error("full mode prompt should include dependencies instructions")
	}
}

func TestBuildTriagePrompt_IncludesIssueContext(t *testing.T) {
	issue := IssueContext{
		Number: 99,
		Title:  "Refactor database layer",
		Body:   "The current DB layer is too tightly coupled.",
		Labels: []string{"refactor", "backend"},
		Author: "charlie",
	}
	tracks := []TrackInfo{{Name: "backend"}}

	prompt := BuildTriagePrompt(issue, "standard", tracks, "")

	if !strings.Contains(prompt, "99") {
		t.Error("prompt should include issue number")
	}
	if !strings.Contains(prompt, "Refactor database layer") {
		t.Error("prompt should include issue title")
	}
	if !strings.Contains(prompt, "The current DB layer is too tightly coupled.") {
		t.Error("prompt should include issue body")
	}
	if !strings.Contains(prompt, "refactor") || !strings.Contains(prompt, "backend") {
		t.Error("prompt should include issue labels")
	}
	if !strings.Contains(prompt, "charlie") {
		t.Error("prompt should include issue author")
	}
}

func TestBuildTriagePrompt_IncludesTracksList(t *testing.T) {
	issue := IssueContext{
		Number: 1,
		Title:  "Test",
		Body:   "Test body",
		Author: "dev",
	}
	tracks := []TrackInfo{{Name: "backend"}, {Name: "frontend"}, {Name: "infra"}}

	prompt := BuildTriagePrompt(issue, "standard", tracks, "")

	for _, track := range tracks {
		if !strings.Contains(prompt, track.Name) {
			t.Errorf("prompt should include track %q", track.Name)
		}
	}
}

func TestBuildTriagePrompt_IncludesCodeContext(t *testing.T) {
	issue := IssueContext{
		Number: 10,
		Title:  "Fix handler",
		Body:   "Handler returns 500",
		Author: "dev",
	}
	codeCtx := "func HandleLogin(w http.ResponseWriter, r *http.Request) { ... }"

	prompt := BuildTriagePrompt(issue, "standard", []TrackInfo{{Name: "backend"}}, codeCtx)

	if !strings.Contains(prompt, codeCtx) {
		t.Error("prompt should include code context when provided")
	}
}

func TestBuildTriagePrompt_OmitsCodeContextWhenEmpty(t *testing.T) {
	issue := IssueContext{
		Number: 10,
		Title:  "Fix handler",
		Body:   "Handler returns 500",
		Author: "dev",
	}

	prompt := BuildTriagePrompt(issue, "standard", []TrackInfo{{Name: "backend"}}, "")

	// Should not contain a code context section header when code context is empty
	if strings.Contains(prompt, "Code Context") || strings.Contains(prompt, "Relevant Code") {
		t.Error("prompt should omit code context section when code context is empty")
	}
}

func TestBuildTriagePrompt_IncludesTrackMetadata(t *testing.T) {
	issue := IssueContext{
		Number: 20,
		Title:  "Fix API",
		Body:   "The API is broken",
		Author: "dev",
	}
	tracks := []TrackInfo{
		{
			Name:         "backend",
			Language:     "go",
			FilePatterns: []string{"*.go"},
			Conventions:  []string{"use-interfaces"},
		},
	}

	prompt := BuildTriagePrompt(issue, "standard", tracks, "")

	if !strings.Contains(prompt, "### backend") {
		t.Error("prompt should include '### backend' header")
	}
	if !strings.Contains(prompt, "Language") || !strings.Contains(prompt, "go") {
		t.Error("prompt should include Language metadata")
	}
	if !strings.Contains(prompt, "*.go") {
		t.Error("prompt should include file pattern '*.go'")
	}
	if !strings.Contains(prompt, "use-interfaces") {
		t.Error("prompt should include convention key")
	}
}

func TestBuildTriagePrompt_TrackWithNoMetadata(t *testing.T) {
	issue := IssueContext{
		Number: 21,
		Title:  "Fix something",
		Body:   "Something is broken",
		Author: "dev",
	}
	tracks := []TrackInfo{
		{Name: "backend"},
	}

	prompt := BuildTriagePrompt(issue, "standard", tracks, "")

	if !strings.Contains(prompt, "backend") {
		t.Error("prompt should include track name")
	}
	// Should not contain empty bullet lines for Language, FilePatterns, or Conventions
	if strings.Contains(prompt, "**Language**:") {
		t.Error("prompt should not include Language bullet when Language is empty")
	}
	if strings.Contains(prompt, "**File patterns**:") {
		t.Error("prompt should not include File patterns bullet when FilePatterns is empty")
	}
	if strings.Contains(prompt, "**Conventions**:") {
		t.Error("prompt should not include Conventions bullet when Conventions is empty")
	}
}

// Fix #1: Conventions should include "key: value" pairs, not just keys.
func TestBuildTriagePrompt_ConventionsIncludeKeyValuePairs(t *testing.T) {
	issue := IssueContext{
		Number: 30,
		Title:  "Refactor handler",
		Body:   "Needs refactoring",
		Author: "dev",
	}
	tracks := []TrackInfo{
		{
			Name:     "backend",
			Language: "go",
			// Conventions now carry "key: value" strings.
			Conventions: []string{"naming: snake_case", "errors: wrap with fmt.Errorf"},
		},
	}

	prompt := BuildTriagePrompt(issue, "standard", tracks, "")

	if !strings.Contains(prompt, "naming: snake_case") {
		t.Error("prompt should include convention value 'naming: snake_case'")
	}
	if !strings.Contains(prompt, "errors: wrap with fmt.Errorf") {
		t.Error("prompt should include convention value 'errors: wrap with fmt.Errorf'")
	}
}

func TestBuildTriagePrompt_IncludesDiscussionSection(t *testing.T) {
	issue := IssueContext{
		Number: 50,
		Title:  "Bug in parser",
		Body:   "The parser fails on large inputs.",
		Author: "alice",
		Comments: []CommentContext{
			{Author: "bob", Body: "I can reproduce this with a 10MB file.", Date: "2026-03-01"},
			{Author: "carol", Body: "Same here, also crashes with 5MB.", Date: "2026-03-02"},
		},
	}

	prompt := BuildTriagePrompt(issue, "standard", []TrackInfo{{Name: "backend"}}, "")

	if !strings.Contains(prompt, "### Discussion") {
		t.Error("prompt should include '### Discussion' section when comments are present")
	}
	if !strings.Contains(prompt, "@bob") {
		t.Error("prompt should include comment author @bob")
	}
	if !strings.Contains(prompt, "I can reproduce this with a 10MB file.") {
		t.Error("prompt should include bob's comment body")
	}
	if !strings.Contains(prompt, "@carol") {
		t.Error("prompt should include comment author @carol")
	}
	if !strings.Contains(prompt, "2026-03-01") {
		t.Error("prompt should include comment date")
	}
}

func TestBuildTriagePrompt_OmitsDiscussionWhenNoComments(t *testing.T) {
	issue := IssueContext{
		Number: 51,
		Title:  "Simple bug",
		Body:   "Something is broken in the application.",
		Author: "dev",
	}

	prompt := BuildTriagePrompt(issue, "standard", []TrackInfo{{Name: "backend"}}, "")

	if strings.Contains(prompt, "Discussion") {
		t.Error("prompt should not include Discussion section when no comments")
	}
}

func TestBuildTriagePrompt_CommentsInChronologicalOrder(t *testing.T) {
	issue := IssueContext{
		Number: 52,
		Title:  "Ordering test",
		Body:   "Test body for verifying comment order in prompt.",
		Author: "dev",
		Comments: []CommentContext{
			{Author: "first", Body: "First comment posted.", Date: "2026-01-01"},
			{Author: "second", Body: "Second comment posted.", Date: "2026-01-02"},
			{Author: "third", Body: "Third comment posted.", Date: "2026-01-03"},
		},
	}

	prompt := BuildTriagePrompt(issue, "standard", []TrackInfo{{Name: "backend"}}, "")

	idxFirst := strings.Index(prompt, "First comment posted.")
	idxSecond := strings.Index(prompt, "Second comment posted.")
	idxThird := strings.Index(prompt, "Third comment posted.")

	if idxFirst == -1 || idxSecond == -1 || idxThird == -1 {
		t.Fatal("all three comments should appear in prompt")
	}
	if idxFirst > idxSecond || idxSecond > idxThird {
		t.Error("comments should appear in chronological order (oldest first)")
	}
}

func TestParseTriageResult_ValidJSON(t *testing.T) {
	response := `{"classification":"bug","priority":2,"track":"frontend","title":"Fix login button","description":"The login button is broken.","acceptance":"Button responds to clicks."}`

	result, err := ParseTriageResult(response)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Classification != "bug" {
		t.Errorf("Classification = %q, want %q", result.Classification, "bug")
	}
	if result.Priority != 2 {
		t.Errorf("Priority = %d, want %d", result.Priority, 2)
	}
	if result.Track != "frontend" {
		t.Errorf("Track = %q, want %q", result.Track, "frontend")
	}
	if result.Title != "Fix login button" {
		t.Errorf("Title = %q, want %q", result.Title, "Fix login button")
	}
	if result.Description != "The login button is broken." {
		t.Errorf("Description = %q, want %q", result.Description, "The login button is broken.")
	}
	if result.Acceptance != "Button responds to clicks." {
		t.Errorf("Acceptance = %q, want %q", result.Acceptance, "Button responds to clicks.")
	}
}

func TestParseTriageResult_JSONInCodeFence(t *testing.T) {
	response := "Here is the result:\n```json\n" + `{"classification":"task","priority":1,"track":"backend","title":"Add endpoint","description":"New REST endpoint.","acceptance":"Endpoint returns 200."}` + "\n```\nDone."

	result, err := ParseTriageResult(response)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Classification != "task" {
		t.Errorf("Classification = %q, want %q", result.Classification, "task")
	}
	if result.Title != "Add endpoint" {
		t.Errorf("Title = %q, want %q", result.Title, "Add endpoint")
	}
}

func TestParseTriageResult_MalformedJSON(t *testing.T) {
	response := "this is not json at all"

	_, err := ParseTriageResult(response)
	if err == nil {
		t.Fatal("expected error for malformed JSON, got nil")
	}
}

func TestParseTriageResult_MissingClassification(t *testing.T) {
	response := `{"priority":1,"track":"backend","title":"Something","description":"desc","acceptance":"acc"}`

	_, err := ParseTriageResult(response)
	if err == nil {
		t.Fatal("expected error for missing classification, got nil")
	}
	if !strings.Contains(err.Error(), "classification") {
		t.Errorf("error should mention classification, got: %v", err)
	}
}

func TestParseTriageResult_MissingTitle(t *testing.T) {
	response := `{"classification":"bug","priority":1,"track":"backend","description":"desc","acceptance":"acc"}`

	_, err := ParseTriageResult(response)
	if err == nil {
		t.Fatal("expected error for missing title, got nil")
	}
	if !strings.Contains(err.Error(), "title") {
		t.Errorf("error should mention title, got: %v", err)
	}
}

func TestParseTriageResult_FullModeFields(t *testing.T) {
	data := map[string]interface{}{
		"classification": "task",
		"priority":       1,
		"track":          "backend",
		"title":          "Design auth system",
		"description":    "Design the authentication system.",
		"acceptance":     "Design doc produced.",
		"design_notes":   "Use JWT with refresh tokens.",
		"dependencies":   []string{"auth-middleware", "user-model"},
	}
	raw, _ := json.Marshal(data)
	response := string(raw)

	result, err := ParseTriageResult(response)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.DesignNotes != "Use JWT with refresh tokens." {
		t.Errorf("DesignNotes = %q, want %q", result.DesignNotes, "Use JWT with refresh tokens.")
	}
	if len(result.Dependencies) != 2 {
		t.Fatalf("Dependencies len = %d, want 2", len(result.Dependencies))
	}
	if result.Dependencies[0] != "auth-middleware" {
		t.Errorf("Dependencies[0] = %q, want %q", result.Dependencies[0], "auth-middleware")
	}
	if result.Dependencies[1] != "user-model" {
		t.Errorf("Dependencies[1] = %q, want %q", result.Dependencies[1], "user-model")
	}
}
