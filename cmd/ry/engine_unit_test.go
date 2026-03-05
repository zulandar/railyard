package main

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

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

func engineTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	gormDB, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{
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

func closedEngineDB(t *testing.T) *gorm.DB {
	t.Helper()
	gormDB, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	sqlDB, _ := gormDB.DB()
	sqlDB.Close()
	return gormDB
}

// ---------------------------------------------------------------------------
// sleepWithContext
// ---------------------------------------------------------------------------

func TestSleepWithContext_CancelledEarly(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())

	go func() {
		time.Sleep(10 * time.Millisecond)
		cancel()
	}()

	start := time.Now()
	sleepWithContext(ctx, 5*time.Second)
	elapsed := time.Since(start)

	if elapsed >= 1*time.Second {
		t.Fatalf("expected early return on cancel, took %s", elapsed)
	}
}

func TestSleepWithContext_NormalSleep(t *testing.T) {
	ctx := context.Background()

	start := time.Now()
	sleepWithContext(ctx, 10*time.Millisecond)
	elapsed := time.Since(start)

	if elapsed < 10*time.Millisecond {
		t.Fatalf("expected at least 10ms sleep, took %s", elapsed)
	}
	if elapsed >= 1*time.Second {
		t.Fatalf("sleep took too long: %s", elapsed)
	}
}

// ---------------------------------------------------------------------------
// loadProgress
// ---------------------------------------------------------------------------

func TestLoadProgress_EmptyForNonexistentCar(t *testing.T) {
	gormDB := engineTestDB(t)

	progress, err := loadProgress(gormDB, "car-does-not-exist")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(progress) != 0 {
		t.Fatalf("expected empty slice, got %d items", len(progress))
	}
}

func TestLoadProgress_ReturnsInOrder(t *testing.T) {
	gormDB := engineTestDB(t)

	now := time.Now().UTC()
	notes := []models.CarProgress{
		{CarID: "car-1", Note: "first", CreatedAt: now.Add(-2 * time.Minute), Cycle: 1, EngineID: "eng-1"},
		{CarID: "car-1", Note: "third", CreatedAt: now, Cycle: 3, EngineID: "eng-1"},
		{CarID: "car-1", Note: "second", CreatedAt: now.Add(-1 * time.Minute), Cycle: 2, EngineID: "eng-1"},
	}
	for _, n := range notes {
		if err := gormDB.Create(&n).Error; err != nil {
			t.Fatalf("insert progress: %v", err)
		}
	}

	progress, err := loadProgress(gormDB, "car-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(progress) != 3 {
		t.Fatalf("expected 3 rows, got %d", len(progress))
	}
	if progress[0].Note != "first" || progress[1].Note != "second" || progress[2].Note != "third" {
		t.Fatalf("wrong order: %v, %v, %v", progress[0].Note, progress[1].Note, progress[2].Note)
	}
}

func TestLoadProgress_ErrorOnClosedDB(t *testing.T) {
	gormDB := closedEngineDB(t)

	_, err := loadProgress(gormDB, "car-1")
	if err == nil {
		t.Fatal("expected error for closed db, got nil")
	}
}

// ---------------------------------------------------------------------------
// loadMessages
// ---------------------------------------------------------------------------

func TestLoadMessages_EmptyForNonexistentEngine(t *testing.T) {
	gormDB := engineTestDB(t)

	msgs, err := loadMessages(gormDB, "engine-does-not-exist")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(msgs) != 0 {
		t.Fatalf("expected empty slice, got %d items", len(msgs))
	}
}

func TestLoadMessages_ReturnsUnacknowledged(t *testing.T) {
	gormDB := engineTestDB(t)

	messages := []models.Message{
		{FromAgent: "yard", ToAgent: "eng-1", Body: "unacked-1", Acknowledged: false},
		{FromAgent: "yard", ToAgent: "eng-1", Body: "acked", Acknowledged: true},
		{FromAgent: "yard", ToAgent: "eng-1", Body: "unacked-2", Acknowledged: false},
		{FromAgent: "yard", ToAgent: "eng-2", Body: "other-engine", Acknowledged: false},
	}
	for _, m := range messages {
		if err := gormDB.Create(&m).Error; err != nil {
			t.Fatalf("insert message: %v", err)
		}
	}

	msgs, err := loadMessages(gormDB, "eng-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(msgs) != 2 {
		t.Fatalf("expected 2 unacknowledged messages, got %d", len(msgs))
	}
	for _, m := range msgs {
		if m.Acknowledged {
			t.Fatalf("returned acknowledged message: %+v", m)
		}
		if m.ToAgent != "eng-1" {
			t.Fatalf("returned message for wrong engine: %+v", m)
		}
	}
}

func TestLoadMessages_ErrorForEmptyEngineID(t *testing.T) {
	gormDB := engineTestDB(t)

	_, err := loadMessages(gormDB, "")
	if err == nil {
		t.Fatal("expected error for empty engineID, got nil")
	}
}
