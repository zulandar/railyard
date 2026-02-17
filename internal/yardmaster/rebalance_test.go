package yardmaster

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/zulandar/railyard/internal/config"
	"github.com/zulandar/railyard/internal/engine"
	"github.com/zulandar/railyard/internal/models"
	"github.com/zulandar/railyard/internal/orchestration"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// testDB creates an in-memory SQLite database with all required tables.
func testDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	if err := db.AutoMigrate(
		&models.Engine{},
		&models.Car{},
		&models.CarDep{},
		&models.Message{},
		&models.BroadcastAck{},
		&models.Track{},
	); err != nil {
		t.Fatalf("migrate test db: %v", err)
	}
	return db
}

// mockTmux is a test double for orchestration.Tmux.
type mockTmux struct {
	sessionExists bool
	panesCreated  int
	sentKeys      []string
}

func (m *mockTmux) SessionExists(name string) bool              { return m.sessionExists }
func (m *mockTmux) CreateSession(name string) error             { return nil }
func (m *mockTmux) NewPane(session string) (string, error)      { m.panesCreated++; return "%mock", nil }
func (m *mockTmux) SendKeys(paneID, keys string) error          { m.sentKeys = append(m.sentKeys, keys); return nil }
func (m *mockTmux) SendSignal(paneID, signal string) error      { return nil }
func (m *mockTmux) KillPane(paneID string) error                { return nil }
func (m *mockTmux) KillSession(name string) error               { return nil }
func (m *mockTmux) ListPanes(session string) ([]string, error)  { return nil, nil }
func (m *mockTmux) TileLayout(session string) error             { return nil }

func twoTrackConfig() *config.Config {
	return &config.Config{
		Owner: "test",
		Repo:  "git@github.com:test/test.git",
		Tracks: []config.TrackConfig{
			{Name: "backend", Language: "go", EngineSlots: 5},
			{Name: "frontend", Language: "ts", EngineSlots: 5},
		},
	}
}

func newState(tmux orchestration.Tmux) *rebalanceState {
	return &rebalanceState{
		lastTrackMoveAt: make(map[string]time.Time),
		tmux:            tmux,
	}
}

func TestRebalanceEngines_CooldownSkip(t *testing.T) {
	db := testDB(t)
	cfg := twoTrackConfig()
	tmux := &mockTmux{sessionExists: true}
	state := newState(tmux)
	state.lastRebalanceAt = time.Now() // just rebalanced

	var buf bytes.Buffer
	if err := rebalanceEngines(db, cfg, "test.yaml", state, &buf); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Should skip — no output.
	if buf.Len() != 0 {
		t.Errorf("expected no output during cooldown, got: %s", buf.String())
	}
	if tmux.panesCreated != 0 {
		t.Errorf("expected no panes created during cooldown, got %d", tmux.panesCreated)
	}
}

func TestRebalanceEngines_NoSurplusNoDeficit(t *testing.T) {
	db := testDB(t)
	cfg := twoTrackConfig()
	tmux := &mockTmux{sessionExists: true}
	state := newState(tmux)
	now := time.Now()

	// Both engines are working — no surplus.
	db.Create(&models.Engine{ID: "eng-1", Track: "backend", Status: engine.StatusWorking, CurrentCar: "car-1", StartedAt: now, LastActivity: now})
	db.Create(&models.Engine{ID: "eng-2", Track: "frontend", Status: engine.StatusWorking, CurrentCar: "car-2", StartedAt: now, LastActivity: now})

	var buf bytes.Buffer
	if err := rebalanceEngines(db, cfg, "test.yaml", state, &buf); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if tmux.panesCreated != 0 {
		t.Errorf("expected no panes created, got %d", tmux.panesCreated)
	}
}

func TestRebalanceEngines_SingleMove(t *testing.T) {
	db := testDB(t)
	cfg := twoTrackConfig()
	tmux := &mockTmux{sessionExists: true}
	state := newState(tmux)
	now := time.Now()
	idle := now.Add(-3 * time.Minute) // idle for 3 min (> 2 min threshold)

	// Backend: 2 idle engines, no work.
	db.Create(&models.Engine{ID: "eng-b1", Track: "backend", Status: engine.StatusIdle, CurrentCar: "", StartedAt: now.Add(-10 * time.Minute), LastActivity: idle})
	db.Create(&models.Engine{ID: "eng-b2", Track: "backend", Status: engine.StatusIdle, CurrentCar: "", StartedAt: now.Add(-5 * time.Minute), LastActivity: idle})

	// Frontend: 1 engine working, 2 ready cars.
	db.Create(&models.Engine{ID: "eng-f1", Track: "frontend", Status: engine.StatusWorking, CurrentCar: "car-f1", StartedAt: now, LastActivity: now})
	db.Create(&models.Car{ID: "car-f1", Track: "frontend", Status: "in_progress", Assignee: "eng-f1", Type: "task"})
	db.Create(&models.Car{ID: "car-f2", Track: "frontend", Status: "open", Assignee: "", Type: "task"})
	db.Create(&models.Car{ID: "car-f3", Track: "frontend", Status: "open", Assignee: "", Type: "task"})

	var buf bytes.Buffer
	if err := rebalanceEngines(db, cfg, "test.yaml", state, &buf); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should have moved 1 engine: backend → frontend.
	if !strings.Contains(buf.String(), "Rebalanced 1 engine: backend → frontend") {
		t.Errorf("output = %q, want rebalance message", buf.String())
	}

	// One idle backend engine should be dead.
	var deadCount int64
	db.Model(&models.Engine{}).Where("track = ? AND status = ?", "backend", engine.StatusDead).Count(&deadCount)
	if deadCount != 1 {
		t.Errorf("dead backend engines = %d, want 1", deadCount)
	}

	// Scale should have been called (1 pane created).
	if tmux.panesCreated != 1 {
		t.Errorf("panes created = %d, want 1", tmux.panesCreated)
	}
}

func TestRebalanceEngines_RespectsEngineSlots(t *testing.T) {
	db := testDB(t)
	cfg := &config.Config{
		Owner: "test",
		Repo:  "git@github.com:test/test.git",
		Tracks: []config.TrackConfig{
			{Name: "backend", Language: "go", EngineSlots: 5},
			{Name: "frontend", Language: "ts", EngineSlots: 2}, // max 2
		},
	}
	tmux := &mockTmux{sessionExists: true}
	state := newState(tmux)
	now := time.Now()
	idle := now.Add(-3 * time.Minute)

	// Backend: idle engine, no work.
	db.Create(&models.Engine{ID: "eng-b1", Track: "backend", Status: engine.StatusIdle, CurrentCar: "", StartedAt: now, LastActivity: idle})

	// Frontend: already at max slots (2 engines), has ready work.
	db.Create(&models.Engine{ID: "eng-f1", Track: "frontend", Status: engine.StatusWorking, CurrentCar: "car-1", StartedAt: now, LastActivity: now})
	db.Create(&models.Engine{ID: "eng-f2", Track: "frontend", Status: engine.StatusWorking, CurrentCar: "car-2", StartedAt: now, LastActivity: now})
	db.Create(&models.Car{ID: "car-1", Track: "frontend", Status: "in_progress", Assignee: "eng-f1", Type: "task"})
	db.Create(&models.Car{ID: "car-2", Track: "frontend", Status: "in_progress", Assignee: "eng-f2", Type: "task"})
	db.Create(&models.Car{ID: "car-3", Track: "frontend", Status: "open", Assignee: "", Type: "task"})

	var buf bytes.Buffer
	if err := rebalanceEngines(db, cfg, "test.yaml", state, &buf); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Frontend is at max slots (2 live == 2 max), so it's not a deficit track.
	if tmux.panesCreated != 0 {
		t.Errorf("panes created = %d, want 0 (at engine_slots cap)", tmux.panesCreated)
	}
}

func TestRebalanceEngines_IdleThresholdNotMet(t *testing.T) {
	db := testDB(t)
	cfg := twoTrackConfig()
	tmux := &mockTmux{sessionExists: true}
	state := newState(tmux)
	now := time.Now()

	// Backend: engine just went idle (< 2 min ago).
	db.Create(&models.Engine{ID: "eng-b1", Track: "backend", Status: engine.StatusIdle, CurrentCar: "", StartedAt: now, LastActivity: now.Add(-30 * time.Second)})

	// Frontend: has backlog.
	db.Create(&models.Engine{ID: "eng-f1", Track: "frontend", Status: engine.StatusWorking, CurrentCar: "car-1", StartedAt: now, LastActivity: now})
	db.Create(&models.Car{ID: "car-1", Track: "frontend", Status: "in_progress", Assignee: "eng-f1", Type: "task"})
	db.Create(&models.Car{ID: "car-2", Track: "frontend", Status: "open", Assignee: "", Type: "task"})

	var buf bytes.Buffer
	if err := rebalanceEngines(db, cfg, "test.yaml", state, &buf); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Engine idle < 2 min → not counted as surplus.
	if tmux.panesCreated != 0 {
		t.Errorf("panes created = %d, want 0 (idle threshold not met)", tmux.panesCreated)
	}
}

func TestRebalanceEngines_TrackCooldown(t *testing.T) {
	db := testDB(t)
	cfg := twoTrackConfig()
	tmux := &mockTmux{sessionExists: true}
	state := newState(tmux)
	now := time.Now()
	idle := now.Add(-3 * time.Minute)

	// Mark backend as recently rebalanced.
	state.lastTrackMoveAt["backend"] = now.Add(-2 * time.Minute) // 2 min ago (< 5 min cooldown)

	// Backend: idle, no work.
	db.Create(&models.Engine{ID: "eng-b1", Track: "backend", Status: engine.StatusIdle, CurrentCar: "", StartedAt: now, LastActivity: idle})

	// Frontend: has backlog.
	db.Create(&models.Engine{ID: "eng-f1", Track: "frontend", Status: engine.StatusWorking, CurrentCar: "car-1", StartedAt: now, LastActivity: now})
	db.Create(&models.Car{ID: "car-1", Track: "frontend", Status: "in_progress", Assignee: "eng-f1", Type: "task"})
	db.Create(&models.Car{ID: "car-2", Track: "frontend", Status: "open", Assignee: "", Type: "task"})

	var buf bytes.Buffer
	if err := rebalanceEngines(db, cfg, "test.yaml", state, &buf); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Backend donor is on cooldown — should skip.
	if tmux.panesCreated != 0 {
		t.Errorf("panes created = %d, want 0 (track on cooldown)", tmux.panesCreated)
	}
}

func TestCountReadyWork(t *testing.T) {
	db := testDB(t)

	// Open, unassigned, non-epic, on backend → ready.
	db.Create(&models.Car{ID: "c1", Track: "backend", Status: "open", Type: "task"})
	db.Create(&models.Car{ID: "c2", Track: "backend", Status: "open", Type: "bug"})

	// Epic → excluded.
	db.Create(&models.Car{ID: "c3", Track: "backend", Status: "open", Type: "epic"})

	// Assigned → excluded.
	db.Create(&models.Car{ID: "c4", Track: "backend", Status: "open", Assignee: "eng-1", Type: "task"})

	// Wrong status → excluded.
	db.Create(&models.Car{ID: "c5", Track: "backend", Status: "done", Type: "task"})

	// Wrong track → excluded.
	db.Create(&models.Car{ID: "c6", Track: "frontend", Status: "open", Type: "task"})

	// Blocked by a non-done car → excluded.
	db.Create(&models.Car{ID: "c7", Track: "backend", Status: "open", Type: "task"})
	db.Create(&models.Car{ID: "blocker", Track: "frontend", Status: "open", Type: "task"})
	db.Create(&models.CarDep{CarID: "c7", BlockedBy: "blocker"})

	// Blocked by a done car → NOT excluded (blocker resolved).
	db.Create(&models.Car{ID: "c8", Track: "backend", Status: "open", Type: "task"})
	db.Create(&models.Car{ID: "done-blocker", Track: "backend", Status: "done", Type: "task"})
	db.Create(&models.CarDep{CarID: "c8", BlockedBy: "done-blocker"})

	count, err := countReadyWork(db, "backend")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// c1, c2, c8 are ready. c3 (epic), c4 (assigned), c5 (done), c7 (blocked) excluded.
	if count != 3 {
		t.Errorf("ready work = %d, want 3", count)
	}
}

func TestCountReadyWork_EmptyTrack(t *testing.T) {
	db := testDB(t)

	count, err := countReadyWork(db, "nonexistent")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if count != 0 {
		t.Errorf("ready work = %d, want 0", count)
	}
}

func TestRebalanceMove_NoIdleEngine(t *testing.T) {
	db := testDB(t)
	cfg := twoTrackConfig()
	tmux := &mockTmux{sessionExists: true}
	state := newState(tmux)

	// No engines at all on backend.
	var buf bytes.Buffer
	err := rebalanceMove(db, cfg, "test.yaml", "backend", "frontend", state, &buf)
	if err == nil {
		t.Fatal("expected error for no idle engine on donor")
	}
	if !strings.Contains(err.Error(), "no idle engine on donor track") {
		t.Errorf("error = %q, want to contain 'no idle engine on donor track'", err.Error())
	}
}

func TestRebalanceMove_Success(t *testing.T) {
	db := testDB(t)
	cfg := twoTrackConfig()
	tmux := &mockTmux{sessionExists: true}
	state := newState(tmux)
	now := time.Now()
	idle := now.Add(-3 * time.Minute)

	// Idle engine on backend.
	db.Create(&models.Engine{ID: "eng-b1", Track: "backend", Status: engine.StatusIdle, CurrentCar: "", StartedAt: now, LastActivity: idle})
	// One engine on frontend.
	db.Create(&models.Engine{ID: "eng-f1", Track: "frontend", Status: engine.StatusWorking, CurrentCar: "car-1", StartedAt: now, LastActivity: now})

	var buf bytes.Buffer
	err := rebalanceMove(db, cfg, "test.yaml", "backend", "frontend", state, &buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Backend engine should be dead.
	var eng models.Engine
	db.Where("id = ?", "eng-b1").First(&eng)
	if eng.Status != engine.StatusDead {
		t.Errorf("donor engine status = %q, want dead", eng.Status)
	}

	// Scale called on frontend (1 pane created: from 1 → 2).
	if tmux.panesCreated != 1 {
		t.Errorf("panes created = %d, want 1", tmux.panesCreated)
	}
	if len(tmux.sentKeys) != 1 {
		t.Errorf("sent keys = %d, want 1", len(tmux.sentKeys))
	}
	if len(tmux.sentKeys) > 0 && !strings.Contains(tmux.sentKeys[0], "--track frontend") {
		t.Errorf("sent keys = %q, want to contain '--track frontend'", tmux.sentKeys[0])
	}
}

func TestRebalanceEngines_YardmasterExcluded(t *testing.T) {
	db := testDB(t)
	cfg := twoTrackConfig()
	tmux := &mockTmux{sessionExists: true}
	state := newState(tmux)
	now := time.Now()
	idle := now.Add(-3 * time.Minute)

	// Register yardmaster as idle on backend's track ("*" but let's test with backend).
	db.Create(&models.Engine{ID: YardmasterID, Track: "backend", Status: engine.StatusIdle, CurrentCar: "", StartedAt: now, LastActivity: idle})

	// Frontend has backlog.
	db.Create(&models.Engine{ID: "eng-f1", Track: "frontend", Status: engine.StatusWorking, CurrentCar: "car-1", StartedAt: now, LastActivity: now})
	db.Create(&models.Car{ID: "car-1", Track: "frontend", Status: "in_progress", Assignee: "eng-f1", Type: "task"})
	db.Create(&models.Car{ID: "car-2", Track: "frontend", Status: "open", Assignee: "", Type: "task"})

	var buf bytes.Buffer
	if err := rebalanceEngines(db, cfg, "test.yaml", state, &buf); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Yardmaster should NOT be moved — no panes created.
	if tmux.panesCreated != 0 {
		t.Errorf("panes created = %d, want 0 (yardmaster should be excluded)", tmux.panesCreated)
	}
}
