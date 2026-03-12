package car

import (
	"testing"
	"time"

	"github.com/zulandar/railyard/internal/models"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func testCycleDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	if err := db.AutoMigrate(&models.CarProgress{}); err != nil {
		t.Fatalf("auto-migrate: %v", err)
	}
	return db
}

func TestGetCycleMetrics_NoCar(t *testing.T) {
	db := testCycleDB(t)

	summary, details, err := GetCycleMetrics(db, "car-nonexistent")
	if err != nil {
		t.Fatalf("GetCycleMetrics: %v", err)
	}
	if summary.TotalCycles != 0 {
		t.Errorf("expected 0 cycles, got %d", summary.TotalCycles)
	}
	if len(details) != 0 {
		t.Errorf("expected no details, got %d", len(details))
	}
	if summary.Stalled {
		t.Error("expected not stalled")
	}
}

func TestGetCycleMetrics_WithProgress(t *testing.T) {
	db := testCycleDB(t)

	now := time.Now()
	rows := []models.CarProgress{
		{CarID: "car-1", Cycle: 1, EngineID: "eng-1", Note: "First cycle", FilesChanged: `["a.go","b.go"]`, CommitHash: "abc1234", CreatedAt: now.Add(-4 * time.Minute)},
		{CarID: "car-1", Cycle: 2, EngineID: "eng-1", Note: "Second cycle", FilesChanged: `["c.go"]`, CommitHash: "def5678", CreatedAt: now.Add(-2 * time.Minute)},
		{CarID: "car-1", Cycle: 3, EngineID: "eng-2", Note: "Third cycle", FilesChanged: `["d.go","e.go","f.go"]`, CommitHash: "ghi9012", CreatedAt: now},
	}
	for _, r := range rows {
		if err := db.Create(&r).Error; err != nil {
			t.Fatalf("create progress: %v", err)
		}
	}

	summary, details, err := GetCycleMetrics(db, "car-1")
	if err != nil {
		t.Fatalf("GetCycleMetrics: %v", err)
	}
	if summary.TotalCycles != 3 {
		t.Errorf("TotalCycles = %d, want 3", summary.TotalCycles)
	}
	if summary.TotalFilesChanged != 6 {
		t.Errorf("TotalFilesChanged = %d, want 6", summary.TotalFilesChanged)
	}
	if summary.Stalled {
		t.Error("expected not stalled with 3 cycles")
	}

	// AvgDurationSec should be ~120s (2 gaps of 120s each, avg = 120).
	if summary.AvgDurationSec < 119 || summary.AvgDurationSec > 121 {
		t.Errorf("AvgDurationSec = %f, want ~120", summary.AvgDurationSec)
	}

	// Should have 2 unique engines.
	if len(summary.Engines) != 2 {
		t.Errorf("Engines count = %d, want 2", len(summary.Engines))
	}

	if len(details) != 3 {
		t.Fatalf("details count = %d, want 3", len(details))
	}
	// First cycle should have 0 duration.
	if details[0].DurationSec != 0 {
		t.Errorf("first cycle DurationSec = %f, want 0", details[0].DurationSec)
	}
	if details[0].FilesChanged != 2 {
		t.Errorf("first cycle FilesChanged = %d, want 2", details[0].FilesChanged)
	}
}

func TestGetCycleMetrics_Stalled(t *testing.T) {
	db := testCycleDB(t)

	now := time.Now()
	for i := 1; i <= 6; i++ {
		if err := db.Create(&models.CarProgress{
			CarID:        "car-stall",
			Cycle:        i,
			EngineID:     "eng-1",
			Note:         "cycle",
			FilesChanged: "[]",
			CreatedAt:    now.Add(time.Duration(i) * time.Minute),
		}).Error; err != nil {
			t.Fatalf("create progress: %v", err)
		}
	}

	summary, _, err := GetCycleMetrics(db, "car-stall")
	if err != nil {
		t.Fatalf("GetCycleMetrics: %v", err)
	}
	if summary.TotalCycles != 6 {
		t.Errorf("TotalCycles = %d, want 6", summary.TotalCycles)
	}
	if !summary.Stalled {
		t.Error("expected stalled with 6 cycles (> 5)")
	}
}

func TestGetCycleMetrics_ExcludesZeroCycle(t *testing.T) {
	db := testCycleDB(t)

	now := time.Now()
	rows := []models.CarProgress{
		{CarID: "car-z", Cycle: 0, EngineID: "eng-1", Note: "progress note", FilesChanged: `["x.go"]`, CreatedAt: now.Add(-6 * time.Minute)},
		{CarID: "car-z", Cycle: 1, EngineID: "eng-1", Note: "First cycle", FilesChanged: `["a.go","b.go"]`, CreatedAt: now.Add(-4 * time.Minute)},
		{CarID: "car-z", Cycle: 2, EngineID: "eng-1", Note: "Second cycle", FilesChanged: `["c.go"]`, CreatedAt: now.Add(-2 * time.Minute)},
		{CarID: "car-z", Cycle: 0, EngineID: "eng-1", Note: "completion note", FilesChanged: `[]`, CreatedAt: now},
	}
	for _, r := range rows {
		if err := db.Create(&r).Error; err != nil {
			t.Fatalf("create progress: %v", err)
		}
	}

	summary, details, err := GetCycleMetrics(db, "car-z")
	if err != nil {
		t.Fatalf("GetCycleMetrics: %v", err)
	}
	if summary.TotalCycles != 2 {
		t.Errorf("TotalCycles = %d, want 2 (Cycle=0 rows excluded)", summary.TotalCycles)
	}
	if len(details) != 2 {
		t.Errorf("details count = %d, want 2", len(details))
	}
	if summary.TotalFilesChanged != 3 {
		t.Errorf("TotalFilesChanged = %d, want 3 (Cycle=0 files excluded)", summary.TotalFilesChanged)
	}
	if summary.Stalled {
		t.Error("expected not stalled with 2 real cycles")
	}
	// Duration should be based only on the 2 real cycle rows.
	if summary.AvgDurationSec < 119 || summary.AvgDurationSec > 121 {
		t.Errorf("AvgDurationSec = %f, want ~120", summary.AvgDurationSec)
	}
}

func TestGetCycleMetrics_StalledExcludesZeroCycle(t *testing.T) {
	db := testCycleDB(t)

	now := time.Now()
	// 5 real cycles + 2 Cycle=0 rows = 7 total rows, but only 5 count => not stalled
	for i := 0; i < 2; i++ {
		db.Create(&models.CarProgress{CarID: "car-s", Cycle: 0, EngineID: "eng-1", CreatedAt: now.Add(time.Duration(i) * time.Minute)})
	}
	for i := 1; i <= 5; i++ {
		db.Create(&models.CarProgress{CarID: "car-s", Cycle: i, EngineID: "eng-1", CreatedAt: now.Add(time.Duration(i+2) * time.Minute)})
	}

	summary, _, err := GetCycleMetrics(db, "car-s")
	if err != nil {
		t.Fatalf("GetCycleMetrics: %v", err)
	}
	if summary.TotalCycles != 5 {
		t.Errorf("TotalCycles = %d, want 5", summary.TotalCycles)
	}
	if summary.Stalled {
		t.Error("expected not stalled with 5 real cycles (threshold is >5)")
	}
}

func TestCarCycleMap_ExcludesZeroCycle(t *testing.T) {
	db := testCycleDB(t)

	now := time.Now()
	db.Create(&models.CarProgress{CarID: "car-m", Cycle: 0, EngineID: "eng-1", CreatedAt: now})
	db.Create(&models.CarProgress{CarID: "car-m", Cycle: 1, EngineID: "eng-1", CreatedAt: now})
	db.Create(&models.CarProgress{CarID: "car-m", Cycle: 2, EngineID: "eng-1", CreatedAt: now})

	result, err := CarCycleMap(db, []string{"car-m"})
	if err != nil {
		t.Fatalf("CarCycleMap: %v", err)
	}
	if m, ok := result["car-m"]; !ok {
		t.Error("car-m missing from result")
	} else if m.TotalCycles != 2 {
		t.Errorf("car-m TotalCycles = %d, want 2 (Cycle=0 excluded)", m.TotalCycles)
	}
}

func TestCarCycleMap_MultiCar(t *testing.T) {
	db := testCycleDB(t)

	now := time.Now()
	// car-a: 2 cycles
	db.Create(&models.CarProgress{CarID: "car-a", Cycle: 1, EngineID: "eng-1", CreatedAt: now})
	db.Create(&models.CarProgress{CarID: "car-a", Cycle: 2, EngineID: "eng-1", CreatedAt: now})
	// car-b: 6 cycles (stalled)
	for i := 1; i <= 6; i++ {
		db.Create(&models.CarProgress{CarID: "car-b", Cycle: i, EngineID: "eng-1", CreatedAt: now})
	}

	result, err := CarCycleMap(db, []string{"car-a", "car-b", "car-nonexistent"})
	if err != nil {
		t.Fatalf("CarCycleMap: %v", err)
	}

	if a, ok := result["car-a"]; !ok {
		t.Error("car-a missing from result")
	} else {
		if a.TotalCycles != 2 {
			t.Errorf("car-a TotalCycles = %d, want 2", a.TotalCycles)
		}
		if a.Stalled {
			t.Error("car-a should not be stalled")
		}
	}

	if b, ok := result["car-b"]; !ok {
		t.Error("car-b missing from result")
	} else {
		if b.TotalCycles != 6 {
			t.Errorf("car-b TotalCycles = %d, want 6", b.TotalCycles)
		}
		if !b.Stalled {
			t.Error("car-b should be stalled")
		}
	}

	if _, ok := result["car-nonexistent"]; ok {
		t.Error("car-nonexistent should not be in result")
	}
}

func TestCarCycleMap_EmptyIDs(t *testing.T) {
	db := testCycleDB(t)

	result, err := CarCycleMap(db, []string{})
	if err != nil {
		t.Fatalf("CarCycleMap: %v", err)
	}
	if len(result) != 0 {
		t.Errorf("expected empty map, got %d entries", len(result))
	}
}
