package engine

import (
	"strings"
	"testing"
	"time"

	"github.com/zulandar/railyard/internal/config"
	"github.com/zulandar/railyard/internal/models"
)

func makeInput() ContextInput {
	return ContextInput{
		Car: &models.Car{
			ID:          "car-001",
			Title:       "Implement widget",
			Priority:    1,
			Branch:      "ry/alice/backend/car-001",
			Description: "Build the widget feature",
			DesignNotes: "Use existing patterns",
			Acceptance:  "Widget works end-to-end",
		},
		Track: &models.Track{
			Name:         "backend",
			Language:     "Go",
			Conventions:  `{"test_framework":"testing","style":"gofmt"}`,
			SystemPrompt: "You are a Go backend engineer.",
		},
		Config: &config.Config{
			Owner:        "alice",
			BranchPrefix: "ry/alice",
		},
	}
}

// --- Validation tests ---

func TestRenderContext_NilCar(t *testing.T) {
	input := makeInput()
	input.Car = nil
	_, err := RenderContext(input)
	if err == nil {
		t.Fatal("expected error for nil car")
	}
	if !strings.Contains(err.Error(), "car is required") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestRenderContext_NilTrack(t *testing.T) {
	input := makeInput()
	input.Track = nil
	_, err := RenderContext(input)
	if err == nil {
		t.Fatal("expected error for nil track")
	}
	if !strings.Contains(err.Error(), "track is required") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestRenderContext_NilConfig(t *testing.T) {
	input := makeInput()
	input.Config = nil
	_, err := RenderContext(input)
	if err == nil {
		t.Fatal("expected error for nil config")
	}
	if !strings.Contains(err.Error(), "config is required") {
		t.Errorf("unexpected error: %v", err)
	}
}

// --- Section tests ---

func TestRenderContext_Header(t *testing.T) {
	out, err := RenderContext(makeInput())
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"# You are an engine on track: backend",
		"# Railyard owner: alice",
		"# Branch prefix: ry/alice/backend/",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("header missing %q", want)
		}
	}
}

func TestRenderContext_Conventions(t *testing.T) {
	out, err := RenderContext(makeInput())
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"## Project Conventions",
		"You are a Go backend engineer.",
		"Language: Go",
		"- style: gofmt",
		"- test_framework: testing",
		"IMPORTANT: You ONLY work on this project.",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("conventions missing %q", want)
		}
	}
}

func TestRenderContext_ConventionsEmpty(t *testing.T) {
	input := makeInput()
	input.Track.Conventions = ""
	input.Track.SystemPrompt = ""
	out, err := RenderContext(input)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "## Project Conventions") {
		t.Error("conventions section missing")
	}
	if !strings.Contains(out, "Language: Go") {
		t.Error("language missing")
	}
}

func TestRenderContext_CurrentCar(t *testing.T) {
	out, err := RenderContext(makeInput())
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"## Your Current Car",
		"Car: car-001",
		"Title: Implement widget",
		"Priority: 1",
		"Branch: ry/alice/backend/car-001",
		"### Description",
		"Build the widget feature",
		"### Design Notes",
		"Use existing patterns",
		"### Acceptance Criteria",
		"Widget works end-to-end",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("current car missing %q", want)
		}
	}
}

func TestRenderContext_Progress(t *testing.T) {
	input := makeInput()
	input.Progress = []models.CarProgress{
		{Cycle: 2, Note: "Fixed tests", FilesChanged: `["main.go"]`, CommitHash: "abc1234"},
		{Cycle: 1, Note: "Initial scaffolding", FilesChanged: `["main.go","go.mod"]`, CommitHash: "def5678"},
	}
	out, err := RenderContext(input)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"## Previous Progress (if resuming)",
		"### Cycle 2",
		"Fixed tests",
		`Files: ["main.go"]`,
		"Commit: abc1234",
		"### Cycle 1",
		"Initial scaffolding",
		"Commit: def5678",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("progress missing %q", want)
		}
	}
	// Verify cycle 2 appears before cycle 1 (caller provides order).
	idx2 := strings.Index(out, "### Cycle 2")
	idx1 := strings.Index(out, "### Cycle 1")
	if idx2 > idx1 {
		t.Error("expected cycle 2 before cycle 1 (most recent first)")
	}
}

func TestRenderContext_ProgressEmpty(t *testing.T) {
	out, err := RenderContext(makeInput())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "## Previous Progress") {
		t.Error("progress section should be omitted when empty")
	}
}

func TestRenderContext_Messages(t *testing.T) {
	input := makeInput()
	input.Messages = []models.Message{
		{
			FromAgent: "yardmaster",
			Subject:   "Check the API docs",
			Body:      "Refer to the swagger spec before implementing.",
			Priority:  "high",
			CreatedAt: time.Date(2026, 2, 14, 10, 30, 0, 0, time.UTC),
		},
	}
	out, err := RenderContext(input)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"## Yardmaster Messages",
		"### [high] Check the API docs",
		"From: yardmaster | 2026-02-14 10:30",
		"Refer to the swagger spec before implementing.",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("messages missing %q", want)
		}
	}
}

func TestRenderContext_MessagesEmpty(t *testing.T) {
	out, err := RenderContext(makeInput())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "## Yardmaster Messages") {
		t.Error("messages section should be omitted when empty")
	}
}

func TestRenderContext_RecentCommits(t *testing.T) {
	input := makeInput()
	input.RecentCommits = []string{
		"abc1234 Add widget handler",
		"def5678 Initial commit",
	}
	out, err := RenderContext(input)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"## Recent Commits on Your Branch",
		"abc1234 Add widget handler",
		"def5678 Initial commit",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("commits missing %q", want)
		}
	}
}

func TestRenderContext_RecentCommitsEmpty(t *testing.T) {
	out, err := RenderContext(makeInput())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "## Recent Commits") {
		t.Error("commits section should be omitted when empty")
	}
}

func TestRenderContext_Instructions(t *testing.T) {
	out, err := RenderContext(makeInput())
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"## When You're Done",
		"ry car complete",
		"## If You're Stuck",
		"ry car progress",
		"ry message send",
		"## If You Need to Split Work",
		"ry car create",
		"## If You Discover a Bug",
		"--type bug",
		"bug-filed",
		"Scope rule",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("instructions missing %q", want)
		}
	}
}

func TestRenderContext_FullTemplate(t *testing.T) {
	input := makeInput()
	input.Progress = []models.CarProgress{
		{Cycle: 1, Note: "Scaffolded", FilesChanged: `["main.go"]`, CommitHash: "aaa1111"},
	}
	input.Messages = []models.Message{
		{FromAgent: "yardmaster", Subject: "Heads up", Body: "Deploy freeze today.", Priority: "normal", CreatedAt: time.Date(2026, 2, 14, 9, 0, 0, 0, time.UTC)},
	}
	input.RecentCommits = []string{"aaa1111 Scaffold widget"}

	out, err := RenderContext(input)
	if err != nil {
		t.Fatal(err)
	}

	// Verify all major sections appear in order.
	sections := []string{
		"# You are an engine on track:",
		"## Project Conventions",
		"## Your Current Car",
		"## Previous Progress (if resuming)",
		"## Yardmaster Messages",
		"## Recent Commits on Your Branch",
		"## When You're Done",
		"## If You're Stuck",
		"## If You Need to Split Work",
		"## If You Discover a Bug",
	}
	lastIdx := -1
	for _, s := range sections {
		idx := strings.Index(out, s)
		if idx == -1 {
			t.Errorf("missing section %q", s)
			continue
		}
		if idx <= lastIdx {
			t.Errorf("section %q out of order", s)
		}
		lastIdx = idx
	}
}

// --- Helper tests ---

func TestFormatConventions(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  []string // substrings that must appear
		empty bool     // expect empty result
	}{
		{
			name:  "flat",
			input: `{"style":"gofmt","test_framework":"testing"}`,
			want:  []string{"- style: gofmt", "- test_framework: testing"},
		},
		{
			name:  "nested",
			input: `{"lint":{"enabled":true,"tool":"golangci-lint"}}`,
			want:  []string{`- lint: {"enabled":true,"tool":"golangci-lint"}`},
		},
		{
			name:  "numeric",
			input: `{"max_line_length":120}`,
			want:  []string{"- max_line_length: 120"},
		},
		{
			name:  "boolean",
			input: `{"strict":true}`,
			want:  []string{"- strict: true"},
		},
		{
			name:  "empty_object",
			input: `{}`,
			empty: true,
		},
		{
			name:  "empty_string",
			input: "",
			empty: true,
		},
		{
			name:  "invalid_json",
			input: "not json",
			empty: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := formatConventions(tt.input)
			if tt.empty {
				if got != "" {
					t.Errorf("expected empty, got %q", got)
				}
				return
			}
			for _, w := range tt.want {
				if !strings.Contains(got, w) {
					t.Errorf("missing %q in %q", w, got)
				}
			}
		})
	}
}
