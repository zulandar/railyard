package telegraph

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/zulandar/railyard/internal/models"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// ---------------------------------------------------------------------------
// Mock process and spawner for tests
// ---------------------------------------------------------------------------

type mockProcess struct {
	mu     sync.Mutex
	sent   []string
	recvCh chan string
	doneCh chan struct{}
	closed bool
	prompt string
}

func newMockProcess(prompt string) *mockProcess {
	return &mockProcess{
		recvCh: make(chan string, 100),
		doneCh: make(chan struct{}),
		prompt: prompt,
	}
}

func (p *mockProcess) Send(msg string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return fmt.Errorf("process closed")
	}
	p.sent = append(p.sent, msg)
	return nil
}

func (p *mockProcess) Recv() <-chan string { return p.recvCh }

func (p *mockProcess) Done() <-chan struct{} { return p.doneCh }

func (p *mockProcess) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.closed {
		p.closed = true
		close(p.doneCh)
	}
	return nil
}

func (p *mockProcess) sentMessages() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	cp := make([]string, len(p.sent))
	copy(cp, p.sent)
	return cp
}

type mockSpawner struct {
	mu        sync.Mutex
	processes []*mockProcess
	err       error
}

func (s *mockSpawner) Spawn(_ context.Context, prompt string) (Process, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err != nil {
		return nil, s.err
	}
	p := newMockProcess(prompt)
	s.processes = append(s.processes, p)
	return p, nil
}

func (s *mockSpawner) lastProcess() *mockProcess {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.processes) == 0 {
		return nil
	}
	return s.processes[len(s.processes)-1]
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func openSessionTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	if err := db.AutoMigrate(&models.DispatchSession{}, &models.TelegraphConversation{}); err != nil {
		t.Fatalf("migrate test db: %v", err)
	}
	return db
}

// ---------------------------------------------------------------------------
// NewSessionManager tests
// ---------------------------------------------------------------------------

func TestNewSessionManager_NilDB(t *testing.T) {
	_, err := NewSessionManager(SessionManagerOpts{Spawner: &mockSpawner{}})
	if err == nil {
		t.Fatal("expected error for nil DB")
	}
}

func TestNewSessionManager_NilSpawner(t *testing.T) {
	db := openSessionTestDB(t)
	_, err := NewSessionManager(SessionManagerOpts{DB: db})
	if err == nil {
		t.Fatal("expected error for nil spawner")
	}
}

func TestNewSessionManager_Success(t *testing.T) {
	db := openSessionTestDB(t)
	sm, err := NewSessionManager(SessionManagerOpts{
		DB:      db,
		Spawner: &mockSpawner{},
	})
	if err != nil {
		t.Fatalf("NewSessionManager: %v", err)
	}
	if sm == nil {
		t.Fatal("expected non-nil SessionManager")
	}
}

func TestNewSessionManager_DefaultTimeout(t *testing.T) {
	db := openSessionTestDB(t)
	sm, _ := NewSessionManager(SessionManagerOpts{
		DB:      db,
		Spawner: &mockSpawner{},
	})
	if sm.timeout != DefaultHeartbeatTimeout {
		t.Errorf("timeout = %v, want %v", sm.timeout, DefaultHeartbeatTimeout)
	}
}

// ---------------------------------------------------------------------------
// NewSession tests
// ---------------------------------------------------------------------------

func TestNewSession_Success(t *testing.T) {
	db := openSessionTestDB(t)
	spawner := &mockSpawner{}
	sm, _ := NewSessionManager(SessionManagerOpts{DB: db, Spawner: spawner})

	session, err := sm.NewSession(context.Background(), "telegraph", "alice", "thread-1", "C01")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if session.ID == 0 {
		t.Fatal("expected session ID to be set")
	}
	if session.Source != "telegraph" {
		t.Errorf("Source = %q, want %q", session.Source, "telegraph")
	}
	if !sm.HasSession("C01", "thread-1") {
		t.Error("HasSession should return true")
	}
}

func TestNewSession_SpawnFails(t *testing.T) {
	db := openSessionTestDB(t)
	spawner := &mockSpawner{err: fmt.Errorf("spawn failed")}
	sm, _ := NewSessionManager(SessionManagerOpts{DB: db, Spawner: spawner})

	_, err := sm.NewSession(context.Background(), "telegraph", "alice", "thread-1", "C01")
	if err == nil {
		t.Fatal("expected error when spawn fails")
	}
	if !strings.Contains(err.Error(), "spawn") {
		t.Errorf("error = %q, want to contain %q", err.Error(), "spawn")
	}

	// Lock should be released on spawn failure.
	if sm.HasSession("C01", "thread-1") {
		t.Error("session should not exist after spawn failure")
	}
}

func TestNewSession_LockConflict(t *testing.T) {
	db := openSessionTestDB(t)
	spawner := &mockSpawner{}
	sm, _ := NewSessionManager(SessionManagerOpts{DB: db, Spawner: spawner})

	sm.NewSession(context.Background(), "telegraph", "alice", "thread-1", "C01")

	// Second session on same thread should fail.
	_, err := sm.NewSession(context.Background(), "telegraph", "bob", "thread-1", "C01")
	if err == nil {
		t.Fatal("expected lock conflict error")
	}
}

// ---------------------------------------------------------------------------
// Route tests
// ---------------------------------------------------------------------------

func TestRoute_Success(t *testing.T) {
	db := openSessionTestDB(t)
	spawner := &mockSpawner{}
	sm, _ := NewSessionManager(SessionManagerOpts{DB: db, Spawner: spawner})
	sm.NewSession(context.Background(), "telegraph", "alice", "thread-1", "C01")

	err := sm.Route(context.Background(), "C01", "thread-1", "alice", "create a bug fix")
	if err != nil {
		t.Fatalf("Route: %v", err)
	}

	// Verify message was sent to subprocess.
	proc := spawner.lastProcess()
	sent := proc.sentMessages()
	if len(sent) != 1 || sent[0] != "create a bug fix" {
		t.Errorf("sent = %v, want [\"create a bug fix\"]", sent)
	}

	// Verify conversation was recorded.
	var conv models.TelegraphConversation
	db.Last(&conv)
	if conv.Content != "create a bug fix" {
		t.Errorf("Content = %q, want %q", conv.Content, "create a bug fix")
	}
	if conv.Sequence != 1 {
		t.Errorf("Sequence = %d, want 1", conv.Sequence)
	}
}

func TestRoute_MultipleMessages(t *testing.T) {
	db := openSessionTestDB(t)
	spawner := &mockSpawner{}
	sm, _ := NewSessionManager(SessionManagerOpts{DB: db, Spawner: spawner})
	sm.NewSession(context.Background(), "telegraph", "alice", "thread-1", "C01")

	sm.Route(context.Background(), "C01", "thread-1", "alice", "first message")
	sm.Route(context.Background(), "C01", "thread-1", "alice", "second message")

	// Verify sequence numbers increment.
	var convos []models.TelegraphConversation
	db.Order("sequence").Find(&convos)
	if len(convos) != 2 {
		t.Fatalf("conversation count = %d, want 2", len(convos))
	}
	if convos[0].Sequence != 1 {
		t.Errorf("first sequence = %d, want 1", convos[0].Sequence)
	}
	if convos[1].Sequence != 2 {
		t.Errorf("second sequence = %d, want 2", convos[1].Sequence)
	}
}

func TestRoute_NoActiveSession(t *testing.T) {
	db := openSessionTestDB(t)
	sm, _ := NewSessionManager(SessionManagerOpts{DB: db, Spawner: &mockSpawner{}})

	err := sm.Route(context.Background(), "C01", "thread-1", "alice", "hello")
	if err == nil {
		t.Fatal("expected error for no active session")
	}
	if !strings.Contains(err.Error(), "no active session") {
		t.Errorf("error = %q, want to contain %q", err.Error(), "no active session")
	}
}

// ---------------------------------------------------------------------------
// HasSession / HasHistoricSession tests
// ---------------------------------------------------------------------------

func TestHasSession_NotFound(t *testing.T) {
	db := openSessionTestDB(t)
	sm, _ := NewSessionManager(SessionManagerOpts{DB: db, Spawner: &mockSpawner{}})

	if sm.HasSession("C01", "thread-1") {
		t.Error("HasSession should return false for non-existent session")
	}
}

func TestHasHistoricSession_Found(t *testing.T) {
	db := openSessionTestDB(t)
	sm, _ := NewSessionManager(SessionManagerOpts{DB: db, Spawner: &mockSpawner{}})

	// Create a completed session in DB.
	now := time.Now()
	db.Create(&models.DispatchSession{
		Source:           "telegraph",
		UserName:         "alice",
		PlatformThreadID: "thread-1",
		ChannelID:        "C01",
		Status:           "completed",
		CarsCreated:      "[]",
		LastHeartbeat:    now,
		CompletedAt:      &now,
	})

	if !sm.HasHistoricSession("C01", "thread-1") {
		t.Error("HasHistoricSession should return true for completed session")
	}
}

func TestHasHistoricSession_ActiveNotHistoric(t *testing.T) {
	db := openSessionTestDB(t)
	sm, _ := NewSessionManager(SessionManagerOpts{DB: db, Spawner: &mockSpawner{}})

	// Active sessions should not count as historic.
	db.Create(&models.DispatchSession{
		Source:           "telegraph",
		UserName:         "alice",
		PlatformThreadID: "thread-1",
		ChannelID:        "C01",
		Status:           "active",
		CarsCreated:      "[]",
		LastHeartbeat:    time.Now(),
	})

	if sm.HasHistoricSession("C01", "thread-1") {
		t.Error("HasHistoricSession should return false for active session")
	}
}

func TestHasHistoricSession_NotFound(t *testing.T) {
	db := openSessionTestDB(t)
	sm, _ := NewSessionManager(SessionManagerOpts{DB: db, Spawner: &mockSpawner{}})

	if sm.HasHistoricSession("C01", "thread-1") {
		t.Error("HasHistoricSession should return false when no sessions exist")
	}
}

// ---------------------------------------------------------------------------
// Resume tests
// ---------------------------------------------------------------------------

func TestResume_WithDoltHistory(t *testing.T) {
	db := openSessionTestDB(t)
	spawner := &mockSpawner{}
	sm, _ := NewSessionManager(SessionManagerOpts{DB: db, Spawner: spawner})

	// Create a completed session with conversation history.
	now := time.Now()
	oldSession := models.DispatchSession{
		Source:           "telegraph",
		UserName:         "alice",
		PlatformThreadID: "thread-1",
		ChannelID:        "C01",
		Status:           "completed",
		CarsCreated:      "[]",
		LastHeartbeat:    now,
		CompletedAt:      &now,
	}
	db.Create(&oldSession)
	db.Create(&models.TelegraphConversation{
		SessionID: oldSession.ID,
		Sequence:  1,
		Role:      "user",
		UserName:  "alice",
		Content:   "create a task for auth",
	})
	db.Create(&models.TelegraphConversation{
		SessionID: oldSession.ID,
		Sequence:  2,
		Role:      "assistant",
		UserName:  "",
		Content:   "Created car-001 for auth feature",
	})

	// Resume should succeed.
	newSession, err := sm.Resume(context.Background(), "C01", "thread-1", "alice")
	if err != nil {
		t.Fatalf("Resume: %v", err)
	}
	if newSession.ID == 0 {
		t.Fatal("expected session ID")
	}

	// Verify the spawner received a recovery prompt with conversation history.
	proc := spawner.lastProcess()
	if !strings.Contains(proc.prompt, "Previous conversation context") {
		t.Error("recovery prompt should contain conversation context")
	}
	if !strings.Contains(proc.prompt, "create a task for auth") {
		t.Error("recovery prompt should contain original message")
	}
}

func TestResume_WithAdapterFallback(t *testing.T) {
	db := openSessionTestDB(t)
	spawner := &mockSpawner{}
	adapter := NewMockAdapter()
	adapter.Connect(context.Background())
	adapter.SetThreadHistory("C01", "thread-1", []ThreadMessage{
		{UserName: "alice", Text: "hey railyard"},
		{UserName: "bot", Text: "how can I help?"},
	})

	sm, _ := NewSessionManager(SessionManagerOpts{
		DB:      db,
		Spawner: spawner,
		Adapter: adapter,
	})

	// No Dolt history — should fall back to adapter.
	newSession, err := sm.Resume(context.Background(), "C01", "thread-1", "alice")
	if err != nil {
		t.Fatalf("Resume: %v", err)
	}
	if newSession.ID == 0 {
		t.Fatal("expected session ID")
	}

	proc := spawner.lastProcess()
	if !strings.Contains(proc.prompt, "Previous thread context") {
		t.Error("recovery prompt should use thread context fallback")
	}
	if !strings.Contains(proc.prompt, "hey railyard") {
		t.Error("recovery prompt should contain adapter thread history")
	}
}

func TestResume_NoHistory(t *testing.T) {
	db := openSessionTestDB(t)
	spawner := &mockSpawner{}
	sm, _ := NewSessionManager(SessionManagerOpts{DB: db, Spawner: spawner})

	// Resume with no history should still work (empty prompt).
	session, err := sm.Resume(context.Background(), "C01", "thread-new", "alice")
	if err != nil {
		t.Fatalf("Resume: %v", err)
	}
	if session.ID == 0 {
		t.Fatal("expected session ID")
	}
}

// ---------------------------------------------------------------------------
// CloseSession tests
// ---------------------------------------------------------------------------

func TestCloseSession_Success(t *testing.T) {
	db := openSessionTestDB(t)
	spawner := &mockSpawner{}
	sm, _ := NewSessionManager(SessionManagerOpts{DB: db, Spawner: spawner})
	sm.NewSession(context.Background(), "telegraph", "alice", "thread-1", "C01")

	err := sm.CloseSession("C01", "thread-1")
	if err != nil {
		t.Fatalf("CloseSession: %v", err)
	}
	if sm.HasSession("C01", "thread-1") {
		t.Error("session should be removed after close")
	}
}

func TestCloseSession_NotFound(t *testing.T) {
	db := openSessionTestDB(t)
	sm, _ := NewSessionManager(SessionManagerOpts{DB: db, Spawner: &mockSpawner{}})

	err := sm.CloseSession("C01", "thread-1")
	if err == nil {
		t.Fatal("expected error for non-existent session")
	}
}

// ---------------------------------------------------------------------------
// Process exit cleanup test
// ---------------------------------------------------------------------------

func TestProcessExit_CleansUpSession(t *testing.T) {
	db := openSessionTestDB(t)
	spawner := &mockSpawner{}
	sm, _ := NewSessionManager(SessionManagerOpts{DB: db, Spawner: spawner})
	sm.NewSession(context.Background(), "telegraph", "alice", "thread-1", "C01")

	proc := spawner.lastProcess()

	// Simulate process exit.
	proc.Close()

	// Give monitorProcess goroutine time to clean up.
	time.Sleep(50 * time.Millisecond)

	if sm.HasSession("C01", "thread-1") {
		t.Error("session should be cleaned up after process exit")
	}

	// DB session should be released.
	var dbSession models.DispatchSession
	db.Last(&dbSession)
	if dbSession.Status != "completed" {
		t.Errorf("DB session status = %q, want %q", dbSession.Status, "completed")
	}
}

// ---------------------------------------------------------------------------
// formatConversationHistory / formatThreadHistory tests
// ---------------------------------------------------------------------------

func TestFormatConversationHistory(t *testing.T) {
	convos := []models.TelegraphConversation{
		{Role: "user", UserName: "alice", Content: "hello"},
		{Role: "assistant", UserName: "", Content: "hi there"},
	}
	result := formatConversationHistory(convos)
	if !strings.Contains(result, "Previous conversation context") {
		t.Error("should contain header")
	}
	if !strings.Contains(result, "[user] alice: hello") {
		t.Error("should format user message")
	}
	if !strings.Contains(result, "[assistant] : hi there") {
		t.Error("should format assistant message")
	}
}

func TestFormatThreadHistory(t *testing.T) {
	msgs := []ThreadMessage{
		{UserName: "alice", Text: "hey"},
		{UserName: "bot", Text: "hello"},
	}
	result := formatThreadHistory(msgs)
	if !strings.Contains(result, "Previous thread context") {
		t.Error("should contain header")
	}
	if !strings.Contains(result, "alice: hey") {
		t.Error("should format thread message")
	}
}
