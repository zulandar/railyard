package dispatch

import (
	"strings"
	"testing"
)

func TestValidatePlan_Nil(t *testing.T) {
	errs := ValidatePlan(nil)
	if len(errs) != 1 || errs[0] != "plan is nil" {
		t.Errorf("errs = %v, want [plan is nil]", errs)
	}
}

func TestValidatePlan_Empty(t *testing.T) {
	errs := ValidatePlan(&DecompositionPlan{})
	if len(errs) == 0 {
		t.Fatal("expected errors for empty plan")
	}
	found := false
	for _, e := range errs {
		if strings.Contains(e, "no beads") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected 'no beads' error, got: %v", errs)
	}
}

func TestValidatePlan_Valid(t *testing.T) {
	plan := &DecompositionPlan{
		Beads: []BeadPlan{
			{ID: "be-001", Title: "Epic", Track: "backend", Type: "epic", Acceptance: ">90% coverage"},
			{ID: "be-002", Title: "Task", Track: "backend", Type: "task", ParentID: "be-001", Acceptance: ">90% coverage"},
		},
		Deps: []DepPlan{
			{BeadID: "be-002", BlockedBy: "be-001"},
		},
	}
	errs := ValidatePlan(plan)
	if len(errs) != 0 {
		t.Errorf("expected no errors, got: %v", errs)
	}
}

func TestValidatePlan_MissingFields(t *testing.T) {
	plan := &DecompositionPlan{
		Beads: []BeadPlan{
			{ID: "", Title: "", Track: "", Type: "", Acceptance: ""},
		},
	}
	errs := ValidatePlan(plan)
	if len(errs) < 4 {
		t.Errorf("expected at least 4 errors, got %d: %v", len(errs), errs)
	}
}

func TestValidatePlan_InvalidType(t *testing.T) {
	plan := &DecompositionPlan{
		Beads: []BeadPlan{
			{ID: "be-001", Title: "Test", Track: "backend", Type: "invalid", Acceptance: "done"},
		},
	}
	errs := ValidatePlan(plan)
	found := false
	for _, e := range errs {
		if strings.Contains(e, "invalid type") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected 'invalid type' error, got: %v", errs)
	}
}

func TestValidatePlan_InvalidPriority(t *testing.T) {
	plan := &DecompositionPlan{
		Beads: []BeadPlan{
			{ID: "be-001", Title: "Test", Track: "backend", Type: "task", Priority: 5, Acceptance: "done"},
		},
	}
	errs := ValidatePlan(plan)
	found := false
	for _, e := range errs {
		if strings.Contains(e, "priority") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected priority error, got: %v", errs)
	}
}

func TestValidatePlan_DuplicateID(t *testing.T) {
	plan := &DecompositionPlan{
		Beads: []BeadPlan{
			{ID: "be-001", Title: "A", Track: "backend", Type: "task", Acceptance: "done"},
			{ID: "be-001", Title: "B", Track: "backend", Type: "task", Acceptance: "done"},
		},
	}
	errs := ValidatePlan(plan)
	found := false
	for _, e := range errs {
		if strings.Contains(e, "duplicate ID") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected 'duplicate ID' error, got: %v", errs)
	}
}

func TestValidatePlan_BadParentRef(t *testing.T) {
	plan := &DecompositionPlan{
		Beads: []BeadPlan{
			{ID: "be-001", Title: "Test", Track: "backend", Type: "task", ParentID: "be-999", Acceptance: "done"},
		},
	}
	errs := ValidatePlan(plan)
	found := false
	for _, e := range errs {
		if strings.Contains(e, "parent") && strings.Contains(e, "not found") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected 'parent not found' error, got: %v", errs)
	}
}

func TestValidatePlan_SelfDep(t *testing.T) {
	plan := &DecompositionPlan{
		Beads: []BeadPlan{
			{ID: "be-001", Title: "Test", Track: "backend", Type: "task", Acceptance: "done"},
		},
		Deps: []DepPlan{
			{BeadID: "be-001", BlockedBy: "be-001"},
		},
	}
	errs := ValidatePlan(plan)
	found := false
	for _, e := range errs {
		if strings.Contains(e, "cannot depend on itself") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected self-dep error, got: %v", errs)
	}
}

func TestDetectCycle_NoCycle(t *testing.T) {
	deps := []DepPlan{
		{BeadID: "b", BlockedBy: "a"},
		{BeadID: "c", BlockedBy: "b"},
	}
	if cycle := DetectCycle(deps); cycle != nil {
		t.Errorf("expected no cycle, got: %v", cycle)
	}
}

func TestDetectCycle_SimpleCycle(t *testing.T) {
	deps := []DepPlan{
		{BeadID: "a", BlockedBy: "b"},
		{BeadID: "b", BlockedBy: "a"},
	}
	cycle := DetectCycle(deps)
	if cycle == nil {
		t.Fatal("expected cycle")
	}
	joined := strings.Join(cycle, " → ")
	if !strings.Contains(joined, "a") || !strings.Contains(joined, "b") {
		t.Errorf("cycle should contain a and b, got: %s", joined)
	}
}

func TestDetectCycle_ThreeNodeCycle(t *testing.T) {
	deps := []DepPlan{
		{BeadID: "a", BlockedBy: "b"},
		{BeadID: "b", BlockedBy: "c"},
		{BeadID: "c", BlockedBy: "a"},
	}
	cycle := DetectCycle(deps)
	if cycle == nil {
		t.Fatal("expected cycle")
	}
}

func TestDetectCycle_Empty(t *testing.T) {
	if cycle := DetectCycle(nil); cycle != nil {
		t.Errorf("expected nil for empty deps, got: %v", cycle)
	}
}

func TestValidatePlan_CycleDetected(t *testing.T) {
	plan := &DecompositionPlan{
		Beads: []BeadPlan{
			{ID: "be-001", Title: "A", Track: "backend", Type: "task", Acceptance: "done"},
			{ID: "be-002", Title: "B", Track: "backend", Type: "task", Acceptance: "done"},
		},
		Deps: []DepPlan{
			{BeadID: "be-001", BlockedBy: "be-002"},
			{BeadID: "be-002", BlockedBy: "be-001"},
		},
	}
	errs := ValidatePlan(plan)
	found := false
	for _, e := range errs {
		if strings.Contains(e, "cycle detected") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected cycle error, got: %v", errs)
	}
}

func TestTrackSummary(t *testing.T) {
	plan := &DecompositionPlan{
		Beads: []BeadPlan{
			{ID: "be-001", Title: "A", Track: "backend", Type: "task"},
			{ID: "be-002", Title: "B", Track: "frontend", Type: "task"},
			{ID: "be-003", Title: "C", Track: "backend", Type: "task"},
		},
	}
	summary := TrackSummary(plan)
	if len(summary["backend"]) != 2 {
		t.Errorf("backend count = %d, want 2", len(summary["backend"]))
	}
	if len(summary["frontend"]) != 1 {
		t.Errorf("frontend count = %d, want 1", len(summary["frontend"]))
	}
}

func TestTrackSummary_Empty(t *testing.T) {
	plan := &DecompositionPlan{}
	summary := TrackSummary(plan)
	if len(summary) != 0 {
		t.Errorf("expected empty summary, got: %v", summary)
	}
}
