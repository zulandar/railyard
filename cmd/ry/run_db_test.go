package main

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/zulandar/railyard/internal/config"
	"github.com/zulandar/railyard/internal/db"
	"github.com/zulandar/railyard/internal/models"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// ---------------------------------------------------------------------------
// Test helpers
// ---------------------------------------------------------------------------

func mockTestDB(t *testing.T) *gorm.DB {
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

func withMockDB(t *testing.T, gormDB *gorm.DB) func() {
	t.Helper()
	orig := connectFromConfig
	connectFromConfig = func(configPath string) (*config.Config, *gorm.DB, error) {
		cfg := &config.Config{
			Owner:  "test-user",
			Tracks: []config.TrackConfig{{Name: "backend", Language: "go", EngineSlots: 3}},
		}
		return cfg, gormDB, nil
	}
	return func() { connectFromConfig = orig }
}

// execCmd builds a root command, captures output, and runs the given args.
func execCmd(t *testing.T, args []string) (string, error) {
	t.Helper()
	cmd := newRootCmd()
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SetArgs(args)
	err := cmd.Execute()
	return buf.String(), err
}

// ---------------------------------------------------------------------------
// 1. runCarList
// ---------------------------------------------------------------------------

func TestRunCarList_EmptyList(t *testing.T) {
	gormDB := mockTestDB(t)
	cleanup := withMockDB(t, gormDB)
	defer cleanup()

	out, err := execCmd(t, []string{"car", "list", "--config", "test.yaml"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "No cars found.") {
		t.Errorf("expected 'No cars found.', got: %s", out)
	}
}

func TestRunCarList_WithCars(t *testing.T) {
	gormDB := mockTestDB(t)
	cleanup := withMockDB(t, gormDB)
	defer cleanup()

	now := time.Now()
	gormDB.Create(&models.Car{ID: "car-001", Title: "First car", Status: "open", Track: "backend", Priority: 2, CreatedAt: now, UpdatedAt: now})
	gormDB.Create(&models.Car{ID: "car-002", Title: "Second car", Status: "done", Track: "backend", Priority: 1, CreatedAt: now, UpdatedAt: now})

	out, err := execCmd(t, []string{"car", "list", "--config", "test.yaml"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	for _, want := range []string{"car-001", "car-002", "First car", "Second car", "ID", "TITLE"} {
		if !strings.Contains(out, want) {
			t.Errorf("expected output to contain %q, got:\n%s", want, out)
		}
	}
}

func TestRunCarList_FilterByTrack(t *testing.T) {
	gormDB := mockTestDB(t)
	cleanup := withMockDB(t, gormDB)
	defer cleanup()

	now := time.Now()
	gormDB.Create(&models.Car{ID: "car-be", Title: "Backend task", Status: "open", Track: "backend", Priority: 2, CreatedAt: now, UpdatedAt: now})
	gormDB.Create(&models.Car{ID: "car-fe", Title: "Frontend task", Status: "open", Track: "frontend", Priority: 2, CreatedAt: now, UpdatedAt: now})

	out, err := execCmd(t, []string{"car", "list", "--track", "backend", "--config", "test.yaml"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "car-be") {
		t.Errorf("expected output to contain 'car-be', got:\n%s", out)
	}
	if strings.Contains(out, "car-fe") {
		t.Errorf("expected output NOT to contain 'car-fe', got:\n%s", out)
	}
}

func TestRunCarList_FilterByStatus(t *testing.T) {
	gormDB := mockTestDB(t)
	cleanup := withMockDB(t, gormDB)
	defer cleanup()

	now := time.Now()
	gormDB.Create(&models.Car{ID: "car-open1", Title: "Open task", Status: "open", Track: "backend", Priority: 2, CreatedAt: now, UpdatedAt: now})
	gormDB.Create(&models.Car{ID: "car-done1", Title: "Done task", Status: "done", Track: "backend", Priority: 1, CreatedAt: now, UpdatedAt: now})

	out, err := execCmd(t, []string{"car", "list", "--status", "open", "--config", "test.yaml"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "car-open1") {
		t.Errorf("expected output to contain 'car-open1', got:\n%s", out)
	}
	if strings.Contains(out, "car-done1") {
		t.Errorf("expected output NOT to contain 'car-done1', got:\n%s", out)
	}
}

// ---------------------------------------------------------------------------
// 2. runCarShow
// ---------------------------------------------------------------------------

func TestRunCarShow_Found(t *testing.T) {
	gormDB := mockTestDB(t)
	cleanup := withMockDB(t, gormDB)
	defer cleanup()

	now := time.Now()
	gormDB.Create(&models.Car{
		ID:          "car-abc",
		Title:       "Test Car",
		Status:      "open",
		Track:       "backend",
		Branch:      "car/car-abc",
		Priority:    2,
		Type:        "task",
		Description: "test desc",
		CreatedAt:   now,
		UpdatedAt:   now,
	})

	out, err := execCmd(t, []string{"car", "show", "car-abc", "--config", "test.yaml"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	for _, want := range []string{"car-abc", "Test Car", "open", "backend", "task", "test desc"} {
		if !strings.Contains(out, want) {
			t.Errorf("expected output to contain %q, got:\n%s", want, out)
		}
	}
}

func TestRunCarShow_NotFound(t *testing.T) {
	gormDB := mockTestDB(t)
	cleanup := withMockDB(t, gormDB)
	defer cleanup()

	_, err := execCmd(t, []string{"car", "show", "nonexistent", "--config", "test.yaml"})
	if err == nil {
		t.Fatal("expected error for nonexistent car")
	}
}

// ---------------------------------------------------------------------------
// 3. runCarUpdate
// ---------------------------------------------------------------------------

func TestRunCarUpdate_Success(t *testing.T) {
	gormDB := mockTestDB(t)
	cleanup := withMockDB(t, gormDB)
	defer cleanup()

	now := time.Now()
	gormDB.Create(&models.Car{ID: "car-upd", Title: "Updatable", Status: "open", Track: "backend", CreatedAt: now, UpdatedAt: now})

	out, err := execCmd(t, []string{"car", "update", "car-upd", "--status", "ready", "--config", "test.yaml"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "Updated car car-upd") {
		t.Errorf("expected 'Updated car car-upd', got:\n%s", out)
	}

	// Verify status changed in DB.
	var c models.Car
	if err := gormDB.First(&c, "id = ?", "car-upd").Error; err != nil {
		t.Fatalf("fetch car: %v", err)
	}
	if c.Status != "ready" {
		t.Errorf("status = %q, want %q", c.Status, "ready")
	}
}

func TestRunCarUpdate_NotFound(t *testing.T) {
	gormDB := mockTestDB(t)
	cleanup := withMockDB(t, gormDB)
	defer cleanup()

	_, err := execCmd(t, []string{"car", "update", "nonexistent", "--status", "done", "--config", "test.yaml"})
	if err == nil {
		t.Fatal("expected error for nonexistent car")
	}
}

// ---------------------------------------------------------------------------
// 4. runCarChildren
// ---------------------------------------------------------------------------

func TestRunCarChildren_WithChildren(t *testing.T) {
	gormDB := mockTestDB(t)
	cleanup := withMockDB(t, gormDB)
	defer cleanup()

	now := time.Now()
	gormDB.Create(&models.Car{ID: "car-parent", Title: "Parent Epic", Type: "epic", Status: "open", Track: "backend", CreatedAt: now, UpdatedAt: now})

	parentID := "car-parent"
	gormDB.Create(&models.Car{ID: "car-c1", Title: "Child 1", Status: "open", Track: "backend", ParentID: &parentID, Priority: 1, CreatedAt: now, UpdatedAt: now})
	gormDB.Create(&models.Car{ID: "car-c2", Title: "Child 2", Status: "done", Track: "backend", ParentID: &parentID, Priority: 2, CreatedAt: now, UpdatedAt: now})

	out, err := execCmd(t, []string{"car", "children", "car-parent", "--config", "test.yaml"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, want := range []string{"car-c1", "car-c2", "Summary"} {
		if !strings.Contains(out, want) {
			t.Errorf("expected output to contain %q, got:\n%s", want, out)
		}
	}
}

func TestRunCarChildren_NoChildren(t *testing.T) {
	gormDB := mockTestDB(t)
	cleanup := withMockDB(t, gormDB)
	defer cleanup()

	now := time.Now()
	gormDB.Create(&models.Car{ID: "car-lonely", Title: "Lonely Epic", Type: "epic", Status: "open", Track: "backend", CreatedAt: now, UpdatedAt: now})

	out, err := execCmd(t, []string{"car", "children", "car-lonely", "--config", "test.yaml"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "No children for") {
		t.Errorf("expected 'No children for', got:\n%s", out)
	}
}

// ---------------------------------------------------------------------------
// 5. runComplete
// ---------------------------------------------------------------------------

func TestRunComplete_Success(t *testing.T) {
	gormDB := mockTestDB(t)
	cleanup := withMockDB(t, gormDB)
	defer cleanup()

	now := time.Now()
	gormDB.Create(&models.Car{ID: "car-done", Title: "Completable", Status: "in_progress", Track: "backend", CreatedAt: now, UpdatedAt: now})

	out, err := execCmd(t, []string{"complete", "car-done", "finished", "work", "--config", "test.yaml"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "car-done") {
		t.Errorf("expected output to contain 'car-done', got:\n%s", out)
	}
	if !strings.Contains(out, "marked done") {
		t.Errorf("expected output to contain 'marked done', got:\n%s", out)
	}

	// Verify car status is "done" in DB.
	var c models.Car
	if err := gormDB.First(&c, "id = ?", "car-done").Error; err != nil {
		t.Fatalf("fetch car: %v", err)
	}
	if c.Status != "done" {
		t.Errorf("status = %q, want %q", c.Status, "done")
	}
}

func TestRunComplete_NotFound(t *testing.T) {
	gormDB := mockTestDB(t)
	cleanup := withMockDB(t, gormDB)
	defer cleanup()

	_, err := execCmd(t, []string{"complete", "nonexistent", "done", "--config", "test.yaml"})
	if err == nil {
		t.Fatal("expected error for nonexistent car")
	}
}

// ---------------------------------------------------------------------------
// 6. runProgress
// ---------------------------------------------------------------------------

func TestRunProgress_Success(t *testing.T) {
	gormDB := mockTestDB(t)
	cleanup := withMockDB(t, gormDB)
	defer cleanup()

	now := time.Now()
	gormDB.Create(&models.Car{ID: "car-prog", Title: "In Progress", Status: "in_progress", Track: "backend", CreatedAt: now, UpdatedAt: now})

	out, err := execCmd(t, []string{"progress", "car-prog", "making", "headway", "--config", "test.yaml"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "Progress note written") {
		t.Errorf("expected 'Progress note written', got:\n%s", out)
	}

	// Verify progress note exists in DB.
	var notes []models.CarProgress
	if err := gormDB.Where("car_id = ?", "car-prog").Find(&notes).Error; err != nil {
		t.Fatalf("fetch progress notes: %v", err)
	}
	if len(notes) == 0 {
		t.Error("expected at least one progress note")
	}
	if !strings.Contains(notes[0].Note, "making") {
		t.Errorf("note = %q, expected to contain 'making'", notes[0].Note)
	}
}

func TestRunProgress_NotFound(t *testing.T) {
	gormDB := mockTestDB(t)
	cleanup := withMockDB(t, gormDB)
	defer cleanup()

	_, err := execCmd(t, []string{"progress", "nonexistent", "note", "--config", "test.yaml"})
	if err == nil {
		t.Fatal("expected error for nonexistent car")
	}
}

// ---------------------------------------------------------------------------
// 7. Car dep commands
// ---------------------------------------------------------------------------

func TestRunCarDepAdd_Success(t *testing.T) {
	gormDB := mockTestDB(t)
	cleanup := withMockDB(t, gormDB)
	defer cleanup()

	now := time.Now()
	gormDB.Create(&models.Car{ID: "car-a", Title: "Car A", Status: "open", Track: "backend", CreatedAt: now, UpdatedAt: now})
	gormDB.Create(&models.Car{ID: "car-b", Title: "Car B", Status: "open", Track: "backend", CreatedAt: now, UpdatedAt: now})

	out, err := execCmd(t, []string{"car", "dep", "add", "car-a", "--blocked-by", "car-b", "--config", "test.yaml"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "Added dependency") {
		t.Errorf("expected 'Added dependency', got:\n%s", out)
	}
}

func TestRunCarDepList_WithDeps(t *testing.T) {
	gormDB := mockTestDB(t)
	cleanup := withMockDB(t, gormDB)
	defer cleanup()

	now := time.Now()
	gormDB.Create(&models.Car{ID: "car-a", Title: "Car A", Status: "open", Track: "backend", CreatedAt: now, UpdatedAt: now})
	gormDB.Create(&models.Car{ID: "car-b", Title: "Car B", Status: "open", Track: "backend", CreatedAt: now, UpdatedAt: now})
	gormDB.Create(&models.CarDep{CarID: "car-a", BlockedBy: "car-b", DepType: "blocks"})

	out, err := execCmd(t, []string{"car", "dep", "list", "car-a", "--config", "test.yaml"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "Blocked by:") {
		t.Errorf("expected 'Blocked by:', got:\n%s", out)
	}
	if !strings.Contains(out, "car-b") {
		t.Errorf("expected output to contain 'car-b', got:\n%s", out)
	}
}

func TestRunCarDepList_NoDeps(t *testing.T) {
	gormDB := mockTestDB(t)
	cleanup := withMockDB(t, gormDB)
	defer cleanup()

	now := time.Now()
	gormDB.Create(&models.Car{ID: "car-a", Title: "Car A", Status: "open", Track: "backend", CreatedAt: now, UpdatedAt: now})

	out, err := execCmd(t, []string{"car", "dep", "list", "car-a", "--config", "test.yaml"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "No dependencies for") {
		t.Errorf("expected 'No dependencies for', got:\n%s", out)
	}
}

func TestRunCarDepRemove_Success(t *testing.T) {
	gormDB := mockTestDB(t)
	cleanup := withMockDB(t, gormDB)
	defer cleanup()

	now := time.Now()
	gormDB.Create(&models.Car{ID: "car-a", Title: "Car A", Status: "open", Track: "backend", CreatedAt: now, UpdatedAt: now})
	gormDB.Create(&models.Car{ID: "car-b", Title: "Car B", Status: "open", Track: "backend", CreatedAt: now, UpdatedAt: now})
	gormDB.Create(&models.CarDep{CarID: "car-a", BlockedBy: "car-b", DepType: "blocks"})

	out, err := execCmd(t, []string{"car", "dep", "remove", "car-a", "--blocked-by", "car-b", "--config", "test.yaml"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "Removed dependency") {
		t.Errorf("expected 'Removed dependency', got:\n%s", out)
	}
}

// ---------------------------------------------------------------------------
// 8. Car ready command
// ---------------------------------------------------------------------------

func TestRunCarReady_WithReadyCars(t *testing.T) {
	gormDB := mockTestDB(t)
	cleanup := withMockDB(t, gormDB)
	defer cleanup()

	now := time.Now()
	// Ready: open, unassigned, no blocking deps.
	gormDB.Create(&models.Car{ID: "car-ready", Title: "Ready Car", Status: "open", Track: "backend", Assignee: "", Priority: 2, CreatedAt: now, UpdatedAt: now})
	// Not ready: open but assigned.
	gormDB.Create(&models.Car{ID: "car-assigned", Title: "Assigned Car", Status: "open", Track: "backend", Assignee: "engine-1", Priority: 2, CreatedAt: now, UpdatedAt: now})
	// Not ready: done.
	gormDB.Create(&models.Car{ID: "car-finished", Title: "Done Car", Status: "done", Track: "backend", Assignee: "", Priority: 2, CreatedAt: now, UpdatedAt: now})

	out, err := execCmd(t, []string{"car", "ready", "--config", "test.yaml"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "car-ready") {
		t.Errorf("expected output to contain 'car-ready', got:\n%s", out)
	}
	if strings.Contains(out, "car-assigned") {
		t.Errorf("expected output NOT to contain 'car-assigned', got:\n%s", out)
	}
	if strings.Contains(out, "car-finished") {
		t.Errorf("expected output NOT to contain 'car-finished', got:\n%s", out)
	}
}

func TestRunCarReady_NoCars(t *testing.T) {
	gormDB := mockTestDB(t)
	cleanup := withMockDB(t, gormDB)
	defer cleanup()

	out, err := execCmd(t, []string{"car", "ready", "--config", "test.yaml"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "No ready cars.") {
		t.Errorf("expected 'No ready cars.', got:\n%s", out)
	}
}

// ---------------------------------------------------------------------------
// 9. Car publish command
// ---------------------------------------------------------------------------

func TestRunCarPublish_Success(t *testing.T) {
	gormDB := mockTestDB(t)
	cleanup := withMockDB(t, gormDB)
	defer cleanup()

	now := time.Now()
	gormDB.Create(&models.Car{ID: "car-pub", Title: "Draft Car", Status: "draft", Track: "backend", CreatedAt: now, UpdatedAt: now})

	out, err := execCmd(t, []string{"car", "publish", "car-pub", "--config", "test.yaml"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "Published") {
		t.Errorf("expected 'Published', got:\n%s", out)
	}
}

func TestRunCarPublish_NoDrafts(t *testing.T) {
	gormDB := mockTestDB(t)
	cleanup := withMockDB(t, gormDB)
	defer cleanup()

	now := time.Now()
	gormDB.Create(&models.Car{ID: "car-open", Title: "Open Car", Status: "open", Track: "backend", CreatedAt: now, UpdatedAt: now})

	out, err := execCmd(t, []string{"car", "publish", "car-open", "--config", "test.yaml"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "No draft cars") {
		t.Errorf("expected 'No draft cars', got:\n%s", out)
	}
}
