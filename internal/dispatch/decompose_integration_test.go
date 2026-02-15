package dispatch

import (
	"fmt"
	"strings"
	"testing"
)

// Test that a realistic multi-track auth decomposition validates correctly.
func TestIntegration_MultiTrackAuthPlan(t *testing.T) {
	plan := &DecompositionPlan{
		Beads: []BeadPlan{
			// Backend track
			{ID: "be-001", Title: "User Auth Backend Epic", Track: "backend", Type: "epic", Priority: 0, Acceptance: ">90% coverage"},
			{ID: "be-002", Title: "User model and migration", Track: "backend", Type: "task", Priority: 0, ParentID: "be-001", Acceptance: ">90% coverage"},
			{ID: "be-003", Title: "POST /auth/login JWT", Track: "backend", Type: "task", Priority: 0, ParentID: "be-001", Acceptance: ">90% coverage"},
			{ID: "be-004", Title: "POST /auth/register", Track: "backend", Type: "task", Priority: 0, ParentID: "be-001", Acceptance: ">90% coverage"},
			{ID: "be-005", Title: "JWT middleware", Track: "backend", Type: "task", Priority: 1, ParentID: "be-001", Acceptance: ">90% coverage"},
			// Frontend track
			{ID: "fe-001", Title: "User Auth Frontend Epic", Track: "frontend", Type: "epic", Priority: 1, Acceptance: ">90% coverage"},
			{ID: "fe-002", Title: "Login page", Track: "frontend", Type: "task", Priority: 1, ParentID: "fe-001", Acceptance: ">90% coverage"},
			{ID: "fe-003", Title: "Auth context provider", Track: "frontend", Type: "task", Priority: 1, ParentID: "fe-001", Acceptance: ">90% coverage"},
			{ID: "fe-004", Title: "Protected route wrapper", Track: "frontend", Type: "task", Priority: 2, ParentID: "fe-001", Acceptance: ">90% coverage"},
		},
		Deps: []DepPlan{
			{BeadID: "be-003", BlockedBy: "be-002"},
			{BeadID: "be-004", BlockedBy: "be-002"},
			{BeadID: "be-005", BlockedBy: "be-003"},
			// Cross-track deps
			{BeadID: "fe-002", BlockedBy: "be-003"},
			{BeadID: "fe-003", BlockedBy: "be-003"},
			{BeadID: "fe-004", BlockedBy: "fe-003"},
		},
	}

	errs := ValidatePlan(plan)
	if len(errs) != 0 {
		t.Errorf("expected valid plan, got errors: %v", errs)
	}

	// Verify track summary
	summary := TrackSummary(plan)
	if len(summary["backend"]) != 5 {
		t.Errorf("backend count = %d, want 5", len(summary["backend"]))
	}
	if len(summary["frontend"]) != 4 {
		t.Errorf("frontend count = %d, want 4", len(summary["frontend"]))
	}
}

// Test that a multi-track plan with a cross-track cycle is detected.
func TestIntegration_CrossTrackCycle(t *testing.T) {
	plan := &DecompositionPlan{
		Beads: []BeadPlan{
			{ID: "be-001", Title: "Backend API", Track: "backend", Type: "task", Priority: 0, Acceptance: "tests pass"},
			{ID: "be-002", Title: "Backend handler", Track: "backend", Type: "task", Priority: 1, Acceptance: "tests pass"},
			{ID: "fe-001", Title: "Frontend consumer", Track: "frontend", Type: "task", Priority: 1, Acceptance: "tests pass"},
			{ID: "fe-002", Title: "Frontend widget", Track: "frontend", Type: "task", Priority: 2, Acceptance: "tests pass"},
		},
		Deps: []DepPlan{
			{BeadID: "be-002", BlockedBy: "be-001"},
			{BeadID: "fe-001", BlockedBy: "be-002"},
			{BeadID: "fe-002", BlockedBy: "fe-001"},
			// Cross-track cycle: backend depends on frontend, creating a cycle
			{BeadID: "be-001", BlockedBy: "fe-002"},
		},
	}

	errs := ValidatePlan(plan)
	foundCycle := false
	for _, e := range errs {
		if strings.Contains(e, "cycle detected") {
			foundCycle = true
		}
	}
	if !foundCycle {
		t.Errorf("expected cross-track cycle to be detected, got errors: %v", errs)
	}
}

// Test plan with spike type beads.
func TestIntegration_SpikeBeads(t *testing.T) {
	plan := &DecompositionPlan{
		Beads: []BeadPlan{
			{ID: "sp-001", Title: "Research auth strategies", Track: "backend", Type: "spike", Priority: 0, Acceptance: "decision document produced"},
			{ID: "be-001", Title: "Auth epic", Track: "backend", Type: "epic", Priority: 0, Acceptance: ">90% coverage"},
			{ID: "be-002", Title: "Implement chosen strategy", Track: "backend", Type: "task", Priority: 1, ParentID: "be-001", Acceptance: ">90% coverage"},
		},
		Deps: []DepPlan{
			{BeadID: "be-002", BlockedBy: "sp-001"},
		},
	}

	errs := ValidatePlan(plan)
	if len(errs) != 0 {
		t.Errorf("expected valid plan with spike beads, got errors: %v", errs)
	}

	// Verify spike is tracked
	summary := TrackSummary(plan)
	backendBeads := summary["backend"]
	foundSpike := false
	for _, b := range backendBeads {
		if b.Type == "spike" {
			foundSpike = true
		}
	}
	if !foundSpike {
		t.Error("expected to find a spike bead in backend track summary")
	}
}

// Test plan with all priority levels (P0 through P4).
func TestIntegration_AllPriorityLevels(t *testing.T) {
	plan := &DecompositionPlan{
		Beads: []BeadPlan{
			{ID: "b-p0", Title: "Foundation", Track: "backend", Type: "task", Priority: 0, Acceptance: "done"},
			{ID: "b-p1", Title: "Core feature", Track: "backend", Type: "task", Priority: 1, Acceptance: "done"},
			{ID: "b-p2", Title: "Polish", Track: "backend", Type: "task", Priority: 2, Acceptance: "done"},
			{ID: "b-p3", Title: "Nice to have", Track: "backend", Type: "task", Priority: 3, Acceptance: "done"},
			{ID: "b-p4", Title: "Stretch goal", Track: "backend", Type: "task", Priority: 4, Acceptance: "done"},
		},
	}

	errs := ValidatePlan(plan)
	if len(errs) != 0 {
		t.Errorf("expected valid plan with all priority levels, got errors: %v", errs)
	}
}

// Test that priority out of range (negative) is rejected.
func TestIntegration_PriorityOutOfRange_Negative(t *testing.T) {
	plan := &DecompositionPlan{
		Beads: []BeadPlan{
			{ID: "b-001", Title: "Negative priority", Track: "backend", Type: "task", Priority: -1, Acceptance: "done"},
		},
	}

	errs := ValidatePlan(plan)
	foundPriority := false
	for _, e := range errs {
		if strings.Contains(e, "priority") && strings.Contains(e, "out of range") {
			foundPriority = true
		}
	}
	if !foundPriority {
		t.Errorf("expected priority out of range error for -1, got: %v", errs)
	}
}

// Test that priority out of range (too high) is rejected.
func TestIntegration_PriorityOutOfRange_TooHigh(t *testing.T) {
	plan := &DecompositionPlan{
		Beads: []BeadPlan{
			{ID: "b-001", Title: "Priority too high", Track: "backend", Type: "task", Priority: 5, Acceptance: "done"},
		},
	}

	errs := ValidatePlan(plan)
	foundPriority := false
	for _, e := range errs {
		if strings.Contains(e, "priority") && strings.Contains(e, "out of range") {
			foundPriority = true
		}
	}
	if !foundPriority {
		t.Errorf("expected priority out of range error for 5, got: %v", errs)
	}
}

// Test large plan (20+ beads) validation performance.
func TestIntegration_LargePlan(t *testing.T) {
	beads := make([]BeadPlan, 0, 25)
	deps := make([]DepPlan, 0, 24)

	// Create 5 tracks with 5 beads each (25 total)
	tracks := []string{"backend", "frontend", "infra", "mobile", "data"}
	for ti, track := range tracks {
		epicID := fmt.Sprintf("%s-epic", track)
		beads = append(beads, BeadPlan{
			ID:         epicID,
			Title:      fmt.Sprintf("%s Epic", track),
			Track:      track,
			Type:       "epic",
			Priority:   0,
			Acceptance: ">90% coverage",
		})
		for j := 1; j <= 4; j++ {
			beadID := fmt.Sprintf("%s-%03d", track, j)
			beads = append(beads, BeadPlan{
				ID:         beadID,
				Title:      fmt.Sprintf("%s task %d", track, j),
				Track:      track,
				Type:       "task",
				Priority:   j % 5,
				ParentID:   epicID,
				Acceptance: ">90% coverage",
			})
			if j > 1 {
				// Chain within track
				prevID := fmt.Sprintf("%s-%03d", track, j-1)
				deps = append(deps, DepPlan{BeadID: beadID, BlockedBy: prevID})
			}
		}
		// Cross-track deps: each track's first task depends on previous track's last task
		if ti > 0 {
			prevTrack := tracks[ti-1]
			deps = append(deps, DepPlan{
				BeadID:    fmt.Sprintf("%s-001", track),
				BlockedBy: fmt.Sprintf("%s-004", prevTrack),
			})
		}
	}

	plan := &DecompositionPlan{Beads: beads, Deps: deps}

	errs := ValidatePlan(plan)
	if len(errs) != 0 {
		t.Errorf("expected valid large plan (25 beads, 5 tracks), got errors: %v", errs)
	}

	// Verify all tracks present in summary
	summary := TrackSummary(plan)
	if len(summary) != 5 {
		t.Errorf("expected 5 tracks in summary, got %d", len(summary))
	}
	for _, track := range tracks {
		if len(summary[track]) != 5 {
			t.Errorf("track %q: expected 5 beads, got %d", track, len(summary[track]))
		}
	}
}

// Test that missing acceptance criteria is caught.
func TestIntegration_MissingAcceptanceCriteria(t *testing.T) {
	plan := &DecompositionPlan{
		Beads: []BeadPlan{
			{ID: "be-001", Title: "Epic", Track: "backend", Type: "epic", Priority: 0, Acceptance: ">90% coverage"},
			{ID: "be-002", Title: "Task without acceptance", Track: "backend", Type: "task", Priority: 1, ParentID: "be-001", Acceptance: ""},
			{ID: "be-003", Title: "Another task without acceptance", Track: "backend", Type: "task", Priority: 1, ParentID: "be-001", Acceptance: ""},
		},
	}

	errs := ValidatePlan(plan)
	acceptanceErrors := 0
	for _, e := range errs {
		if strings.Contains(e, "acceptance criteria required") {
			acceptanceErrors++
		}
	}
	if acceptanceErrors != 2 {
		t.Errorf("expected 2 acceptance criteria errors, got %d; all errors: %v", acceptanceErrors, errs)
	}
}

// Test that deps referencing non-existent beads are caught.
func TestIntegration_DepsReferencingNonExistentBeads(t *testing.T) {
	plan := &DecompositionPlan{
		Beads: []BeadPlan{
			{ID: "be-001", Title: "Real bead", Track: "backend", Type: "task", Priority: 0, Acceptance: "done"},
		},
		Deps: []DepPlan{
			{BeadID: "be-001", BlockedBy: "ghost-001"},
			{BeadID: "ghost-002", BlockedBy: "be-001"},
		},
	}

	errs := ValidatePlan(plan)
	notFoundErrors := 0
	for _, e := range errs {
		if strings.Contains(e, "not found in plan") {
			notFoundErrors++
		}
	}
	if notFoundErrors != 2 {
		t.Errorf("expected 2 'not found' errors for non-existent beads, got %d; all errors: %v", notFoundErrors, errs)
	}
}

// Test multi-track plan with no dependencies (valid — deps are optional).
func TestIntegration_NoDeps(t *testing.T) {
	plan := &DecompositionPlan{
		Beads: []BeadPlan{
			{ID: "be-001", Title: "Backend task", Track: "backend", Type: "task", Priority: 0, Acceptance: "done"},
			{ID: "fe-001", Title: "Frontend task", Track: "frontend", Type: "task", Priority: 0, Acceptance: "done"},
		},
	}

	errs := ValidatePlan(plan)
	if len(errs) != 0 {
		t.Errorf("expected valid plan with no deps, got errors: %v", errs)
	}
}

// Test that duplicate IDs across tracks are caught.
func TestIntegration_DuplicateIDsAcrossTracks(t *testing.T) {
	plan := &DecompositionPlan{
		Beads: []BeadPlan{
			{ID: "shared-001", Title: "Backend task", Track: "backend", Type: "task", Priority: 0, Acceptance: "done"},
			{ID: "shared-001", Title: "Frontend task", Track: "frontend", Type: "task", Priority: 0, Acceptance: "done"},
		},
	}

	errs := ValidatePlan(plan)
	foundDup := false
	for _, e := range errs {
		if strings.Contains(e, "duplicate ID") {
			foundDup = true
		}
	}
	if !foundDup {
		t.Errorf("expected duplicate ID error, got: %v", errs)
	}
}

// Test three-track plan with complex cross-track dependencies.
func TestIntegration_ThreeTrackPlan(t *testing.T) {
	plan := &DecompositionPlan{
		Beads: []BeadPlan{
			// Infra track
			{ID: "infra-001", Title: "Database setup", Track: "infra", Type: "task", Priority: 0, Acceptance: "DB accessible"},
			// Backend track
			{ID: "be-001", Title: "API epic", Track: "backend", Type: "epic", Priority: 0, Acceptance: ">90% coverage"},
			{ID: "be-002", Title: "Models", Track: "backend", Type: "task", Priority: 0, ParentID: "be-001", Acceptance: ">90% coverage"},
			{ID: "be-003", Title: "Handlers", Track: "backend", Type: "task", Priority: 1, ParentID: "be-001", Acceptance: ">90% coverage"},
			// Frontend track
			{ID: "fe-001", Title: "UI epic", Track: "frontend", Type: "epic", Priority: 1, Acceptance: ">90% coverage"},
			{ID: "fe-002", Title: "Components", Track: "frontend", Type: "task", Priority: 1, ParentID: "fe-001", Acceptance: ">90% coverage"},
		},
		Deps: []DepPlan{
			// Infra -> Backend
			{BeadID: "be-002", BlockedBy: "infra-001"},
			// Backend chain
			{BeadID: "be-003", BlockedBy: "be-002"},
			// Backend -> Frontend
			{BeadID: "fe-002", BlockedBy: "be-003"},
		},
	}

	errs := ValidatePlan(plan)
	if len(errs) != 0 {
		t.Errorf("expected valid three-track plan, got errors: %v", errs)
	}

	summary := TrackSummary(plan)
	if len(summary) != 3 {
		t.Errorf("expected 3 tracks, got %d", len(summary))
	}
	if len(summary["infra"]) != 1 {
		t.Errorf("infra count = %d, want 1", len(summary["infra"]))
	}
	if len(summary["backend"]) != 3 {
		t.Errorf("backend count = %d, want 3", len(summary["backend"]))
	}
	if len(summary["frontend"]) != 2 {
		t.Errorf("frontend count = %d, want 2", len(summary["frontend"]))
	}
}
