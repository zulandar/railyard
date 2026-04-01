package inspect

import (
	"strings"
	"testing"
)

func TestBuildReviewPrompt_Basic(t *testing.T) {
	ctx := ReviewContext{
		PRNumber:      42,
		PRTitle:       "Add widget feature",
		Diff:          "diff --git a/main.go b/main.go\n+func hello() {}",
		Files:         []FileContext{{Path: "main.go", Content: "package main"}},
		TrackName:     "backend",
		TrackLanguage: "Go",
	}

	prompt := BuildReviewPrompt(ctx)

	checks := map[string]string{
		"role name":  "Inspection Pit",
		"PR number":  "#42",
		"PR title":   "Add widget feature",
		"diff":       "diff --git a/main.go b/main.go",
		"file path":  "main.go",
		"language":   "Go",
		"track name": "backend",
	}
	for label, want := range checks {
		if !strings.Contains(prompt, want) {
			t.Errorf("prompt missing %s (%q)", label, want)
		}
	}
}

func TestBuildReviewPrompt_DeepReview(t *testing.T) {
	ctx := ReviewContext{
		PRNumber:    10,
		PRTitle:     "Fix bug",
		Diff:        "some diff",
		CodeContext: "This function handles authentication tokens",
	}

	prompt := BuildReviewPrompt(ctx)

	if !strings.Contains(prompt, "This function handles authentication tokens") {
		t.Error("prompt missing code context for deep review")
	}
}

func TestBuildReviewPrompt_TruncationNote(t *testing.T) {
	ctx := ReviewContext{
		PRNumber:      1,
		PRTitle:       "Large PR",
		Diff:          "diff content",
		Truncated:     true,
		TotalFiles:    20,
		IncludedFiles: 5,
	}

	prompt := BuildReviewPrompt(ctx)

	if !strings.Contains(prompt, "truncated") && !strings.Contains(prompt, "Truncated") {
		t.Error("prompt missing truncation note")
	}
	if !strings.Contains(prompt, "20") {
		t.Error("prompt missing total file count")
	}
	if !strings.Contains(prompt, "5") {
		t.Error("prompt missing included file count")
	}
}

func TestParseReviewResult_Valid(t *testing.T) {
	raw := `{
		"summary": "Looks good overall",
		"comments": [
			{"path": "main.go", "line": 10, "side": "RIGHT", "body": "Missing error check"}
		],
		"severity": "warning"
	}`

	result, err := ParseReviewResult(raw)
	if err != nil {
		t.Fatalf("ParseReviewResult() error: %v", err)
	}
	if result.Summary != "Looks good overall" {
		t.Errorf("Summary = %q, want %q", result.Summary, "Looks good overall")
	}
	if result.Severity != "warning" {
		t.Errorf("Severity = %q, want %q", result.Severity, "warning")
	}
	if len(result.Comments) != 1 {
		t.Fatalf("len(Comments) = %d, want 1", len(result.Comments))
	}
	c := result.Comments[0]
	if c.Path != "main.go" {
		t.Errorf("Comment.Path = %q, want %q", c.Path, "main.go")
	}
	if c.Line != 10 {
		t.Errorf("Comment.Line = %d, want 10", c.Line)
	}
	if c.Side != "RIGHT" {
		t.Errorf("Comment.Side = %q, want %q", c.Side, "RIGHT")
	}
	if c.Body != "Missing error check" {
		t.Errorf("Comment.Body = %q, want %q", c.Body, "Missing error check")
	}
}

func TestParseReviewResult_MarkdownFenced(t *testing.T) {
	raw := "```json\n{\"summary\": \"All clear\", \"comments\": [], \"severity\": \"info\"}\n```"

	result, err := ParseReviewResult(raw)
	if err != nil {
		t.Fatalf("ParseReviewResult() error: %v", err)
	}
	if result.Summary != "All clear" {
		t.Errorf("Summary = %q, want %q", result.Summary, "All clear")
	}
	if result.Severity != "info" {
		t.Errorf("Severity = %q, want %q", result.Severity, "info")
	}
	if len(result.Comments) != 0 {
		t.Errorf("len(Comments) = %d, want 0", len(result.Comments))
	}
}

func TestParseReviewResult_Invalid(t *testing.T) {
	_, err := ParseReviewResult("this is not json at all")
	if err == nil {
		t.Error("ParseReviewResult() expected error for invalid input, got nil")
	}
}

func TestParseReviewResult_Empty(t *testing.T) {
	_, err := ParseReviewResult("")
	if err == nil {
		t.Error("ParseReviewResult() expected error for empty input, got nil")
	}
}

func TestTruncateDiff_UnderLimit(t *testing.T) {
	files := []DiffFile{
		{Path: "a.go", Diff: "diff a", Lines: 10},
		{Path: "b.go", Diff: "diff b", Lines: 20},
	}

	result := TruncateDiff(files, 100)

	if result.Truncated {
		t.Error("expected Truncated=false when under limit")
	}
	if len(result.Files) != 2 {
		t.Errorf("len(Files) = %d, want 2", len(result.Files))
	}
	if result.TotalFiles != 2 {
		t.Errorf("TotalFiles = %d, want 2", result.TotalFiles)
	}
	if result.IncludedFiles != 2 {
		t.Errorf("IncludedFiles = %d, want 2", result.IncludedFiles)
	}
}

func TestTruncateDiff_OverLimit(t *testing.T) {
	files := []DiffFile{
		{Path: "small.go", Diff: "diff small", Lines: 5},
		{Path: "big.go", Diff: "diff big", Lines: 50},
		{Path: "medium.go", Diff: "diff medium", Lines: 20},
	}

	result := TruncateDiff(files, 60)

	if !result.Truncated {
		t.Error("expected Truncated=true when over limit")
	}
	if result.TotalFiles != 3 {
		t.Errorf("TotalFiles = %d, want 3", result.TotalFiles)
	}
	// Sorted by size desc: big(50), medium(20), small(5)
	// big(50) fits within 60, medium(20) would push to 70 which exceeds 60
	// So we expect 1 file included (big.go)
	if result.IncludedFiles != 1 {
		t.Errorf("IncludedFiles = %d, want 1", result.IncludedFiles)
	}
	if result.Files[0].Path != "big.go" {
		t.Errorf("Files[0].Path = %q, want %q", result.Files[0].Path, "big.go")
	}
}

func TestTruncateDiff_SkipsGenerated(t *testing.T) {
	files := []DiffFile{
		{Path: "vendor/lib/foo.go", Diff: "diff vendor", Lines: 10},
		{Path: "go.sum", Diff: "diff sum", Lines: 100},
		{Path: "app.min.js", Diff: "diff minjs", Lines: 50},
		{Path: "real.go", Diff: "diff real", Lines: 15},
		{Path: "types.pb.go", Diff: "diff proto", Lines: 30},
		{Path: "code_gen.go", Diff: "diff gen", Lines: 20},
		{Path: "output.generated.go", Diff: "diff generated", Lines: 25},
		{Path: "style.min.css", Diff: "diff mincss", Lines: 40},
		{Path: "package-lock.json", Diff: "diff lock", Lines: 200},
	}

	result := TruncateDiff(files, 1000)

	if len(result.Files) != 1 {
		t.Errorf("len(Files) = %d, want 1 (only real.go should remain)", len(result.Files))
	}
	if len(result.Files) > 0 && result.Files[0].Path != "real.go" {
		t.Errorf("Files[0].Path = %q, want %q", result.Files[0].Path, "real.go")
	}
}

func TestTruncateDiff_AlwaysIncludesAtLeastOne(t *testing.T) {
	files := []DiffFile{
		{Path: "huge.go", Diff: "very large diff", Lines: 5000},
	}

	result := TruncateDiff(files, 100)

	if len(result.Files) != 1 {
		t.Errorf("len(Files) = %d, want 1 (always include at least 1 file)", len(result.Files))
	}
	if result.IncludedFiles != 1 {
		t.Errorf("IncludedFiles = %d, want 1", result.IncludedFiles)
	}
}
