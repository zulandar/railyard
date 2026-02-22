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

	// Active sessions with fresh heartbeat should not count as historic.
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
		t.Error("HasHistoricSession should return false for active session with fresh heartbeat")
	}
}

func TestHasHistoricSession_StaleActiveIsHistoric(t *testing.T) {
	db := openSessionTestDB(t)
	sm, _ := NewSessionManager(SessionManagerOpts{DB: db, Spawner: &mockSpawner{}})

	// Active sessions with stale heartbeat (orphaned) should count as historic.
	// This covers the case where monitorProcess cleaned up the in-memory map
	// but ReleaseLock failed, leaving the DB record in "active" status.
	staleTime := time.Now().Add(-2 * DefaultHeartbeatTimeout)
	db.Create(&models.DispatchSession{
		Source:           "telegraph",
		UserName:         "alice",
		PlatformThreadID: "thread-1",
		ChannelID:        "C01",
		Status:           "active",
		CarsCreated:      "[]",
		LastHeartbeat:    staleTime,
	})

	if !sm.HasHistoricSession("C01", "thread-1") {
		t.Error("HasHistoricSession should return true for active session with stale heartbeat")
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
	newSession, err := sm.Resume(context.Background(), "C01", "thread-1", "alice", "continue where we left off")
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
	if !strings.Contains(proc.prompt, "continue where we left off") {
		t.Error("recovery prompt should contain the new message")
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
	newSession, err := sm.Resume(context.Background(), "C01", "thread-1", "alice", "pick up where we left off")
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
	session, err := sm.Resume(context.Background(), "C01", "thread-new", "alice", "hello")
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

// ---------------------------------------------------------------------------
// relayOutput tests
// ---------------------------------------------------------------------------

func TestRelayOutput_SendsToAdapter(t *testing.T) {
	db := openSessionTestDB(t)
	adapter := NewMockAdapter()
	adapter.Connect(context.Background())
	spawner := &mockSpawner{}

	sm, _ := NewSessionManager(SessionManagerOpts{
		DB:      db,
		Spawner: spawner,
		Adapter: adapter,
	})

	proc := newMockProcess("")
	// Simulate process output.
	proc.recvCh <- "Hello from dispatch"
	proc.recvCh <- "Created car-001"
	close(proc.recvCh)

	sm.relayOutput(context.Background(), "C01", "thread-1", 1, proc)

	sent := adapter.AllSent()
	if len(sent) != 1 {
		t.Fatalf("sent count = %d, want 1", len(sent))
	}
	if sent[0].ChannelID != "C01" {
		t.Errorf("ChannelID = %q, want %q", sent[0].ChannelID, "C01")
	}
	if sent[0].ThreadID != "thread-1" {
		t.Errorf("ThreadID = %q, want %q", sent[0].ThreadID, "thread-1")
	}
	expected := "Hello from dispatch\nCreated car-001"
	if sent[0].Text != expected {
		t.Errorf("Text = %q, want %q", sent[0].Text, expected)
	}

	// Verify conversation was recorded.
	var conv models.TelegraphConversation
	db.Last(&conv)
	if conv.Role != "assistant" {
		t.Errorf("Role = %q, want %q", conv.Role, "assistant")
	}
	if conv.Content != expected {
		t.Errorf("Content = %q, want %q", conv.Content, expected)
	}
}

func TestRelayOutput_ChunksLongMessages(t *testing.T) {
	db := openSessionTestDB(t)
	adapter := NewMockAdapter()
	adapter.Connect(context.Background())
	spawner := &mockSpawner{}

	sm, _ := NewSessionManager(SessionManagerOpts{
		DB:      db,
		Spawner: spawner,
		Adapter: adapter,
	})

	// Create a message that's longer than 2000 chars.
	proc := newMockProcess("")
	longLine := strings.Repeat("a", 1500)
	proc.recvCh <- longLine
	proc.recvCh <- longLine
	close(proc.recvCh)

	sm.relayOutput(context.Background(), "C01", "thread-1", 1, proc)

	sent := adapter.AllSent()
	if len(sent) < 2 {
		t.Fatalf("sent count = %d, want >= 2 (message should be chunked)", len(sent))
	}
	for i, msg := range sent {
		if len(msg.Text) > 2000 {
			t.Errorf("chunk %d length = %d, want <= 2000", i, len(msg.Text))
		}
	}
}

func TestRelayOutput_EmptyOutput(t *testing.T) {
	db := openSessionTestDB(t)
	adapter := NewMockAdapter()
	adapter.Connect(context.Background())
	spawner := &mockSpawner{}

	sm, _ := NewSessionManager(SessionManagerOpts{
		DB:      db,
		Spawner: spawner,
		Adapter: adapter,
	})

	proc := newMockProcess("")
	close(proc.recvCh) // no output

	sm.relayOutput(context.Background(), "C01", "thread-1", 1, proc)

	if adapter.SentCount() != 0 {
		t.Errorf("sent count = %d, want 0 for empty output", adapter.SentCount())
	}
}

// ---------------------------------------------------------------------------
// chunkMessage tests
// ---------------------------------------------------------------------------

// ---------------------------------------------------------------------------
// ClearSessionHistory tests
// ---------------------------------------------------------------------------

func TestClearSessionHistory_WithData(t *testing.T) {
	db := openSessionTestDB(t)

	// Create telegraph sessions with conversations.
	now := time.Now()
	s1 := models.DispatchSession{
		Source: "telegraph", UserName: "alice", PlatformThreadID: "t1",
		ChannelID: "C01", Status: "completed", CarsCreated: "[]",
		LastHeartbeat: now, CompletedAt: &now,
	}
	s2 := models.DispatchSession{
		Source: "telegraph", UserName: "bob", PlatformThreadID: "t2",
		ChannelID: "C02", Status: "active", CarsCreated: "[]",
		LastHeartbeat: now,
	}
	db.Create(&s1)
	db.Create(&s2)
	db.Create(&models.TelegraphConversation{SessionID: s1.ID, Sequence: 1, Role: "user", Content: "hello"})
	db.Create(&models.TelegraphConversation{SessionID: s1.ID, Sequence: 2, Role: "assistant", Content: "hi"})
	db.Create(&models.TelegraphConversation{SessionID: s2.ID, Sequence: 1, Role: "user", Content: "hey"})

	sessions, convos, err := ClearSessionHistory(db)
	if err != nil {
		t.Fatalf("ClearSessionHistory: %v", err)
	}
	if sessions != 2 {
		t.Errorf("sessions deleted = %d, want 2", sessions)
	}
	if convos != 3 {
		t.Errorf("conversations deleted = %d, want 3", convos)
	}

	// Verify DB is empty.
	var sessionCount, convoCount int64
	db.Model(&models.DispatchSession{}).Count(&sessionCount)
	db.Model(&models.TelegraphConversation{}).Count(&convoCount)
	if sessionCount != 0 {
		t.Errorf("remaining sessions = %d, want 0", sessionCount)
	}
	if convoCount != 0 {
		t.Errorf("remaining conversations = %d, want 0", convoCount)
	}
}

func TestClearSessionHistory_EmptyDB(t *testing.T) {
	db := openSessionTestDB(t)

	sessions, convos, err := ClearSessionHistory(db)
	if err != nil {
		t.Fatalf("ClearSessionHistory: %v", err)
	}
	if sessions != 0 {
		t.Errorf("sessions deleted = %d, want 0", sessions)
	}
	if convos != 0 {
		t.Errorf("conversations deleted = %d, want 0", convos)
	}
}

func TestClearSessionHistory_PreservesNonTelegraphSessions(t *testing.T) {
	db := openSessionTestDB(t)

	now := time.Now()
	// Telegraph session — should be deleted.
	tgSession := models.DispatchSession{
		Source: "telegraph", UserName: "alice", PlatformThreadID: "t1",
		ChannelID: "C01", Status: "completed", CarsCreated: "[]",
		LastHeartbeat: now, CompletedAt: &now,
	}
	// Local session — should be preserved.
	localSession := models.DispatchSession{
		Source: "local", UserName: "bob", PlatformThreadID: "",
		ChannelID: "", Status: "completed", CarsCreated: "[]",
		LastHeartbeat: now, CompletedAt: &now,
	}
	db.Create(&tgSession)
	db.Create(&localSession)
	db.Create(&models.TelegraphConversation{SessionID: tgSession.ID, Sequence: 1, Role: "user", Content: "hello"})

	sessions, convos, err := ClearSessionHistory(db)
	if err != nil {
		t.Fatalf("ClearSessionHistory: %v", err)
	}
	if sessions != 1 {
		t.Errorf("sessions deleted = %d, want 1", sessions)
	}
	if convos != 1 {
		t.Errorf("conversations deleted = %d, want 1", convos)
	}

	// Local session should still exist.
	var remaining []models.DispatchSession
	db.Find(&remaining)
	if len(remaining) != 1 {
		t.Fatalf("remaining sessions = %d, want 1", len(remaining))
	}
	if remaining[0].Source != "local" {
		t.Errorf("remaining session source = %q, want %q", remaining[0].Source, "local")
	}
}

func TestChunkMessage(t *testing.T) {
	tests := []struct {
		name    string
		text    string
		maxLen  int
		wantN   int
		wantAll bool // if true, verify all chunks <= maxLen
	}{
		{
			name:   "short message",
			text:   "hello",
			maxLen: 2000,
			wantN:  1,
		},
		{
			name:   "exactly at limit",
			text:   strings.Repeat("x", 2000),
			maxLen: 2000,
			wantN:  1,
		},
		{
			name:    "just over limit",
			text:    strings.Repeat("x", 2001),
			maxLen:  2000,
			wantN:   2,
			wantAll: true,
		},
		{
			name:    "break at newline",
			text:    strings.Repeat("x", 1500) + "\n" + strings.Repeat("y", 1500),
			maxLen:  2000,
			wantN:   2,
			wantAll: true,
		},
		{
			name:    "multiple chunks",
			text:    strings.Repeat("x", 5000),
			maxLen:  2000,
			wantN:   3,
			wantAll: true,
		},
		{
			name:   "empty text",
			text:   "",
			maxLen: 2000,
			wantN:  1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			chunks := chunkMessage(tt.text, tt.maxLen)
			if len(chunks) != tt.wantN {
				t.Errorf("chunks = %d, want %d", len(chunks), tt.wantN)
			}
			if tt.wantAll {
				for i, c := range chunks {
					if len(c) > tt.maxLen {
						t.Errorf("chunk[%d] len = %d, want <= %d", i, len(c), tt.maxLen)
					}
				}
			}
			// Verify no data is lost (rejoin should equal original minus newline splits).
			joined := strings.Join(chunks, "\n")
			if !tt.wantAll && len(chunks) == 1 && chunks[0] != tt.text {
				t.Errorf("single chunk doesn't match original")
			}
			if tt.wantAll && len(joined) < len(tt.text)-len(chunks) {
				t.Errorf("data lost: joined len = %d, original len = %d", len(joined), len(tt.text))
			}
		})
	}
}
