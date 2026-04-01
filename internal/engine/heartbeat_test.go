package engine

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/zulandar/railyard/internal/db"
	"github.com/zulandar/railyard/internal/models"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

var heartbeatTestSeq int

func heartbeatTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	heartbeatTestSeq++
	// Use a unique shared-cache in-memory DB per test so the heartbeat
	// goroutine can see writes from the test goroutine.
	dsn := fmt.Sprintf("file:hbtest%d?mode=memory&cache=shared", heartbeatTestSeq)
	gormDB, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
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

	// Wait for at least one heartbeat tick.
	time.Sleep(200 * time.Millisecond)

	// Check if heartbeat errored.
	select {
	case err := <-errCh:
		t.Fatalf("heartbeat error: %v", err)
	default:
	}

	// Verify the engine status was self-healed to idle.
	var eng models.Engine
	gormDB.Where("id = ?", "eng-test").First(&eng)

	if eng.Status != StatusIdle {
		t.Errorf("status = %q, want %q (heartbeat should self-heal from dead)", eng.Status, StatusIdle)
	}

	// Verify last_activity was updated.
	if time.Since(eng.LastActivity) > 2*time.Second {
		t.Error("last_activity should be recent after heartbeat")
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

	time.Sleep(200 * time.Millisecond)

	var eng models.Engine
	gormDB.Where("id = ?", "eng-work").First(&eng)

	if eng.Status != StatusWorking {
		t.Errorf("status = %q, want %q (heartbeat should not overwrite working status)", eng.Status, StatusWorking)
	}
}
