package orchestration

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/zulandar/railyard/internal/config"
	"github.com/zulandar/railyard/internal/models"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// ---------------------------------------------------------------------------
// mockTmux — test double for the Tmux interface
// ---------------------------------------------------------------------------

type mockTmux struct {
	sessionExists   bool
	createErr       error
	sendKeysErr     error
	sendSignalErr   error
	killSessionErr  error
	listSessions    []string
	listSessionsErr error

	// Per-call overrides (take precedence over flat fields above).
	sessionExistsFunc func(name string) bool
	createSessionFunc func(name string) error
	sendKeysFunc      func(session, keys string) error
	listSessionsFunc  func(prefix string) ([]string, error)

	// Recording.
	createdSessions []string
	sentKeys        []string
	sentSignals     []string
	killedSessions  []string
}

func (m *mockTmux) SessionExists(name string) bool {
	if m.sessionExistsFunc != nil {
		return m.sessionExistsFunc(name)
	}
	return m.sessionExists
}
func (m *mockTmux) CreateSession(name string) error {
	if m.createSessionFunc != nil {
		return m.createSessionFunc(name)
	}
	if m.createErr != nil {
		return m.createErr
	}
	m.createdSessions = append(m.createdSessions, name)
	return nil
}
func (m *mockTmux) SendKeys(session, keys string) error {
	if m.sendKeysFunc != nil {
		return m.sendKeysFunc(session, keys)
	}
	if m.sendKeysErr != nil {
		return m.sendKeysErr
	}
	m.sentKeys = append(m.sentKeys, keys)
	return nil
}
func (m *mockTmux) SendSignal(session, signal string) error {
	m.sentSignals = append(m.sentSignals, signal)
	return m.sendSignalErr
}
func (m *mockTmux) KillSession(name string) error {
	m.killedSessions = append(m.killedSessions, name)
	return m.killSessionErr
}
func (m *mockTmux) ListSessions(prefix string) ([]string, error) {
	if m.listSessionsFunc != nil {
		return m.listSessionsFunc(prefix)
	}
	if m.listSessionsErr != nil {
		return nil, m.listSessionsErr
	}
	// Filter mock sessions by prefix.
	var result []string
	for _, s := range m.listSessions {
		if strings.HasPrefix(s, prefix) {
			result = append(result, s)
		}
	}
	return result, nil
}

// ---------------------------------------------------------------------------
// testDB — helper to create an in-memory SQLite database with all tables
// ---------------------------------------------------------------------------

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
		&models.Track{},
		&models.Car{},
		&models.CarDep{},
		&models.Message{},
	); err != nil {
		t.Fatalf("migrate test db: %v", err)
	}
	return db
}

// testConfig returns a minimal config for testing with the given owner.
func testConfig(owner string, tracks ...config.TrackConfig) *config.Config {
	return &config.Config{
		Owner:  owner,
		Tracks: tracks,
	}
}

// ---------------------------------------------------------------------------
// AssignTracks tests (unchanged)
// ---------------------------------------------------------------------------

func TestAssignTracks_Proportional(t *testing.T) {
	cfg := &config.Config{
		Tracks: []config.TrackConfig{
			{Name: "backend", EngineSlots: 5},
			{Name: "frontend", EngineSlots: 3},
			{Name: "infra", EngineSlots: 2},
		},
	}

	result := AssignTracks(cfg, 10)
	total := 0
	for _, v := range result {
		total += v
	}
	if total != 10 {
		t.Errorf("total = %d, want 10", total)
	}
	if result["backend"] != 5 {
		t.Errorf("backend = %d, want 5", result["backend"])
	}
	if result["frontend"] != 3 {
		t.Errorf("frontend = %d, want 3", result["frontend"])
	}
	if result["infra"] != 2 {
		t.Errorf("infra = %d, want 2", result["infra"])
	}
}

func TestAssignTracks_FewerEnginesThanTracks(t *testing.T) {
	cfg := &config.Config{
		Tracks: []config.TrackConfig{
			{Name: "backend", EngineSlots: 5},
			{Name: "frontend", EngineSlots: 3},
			{Name: "infra", EngineSlots: 2},
		},
	}

	result := AssignTracks(cfg, 2)
	total := 0
	for _, v := range result {
		total += v
	}
	if total != 2 {
		t.Errorf("total = %d, want 2", total)
	}
	if result["backend"] != 1 {
		t.Errorf("backend = %d, want 1", result["backend"])
	}
	if result["frontend"] != 1 {
		t.Errorf("frontend = %d, want 1", result["frontend"])
	}
}

func TestAssignTracks_SingleEngine(t *testing.T) {
	cfg := &config.Config{
		Tracks: []config.TrackConfig{
			{Name: "backend", EngineSlots: 5},
			{Name: "frontend", EngineSlots: 3},
		},
	}

	result := AssignTracks(cfg, 1)
	total := 0
	for _, v := range result {
		total += v
	}
	if total != 1 {
		t.Errorf("total = %d, want 1", total)
	}
}

func TestAssignTracks_EqualSlots(t *testing.T) {
	cfg := &config.Config{
		Tracks: []config.TrackConfig{
			{Name: "a", EngineSlots: 3},
			{Name: "b", EngineSlots: 3},
		},
	}

	result := AssignTracks(cfg, 6)
	if result["a"] != 3 {
		t.Errorf("a = %d, want 3", result["a"])
	}
	if result["b"] != 3 {
		t.Errorf("b = %d, want 3", result["b"])
	}
}

func TestAssignTracks_ManyEngines(t *testing.T) {
	cfg := &config.Config{
		Tracks: []config.TrackConfig{
			{Name: "backend", EngineSlots: 5},
			{Name: "frontend", EngineSlots: 3},
			{Name: "infra", EngineSlots: 2},
		},
	}

	result := AssignTracks(cfg, 100)
	total := 0
	for _, v := range result {
		total += v
	}
	if total != 100 {
		t.Errorf("total = %d, want 100", total)
	}
	if result["backend"] != 50 {
		t.Errorf("backend = %d, want 50", result["backend"])
	}
	if result["frontend"] != 30 {
		t.Errorf("frontend = %d, want 30", result["frontend"])
	}
	if result["infra"] != 20 {
		t.Errorf("infra = %d, want 20", result["infra"])
	}
}

func TestAssignTracks_NilConfig(t *testing.T) {
	result := AssignTracks(nil, 5)
	if len(result) != 0 {
		t.Errorf("expected empty result for nil config, got %v", result)
	}
}

func TestAssignTracks_NoTracks(t *testing.T) {
	cfg := &config.Config{}
	result := AssignTracks(cfg, 5)
	if len(result) != 0 {
		t.Errorf("expected empty result for no tracks, got %v", result)
	}
}

func TestAssignTracks_ZeroEngines(t *testing.T) {
	cfg := &config.Config{
		Tracks: []config.TrackConfig{{Name: "a", EngineSlots: 3}},
	}
	result := AssignTracks(cfg, 0)
	if len(result) != 0 {
		t.Errorf("expected empty result for 0 engines, got %v", result)
	}
}

func TestAssignTracks_SingleTrack(t *testing.T) {
	cfg := &config.Config{
		Tracks: []config.TrackConfig{{Name: "backend", EngineSlots: 5}},
	}
	result := AssignTracks(cfg, 3)
	if result["backend"] != 3 {
		t.Errorf("backend = %d, want 3", result["backend"])
	}
}

func TestAssignTracks_ZeroSlots(t *testing.T) {
	cfg := &config.Config{
		Tracks: []config.TrackConfig{
			{Name: "a", EngineSlots: 0},
			{Name: "b", EngineSlots: 0},
		},
	}
	result := AssignTracks(cfg, 4)
	total := 0
	for _, v := range result {
		total += v
	}
	if total != 4 {
		t.Errorf("total = %d, want 4", total)
	}
}

func TestAssignTracks_OverAssignment(t *testing.T) {
	cfg := &config.Config{
		Tracks: []config.TrackConfig{
			{Name: "big", EngineSlots: 10},
			{Name: "t1", EngineSlots: 1},
			{Name: "t2", EngineSlots: 1},
			{Name: "t3", EngineSlots: 1},
			{Name: "t4", EngineSlots: 1},
		},
	}
	result := AssignTracks(cfg, 5)
	total := 0
	for _, v := range result {
		total += v
	}
	if total != 5 {
		t.Errorf("total = %d, want 5", total)
	}
}

func TestAssignTracks_NegativeEngines(t *testing.T) {
	cfg := &config.Config{
		Tracks: []config.TrackConfig{{Name: "a", EngineSlots: 3}},
	}
	result := AssignTracks(cfg, -1)
	if len(result) != 0 {
		t.Errorf("expected empty result for negative engines, got %v", result)
	}
}

func TestAssignTracks_RemainderDistribution(t *testing.T) {
	cfg := &config.Config{
		Tracks: []config.TrackConfig{
			{Name: "a", EngineSlots: 3},
			{Name: "b", EngineSlots: 1},
		},
	}
	result := AssignTracks(cfg, 5)
	total := 0
	for _, v := range result {
		total += v
	}
	if total != 5 {
		t.Errorf("total = %d, want 5", total)
	}
	if result["a"] != 4 {
		t.Errorf("a = %d, want 4", result["a"])
	}
	if result["b"] != 1 {
		t.Errorf("b = %d, want 1", result["b"])
	}
}

// ---------------------------------------------------------------------------
// Start tests
// ---------------------------------------------------------------------------

func TestStart_NilConfig(t *testing.T) {
	_, err := Start(StartOpts{})
	if err == nil {
		t.Fatal("expected error for nil config")
	}
	if !strings.Contains(err.Error(), "config is required") {
		t.Errorf("error = %q, want to contain 'config is required'", err.Error())
	}
}

func TestStart_MissingConfigPath(t *testing.T) {
	_, err := Start(StartOpts{Config: &config.Config{Tracks: []config.TrackConfig{{Name: "a"}}}})
	if err == nil {
		t.Fatal("expected error for missing config path")
	}
	if !strings.Contains(err.Error(), "config path is required") {
		t.Errorf("error = %q, want to contain 'config path is required'", err.Error())
	}
}

func TestStart_NilDB(t *testing.T) {
	_, err := Start(StartOpts{
		Config:     &config.Config{Tracks: []config.TrackConfig{{Name: "a"}}},
		ConfigPath: "/tmp/test.yaml",
	})
	if err == nil {
		t.Fatal("expected error for nil DB")
	}
	if !strings.Contains(err.Error(), "database connection is required") {
		t.Errorf("error = %q, want to contain 'database connection is required'", err.Error())
	}
}

func TestStart_NoTracks(t *testing.T) {
	_, err := Start(StartOpts{
		Config:     &config.Config{},
		ConfigPath: "/tmp/test.yaml",
	})
	if err == nil {
		t.Fatal("expected error for no tracks")
	}
}

func TestStart_AlreadyRunning(t *testing.T) {
	db := testDB(t)
	m := &mockTmux{sessionExists: true}
	cfg := testConfig("test", config.TrackConfig{Name: "a", EngineSlots: 2})
	_, err := Start(StartOpts{
		Config:     cfg,
		ConfigPath: "/tmp/test.yaml",
		DB:         db,
		Tmux:       m,
	})
	if err == nil {
		t.Fatal("expected error for already running")
	}
	if !strings.Contains(err.Error(), "already running") {
		t.Errorf("error = %q, want to contain 'already running'", err.Error())
	}
}

func TestStart_CreateSessionError(t *testing.T) {
	db := testDB(t)
	m := &mockTmux{
		createErr: fmt.Errorf("tmux not found"),
	}
	cfg := testConfig("test", config.TrackConfig{Name: "a", EngineSlots: 1})
	_, err := Start(StartOpts{
		Config:     cfg,
		ConfigPath: "/tmp/test.yaml",
		DB:         db,
		Tmux:       m,
	})
	if err == nil {
		t.Fatal("expected error for create session failure")
	}
	if !strings.Contains(err.Error(), "tmux not found") {
		t.Errorf("error = %q, want to contain 'tmux not found'", err.Error())
	}
}

func TestStart_Success(t *testing.T) {
	db := testDB(t)
	m := &mockTmux{}
	cfg := testConfig("test", config.TrackConfig{Name: "backend", EngineSlots: 2})
	result, err := Start(StartOpts{
		Config:     cfg,
		ConfigPath: "/tmp/test.yaml",
		DB:         db,
		Tmux:       m,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.YardmasterSession != YardmasterSession("test") {
		t.Errorf("yardmaster session = %q, want %q", result.YardmasterSession, YardmasterSession("test"))
	}
	if result.TelegraphSession != "" {
		t.Errorf("telegraph session = %q, want empty (Telegraph not requested)", result.TelegraphSession)
	}
	if len(result.EngineSessions) != 2 {
		t.Errorf("engine sessions = %d, want 2", len(result.EngineSessions))
	}
	// 1 yardmaster + 2 engines = 3 sessions created.
	if len(m.createdSessions) != 3 {
		t.Errorf("created sessions = %d, want 3", len(m.createdSessions))
	}
	if m.createdSessions[0] != YardmasterSession("test") {
		t.Errorf("first session = %q, want %q", m.createdSessions[0], YardmasterSession("test"))
	}
	// 1 yardmaster + 2 engines = 3 send-keys calls.
	if len(m.sentKeys) != 3 {
		t.Errorf("sent keys = %d, want 3", len(m.sentKeys))
	}
	// Verify yardmaster command was sent first.
	if !strings.Contains(m.sentKeys[0], "ry yardmaster") {
		t.Errorf("first send-keys = %q, want to contain 'ry yardmaster'", m.sentKeys[0])
	}
}

func TestStart_WithTelegraph(t *testing.T) {
	db := testDB(t)
	m := &mockTmux{}
	cfg := testConfig("test", config.TrackConfig{Name: "backend", EngineSlots: 1})
	result, err := Start(StartOpts{
		Config:     cfg,
		ConfigPath: "/tmp/test.yaml",
		DB:         db,
		Telegraph:  true,
		Tmux:       m,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.TelegraphSession == "" {
		t.Error("telegraph session should not be empty when Telegraph=true")
	}
	if result.TelegraphSession != TelegraphSession("test") {
		t.Errorf("telegraph session = %q, want %q", result.TelegraphSession, TelegraphSession("test"))
	}
	// 1 yardmaster + 1 telegraph + 1 engine = 3 sessions, 3 send-keys.
	if len(m.createdSessions) != 3 {
		t.Errorf("created sessions = %d, want 3", len(m.createdSessions))
	}
	if len(m.sentKeys) != 3 {
		t.Errorf("sent keys = %d, want 3", len(m.sentKeys))
	}
	// Verify telegraph command was sent.
	foundTelegraph := false
	for _, k := range m.sentKeys {
		if strings.Contains(k, "ry telegraph start") {
			foundTelegraph = true
		}
	}
	if !foundTelegraph {
		t.Errorf("expected 'ry telegraph start' in sent keys: %v", m.sentKeys)
	}
}

func TestStart_EngineCount_Default(t *testing.T) {
	db := testDB(t)
	m := &mockTmux{}
	cfg := testConfig("test",
		config.TrackConfig{Name: "a", EngineSlots: 3},
		config.TrackConfig{Name: "b", EngineSlots: 2},
	)
	result, err := Start(StartOpts{
		Config:     cfg,
		ConfigPath: "/tmp/test.yaml",
		DB:         db,
		Tmux:       m,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Default engines = 3 + 2 = 5.
	if len(result.EngineSessions) != 5 {
		t.Errorf("engine sessions = %d, want 5", len(result.EngineSessions))
	}
}

func TestStart_EngineCount_Custom(t *testing.T) {
	db := testDB(t)
	m := &mockTmux{}
	cfg := testConfig("test",
		config.TrackConfig{Name: "a", EngineSlots: 3},
		config.TrackConfig{Name: "b", EngineSlots: 2},
	)
	result, err := Start(StartOpts{
		Config:     cfg,
		ConfigPath: "/tmp/test.yaml",
		DB:         db,
		Engines:    3,
		Tmux:       m,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.EngineSessions) != 3 {
		t.Errorf("engine sessions = %d, want 3", len(result.EngineSessions))
	}
}

func TestStart_EngineSessionError(t *testing.T) {
	db := testDB(t)
	callCount := 0
	m := &mockTmux{
		createSessionFunc: func(name string) error {
			callCount++
			// Fail on 2nd call (first engine session; yardmaster is 1st).
			if callCount >= 2 {
				return fmt.Errorf("session create failed")
			}
			return nil
		},
	}
	cfg := testConfig("test", config.TrackConfig{Name: "a", EngineSlots: 1})
	_, err := Start(StartOpts{
		Config:     cfg,
		ConfigPath: "/tmp/test.yaml",
		DB:         db,
		Tmux:       m,
	})
	if err == nil {
		t.Fatal("expected error for engine session creation failure")
	}
	if !strings.Contains(err.Error(), "create engine session") {
		t.Errorf("error = %q, want to contain 'create engine session'", err.Error())
	}
}

func TestStart_YardmasterSendKeysError(t *testing.T) {
	db := testDB(t)
	callCount := 0
	m := &mockTmux{
		sendKeysFunc: func(session, keys string) error {
			callCount++
			if callCount >= 1 {
				return fmt.Errorf("send keys failed")
			}
			return nil
		},
	}
	cfg := testConfig("test", config.TrackConfig{Name: "a", EngineSlots: 1})
	_, err := Start(StartOpts{
		Config:     cfg,
		ConfigPath: "/tmp/test.yaml",
		DB:         db,
		Tmux:       m,
	})
	if err == nil {
		t.Fatal("expected error for yardmaster send keys failure")
	}
	if !strings.Contains(err.Error(), "start yardmaster") {
		t.Errorf("error = %q, want to contain 'start yardmaster'", err.Error())
	}
}

func TestStart_EngineSendKeysError(t *testing.T) {
	db := testDB(t)
	callCount := 0
	m := &mockTmux{
		sendKeysFunc: func(session, keys string) error {
			callCount++
			// Fail on 2nd SendKeys call (1st=yardmaster, 2nd=engine).
			if callCount >= 2 {
				return fmt.Errorf("send keys failed")
			}
			return nil
		},
	}
	cfg := testConfig("test", config.TrackConfig{Name: "a", EngineSlots: 1})
	_, err := Start(StartOpts{
		Config:     cfg,
		ConfigPath: "/tmp/test.yaml",
		DB:         db,
		Tmux:       m,
	})
	if err == nil {
		t.Fatal("expected error for engine send keys failure")
	}
	if !strings.Contains(err.Error(), "start engine on") {
		t.Errorf("error = %q, want to contain 'start engine on'", err.Error())
	}
}

func TestStart_ZeroEngineSlots(t *testing.T) {
	db := testDB(t)
	m := &mockTmux{}
	cfg := testConfig("test", config.TrackConfig{Name: "a", EngineSlots: 0})
	result, err := Start(StartOpts{
		Config:     cfg,
		ConfigPath: "/tmp/test.yaml",
		DB:         db,
		Tmux:       m,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// 0 slots => totalEngines defaults to 1.
	if len(result.EngineSessions) != 1 {
		t.Errorf("engine sessions = %d, want 1", len(result.EngineSessions))
	}
}

// ---------------------------------------------------------------------------
// Stop tests
// ---------------------------------------------------------------------------

func TestStop_NilDB(t *testing.T) {
	err := Stop(StopOpts{})
	if err == nil {
		t.Fatal("expected error for nil DB")
	}
	if !strings.Contains(err.Error(), "database connection is required") {
		t.Errorf("error = %q, want to contain 'database connection is required'", err.Error())
	}
}

func TestStop_NoSession(t *testing.T) {
	db := testDB(t)
	m := &mockTmux{sessionExists: false}
	err := Stop(StopOpts{DB: db, Tmux: m})
	if err == nil {
		t.Fatal("expected error for no session")
	}
	if !strings.Contains(err.Error(), "no railyard session running") {
		t.Errorf("error = %q, want to contain 'no railyard session running'", err.Error())
	}
}

func TestStop_Success(t *testing.T) {
	db := testDB(t)
	// Create some engines.
	db.Create(&models.Engine{ID: "eng-1", Track: "backend", Status: "idle"})
	db.Create(&models.Engine{ID: "eng-2", Track: "backend", Status: "idle"})

	cfg := testConfig("test")
	m := &mockTmux{
		listSessionsFunc: func(prefix string) ([]string, error) {
			return []string{
				"railyard_test_yardmaster",
				"railyard_test_eng000",
				"railyard_test_eng001",
			}, nil
		},
	}
	err := Stop(StopOpts{DB: db, Config: cfg, Timeout: 1 * time.Millisecond, Tmux: m})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Should have sent C-c to 3 sessions.
	if len(m.sentSignals) != 3 {
		t.Errorf("sent signals = %d, want 3", len(m.sentSignals))
	}
	// All 3 sessions should have been killed.
	if len(m.killedSessions) != 3 {
		t.Errorf("killed sessions = %d, want 3", len(m.killedSessions))
	}
	// All engines should be marked dead.
	var count int64
	db.Model(&models.Engine{}).Where("status != ?", "dead").Count(&count)
	if count != 0 {
		t.Errorf("non-dead engines = %d, want 0", count)
	}
}

func TestStop_KillSessionError(t *testing.T) {
	db := testDB(t)
	cfg := testConfig("test")
	m := &mockTmux{
		listSessionsFunc: func(prefix string) ([]string, error) {
			return []string{"railyard_test_yardmaster"}, nil
		},
		killSessionErr: fmt.Errorf("kill failed"),
	}
	err := Stop(StopOpts{DB: db, Config: cfg, Timeout: 1 * time.Millisecond, Tmux: m})
	if err == nil {
		t.Fatal("expected error for kill session failure")
	}
	if !strings.Contains(err.Error(), "kill failed") {
		t.Errorf("error = %q, want to contain 'kill failed'", err.Error())
	}
}

func TestStop_OnlyLegacyDispatchRunning(t *testing.T) {
	db := testDB(t)
	m := &mockTmux{
		sessionExistsFunc: func(name string) bool {
			return name == legacyDispatchSessionName
		},
	}
	err := Stop(StopOpts{DB: db, Timeout: 1 * time.Millisecond, Tmux: m})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Only legacy dispatch session should be killed.
	if len(m.killedSessions) != 1 {
		t.Errorf("killed sessions = %d, want 1", len(m.killedSessions))
	}
	if m.killedSessions[0] != legacyDispatchSessionName {
		t.Errorf("killed session = %q, want %q", m.killedSessions[0], legacyDispatchSessionName)
	}
}

func TestStop_DefaultTimeout(t *testing.T) {
	db := testDB(t)
	m := &mockTmux{
		sessionExistsFunc: func(name string) bool {
			return name == legacySessionName
		},
	}
	// Pass 0 timeout — should default to 60s.
	// Just verify it doesn't error (won't actually wait 60s since no working engines).
	err := Stop(StopOpts{DB: db, Tmux: m})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Legacy session should be killed.
	if len(m.killedSessions) != 1 {
		t.Errorf("killed sessions = %d, want 1", len(m.killedSessions))
	}
}

// ---------------------------------------------------------------------------
// Status tests
// ---------------------------------------------------------------------------

func TestStatus_NilDB(t *testing.T) {
	_, err := Status(nil, nil, nil)
	if err == nil {
		t.Fatal("expected error for nil DB")
	}
}

func TestStatus_EmptyDB(t *testing.T) {
	db := testDB(t)
	m := &mockTmux{sessionExists: false}
	info, err := Status(db, m, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if info.SessionRunning {
		t.Error("expected session not running")
	}
	if len(info.Engines) != 0 {
		t.Errorf("engines = %d, want 0", len(info.Engines))
	}
	if len(info.TrackSummary) != 0 {
		t.Errorf("track summary = %d, want 0", len(info.TrackSummary))
	}
	if info.MessageDepth != 0 {
		t.Errorf("message depth = %d, want 0", info.MessageDepth)
	}
}

func TestStatus_WithEnginesAndTracks(t *testing.T) {
	db := testDB(t)
	now := time.Now()

	// Create engines.
	db.Create(&models.Engine{
		ID: "eng-1", Track: "backend", Status: "working",
		CurrentCar: "b-1", StartedAt: now.Add(-30 * time.Minute), LastActivity: now,
	})
	db.Create(&models.Engine{
		ID: "eng-2", Track: "backend", Status: "idle",
		StartedAt: now.Add(-10 * time.Minute), LastActivity: now,
	})
	// Dead engine — should be excluded.
	db.Create(&models.Engine{
		ID: "eng-dead", Track: "backend", Status: "dead",
		StartedAt: now.Add(-1 * time.Hour), LastActivity: now,
	})

	// Create active tracks.
	db.Create(&models.Track{Name: "backend", Active: true})
	db.Create(&models.Track{Name: "frontend", Active: true})

	// Create cars.
	db.Create(&models.Car{ID: "b-1", Track: "backend", Status: "open"})
	db.Create(&models.Car{ID: "b-2", Track: "backend", Status: "in_progress", Assignee: "eng-1"})
	db.Create(&models.Car{ID: "b-3", Track: "backend", Status: "done"})

	// Create messages.
	db.Create(&models.Message{FromAgent: "a", ToAgent: "eng-1", Acknowledged: false})
	db.Create(&models.Message{FromAgent: "a", ToAgent: "eng-2", Acknowledged: false})
	db.Create(&models.Message{FromAgent: "a", ToAgent: "broadcast", Acknowledged: false})

	cfg := testConfig("test")
	m := &mockTmux{
		listSessionsFunc: func(prefix string) ([]string, error) {
			return []string{"railyard_test_yardmaster"}, nil
		},
	}
	info, err := Status(db, m, cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !info.SessionRunning {
		t.Error("expected session running")
	}
	if len(info.Engines) != 2 {
		t.Errorf("engines = %d, want 2", len(info.Engines))
	}
	if len(info.TrackSummary) != 2 {
		t.Errorf("track summary = %d, want 2 (backend + frontend)", len(info.TrackSummary))
	}
	// 2 non-broadcast unacknowledged messages.
	if info.MessageDepth != 2 {
		t.Errorf("message depth = %d, want 2", info.MessageDepth)
	}
	// Component sessions should be reported.
	if len(info.ComponentSessions) != 1 {
		t.Errorf("component sessions = %d, want 1", len(info.ComponentSessions))
	}
}

func TestStatus_LegacyFallback(t *testing.T) {
	db := testDB(t)
	m := &mockTmux{
		sessionExistsFunc: func(name string) bool {
			return name == legacySessionName
		},
	}
	// No config — falls back to legacy session name check.
	info, err := Status(db, m, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !info.SessionRunning {
		t.Error("expected session running via legacy fallback")
	}
}

// ---------------------------------------------------------------------------
// Scale tests
// ---------------------------------------------------------------------------

func TestScale_NilDB(t *testing.T) {
	_, err := Scale(ScaleOpts{})
	if err == nil {
		t.Fatal("expected error for nil DB")
	}
}

func TestScale_NilConfig(t *testing.T) {
	_, err := Scale(ScaleOpts{Config: &config.Config{}})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestScale_MissingTrack(t *testing.T) {
	_, err := Scale(ScaleOpts{
		Config: &config.Config{Tracks: []config.TrackConfig{{Name: "a"}}},
	})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestScale_NegativeCount(t *testing.T) {
	_, err := Scale(ScaleOpts{
		Config: &config.Config{Tracks: []config.TrackConfig{{Name: "a"}}},
		Track:  "a",
		Count:  -1,
	})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestScale_TrackNotFound(t *testing.T) {
	db := testDB(t)
	_, err := Scale(ScaleOpts{
		DB:     db,
		Config: &config.Config{Tracks: []config.TrackConfig{{Name: "a", EngineSlots: 5}}},
		Track:  "nonexistent",
		Count:  1,
	})
	if err == nil {
		t.Fatal("expected error for track not found")
	}
	if !strings.Contains(err.Error(), "not found in config") {
		t.Errorf("error = %q, want to contain 'not found in config'", err.Error())
	}
}

func TestScale_ExceedsSlots(t *testing.T) {
	db := testDB(t)
	_, err := Scale(ScaleOpts{
		DB:     db,
		Config: &config.Config{Tracks: []config.TrackConfig{{Name: "a", EngineSlots: 3}}},
		Track:  "a",
		Count:  5,
	})
	if err == nil {
		t.Fatal("expected error for exceeding slots")
	}
	if !strings.Contains(err.Error(), "exceeds max engine_slots") {
		t.Errorf("error = %q, want to contain 'exceeds max engine_slots'", err.Error())
	}
}

func TestScale_NoSession(t *testing.T) {
	db := testDB(t)
	cfg := testConfig("test", config.TrackConfig{Name: "a", EngineSlots: 5})
	m := &mockTmux{sessionExists: false}
	_, err := Scale(ScaleOpts{
		DB:     db,
		Config: cfg,
		Track:  "a",
		Count:  2,
		Tmux:   m,
	})
	if err == nil {
		t.Fatal("expected error for no session")
	}
	if !strings.Contains(err.Error(), "no railyard session running") {
		t.Errorf("error = %q, want to contain 'no railyard session running'", err.Error())
	}
}

func TestScale_NoChange(t *testing.T) {
	db := testDB(t)
	now := time.Now()
	db.Create(&models.Engine{ID: "eng-1", Track: "backend", Status: "idle", StartedAt: now})
	db.Create(&models.Engine{ID: "eng-2", Track: "backend", Status: "working", StartedAt: now})

	cfg := testConfig("test", config.TrackConfig{Name: "backend", EngineSlots: 5})
	m := &mockTmux{
		sessionExistsFunc: func(name string) bool {
			return name == YardmasterSession("test")
		},
	}
	result, err := Scale(ScaleOpts{
		DB:     db,
		Config: cfg,
		Track:  "backend",
		Count:  2,
		Tmux:   m,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Previous != 2 {
		t.Errorf("previous = %d, want 2", result.Previous)
	}
	if result.Current != 2 {
		t.Errorf("current = %d, want 2", result.Current)
	}
	if len(result.SessionsCreated) != 0 {
		t.Errorf("sessions created = %d, want 0", len(result.SessionsCreated))
	}
}

func TestScale_ScaleUp(t *testing.T) {
	db := testDB(t)
	now := time.Now()
	db.Create(&models.Engine{ID: "eng-1", Track: "backend", Status: "idle", StartedAt: now})

	cfg := testConfig("test", config.TrackConfig{Name: "backend", EngineSlots: 5})
	m := &mockTmux{
		sessionExistsFunc: func(name string) bool {
			return name == YardmasterSession("test")
		},
	}
	result, err := Scale(ScaleOpts{
		DB:         db,
		Config:     cfg,
		ConfigPath: "/tmp/test.yaml",
		Track:      "backend",
		Count:      3,
		Tmux:       m,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Previous != 1 {
		t.Errorf("previous = %d, want 1", result.Previous)
	}
	if result.Current != 3 {
		t.Errorf("current = %d, want 3", result.Current)
	}
	if len(result.SessionsCreated) != 2 {
		t.Errorf("sessions created = %d, want 2", len(result.SessionsCreated))
	}
	// Should have created 2 sessions and sent 2 keys.
	if len(m.createdSessions) != 2 {
		t.Errorf("mock sessions created = %d, want 2", len(m.createdSessions))
	}
	if len(m.sentKeys) != 2 {
		t.Errorf("sent keys = %d, want 2", len(m.sentKeys))
	}
}

func TestScale_ScaleDown(t *testing.T) {
	db := testDB(t)
	now := time.Now()
	db.Create(&models.Engine{ID: "eng-1", Track: "backend", Status: "idle", StartedAt: now.Add(-10 * time.Minute)})
	db.Create(&models.Engine{ID: "eng-2", Track: "backend", Status: "working", StartedAt: now.Add(-5 * time.Minute)})
	db.Create(&models.Engine{ID: "eng-3", Track: "backend", Status: "idle", StartedAt: now.Add(-1 * time.Minute)})

	cfg := testConfig("test", config.TrackConfig{Name: "backend", EngineSlots: 5})
	m := &mockTmux{
		sessionExistsFunc: func(name string) bool {
			return name == YardmasterSession("test")
		},
	}
	result, err := Scale(ScaleOpts{
		DB:     db,
		Config: cfg,
		Track:  "backend",
		Count:  1,
		Tmux:   m,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Previous != 3 {
		t.Errorf("previous = %d, want 3", result.Previous)
	}
	if result.Current != 1 {
		t.Errorf("current = %d, want 1", result.Current)
	}
	if len(result.SessionsKilled) != 2 {
		t.Errorf("sessions killed = %d, want 2", len(result.SessionsKilled))
	}
	// Newest engines should be killed first (LIFO).
	if result.SessionsKilled[0] != "eng-3" {
		t.Errorf("first killed = %q, want eng-3", result.SessionsKilled[0])
	}
	if result.SessionsKilled[1] != "eng-2" {
		t.Errorf("second killed = %q, want eng-2", result.SessionsKilled[1])
	}
}

func TestScale_ScaleUpCreateSessionError(t *testing.T) {
	db := testDB(t)
	cfg := testConfig("test", config.TrackConfig{Name: "a", EngineSlots: 5})
	m := &mockTmux{
		sessionExistsFunc: func(name string) bool {
			return name == YardmasterSession("test")
		},
		createErr: fmt.Errorf("session failed"),
	}
	result, err := Scale(ScaleOpts{
		DB:     db,
		Config: cfg,
		Track:  "a",
		Count:  2,
		Tmux:   m,
	})
	if err == nil {
		t.Fatal("expected error for session creation failure")
	}
	if !strings.Contains(err.Error(), "create engine session") {
		t.Errorf("error = %q, want to contain 'create engine session'", err.Error())
	}
	// Partial result returned.
	if result.Previous != 0 {
		t.Errorf("previous = %d, want 0", result.Previous)
	}
}

func TestScale_ScaleUpSendKeysError(t *testing.T) {
	db := testDB(t)
	cfg := testConfig("test", config.TrackConfig{Name: "a", EngineSlots: 5})
	m := &mockTmux{
		sessionExistsFunc: func(name string) bool {
			return name == YardmasterSession("test")
		},
		sendKeysErr: fmt.Errorf("keys failed"),
	}
	_, err := Scale(ScaleOpts{
		DB:         db,
		Config:     cfg,
		ConfigPath: "/tmp/test.yaml",
		Track:      "a",
		Count:      1,
		Tmux:       m,
	})
	if err == nil {
		t.Fatal("expected error for send keys failure")
	}
	if !strings.Contains(err.Error(), "start engine on") {
		t.Errorf("error = %q, want to contain 'start engine on'", err.Error())
	}
}

// ---------------------------------------------------------------------------
// ListEngines tests
// ---------------------------------------------------------------------------

func TestListEngines_NilDB(t *testing.T) {
	_, err := ListEngines(EngineListOpts{})
	if err == nil {
		t.Fatal("expected error for nil DB")
	}
}

func TestListEngines_NoFilter(t *testing.T) {
	db := testDB(t)
	now := time.Now()
	db.Create(&models.Engine{ID: "eng-1", Track: "backend", Status: "idle", StartedAt: now})
	db.Create(&models.Engine{ID: "eng-2", Track: "frontend", Status: "working", StartedAt: now})
	db.Create(&models.Engine{ID: "eng-dead", Track: "backend", Status: "dead", StartedAt: now})

	engines, err := ListEngines(EngineListOpts{DB: db})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// 2 non-dead engines.
	if len(engines) != 2 {
		t.Errorf("engines = %d, want 2", len(engines))
	}
}

func TestListEngines_FilterByTrack(t *testing.T) {
	db := testDB(t)
	now := time.Now()
	db.Create(&models.Engine{ID: "eng-1", Track: "backend", Status: "idle", StartedAt: now})
	db.Create(&models.Engine{ID: "eng-2", Track: "frontend", Status: "idle", StartedAt: now})

	engines, err := ListEngines(EngineListOpts{DB: db, Track: "backend"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(engines) != 1 {
		t.Errorf("engines = %d, want 1", len(engines))
	}
	if engines[0].Track != "backend" {
		t.Errorf("track = %q, want backend", engines[0].Track)
	}
}

func TestListEngines_FilterByStatus(t *testing.T) {
	db := testDB(t)
	now := time.Now()
	db.Create(&models.Engine{ID: "eng-1", Track: "backend", Status: "idle", StartedAt: now})
	db.Create(&models.Engine{ID: "eng-2", Track: "backend", Status: "working", StartedAt: now})
	db.Create(&models.Engine{ID: "eng-3", Track: "backend", Status: "dead", StartedAt: now})

	engines, err := ListEngines(EngineListOpts{DB: db, Status: "dead"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(engines) != 1 {
		t.Errorf("engines = %d, want 1", len(engines))
	}
	if engines[0].Status != "dead" {
		t.Errorf("status = %q, want dead", engines[0].Status)
	}
}

// ---------------------------------------------------------------------------
// RestartEngine tests
// ---------------------------------------------------------------------------

func TestRestartEngine_NilDB(t *testing.T) {
	err := RestartEngine(nil, nil, "", "eng-123", nil)
	if err == nil {
		t.Fatal("expected error for nil DB")
	}
}

func TestRestartEngine_EmptyID(t *testing.T) {
	err := RestartEngine(nil, nil, "", "", nil)
	if err == nil {
		t.Fatal("expected error for empty ID")
	}
}

func TestRestartEngine_NilConfig(t *testing.T) {
	db := testDB(t)
	err := RestartEngine(db, nil, "/tmp/test.yaml", "eng-123", nil)
	if err == nil {
		t.Fatal("expected error for nil config")
	}
	if !strings.Contains(err.Error(), "config is required") {
		t.Errorf("error = %q, want to contain 'config is required'", err.Error())
	}
}

func TestRestartEngine_NoSession(t *testing.T) {
	db := testDB(t)
	cfg := testConfig("test")
	m := &mockTmux{sessionExists: false}
	err := RestartEngine(db, cfg, "/tmp/test.yaml", "eng-123", m)
	if err == nil {
		t.Fatal("expected error for no session")
	}
	if !strings.Contains(err.Error(), "no railyard session running") {
		t.Errorf("error = %q, want to contain 'no railyard session running'", err.Error())
	}
}

func TestRestartEngine_EngineNotFound(t *testing.T) {
	db := testDB(t)
	cfg := testConfig("test")
	m := &mockTmux{
		sessionExistsFunc: func(name string) bool {
			return name == YardmasterSession("test")
		},
	}
	err := RestartEngine(db, cfg, "/tmp/test.yaml", "nonexistent", m)
	if err == nil {
		t.Fatal("expected error for engine not found")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("error = %q, want to contain 'not found'", err.Error())
	}
}

func TestRestartEngine_Success(t *testing.T) {
	db := testDB(t)
	now := time.Now()
	db.Create(&models.Engine{ID: "eng-1", Track: "backend", Status: "idle", StartedAt: now, LastActivity: now})

	cfg := testConfig("test")
	m := &mockTmux{
		sessionExistsFunc: func(name string) bool {
			return name == YardmasterSession("test")
		},
	}
	err := RestartEngine(db, cfg, "/tmp/test.yaml", "eng-1", m)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Old engine should be marked dead.
	var eng models.Engine
	db.Where("id = ?", "eng-1").First(&eng)
	if eng.Status != "dead" {
		t.Errorf("old engine status = %q, want dead", eng.Status)
	}
	// A new session should have been created and keys sent.
	if len(m.createdSessions) != 1 {
		t.Errorf("sessions created = %d, want 1", len(m.createdSessions))
	}
	if len(m.sentKeys) != 1 {
		t.Errorf("sent keys = %d, want 1", len(m.sentKeys))
	}
	if !strings.Contains(m.sentKeys[0], "--track backend") {
		t.Errorf("sent keys = %q, want to contain '--track backend'", m.sentKeys[0])
	}
}

func TestRestartEngine_CreateSessionError(t *testing.T) {
	db := testDB(t)
	now := time.Now()
	db.Create(&models.Engine{ID: "eng-1", Track: "backend", Status: "idle", StartedAt: now, LastActivity: now})

	cfg := testConfig("test")
	m := &mockTmux{
		sessionExistsFunc: func(name string) bool {
			return name == YardmasterSession("test")
		},
		createErr: fmt.Errorf("session failed"),
	}
	err := RestartEngine(db, cfg, "/tmp/test.yaml", "eng-1", m)
	if err == nil {
		t.Fatal("expected error for session creation failure")
	}
	if !strings.Contains(err.Error(), "create replacement session") {
		t.Errorf("error = %q, want to contain 'create replacement session'", err.Error())
	}
}

func TestRestartEngine_SendKeysError(t *testing.T) {
	db := testDB(t)
	now := time.Now()
	db.Create(&models.Engine{ID: "eng-1", Track: "backend", Status: "idle", StartedAt: now, LastActivity: now})

	cfg := testConfig("test")
	m := &mockTmux{
		sessionExistsFunc: func(name string) bool {
			return name == YardmasterSession("test")
		},
		sendKeysErr: fmt.Errorf("keys failed"),
	}
	err := RestartEngine(db, cfg, "/tmp/test.yaml", "eng-1", m)
	if err == nil {
		t.Fatal("expected error for send keys failure")
	}
	if !strings.Contains(err.Error(), "start replacement engine") {
		t.Errorf("error = %q, want to contain 'start replacement engine'", err.Error())
	}
}

// ---------------------------------------------------------------------------
// FormatStatus tests
// ---------------------------------------------------------------------------

func TestFormatStatus_Running(t *testing.T) {
	info := &StatusInfo{
		SessionRunning:  true,
		DispatchRunning: true,
		ComponentSessions: []string{
			"railyard_test_yardmaster",
			"railyard_test_eng000",
		},
		Engines: []EngineInfo{
			{
				ID:           "eng-abcd1234",
				Track:        "backend",
				Status:       "working",
				CurrentCar:   "car-12345",
				LastActivity: time.Now(),
				Uptime:       30 * time.Minute,
			},
		},
		TrackSummary: []TrackSummary{
			{Track: "backend", Open: 5, Ready: 3, InProgress: 1, Done: 10, Blocked: 2},
		},
		MessageDepth: 3,
	}

	out := FormatStatus(info)
	if !strings.Contains(out, "RUNNING") {
		t.Errorf("expected 'RUNNING', got: %s", out)
	}
	if !strings.Contains(out, "eng-abcd1234") {
		t.Errorf("expected engine ID, got: %s", out)
	}
	if !strings.Contains(out, "backend") {
		t.Errorf("expected track name, got: %s", out)
	}
	if !strings.Contains(out, "3 unacknowledged") {
		t.Errorf("expected message depth, got: %s", out)
	}
	// Component sessions should be listed.
	if !strings.Contains(out, "SESSIONS") {
		t.Errorf("expected SESSIONS section, got: %s", out)
	}
	if !strings.Contains(out, "railyard_test_yardmaster") {
		t.Errorf("expected yardmaster session in output, got: %s", out)
	}
}

func TestFormatStatus_Stopped(t *testing.T) {
	info := &StatusInfo{SessionRunning: false}
	out := FormatStatus(info)
	if !strings.Contains(out, "STOPPED") {
		t.Errorf("expected 'STOPPED', got: %s", out)
	}
	if !strings.Contains(out, "no active engines") {
		t.Errorf("expected 'no active engines', got: %s", out)
	}
}

func TestFormatStatus_EmptyCar(t *testing.T) {
	info := &StatusInfo{
		SessionRunning: true,
		Engines: []EngineInfo{
			{
				ID:           "eng-1",
				Track:        "backend",
				Status:       "idle",
				CurrentCar:   "",
				LastActivity: time.Now(),
				Uptime:       5 * time.Minute,
			},
		},
	}
	out := FormatStatus(info)
	if !strings.Contains(out, "-") {
		t.Errorf("expected '-' for empty car, got: %s", out)
	}
}

func TestFormatStatus_NoTracks(t *testing.T) {
	info := &StatusInfo{SessionRunning: true, DispatchRunning: true}
	out := FormatStatus(info)
	if !strings.Contains(out, "no active tracks") {
		t.Errorf("expected 'no active tracks', got: %s", out)
	}
}

func TestFormatStatus_MultipleBaseBranches(t *testing.T) {
	info := &StatusInfo{
		SessionRunning:  true,
		DispatchRunning: true,
		TrackSummary: []TrackSummary{
			{Track: "backend", Open: 3, BaseBranches: []string{"main", "develop"}},
			{Track: "frontend", Open: 2, BaseBranches: []string{"main"}},
		},
	}
	out := FormatStatus(info)
	if !strings.Contains(out, "BASE") {
		t.Errorf("expected BASE column header when multiple base branches exist, got: %s", out)
	}
	if !strings.Contains(out, "main,develop") {
		t.Errorf("expected 'main,develop' for backend track, got: %s", out)
	}
}

func TestFormatStatus_SingleBaseBranch(t *testing.T) {
	info := &StatusInfo{
		SessionRunning:  true,
		DispatchRunning: true,
		TrackSummary: []TrackSummary{
			{Track: "backend", Open: 3, BaseBranches: []string{"main"}},
			{Track: "frontend", Open: 2, BaseBranches: []string{"main"}},
		},
	}
	out := FormatStatus(info)
	if strings.Contains(out, "BASE") {
		t.Errorf("BASE column should not appear when all tracks use the same base branch, got: %s", out)
	}
}

// ---------------------------------------------------------------------------
// hasMultipleBases tests
// ---------------------------------------------------------------------------

func TestHasMultipleBases_Mixed(t *testing.T) {
	tracks := []TrackSummary{
		{Track: "a", BaseBranches: []string{"main"}},
		{Track: "b", BaseBranches: []string{"develop"}},
	}
	if !hasMultipleBases(tracks) {
		t.Error("expected true when tracks have different bases")
	}
}

func TestHasMultipleBases_SameBase(t *testing.T) {
	tracks := []TrackSummary{
		{Track: "a", BaseBranches: []string{"main"}},
		{Track: "b", BaseBranches: []string{"main"}},
	}
	if hasMultipleBases(tracks) {
		t.Error("expected false when all tracks use main")
	}
}

func TestHasMultipleBases_TrackWithMultiple(t *testing.T) {
	tracks := []TrackSummary{
		{Track: "a", BaseBranches: []string{"main", "develop"}},
	}
	if !hasMultipleBases(tracks) {
		t.Error("expected true when a single track has multiple bases")
	}
}

// ---------------------------------------------------------------------------
// formatDuration tests
// ---------------------------------------------------------------------------

func TestFormatDuration_Hours(t *testing.T) {
	d := 2*time.Hour + 30*time.Minute
	got := formatDuration(d)
	if got != "2h 30m" {
		t.Errorf("formatDuration(%v) = %q, want %q", d, got, "2h 30m")
	}
}

func TestFormatDuration_Minutes(t *testing.T) {
	d := 5*time.Minute + 15*time.Second
	got := formatDuration(d)
	if got != "5m 15s" {
		t.Errorf("formatDuration(%v) = %q, want %q", d, got, "5m 15s")
	}
}

func TestFormatDuration_Zero(t *testing.T) {
	got := formatDuration(0)
	if got != "0m 0s" {
		t.Errorf("formatDuration(0) = %q, want %q", got, "0m 0s")
	}
}

// ---------------------------------------------------------------------------
// Session naming tests
// ---------------------------------------------------------------------------

func TestSessionNaming(t *testing.T) {
	if got := SessionPrefix("testuser"); got != "railyard_testuser" {
		t.Errorf("SessionPrefix = %q, want railyard_testuser", got)
	}
	if got := YardmasterSession("testuser"); got != "railyard_testuser_yardmaster" {
		t.Errorf("YardmasterSession = %q, want railyard_testuser_yardmaster", got)
	}
	if got := TelegraphSession("testuser"); got != "railyard_testuser_telegraph" {
		t.Errorf("TelegraphSession = %q, want railyard_testuser_telegraph", got)
	}
	if got := EngineSession("testuser", 0); got != "railyard_testuser_eng000" {
		t.Errorf("EngineSession(0) = %q, want railyard_testuser_eng000", got)
	}
	if got := EngineSession("testuser", 42); got != "railyard_testuser_eng042" {
		t.Errorf("EngineSession(42) = %q, want railyard_testuser_eng042", got)
	}
	if got := DispatchSession("testuser"); got != "railyard_testuser_dispatch" {
		t.Errorf("DispatchSession = %q, want railyard_testuser_dispatch", got)
	}
}

// ---------------------------------------------------------------------------
// Tmux interface sanity check — verify DefaultTmux is set
// ---------------------------------------------------------------------------

func TestDefaultTmux_IsSet(t *testing.T) {
	if DefaultTmux == nil {
		t.Fatal("DefaultTmux should not be nil")
	}
	_, ok := DefaultTmux.(RealTmux)
	if !ok {
		t.Fatalf("DefaultTmux is %T, want RealTmux", DefaultTmux)
	}
}

// ---------------------------------------------------------------------------
// nextEngineIndex tests
// ---------------------------------------------------------------------------

func TestNextEngineIndex_NoExisting(t *testing.T) {
	m := &mockTmux{}
	idx := nextEngineIndex(m, "test")
	if idx != 0 {
		t.Errorf("nextEngineIndex = %d, want 0", idx)
	}
}

func TestNextEngineIndex_WithExisting(t *testing.T) {
	m := &mockTmux{
		listSessions: []string{
			"railyard_test_eng000",
			"railyard_test_eng001",
			"railyard_test_eng003",
		},
	}
	idx := nextEngineIndex(m, "test")
	if idx != 4 {
		t.Errorf("nextEngineIndex = %d, want 4", idx)
	}
}

// ---------------------------------------------------------------------------
// appendUnique tests
// ---------------------------------------------------------------------------

func TestAppendUnique_NewItem(t *testing.T) {
	s := appendUnique([]string{"a", "b"}, "c")
	if len(s) != 3 || s[2] != "c" {
		t.Errorf("appendUnique = %v, want [a b c]", s)
	}
}

func TestAppendUnique_Duplicate(t *testing.T) {
	s := appendUnique([]string{"a", "b"}, "a")
	if len(s) != 2 {
		t.Errorf("appendUnique = %v, want [a b]", s)
	}
}
