package main

import (
	"context"
	"testing"
	"time"

	"github.com/zulandar/railyard/internal/db"
	"github.com/zulandar/railyard/internal/engine"
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

// ---------------------------------------------------------------------------
// monitorSessionWithDB
// ---------------------------------------------------------------------------

func TestMonitorSession_ZeroExitCarDone(t *testing.T) {
	gormDB := engineTestDB(t)

	gormDB.Create(&models.Car{
		ID:     "car-ms1",
		Title:  "Done car",
		Status: "done",
		Track:  "backend",
	})

	doneCh := make(chan error, 1)
	doneCh <- nil // zero exit

	stallCh := make(chan engine.StallReason, 1)

	outcome := monitorSessionWithDB(
		context.Background(), doneCh, stallCh, gormDB, "car-ms1",
	)

	if outcome.kind != outcomeCompleted {
		t.Errorf("kind = %d, want outcomeCompleted (%d)", outcome.kind, outcomeCompleted)
	}
}

func TestMonitorSession_ZeroExitCarNotDone(t *testing.T) {
	gormDB := engineTestDB(t)

	gormDB.Create(&models.Car{
		ID:     "car-ms2",
		Title:  "In-progress car",
		Status: "in_progress",
		Track:  "backend",
	})

	doneCh := make(chan error, 1)
	doneCh <- nil // zero exit

	stallCh := make(chan engine.StallReason, 1)

	outcome := monitorSessionWithDB(
		context.Background(), doneCh, stallCh, gormDB, "car-ms2",
	)

	if outcome.kind != outcomeClear {
		t.Errorf("kind = %d, want outcomeClear (%d)", outcome.kind, outcomeClear)
	}
}

func TestMonitorSession_StallButCarDone(t *testing.T) {
	gormDB := engineTestDB(t)

	gormDB.Create(&models.Car{
		ID:     "car-ms3",
		Title:  "Done car with stall race",
		Status: "done",
		Track:  "backend",
	})

	doneCh := make(chan error, 1) // not fired
	stallCh := make(chan engine.StallReason, 1)
	stallCh <- engine.StallReason{Type: "stdout_timeout", Detail: "no output"}

	outcome := monitorSessionWithDB(
		context.Background(), doneCh, stallCh, gormDB, "car-ms3",
	)

	if outcome.kind != outcomeCompleted {
		t.Errorf("kind = %d, want outcomeCompleted (%d) — stall was false alarm", outcome.kind, outcomeCompleted)
	}
}

func TestMonitorSession_StallCarNotDone(t *testing.T) {
	gormDB := engineTestDB(t)

	gormDB.Create(&models.Car{
		ID:     "car-ms4",
		Title:  "In-progress car with stall",
		Status: "in_progress",
		Track:  "backend",
	})

	doneCh := make(chan error, 1)
	stallCh := make(chan engine.StallReason, 1)
	stallCh <- engine.StallReason{Type: "stdout_timeout", Detail: "no output"}

	outcome := monitorSessionWithDB(
		context.Background(), doneCh, stallCh, gormDB, "car-ms4",
	)

	if outcome.kind != outcomeStall {
		t.Errorf("kind = %d, want outcomeStall (%d)", outcome.kind, outcomeStall)
	}
}

// ---------------------------------------------------------------------------
// handleCompletionFailure
// ---------------------------------------------------------------------------

func TestEngine_CompletionPushFailure_NonFatal(t *testing.T) {
	gormDB := engineTestDB(t)

	gormDB.Create(&models.Car{
		ID:     "car-pf1",
		Title:  "Push failure test",
		Status: "done",
		Track:  "backend",
		Branch: "ry/alice/backend/car-pf1",
	})
	gormDB.Create(&models.Engine{
		ID:         "eng-pf1",
		Track:      "backend",
		Status:     "busy",
		CurrentCar: "car-pf1",
	})

	// HandleCompletion re-push will fail (bad repoDir), but this is non-fatal
	// because ry complete already pushed the branch before setting status to done.
	var eng models.Engine
	gormDB.First(&eng, "id = ?", "eng-pf1")
	var car models.Car
	gormDB.First(&car, "id = ?", "car-pf1")

	err := engine.HandleCompletion(gormDB, &car, &eng, engine.CompletionOpts{
		RepoDir:   "/nonexistent/path",
		SessionID: "sess-pf1",
	})
	if err != nil {
		t.Fatalf("HandleCompletion should succeed even with bad push (non-fatal): %v", err)
	}

	// Verify engine went idle despite push failure.
	gormDB.First(&eng, "id = ?", "eng-pf1")
	if eng.Status != engine.StatusIdle {
		t.Errorf("engine.Status = %q, want %q", eng.Status, engine.StatusIdle)
	}
	if eng.CurrentCar != "" {
		t.Errorf("engine.CurrentCar = %q, want empty", eng.CurrentCar)
	}

	// Verify progress note was still written.
	var notes []models.CarProgress
	gormDB.Where("car_id = ?", "car-pf1").Find(&notes)
	if len(notes) == 0 {
		t.Fatal("expected progress note")
	}

	// With the new design, push is handled by ry complete before status=done,
	// so HandleCompletion push failure is non-fatal — no blocking or alerting.
	var msgs []models.Message
	gormDB.Where("subject = ? AND car_id = ?", "completion-failed", "car-pf1").Find(&msgs)
	if len(msgs) != 0 {
		t.Errorf("expected no completion-failed messages (push is non-fatal), got %d", len(msgs))
	}
}
