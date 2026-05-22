package cli

import (
	"context"
	"strings"
	"testing"

	"github.com/zulandar/railyard/internal/car"
	"github.com/zulandar/railyard/internal/models"
)

// TestForceCompleteAdapter_WritesAuditProgressNote verifies that the
// force_complete plugin-host adapter persists a CarProgress audit row whose
// Note carries the operator-supplied reason and whose EngineID is the
// fixed "<plugin-dispatched>" marker. The status update and the progress
// write must both land — exercising the wrapping transaction's happy path.
func TestForceCompleteAdapter_WritesAuditProgressNote(t *testing.T) {
	gormDB := mockTestDB(t)

	// Create a car and drive it forward into in_progress so the
	// in_progress → done transition the adapter performs is valid.
	c, err := car.Create(gormDB, car.CreateOpts{
		Title:        "force complete audit test",
		Track:        "backend",
		BranchPrefix: "ry/test",
	})
	if err != nil {
		t.Fatalf("car.Create: %v", err)
	}
	for _, status := range []string{"open", "ready", "claimed", "in_progress"} {
		updates := map[string]interface{}{"status": status}
		if status == "claimed" {
			updates["assignee"] = "engine-1"
		}
		if err := car.Update(gormDB, c.ID, updates); err != nil {
			t.Fatalf("seed status %q: %v", status, err)
		}
	}

	const reason = "operator override: blocking yard restart"
	adapter := forceCompleteAdapter(gormDB, nil)
	if err := adapter(context.Background(), c.ID, reason); err != nil {
		t.Fatalf("adapter: %v", err)
	}

	// The status update must have landed.
	var got models.Car
	if err := gormDB.Where("id = ?", c.ID).First(&got).Error; err != nil {
		t.Fatalf("reload car: %v", err)
	}
	if got.Status != "done" {
		t.Errorf("status = %q, want %q", got.Status, "done")
	}

	// The progress note must exist with the right Note + marker EngineID.
	var notes []models.CarProgress
	if err := gormDB.Where("car_id = ?", c.ID).Find(&notes).Error; err != nil {
		t.Fatalf("load progress notes: %v", err)
	}
	if len(notes) != 1 {
		t.Fatalf("progress note count = %d, want 1; notes = %+v", len(notes), notes)
	}
	if notes[0].Note != reason {
		t.Errorf("Note = %q, want %q", notes[0].Note, reason)
	}
	if notes[0].EngineID != "<plugin-dispatched>" {
		t.Errorf("EngineID = %q, want %q", notes[0].EngineID, "<plugin-dispatched>")
	}
}

// TestForceCompleteAdapter_InvalidTransitionRollsBack verifies that when
// UpdateWithBus rejects the status transition (e.g. the car is in a state
// that cannot reach "done"), no CarProgress audit row is left behind. The
// adapter wraps both writes in a single transaction so the progress-note
// insert must roll back together with the failed status update.
func TestForceCompleteAdapter_InvalidTransitionRollsBack(t *testing.T) {
	gormDB := mockTestDB(t)

	// Car stays in "draft" — draft → done is not a valid transition, so
	// UpdateWithBus will return an error before any DB write occurs.
	c, err := car.Create(gormDB, car.CreateOpts{
		Title:        "force complete rollback test",
		Track:        "backend",
		BranchPrefix: "ry/test",
	})
	if err != nil {
		t.Fatalf("car.Create: %v", err)
	}

	adapter := forceCompleteAdapter(gormDB, nil)
	err = adapter(context.Background(), c.ID, "should not persist")
	if err == nil {
		t.Fatal("adapter: expected error, got nil")
	}
	if !strings.Contains(err.Error(), "force_complete") {
		t.Errorf("error = %q, want it to mention force_complete", err.Error())
	}

	// Status must NOT have changed.
	var got models.Car
	if err := gormDB.Where("id = ?", c.ID).First(&got).Error; err != nil {
		t.Fatalf("reload car: %v", err)
	}
	if got.Status != "draft" {
		t.Errorf("status = %q, want %q (rollback failed)", got.Status, "draft")
	}

	// No progress note should exist for this car.
	var count int64
	if err := gormDB.Model(&models.CarProgress{}).Where("car_id = ?", c.ID).Count(&count).Error; err != nil {
		t.Fatalf("count progress notes: %v", err)
	}
	if count != 0 {
		t.Errorf("progress note count = %d, want 0 (rollback failed)", count)
	}
}
