package pluginhost

import (
	"context"
	"testing"
	"time"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/zulandar/railyard/internal/models"
)

// snapshotDB returns an in-memory SQLite handle pre-migrated with the
// tables Snapshot reads. Follows the pattern used by
// internal/dashboard/queries_db_test.go.
func snapshotDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	if err := db.AutoMigrate(
		&models.Track{},
		&models.Engine{},
		&models.Car{},
	); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return db
}

// TestSnapshotShape verifies the snapshot returns the expected entities
// and counts, including the active/terminal split for cars and the
// per-track engine list.
func TestSnapshotShape(t *testing.T) {
	db := snapshotDB(t)

	// Tracks.
	if err := db.Create(&models.Track{Name: "go", Language: "go", EngineSlots: 3}).Error; err != nil {
		t.Fatalf("seed track: %v", err)
	}
	if err := db.Create(&models.Track{Name: "py", Language: "python", EngineSlots: 2}).Error; err != nil {
		t.Fatalf("seed track: %v", err)
	}

	// Engines.
	now := time.Now().UTC()
	if err := db.Create(&models.Engine{
		ID:           "eng-1",
		Track:        "go",
		Status:       "working",
		CurrentCar:   "car-a",
		LastActivity: now,
	}).Error; err != nil {
		t.Fatalf("seed engine: %v", err)
	}
	if err := db.Create(&models.Engine{
		ID:           "eng-2",
		Track:        "py",
		Status:       "idle",
		LastActivity: now,
	}).Error; err != nil {
		t.Fatalf("seed engine: %v", err)
	}

	// Cars: two active (different statuses) and one terminal.
	claimedAt := now.Add(-time.Minute)
	cars := []models.Car{
		{ID: "car-a", Title: "feature A", Track: "go", Status: "in_progress", Type: "feature", Priority: 1, Assignee: "eng-1", Branch: "ry/go/a", RequestedBy: "alice", CreatedAt: now, ClaimedAt: &claimedAt},
		{ID: "car-b", Title: "fix B", Track: "py", Status: "open", Type: "bug", Priority: 2, RequestedBy: "bob", CreatedAt: now},
		{ID: "car-c", Title: "done C", Track: "go", Status: "merged", Type: "feature", Priority: 1, CreatedAt: now},
	}
	for i := range cars {
		if err := db.Create(&cars[i]).Error; err != nil {
			t.Fatalf("seed car %s: %v", cars[i].ID, err)
		}
	}

	host := NewHost(Dependencies{DB: db})
	snap, err := host.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}

	if len(snap.Tracks) != 2 {
		t.Errorf("Tracks count = %d, want 2", len(snap.Tracks))
	}
	if len(snap.Engines) != 2 {
		t.Errorf("Engines count = %d, want 2", len(snap.Engines))
	}

	// Active should contain car-a and car-b, NOT car-c.
	if len(snap.Cars.Active) != 2 {
		t.Errorf("Active count = %d (%v), want 2", len(snap.Cars.Active), snap.Cars.Active)
	}
	for _, c := range snap.Cars.Active {
		if c.ID == "car-c" {
			t.Errorf("terminal car %q leaked into Active set", c.ID)
		}
	}

	// Counts should tally every status (active + terminal).
	if snap.Cars.Counts["in_progress"] != 1 || snap.Cars.Counts["open"] != 1 || snap.Cars.Counts["merged"] != 1 {
		t.Errorf("Counts = %v, want in_progress=1, open=1, merged=1", snap.Cars.Counts)
	}

	// Engine count by status.
	if snap.Stats.EngineCountsByStatus["working"] != 1 || snap.Stats.EngineCountsByStatus["idle"] != 1 {
		t.Errorf("EngineCountsByStatus = %v", snap.Stats.EngineCountsByStatus)
	}

	// Track ActiveEngines join.
	for _, ts := range snap.Tracks {
		switch ts.Name {
		case "go":
			if len(ts.ActiveEngines) != 1 || ts.ActiveEngines[0] != "eng-1" {
				t.Errorf("track go ActiveEngines = %v, want [eng-1]", ts.ActiveEngines)
			}
		case "py":
			if len(ts.ActiveEngines) != 1 || ts.ActiveEngines[0] != "eng-2" {
				t.Errorf("track py ActiveEngines = %v, want [eng-2]", ts.ActiveEngines)
			}
		}
	}

	// Timestamp roughly current.
	if time.Since(snap.Timestamp) > time.Minute {
		t.Errorf("Timestamp %v is not recent", snap.Timestamp)
	}

	// Yardmaster placeholder status.
	if snap.Yardmaster.Status != "running" {
		t.Errorf("Yardmaster.Status = %q, want running", snap.Yardmaster.Status)
	}

	// CarSummary fidelity check for the in_progress car.
	var carA *struct{ ID, Branch, RequestedBy string } // sentinel via closure
	_ = carA
	found := false
	for _, c := range snap.Cars.Active {
		if c.ID != "car-a" {
			continue
		}
		found = true
		if c.Branch != "ry/go/a" || c.RequestedBy != "alice" || c.Assignee != "eng-1" {
			t.Errorf("car-a summary mismatch: %+v", c)
		}
		if c.ClaimedAt == nil {
			t.Error("car-a ClaimedAt should be set")
		}
	}
	if !found {
		t.Error("car-a missing from Active")
	}
}

// TestSnapshotRequiresDB confirms a nil DB returns a clear error rather
// than panicking.
func TestSnapshotRequiresDB(t *testing.T) {
	host := NewHost(Dependencies{})
	_, err := host.Snapshot(context.Background())
	if err == nil {
		t.Fatal("expected error when DB is nil")
	}
}
