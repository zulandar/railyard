//go:build integration

package car

import (
	"fmt"
	"net"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/zulandar/railyard/internal/db"
	"gorm.io/gorm"
)

// testDoltServer manages a Dolt SQL server lifecycle for integration tests.
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

func setupTestDB(t *testing.T, dbName string) *testDoltServer {
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
	return srv
}

func TestIntegration_Create(t *testing.T) {
	srv := setupTestDB(t, "railyard_car_create")
	gormDB, err := db.Connect("127.0.0.1", srv.Port, "railyard_car_create")
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}

	car, err := Create(gormDB, CreateOpts{
		Title:        "Test car",
		Description:  "A test car",
		Track:        "backend",
		Priority:     2,
		BranchPrefix: "ry/alice",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if !strings.HasPrefix(car.ID, "car-") {
		t.Errorf("ID %q missing car- prefix", car.ID)
	}
	if car.Title != "Test car" {
		t.Errorf("Title = %q, want %q", car.Title, "Test car")
	}
	if car.Status != "open" {
		t.Errorf("Status = %q, want %q", car.Status, "open")
	}
	if car.Type != "task" {
		t.Errorf("Type = %q, want %q (default)", car.Type, "task")
	}
	if !strings.HasPrefix(car.Branch, "ry/alice/backend/car-") {
		t.Errorf("Branch = %q, want prefix %q", car.Branch, "ry/alice/backend/car-")
	}
}

func TestIntegration_Create_WithType(t *testing.T) {
	srv := setupTestDB(t, "railyard_car_type")
	gormDB, err := db.Connect("127.0.0.1", srv.Port, "railyard_car_type")
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}

	car, err := Create(gormDB, CreateOpts{
		Title:        "Epic car",
		Track:        "frontend",
		Type:         "epic",
		BranchPrefix: "ry/bob",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if car.Type != "epic" {
		t.Errorf("Type = %q, want %q", car.Type, "epic")
	}
}

func TestIntegration_Create_WithParent(t *testing.T) {
	srv := setupTestDB(t, "railyard_car_parent")
	gormDB, err := db.Connect("127.0.0.1", srv.Port, "railyard_car_parent")
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}

	parent, err := Create(gormDB, CreateOpts{
		Title:        "Parent epic",
		Track:        "backend",
		Type:         "epic",
		BranchPrefix: "ry/alice",
	})
	if err != nil {
		t.Fatalf("Create parent: %v", err)
	}

	child, err := Create(gormDB, CreateOpts{
		Title:        "Child task",
		Track:        "backend",
		ParentID:     parent.ID,
		BranchPrefix: "ry/alice",
	})
	if err != nil {
		t.Fatalf("Create child: %v", err)
	}
	if child.ParentID == nil || *child.ParentID != parent.ID {
		t.Errorf("ParentID = %v, want %q", child.ParentID, parent.ID)
	}
}

func TestIntegration_Create_ValidationErrors(t *testing.T) {
	srv := setupTestDB(t, "railyard_car_val")
	gormDB, err := db.Connect("127.0.0.1", srv.Port, "railyard_car_val")
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}

	tests := []struct {
		name    string
		opts    CreateOpts
		wantErr string
	}{
		{
			name:    "missing title",
			opts:    CreateOpts{Track: "backend"},
			wantErr: "title is required",
		},
		{
			name:    "missing track",
			opts:    CreateOpts{Title: "test"},
			wantErr: "track is required",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Create(gormDB, tt.opts)
			if err == nil {
				t.Fatal("expected error")
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("error = %q, want to contain %q", err.Error(), tt.wantErr)
			}
		})
	}
}

func TestIntegration_Get(t *testing.T) {
	srv := setupTestDB(t, "railyard_car_get")
	gormDB, err := db.Connect("127.0.0.1", srv.Port, "railyard_car_get")
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}

	created, err := Create(gormDB, CreateOpts{
		Title:        "Get test",
		Track:        "backend",
		Description:  "Testing get",
		BranchPrefix: "ry/alice",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	got, err := Get(gormDB, created.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.ID != created.ID {
		t.Errorf("ID = %q, want %q", got.ID, created.ID)
	}
	if got.Title != "Get test" {
		t.Errorf("Title = %q, want %q", got.Title, "Get test")
	}
	if got.Deps == nil {
		t.Error("Deps should be preloaded (empty slice, not nil)")
	}
	if got.Progress == nil {
		t.Error("Progress should be preloaded (empty slice, not nil)")
	}
}

func TestIntegration_Get_NotFound(t *testing.T) {
	srv := setupTestDB(t, "railyard_car_getnf")
	gormDB, err := db.Connect("127.0.0.1", srv.Port, "railyard_car_getnf")
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}

	_, err = Get(gormDB, "car-zzzzz")
	if err == nil {
		t.Fatal("expected error for non-existent ID")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("error = %q, want to contain %q", err.Error(), "not found")
	}
}

func TestIntegration_List(t *testing.T) {
	srv := setupTestDB(t, "railyard_car_list")
	gormDB, err := db.Connect("127.0.0.1", srv.Port, "railyard_car_list")
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}

	for _, tc := range []struct {
		title string
		track string
	}{
		{"Backend task 1", "backend"},
		{"Backend task 2", "backend"},
		{"Frontend task 1", "frontend"},
	} {
		if _, err := Create(gormDB, CreateOpts{
			Title:        tc.title,
			Track:        tc.track,
			BranchPrefix: "ry/alice",
		}); err != nil {
			t.Fatalf("Create %q: %v", tc.title, err)
		}
	}

	// List all
	all, err := List(gormDB, ListFilters{})
	if err != nil {
		t.Fatalf("List all: %v", err)
	}
	if len(all) != 3 {
		t.Errorf("List all: got %d cars, want 3", len(all))
	}

	// Filter by track
	be, err := List(gormDB, ListFilters{Track: "backend"})
	if err != nil {
		t.Fatalf("List backend: %v", err)
	}
	if len(be) != 2 {
		t.Errorf("List backend: got %d cars, want 2", len(be))
	}

	fe, err := List(gormDB, ListFilters{Track: "frontend"})
	if err != nil {
		t.Fatalf("List frontend: %v", err)
	}
	if len(fe) != 1 {
		t.Errorf("List frontend: got %d cars, want 1", len(fe))
	}
}

func TestIntegration_List_Empty(t *testing.T) {
	srv := setupTestDB(t, "railyard_car_empty")
	gormDB, err := db.Connect("127.0.0.1", srv.Port, "railyard_car_empty")
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}

	cars, err := List(gormDB, ListFilters{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(cars) != 0 {
		t.Errorf("List empty DB: got %d cars, want 0", len(cars))
	}
}

func TestIntegration_Update_Status(t *testing.T) {
	srv := setupTestDB(t, "railyard_car_upd")
	gormDB, err := db.Connect("127.0.0.1", srv.Port, "railyard_car_upd")
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}

	car, err := Create(gormDB, CreateOpts{
		Title:        "Update test",
		Track:        "backend",
		BranchPrefix: "ry/alice",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// open → ready
	if err := Update(gormDB, car.ID, map[string]interface{}{"status": "ready"}); err != nil {
		t.Fatalf("Update open→ready: %v", err)
	}

	// ready → claimed (should set claimed_at)
	if err := Update(gormDB, car.ID, map[string]interface{}{
		"status":   "claimed",
		"assignee": "engine-01",
	}); err != nil {
		t.Fatalf("Update ready→claimed: %v", err)
	}

	got, err := Get(gormDB, car.ID)
	if err != nil {
		t.Fatalf("Get after claimed: %v", err)
	}
	if got.Status != "claimed" {
		t.Errorf("Status = %q, want %q", got.Status, "claimed")
	}
	if got.Assignee != "engine-01" {
		t.Errorf("Assignee = %q, want %q", got.Assignee, "engine-01")
	}
	if got.ClaimedAt == nil {
		t.Error("ClaimedAt should be set when status transitions to claimed")
	}
}

func TestIntegration_Update_InvalidTransition(t *testing.T) {
	srv := setupTestDB(t, "railyard_car_inv")
	gormDB, err := db.Connect("127.0.0.1", srv.Port, "railyard_car_inv")
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}

	car, err := Create(gormDB, CreateOpts{
		Title:        "Invalid transition",
		Track:        "backend",
		BranchPrefix: "ry/alice",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// open → done is not valid
	err = Update(gormDB, car.ID, map[string]interface{}{"status": "done"})
	if err == nil {
		t.Fatal("expected error for invalid transition open→done")
	}
	if !strings.Contains(err.Error(), "invalid status transition") {
		t.Errorf("error = %q, want to contain %q", err.Error(), "invalid status transition")
	}
}

func TestIntegration_Update_Blocked(t *testing.T) {
	srv := setupTestDB(t, "railyard_car_blk")
	gormDB, err := db.Connect("127.0.0.1", srv.Port, "railyard_car_blk")
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}

	car, err := Create(gormDB, CreateOpts{
		Title:        "Block test",
		Track:        "backend",
		BranchPrefix: "ry/alice",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Any status → blocked should work
	if err := Update(gormDB, car.ID, map[string]interface{}{"status": "blocked"}); err != nil {
		t.Fatalf("Update open→blocked: %v", err)
	}

	// blocked → open (unblock)
	if err := Update(gormDB, car.ID, map[string]interface{}{"status": "open"}); err != nil {
		t.Fatalf("Update blocked→open: %v", err)
	}
}

func TestIntegration_Update_NotFound(t *testing.T) {
	srv := setupTestDB(t, "railyard_car_updnf")
	gormDB, err := db.Connect("127.0.0.1", srv.Port, "railyard_car_updnf")
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}

	err = Update(gormDB, "car-zzzzz", map[string]interface{}{"status": "ready"})
	if err == nil {
		t.Fatal("expected error for non-existent ID")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("error = %q, want to contain %q", err.Error(), "not found")
	}
}

func TestIntegration_Update_NonStatusFields(t *testing.T) {
	srv := setupTestDB(t, "railyard_car_fld")
	gormDB, err := db.Connect("127.0.0.1", srv.Port, "railyard_car_fld")
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}

	car, err := Create(gormDB, CreateOpts{
		Title:        "Field update test",
		Track:        "backend",
		BranchPrefix: "ry/alice",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if err := Update(gormDB, car.ID, map[string]interface{}{
		"description": "Updated description",
		"priority":    1,
	}); err != nil {
		t.Fatalf("Update fields: %v", err)
	}

	got, err := Get(gormDB, car.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Description != "Updated description" {
		t.Errorf("Description = %q, want %q", got.Description, "Updated description")
	}
	if got.Priority != 1 {
		t.Errorf("Priority = %d, want 1", got.Priority)
	}
}

func TestIntegration_List_FilterByStatus(t *testing.T) {
	srv := setupTestDB(t, "railyard_car_lstat")
	gormDB, err := db.Connect("127.0.0.1", srv.Port, "railyard_car_lstat")
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}

	b1, err := Create(gormDB, CreateOpts{
		Title: "Open car", Track: "backend", BranchPrefix: "ry/alice",
	})
	if err != nil {
		t.Fatalf("Create b1: %v", err)
	}
	b2, err := Create(gormDB, CreateOpts{
		Title: "Ready car", Track: "backend", BranchPrefix: "ry/alice",
	})
	if err != nil {
		t.Fatalf("Create b2: %v", err)
	}
	if err := Update(gormDB, b2.ID, map[string]interface{}{"status": "ready"}); err != nil {
		t.Fatalf("Update b2: %v", err)
	}

	open, err := List(gormDB, ListFilters{Status: "open"})
	if err != nil {
		t.Fatalf("List status=open: %v", err)
	}
	if len(open) != 1 || open[0].ID != b1.ID {
		t.Errorf("List status=open: got %d cars, want 1 with ID %s", len(open), b1.ID)
	}

	ready, err := List(gormDB, ListFilters{Status: "ready"})
	if err != nil {
		t.Fatalf("List status=ready: %v", err)
	}
	if len(ready) != 1 || ready[0].ID != b2.ID {
		t.Errorf("List status=ready: got %d cars, want 1 with ID %s", len(ready), b2.ID)
	}
}

func TestIntegration_List_FilterByType(t *testing.T) {
	srv := setupTestDB(t, "railyard_car_ltype")
	gormDB, err := db.Connect("127.0.0.1", srv.Port, "railyard_car_ltype")
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}

	if _, err := Create(gormDB, CreateOpts{
		Title: "A task", Track: "backend", Type: "task", BranchPrefix: "ry/alice",
	}); err != nil {
		t.Fatalf("Create task: %v", err)
	}
	if _, err := Create(gormDB, CreateOpts{
		Title: "An epic", Track: "backend", Type: "epic", BranchPrefix: "ry/alice",
	}); err != nil {
		t.Fatalf("Create epic: %v", err)
	}

	tasks, err := List(gormDB, ListFilters{Type: "task"})
	if err != nil {
		t.Fatalf("List type=task: %v", err)
	}
	if len(tasks) != 1 {
		t.Errorf("List type=task: got %d, want 1", len(tasks))
	}

	epics, err := List(gormDB, ListFilters{Type: "epic"})
	if err != nil {
		t.Fatalf("List type=epic: %v", err)
	}
	if len(epics) != 1 {
		t.Errorf("List type=epic: got %d, want 1", len(epics))
	}
}

func TestIntegration_List_FilterByAssignee(t *testing.T) {
	srv := setupTestDB(t, "railyard_car_lassn")
	gormDB, err := db.Connect("127.0.0.1", srv.Port, "railyard_car_lassn")
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}

	b, err := Create(gormDB, CreateOpts{
		Title: "Assigned car", Track: "backend", BranchPrefix: "ry/alice",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := Update(gormDB, b.ID, map[string]interface{}{"status": "ready"}); err != nil {
		t.Fatalf("Update ready: %v", err)
	}
	if err := Update(gormDB, b.ID, map[string]interface{}{"status": "claimed", "assignee": "engine-01"}); err != nil {
		t.Fatalf("Update claimed: %v", err)
	}
	if _, err := Create(gormDB, CreateOpts{
		Title: "Unassigned car", Track: "backend", BranchPrefix: "ry/alice",
	}); err != nil {
		t.Fatalf("Create unassigned: %v", err)
	}

	assigned, err := List(gormDB, ListFilters{Assignee: "engine-01"})
	if err != nil {
		t.Fatalf("List assignee=engine-01: %v", err)
	}
	if len(assigned) != 1 {
		t.Errorf("List assignee=engine-01: got %d, want 1", len(assigned))
	}
}

func TestIntegration_List_MultipleFilters(t *testing.T) {
	srv := setupTestDB(t, "railyard_car_lmulti")
	gormDB, err := db.Connect("127.0.0.1", srv.Port, "railyard_car_lmulti")
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}

	if _, err := Create(gormDB, CreateOpts{
		Title: "Backend task", Track: "backend", Type: "task", BranchPrefix: "ry/alice",
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := Create(gormDB, CreateOpts{
		Title: "Backend epic", Track: "backend", Type: "epic", BranchPrefix: "ry/alice",
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := Create(gormDB, CreateOpts{
		Title: "Frontend task", Track: "frontend", Type: "task", BranchPrefix: "ry/alice",
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Filter by track AND type
	result, err := List(gormDB, ListFilters{Track: "backend", Type: "task"})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(result) != 1 {
		t.Errorf("List track=backend,type=task: got %d, want 1", len(result))
	}
}

func TestIntegration_Update_Cancelled(t *testing.T) {
	srv := setupTestDB(t, "railyard_car_cancel")
	gormDB, err := db.Connect("127.0.0.1", srv.Port, "railyard_car_cancel")
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}

	car, err := Create(gormDB, CreateOpts{
		Title: "Cancel test", Track: "backend", BranchPrefix: "ry/alice",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// open → cancelled
	if err := Update(gormDB, car.ID, map[string]interface{}{"status": "cancelled"}); err != nil {
		t.Fatalf("Update open→cancelled: %v", err)
	}

	got, err := Get(gormDB, car.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Status != "cancelled" {
		t.Errorf("Status = %q, want %q", got.Status, "cancelled")
	}
}

func TestIntegration_Create_InvalidParent(t *testing.T) {
	srv := setupTestDB(t, "railyard_car_invpar")
	gormDB, err := db.Connect("127.0.0.1", srv.Port, "railyard_car_invpar")
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}

	_, err = Create(gormDB, CreateOpts{
		Title:        "Orphan task",
		Track:        "backend",
		ParentID:     "car-zzzzz",
		BranchPrefix: "ry/alice",
	})
	if err == nil {
		t.Fatal("expected error for non-existent parent")
	}
	if !strings.Contains(err.Error(), "parent not found") {
		t.Errorf("error = %q, want to contain %q", err.Error(), "parent not found")
	}
}

func TestIntegration_Create_NonEpicParent(t *testing.T) {
	srv := setupTestDB(t, "railyard_car_neppar")
	gormDB, err := db.Connect("127.0.0.1", srv.Port, "railyard_car_neppar")
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}

	task, err := Create(gormDB, CreateOpts{
		Title:        "A task (not epic)",
		Track:        "backend",
		Type:         "task",
		BranchPrefix: "ry/alice",
	})
	if err != nil {
		t.Fatalf("Create task: %v", err)
	}

	_, err = Create(gormDB, CreateOpts{
		Title:        "Child of task",
		Track:        "backend",
		ParentID:     task.ID,
		BranchPrefix: "ry/alice",
	})
	if err == nil {
		t.Fatal("expected error for non-epic parent")
	}
	if !strings.Contains(err.Error(), "only epics can have children") {
		t.Errorf("error = %q, want to contain %q", err.Error(), "only epics can have children")
	}
}

func TestIntegration_Create_TrackInheritance(t *testing.T) {
	srv := setupTestDB(t, "railyard_car_trackinh")
	gormDB, err := db.Connect("127.0.0.1", srv.Port, "railyard_car_trackinh")
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}

	parent, err := Create(gormDB, CreateOpts{
		Title:        "Parent epic",
		Track:        "backend",
		Type:         "epic",
		BranchPrefix: "ry/alice",
	})
	if err != nil {
		t.Fatalf("Create parent: %v", err)
	}

	// Create child without specifying track — should inherit from parent.
	child, err := Create(gormDB, CreateOpts{
		Title:        "Inherited track child",
		ParentID:     parent.ID,
		BranchPrefix: "ry/alice",
	})
	if err != nil {
		t.Fatalf("Create child: %v", err)
	}
	if child.Track != "backend" {
		t.Errorf("Track = %q, want %q (inherited from parent)", child.Track, "backend")
	}
	if !strings.Contains(child.Branch, "backend") {
		t.Errorf("Branch = %q, want to contain %q", child.Branch, "backend")
	}
}

func TestIntegration_GetChildren(t *testing.T) {
	srv := setupTestDB(t, "railyard_car_getchi")
	gormDB, err := db.Connect("127.0.0.1", srv.Port, "railyard_car_getchi")
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}

	epic, err := Create(gormDB, CreateOpts{
		Title:        "Parent epic",
		Track:        "backend",
		Type:         "epic",
		BranchPrefix: "ry/alice",
	})
	if err != nil {
		t.Fatalf("Create epic: %v", err)
	}

	for _, title := range []string{"Child 1", "Child 2", "Child 3"} {
		if _, err := Create(gormDB, CreateOpts{
			Title:        title,
			Track:        "backend",
			ParentID:     epic.ID,
			BranchPrefix: "ry/alice",
		}); err != nil {
			t.Fatalf("Create %q: %v", title, err)
		}
	}

	children, err := GetChildren(gormDB, epic.ID)
	if err != nil {
		t.Fatalf("GetChildren: %v", err)
	}
	if len(children) != 3 {
		t.Errorf("GetChildren: got %d, want 3", len(children))
	}

	// Non-existent parent.
	_, err = GetChildren(gormDB, "car-zzzzz")
	if err == nil {
		t.Fatal("expected error for non-existent parent")
	}
	if !strings.Contains(err.Error(), "parent not found") {
		t.Errorf("error = %q, want to contain %q", err.Error(), "parent not found")
	}
}

func TestIntegration_ChildrenSummary(t *testing.T) {
	srv := setupTestDB(t, "railyard_car_chsum")
	gormDB, err := db.Connect("127.0.0.1", srv.Port, "railyard_car_chsum")
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}

	epic, err := Create(gormDB, CreateOpts{
		Title:        "Summary epic",
		Track:        "backend",
		Type:         "epic",
		BranchPrefix: "ry/alice",
	})
	if err != nil {
		t.Fatalf("Create epic: %v", err)
	}

	// Create 3 children, move one to ready and one through to done.
	c1, err := Create(gormDB, CreateOpts{
		Title: "Open child", Track: "backend", ParentID: epic.ID, BranchPrefix: "ry/alice",
	})
	if err != nil {
		t.Fatalf("Create c1: %v", err)
	}
	_ = c1

	c2, err := Create(gormDB, CreateOpts{
		Title: "Ready child", Track: "backend", ParentID: epic.ID, BranchPrefix: "ry/alice",
	})
	if err != nil {
		t.Fatalf("Create c2: %v", err)
	}
	if err := Update(gormDB, c2.ID, map[string]interface{}{"status": "ready"}); err != nil {
		t.Fatalf("Update c2 ready: %v", err)
	}

	c3, err := Create(gormDB, CreateOpts{
		Title: "Done child", Track: "backend", ParentID: epic.ID, BranchPrefix: "ry/alice",
	})
	if err != nil {
		t.Fatalf("Create c3: %v", err)
	}
	for _, status := range []string{"ready", "claimed", "in_progress", "done"} {
		updates := map[string]interface{}{"status": status}
		if status == "claimed" {
			updates["assignee"] = "engine-01"
		}
		if err := Update(gormDB, c3.ID, updates); err != nil {
			t.Fatalf("Update c3 %s: %v", status, err)
		}
	}

	summary, err := ChildrenSummary(gormDB, epic.ID)
	if err != nil {
		t.Fatalf("ChildrenSummary: %v", err)
	}

	counts := make(map[string]int)
	total := 0
	for _, sc := range summary {
		counts[sc.Status] = sc.Count
		total += sc.Count
	}
	if total != 3 {
		t.Errorf("total children = %d, want 3", total)
	}
	if counts["open"] != 1 {
		t.Errorf("open count = %d, want 1", counts["open"])
	}
	if counts["done"] != 1 {
		t.Errorf("done count = %d, want 1", counts["done"])
	}
	if counts["ready"] != 1 {
		t.Errorf("ready count = %d, want 1", counts["ready"])
	}
}

// closedGormDB returns a GORM connection with the underlying sql.DB closed.
func closedGormDB(t *testing.T) *gorm.DB {
	t.Helper()
	srv := setupTestDB(t, "railyard_car_closed")
	gormDB, err := db.Connect("127.0.0.1", srv.Port, "railyard_car_closed")
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	sqlDB, _ := gormDB.DB()
	sqlDB.Close()
	return gormDB
}

func TestIntegration_Create_DBError(t *testing.T) {
	gormDB := closedGormDB(t)
	_, err := Create(gormDB, CreateOpts{
		Title:        "DB error test",
		Track:        "backend",
		BranchPrefix: "ry/alice",
	})
	if err == nil {
		t.Fatal("expected error from Create with closed DB")
	}
}

func TestIntegration_Get_DBError(t *testing.T) {
	gormDB := closedGormDB(t)
	_, err := Get(gormDB, "car-12345")
	if err == nil {
		t.Fatal("expected error from Get with closed DB")
	}
	if !strings.Contains(err.Error(), "car: get") {
		t.Errorf("error = %q, want to contain %q", err.Error(), "car: get")
	}
}

func TestIntegration_List_DBError(t *testing.T) {
	gormDB := closedGormDB(t)
	_, err := List(gormDB, ListFilters{})
	if err == nil {
		t.Fatal("expected error from List with closed DB")
	}
	if !strings.Contains(err.Error(), "car: list") {
		t.Errorf("error = %q, want to contain %q", err.Error(), "car: list")
	}
}

func TestIntegration_Update_DBError(t *testing.T) {
	gormDB := closedGormDB(t)
	err := Update(gormDB, "car-12345", map[string]interface{}{"status": "ready"})
	if err == nil {
		t.Fatal("expected error from Update with closed DB")
	}
	if !strings.Contains(err.Error(), "car:") {
		t.Errorf("error = %q, want to contain %q", err.Error(), "car:")
	}
}

func TestIntegration_FullLifecycle(t *testing.T) {
	srv := setupTestDB(t, "railyard_car_life")
	gormDB, err := db.Connect("127.0.0.1", srv.Port, "railyard_car_life")
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}

	// Create
	car, err := Create(gormDB, CreateOpts{
		Title:        "Lifecycle test",
		Track:        "backend",
		Priority:     1,
		BranchPrefix: "ry/alice",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// open → ready
	if err := Update(gormDB, car.ID, map[string]interface{}{"status": "ready"}); err != nil {
		t.Fatalf("open→ready: %v", err)
	}

	// ready → claimed
	if err := Update(gormDB, car.ID, map[string]interface{}{
		"status":   "claimed",
		"assignee": "engine-01",
	}); err != nil {
		t.Fatalf("ready→claimed: %v", err)
	}

	// claimed → in_progress
	if err := Update(gormDB, car.ID, map[string]interface{}{"status": "in_progress"}); err != nil {
		t.Fatalf("claimed→in_progress: %v", err)
	}

	// in_progress → done
	if err := Update(gormDB, car.ID, map[string]interface{}{"status": "done"}); err != nil {
		t.Fatalf("in_progress→done: %v", err)
	}

	got, err := Get(gormDB, car.ID)
	if err != nil {
		t.Fatalf("Get final: %v", err)
	}
	if got.Status != "done" {
		t.Errorf("final Status = %q, want %q", got.Status, "done")
	}
	if got.CompletedAt == nil {
		t.Error("CompletedAt should be set when status is done")
	}
	if got.ClaimedAt == nil {
		t.Error("ClaimedAt should be set from claimed transition")
	}
}
