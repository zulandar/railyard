package engine

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/zulandar/railyard/internal/db"
	"github.com/zulandar/railyard/internal/models"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func heartbeatTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	gormDB, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	sqlDB, _ := gormDB.DB()
	sqlDB.SetMaxOpenConns(1)
	if err := db.AutoMigrate(gormDB); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return gormDB
}

// TestHeartbeat_MarkedDead_SignalsDrain: an external dead mark (scale-down,
// RestartEngine, yardmaster stale-marking) must surface as ErrMarkedDead on
// the heartbeat channel so the daemon drains, instead of the old self-heal
// silently flipping dead back to idle (railyard-7em / railyard-8m6).
func TestHeartbeat_MarkedDead_SignalsDrain(t *testing.T) {
	gormDB := heartbeatTestDB(t)

	now := time.Now()
	if err := gormDB.Create(&models.Engine{
		ID:           "eng-test",
		Track:        "backend",
		Role:         "engine",
		Status:       StatusDead,
		StartedAt:    now,
		LastActivity: now.Add(-5 * time.Minute),
	}).Error; err != nil {
		t.Fatalf("create engine: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errCh := StartHeartbeat(ctx, gormDB, "eng-test", 50*time.Millisecond)

	select {
	case err := <-errCh:
		if !errors.Is(err, ErrMarkedDead) {
			t.Fatalf("heartbeat error = %v, want ErrMarkedDead", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for ErrMarkedDead signal")
	}

	// The engine must NOT resurrect itself.
	var eng models.Engine
	gormDB.Where("id = ?", "eng-test").First(&eng)
	if eng.Status != StatusDead {
		t.Errorf("status = %q, want %q (no self-heal)", eng.Status, StatusDead)
	}
}

// TestHeartbeat_MarkedDeadWhileWorking_SignalsDrain: the duplicate-work
// scenario — yardmaster marked a slow-but-alive engine dead and reassigned
// its car. The engine must signal drain, never resurrect to idle while
// current_car is set.
func TestHeartbeat_MarkedDeadWhileWorking_SignalsDrain(t *testing.T) {
	gormDB := heartbeatTestDB(t)

	now := time.Now()
	if err := gormDB.Create(&models.Engine{
		ID:           "eng-busy",
		Track:        "backend",
		Role:         "engine",
		Status:       StatusDead,
		CurrentCar:   "car-123",
		StartedAt:    now,
		LastActivity: now,
	}).Error; err != nil {
		t.Fatalf("create engine: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errCh := StartHeartbeat(ctx, gormDB, "eng-busy", 50*time.Millisecond)

	select {
	case err := <-errCh:
		if !errors.Is(err, ErrMarkedDead) {
			t.Fatalf("heartbeat error = %v, want ErrMarkedDead", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for ErrMarkedDead signal")
	}

	var eng models.Engine
	gormDB.Where("id = ?", "eng-busy").First(&eng)
	if eng.Status != StatusDead {
		t.Errorf("status = %q, want %q (no idle-with-current-car state possible)", eng.Status, StatusDead)
	}
}

func TestHeartbeat_DoesNotOverwriteWorking(t *testing.T) {
	gormDB := heartbeatTestDB(t)

	// Create an engine with status "working".
	now := time.Now()
	gormDB.Create(&models.Engine{
		ID:           "eng-work",
		Track:        "backend",
		Role:         "engine",
		Status:       StatusWorking,
		StartedAt:    now,
		LastActivity: now,
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	StartHeartbeat(ctx, gormDB, "eng-work", 50*time.Millisecond)

	// Poll until last_activity is updated (proves heartbeat ran), then check status.
	deadline := time.After(2 * time.Second)
	tick := time.NewTicker(25 * time.Millisecond)
	defer tick.Stop()

	for {
		select {
		case <-deadline:
			t.Fatal("timed out waiting for heartbeat to update last_activity")
		case <-tick.C:
			var eng models.Engine
			gormDB.Where("id = ?", "eng-work").First(&eng)
			if time.Since(eng.LastActivity) < 1*time.Second {
				// Heartbeat ran — verify it didn't overwrite working status.
				if eng.Status != StatusWorking {
					t.Errorf("status = %q, want %q (heartbeat should not overwrite working status)", eng.Status, StatusWorking)
				}
				return
			}
		}
	}
}
