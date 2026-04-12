package engine

import (
	"context"
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

func TestHeartbeat_SelfHealFromDead(t *testing.T) {
	gormDB := heartbeatTestDB(t)

	// Create an engine with status "dead" (simulates race during rolling restart).
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

	// Start heartbeat with a short interval.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errCh := StartHeartbeat(ctx, gormDB, "eng-test", 50*time.Millisecond)

	// Poll for the expected state instead of fixed sleep.
	deadline := time.After(2 * time.Second)
	tick := time.NewTicker(25 * time.Millisecond)
	defer tick.Stop()

	for {
		select {
		case err := <-errCh:
			t.Fatalf("heartbeat error: %v", err)
		case <-deadline:
			var eng models.Engine
			gormDB.Where("id = ?", "eng-test").First(&eng)
			t.Fatalf("timed out waiting for self-heal: status=%q", eng.Status)
		case <-tick.C:
			var eng models.Engine
			gormDB.Where("id = ?", "eng-test").First(&eng)
			if eng.Status == StatusIdle && time.Since(eng.LastActivity) < 2*time.Second {
				return // success
			}
		}
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
