//go:build integration

package bead

import (
	"strings"
	"testing"

	"github.com/zulandar/railyard/internal/db"
	"gorm.io/gorm"
)

func setupDepsDB(t *testing.T, name string) *gorm.DB {
	t.Helper()
	srv := setupTestDB(t, name)
	gormDB, err := db.Connect("127.0.0.1", srv.Port, name)
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	return gormDB
}

func createTestBead(t *testing.T, gormDB *gorm.DB, title, track string) string {
	t.Helper()
	b, err := Create(gormDB, CreateOpts{
		Title: title, Track: track, BranchPrefix: "ry/test",
	})
	if err != nil {
		t.Fatalf("Create %q: %v", title, err)
	}
	return b.ID
}

func TestIntegration_AddDep(t *testing.T) {
	gormDB := setupDepsDB(t, "ry_dep_add")
	a := createTestBead(t, gormDB, "Bead A", "backend")
	b := createTestBead(t, gormDB, "Bead B", "backend")

	if err := AddDep(gormDB, a, b, "blocks"); err != nil {
		t.Fatalf("AddDep: %v", err)
	}

	blockers, _, err := ListDeps(gormDB, a)
	if err != nil {
		t.Fatalf("ListDeps: %v", err)
	}
	if len(blockers) != 1 {
		t.Fatalf("expected 1 blocker, got %d", len(blockers))
	}
	if blockers[0].BlockedBy != b {
		t.Errorf("blocker = %q, want %q", blockers[0].BlockedBy, b)
	}
}

func TestIntegration_AddDep_DefaultType(t *testing.T) {
	gormDB := setupDepsDB(t, "ry_dep_deftype")
	a := createTestBead(t, gormDB, "Bead A", "backend")
	b := createTestBead(t, gormDB, "Bead B", "backend")

	if err := AddDep(gormDB, a, b, ""); err != nil {
		t.Fatalf("AddDep: %v", err)
	}

	blockers, _, err := ListDeps(gormDB, a)
	if err != nil {
		t.Fatalf("ListDeps: %v", err)
	}
	if blockers[0].DepType != "blocks" {
		t.Errorf("DepType = %q, want %q", blockers[0].DepType, "blocks")
	}
}

func TestIntegration_AddDep_SelfDep(t *testing.T) {
	gormDB := setupDepsDB(t, "ry_dep_self")
	a := createTestBead(t, gormDB, "Bead A", "backend")

	err := AddDep(gormDB, a, a, "blocks")
	if err == nil {
		t.Fatal("expected error for self-dependency")
	}
	if !strings.Contains(err.Error(), "self-dependency") {
		t.Errorf("error = %q, want to contain %q", err.Error(), "self-dependency")
	}
}

func TestIntegration_AddDep_BeadNotFound(t *testing.T) {
	gormDB := setupDepsDB(t, "ry_dep_nf")
	a := createTestBead(t, gormDB, "Bead A", "backend")

	err := AddDep(gormDB, a, "be-zzzzz", "blocks")
	if err == nil {
		t.Fatal("expected error for non-existent blocker")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("error = %q, want to contain %q", err.Error(), "not found")
	}

	err = AddDep(gormDB, "be-zzzzz", a, "blocks")
	if err == nil {
		t.Fatal("expected error for non-existent bead")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("error = %q, want to contain %q", err.Error(), "not found")
	}
}

func TestIntegration_AddDep_SimpleCycle(t *testing.T) {
	gormDB := setupDepsDB(t, "ry_dep_cycle")
	a := createTestBead(t, gormDB, "Bead A", "backend")
	b := createTestBead(t, gormDB, "Bead B", "backend")

	// A blocked by B
	if err := AddDep(gormDB, a, b, "blocks"); err != nil {
		t.Fatalf("AddDep A→B: %v", err)
	}

	// B blocked by A would create cycle
	err := AddDep(gormDB, b, a, "blocks")
	if err == nil {
		t.Fatal("expected error for cycle B→A")
	}
	if !strings.Contains(err.Error(), "cycle") {
		t.Errorf("error = %q, want to contain %q", err.Error(), "cycle")
	}
}

func TestIntegration_AddDep_TransitiveCycle(t *testing.T) {
	gormDB := setupDepsDB(t, "ry_dep_tcycle")
	a := createTestBead(t, gormDB, "Bead A", "backend")
	b := createTestBead(t, gormDB, "Bead B", "backend")
	c := createTestBead(t, gormDB, "Bead C", "backend")

	// A blocked by B, B blocked by C
	if err := AddDep(gormDB, a, b, "blocks"); err != nil {
		t.Fatalf("AddDep A→B: %v", err)
	}
	if err := AddDep(gormDB, b, c, "blocks"); err != nil {
		t.Fatalf("AddDep B→C: %v", err)
	}

	// C blocked by A would create cycle A→B→C→A
	err := AddDep(gormDB, c, a, "blocks")
	if err == nil {
		t.Fatal("expected error for transitive cycle C→A")
	}
	if !strings.Contains(err.Error(), "cycle") {
		t.Errorf("error = %q, want to contain %q", err.Error(), "cycle")
	}
}

func TestIntegration_ListDeps_BothDirections(t *testing.T) {
	gormDB := setupDepsDB(t, "ry_dep_both")
	a := createTestBead(t, gormDB, "Bead A", "backend")
	b := createTestBead(t, gormDB, "Bead B", "backend")
	c := createTestBead(t, gormDB, "Bead C", "backend")

	// B blocks A, C blocks A
	if err := AddDep(gormDB, a, b, "blocks"); err != nil {
		t.Fatalf("AddDep A←B: %v", err)
	}
	if err := AddDep(gormDB, a, c, "blocks"); err != nil {
		t.Fatalf("AddDep A←C: %v", err)
	}

	blockers, _, err := ListDeps(gormDB, a)
	if err != nil {
		t.Fatalf("ListDeps A: %v", err)
	}
	if len(blockers) != 2 {
		t.Errorf("A has %d blockers, want 2", len(blockers))
	}

	// B blocks A, so B should show A as a dependent
	_, dependents, err := ListDeps(gormDB, b)
	if err != nil {
		t.Fatalf("ListDeps B: %v", err)
	}
	if len(dependents) != 1 {
		t.Errorf("B has %d dependents, want 1", len(dependents))
	}
	if dependents[0].BeadID != a {
		t.Errorf("B's dependent = %q, want %q", dependents[0].BeadID, a)
	}
}

func TestIntegration_ListDeps_Empty(t *testing.T) {
	gormDB := setupDepsDB(t, "ry_dep_empty")
	a := createTestBead(t, gormDB, "Bead A", "backend")

	blockers, dependents, err := ListDeps(gormDB, a)
	if err != nil {
		t.Fatalf("ListDeps: %v", err)
	}
	if len(blockers) != 0 {
		t.Errorf("expected 0 blockers, got %d", len(blockers))
	}
	if len(dependents) != 0 {
		t.Errorf("expected 0 dependents, got %d", len(dependents))
	}
}

func TestIntegration_RemoveDep(t *testing.T) {
	gormDB := setupDepsDB(t, "ry_dep_rm")
	a := createTestBead(t, gormDB, "Bead A", "backend")
	b := createTestBead(t, gormDB, "Bead B", "backend")

	if err := AddDep(gormDB, a, b, "blocks"); err != nil {
		t.Fatalf("AddDep: %v", err)
	}

	if err := RemoveDep(gormDB, a, b); err != nil {
		t.Fatalf("RemoveDep: %v", err)
	}

	blockers, _, err := ListDeps(gormDB, a)
	if err != nil {
		t.Fatalf("ListDeps: %v", err)
	}
	if len(blockers) != 0 {
		t.Errorf("expected 0 blockers after remove, got %d", len(blockers))
	}
}

func TestIntegration_RemoveDep_NotFound(t *testing.T) {
	gormDB := setupDepsDB(t, "ry_dep_rmnf")
	err := RemoveDep(gormDB, "be-aaaaa", "be-bbbbb")
	if err == nil {
		t.Fatal("expected error for non-existent dependency")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("error = %q, want to contain %q", err.Error(), "not found")
	}
}

func TestIntegration_ReadyBeads_NoDeps(t *testing.T) {
	gormDB := setupDepsDB(t, "ry_ready_nodep")
	createTestBead(t, gormDB, "Ready bead", "backend")

	ready, err := ReadyBeads(gormDB, "backend")
	if err != nil {
		t.Fatalf("ReadyBeads: %v", err)
	}
	if len(ready) != 1 {
		t.Errorf("expected 1 ready bead, got %d", len(ready))
	}
}

func TestIntegration_ReadyBeads_BlockedByOpen(t *testing.T) {
	gormDB := setupDepsDB(t, "ry_ready_blk")
	a := createTestBead(t, gormDB, "Blocked bead", "backend")
	b := createTestBead(t, gormDB, "Blocker (open)", "backend")

	if err := AddDep(gormDB, a, b, "blocks"); err != nil {
		t.Fatalf("AddDep: %v", err)
	}

	ready, err := ReadyBeads(gormDB, "backend")
	if err != nil {
		t.Fatalf("ReadyBeads: %v", err)
	}
	// Only B should be ready (no deps), A is blocked by B which is still open
	if len(ready) != 1 {
		t.Fatalf("expected 1 ready bead, got %d", len(ready))
	}
	if ready[0].ID != b {
		t.Errorf("ready bead = %q, want %q (the one without deps)", ready[0].ID, b)
	}
}

func TestIntegration_ReadyBeads_BlockerDone(t *testing.T) {
	gormDB := setupDepsDB(t, "ry_ready_done")
	a := createTestBead(t, gormDB, "Was blocked", "backend")
	b := createTestBead(t, gormDB, "Blocker", "backend")

	if err := AddDep(gormDB, a, b, "blocks"); err != nil {
		t.Fatalf("AddDep: %v", err)
	}

	// Move B through lifecycle to done
	for _, s := range []string{"ready", "claimed", "in_progress", "done"} {
		updates := map[string]interface{}{"status": s}
		if s == "claimed" {
			updates["assignee"] = "engine-01"
		}
		if err := Update(gormDB, b, updates); err != nil {
			t.Fatalf("Update B→%s: %v", s, err)
		}
	}

	ready, err := ReadyBeads(gormDB, "backend")
	if err != nil {
		t.Fatalf("ReadyBeads: %v", err)
	}
	// A should now be ready since B is done
	if len(ready) != 1 {
		t.Fatalf("expected 1 ready bead, got %d", len(ready))
	}
	if ready[0].ID != a {
		t.Errorf("ready bead = %q, want %q", ready[0].ID, a)
	}
}

func TestIntegration_ReadyBeads_BlockerCancelled(t *testing.T) {
	gormDB := setupDepsDB(t, "ry_ready_cancel")
	a := createTestBead(t, gormDB, "Was blocked", "backend")
	b := createTestBead(t, gormDB, "Blocker", "backend")

	if err := AddDep(gormDB, a, b, "blocks"); err != nil {
		t.Fatalf("AddDep: %v", err)
	}

	// Cancel B
	if err := Update(gormDB, b, map[string]interface{}{"status": "cancelled"}); err != nil {
		t.Fatalf("Update B→cancelled: %v", err)
	}

	ready, err := ReadyBeads(gormDB, "backend")
	if err != nil {
		t.Fatalf("ReadyBeads: %v", err)
	}
	if len(ready) != 1 {
		t.Fatalf("expected 1 ready bead, got %d", len(ready))
	}
	if ready[0].ID != a {
		t.Errorf("ready bead = %q, want %q", ready[0].ID, a)
	}
}

func TestIntegration_ReadyBeads_PartialBlockers(t *testing.T) {
	gormDB := setupDepsDB(t, "ry_ready_partial")
	a := createTestBead(t, gormDB, "Blocked by two", "backend")
	b := createTestBead(t, gormDB, "Blocker 1", "backend")
	c := createTestBead(t, gormDB, "Blocker 2", "backend")

	if err := AddDep(gormDB, a, b, "blocks"); err != nil {
		t.Fatalf("AddDep A←B: %v", err)
	}
	if err := AddDep(gormDB, a, c, "blocks"); err != nil {
		t.Fatalf("AddDep A←C: %v", err)
	}

	// Complete B but leave C open
	for _, s := range []string{"ready", "claimed", "in_progress", "done"} {
		updates := map[string]interface{}{"status": s}
		if s == "claimed" {
			updates["assignee"] = "engine-01"
		}
		if err := Update(gormDB, b, updates); err != nil {
			t.Fatalf("Update B→%s: %v", s, err)
		}
	}

	ready, err := ReadyBeads(gormDB, "backend")
	if err != nil {
		t.Fatalf("ReadyBeads: %v", err)
	}
	// A should NOT be ready — C is still open
	for _, r := range ready {
		if r.ID == a {
			t.Errorf("bead A should not be ready; blocker C is still open")
		}
	}
}

func TestIntegration_ReadyBeads_TrackFilter(t *testing.T) {
	gormDB := setupDepsDB(t, "ry_ready_track")
	createTestBead(t, gormDB, "Backend bead", "backend")
	createTestBead(t, gormDB, "Frontend bead", "frontend")

	be, err := ReadyBeads(gormDB, "backend")
	if err != nil {
		t.Fatalf("ReadyBeads backend: %v", err)
	}
	if len(be) != 1 {
		t.Errorf("ReadyBeads backend: got %d, want 1", len(be))
	}

	fe, err := ReadyBeads(gormDB, "frontend")
	if err != nil {
		t.Fatalf("ReadyBeads frontend: %v", err)
	}
	if len(fe) != 1 {
		t.Errorf("ReadyBeads frontend: got %d, want 1", len(fe))
	}

	all, err := ReadyBeads(gormDB, "")
	if err != nil {
		t.Fatalf("ReadyBeads all: %v", err)
	}
	if len(all) != 2 {
		t.Errorf("ReadyBeads all: got %d, want 2", len(all))
	}
}

func TestIntegration_ReadyBeads_AssignedNotReady(t *testing.T) {
	gormDB := setupDepsDB(t, "ry_ready_assigned")
	a := createTestBead(t, gormDB, "Assigned bead", "backend")

	// Move to claimed (assigns it)
	if err := Update(gormDB, a, map[string]interface{}{"status": "ready"}); err != nil {
		t.Fatalf("ready: %v", err)
	}
	if err := Update(gormDB, a, map[string]interface{}{"status": "claimed", "assignee": "engine-01"}); err != nil {
		t.Fatalf("claimed: %v", err)
	}

	ready, err := ReadyBeads(gormDB, "backend")
	if err != nil {
		t.Fatalf("ReadyBeads: %v", err)
	}
	if len(ready) != 0 {
		t.Errorf("assigned bead should not be ready, got %d", len(ready))
	}
}

func TestIntegration_ReadyBeads_PriorityOrder(t *testing.T) {
	gormDB := setupDepsDB(t, "ry_ready_prio")

	// Create beads with different priorities (lower = higher priority)
	bLow, err := Create(gormDB, CreateOpts{
		Title: "Low priority", Track: "backend", Priority: 3, BranchPrefix: "ry/test",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	bHigh, err := Create(gormDB, CreateOpts{
		Title: "High priority", Track: "backend", Priority: 0, BranchPrefix: "ry/test",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	ready, err := ReadyBeads(gormDB, "backend")
	if err != nil {
		t.Fatalf("ReadyBeads: %v", err)
	}
	if len(ready) != 2 {
		t.Fatalf("expected 2 ready, got %d", len(ready))
	}
	if ready[0].ID != bHigh.ID {
		t.Errorf("first ready = %q (pri=%d), want %q (pri=0)", ready[0].ID, ready[0].Priority, bHigh.ID)
	}
	if ready[1].ID != bLow.ID {
		t.Errorf("second ready = %q, want %q", ready[1].ID, bLow.ID)
	}
}

func TestIntegration_ReadyBeads_Empty(t *testing.T) {
	gormDB := setupDepsDB(t, "ry_ready_empty")

	ready, err := ReadyBeads(gormDB, "backend")
	if err != nil {
		t.Fatalf("ReadyBeads: %v", err)
	}
	if len(ready) != 0 {
		t.Errorf("expected 0 ready beads on empty DB, got %d", len(ready))
	}
}

func TestIntegration_AddDep_DBError(t *testing.T) {
	gormDB := closedGormDB(t)
	err := AddDep(gormDB, "be-aaaaa", "be-bbbbb", "blocks")
	if err == nil {
		t.Fatal("expected error from AddDep with closed DB")
	}
}

func TestIntegration_ListDeps_DBError(t *testing.T) {
	gormDB := closedGormDB(t)
	_, _, err := ListDeps(gormDB, "be-aaaaa")
	if err == nil {
		t.Fatal("expected error from ListDeps with closed DB")
	}
}

func TestIntegration_RemoveDep_DBError(t *testing.T) {
	gormDB := closedGormDB(t)
	err := RemoveDep(gormDB, "be-aaaaa", "be-bbbbb")
	if err == nil {
		t.Fatal("expected error from RemoveDep with closed DB")
	}
}

func TestIntegration_ReadyBeads_DBError(t *testing.T) {
	gormDB := closedGormDB(t)
	_, err := ReadyBeads(gormDB, "backend")
	if err == nil {
		t.Fatal("expected error from ReadyBeads with closed DB")
	}
}
