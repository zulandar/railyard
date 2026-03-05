package db

import (
	"strings"
	"testing"

	"github.com/zulandar/railyard/internal/config"
	"github.com/zulandar/railyard/internal/models"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// testDB creates an in-memory SQLite database with all models migrated.
func testDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	if err := AutoMigrate(db); err != nil {
		t.Fatalf("migrate test db: %v", err)
	}
	return db
}

// closedTestDB returns a GORM DB with the underlying sql.DB closed.
func closedTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db := testDB(t)
	sqlDB, _ := db.DB()
	sqlDB.Close()
	return db
}

// --- AutoMigrate tests ---

func TestAutoMigrate_CreatesAllTables(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}

	if err := AutoMigrate(db); err != nil {
		t.Fatalf("AutoMigrate: %v", err)
	}

	// Verify all 12 model tables exist.
	expectedTables := []string{
		"cars",
		"car_deps",
		"car_progresses",
		"tracks",
		"engines",
		"messages",
		"broadcast_acks",
		"agent_logs",
		"railyard_configs",
		"dispatch_sessions",
		"telegraph_conversations",
	}

	for _, table := range expectedTables {
		if !db.Migrator().HasTable(table) {
			t.Errorf("expected table %q to exist after AutoMigrate", table)
		}
	}
}

func TestAutoMigrate_Idempotent(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}

	// Run twice — should not error.
	if err := AutoMigrate(db); err != nil {
		t.Fatalf("AutoMigrate (1st): %v", err)
	}
	if err := AutoMigrate(db); err != nil {
		t.Fatalf("AutoMigrate (2nd): %v", err)
	}
}

func TestAutoMigrate_DBError(t *testing.T) {
	db := closedTestDB(t)

	err := AutoMigrate(db)
	if err == nil {
		t.Fatal("expected error from AutoMigrate with closed DB")
	}
	if !strings.Contains(err.Error(), "db: auto-migrate") {
		t.Errorf("error = %q, want to contain %q", err.Error(), "db: auto-migrate")
	}
}

// --- SeedTracks tests ---

func TestSeedTracks_SingleTrack(t *testing.T) {
	db := testDB(t)

	tracks := []config.TrackConfig{
		{
			Name:        "backend",
			Language:    "go",
			EngineSlots: 5,
		},
	}

	if err := SeedTracks(db, tracks); err != nil {
		t.Fatalf("SeedTracks: %v", err)
	}

	var result models.Track
	if err := db.Where("name = ?", "backend").First(&result).Error; err != nil {
		t.Fatalf("query track: %v", err)
	}
	if result.Language != "go" {
		t.Errorf("Language = %q, want %q", result.Language, "go")
	}
	if result.EngineSlots != 5 {
		t.Errorf("EngineSlots = %d, want 5", result.EngineSlots)
	}
	if !result.Active {
		t.Error("Active = false, want true")
	}
}

func TestSeedTracks_MultipleTracks(t *testing.T) {
	db := testDB(t)

	tracks := []config.TrackConfig{
		{Name: "backend", Language: "go", EngineSlots: 5},
		{Name: "frontend", Language: "typescript", EngineSlots: 3},
		{Name: "infra", Language: "python", EngineSlots: 2},
	}

	if err := SeedTracks(db, tracks); err != nil {
		t.Fatalf("SeedTracks: %v", err)
	}

	var count int64
	db.Model(&models.Track{}).Count(&count)
	if count != 3 {
		t.Errorf("track count = %d, want 3", count)
	}
}

func TestSeedTracks_WithConventions(t *testing.T) {
	db := testDB(t)

	tracks := []config.TrackConfig{
		{
			Name:        "backend",
			Language:    "go",
			EngineSlots: 3,
			Conventions: map[string]interface{}{"go_version": "1.22", "style": "standard"},
		},
	}

	if err := SeedTracks(db, tracks); err != nil {
		t.Fatalf("SeedTracks: %v", err)
	}

	var result models.Track
	db.Where("name = ?", "backend").First(&result)
	if !strings.Contains(result.Conventions, "go_version") {
		t.Errorf("Conventions = %q, want to contain 'go_version'", result.Conventions)
	}
}

func TestSeedTracks_WithFilePatterns(t *testing.T) {
	db := testDB(t)

	tracks := []config.TrackConfig{
		{
			Name:         "backend",
			Language:     "go",
			EngineSlots:  3,
			FilePatterns: []string{"cmd/**", "internal/**"},
		},
	}

	if err := SeedTracks(db, tracks); err != nil {
		t.Fatalf("SeedTracks: %v", err)
	}

	var result models.Track
	db.Where("name = ?", "backend").First(&result)
	if !strings.Contains(result.FilePatterns, "cmd/**") {
		t.Errorf("FilePatterns = %q, want to contain 'cmd/**'", result.FilePatterns)
	}
	if !strings.Contains(result.FilePatterns, "internal/**") {
		t.Errorf("FilePatterns = %q, want to contain 'internal/**'", result.FilePatterns)
	}
}

func TestSeedTracks_UpsertUpdatesExisting(t *testing.T) {
	db := testDB(t)

	// Seed initial track.
	initial := []config.TrackConfig{
		{Name: "backend", Language: "go", EngineSlots: 3},
	}
	if err := SeedTracks(db, initial); err != nil {
		t.Fatalf("SeedTracks initial: %v", err)
	}

	// Seed again with updated values.
	updated := []config.TrackConfig{
		{Name: "backend", Language: "go", EngineSlots: 7},
	}
	if err := SeedTracks(db, updated); err != nil {
		t.Fatalf("SeedTracks updated: %v", err)
	}

	// Verify only 1 track and it has the new value.
	var count int64
	db.Model(&models.Track{}).Count(&count)
	if count != 1 {
		t.Errorf("track count = %d after upsert, want 1", count)
	}

	var track models.Track
	db.Where("name = ?", "backend").First(&track)
	if track.EngineSlots != 7 {
		t.Errorf("EngineSlots = %d after upsert, want 7", track.EngineSlots)
	}
}

func TestSeedTracks_DBError(t *testing.T) {
	db := closedTestDB(t)

	tracks := []config.TrackConfig{
		{Name: "backend", Language: "go", EngineSlots: 3},
	}
	err := SeedTracks(db, tracks)
	if err == nil {
		t.Fatal("expected error from SeedTracks with closed DB")
	}
	if !strings.Contains(err.Error(), "db: seed track") {
		t.Errorf("error = %q, want to contain %q", err.Error(), "db: seed track")
	}
}

// --- SeedConfig tests ---

func TestSeedConfig_CreatesConfig(t *testing.T) {
	db := testDB(t)

	cfg := &config.Config{
		Owner: "alice",
		Repo:  "git@github.com:org/myapp.git",
	}

	if err := SeedConfig(db, cfg); err != nil {
		t.Fatalf("SeedConfig: %v", err)
	}

	var rc models.RailyardConfig
	if err := db.Where("owner = ?", "alice").First(&rc).Error; err != nil {
		t.Fatalf("query config: %v", err)
	}
	if rc.Owner != "alice" {
		t.Errorf("Owner = %q, want %q", rc.Owner, "alice")
	}
	if rc.RepoURL != "git@github.com:org/myapp.git" {
		t.Errorf("RepoURL = %q, want %q", rc.RepoURL, "git@github.com:org/myapp.git")
	}
	if rc.Mode != "local" {
		t.Errorf("Mode = %q, want %q (default)", rc.Mode, "local")
	}
	if rc.Settings != "{}" {
		t.Errorf("Settings = %q, want %q (default)", rc.Settings, "{}")
	}
}

func TestSeedConfig_UpsertUpdatesExisting(t *testing.T) {
	db := testDB(t)

	cfg1 := &config.Config{
		Owner: "alice",
		Repo:  "git@github.com:org/app-v1.git",
	}
	if err := SeedConfig(db, cfg1); err != nil {
		t.Fatalf("SeedConfig (1st): %v", err)
	}

	cfg2 := &config.Config{
		Owner: "alice",
		Repo:  "git@github.com:org/app-v2.git",
	}
	if err := SeedConfig(db, cfg2); err != nil {
		t.Fatalf("SeedConfig (2nd): %v", err)
	}

	// Verify only 1 config row with updated repo.
	var count int64
	db.Model(&models.RailyardConfig{}).Count(&count)
	if count != 1 {
		t.Errorf("config count = %d after upsert, want 1", count)
	}

	var rc models.RailyardConfig
	db.Where("owner = ?", "alice").First(&rc)
	if rc.RepoURL != "git@github.com:org/app-v2.git" {
		t.Errorf("RepoURL = %q after upsert, want v2 URL", rc.RepoURL)
	}
}

func TestSeedConfig_DBError(t *testing.T) {
	db := closedTestDB(t)

	cfg := &config.Config{
		Owner: "alice",
		Repo:  "git@github.com:org/app.git",
	}
	err := SeedConfig(db, cfg)
	if err == nil {
		t.Fatal("expected error from SeedConfig with closed DB")
	}
	if !strings.Contains(err.Error(), "db: seed config") {
		t.Errorf("error = %q, want to contain %q", err.Error(), "db: seed config")
	}
}

func TestSeedTracks_ConventionsMarshalError(t *testing.T) {
	db := testDB(t)

	// Channels cannot be marshaled to JSON.
	tracks := []config.TrackConfig{
		{
			Name:        "bad",
			Language:    "go",
			EngineSlots: 1,
			Conventions: map[string]interface{}{"bad": make(chan int)},
		},
	}
	err := SeedTracks(db, tracks)
	if err == nil {
		t.Fatal("expected error from SeedTracks with unmarshalable conventions")
	}
	if !strings.Contains(err.Error(), "marshal conventions") {
		t.Errorf("error = %q, want to contain %q", err.Error(), "marshal conventions")
	}
}

// --- DropDatabase / CreateDatabase error path tests ---

func TestDropDatabase_DBError(t *testing.T) {
	db := closedTestDB(t)

	err := DropDatabase(db, "test_db")
	if err == nil {
		t.Fatal("expected error from DropDatabase with closed DB")
	}
	if !strings.Contains(err.Error(), "db: drop database") {
		t.Errorf("error = %q, want to contain %q", err.Error(), "db: drop database")
	}
}

func TestCreateDatabase_DBError(t *testing.T) {
	db := closedTestDB(t)

	err := CreateDatabase(db, "test_db")
	if err == nil {
		t.Fatal("expected error from CreateDatabase with closed DB")
	}
	if !strings.Contains(err.Error(), "db: create database") {
		t.Errorf("error = %q, want to contain %q", err.Error(), "db: create database")
	}
}
