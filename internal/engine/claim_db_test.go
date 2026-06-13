package engine

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/zulandar/railyard/internal/db"
	"github.com/zulandar/railyard/internal/models"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func claimTestDB(t *testing.T) *gorm.DB {
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

func createClaimTestCar(t *testing.T, gormDB *gorm.DB, id, status, assignee string) {
	t.Helper()
	now := time.Now()
	if err := gormDB.Create(&models.Car{
		ID:        id,
		Title:     "test car " + id,
		Status:    status,
		Track:     "backend",
		Assignee:  assignee,
		CreatedAt: now,
		UpdatedAt: now,
	}).Error; err != nil {
		t.Fatalf("create car: %v", err)
	}
}

func TestMarkInProgress_FromClaimed(t *testing.T) {
	gormDB := claimTestDB(t)
	createClaimTestCar(t, gormDB, "car-mip1", "claimed", "eng-1")

	transitioned, err := MarkInProgress(gormDB, "car-mip1", "eng-1")
	if err != nil {
		t.Fatalf("MarkInProgress: %v", err)
	}
	if !transitioned {
		t.Error("transitioned = false, want true")
	}

	var c models.Car
	if err := gormDB.First(&c, "id = ?", "car-mip1").Error; err != nil {
		t.Fatalf("fetch car: %v", err)
	}
	if c.Status != "in_progress" {
		t.Errorf("status = %q, want %q", c.Status, "in_progress")
	}
}

func TestMarkInProgress_AlreadyInProgress(t *testing.T) {
	gormDB := claimTestDB(t)
	createClaimTestCar(t, gormDB, "car-mip2", "in_progress", "eng-1")

	// Re-claim cycle: the car is already in_progress. Must be a silent no-op.
	transitioned, err := MarkInProgress(gormDB, "car-mip2", "eng-1")
	if err != nil {
		t.Fatalf("MarkInProgress: %v", err)
	}
	if transitioned {
		t.Error("transitioned = true, want false (already in_progress)")
	}

	var c models.Car
	if err := gormDB.First(&c, "id = ?", "car-mip2").Error; err != nil {
		t.Fatalf("fetch car: %v", err)
	}
	if c.Status != "in_progress" {
		t.Errorf("status = %q, want %q", c.Status, "in_progress")
	}
}

func TestMarkInProgress_WrongAssignee(t *testing.T) {
	gormDB := claimTestDB(t)
	createClaimTestCar(t, gormDB, "car-mip3", "claimed", "eng-2")

	// Car was reassigned to a different engine: must not be touched.
	transitioned, err := MarkInProgress(gormDB, "car-mip3", "eng-1")
	if err != nil {
		t.Fatalf("MarkInProgress: %v", err)
	}
	if transitioned {
		t.Error("transitioned = true, want false (assigned to another engine)")
	}

	var c models.Car
	if err := gormDB.First(&c, "id = ?", "car-mip3").Error; err != nil {
		t.Fatalf("fetch car: %v", err)
	}
	if c.Status != "claimed" {
		t.Errorf("status = %q, want %q (untouched)", c.Status, "claimed")
	}
	if c.Assignee != "eng-2" {
		t.Errorf("assignee = %q, want %q (untouched)", c.Assignee, "eng-2")
	}
}

// TestClaimCar_NoReadyCars_CleanError: the idle path (no claimable car) must
// return an error wrapping gorm.ErrRecordNotFound whose message names the
// track and does NOT mention retries — the old message claimed "3 retries" on
// every idle poll, which is misleading noise during triage (railyard-j0j).
func TestClaimCar_NoReadyCars_CleanError(t *testing.T) {
	gormDB := claimTestDB(t)

	_, err := ClaimCar(gormDB, "eng-idle", "backend")
	if err == nil {
		t.Fatal("expected error when no ready cars exist")
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Errorf("idle error must wrap gorm.ErrRecordNotFound, got: %v", err)
	}
	if strings.Contains(err.Error(), "retries") {
		t.Errorf("idle error must not mention retries, got: %v", err)
	}
	if !strings.Contains(err.Error(), "no ready cars") {
		t.Errorf("idle error should say 'no ready cars', got: %v", err)
	}
	if !strings.Contains(err.Error(), "backend") {
		t.Errorf("idle error should name the track, got: %v", err)
	}
}
