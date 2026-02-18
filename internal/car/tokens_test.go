package car

import (
	"testing"
	"time"

	"github.com/zulandar/railyard/internal/models"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func testTokenDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	if err := db.AutoMigrate(&models.AgentLog{}); err != nil {
		t.Fatalf("auto-migrate: %v", err)
	}
	return db
}

func TestGetTokenUsage_NoCar(t *testing.T) {
	db := testTokenDB(t)

	summary, err := GetTokenUsage(db, "car-nonexistent")
	if err != nil {
		t.Fatalf("GetTokenUsage: %v", err)
	}
	if summary.InputTokens != 0 || summary.OutputTokens != 0 || summary.TotalTokens != 0 {
		t.Errorf("expected zero tokens for missing car, got %+v", summary)
	}
	if summary.Model != "" {
		t.Errorf("expected empty model for missing car, got %q", summary.Model)
	}
}

func TestGetTokenUsage_WithLogs(t *testing.T) {
	db := testTokenDB(t)

	now := time.Now()
	logs := []models.AgentLog{
		{EngineID: "eng-1", SessionID: "sess-1", CarID: "car-1", Direction: "out", InputTokens: 1000, OutputTokens: 200, TokenCount: 1200, Model: "claude-sonnet-4-5-20250514", CreatedAt: now.Add(-2 * time.Minute)},
		{EngineID: "eng-1", SessionID: "sess-1", CarID: "car-1", Direction: "out", InputTokens: 500, OutputTokens: 100, TokenCount: 600, Model: "claude-sonnet-4-5-20250514", CreatedAt: now.Add(-1 * time.Minute)},
		{EngineID: "eng-1", SessionID: "sess-1", CarID: "car-1", Direction: "out", InputTokens: 300, OutputTokens: 50, TokenCount: 350, Model: "claude-opus-4-6", CreatedAt: now},
	}
	for _, l := range logs {
		if err := db.Create(&l).Error; err != nil {
			t.Fatalf("create log: %v", err)
		}
	}

	summary, err := GetTokenUsage(db, "car-1")
	if err != nil {
		t.Fatalf("GetTokenUsage: %v", err)
	}
	if summary.InputTokens != 1800 {
		t.Errorf("InputTokens = %d, want 1800", summary.InputTokens)
	}
	if summary.OutputTokens != 350 {
		t.Errorf("OutputTokens = %d, want 350", summary.OutputTokens)
	}
	if summary.TotalTokens != 2150 {
		t.Errorf("TotalTokens = %d, want 2150", summary.TotalTokens)
	}
	if summary.Model != "claude-opus-4-6" {
		t.Errorf("Model = %q, want %q", summary.Model, "claude-opus-4-6")
	}
}

func TestGetTokenUsage_IgnoresStderr(t *testing.T) {
	db := testTokenDB(t)

	now := time.Now()
	logs := []models.AgentLog{
		{EngineID: "eng-1", SessionID: "sess-1", CarID: "car-2", Direction: "out", InputTokens: 100, OutputTokens: 50, TokenCount: 150, CreatedAt: now},
		{EngineID: "eng-1", SessionID: "sess-1", CarID: "car-2", Direction: "err", InputTokens: 999, OutputTokens: 999, TokenCount: 1998, CreatedAt: now},
	}
	for _, l := range logs {
		if err := db.Create(&l).Error; err != nil {
			t.Fatalf("create log: %v", err)
		}
	}

	summary, err := GetTokenUsage(db, "car-2")
	if err != nil {
		t.Fatalf("GetTokenUsage: %v", err)
	}
	if summary.InputTokens != 100 {
		t.Errorf("InputTokens = %d, want 100 (stderr should be excluded)", summary.InputTokens)
	}
	if summary.OutputTokens != 50 {
		t.Errorf("OutputTokens = %d, want 50 (stderr should be excluded)", summary.OutputTokens)
	}
}

func TestCarTokenMap_MultiCar(t *testing.T) {
	db := testTokenDB(t)

	now := time.Now()
	logs := []models.AgentLog{
		{EngineID: "eng-1", SessionID: "sess-1", CarID: "car-a", Direction: "out", InputTokens: 100, OutputTokens: 50, TokenCount: 150, CreatedAt: now},
		{EngineID: "eng-1", SessionID: "sess-1", CarID: "car-a", Direction: "out", InputTokens: 200, OutputTokens: 75, TokenCount: 275, CreatedAt: now},
		{EngineID: "eng-1", SessionID: "sess-1", CarID: "car-b", Direction: "out", InputTokens: 500, OutputTokens: 100, TokenCount: 600, CreatedAt: now},
	}
	for _, l := range logs {
		if err := db.Create(&l).Error; err != nil {
			t.Fatalf("create log: %v", err)
		}
	}

	result, err := CarTokenMap(db, []string{"car-a", "car-b", "car-nonexistent"})
	if err != nil {
		t.Fatalf("CarTokenMap: %v", err)
	}

	if a, ok := result["car-a"]; !ok {
		t.Error("car-a missing from result")
	} else {
		if a.InputTokens != 300 {
			t.Errorf("car-a InputTokens = %d, want 300", a.InputTokens)
		}
		if a.OutputTokens != 125 {
			t.Errorf("car-a OutputTokens = %d, want 125", a.OutputTokens)
		}
		if a.TotalTokens != 425 {
			t.Errorf("car-a TotalTokens = %d, want 425", a.TotalTokens)
		}
	}

	if b, ok := result["car-b"]; !ok {
		t.Error("car-b missing from result")
	} else {
		if b.TotalTokens != 600 {
			t.Errorf("car-b TotalTokens = %d, want 600", b.TotalTokens)
		}
	}

	if _, ok := result["car-nonexistent"]; ok {
		t.Error("car-nonexistent should not be in result")
	}
}

func TestCarTokenMap_EmptyIDs(t *testing.T) {
	db := testTokenDB(t)

	result, err := CarTokenMap(db, []string{})
	if err != nil {
		t.Fatalf("CarTokenMap: %v", err)
	}
	if len(result) != 0 {
		t.Errorf("expected empty map, got %d entries", len(result))
	}
}
