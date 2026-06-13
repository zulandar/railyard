package bull

import (
	"context"
	"testing"

	"github.com/zulandar/railyard/internal/models"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func storeTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	if err := db.AutoMigrate(
		&models.Car{},
		&models.CarDep{},
		&models.CarProgress{},
		&models.BullIssue{},
	); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return db
}

// TestCreateCarAndRecord_Atomic: the car and the bull_issues tracking row are
// created in one transaction. If recording the issue fails, the car must be
// rolled back so the next poll cannot create a second car for the same GitHub
// issue (railyard-p9t).
func TestCreateCarAndRecord_RollsBackCarOnRecordFailure(t *testing.T) {
	db := storeTestDB(t)
	store := NewStore(db, "ry/test")

	// A prior tracking row already exists for issue #10. The unique index on
	// bull_issues.issue_number makes the record step fail, which must roll the
	// car back.
	if err := db.Create(&models.BullIssue{IssueNumber: 10, CarID: "car-old", LastKnownStatus: "open"}).Error; err != nil {
		t.Fatalf("seed bull issue: %v", err)
	}

	_, err := store.CreateCarAndRecord(context.Background(),
		CarCreateOpts{Title: "dup", Track: "backend", Type: "bug", SourceIssue: 10},
		models.BullIssue{IssueNumber: 10, LastKnownStatus: "draft"},
	)
	if err == nil {
		t.Fatal("expected error from duplicate issue number")
	}

	var carCount int64
	db.Model(&models.Car{}).Count(&carCount)
	if carCount != 0 {
		t.Errorf("car count = %d, want 0 (car must roll back when record fails)", carCount)
	}
}

// TestCreateCarAndRecord_HappyPath: on success the car exists, the tracking
// row exists, and the row points at the new car.
func TestCreateCarAndRecord_HappyPath(t *testing.T) {
	db := storeTestDB(t)
	store := NewStore(db, "ry/test")

	carID, err := store.CreateCarAndRecord(context.Background(),
		CarCreateOpts{Title: "real", Track: "backend", Type: "bug", SourceIssue: 11, BranchPrefix: "ry/test"},
		models.BullIssue{IssueNumber: 11, LastKnownStatus: "draft"},
	)
	if err != nil {
		t.Fatalf("CreateCarAndRecord: %v", err)
	}
	if carID == "" {
		t.Fatal("expected non-empty car ID")
	}

	var car models.Car
	if err := db.First(&car, "id = ?", carID).Error; err != nil {
		t.Fatalf("car not found: %v", err)
	}
	if car.SourceIssue != 11 {
		t.Errorf("car.SourceIssue = %d, want 11", car.SourceIssue)
	}

	var issue models.BullIssue
	if err := db.First(&issue, "issue_number = ?", 11).Error; err != nil {
		t.Fatalf("bull issue not found: %v", err)
	}
	if issue.CarID != carID {
		t.Errorf("bull issue CarID = %q, want %q", issue.CarID, carID)
	}
}
