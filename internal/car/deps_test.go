package car

import (
	"strings"
	"testing"
)

// --- AddDep tests ---

func TestAddDep_Valid(t *testing.T) {
	db := testDB(t)

	a := createCar(t, db, CreateOpts{Title: "Car A", Track: "backend"})
	b := createCar(t, db, CreateOpts{Title: "Car B", Track: "backend"})

	if err := AddDep(db, a.ID, b.ID, "blocks"); err != nil {
		t.Fatalf("AddDep: %v", err)
	}

	blockers, _, err := ListDeps(db, a.ID)
	if err != nil {
		t.Fatalf("ListDeps: %v", err)
	}
	if len(blockers) != 1 {
		t.Fatalf("blockers = %d, want 1", len(blockers))
	}
	if blockers[0].BlockedBy != b.ID {
		t.Errorf("BlockedBy = %q, want %q", blockers[0].BlockedBy, b.ID)
	}
}

func TestAddDep_DefaultDepType(t *testing.T) {
	db := testDB(t)

	a := createCar(t, db, CreateOpts{Title: "Car A", Track: "backend"})
	b := createCar(t, db, CreateOpts{Title: "Car B", Track: "backend"})

	if err := AddDep(db, a.ID, b.ID, ""); err != nil {
		t.Fatalf("AddDep: %v", err)
	}

	blockers, _, err := ListDeps(db, a.ID)
	if err != nil {
		t.Fatalf("ListDeps: %v", err)
	}
	if blockers[0].DepType != "blocks" {
		t.Errorf("DepType = %q, want %q (default)", blockers[0].DepType, "blocks")
	}
}

func TestAddDep_SelfDependency(t *testing.T) {
	db := testDB(t)

	a := createCar(t, db, CreateOpts{Title: "Car A", Track: "backend"})

	err := AddDep(db, a.ID, a.ID, "blocks")
	if err == nil {
		t.Fatal("expected error for self-dependency")
	}
	if !strings.Contains(err.Error(), "self-dependency") {
		t.Errorf("error = %q, want to contain %q", err.Error(), "self-dependency")
	}
}

func TestAddDep_CarNotFound(t *testing.T) {
	db := testDB(t)

	a := createCar(t, db, CreateOpts{Title: "Car A", Track: "backend"})

	err := AddDep(db, a.ID, "car-zzzzz", "blocks")
	if err == nil {
		t.Fatal("expected error for non-existent car")
	}
	if !strings.Contains(err.Error(), "car not found") {
		t.Errorf("error = %q, want to contain %q", err.Error(), "car not found")
	}

	err = AddDep(db, "car-zzzzz", a.ID, "blocks")
	if err == nil {
		t.Fatal("expected error for non-existent car")
	}
}

func TestAddDep_CycleDetection(t *testing.T) {
	db := testDB(t)

	a := createCar(t, db, CreateOpts{Title: "Car A", Track: "backend"})
	b := createCar(t, db, CreateOpts{Title: "Car B", Track: "backend"})
	c := createCar(t, db, CreateOpts{Title: "Car C", Track: "backend"})

	// A blocked by B.
	if err := AddDep(db, a.ID, b.ID, "blocks"); err != nil {
		t.Fatalf("AddDep A→B: %v", err)
	}
	// B blocked by C.
	if err := AddDep(db, b.ID, c.ID, "blocks"); err != nil {
		t.Fatalf("AddDep B→C: %v", err)
	}

	// C blocked by A would create cycle: A→B→C→A.
	err := AddDep(db, c.ID, a.ID, "blocks")
	if err == nil {
		t.Fatal("expected error for cycle")
	}
	if !strings.Contains(err.Error(), "cycle") {
		t.Errorf("error = %q, want to contain %q", err.Error(), "cycle")
	}
}

// --- ListDeps tests ---

func TestListDeps_BlockersAndDependents(t *testing.T) {
	db := testDB(t)

	a := createCar(t, db, CreateOpts{Title: "Car A", Track: "backend"})
	b := createCar(t, db, CreateOpts{Title: "Car B", Track: "backend"})
	c := createCar(t, db, CreateOpts{Title: "Car C", Track: "backend"})

	// A is blocked by B.
	AddDep(db, a.ID, b.ID, "blocks")
	// C is blocked by A.
	AddDep(db, c.ID, a.ID, "blocks")

	blockers, dependents, err := ListDeps(db, a.ID)
	if err != nil {
		t.Fatalf("ListDeps: %v", err)
	}
	if len(blockers) != 1 || blockers[0].BlockedBy != b.ID {
		t.Errorf("blockers: got %d, want 1 blocking by %s", len(blockers), b.ID)
	}
	if len(dependents) != 1 || dependents[0].CarID != c.ID {
		t.Errorf("dependents: got %d, want 1 dependent %s", len(dependents), c.ID)
	}
}

func TestListDeps_NoDeps(t *testing.T) {
	db := testDB(t)

	a := createCar(t, db, CreateOpts{Title: "Car A", Track: "backend"})

	blockers, dependents, err := ListDeps(db, a.ID)
	if err != nil {
		t.Fatalf("ListDeps: %v", err)
	}
	if len(blockers) != 0 {
		t.Errorf("blockers: got %d, want 0", len(blockers))
	}
	if len(dependents) != 0 {
		t.Errorf("dependents: got %d, want 0", len(dependents))
	}
}

// --- RemoveDep tests ---

func TestRemoveDep_Valid(t *testing.T) {
	db := testDB(t)

	a := createCar(t, db, CreateOpts{Title: "Car A", Track: "backend"})
	b := createCar(t, db, CreateOpts{Title: "Car B", Track: "backend"})

	AddDep(db, a.ID, b.ID, "blocks")

	if err := RemoveDep(db, a.ID, b.ID); err != nil {
		t.Fatalf("RemoveDep: %v", err)
	}

	blockers, _, err := ListDeps(db, a.ID)
	if err != nil {
		t.Fatalf("ListDeps: %v", err)
	}
	if len(blockers) != 0 {
		t.Errorf("blockers after remove: got %d, want 0", len(blockers))
	}
}

func TestRemoveDep_NotFound(t *testing.T) {
	db := testDB(t)

	err := RemoveDep(db, "car-aaaaa", "car-bbbbb")
	if err == nil {
		t.Fatal("expected error for non-existent dependency")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("error = %q, want to contain %q", err.Error(), "not found")
	}
}

// --- ReadyCars tests ---

func TestReadyCars_BasicFiltering(t *testing.T) {
	db := testDB(t)

	// Create cars in various states. ReadyCars wants: status=open, unassigned, not epic.
	open := createCar(t, db, CreateOpts{Title: "Open task", Track: "backend"})
	db.Model(open).Update("status", "open")

	draft := createCar(t, db, CreateOpts{Title: "Draft task", Track: "backend"})
	_ = draft // stays draft — should not appear.

	epic := createCar(t, db, CreateOpts{Title: "Open epic", Track: "backend", Type: "epic"})
	db.Model(epic).Update("status", "open")
	// Epics should not appear.

	ready, err := ReadyCars(db, "")
	if err != nil {
		t.Fatalf("ReadyCars: %v", err)
	}
	if len(ready) != 1 {
		t.Fatalf("ReadyCars: got %d, want 1", len(ready))
	}
	if ready[0].ID != open.ID {
		t.Errorf("ReadyCars[0].ID = %q, want %q", ready[0].ID, open.ID)
	}
}

func TestReadyCars_ExcludesAssigned(t *testing.T) {
	db := testDB(t)

	c := createCar(t, db, CreateOpts{Title: "Assigned", Track: "backend"})
	db.Model(c).Updates(map[string]interface{}{"status": "open", "assignee": "engine-01"})

	ready, err := ReadyCars(db, "")
	if err != nil {
		t.Fatalf("ReadyCars: %v", err)
	}
	if len(ready) != 0 {
		t.Errorf("ReadyCars: got %d, want 0 (assigned car excluded)", len(ready))
	}
}

func TestReadyCars_ExcludesBlockedByUnresolved(t *testing.T) {
	db := testDB(t)

	a := createCar(t, db, CreateOpts{Title: "Car A", Track: "backend"})
	db.Model(a).Update("status", "open")

	blocker := createCar(t, db, CreateOpts{Title: "Blocker", Track: "backend"})
	db.Model(blocker).Update("status", "open") // open = unresolved.

	AddDep(db, a.ID, blocker.ID, "blocks")

	ready, err := ReadyCars(db, "")
	if err != nil {
		t.Fatalf("ReadyCars: %v", err)
	}

	// A is blocked by an unresolved blocker — should not appear.
	// blocker itself is open and unassigned but also has no unresolved blockers.
	for _, c := range ready {
		if c.ID == a.ID {
			t.Error("blocked car A should not appear in ready list")
		}
	}
}

func TestReadyCars_IncludesWhenBlockerResolved(t *testing.T) {
	db := testDB(t)

	a := createCar(t, db, CreateOpts{Title: "Car A", Track: "backend"})
	db.Model(a).Update("status", "open")

	blocker := createCar(t, db, CreateOpts{Title: "Blocker", Track: "backend"})
	db.Model(blocker).Update("status", "merged") // merged = resolved.

	AddDep(db, a.ID, blocker.ID, "blocks")

	ready, err := ReadyCars(db, "")
	if err != nil {
		t.Fatalf("ReadyCars: %v", err)
	}

	found := false
	for _, c := range ready {
		if c.ID == a.ID {
			found = true
		}
	}
	if !found {
		t.Error("car A should appear in ready list (blocker is resolved)")
	}
}

func TestReadyCars_FilterByTrack(t *testing.T) {
	db := testDB(t)

	be := createCar(t, db, CreateOpts{Title: "Backend", Track: "backend"})
	db.Model(be).Update("status", "open")

	fe := createCar(t, db, CreateOpts{Title: "Frontend", Track: "frontend"})
	db.Model(fe).Update("status", "open")

	ready, err := ReadyCars(db, "backend")
	if err != nil {
		t.Fatalf("ReadyCars: %v", err)
	}
	if len(ready) != 1 {
		t.Fatalf("ReadyCars backend: got %d, want 1", len(ready))
	}
	if ready[0].ID != be.ID {
		t.Errorf("ReadyCars[0].ID = %q, want %q", ready[0].ID, be.ID)
	}
}

func TestReadyCars_AllTracks(t *testing.T) {
	db := testDB(t)

	be := createCar(t, db, CreateOpts{Title: "Backend", Track: "backend"})
	db.Model(be).Update("status", "open")

	fe := createCar(t, db, CreateOpts{Title: "Frontend", Track: "frontend"})
	db.Model(fe).Update("status", "open")

	ready, err := ReadyCars(db, "")
	if err != nil {
		t.Fatalf("ReadyCars: %v", err)
	}
	if len(ready) != 2 {
		t.Errorf("ReadyCars all: got %d, want 2", len(ready))
	}
}

func TestReadyCars_DoneBlockerDoesNotUnblock(t *testing.T) {
	db := testDB(t)

	dependent := createCar(t, db, CreateOpts{Title: "Dependent", Track: "backend"})
	db.Model(dependent).Update("status", "open")

	blocker := createCar(t, db, CreateOpts{Title: "Blocker", Track: "backend"})
	db.Model(blocker).Update("status", "done") // done should NOT unblock.

	AddDep(db, dependent.ID, blocker.ID, "blocks")

	ready, err := ReadyCars(db, "")
	if err != nil {
		t.Fatalf("ReadyCars: %v", err)
	}

	for _, c := range ready {
		if c.ID == dependent.ID {
			t.Error("car with done blocker should NOT appear in ready list")
		}
	}
}

func TestReadyCars_MergedBlockerUnblocks(t *testing.T) {
	db := testDB(t)

	dependent := createCar(t, db, CreateOpts{Title: "Dependent", Track: "backend"})
	db.Model(dependent).Update("status", "open")

	blocker := createCar(t, db, CreateOpts{Title: "Blocker", Track: "backend"})
	db.Model(blocker).Update("status", "merged") // merged SHOULD unblock.

	AddDep(db, dependent.ID, blocker.ID, "blocks")

	ready, err := ReadyCars(db, "")
	if err != nil {
		t.Fatalf("ReadyCars: %v", err)
	}

	found := false
	for _, c := range ready {
		if c.ID == dependent.ID {
			found = true
		}
	}
	if !found {
		t.Error("car with merged blocker should appear in ready list")
	}
}

// --- DB error tests ---

func TestAddDep_DBError(t *testing.T) {
	db := closedDB(t)
	err := AddDep(db, "car-aaaaa", "car-bbbbb", "blocks")
	if err == nil {
		t.Fatal("expected error from AddDep with closed DB")
	}
}

func TestListDeps_DBError(t *testing.T) {
	db := closedDB(t)
	_, _, err := ListDeps(db, "car-aaaaa")
	if err == nil {
		t.Fatal("expected error from ListDeps with closed DB")
	}
}

func TestRemoveDep_DBError(t *testing.T) {
	db := closedDB(t)
	err := RemoveDep(db, "car-aaaaa", "car-bbbbb")
	if err == nil {
		t.Fatal("expected error from RemoveDep with closed DB")
	}
}

func TestReadyCars_DBError(t *testing.T) {
	db := closedDB(t)
	_, err := ReadyCars(db, "")
	if err == nil {
		t.Fatal("expected error from ReadyCars with closed DB")
	}
}

func TestReadyCars_ResolvedStatuses(t *testing.T) {
	db := testDB(t)

	// Test all resolved blocker statuses: cancelled, merged.
	for _, resolvedStatus := range []string{"cancelled", "merged"} {
		car := createCar(t, db, CreateOpts{Title: "Ready " + resolvedStatus, Track: "backend"})
		db.Model(car).Update("status", "open")

		blocker := createCar(t, db, CreateOpts{Title: "Blocker " + resolvedStatus, Track: "backend"})
		db.Model(blocker).Update("status", resolvedStatus)

		AddDep(db, car.ID, blocker.ID, "blocks")
	}

	ready, err := ReadyCars(db, "")
	if err != nil {
		t.Fatalf("ReadyCars: %v", err)
	}
	if len(ready) != 2 {
		t.Errorf("ReadyCars with resolved blockers: got %d, want 2", len(ready))
	}
}
