//go:build integration

package yardmaster

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/zulandar/railyard/internal/db"
	"github.com/zulandar/railyard/internal/models"
	"gorm.io/gorm"
)

// --- Test helpers ---

type testDoltServer struct {
	Port int
	Dir  string
	cmd  *exec.Cmd
}

func startDoltServer(t *testing.T) *testDoltServer {
	t.Helper()
	dir := t.TempDir()

	for _, kv := range [][2]string{
		{"user.name", "Test Runner"},
		{"user.email", "test@railyard.dev"},
	} {
		cfg := exec.Command("dolt", "config", "--global", "--add", kv[0], kv[1])
		cfg.Dir = dir
		cfg.CombinedOutput()
	}

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

func setupTestDB(t *testing.T, dbName string) *gorm.DB {
	t.Helper()
	srv := startDoltServer(t)

	adminDB, err := db.ConnectAdmin("127.0.0.1", srv.Port)
	if err != nil {
		t.Fatalf("ConnectAdmin: %v", err)
	}
	if err := db.CreateDatabase(adminDB, dbName); err != nil {
		t.Fatalf("CreateDatabase: %v", err)
	}
	gormDB, err := db.Connect("127.0.0.1", srv.Port, dbName)
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	if err := db.AutoMigrate(gormDB); err != nil {
		t.Fatalf("AutoMigrate: %v", err)
	}
	return gormDB
}

// --- Validation with real DB ---

func TestIntegration_CheckEngineHealth_ZeroThreshold(t *testing.T) {
	gormDB := setupTestDB(t, "railyard_ym_hval1")
	_, err := CheckEngineHealth(gormDB, 0)
	if err == nil {
		t.Fatal("expected error for zero threshold")
	}
}

func TestIntegration_ReassignCar_EmptyCarID(t *testing.T) {
	gormDB := setupTestDB(t, "railyard_ym_hval2")
	err := ReassignCar(gormDB, "", "eng-001", "stalled")
	if err == nil {
		t.Fatal("expected error for empty carID")
	}
}

func TestIntegration_ReassignCar_EmptyEngineID(t *testing.T) {
	gormDB := setupTestDB(t, "railyard_ym_hval3")
	err := ReassignCar(gormDB, "car-001", "", "stalled")
	if err == nil {
		t.Fatal("expected error for empty engineID")
	}
}

// --- CheckEngineHealth integration tests ---

func TestIntegration_CheckEngineHealth_NoStaleEngines(t *testing.T) {
	gormDB := setupTestDB(t, "railyard_ym_health1")

	// Create an engine with recent activity.
	gormDB.Create(&models.Engine{
		ID:           "eng-001",
		Track:        "backend",
		Status:       "idle",
		LastActivity: time.Now(),
		StartedAt:    time.Now(),
	})

	stale, err := CheckEngineHealth(gormDB, 60*time.Second)
	if err != nil {
		t.Fatalf("CheckEngineHealth: %v", err)
	}
	if len(stale) != 0 {
		t.Errorf("expected 0 stale engines, got %d", len(stale))
	}
}

func TestIntegration_CheckEngineHealth_StaleEngine(t *testing.T) {
	gormDB := setupTestDB(t, "railyard_ym_health2")

	// Create an engine with old activity.
	gormDB.Create(&models.Engine{
		ID:           "eng-001",
		Track:        "backend",
		Status:       "working",
		LastActivity: time.Now().Add(-2 * time.Minute),
		StartedAt:    time.Now().Add(-5 * time.Minute),
	})

	stale, err := CheckEngineHealth(gormDB, 60*time.Second)
	if err != nil {
		t.Fatalf("CheckEngineHealth: %v", err)
	}
	if len(stale) != 1 {
		t.Fatalf("expected 1 stale engine, got %d", len(stale))
	}
	if stale[0].ID != "eng-001" {
		t.Errorf("stale engine ID = %q, want %q", stale[0].ID, "eng-001")
	}
}

func TestIntegration_CheckEngineHealth_ExcludesDead(t *testing.T) {
	gormDB := setupTestDB(t, "railyard_ym_health3")

	// Create a dead engine with old activity — should be excluded.
	gormDB.Create(&models.Engine{
		ID:           "eng-001",
		Track:        "backend",
		Status:       "dead",
		LastActivity: time.Now().Add(-5 * time.Minute),
		StartedAt:    time.Now().Add(-10 * time.Minute),
	})

	stale, err := CheckEngineHealth(gormDB, 60*time.Second)
	if err != nil {
		t.Fatalf("CheckEngineHealth: %v", err)
	}
	if len(stale) != 0 {
		t.Errorf("expected 0 stale engines (dead excluded), got %d", len(stale))
	}
}

func TestIntegration_CheckEngineHealth_MixedEngines(t *testing.T) {
	gormDB := setupTestDB(t, "railyard_ym_health4")

	now := time.Now()
	// Healthy engine.
	gormDB.Create(&models.Engine{
		ID: "eng-healthy", Track: "backend", Status: "idle",
		LastActivity: now, StartedAt: now,
	})
	// Stale engine.
	gormDB.Create(&models.Engine{
		ID: "eng-stale", Track: "backend", Status: "working",
		LastActivity: now.Add(-2 * time.Minute), StartedAt: now.Add(-5 * time.Minute),
	})
	// Dead engine.
	gormDB.Create(&models.Engine{
		ID: "eng-dead", Track: "backend", Status: "dead",
		LastActivity: now.Add(-10 * time.Minute), StartedAt: now.Add(-20 * time.Minute),
	})

	stale, err := CheckEngineHealth(gormDB, 60*time.Second)
	if err != nil {
		t.Fatalf("CheckEngineHealth: %v", err)
	}
	if len(stale) != 1 {
		t.Fatalf("expected 1 stale, got %d", len(stale))
	}
	if stale[0].ID != "eng-stale" {
		t.Errorf("expected eng-stale, got %q", stale[0].ID)
	}
}

func TestIntegration_CheckEngineHealth_DBError(t *testing.T) {
	gormDB := setupTestDB(t, "railyard_ym_healtherr")
	sqlDB, _ := gormDB.DB()
	sqlDB.Close()

	_, err := CheckEngineHealth(gormDB, 60*time.Second)
	if err == nil {
		t.Fatal("expected error for closed DB")
	}
}

// --- ReassignCar integration tests ---

func TestIntegration_ReassignCar(t *testing.T) {
	gormDB := setupTestDB(t, "railyard_ym_reassign1")

	// Create engine and car.
	gormDB.Create(&models.Engine{
		ID: "eng-001", Track: "backend", Status: "stalled",
		CurrentCar: "car-001", LastActivity: time.Now(), StartedAt: time.Now(),
	})
	gormDB.Create(&models.Car{
		ID: "car-001", Title: "Test Task", Track: "backend",
		Status: "in_progress", Assignee: "eng-001",
	})

	err := ReassignCar(gormDB, "car-001", "eng-001", "heartbeat stale >60s")
	if err != nil {
		t.Fatalf("ReassignCar: %v", err)
	}

	// Verify car is open and unassigned.
	var b models.Car
	gormDB.First(&b, "id = ?", "car-001")
	if b.Status != "open" {
		t.Errorf("car status = %q, want %q", b.Status, "open")
	}
	if b.Assignee != "" {
		t.Errorf("car assignee = %q, want empty", b.Assignee)
	}

	// Verify engine is dead.
	var eng models.Engine
	gormDB.First(&eng, "id = ?", "eng-001")
	if eng.Status != "dead" {
		t.Errorf("engine status = %q, want %q", eng.Status, "dead")
	}
	if eng.CurrentCar != "" {
		t.Errorf("engine current_car = %q, want empty", eng.CurrentCar)
	}

	// Verify progress note was written.
	var progress []models.CarProgress
	gormDB.Where("car_id = ?", "car-001").Find(&progress)
	if len(progress) != 1 {
		t.Fatalf("expected 1 progress note, got %d", len(progress))
	}
	if progress[0].Note == "" {
		t.Error("progress note should not be empty")
	}

	// Verify broadcast was sent.
	var msgs []models.Message
	gormDB.Where("to_agent = ? AND subject = ?", "broadcast", "reassignment").Find(&msgs)
	if len(msgs) != 1 {
		t.Fatalf("expected 1 broadcast message, got %d", len(msgs))
	}
}

func TestIntegration_ReassignCar_NotFound(t *testing.T) {
	gormDB := setupTestDB(t, "railyard_ym_reassign2")

	err := ReassignCar(gormDB, "car-nonexistent", "eng-001", "stalled")
	if err == nil {
		t.Fatal("expected error for non-existent car")
	}
	if err.Error() != "yardmaster: car car-nonexistent not found" {
		t.Errorf("error = %q", err)
	}
}

func TestIntegration_ReassignCar_DBError(t *testing.T) {
	gormDB := setupTestDB(t, "railyard_ym_reassignerr")
	sqlDB, _ := gormDB.DB()
	sqlDB.Close()

	err := ReassignCar(gormDB, "car-001", "eng-001", "stalled")
	if err == nil {
		t.Fatal("expected error for closed DB")
	}
}

// --- Switch integration tests ---

func TestIntegration_Switch_CarNotFound(t *testing.T) {
	gormDB := setupTestDB(t, "railyard_ym_switch1")

	_, err := Switch(gormDB, "car-nonexistent", SwitchOpts{RepoDir: t.TempDir()})
	if err == nil {
		t.Fatal("expected error for non-existent car")
	}
}

func TestIntegration_Switch_NoBranch(t *testing.T) {
	gormDB := setupTestDB(t, "railyard_ym_switch2")

	gormDB.Create(&models.Car{
		ID: "car-001", Title: "Test", Track: "backend", Status: "done", Branch: "",
	})

	_, err := Switch(gormDB, "car-001", SwitchOpts{RepoDir: t.TempDir()})
	if err == nil {
		t.Fatal("expected error for empty branch")
	}
}

func TestIntegration_Switch_DBError(t *testing.T) {
	gormDB := setupTestDB(t, "railyard_ym_switcherr")
	sqlDB, _ := gormDB.DB()
	sqlDB.Close()

	_, err := Switch(gormDB, "car-001", SwitchOpts{RepoDir: t.TempDir()})
	if err == nil {
		t.Fatal("expected error for closed DB")
	}
}

// --- UnblockDeps integration tests ---

func TestIntegration_UnblockDeps_NoDeps(t *testing.T) {
	gormDB := setupTestDB(t, "railyard_ym_unblock1")

	gormDB.Create(&models.Car{
		ID: "car-001", Title: "Done Task", Track: "backend", Status: "done",
	})

	unblocked, err := UnblockDeps(gormDB, "car-001")
	if err != nil {
		t.Fatalf("UnblockDeps: %v", err)
	}
	if len(unblocked) != 0 {
		t.Errorf("expected 0 unblocked, got %d", len(unblocked))
	}
}

func TestIntegration_UnblockDeps_SingleDep(t *testing.T) {
	gormDB := setupTestDB(t, "railyard_ym_unblock2")

	// car-001 (done) blocks car-002 (blocked).
	gormDB.Create(&models.Car{
		ID: "car-001", Title: "Backend API", Track: "backend", Status: "done",
	})
	gormDB.Create(&models.Car{
		ID: "car-002", Title: "Frontend Consumer", Track: "frontend", Status: "blocked",
	})
	gormDB.Create(&models.CarDep{
		CarID: "car-002", BlockedBy: "car-001", DepType: "blocks",
	})

	unblocked, err := UnblockDeps(gormDB, "car-001")
	if err != nil {
		t.Fatalf("UnblockDeps: %v", err)
	}
	if len(unblocked) != 1 {
		t.Fatalf("expected 1 unblocked, got %d", len(unblocked))
	}
	if unblocked[0].ID != "car-002" {
		t.Errorf("unblocked ID = %q, want %q", unblocked[0].ID, "car-002")
	}

	// Verify car status changed to open.
	var b models.Car
	gormDB.First(&b, "id = ?", "car-002")
	if b.Status != "open" {
		t.Errorf("car status = %q, want %q", b.Status, "open")
	}
}

func TestIntegration_UnblockDeps_MultipleBlockers(t *testing.T) {
	gormDB := setupTestDB(t, "railyard_ym_unblock3")

	// car-002 is blocked by both car-001 (done) and car-003 (in_progress).
	gormDB.Create(&models.Car{
		ID: "car-001", Title: "Task A", Track: "backend", Status: "done",
	})
	gormDB.Create(&models.Car{
		ID: "car-003", Title: "Task C", Track: "backend", Status: "in_progress",
	})
	gormDB.Create(&models.Car{
		ID: "car-002", Title: "Task B", Track: "frontend", Status: "blocked",
	})
	gormDB.Create(&models.CarDep{
		CarID: "car-002", BlockedBy: "car-001", DepType: "blocks",
	})
	gormDB.Create(&models.CarDep{
		CarID: "car-002", BlockedBy: "car-003", DepType: "blocks",
	})

	// Only car-001 is done — car-002 should NOT be unblocked (car-003 still blocking).
	unblocked, err := UnblockDeps(gormDB, "car-001")
	if err != nil {
		t.Fatalf("UnblockDeps: %v", err)
	}
	if len(unblocked) != 0 {
		t.Errorf("expected 0 unblocked (still blocked by car-003), got %d", len(unblocked))
	}
}

func TestIntegration_UnblockDeps_DBError(t *testing.T) {
	gormDB := setupTestDB(t, "railyard_ym_unblockerr")
	sqlDB, _ := gormDB.DB()
	sqlDB.Close()

	_, err := UnblockDeps(gormDB, "car-001")
	if err == nil {
		t.Fatal("expected error for closed DB")
	}
}

// --- CreateReindexJob integration tests ---

func TestIntegration_CreateReindexJob(t *testing.T) {
	gormDB := setupTestDB(t, "railyard_ym_reindex1")

	err := CreateReindexJob(gormDB, "backend", "abc123def")
	if err != nil {
		t.Fatalf("CreateReindexJob: %v", err)
	}

	var job models.ReindexJob
	if err := gormDB.First(&job).Error; err != nil {
		t.Fatalf("query reindex job: %v", err)
	}
	if job.Track != "backend" {
		t.Errorf("Track = %q, want %q", job.Track, "backend")
	}
	if job.TriggerCommit != "abc123def" {
		t.Errorf("TriggerCommit = %q, want %q", job.TriggerCommit, "abc123def")
	}
	if job.Status != "pending" {
		t.Errorf("Status = %q, want %q", job.Status, "pending")
	}
}

func TestIntegration_CreateReindexJob_DBError(t *testing.T) {
	gormDB := setupTestDB(t, "railyard_ym_reindexerr")
	sqlDB, _ := gormDB.DB()
	sqlDB.Close()

	err := CreateReindexJob(gormDB, "backend", "abc123")
	if err == nil {
		t.Fatal("expected error for closed DB")
	}
}

func TestIntegration_CreateReindexJob_EmptyTrackWithDB(t *testing.T) {
	gormDB := setupTestDB(t, "railyard_ym_reindextrack")

	err := CreateReindexJob(gormDB, "", "abc123")
	if err == nil {
		t.Fatal("expected error for empty track")
	}
}

// --- Switch with real git repo ---

func setupGitRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()

	// Init repo.
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=Test",
			"GIT_AUTHOR_EMAIL=test@test.com",
			"GIT_COMMITTER_NAME=Test",
			"GIT_COMMITTER_EMAIL=test@test.com",
		)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %s: %s: %v", strings.Join(args, " "), out, err)
		}
	}

	run("init", "-b", "main")
	run("config", "user.email", "test@test.com")
	run("config", "user.name", "Test")

	// Create a proper Go module so `go test ./...` works.
	writeFile(t, dir, "go.mod", "module testmod\n\ngo 1.21\n")
	writeFile(t, dir, "main.go", "package main\nfunc main() {}\n")
	run("add", ".")
	run("commit", "-m", "initial")

	return dir
}

func writeFile(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(dir+"/"+name, []byte(content), 0644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}

func TestIntegration_Switch_FullFlow(t *testing.T) {
	gormDB := setupTestDB(t, "railyard_ym_switchfull")
	repoDir := setupGitRepo(t)

	// Create a feature branch with a change.
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = repoDir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=Test",
			"GIT_AUTHOR_EMAIL=test@test.com",
			"GIT_COMMITTER_NAME=Test",
			"GIT_COMMITTER_EMAIL=test@test.com",
		)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %s: %s: %v", strings.Join(args, " "), out, err)
		}
	}

	run("checkout", "-b", "ry/alice/backend/car-001")
	writeFile(t, repoDir, "feature.go", "package main\n// new feature\n")
	run("add", ".")
	run("commit", "-m", "add feature")
	run("checkout", "main")

	// Create the car in DB.
	gormDB.Create(&models.Car{
		ID: "car-001", Title: "Test Feature", Track: "backend",
		Status: "done", Branch: "ry/alice/backend/car-001",
	})

	result, err := Switch(gormDB, "car-001", SwitchOpts{
		RepoDir: repoDir,
	})
	if err != nil {
		t.Fatalf("Switch: %v", err)
	}

	if !result.TestsPassed {
		t.Errorf("expected tests to pass, output: %s", result.TestOutput)
	}
	if !result.Merged {
		t.Error("expected branch to be merged")
	}
}

func TestIntegration_Switch_DryRun(t *testing.T) {
	gormDB := setupTestDB(t, "railyard_ym_switchdry")
	repoDir := setupGitRepo(t)

	// Create a feature branch.
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = repoDir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=Test",
			"GIT_AUTHOR_EMAIL=test@test.com",
			"GIT_COMMITTER_NAME=Test",
			"GIT_COMMITTER_EMAIL=test@test.com",
		)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %s: %s: %v", strings.Join(args, " "), out, err)
		}
	}

	run("checkout", "-b", "ry/alice/backend/car-dry")
	writeFile(t, repoDir, "dry.go", "package main\n// dry run\n")
	run("add", ".")
	run("commit", "-m", "dry run feature")
	run("checkout", "main")

	gormDB.Create(&models.Car{
		ID: "car-dry", Title: "Dry Run", Track: "backend",
		Status: "done", Branch: "ry/alice/backend/car-dry",
	})

	result, err := Switch(gormDB, "car-dry", SwitchOpts{
		RepoDir: repoDir,
		DryRun:  true,
	})
	if err != nil {
		t.Fatalf("Switch: %v", err)
	}

	if !result.TestsPassed {
		t.Errorf("expected tests to pass")
	}
	if result.Merged {
		t.Error("dry run should not merge")
	}
}

func TestIntegration_Switch_MergeWithUnblock(t *testing.T) {
	gormDB := setupTestDB(t, "railyard_ym_switchunblock")
	repoDir := setupGitRepo(t)

	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = repoDir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=Test",
			"GIT_AUTHOR_EMAIL=test@test.com",
			"GIT_COMMITTER_NAME=Test",
			"GIT_COMMITTER_EMAIL=test@test.com",
		)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %s: %s: %v", strings.Join(args, " "), out, err)
		}
	}

	// Create feature branch.
	run("checkout", "-b", "ry/alice/backend/car-parent")
	writeFile(t, repoDir, "api.go", "package main\n// api\n")
	run("add", ".")
	run("commit", "-m", "add api")
	run("checkout", "main")

	// Create cars: car-parent (done) blocks car-child (blocked).
	gormDB.Create(&models.Car{
		ID: "car-parent", Title: "Backend API", Track: "backend",
		Status: "done", Branch: "ry/alice/backend/car-parent",
	})
	gormDB.Create(&models.Car{
		ID: "car-child", Title: "Frontend Consumer", Track: "frontend",
		Status: "blocked",
	})
	gormDB.Create(&models.CarDep{
		CarID: "car-child", BlockedBy: "car-parent", DepType: "blocks",
	})

	result, err := Switch(gormDB, "car-parent", SwitchOpts{RepoDir: repoDir})
	if err != nil {
		t.Fatalf("Switch: %v", err)
	}
	if !result.Merged {
		t.Error("expected merge")
	}

	// Verify car-child was unblocked.
	var child models.Car
	gormDB.First(&child, "id = ?", "car-child")
	if child.Status != "open" {
		t.Errorf("child status = %q, want %q", child.Status, "open")
	}

	// Verify broadcast about unblocking was sent.
	var msgs []models.Message
	gormDB.Where("subject = ?", "deps-unblocked").Find(&msgs)
	if len(msgs) != 1 {
		t.Errorf("expected 1 deps-unblocked message, got %d", len(msgs))
	}
}

func TestIntegration_Switch_FetchError(t *testing.T) {
	gormDB := setupTestDB(t, "railyard_ym_switchfetcherr")

	gormDB.Create(&models.Car{
		ID: "car-fetch", Title: "Fetch Test", Track: "backend",
		Status: "done", Branch: "ry/alice/backend/car-fetch",
	})

	// Use a non-git directory to cause fetch failure.
	_, err := Switch(gormDB, "car-fetch", SwitchOpts{RepoDir: t.TempDir()})
	if err == nil {
		t.Fatal("expected error for fetch in non-git dir")
	}
}

func TestIntegration_Switch_TestFailure(t *testing.T) {
	gormDB := setupTestDB(t, "railyard_ym_switchfail")
	repoDir := setupGitRepo(t)

	// Create a feature branch with a broken test.
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = repoDir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=Test",
			"GIT_AUTHOR_EMAIL=test@test.com",
			"GIT_COMMITTER_NAME=Test",
			"GIT_COMMITTER_EMAIL=test@test.com",
		)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %s: %s: %v", strings.Join(args, " "), out, err)
		}
	}

	run("checkout", "-b", "ry/alice/backend/car-fail")
	writeFile(t, repoDir, "main_test.go", "package main\nimport \"testing\"\nfunc TestFail(t *testing.T) { t.Fatal(\"broken\") }\n")
	run("add", ".")
	run("commit", "-m", "broken test")
	run("checkout", "main")

	gormDB.Create(&models.Car{
		ID: "car-fail", Title: "Broken", Track: "backend",
		Status: "done", Branch: "ry/alice/backend/car-fail",
		Assignee: "eng-001",
	})
	gormDB.Create(&models.Engine{
		ID: "eng-001", Track: "backend", Status: "idle",
		LastActivity: time.Now(), StartedAt: time.Now(),
	})

	result, err := Switch(gormDB, "car-fail", SwitchOpts{
		RepoDir: repoDir,
	})
	// Switch returns nil error for test failure (it's a normal outcome).
	if err != nil {
		t.Fatalf("Switch: %v", err)
	}
	if result.TestsPassed {
		t.Error("expected tests to fail")
	}
	if result.Merged {
		t.Error("should not merge when tests fail")
	}

	// Verify car was set to blocked.
	var b models.Car
	gormDB.First(&b, "id = ?", "car-fail")
	if b.Status != "blocked" {
		t.Errorf("car status = %q, want %q", b.Status, "blocked")
	}

	// Verify test failure message was sent to the engine.
	var msgs []models.Message
	gormDB.Where("to_agent = ? AND subject = ?", "eng-001", "test-failure").Find(&msgs)
	if len(msgs) != 1 {
		t.Errorf("expected 1 test failure message, got %d", len(msgs))
	}
}
