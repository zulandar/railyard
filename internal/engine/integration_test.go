//go:build integration

package engine

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zulandar/railyard/internal/bead"
	"github.com/zulandar/railyard/internal/db"
	"github.com/zulandar/railyard/internal/models"
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

func connectDB(t *testing.T, srv *testDoltServer, dbName string) *gorm.DB {
	t.Helper()
	gormDB, err := db.Connect("127.0.0.1", srv.Port, dbName)
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	return gormDB
}

// closedGormDB returns a GORM connection with the underlying sql.DB closed.
func closedGormDB(t *testing.T) *gorm.DB {
	t.Helper()
	dbName := "railyard_eng_closed"
	srv := setupTestDB(t, dbName)
	gormDB := connectDB(t, srv, dbName)
	sqlDB, _ := gormDB.DB()
	sqlDB.Close()
	return gormDB
}

// --- Registration tests ---

func TestIntegration_Register(t *testing.T) {
	dbName := "railyard_eng_reg"
	srv := setupTestDB(t, dbName)
	gormDB := connectDB(t, srv, dbName)

	eng, err := Register(gormDB, RegisterOpts{
		Track:     "backend",
		Role:      "builder",
		VMID:      "vm-123",
		SessionID: "sess-abc",
	})
	if err != nil {
		t.Fatalf("Register: %v", err)
	}

	if !strings.HasPrefix(eng.ID, "eng-") {
		t.Errorf("ID %q missing eng- prefix", eng.ID)
	}
	if eng.Track != "backend" {
		t.Errorf("Track = %q, want %q", eng.Track, "backend")
	}
	if eng.Role != "builder" {
		t.Errorf("Role = %q, want %q", eng.Role, "builder")
	}
	if eng.Status != StatusIdle {
		t.Errorf("Status = %q, want %q", eng.Status, StatusIdle)
	}
	if eng.VMID != "vm-123" {
		t.Errorf("VMID = %q, want %q", eng.VMID, "vm-123")
	}
	if eng.SessionID != "sess-abc" {
		t.Errorf("SessionID = %q, want %q", eng.SessionID, "sess-abc")
	}
	if eng.StartedAt.IsZero() {
		t.Error("StartedAt should be set")
	}
	if eng.LastActivity.IsZero() {
		t.Error("LastActivity should be set")
	}
}

func TestIntegration_Register_DefaultRole(t *testing.T) {
	dbName := "railyard_eng_defrole"
	srv := setupTestDB(t, dbName)
	gormDB := connectDB(t, srv, dbName)

	eng, err := Register(gormDB, RegisterOpts{Track: "backend"})
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	if eng.Role != "engine" {
		t.Errorf("Role = %q, want default %q", eng.Role, "engine")
	}
}

func TestIntegration_Register_ValidationError(t *testing.T) {
	dbName := "railyard_eng_regval"
	srv := setupTestDB(t, dbName)
	gormDB := connectDB(t, srv, dbName)

	_, err := Register(gormDB, RegisterOpts{})
	if err == nil {
		t.Fatal("expected error for missing track")
	}
	if !strings.Contains(err.Error(), "track is required") {
		t.Errorf("error = %q, want to contain %q", err.Error(), "track is required")
	}
}

func TestIntegration_Register_DBError(t *testing.T) {
	gormDB := closedGormDB(t)
	_, err := Register(gormDB, RegisterOpts{Track: "backend"})
	if err == nil {
		t.Fatal("expected error from Register with closed DB")
	}
}

// --- Deregistration tests ---

func TestIntegration_Deregister(t *testing.T) {
	dbName := "railyard_eng_dereg"
	srv := setupTestDB(t, dbName)
	gormDB := connectDB(t, srv, dbName)

	eng, err := Register(gormDB, RegisterOpts{Track: "backend"})
	if err != nil {
		t.Fatalf("Register: %v", err)
	}

	if err := Deregister(gormDB, eng.ID); err != nil {
		t.Fatalf("Deregister: %v", err)
	}

	got, err := Get(gormDB, eng.ID)
	if err != nil {
		t.Fatalf("Get after deregister: %v", err)
	}
	if got.Status != StatusDead {
		t.Errorf("Status = %q, want %q", got.Status, StatusDead)
	}
}

func TestIntegration_Deregister_NotFound(t *testing.T) {
	dbName := "railyard_eng_deregnf"
	srv := setupTestDB(t, dbName)
	gormDB := connectDB(t, srv, dbName)

	err := Deregister(gormDB, "eng-zzzzz")
	if err == nil {
		t.Fatal("expected error for non-existent engine")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("error = %q, want to contain %q", err.Error(), "not found")
	}
}

func TestIntegration_Deregister_DBError(t *testing.T) {
	gormDB := closedGormDB(t)
	err := Deregister(gormDB, "eng-12345")
	if err == nil {
		t.Fatal("expected error from Deregister with closed DB")
	}
}

// --- Get tests ---

func TestIntegration_Get(t *testing.T) {
	dbName := "railyard_eng_get"
	srv := setupTestDB(t, dbName)
	gormDB := connectDB(t, srv, dbName)

	eng, err := Register(gormDB, RegisterOpts{Track: "backend", Role: "builder"})
	if err != nil {
		t.Fatalf("Register: %v", err)
	}

	got, err := Get(gormDB, eng.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.ID != eng.ID {
		t.Errorf("ID = %q, want %q", got.ID, eng.ID)
	}
	if got.Track != "backend" {
		t.Errorf("Track = %q, want %q", got.Track, "backend")
	}
}

func TestIntegration_Get_NotFound(t *testing.T) {
	dbName := "railyard_eng_getnf"
	srv := setupTestDB(t, dbName)
	gormDB := connectDB(t, srv, dbName)

	_, err := Get(gormDB, "eng-zzzzz")
	if err == nil {
		t.Fatal("expected error for non-existent engine")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("error = %q, want to contain %q", err.Error(), "not found")
	}
}

func TestIntegration_Get_DBError(t *testing.T) {
	gormDB := closedGormDB(t)
	_, err := Get(gormDB, "eng-12345")
	if err == nil {
		t.Fatal("expected error from Get with closed DB")
	}
	if !strings.Contains(err.Error(), "engine: get") {
		t.Errorf("error = %q, want to contain %q", err.Error(), "engine: get")
	}
}

// --- Heartbeat tests ---

func TestIntegration_StartHeartbeat(t *testing.T) {
	dbName := "railyard_eng_hb"
	srv := setupTestDB(t, dbName)
	gormDB := connectDB(t, srv, dbName)

	eng, err := Register(gormDB, RegisterOpts{Track: "backend"})
	if err != nil {
		t.Fatalf("Register: %v", err)
	}

	initialActivity := eng.LastActivity

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errCh := StartHeartbeat(ctx, gormDB, eng.ID, 100*time.Millisecond)

	// Wait for at least 2 heartbeats.
	time.Sleep(350 * time.Millisecond)
	cancel()

	// Check for errors.
	select {
	case err := <-errCh:
		t.Fatalf("heartbeat error: %v", err)
	default:
	}

	got, err := Get(gormDB, eng.ID)
	if err != nil {
		t.Fatalf("Get after heartbeat: %v", err)
	}
	if !got.LastActivity.After(initialActivity) {
		t.Errorf("LastActivity not updated: initial=%v, current=%v", initialActivity, got.LastActivity)
	}
}

func TestIntegration_StartHeartbeat_ContextCancel(t *testing.T) {
	dbName := "railyard_eng_hbcancel"
	srv := setupTestDB(t, dbName)
	gormDB := connectDB(t, srv, dbName)

	eng, err := Register(gormDB, RegisterOpts{Track: "backend"})
	if err != nil {
		t.Fatalf("Register: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	errCh := StartHeartbeat(ctx, gormDB, eng.ID, 100*time.Millisecond)

	// Cancel immediately.
	cancel()

	// Give goroutine time to exit.
	time.Sleep(200 * time.Millisecond)

	// Should not have sent an error (context cancellation is not an error).
	select {
	case err := <-errCh:
		t.Errorf("unexpected error after cancel: %v", err)
	default:
	}
}

// --- ClaimBead tests ---

func TestIntegration_ClaimBead(t *testing.T) {
	dbName := "railyard_eng_claim"
	srv := setupTestDB(t, dbName)
	gormDB := connectDB(t, srv, dbName)

	// Create an engine.
	eng, err := Register(gormDB, RegisterOpts{Track: "backend"})
	if err != nil {
		t.Fatalf("Register: %v", err)
	}

	// Create a bead.
	b, err := bead.Create(gormDB, bead.CreateOpts{
		Title:        "Claimable bead",
		Track:        "backend",
		Priority:     2,
		BranchPrefix: "ry/test",
	})
	if err != nil {
		t.Fatalf("bead.Create: %v", err)
	}

	// Claim it.
	claimed, err := ClaimBead(gormDB, eng.ID, "backend")
	if err != nil {
		t.Fatalf("ClaimBead: %v", err)
	}

	if claimed.ID != b.ID {
		t.Errorf("claimed ID = %q, want %q", claimed.ID, b.ID)
	}
	if claimed.Status != "claimed" {
		t.Errorf("claimed Status = %q, want %q", claimed.Status, "claimed")
	}
	if claimed.Assignee != eng.ID {
		t.Errorf("claimed Assignee = %q, want %q", claimed.Assignee, eng.ID)
	}
	if claimed.ClaimedAt == nil {
		t.Error("ClaimedAt should be set")
	}

	// Verify engine was updated.
	gotEng, err := Get(gormDB, eng.ID)
	if err != nil {
		t.Fatalf("Get engine: %v", err)
	}
	if gotEng.Status != StatusWorking {
		t.Errorf("engine Status = %q, want %q", gotEng.Status, StatusWorking)
	}
	if gotEng.CurrentBead != b.ID {
		t.Errorf("engine CurrentBead = %q, want %q", gotEng.CurrentBead, b.ID)
	}
}

func TestIntegration_ClaimBead_NoReadyBeads(t *testing.T) {
	dbName := "railyard_eng_claimnone"
	srv := setupTestDB(t, dbName)
	gormDB := connectDB(t, srv, dbName)

	eng, err := Register(gormDB, RegisterOpts{Track: "backend"})
	if err != nil {
		t.Fatalf("Register: %v", err)
	}

	_, err = ClaimBead(gormDB, eng.ID, "backend")
	if err == nil {
		t.Fatal("expected error when no beads available")
	}
	if !strings.Contains(err.Error(), "no ready beads") {
		t.Errorf("error = %q, want to contain %q", err.Error(), "no ready beads")
	}
}

func TestIntegration_ClaimBead_PriorityOrder(t *testing.T) {
	dbName := "railyard_eng_claimpri"
	srv := setupTestDB(t, dbName)
	gormDB := connectDB(t, srv, dbName)

	eng, err := Register(gormDB, RegisterOpts{Track: "backend"})
	if err != nil {
		t.Fatalf("Register: %v", err)
	}

	// Create beads with different priorities.
	lowPri, err := bead.Create(gormDB, bead.CreateOpts{
		Title:        "Low priority",
		Track:        "backend",
		Priority:     4,
		BranchPrefix: "ry/test",
	})
	if err != nil {
		t.Fatalf("bead.Create low: %v", err)
	}

	highPri, err := bead.Create(gormDB, bead.CreateOpts{
		Title:        "High priority",
		Track:        "backend",
		Priority:     1,
		BranchPrefix: "ry/test",
	})
	if err != nil {
		t.Fatalf("bead.Create high: %v", err)
	}
	_ = lowPri

	// Should claim the higher-priority bead first.
	claimed, err := ClaimBead(gormDB, eng.ID, "backend")
	if err != nil {
		t.Fatalf("ClaimBead: %v", err)
	}
	if claimed.ID != highPri.ID {
		t.Errorf("claimed ID = %q, want highest priority %q", claimed.ID, highPri.ID)
	}
}

func TestIntegration_ClaimBead_SkipsBlocked(t *testing.T) {
	dbName := "railyard_eng_claimblk"
	srv := setupTestDB(t, dbName)
	gormDB := connectDB(t, srv, dbName)

	eng, err := Register(gormDB, RegisterOpts{Track: "backend"})
	if err != nil {
		t.Fatalf("Register: %v", err)
	}

	// Create a blocker bead (open, so it blocks).
	blocker, err := bead.Create(gormDB, bead.CreateOpts{
		Title:        "Blocker",
		Track:        "backend",
		Priority:     2,
		BranchPrefix: "ry/test",
	})
	if err != nil {
		t.Fatalf("bead.Create blocker: %v", err)
	}

	// Create a blocked bead (depends on blocker).
	blocked, err := bead.Create(gormDB, bead.CreateOpts{
		Title:        "Blocked bead",
		Track:        "backend",
		Priority:     1, // higher priority, but blocked
		BranchPrefix: "ry/test",
	})
	if err != nil {
		t.Fatalf("bead.Create blocked: %v", err)
	}

	if err := bead.AddDep(gormDB, blocked.ID, blocker.ID, "blocks"); err != nil {
		t.Fatalf("AddDep: %v", err)
	}

	// Create an unblocked bead.
	unblocked, err := bead.Create(gormDB, bead.CreateOpts{
		Title:        "Unblocked bead",
		Track:        "backend",
		Priority:     3,
		BranchPrefix: "ry/test",
	})
	if err != nil {
		t.Fatalf("bead.Create unblocked: %v", err)
	}

	// Should claim the unblocked bead, not the blocked one (even though blocked has higher priority).
	// The blocker itself is also claimable since it has no deps.
	claimed, err := ClaimBead(gormDB, eng.ID, "backend")
	if err != nil {
		t.Fatalf("ClaimBead: %v", err)
	}
	if claimed.ID == blocked.ID {
		t.Errorf("should not have claimed blocked bead %q", blocked.ID)
	}
	// Should be blocker (priority 2) since it's unblocked and higher priority than unblocked (3).
	if claimed.ID != blocker.ID {
		t.Errorf("claimed ID = %q, want blocker %q (priority 2, unblocked); unblocked = %q", claimed.ID, blocker.ID, unblocked.ID)
	}
}

func TestIntegration_ClaimBead_BlockerDone(t *testing.T) {
	dbName := "railyard_eng_claimbd"
	srv := setupTestDB(t, dbName)
	gormDB := connectDB(t, srv, dbName)

	eng, err := Register(gormDB, RegisterOpts{Track: "backend"})
	if err != nil {
		t.Fatalf("Register: %v", err)
	}

	// Create and complete a blocker.
	blocker, err := bead.Create(gormDB, bead.CreateOpts{
		Title:        "Blocker",
		Track:        "backend",
		BranchPrefix: "ry/test",
	})
	if err != nil {
		t.Fatalf("bead.Create blocker: %v", err)
	}

	// Transition blocker to done.
	for _, status := range []string{"ready", "claimed", "in_progress", "done"} {
		updates := map[string]interface{}{"status": status}
		if status == "claimed" {
			updates["assignee"] = "other-engine"
		}
		if err := bead.Update(gormDB, blocker.ID, updates); err != nil {
			t.Fatalf("bead.Update blocker %s: %v", status, err)
		}
	}

	// Create a bead that depends on the now-done blocker.
	dependent, err := bead.Create(gormDB, bead.CreateOpts{
		Title:        "Dependent bead",
		Track:        "backend",
		Priority:     1,
		BranchPrefix: "ry/test",
	})
	if err != nil {
		t.Fatalf("bead.Create dependent: %v", err)
	}

	if err := bead.AddDep(gormDB, dependent.ID, blocker.ID, "blocks"); err != nil {
		t.Fatalf("AddDep: %v", err)
	}

	// Since blocker is done, dependent should be claimable.
	claimed, err := ClaimBead(gormDB, eng.ID, "backend")
	if err != nil {
		t.Fatalf("ClaimBead: %v", err)
	}
	if claimed.ID != dependent.ID {
		t.Errorf("claimed ID = %q, want %q (blocker is done)", claimed.ID, dependent.ID)
	}
}

func TestIntegration_ClaimBead_AlreadyAssigned(t *testing.T) {
	dbName := "railyard_eng_claimas"
	srv := setupTestDB(t, dbName)
	gormDB := connectDB(t, srv, dbName)

	eng, err := Register(gormDB, RegisterOpts{Track: "backend"})
	if err != nil {
		t.Fatalf("Register: %v", err)
	}

	// Create a bead that is already assigned.
	b, err := bead.Create(gormDB, bead.CreateOpts{
		Title:        "Already assigned",
		Track:        "backend",
		BranchPrefix: "ry/test",
	})
	if err != nil {
		t.Fatalf("bead.Create: %v", err)
	}
	if err := bead.Update(gormDB, b.ID, map[string]interface{}{"status": "ready"}); err != nil {
		t.Fatalf("bead.Update ready: %v", err)
	}
	if err := bead.Update(gormDB, b.ID, map[string]interface{}{
		"status":   "claimed",
		"assignee": "other-engine",
	}); err != nil {
		t.Fatalf("bead.Update claimed: %v", err)
	}

	// No open unassigned beads should be found.
	_, err = ClaimBead(gormDB, eng.ID, "backend")
	if err == nil {
		t.Fatal("expected error when all beads assigned")
	}
	if !strings.Contains(err.Error(), "no ready beads") {
		t.Errorf("error = %q, want to contain %q", err.Error(), "no ready beads")
	}
}

func TestIntegration_ClaimBead_TrackFilter(t *testing.T) {
	dbName := "railyard_eng_claimtr"
	srv := setupTestDB(t, dbName)
	gormDB := connectDB(t, srv, dbName)

	eng, err := Register(gormDB, RegisterOpts{Track: "backend"})
	if err != nil {
		t.Fatalf("Register: %v", err)
	}

	// Create a bead on a different track.
	_, err = bead.Create(gormDB, bead.CreateOpts{
		Title:        "Frontend bead",
		Track:        "frontend",
		BranchPrefix: "ry/test",
	})
	if err != nil {
		t.Fatalf("bead.Create frontend: %v", err)
	}

	// Create a bead on the engine's track.
	backendBead, err := bead.Create(gormDB, bead.CreateOpts{
		Title:        "Backend bead",
		Track:        "backend",
		BranchPrefix: "ry/test",
	})
	if err != nil {
		t.Fatalf("bead.Create backend: %v", err)
	}

	// Should only claim the backend bead.
	claimed, err := ClaimBead(gormDB, eng.ID, "backend")
	if err != nil {
		t.Fatalf("ClaimBead: %v", err)
	}
	if claimed.ID != backendBead.ID {
		t.Errorf("claimed ID = %q, want backend bead %q", claimed.ID, backendBead.ID)
	}
}

func TestIntegration_ClaimBead_ValidationError(t *testing.T) {
	dbName := "railyard_eng_claimval"
	srv := setupTestDB(t, dbName)
	gormDB := connectDB(t, srv, dbName)

	tests := []struct {
		name     string
		engineID string
		track    string
		wantErr  string
	}{
		{
			name:     "missing engineID",
			engineID: "",
			track:    "backend",
			wantErr:  "engineID is required",
		},
		{
			name:     "missing track",
			engineID: "eng-12345",
			track:    "",
			wantErr:  "track is required",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ClaimBead(gormDB, tt.engineID, tt.track)
			if err == nil {
				t.Fatal("expected error")
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("error = %q, want to contain %q", err.Error(), tt.wantErr)
			}
		})
	}
}

func TestIntegration_ClaimBead_DBError(t *testing.T) {
	gormDB := closedGormDB(t)
	_, err := ClaimBead(gormDB, "eng-12345", "backend")
	if err == nil {
		t.Fatal("expected error from ClaimBead with closed DB")
	}
}

// --- SpawnAgent tests ---

func TestIntegration_SpawnAgent(t *testing.T) {
	dbName := "railyard_eng_spawn"
	srv := setupTestDB(t, dbName)
	gormDB := connectDB(t, srv, dbName)

	// Register an engine.
	eng, err := Register(gormDB, RegisterOpts{Track: "backend"})
	if err != nil {
		t.Fatalf("Register: %v", err)
	}

	// Create a mock binary that writes known output.
	dir := t.TempDir()
	mockScript := `#!/bin/sh
echo "stdout line 1"
echo "stdout line 2"
echo "stderr output" >&2
`
	mockPath := filepath.Join(dir, "mock-claude")
	if err := os.WriteFile(mockPath, []byte(mockScript), 0755); err != nil {
		t.Fatalf("write mock binary: %v", err)
	}

	sess, err := SpawnAgent(context.Background(), gormDB, SpawnOpts{
		EngineID:       eng.ID,
		BeadID:         "bead-integ1",
		ContextPayload: "integration test context",
		WorkDir:        dir,
		ClaudeBinary:   mockPath,
	})
	if err != nil {
		t.Fatalf("SpawnAgent: %v", err)
	}

	if err := sess.Wait(); err != nil {
		t.Fatalf("Wait: %v", err)
	}

	// Give a moment for final flushes to settle.
	time.Sleep(100 * time.Millisecond)

	// Verify agent_logs rows were created.
	var logs []models.AgentLog
	if err := gormDB.Where("session_id = ?", sess.ID).Find(&logs).Error; err != nil {
		t.Fatalf("query agent_logs: %v", err)
	}

	if len(logs) == 0 {
		t.Fatal("expected agent_log rows, got 0")
	}

	// Check we have both stdout and stderr entries.
	var hasOut, hasErr bool
	for _, log := range logs {
		if log.EngineID != eng.ID {
			t.Errorf("log.EngineID = %q, want %q", log.EngineID, eng.ID)
		}
		if log.SessionID != sess.ID {
			t.Errorf("log.SessionID = %q, want %q", log.SessionID, sess.ID)
		}
		if log.BeadID != "bead-integ1" {
			t.Errorf("log.BeadID = %q, want %q", log.BeadID, "bead-integ1")
		}
		switch log.Direction {
		case "out":
			hasOut = true
			if !strings.Contains(log.Content, "stdout line 1") {
				t.Errorf("stdout log content = %q, want to contain %q", log.Content, "stdout line 1")
			}
		case "err":
			hasErr = true
			if !strings.Contains(log.Content, "stderr output") {
				t.Errorf("stderr log content = %q, want to contain %q", log.Content, "stderr output")
			}
		}
	}

	if !hasOut {
		t.Error("no stdout log entry found")
	}
	if !hasErr {
		t.Error("no stderr log entry found")
	}

	// Verify engine.session_id was updated.
	gotEng, err := Get(gormDB, eng.ID)
	if err != nil {
		t.Fatalf("Get engine: %v", err)
	}
	if gotEng.SessionID != sess.ID {
		t.Errorf("engine.SessionID = %q, want %q", gotEng.SessionID, sess.ID)
	}
}
