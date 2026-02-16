//go:build integration

package car

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

func createTestCar(t *testing.T, gormDB *gorm.DB, title, track string) string {
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
	a := createTestCar(t, gormDB, "Car A", "backend")
	b := createTestCar(t, gormDB, "Car B", "backend")

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
	a := createTestCar(t, gormDB, "Car A", "backend")
	b := createTestCar(t, gormDB, "Car B", "backend")

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
	a := createTestCar(t, gormDB, "Car A", "backend")

	err := AddDep(gormDB, a, a, "blocks")
	if err == nil {
		t.Fatal("expected error for self-dependency")
	}
	if !strings.Contains(err.Error(), "self-dependency") {
		t.Errorf("error = %q, want to contain %q", err.Error(), "self-dependency")
	}
}

func TestIntegration_AddDep_CarNotFound(t *testing.T) {
	gormDB := setupDepsDB(t, "ry_dep_nf")
	a := createTestCar(t, gormDB, "Car A", "backend")

	err := AddDep(gormDB, a, "car-zzzzz", "blocks")
	if err == nil {
		t.Fatal("expected error for non-existent blocker")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("error = %q, want to contain %q", err.Error(), "not found")
	}

	err = AddDep(gormDB, "car-zzzzz", a, "blocks")
	if err == nil {
		t.Fatal("expected error for non-existent car")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("error = %q, want to contain %q", err.Error(), "not found")
	}
}

func TestIntegration_AddDep_SimpleCycle(t *testing.T) {
	gormDB := setupDepsDB(t, "ry_dep_cycle")
	a := createTestCar(t, gormDB, "Car A", "backend")
	b := createTestCar(t, gormDB, "Car B", "backend")

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
	a := createTestCar(t, gormDB, "Car A", "backend")
	b := createTestCar(t, gormDB, "Car B", "backend")
	c := createTestCar(t, gormDB, "Car C", "backend")

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
	a := createTestCar(t, gormDB, "Car A", "backend")
	b := createTestCar(t, gormDB, "Car B", "backend")
	c := createTestCar(t, gormDB, "Car C", "backend")

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
	if dependents[0].CarID != a {
		t.Errorf("B's dependent = %q, want %q", dependents[0].CarID, a)
	}
}

func TestIntegration_ListDeps_Empty(t *testing.T) {
	gormDB := setupDepsDB(t, "ry_dep_empty")
	a := createTestCar(t, gormDB, "Car A", "backend")

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
	a := createTestCar(t, gormDB, "Car A", "backend")
	b := createTestCar(t, gormDB, "Car B", "backend")

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
	err := RemoveDep(gormDB, "car-aaaaa", "car-bbbbb")
	if err == nil {
		t.Fatal("expected error for non-existent dependency")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("error = %q, want to contain %q", err.Error(), "not found")
	}
}

func TestIntegration_ReadyCars_NoDeps(t *testing.T) {
	gormDB := setupDepsDB(t, "ry_ready_nodep")
	createTestCar(t, gormDB, "Ready car", "backend")

	ready, err := ReadyCars(gormDB, "backend")
	if err != nil {
		t.Fatalf("ReadyCars: %v", err)
	}
	if len(ready) != 1 {
		t.Errorf("expected 1 ready car, got %d", len(ready))
	}
}

func TestIntegration_ReadyCars_BlockedByOpen(t *testing.T) {
	gormDB := setupDepsDB(t, "ry_ready_blk")
	a := createTestCar(t, gormDB, "Blocked car", "backend")
	b := createTestCar(t, gormDB, "Blocker (open)", "backend")

	if err := AddDep(gormDB, a, b, "blocks"); err != nil {
		t.Fatalf("AddDep: %v", err)
	}

	ready, err := ReadyCars(gormDB, "backend")
	if err != nil {
		t.Fatalf("ReadyCars: %v", err)
	}
	// Only B should be ready (no deps), A is blocked by B which is still open
	if len(ready) != 1 {
		t.Fatalf("expected 1 ready car, got %d", len(ready))
	}
	if ready[0].ID != b {
		t.Errorf("ready car = %q, want %q (the one without deps)", ready[0].ID, b)
	}
}

func TestIntegration_ReadyCars_BlockerDone(t *testing.T) {
	gormDB := setupDepsDB(t, "ry_ready_done")
	a := createTestCar(t, gormDB, "Was blocked", "backend")
	b := createTestCar(t, gormDB, "Blocker", "backend")

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

	ready, err := ReadyCars(gormDB, "backend")
	if err != nil {
		t.Fatalf("ReadyCars: %v", err)
	}
	// A should now be ready since B is done
	if len(ready) != 1 {
		t.Fatalf("expected 1 ready car, got %d", len(ready))
	}
	if ready[0].ID != a {
		t.Errorf("ready car = %q, want %q", ready[0].ID, a)
	}
}

func TestIntegration_ReadyCars_BlockerCancelled(t *testing.T) {
	gormDB := setupDepsDB(t, "ry_ready_cancel")
	a := createTestCar(t, gormDB, "Was blocked", "backend")
	b := createTestCar(t, gormDB, "Blocker", "backend")

	if err := AddDep(gormDB, a, b, "blocks"); err != nil {
		t.Fatalf("AddDep: %v", err)
	}

	// Cancel B
	if err := Update(gormDB, b, map[string]interface{}{"status": "cancelled"}); err != nil {
		t.Fatalf("Update B→cancelled: %v", err)
	}

	ready, err := ReadyCars(gormDB, "backend")
	if err != nil {
		t.Fatalf("ReadyCars: %v", err)
	}
	if len(ready) != 1 {
		t.Fatalf("expected 1 ready car, got %d", len(ready))
	}
	if ready[0].ID != a {
		t.Errorf("ready car = %q, want %q", ready[0].ID, a)
	}
}

func TestIntegration_ReadyCars_PartialBlockers(t *testing.T) {
	gormDB := setupDepsDB(t, "ry_ready_partial")
	a := createTestCar(t, gormDB, "Blocked by two", "backend")
	b := createTestCar(t, gormDB, "Blocker 1", "backend")
	c := createTestCar(t, gormDB, "Blocker 2", "backend")

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

	ready, err := ReadyCars(gormDB, "backend")
	if err != nil {
		t.Fatalf("ReadyCars: %v", err)
	}
	// A should NOT be ready — C is still open
	for _, r := range ready {
		if r.ID == a {
			t.Errorf("car A should not be ready; blocker C is still open")
		}
	}
}

func TestIntegration_ReadyCars_TrackFilter(t *testing.T) {
	gormDB := setupDepsDB(t, "ry_ready_track")
	createTestCar(t, gormDB, "Backend car", "backend")
	createTestCar(t, gormDB, "Frontend car", "frontend")

	be, err := ReadyCars(gormDB, "backend")
	if err != nil {
		t.Fatalf("ReadyCars backend: %v", err)
	}
	if len(be) != 1 {
		t.Errorf("ReadyCars backend: got %d, want 1", len(be))
	}

	fe, err := ReadyCars(gormDB, "frontend")
	if err != nil {
		t.Fatalf("ReadyCars frontend: %v", err)
	}
	if len(fe) != 1 {
		t.Errorf("ReadyCars frontend: got %d, want 1", len(fe))
	}

	all, err := ReadyCars(gormDB, "")
	if err != nil {
		t.Fatalf("ReadyCars all: %v", err)
	}
	if len(all) != 2 {
		t.Errorf("ReadyCars all: got %d, want 2", len(all))
	}
}

func TestIntegration_ReadyCars_AssignedNotReady(t *testing.T) {
	gormDB := setupDepsDB(t, "ry_ready_assigned")
	a := createTestCar(t, gormDB, "Assigned car", "backend")

	// Move to claimed (assigns it)
	if err := Update(gormDB, a, map[string]interface{}{"status": "ready"}); err != nil {
		t.Fatalf("ready: %v", err)
	}
	if err := Update(gormDB, a, map[string]interface{}{"status": "claimed", "assignee": "engine-01"}); err != nil {
		t.Fatalf("claimed: %v", err)
	}

	ready, err := ReadyCars(gormDB, "backend")
	if err != nil {
		t.Fatalf("ReadyCars: %v", err)
	}
	if len(ready) != 0 {
		t.Errorf("assigned car should not be ready, got %d", len(ready))
	}
}

func TestIntegration_ReadyCars_PriorityOrder(t *testing.T) {
	gormDB := setupDepsDB(t, "ry_ready_prio")

	// Create cars with different priorities (lower = higher priority)
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

	ready, err := ReadyCars(gormDB, "backend")
	if err != nil {
		t.Fatalf("ReadyCars: %v", err)
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

func TestIntegration_ReadyCars_Empty(t *testing.T) {
	gormDB := setupDepsDB(t, "ry_ready_empty")

	ready, err := ReadyCars(gormDB, "backend")
	if err != nil {
		t.Fatalf("ReadyCars: %v", err)
	}
	if len(ready) != 0 {
		t.Errorf("expected 0 ready cars on empty DB, got %d", len(ready))
	}
}

func TestIntegration_AddDep_DBError(t *testing.T) {
	gormDB := closedGormDB(t)
	err := AddDep(gormDB, "car-aaaaa", "car-bbbbb", "blocks")
	if err == nil {
		t.Fatal("expected error from AddDep with closed DB")
	}
}

func TestIntegration_ListDeps_DBError(t *testing.T) {
	gormDB := closedGormDB(t)
	_, _, err := ListDeps(gormDB, "car-aaaaa")
	if err == nil {
		t.Fatal("expected error from ListDeps with closed DB")
	}
}

func TestIntegration_RemoveDep_DBError(t *testing.T) {
	gormDB := closedGormDB(t)
	err := RemoveDep(gormDB, "car-aaaaa", "car-bbbbb")
	if err == nil {
		t.Fatal("expected error from RemoveDep with closed DB")
	}
}

func TestIntegration_ReadyCars_DBError(t *testing.T) {
	gormDB := closedGormDB(t)
	_, err := ReadyCars(gormDB, "backend")
	if err == nil {
		t.Fatal("expected error from ReadyCars with closed DB")
	}
}
