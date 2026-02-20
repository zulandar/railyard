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
	sessionExists  bool
	createErr      error
	newPaneID      string
	newPaneErr     error
	sendKeysErr    error
	sendSignalErr  error
	killPaneErr    error
	killSessionErr error
	listPanes      []string
	listPanesErr   error
	tileErr        error

	// Per-session overrides (take precedence over flat fields above).
	sessionExistsFunc func(name string) bool
	listPanesFunc     func(session string) ([]string, error)

	// Recording.
	createdSessions []string
	sentKeys        []string
	sentSignals     []string
	killedPanes     []string
	killedSessions  []string
	panesCreated    int
}

func (m *mockTmux) SessionExists(name string) bool {
	if m.sessionExistsFunc != nil {
		return m.sessionExistsFunc(name)
	}
	return m.sessionExists
}
func (m *mockTmux) CreateSession(name string) error {
	m.createdSessions = append(m.createdSessions, name)
	return m.createErr
}
func (m *mockTmux) NewPane(session string) (string, error) {
	m.panesCreated++
	id := m.newPaneID
	if id == "" {
		id = fmt.Sprintf("%%pane%d", m.panesCreated)
	}
	return id, m.newPaneErr
}
func (m *mockTmux) SendKeys(paneID, keys string) error {
	m.sentKeys = append(m.sentKeys, keys)
	return m.sendKeysErr
}
func (m *mockTmux) SendSignal(paneID, signal string) error {
	m.sentSignals = append(m.sentSignals, signal)
	return m.sendSignalErr
}
func (m *mockTmux) KillPane(paneID string) error {
	m.killedPanes = append(m.killedPanes, paneID)
	return m.killPaneErr
}
func (m *mockTmux) KillSession(name string) error {
	m.killedSessions = append(m.killedSessions, name)
	return m.killSessionErr
}
func (m *mockTmux) ListPanes(session string) ([]string, error) {
	if m.listPanesFunc != nil {
		return m.listPanesFunc(session)
	}
	return m.listPanes, m.listPanesErr
}
func (m *mockTmux) TileLayout(session string) error { return m.tileErr }

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
	// 5 tracks: one with many slots, four with 1 slot each.
	// totalEngines = 5 (>= len(tracks)), but floor-of-1 gives each track at least 1.
	// Track "big": (10*5)/14 = 3. Others: (1*5)/14 = 0, floor to 1 each.
	// assigned = 3+1+1+1+1 = 7 > 5. Over-assignment should be corrected.
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
	// 2 tracks: slots 3 and 1, totalEngines=3.
	// totalSlots=4. Track "a": (3*3)/4=2. Track "b": (1*3)/4=0, floor to 1.
	// assigned = 3. remaining = 0. No remainder.
	// But with totalEngines=5: Track "a": (3*5)/4=3. Track "b": (1*5)/4=1.
	// assigned = 4. remaining = 1. Fractional remainders: a: 3.75-3=0.75, b: 1.25-1=0.25.
	// a gets the extra. Result: a=4, b=1.
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
	_, err := Start(StartOpts{
		Config:     &config.Config{Tracks: []config.TrackConfig{{Name: "a", EngineSlots: 2}}},
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
		sessionExists: false,
		createErr:     fmt.Errorf("tmux not found"),
	}
	_, err := Start(StartOpts{
		Config:     &config.Config{Tracks: []config.TrackConfig{{Name: "a", EngineSlots: 1}}},
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

func TestStart_ListPanesError(t *testing.T) {
	db := testDB(t)
	m := &mockTmux{
		sessionExists: false,
		listPanesErr:  fmt.Errorf("list panes failed"),
	}
	_, err := Start(StartOpts{
		Config:     &config.Config{Tracks: []config.TrackConfig{{Name: "a", EngineSlots: 1}}},
		ConfigPath: "/tmp/test.yaml",
		DB:         db,
		Tmux:       m,
	})
	if err == nil {
		t.Fatal("expected error for list panes failure")
	}
	if !strings.Contains(err.Error(), "list dispatch panes") {
		t.Errorf("error = %q, want to contain 'list dispatch panes'", err.Error())
	}
	// Should have attempted to kill dispatch session on failure.
	if len(m.killedSessions) != 1 {
		t.Errorf("killedSessions = %d, want 1", len(m.killedSessions))
	}
}

func TestStart_DispatchSendKeysError(t *testing.T) {
	db := testDB(t)
	m := &mockTmux{
		sessionExists: false,
		listPanes:     []string{"%0"},
		sendKeysErr:   fmt.Errorf("send failed"),
	}
	_, err := Start(StartOpts{
		Config:     &config.Config{Tracks: []config.TrackConfig{{Name: "a", EngineSlots: 1}}},
		ConfigPath: "/tmp/test.yaml",
		DB:         db,
		Tmux:       m,
	})
	if err == nil {
		t.Fatal("expected error for send keys failure")
	}
	if !strings.Contains(err.Error(), "start dispatch") {
		t.Errorf("error = %q, want to contain 'start dispatch'", err.Error())
	}
}

func TestStart_MainListPanesError(t *testing.T) {
	db := testDB(t)
	m := &mockTmux{
		sessionExists: false,
		listPanesFunc: func(session string) ([]string, error) {
			if session == DispatchSessionName {
				return []string{"%d0"}, nil
			}
			return nil, fmt.Errorf("list panes failed")
		},
	}
	_, err := Start(StartOpts{
		Config:     &config.Config{Tracks: []config.TrackConfig{{Name: "a", EngineSlots: 1}}},
		ConfigPath: "/tmp/test.yaml",
		DB:         db,
		Tmux:       m,
	})
	if err == nil {
		t.Fatal("expected error for main session list panes failure")
	}
	if !strings.Contains(err.Error(), "list main panes") {
		t.Errorf("error = %q, want to contain 'list main panes'", err.Error())
	}
	// Should kill both sessions on failure.
	if len(m.killedSessions) != 2 {
		t.Errorf("killedSessions = %d, want 2", len(m.killedSessions))
	}
}

func TestStart_Success(t *testing.T) {
	db := testDB(t)
	m := &mockTmux{
		sessionExists: false,
		listPanes:     []string{"%0"},
	}
	cfg := &config.Config{
		Tracks: []config.TrackConfig{
			{Name: "backend", EngineSlots: 2},
		},
	}
	result, err := Start(StartOpts{
		Config:     cfg,
		ConfigPath: "/tmp/test.yaml",
		DB:         db,
		Tmux:       m,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Session != SessionName {
		t.Errorf("session = %q, want %q", result.Session, SessionName)
	}
	if result.DispatchSession != DispatchSessionName {
		t.Errorf("dispatch session = %q, want %q", result.DispatchSession, DispatchSessionName)
	}
	if result.DispatchPane != "%0" {
		t.Errorf("dispatch pane = %q, want %%0", result.DispatchPane)
	}
	if result.YardmasterPane == "%0" {
		// Yardmaster gets its own pane via ListPanes on main session (also %0 from mock).
		// This is fine — it's a different session's %0.
	}
	if result.YardmasterPane == "" {
		t.Error("yardmaster pane should not be empty")
	}
	if len(result.EnginePanes) != 2 {
		t.Errorf("engine panes = %d, want 2", len(result.EnginePanes))
	}
	// 2 sessions created (dispatch + main).
	if len(m.createdSessions) != 2 {
		t.Errorf("created sessions = %d, want 2", len(m.createdSessions))
	}
	if m.createdSessions[0] != DispatchSessionName {
		t.Errorf("first session = %q, want %q", m.createdSessions[0], DispatchSessionName)
	}
	if m.createdSessions[1] != SessionName {
		t.Errorf("second session = %q, want %q", m.createdSessions[1], SessionName)
	}
	// 1 dispatch + 1 yardmaster + 2 engines = 4 send-keys calls.
	if len(m.sentKeys) != 4 {
		t.Errorf("sent keys = %d, want 4", len(m.sentKeys))
	}
	// Verify dispatch command was sent first.
	if !strings.Contains(m.sentKeys[0], "ry dispatch") {
		t.Errorf("first send-keys = %q, want to contain 'ry dispatch'", m.sentKeys[0])
	}
	// Verify yardmaster command was sent second.
	if !strings.Contains(m.sentKeys[1], "ry yardmaster") {
		t.Errorf("second send-keys = %q, want to contain 'ry yardmaster'", m.sentKeys[1])
	}
}

func TestStart_EngineCount_Default(t *testing.T) {
	db := testDB(t)
	m := &mockTmux{
		sessionExists: false,
		listPanes:     []string{"%0"},
	}
	cfg := &config.Config{
		Tracks: []config.TrackConfig{
			{Name: "a", EngineSlots: 3},
			{Name: "b", EngineSlots: 2},
		},
	}
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
	if len(result.EnginePanes) != 5 {
		t.Errorf("engine panes = %d, want 5", len(result.EnginePanes))
	}
}

func TestStart_EngineCount_Custom(t *testing.T) {
	db := testDB(t)
	m := &mockTmux{
		sessionExists: false,
		listPanes:     []string{"%0"},
	}
	cfg := &config.Config{
		Tracks: []config.TrackConfig{
			{Name: "a", EngineSlots: 3},
			{Name: "b", EngineSlots: 2},
		},
	}
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
	if len(result.EnginePanes) != 3 {
		t.Errorf("engine panes = %d, want 3", len(result.EnginePanes))
	}
}

func TestStart_EnginePaneError(t *testing.T) {
	db := testDB(t)
	m := &mockTmux{
		sessionExists: false,
		listPanes:     []string{"%0"},
		newPaneErr:    fmt.Errorf("pane create failed"),
	}
	_, err := Start(StartOpts{
		Config:     &config.Config{Tracks: []config.TrackConfig{{Name: "a", EngineSlots: 1}}},
		ConfigPath: "/tmp/test.yaml",
		DB:         db,
		Tmux:       m,
	})
	if err == nil {
		t.Fatal("expected error for engine pane creation failure")
	}
	if !strings.Contains(err.Error(), "create engine pane") {
		t.Errorf("error = %q, want to contain 'create engine pane'", err.Error())
	}
	// Both sessions should be cleaned up.
	if len(m.killedSessions) != 2 {
		t.Errorf("killedSessions = %d, want 2", len(m.killedSessions))
	}
}

// conditionalMockTmux wraps mockTmux but can fail NewPane at a specific call count.
type conditionalMockTmux struct {
	*mockTmux
	newPaneFailAt int
	callCount     *int
}

func (c *conditionalMockTmux) NewPane(session string) (string, error) {
	*c.callCount++
	if *c.callCount >= c.newPaneFailAt {
		return "", fmt.Errorf("pane create failed")
	}
	return c.mockTmux.NewPane(session)
}

// sendKeysFailMock fails SendKeys on a specific call number.
type sendKeysFailMock struct {
	*mockTmux
	failAt    int
	callCount int
}

func (s *sendKeysFailMock) SendKeys(paneID, keys string) error {
	s.callCount++
	if s.callCount >= s.failAt {
		return fmt.Errorf("send keys failed at call %d", s.callCount)
	}
	s.mockTmux.sentKeys = append(s.mockTmux.sentKeys, keys)
	return nil
}

func TestStart_YardmasterSendKeysError(t *testing.T) {
	db := testDB(t)
	m := &mockTmux{
		sessionExists: false,
		listPanes:     []string{"%0"},
	}
	// Fail on 2nd SendKeys call (1st is dispatch, 2nd is yardmaster).
	sm := &sendKeysFailMock{mockTmux: m, failAt: 2}
	_, err := Start(StartOpts{
		Config:     &config.Config{Tracks: []config.TrackConfig{{Name: "a", EngineSlots: 1}}},
		ConfigPath: "/tmp/test.yaml",
		DB:         db,
		Tmux:       sm,
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
	m := &mockTmux{
		sessionExists: false,
		listPanes:     []string{"%0"},
	}
	// Fail on 3rd SendKeys call (1st=dispatch, 2nd=yardmaster, 3rd=engine).
	sm := &sendKeysFailMock{mockTmux: m, failAt: 3}
	_, err := Start(StartOpts{
		Config:     &config.Config{Tracks: []config.TrackConfig{{Name: "a", EngineSlots: 1}}},
		ConfigPath: "/tmp/test.yaml",
		DB:         db,
		Tmux:       sm,
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
	m := &mockTmux{
		sessionExists: false,
		listPanes:     []string{"%0"},
	}
	cfg := &config.Config{
		Tracks: []config.TrackConfig{
			{Name: "a", EngineSlots: 0},
		},
	}
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
	if len(result.EnginePanes) != 1 {
		t.Errorf("engine panes = %d, want 1", len(result.EnginePanes))
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

	m := &mockTmux{
		sessionExists: true,
		listPanesFunc: func(session string) ([]string, error) {
			if session == SessionName {
				return []string{"%0", "%1", "%2"}, nil
			}
			return []string{"%d0"}, nil // dispatch has 1 pane
		},
	}
	err := Stop(StopOpts{DB: db, Timeout: 1 * time.Millisecond, Tmux: m})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Should have sent C-c to 3 main panes + 1 dispatch pane = 4.
	if len(m.sentSignals) != 4 {
		t.Errorf("sent signals = %d, want 4", len(m.sentSignals))
	}
	// Both sessions should have been killed.
	if len(m.killedSessions) != 2 {
		t.Errorf("killed sessions = %d, want 2", len(m.killedSessions))
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
	m := &mockTmux{
		sessionExists:  true,
		listPanes:      []string{"%0"},
		killSessionErr: fmt.Errorf("kill failed"),
	}
	err := Stop(StopOpts{DB: db, Timeout: 1 * time.Millisecond, Tmux: m})
	if err == nil {
		t.Fatal("expected error for kill session failure")
	}
	if !strings.Contains(err.Error(), "kill failed") {
		t.Errorf("error = %q, want to contain 'kill failed'", err.Error())
	}
}

func TestStop_OnlyDispatchRunning(t *testing.T) {
	db := testDB(t)
	m := &mockTmux{
		sessionExistsFunc: func(name string) bool {
			return name == DispatchSessionName
		},
		listPanes: []string{"%d0"},
	}
	err := Stop(StopOpts{DB: db, Timeout: 1 * time.Millisecond, Tmux: m})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Only dispatch session should be killed.
	if len(m.killedSessions) != 1 {
		t.Errorf("killed sessions = %d, want 1", len(m.killedSessions))
	}
	if m.killedSessions[0] != DispatchSessionName {
		t.Errorf("killed session = %q, want %q", m.killedSessions[0], DispatchSessionName)
	}
}

func TestStop_ListPanesError(t *testing.T) {
	db := testDB(t)
	m := &mockTmux{
		sessionExists: true,
		listPanesErr:  fmt.Errorf("list failed"),
	}
	// Even if list panes fails, stop should continue and kill sessions.
	err := Stop(StopOpts{DB: db, Timeout: 1 * time.Millisecond, Tmux: m})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// No signals sent (list panes failed), but both sessions should be killed.
	if len(m.sentSignals) != 0 {
		t.Errorf("sent signals = %d, want 0", len(m.sentSignals))
	}
	if len(m.killedSessions) != 2 {
		t.Errorf("killed sessions = %d, want 2", len(m.killedSessions))
	}
}

func TestStop_DefaultTimeout(t *testing.T) {
	db := testDB(t)
	m := &mockTmux{
		sessionExists: true,
		listPanes:     []string{"%0"},
	}
	// Pass 0 timeout — should default to 60s.
	// Just verify it doesn't error (won't actually wait 60s since no working engines).
	err := Stop(StopOpts{DB: db, Tmux: m})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Both sessions killed (main + dispatch).
	if len(m.killedSessions) != 2 {
		t.Errorf("killed sessions = %d, want 2", len(m.killedSessions))
	}
}

// ---------------------------------------------------------------------------
// Status tests
// ---------------------------------------------------------------------------

func TestStatus_NilDB(t *testing.T) {
	_, err := Status(nil, nil)
	if err == nil {
		t.Fatal("expected error for nil DB")
	}
}

func TestStatus_EmptyDB(t *testing.T) {
	db := testDB(t)
	m := &mockTmux{sessionExists: false}
	info, err := Status(db, m)
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

	m := &mockTmux{sessionExists: true}
	info, err := Status(db, m)
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
	m := &mockTmux{sessionExists: false}
	_, err := Scale(ScaleOpts{
		DB:     db,
		Config: &config.Config{Tracks: []config.TrackConfig{{Name: "a", EngineSlots: 5}}},
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

	m := &mockTmux{sessionExists: true}
	result, err := Scale(ScaleOpts{
		DB:     db,
		Config: &config.Config{Tracks: []config.TrackConfig{{Name: "backend", EngineSlots: 5}}},
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
	if len(result.PanesCreated) != 0 {
		t.Errorf("panes created = %d, want 0", len(result.PanesCreated))
	}
}

func TestScale_ScaleUp(t *testing.T) {
	db := testDB(t)
	now := time.Now()
	db.Create(&models.Engine{ID: "eng-1", Track: "backend", Status: "idle", StartedAt: now})

	m := &mockTmux{
		sessionExists: true,
		listPanes:     []string{"%0"},
	}
	result, err := Scale(ScaleOpts{
		DB:         db,
		Config:     &config.Config{Tracks: []config.TrackConfig{{Name: "backend", EngineSlots: 5}}},
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
	if len(result.PanesCreated) != 2 {
		t.Errorf("panes created = %d, want 2", len(result.PanesCreated))
	}
	// Should have created 2 panes and sent 2 keys.
	if m.panesCreated != 2 {
		t.Errorf("mock panes created = %d, want 2", m.panesCreated)
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

	m := &mockTmux{sessionExists: true}
	result, err := Scale(ScaleOpts{
		DB:     db,
		Config: &config.Config{Tracks: []config.TrackConfig{{Name: "backend", EngineSlots: 5}}},
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
	if len(result.PanesKilled) != 2 {
		t.Errorf("panes killed = %d, want 2", len(result.PanesKilled))
	}
	// Newest engines should be killed first (LIFO).
	// eng-3 started most recently, then eng-2.
	if result.PanesKilled[0] != "eng-3" {
		t.Errorf("first killed = %q, want eng-3", result.PanesKilled[0])
	}
	if result.PanesKilled[1] != "eng-2" {
		t.Errorf("second killed = %q, want eng-2", result.PanesKilled[1])
	}
}

func TestScale_ScaleUpNewPaneError(t *testing.T) {
	db := testDB(t)
	m := &mockTmux{
		sessionExists: true,
		newPaneErr:    fmt.Errorf("pane failed"),
	}
	result, err := Scale(ScaleOpts{
		DB:     db,
		Config: &config.Config{Tracks: []config.TrackConfig{{Name: "a", EngineSlots: 5}}},
		Track:  "a",
		Count:  2,
		Tmux:   m,
	})
	if err == nil {
		t.Fatal("expected error for new pane failure")
	}
	if !strings.Contains(err.Error(), "create engine pane") {
		t.Errorf("error = %q, want to contain 'create engine pane'", err.Error())
	}
	// Partial result returned.
	if result.Previous != 0 {
		t.Errorf("previous = %d, want 0", result.Previous)
	}
}

func TestScale_ScaleUpSendKeysError(t *testing.T) {
	db := testDB(t)
	m := &mockTmux{
		sessionExists: true,
		sendKeysErr:   fmt.Errorf("keys failed"),
	}
	_, err := Scale(ScaleOpts{
		DB:         db,
		Config:     &config.Config{Tracks: []config.TrackConfig{{Name: "a", EngineSlots: 5}}},
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
	err := RestartEngine(nil, "", "eng-123", nil)
	if err == nil {
		t.Fatal("expected error for nil DB")
	}
}

func TestRestartEngine_EmptyID(t *testing.T) {
	err := RestartEngine(nil, "", "", nil)
	if err == nil {
		t.Fatal("expected error for empty ID")
	}
}

func TestRestartEngine_NoSession(t *testing.T) {
	db := testDB(t)
	m := &mockTmux{sessionExists: false}
	err := RestartEngine(db, "/tmp/test.yaml", "eng-123", m)
	if err == nil {
		t.Fatal("expected error for no session")
	}
	if !strings.Contains(err.Error(), "no railyard session running") {
		t.Errorf("error = %q, want to contain 'no railyard session running'", err.Error())
	}
}

func TestRestartEngine_EngineNotFound(t *testing.T) {
	db := testDB(t)
	m := &mockTmux{sessionExists: true}
	err := RestartEngine(db, "/tmp/test.yaml", "nonexistent", m)
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

	m := &mockTmux{sessionExists: true}
	err := RestartEngine(db, "/tmp/test.yaml", "eng-1", m)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Old engine should be marked dead.
	var eng models.Engine
	db.Where("id = ?", "eng-1").First(&eng)
	if eng.Status != "dead" {
		t.Errorf("old engine status = %q, want dead", eng.Status)
	}
	// A new pane should have been created and keys sent.
	if m.panesCreated != 1 {
		t.Errorf("panes created = %d, want 1", m.panesCreated)
	}
	if len(m.sentKeys) != 1 {
		t.Errorf("sent keys = %d, want 1", len(m.sentKeys))
	}
	if !strings.Contains(m.sentKeys[0], "--track backend") {
		t.Errorf("sent keys = %q, want to contain '--track backend'", m.sentKeys[0])
	}
}

func TestRestartEngine_NewPaneError(t *testing.T) {
	db := testDB(t)
	now := time.Now()
	db.Create(&models.Engine{ID: "eng-1", Track: "backend", Status: "idle", StartedAt: now, LastActivity: now})

	m := &mockTmux{
		sessionExists: true,
		newPaneErr:    fmt.Errorf("pane failed"),
	}
	err := RestartEngine(db, "/tmp/test.yaml", "eng-1", m)
	if err == nil {
		t.Fatal("expected error for new pane failure")
	}
	if !strings.Contains(err.Error(), "create replacement pane") {
		t.Errorf("error = %q, want to contain 'create replacement pane'", err.Error())
	}
}

func TestRestartEngine_SendKeysError(t *testing.T) {
	db := testDB(t)
	now := time.Now()
	db.Create(&models.Engine{ID: "eng-1", Track: "backend", Status: "idle", StartedAt: now, LastActivity: now})

	m := &mockTmux{
		sessionExists: true,
		sendKeysErr:   fmt.Errorf("keys failed"),
	}
	err := RestartEngine(db, "/tmp/test.yaml", "eng-1", m)
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
