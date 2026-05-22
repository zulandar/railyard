package cli

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/zulandar/railyard/internal/car"
	"github.com/zulandar/railyard/internal/events"
	"github.com/zulandar/railyard/internal/models"
	"github.com/zulandar/railyard/pkg/plugin"
	"gorm.io/gorm"
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

// TestForceCompleteAdapter_EmptyReasonRejected pins the invariant
// that the audit row's Note must never be empty. The allow-list's
// arg validator only checks the kind=string, so a caller passing
// Reason="" would otherwise satisfy validation and produce an audit
// row whose Note is the empty string — violating the "never a
// force-completed car without a matching reason on file" contract
// the wrapping transaction was added to enforce.
func TestForceCompleteAdapter_EmptyReasonRejected(t *testing.T) {
	gormDB := mockTestDB(t)

	c, err := car.Create(gormDB, car.CreateOpts{
		Title:        "empty reason test",
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

	cases := []struct {
		name   string
		reason string
	}{
		{"empty", ""},
		{"whitespace", "   "},
		{"tab and newline", "\t\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			adapter := forceCompleteAdapter(gormDB, nil)
			err := adapter(context.Background(), c.ID, tc.reason)
			if err == nil {
				t.Fatalf("expected error for reason %q, got nil", tc.reason)
			}
			if !strings.Contains(err.Error(), "reason required") {
				t.Errorf("error = %q, want it to mention 'reason required'", err.Error())
			}
		})
	}

	// Status must remain in_progress — the guard fires before any DB write.
	var got models.Car
	if err := gormDB.Where("id = ?", c.ID).First(&got).Error; err != nil {
		t.Fatalf("reload car: %v", err)
	}
	if got.Status != "in_progress" {
		t.Errorf("status = %q, want %q (guard must short-circuit before tx)", got.Status, "in_progress")
	}

	// No audit row should have been written.
	var notes int64
	if err := gormDB.Model(&models.CarProgress{}).Where("car_id = ?", c.ID).Count(&notes).Error; err != nil {
		t.Fatalf("count notes: %v", err)
	}
	if notes != 0 {
		t.Errorf("note count = %d, want 0", notes)
	}
}

// TestForceCompleteAdapter_PublishesEventAfterCommit wires a real bus
// through the adapter and confirms a CarStatusChanged event is
// delivered to subscribers AFTER the outer transaction commits. The
// previous design routed publish through car.UpdateWithBus inside the
// transaction, so subscribers could observe a "done" transition even
// when the wrapping transaction rolled back. This test pins the
// post-commit semantics so the regression cannot return silently.
func TestForceCompleteAdapter_PublishesEventAfterCommit(t *testing.T) {
	gormDB := mockTestDB(t)

	c, err := car.Create(gormDB, car.CreateOpts{
		Title:        "post-commit publish test",
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

	bus := events.NewBus()
	defer bus.(interface{ Close() }).Close()

	received := make(chan plugin.CarStatusChangedEvent, 4)
	bus.Subscribe(string(plugin.CarStatusChanged), func(payload any) {
		evt, ok := payload.(plugin.CarStatusChangedEvent)
		if !ok {
			t.Errorf("payload type = %T, want CarStatusChangedEvent", payload)
			return
		}
		received <- evt
	})

	adapter := forceCompleteAdapter(gormDB, bus)
	if err := adapter(context.Background(), c.ID, "blocking yard restart"); err != nil {
		t.Fatalf("adapter: %v", err)
	}

	select {
	case evt := <-received:
		if evt.CarID != c.ID {
			t.Errorf("CarID = %q, want %q", evt.CarID, c.ID)
		}
		if evt.OldStatus != "in_progress" {
			t.Errorf("OldStatus = %q, want in_progress", evt.OldStatus)
		}
		if evt.NewStatus != "done" {
			t.Errorf("NewStatus = %q, want done", evt.NewStatus)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("did not receive CarStatusChanged event within timeout")
	}
}

// TestForceCompleteAdapter_NoPhantomEventOnRollback is the headline
// regression test for review comment #1. We inject a GORM callback
// that forces the CarProgress insert to fail, then assert that:
//   - the adapter returns an error
//   - the car's status remains in_progress (rollback worked)
//   - NO CarStatusChanged event reached a real subscribed bus
//
// The previous design — UpdateWithBus publishing inside the
// transaction — would deliver a CarStatusChanged("done") event to a
// subscriber that the rollback later erased from the DB.
func TestForceCompleteAdapter_NoPhantomEventOnRollback(t *testing.T) {
	gormDB := mockTestDB(t)

	c, err := car.Create(gormDB, car.CreateOpts{
		Title:        "phantom event test",
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

	// Inject a Create callback that fails the CarProgress insert.
	if err := gormDB.Callback().Create().Before("gorm:create").Register(
		"test_force_progress_failure",
		func(tx *gorm.DB) {
			if tx.Statement.Table == "car_progresses" {
				_ = tx.AddError(errors.New("simulated audit-row failure"))
			}
		},
	); err != nil {
		t.Fatalf("register callback: %v", err)
	}

	bus := events.NewBus()
	defer bus.(interface{ Close() }).Close()

	var mu sync.Mutex
	var observed []plugin.CarStatusChangedEvent
	bus.Subscribe(string(plugin.CarStatusChanged), func(payload any) {
		evt, ok := payload.(plugin.CarStatusChangedEvent)
		if !ok {
			return
		}
		mu.Lock()
		observed = append(observed, evt)
		mu.Unlock()
	})

	adapter := forceCompleteAdapter(gormDB, bus)
	if err := adapter(context.Background(), c.ID, "should roll back"); err == nil {
		t.Fatal("expected error from forced audit-row failure, got nil")
	}

	// The status update must have rolled back with the audit failure.
	var got models.Car
	if err := gormDB.Where("id = ?", c.ID).First(&got).Error; err != nil {
		t.Fatalf("reload car: %v", err)
	}
	if got.Status != "in_progress" {
		t.Errorf("status = %q, want %q (outer tx must have rolled back)", got.Status, "in_progress")
	}

	// No progress note should have landed.
	var notes int64
	if err := gormDB.Model(&models.CarProgress{}).Where("car_id = ?", c.ID).Count(&notes).Error; err != nil {
		t.Fatalf("count notes: %v", err)
	}
	if notes != 0 {
		t.Errorf("audit row count = %d, want 0 (rollback failed)", notes)
	}

	// Give the bus drain goroutines a window to deliver any in-flight
	// event before we assert. The events package uses one goroutine
	// per subscriber draining a buffered channel; a small wait is
	// sufficient because the publish (if it happened) would race
	// adapter return.
	time.Sleep(50 * time.Millisecond)

	mu.Lock()
	count := len(observed)
	mu.Unlock()
	if count != 0 {
		t.Errorf("subscriber observed %d phantom CarStatusChanged event(s) on rollback; want 0", count)
	}
}
