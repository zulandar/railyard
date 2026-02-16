//go:build integration

package db

import (
	"fmt"
	"net"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/zulandar/railyard/internal/config"
	"github.com/zulandar/railyard/internal/models"
	"gorm.io/gorm"
)

// testDoltServer manages a Dolt SQL server lifecycle for integration tests.
type testDoltServer struct {
	Port int
	Dir  string
	cmd  *exec.Cmd
}

// startDoltServer initializes a Dolt repo in a temp directory and starts
// dolt sql-server on a free port. The server is automatically stopped
// when the test completes.
func startDoltServer(t *testing.T) *testDoltServer {
	t.Helper()

	dir := t.TempDir()

	// Configure dolt identity for the temp repo
	for _, kv := range [][2]string{
		{"user.name", "Test Runner"},
		{"user.email", "test@railyard.dev"},
	} {
		cfg := exec.Command("dolt", "config", "--global", "--add", kv[0], kv[1])
		cfg.Dir = dir
		cfg.CombinedOutput() // ignore errors if already set
	}

	// Initialize dolt repo
	init := exec.Command("dolt", "init")
	init.Dir = dir
	if out, err := init.CombinedOutput(); err != nil {
		t.Fatalf("dolt init: %s\n%s", err, out)
	}

	port := freePort(t)

	cmd := exec.Command("dolt", "sql-server",
		"--port", fmt.Sprintf("%d", port),
		"--host", "127.0.0.1",
	)
	cmd.Dir = dir

	if err := cmd.Start(); err != nil {
		t.Fatalf("dolt sql-server start: %v", err)
	}

	srv := &testDoltServer{Port: port, Dir: dir, cmd: cmd}

	t.Cleanup(func() {
		srv.cmd.Process.Kill()
		srv.cmd.Wait()
	})

	waitForServer(t, port)
	return srv
}

// freePort finds an available TCP port.
func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("find free port: %v", err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	l.Close()
	return port
}

// waitForServer polls until the Dolt server accepts TCP connections.
func waitForServer(t *testing.T, port int) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	addr := fmt.Sprintf("127.0.0.1:%d", port)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", addr, 100*time.Millisecond)
		if err == nil {
			conn.Close()
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("dolt sql-server not ready on port %d after 10s", port)
}

func TestIntegration_ConnectAdmin(t *testing.T) {
	srv := startDoltServer(t)
	db, err := ConnectAdmin("127.0.0.1", srv.Port)
	if err != nil {
		t.Fatalf("ConnectAdmin: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("get sql.DB: %v", err)
	}
	if err := sqlDB.Ping(); err != nil {
		t.Fatalf("ping: %v", err)
	}
}

func TestIntegration_CreateDatabase(t *testing.T) {
	srv := startDoltServer(t)
	adminDB, err := ConnectAdmin("127.0.0.1", srv.Port)
	if err != nil {
		t.Fatalf("ConnectAdmin: %v", err)
	}
	if err := CreateDatabase(adminDB, "railyard_test"); err != nil {
		t.Fatalf("CreateDatabase: %v", err)
	}

	// Verify database exists by connecting to it
	db, err := Connect("127.0.0.1", srv.Port, "railyard_test")
	if err != nil {
		t.Fatalf("Connect to new database: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("get sql.DB: %v", err)
	}
	if err := sqlDB.Ping(); err != nil {
		t.Fatalf("ping new database: %v", err)
	}
}

func TestIntegration_Connect(t *testing.T) {
	srv := startDoltServer(t)
	adminDB, err := ConnectAdmin("127.0.0.1", srv.Port)
	if err != nil {
		t.Fatalf("ConnectAdmin: %v", err)
	}
	if err := CreateDatabase(adminDB, "railyard_connect"); err != nil {
		t.Fatalf("CreateDatabase: %v", err)
	}

	db, err := Connect("127.0.0.1", srv.Port, "railyard_connect")
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("get sql.DB: %v", err)
	}
	if err := sqlDB.Ping(); err != nil {
		t.Fatalf("ping: %v", err)
	}
}

func TestIntegration_AutoMigrate(t *testing.T) {
	srv := startDoltServer(t)
	adminDB, err := ConnectAdmin("127.0.0.1", srv.Port)
	if err != nil {
		t.Fatalf("ConnectAdmin: %v", err)
	}
	if err := CreateDatabase(adminDB, "railyard_migrate"); err != nil {
		t.Fatalf("CreateDatabase: %v", err)
	}
	db, err := Connect("127.0.0.1", srv.Port, "railyard_migrate")
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}

	if err := AutoMigrate(db); err != nil {
		t.Fatalf("AutoMigrate: %v", err)
	}

	// Verify all 9 tables exist
	expectedTables := []string{
		"cars",
		"car_deps",
		"car_progresses",
		"tracks",
		"engines",
		"messages",
		"agent_logs",
		"railyard_configs",
		"reindex_jobs",
	}

	var tables []string
	if err := db.Raw("SHOW TABLES").Scan(&tables).Error; err != nil {
		t.Fatalf("SHOW TABLES: %v", err)
	}

	tableSet := make(map[string]bool)
	for _, tbl := range tables {
		tableSet[tbl] = true
	}

	for _, expected := range expectedTables {
		if !tableSet[expected] {
			t.Errorf("expected table %q not found; got tables: %v", expected, tables)
		}
	}
}

func TestIntegration_AutoMigrate_TableColumns(t *testing.T) {
	srv := startDoltServer(t)
	adminDB, err := ConnectAdmin("127.0.0.1", srv.Port)
	if err != nil {
		t.Fatalf("ConnectAdmin: %v", err)
	}
	if err := CreateDatabase(adminDB, "railyard_cols"); err != nil {
		t.Fatalf("CreateDatabase: %v", err)
	}
	db, err := Connect("127.0.0.1", srv.Port, "railyard_cols")
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	if err := AutoMigrate(db); err != nil {
		t.Fatalf("AutoMigrate: %v", err)
	}

	// Spot-check key columns on cars table
	type columnInfo struct {
		Field string `gorm:"column:Field"`
	}
	var cols []columnInfo
	if err := db.Raw("DESCRIBE cars").Scan(&cols).Error; err != nil {
		t.Fatalf("DESCRIBE cars: %v", err)
	}

	colSet := make(map[string]bool)
	for _, c := range cols {
		colSet[c.Field] = true
	}

	carCols := []string{"id", "title", "description", "type", "status", "priority", "track", "assignee", "parent_id", "branch"}
	for _, col := range carCols {
		if !colSet[col] {
			t.Errorf("cars table missing column %q", col)
		}
	}

	// Spot-check tracks table
	var trackCols []columnInfo
	if err := db.Raw("DESCRIBE tracks").Scan(&trackCols).Error; err != nil {
		t.Fatalf("DESCRIBE tracks: %v", err)
	}
	trackColSet := make(map[string]bool)
	for _, c := range trackCols {
		trackColSet[c.Field] = true
	}
	for _, col := range []string{"name", "language", "conventions", "file_patterns", "engine_slots", "active"} {
		if !trackColSet[col] {
			t.Errorf("tracks table missing column %q", col)
		}
	}
}

func TestIntegration_SeedTracks(t *testing.T) {
	srv := startDoltServer(t)
	adminDB, err := ConnectAdmin("127.0.0.1", srv.Port)
	if err != nil {
		t.Fatalf("ConnectAdmin: %v", err)
	}
	if err := CreateDatabase(adminDB, "railyard_seed"); err != nil {
		t.Fatalf("CreateDatabase: %v", err)
	}
	db, err := Connect("127.0.0.1", srv.Port, "railyard_seed")
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	if err := AutoMigrate(db); err != nil {
		t.Fatalf("AutoMigrate: %v", err)
	}

	tracks := []config.TrackConfig{
		{
			Name:         "backend",
			Language:     "go",
			FilePatterns: []string{"cmd/**", "internal/**"},
			EngineSlots:  5,
			Conventions:  map[string]interface{}{"go_version": "1.22"},
		},
		{
			Name:         "frontend",
			Language:     "typescript",
			FilePatterns: []string{"src/**"},
			EngineSlots:  3,
		},
	}

	if err := SeedTracks(db, tracks); err != nil {
		t.Fatalf("SeedTracks: %v", err)
	}

	// Verify tracks were created
	var result []models.Track
	if err := db.Find(&result).Error; err != nil {
		t.Fatalf("query tracks: %v", err)
	}
	if len(result) != 2 {
		t.Fatalf("len(tracks) = %d, want 2", len(result))
	}

	// Check first track
	be := result[0]
	if be.Name != "backend" {
		t.Errorf("tracks[0].Name = %q, want %q", be.Name, "backend")
	}
	if be.Language != "go" {
		t.Errorf("tracks[0].Language = %q, want %q", be.Language, "go")
	}
	if be.EngineSlots != 5 {
		t.Errorf("tracks[0].EngineSlots = %d, want 5", be.EngineSlots)
	}
	if !be.Active {
		t.Error("tracks[0].Active = false, want true")
	}
}

func TestIntegration_SeedConfig(t *testing.T) {
	srv := startDoltServer(t)
	adminDB, err := ConnectAdmin("127.0.0.1", srv.Port)
	if err != nil {
		t.Fatalf("ConnectAdmin: %v", err)
	}
	if err := CreateDatabase(adminDB, "railyard_seedcfg"); err != nil {
		t.Fatalf("CreateDatabase: %v", err)
	}
	db, err := Connect("127.0.0.1", srv.Port, "railyard_seedcfg")
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	if err := AutoMigrate(db); err != nil {
		t.Fatalf("AutoMigrate: %v", err)
	}

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
		t.Errorf("Mode = %q, want %q", rc.Mode, "local")
	}
}

func TestIntegration_Idempotent(t *testing.T) {
	srv := startDoltServer(t)
	adminDB, err := ConnectAdmin("127.0.0.1", srv.Port)
	if err != nil {
		t.Fatalf("ConnectAdmin: %v", err)
	}

	// CreateDatabase twice
	if err := CreateDatabase(adminDB, "railyard_idem"); err != nil {
		t.Fatalf("CreateDatabase (1st): %v", err)
	}
	if err := CreateDatabase(adminDB, "railyard_idem"); err != nil {
		t.Fatalf("CreateDatabase (2nd): %v", err)
	}

	db, err := Connect("127.0.0.1", srv.Port, "railyard_idem")
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}

	// AutoMigrate twice
	if err := AutoMigrate(db); err != nil {
		t.Fatalf("AutoMigrate (1st): %v", err)
	}
	if err := AutoMigrate(db); err != nil {
		t.Fatalf("AutoMigrate (2nd): %v", err)
	}

	tracks := []config.TrackConfig{
		{Name: "backend", Language: "go", EngineSlots: 5},
	}
	cfg := &config.Config{
		Owner: "alice",
		Repo:  "git@github.com:org/myapp.git",
	}

	// SeedTracks twice
	if err := SeedTracks(db, tracks); err != nil {
		t.Fatalf("SeedTracks (1st): %v", err)
	}
	if err := SeedTracks(db, tracks); err != nil {
		t.Fatalf("SeedTracks (2nd): %v", err)
	}

	// Verify only 1 track exists (upsert, not duplicate)
	var count int64
	db.Model(&models.Track{}).Count(&count)
	if count != 1 {
		t.Errorf("track count = %d after double seed, want 1", count)
	}

	// SeedConfig twice
	if err := SeedConfig(db, cfg); err != nil {
		t.Fatalf("SeedConfig (1st): %v", err)
	}
	if err := SeedConfig(db, cfg); err != nil {
		t.Fatalf("SeedConfig (2nd): %v", err)
	}

	// Verify only 1 config row
	var cfgCount int64
	db.Model(&models.RailyardConfig{}).Count(&cfgCount)
	if cfgCount != 1 {
		t.Errorf("config count = %d after double seed, want 1", cfgCount)
	}
}

// --- Error path tests using a closed connection ---

// closedGormDB starts a Dolt server, opens a connection, then closes the
// underlying sql.DB so all subsequent GORM operations fail.
func closedGormDB(t *testing.T) *gorm.DB {
	t.Helper()
	srv := startDoltServer(t)
	adminDB, err := ConnectAdmin("127.0.0.1", srv.Port)
	if err != nil {
		t.Fatalf("ConnectAdmin: %v", err)
	}
	if err := CreateDatabase(adminDB, "railyard_closed"); err != nil {
		t.Fatalf("CreateDatabase: %v", err)
	}
	db, err := Connect("127.0.0.1", srv.Port, "railyard_closed")
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	sqlDB, _ := db.DB()
	sqlDB.Close()
	return db
}

func TestIntegration_AutoMigrate_Error(t *testing.T) {
	db := closedGormDB(t)
	err := AutoMigrate(db)
	if err == nil {
		t.Fatal("expected error from AutoMigrate with closed DB")
	}
	if !strings.Contains(err.Error(), "db: auto-migrate") {
		t.Errorf("error = %q, want to contain %q", err.Error(), "db: auto-migrate")
	}
}

func TestIntegration_CreateDatabase_Error(t *testing.T) {
	db := closedGormDB(t)
	err := CreateDatabase(db, "should_fail")
	if err == nil {
		t.Fatal("expected error from CreateDatabase with closed DB")
	}
	if !strings.Contains(err.Error(), "db: create database") {
		t.Errorf("error = %q, want to contain %q", err.Error(), "db: create database")
	}
}

func TestIntegration_SeedTracks_Error(t *testing.T) {
	db := closedGormDB(t)
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

func TestIntegration_SeedConfig_Error(t *testing.T) {
	db := closedGormDB(t)
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

func TestIntegration_SeedTracks_UpdateExisting(t *testing.T) {
	srv := startDoltServer(t)
	adminDB, err := ConnectAdmin("127.0.0.1", srv.Port)
	if err != nil {
		t.Fatalf("ConnectAdmin: %v", err)
	}
	if err := CreateDatabase(adminDB, "railyard_upsert"); err != nil {
		t.Fatalf("CreateDatabase: %v", err)
	}
	db, err := Connect("127.0.0.1", srv.Port, "railyard_upsert")
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	if err := AutoMigrate(db); err != nil {
		t.Fatalf("AutoMigrate: %v", err)
	}

	// Seed with initial values
	initial := []config.TrackConfig{
		{Name: "backend", Language: "go", EngineSlots: 3},
	}
	if err := SeedTracks(db, initial); err != nil {
		t.Fatalf("SeedTracks initial: %v", err)
	}

	// Seed with updated values
	updated := []config.TrackConfig{
		{Name: "backend", Language: "go", EngineSlots: 7},
	}
	if err := SeedTracks(db, updated); err != nil {
		t.Fatalf("SeedTracks updated: %v", err)
	}

	// Verify the update took effect
	var track models.Track
	if err := db.Where("name = ?", "backend").First(&track).Error; err != nil {
		t.Fatalf("query track: %v", err)
	}
	if track.EngineSlots != 7 {
		t.Errorf("EngineSlots = %d after upsert, want 7", track.EngineSlots)
	}
}
