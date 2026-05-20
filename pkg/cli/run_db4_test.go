package main

import (
	"strings"
	"testing"
	"time"

	"github.com/zulandar/railyard/internal/models"
)

// ---------------------------------------------------------------------------
// runCarList – additional branches
// ---------------------------------------------------------------------------

func TestRunCarList_WithTokens(t *testing.T) {
	gormDB := mockTestDB(t)
	cleanup := withMockDB(t, gormDB)
	defer cleanup()

	now := time.Now()
	gormDB.Create(&models.Car{ID: "car-tok", Title: "Token Car", Status: "open", Track: "backend", Priority: 2, CreatedAt: now, UpdatedAt: now})
	gormDB.Create(&models.AgentLog{CarID: "car-tok", EngineID: "eng-1", Direction: "out", InputTokens: 1000, OutputTokens: 500, TokenCount: 1500, Model: "claude-3", CreatedAt: now})

	out, err := execCmd(t, []string{"car", "list", "--config", "test.yaml"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	for _, want := range []string{"TOKENS", "CYCLES", "car-tok", "Token Car"} {
		if !strings.Contains(out, want) {
			t.Errorf("expected output to contain %q, got:\n%s", want, out)
		}
	}
}

func TestRunCarList_WithMultipleBaseBranches(t *testing.T) {
	gormDB := mockTestDB(t)
	cleanup := withMockDB(t, gormDB)
	defer cleanup()

	now := time.Now()
	gormDB.Create(&models.Car{ID: "car-m1", Title: "Main Car", Status: "open", Track: "backend", BaseBranch: "main", Priority: 2, CreatedAt: now, UpdatedAt: now})
	gormDB.Create(&models.Car{ID: "car-d1", Title: "Dev Car", Status: "open", Track: "backend", BaseBranch: "develop", Priority: 2, CreatedAt: now, UpdatedAt: now})

	out, err := execCmd(t, []string{"car", "list", "--config", "test.yaml"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(out, "BASE") {
		t.Errorf("expected output to contain 'BASE' column header, got:\n%s", out)
	}
	for _, want := range []string{"car-m1", "car-d1", "main", "develop"} {
		if !strings.Contains(out, want) {
			t.Errorf("expected output to contain %q, got:\n%s", want, out)
		}
	}
}

func TestRunCarList_WithTokensAndMultipleBases(t *testing.T) {
	gormDB := mockTestDB(t)
	cleanup := withMockDB(t, gormDB)
	defer cleanup()

	now := time.Now()
	gormDB.Create(&models.Car{ID: "car-tb1", Title: "Car TB1", Status: "open", Track: "backend", BaseBranch: "main", Priority: 2, CreatedAt: now, UpdatedAt: now})
	gormDB.Create(&models.Car{ID: "car-tb2", Title: "Car TB2", Status: "open", Track: "backend", BaseBranch: "develop", Priority: 2, CreatedAt: now, UpdatedAt: now})
	gormDB.Create(&models.AgentLog{CarID: "car-tb1", EngineID: "eng-1", Direction: "out", InputTokens: 2000, OutputTokens: 800, TokenCount: 2800, Model: "claude-3", CreatedAt: now})

	out, err := execCmd(t, []string{"car", "list", "--config", "test.yaml"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// TOKENS, CYCLES, and BASE columns should be present.
	for _, want := range []string{"TOKENS", "CYCLES", "BASE", "car-tb1", "car-tb2"} {
		if !strings.Contains(out, want) {
			t.Errorf("expected output to contain %q, got:\n%s", want, out)
		}
	}
}

func TestRunCarList_WithAssignee(t *testing.T) {
	gormDB := mockTestDB(t)
	cleanup := withMockDB(t, gormDB)
	defer cleanup()

	now := time.Now()
	gormDB.Create(&models.Car{ID: "car-a1", Title: "Assigned", Status: "open", Track: "backend", Assignee: "eng-1", Priority: 2, CreatedAt: now, UpdatedAt: now})
	gormDB.Create(&models.Car{ID: "car-a2", Title: "Unassigned", Status: "open", Track: "backend", Priority: 2, CreatedAt: now, UpdatedAt: now})

	out, err := execCmd(t, []string{"car", "list", "--config", "test.yaml"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(out, "eng-1") {
		t.Errorf("expected output to contain 'eng-1' for assigned car, got:\n%s", out)
	}
	// Unassigned car should show "-" as the assignee placeholder.
	if !strings.Contains(out, "-") {
		t.Errorf("expected output to contain '-' for unassigned car, got:\n%s", out)
	}
}

// ---------------------------------------------------------------------------
// runCarShow – additional branches
// ---------------------------------------------------------------------------

func TestRunCarShow_WithAllFields(t *testing.T) {
	gormDB := mockTestDB(t)
	cleanup := withMockDB(t, gormDB)
	defer cleanup()

	now := time.Now()
	claimed := now.Add(-time.Hour)
	completed := now.Add(-time.Minute)
	parentID := "car-parent"

	gormDB.Create(&models.Car{
		ID:        "car-parent",
		Title:     "Parent",
		Status:    "open",
		Track:     "backend",
		Type:      "epic",
		CreatedAt: now,
		UpdatedAt: now,
	})
	gormDB.Create(&models.Car{
		ID:          "car-full",
		Title:       "Fully Populated Car",
		Status:      "done",
		Type:        "task",
		Track:       "backend",
		Branch:      "car/car-full",
		BaseBranch:  "develop",
		Priority:    1,
		Description: "A detailed description",
		Acceptance:  "Must pass all tests",
		DesignNotes: "Use the factory pattern",
		Assignee:    "eng-1",
		ParentID:    &parentID,
		CreatedAt:   now,
		UpdatedAt:   now,
		ClaimedAt:   &claimed,
		CompletedAt: &completed,
	})

	out, err := execCmd(t, []string{"car", "show", "car-full", "--config", "test.yaml"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	for _, want := range []string{
		"car-full",
		"Fully Populated Car",
		"done",
		"backend",
		"develop",
		"Description:",
		"A detailed description",
		"Acceptance:",
		"Must pass all tests",
		"Design Notes:",
		"Use the factory pattern",
		"Assignee:",
		"eng-1",
		"Parent:",
		"car-parent",
		"Claimed:",
		"Completed:",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("expected output to contain %q, got:\n%s", want, out)
		}
	}
}

func TestRunCarShow_EpicWithChildren(t *testing.T) {
	gormDB := mockTestDB(t)
	cleanup := withMockDB(t, gormDB)
	defer cleanup()

	now := time.Now()
	gormDB.Create(&models.Car{ID: "car-epic", Title: "My Epic", Status: "open", Type: "epic", Track: "backend", Priority: 2, CreatedAt: now, UpdatedAt: now})

	epicID := "car-epic"
	gormDB.Create(&models.Car{ID: "car-ch1", Title: "Child 1", Status: "open", Track: "backend", ParentID: &epicID, Priority: 2, CreatedAt: now, UpdatedAt: now})
	gormDB.Create(&models.Car{ID: "car-ch2", Title: "Child 2", Status: "done", Track: "backend", ParentID: &epicID, Priority: 2, CreatedAt: now, UpdatedAt: now})

	out, err := execCmd(t, []string{"car", "show", "car-epic", "--config", "test.yaml"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(out, "Children:") {
		t.Errorf("expected output to contain 'Children:', got:\n%s", out)
	}
	// Should report a count of 2 children total.
	if !strings.Contains(out, "2") {
		t.Errorf("expected output to contain child count '2', got:\n%s", out)
	}
}

func TestRunCarShow_WithDeps(t *testing.T) {
	gormDB := mockTestDB(t)
	cleanup := withMockDB(t, gormDB)
	defer cleanup()

	now := time.Now()
	gormDB.Create(&models.Car{ID: "car-dep1", Title: "Blocked Car", Status: "open", Track: "backend", Priority: 2, CreatedAt: now, UpdatedAt: now})
	gormDB.Create(&models.Car{ID: "car-dep2", Title: "Blocker Car", Status: "open", Track: "backend", Priority: 2, CreatedAt: now, UpdatedAt: now})
	gormDB.Create(&models.CarDep{CarID: "car-dep1", BlockedBy: "car-dep2", DepType: "blocks"})

	out, err := execCmd(t, []string{"car", "show", "car-dep1", "--config", "test.yaml"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(out, "Dependencies:") {
		t.Errorf("expected output to contain 'Dependencies:', got:\n%s", out)
	}
	if !strings.Contains(out, "car-dep2") {
		t.Errorf("expected output to contain 'car-dep2', got:\n%s", out)
	}
}

func TestRunCarShow_WithProgress(t *testing.T) {
	gormDB := mockTestDB(t)
	cleanup := withMockDB(t, gormDB)
	defer cleanup()

	now := time.Now()
	gormDB.Create(&models.Car{ID: "car-prg", Title: "Progress Car", Status: "in_progress", Track: "backend", Priority: 2, CreatedAt: now, UpdatedAt: now})
	gormDB.Create(&models.CarProgress{CarID: "car-prg", Note: "Started working", EngineID: "eng-1", Cycle: 1, FilesChanged: "[]", CreatedAt: now.Add(-time.Hour)})
	gormDB.Create(&models.CarProgress{CarID: "car-prg", Note: "Half done", EngineID: "eng-1", Cycle: 2, FilesChanged: "[]", CreatedAt: now})

	out, err := execCmd(t, []string{"car", "show", "car-prg", "--config", "test.yaml"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	for _, want := range []string{"Progress:", "Started working", "Half done"} {
		if !strings.Contains(out, want) {
			t.Errorf("expected output to contain %q, got:\n%s", want, out)
		}
	}
}

func TestRunCarShow_WithCycleMetrics(t *testing.T) {
	gormDB := mockTestDB(t)
	cleanup := withMockDB(t, gormDB)
	defer cleanup()

	now := time.Now()
	gormDB.Create(&models.Car{ID: "car-cyc", Title: "Cycle Car", Status: "in_progress", Track: "backend", Priority: 2, CreatedAt: now, UpdatedAt: now})
	gormDB.Create(&models.CarProgress{CarID: "car-cyc", Note: "First cycle", EngineID: "eng-1", Cycle: 1, FilesChanged: `["a.go","b.go"]`, CreatedAt: now.Add(-2 * time.Hour)})
	gormDB.Create(&models.CarProgress{CarID: "car-cyc", Note: "Second cycle", EngineID: "eng-1", Cycle: 2, FilesChanged: `["c.go"]`, CreatedAt: now.Add(-time.Hour)})
	gormDB.Create(&models.CarProgress{CarID: "car-cyc", Note: "Third cycle", EngineID: "eng-2", Cycle: 3, FilesChanged: `["d.go","e.go","f.go"]`, CreatedAt: now})

	out, err := execCmd(t, []string{"car", "show", "car-cyc", "--config", "test.yaml"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	for _, want := range []string{"Context Cycles:", "Total:", "3", "Avg Duration:", "Files Changed:", "6"} {
		if !strings.Contains(out, want) {
			t.Errorf("expected output to contain %q, got:\n%s", want, out)
		}
	}
}

func TestRunCarList_WithCycles(t *testing.T) {
	gormDB := mockTestDB(t)
	cleanup := withMockDB(t, gormDB)
	defer cleanup()

	now := time.Now()
	gormDB.Create(&models.Car{ID: "car-cyc1", Title: "Car With Cycles", Status: "open", Track: "backend", Priority: 2, CreatedAt: now, UpdatedAt: now})
	gormDB.Create(&models.CarProgress{CarID: "car-cyc1", Note: "Cycle 1", EngineID: "eng-1", Cycle: 1, CreatedAt: now})
	gormDB.Create(&models.CarProgress{CarID: "car-cyc1", Note: "Cycle 2", EngineID: "eng-1", Cycle: 2, CreatedAt: now})

	out, err := execCmd(t, []string{"car", "list", "--config", "test.yaml"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(out, "CYCLES") {
		t.Errorf("expected CYCLES column header, got:\n%s", out)
	}
	if !strings.Contains(out, "2") {
		t.Errorf("expected cycle count '2' for car-cyc1, got:\n%s", out)
	}
}

func TestRunCarShow_WithTokenUsage(t *testing.T) {
	gormDB := mockTestDB(t)
	cleanup := withMockDB(t, gormDB)
	defer cleanup()

	now := time.Now()
	gormDB.Create(&models.Car{ID: "car-tku", Title: "Token Car", Status: "done", Track: "backend", Priority: 2, CreatedAt: now, UpdatedAt: now})
	gormDB.Create(&models.AgentLog{CarID: "car-tku", EngineID: "eng-1", Direction: "out", InputTokens: 5000, OutputTokens: 2000, TokenCount: 7000, Model: "claude-sonnet-4-20250514", CreatedAt: now})

	out, err := execCmd(t, []string{"car", "show", "car-tku", "--config", "test.yaml"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	for _, want := range []string{"Token Usage:", "Input:", "Output:", "Total:"} {
		if !strings.Contains(out, want) {
			t.Errorf("expected output to contain %q, got:\n%s", want, out)
		}
	}
}
