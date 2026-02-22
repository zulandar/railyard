package telegraph

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/zulandar/railyard/internal/models"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func openConvTestDB(t *testing.T) *gorm.DB {
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

func createTestSession(t *testing.T, db *gorm.DB, channelID, threadID string) *models.DispatchSession {
	t.Helper()
	s := &models.DispatchSession{
		Source:           "telegraph",
		UserName:         "alice",
		PlatformThreadID: threadID,
		ChannelID:        channelID,
		Status:           "active",
		CarsCreated:      "[]",
		LastHeartbeat:    time.Now(),
	}
	if err := db.Create(s).Error; err != nil {
		t.Fatalf("create test session: %v", err)
	}
	return s
}

// ---------------------------------------------------------------------------
// NewConversationStore tests
// ---------------------------------------------------------------------------

func TestNewConversationStore_NilDB(t *testing.T) {
	_, err := NewConversationStore(ConversationStoreOpts{})
	if err == nil {
		t.Fatal("expected error for nil DB")
	}
}

func TestNewConversationStore_Defaults(t *testing.T) {
	db := openConvTestDB(t)
	cs, err := NewConversationStore(ConversationStoreOpts{DB: db})
	if err != nil {
		t.Fatalf("NewConversationStore: %v", err)
	}
	if cs.maxTurnsPerSession != DefaultMaxTurnsPerSession {
		t.Errorf("maxTurnsPerSession = %d, want %d", cs.maxTurnsPerSession, DefaultMaxTurnsPerSession)
	}
	if cs.recoveryLookbackDays != DefaultRecoveryLookbackDays {
		t.Errorf("recoveryLookbackDays = %d, want %d", cs.recoveryLookbackDays, DefaultRecoveryLookbackDays)
	}
}

func TestNewConversationStore_CustomConfig(t *testing.T) {
	db := openConvTestDB(t)
	cs, _ := NewConversationStore(ConversationStoreOpts{
		DB:                   db,
		MaxTurnsPerSession:   50,
		RecoveryLookbackDays: 7,
	})
	if cs.maxTurnsPerSession != 50 {
		t.Errorf("maxTurnsPerSession = %d, want 50", cs.maxTurnsPerSession)
	}
	if cs.recoveryLookbackDays != 7 {
		t.Errorf("recoveryLookbackDays = %d, want 7", cs.recoveryLookbackDays)
	}
}

// ---------------------------------------------------------------------------
// WriteUserMessage tests
// ---------------------------------------------------------------------------

func TestWriteUserMessage_Success(t *testing.T) {
	db := openConvTestDB(t)
	cs, _ := NewConversationStore(ConversationStoreOpts{DB: db})
	session := createTestSession(t, db, "C01", "thread-1")

	err := cs.WriteUserMessage(context.Background(), session.ID, "alice", "hello world", "msg-1")
	if err != nil {
		t.Fatalf("WriteUserMessage: %v", err)
	}

	var conv models.TelegraphConversation
	db.Last(&conv)
	if conv.Role != "user" {
		t.Errorf("Role = %q, want %q", conv.Role, "user")
	}
	if conv.UserName != "alice" {
		t.Errorf("UserName = %q, want %q", conv.UserName, "alice")
	}
	if conv.Content != "hello world" {
		t.Errorf("Content = %q, want %q", conv.Content, "hello world")
	}
	if conv.Sequence != 1 {
		t.Errorf("Sequence = %d, want 1", conv.Sequence)
	}
	if conv.PlatformMsgID != "msg-1" {
		t.Errorf("PlatformMsgID = %q, want %q", conv.PlatformMsgID, "msg-1")
	}
}

func TestWriteUserMessage_DualWrite(t *testing.T) {
	db := openConvTestDB(t)
	adapter := NewMockAdapter()
	adapter.Connect(context.Background())

	cs, _ := NewConversationStore(ConversationStoreOpts{DB: db, Adapter: adapter})
	session := createTestSession(t, db, "C01", "thread-1")

	cs.WriteUserMessage(context.Background(), session.ID, "alice", "hello", "")

	// Verify dual-write to adapter.
	if adapter.SentCount() != 1 {
		t.Fatalf("adapter SentCount = %d, want 1", adapter.SentCount())
	}
	sent, _ := adapter.LastSent()
	if sent.ChannelID != "C01" {
		t.Errorf("ChannelID = %q, want %q", sent.ChannelID, "C01")
	}
	if sent.ThreadID != "thread-1" {
		t.Errorf("ThreadID = %q, want %q", sent.ThreadID, "thread-1")
	}
	if !strings.Contains(sent.Text, "alice") || !strings.Contains(sent.Text, "hello") {
		t.Errorf("Text = %q, want to contain user and message", sent.Text)
	}
}

func TestWriteUserMessage_NoDualWriteWithoutAdapter(t *testing.T) {
	db := openConvTestDB(t)
	cs, _ := NewConversationStore(ConversationStoreOpts{DB: db})
	session := createTestSession(t, db, "C01", "thread-1")

	// Should not panic without adapter.
	err := cs.WriteUserMessage(context.Background(), session.ID, "alice", "hello", "")
	if err != nil {
		t.Fatalf("WriteUserMessage: %v", err)
	}
}

// ---------------------------------------------------------------------------
// WriteAssistantMessage tests
// ---------------------------------------------------------------------------

func TestWriteAssistantMessage_Success(t *testing.T) {
	db := openConvTestDB(t)
	cs, _ := NewConversationStore(ConversationStoreOpts{DB: db})
	session := createTestSession(t, db, "C01", "thread-1")

	err := cs.WriteAssistantMessage(context.Background(), session.ID, "I created car-001", "msg-2", []string{"car-001"})
	if err != nil {
		t.Fatalf("WriteAssistantMessage: %v", err)
	}

	var conv models.TelegraphConversation
	db.Last(&conv)
	if conv.Role != "assistant" {
		t.Errorf("Role = %q, want %q", conv.Role, "assistant")
	}
	if conv.Content != "I created car-001" {
		t.Errorf("Content = %q, want %q", conv.Content, "I created car-001")
	}
	if conv.CarsReferenced != `["car-001"]` {
		t.Errorf("CarsReferenced = %q, want %q", conv.CarsReferenced, `["car-001"]`)
	}
}

func TestWriteAssistantMessage_MultipleCars(t *testing.T) {
	db := openConvTestDB(t)
	cs, _ := NewConversationStore(ConversationStoreOpts{DB: db})
	session := createTestSession(t, db, "C01", "thread-1")

	cs.WriteAssistantMessage(context.Background(), session.ID, "done", "", []string{"car-001", "car-002"})

	var conv models.TelegraphConversation
	db.Last(&conv)
	if conv.CarsReferenced != `["car-001","car-002"]` {
		t.Errorf("CarsReferenced = %q, want %q", conv.CarsReferenced, `["car-001","car-002"]`)
	}
}

func TestWriteAssistantMessage_NoCars(t *testing.T) {
	db := openConvTestDB(t)
	cs, _ := NewConversationStore(ConversationStoreOpts{DB: db})
	session := createTestSession(t, db, "C01", "thread-1")

	cs.WriteAssistantMessage(context.Background(), session.ID, "done", "", nil)

	var conv models.TelegraphConversation
	db.Last(&conv)
	if conv.CarsReferenced != "[]" {
		t.Errorf("CarsReferenced = %q, want %q", conv.CarsReferenced, "[]")
	}
}

func TestWriteAssistantMessage_DualWrite(t *testing.T) {
	db := openConvTestDB(t)
	adapter := NewMockAdapter()
	adapter.Connect(context.Background())
	cs, _ := NewConversationStore(ConversationStoreOpts{DB: db, Adapter: adapter})
	session := createTestSession(t, db, "C01", "thread-1")

	cs.WriteAssistantMessage(context.Background(), session.ID, "Created car-001", "", nil)

	if adapter.SentCount() != 1 {
		t.Fatalf("adapter SentCount = %d, want 1", adapter.SentCount())
	}
	sent, _ := adapter.LastSent()
	if sent.Text != "Created car-001" {
		t.Errorf("Text = %q, want %q", sent.Text, "Created car-001")
	}
}

// ---------------------------------------------------------------------------
// Sequence numbering tests
// ---------------------------------------------------------------------------

func TestSequenceNumbering_Monotonic(t *testing.T) {
	db := openConvTestDB(t)
	cs, _ := NewConversationStore(ConversationStoreOpts{DB: db})
	session := createTestSession(t, db, "C01", "thread-1")

	cs.WriteUserMessage(context.Background(), session.ID, "alice", "msg 1", "")
	cs.WriteAssistantMessage(context.Background(), session.ID, "resp 1", "", nil)
	cs.WriteUserMessage(context.Background(), session.ID, "alice", "msg 2", "")

	convos, _ := cs.LoadHistory(session.ID)
	if len(convos) != 3 {
		t.Fatalf("count = %d, want 3", len(convos))
	}
	for i, c := range convos {
		want := i + 1
		if c.Sequence != want {
			t.Errorf("convos[%d].Sequence = %d, want %d", i, c.Sequence, want)
		}
	}
}

// ---------------------------------------------------------------------------
// MaxTurns tests
// ---------------------------------------------------------------------------

func TestMaxTurns_UserMessageExceeded(t *testing.T) {
	db := openConvTestDB(t)
	cs, _ := NewConversationStore(ConversationStoreOpts{
		DB:                 db,
		MaxTurnsPerSession: 2,
	})
	session := createTestSession(t, db, "C01", "thread-1")

	cs.WriteUserMessage(context.Background(), session.ID, "alice", "msg 1", "")
	cs.WriteAssistantMessage(context.Background(), session.ID, "resp 1", "", nil)

	// Third message should exceed max turns.
	err := cs.WriteUserMessage(context.Background(), session.ID, "alice", "msg 2", "")
	if err == nil {
		t.Fatal("expected max turns error")
	}
	if !strings.Contains(err.Error(), "max turns exceeded") {
		t.Errorf("error = %q, want to contain %q", err.Error(), "max turns exceeded")
	}
}

func TestMaxTurns_AssistantMessageExceeded(t *testing.T) {
	db := openConvTestDB(t)
	cs, _ := NewConversationStore(ConversationStoreOpts{
		DB:                 db,
		MaxTurnsPerSession: 2,
	})
	session := createTestSession(t, db, "C01", "thread-1")

	cs.WriteUserMessage(context.Background(), session.ID, "alice", "msg 1", "")
	cs.WriteAssistantMessage(context.Background(), session.ID, "resp 1", "", nil)

	err := cs.WriteAssistantMessage(context.Background(), session.ID, "resp 2", "", nil)
	if err == nil {
		t.Fatal("expected max turns error")
	}
}

func TestMaxTurns_AtLimit(t *testing.T) {
	db := openConvTestDB(t)
	cs, _ := NewConversationStore(ConversationStoreOpts{
		DB:                 db,
		MaxTurnsPerSession: 3,
	})
	session := createTestSession(t, db, "C01", "thread-1")

	// Exactly 3 messages should succeed.
	cs.WriteUserMessage(context.Background(), session.ID, "alice", "1", "")
	cs.WriteAssistantMessage(context.Background(), session.ID, "2", "", nil)
	err := cs.WriteUserMessage(context.Background(), session.ID, "alice", "3", "")
	if err != nil {
		t.Fatalf("third message should succeed at limit: %v", err)
	}

	// Fourth should fail.
	err = cs.WriteAssistantMessage(context.Background(), session.ID, "4", "", nil)
	if err == nil {
		t.Fatal("expected max turns error")
	}
}

// ---------------------------------------------------------------------------
// LoadHistory tests
// ---------------------------------------------------------------------------

func TestLoadHistory_Success(t *testing.T) {
	db := openConvTestDB(t)
	cs, _ := NewConversationStore(ConversationStoreOpts{DB: db})
	session := createTestSession(t, db, "C01", "thread-1")

	cs.WriteUserMessage(context.Background(), session.ID, "alice", "hello", "")
	cs.WriteAssistantMessage(context.Background(), session.ID, "hi", "", nil)

	convos, err := cs.LoadHistory(session.ID)
	if err != nil {
		t.Fatalf("LoadHistory: %v", err)
	}
	if len(convos) != 2 {
		t.Fatalf("count = %d, want 2", len(convos))
	}
	if convos[0].Role != "user" {
		t.Errorf("first role = %q, want %q", convos[0].Role, "user")
	}
	if convos[1].Role != "assistant" {
		t.Errorf("second role = %q, want %q", convos[1].Role, "assistant")
	}
}

func TestLoadHistory_EmptySession(t *testing.T) {
	db := openConvTestDB(t)
	cs, _ := NewConversationStore(ConversationStoreOpts{DB: db})
	session := createTestSession(t, db, "C01", "thread-1")

	convos, err := cs.LoadHistory(session.ID)
	if err != nil {
		t.Fatalf("LoadHistory: %v", err)
	}
	if len(convos) != 0 {
		t.Errorf("count = %d, want 0", len(convos))
	}
}

func TestLoadHistory_IsolatedSessions(t *testing.T) {
	db := openConvTestDB(t)
	cs, _ := NewConversationStore(ConversationStoreOpts{DB: db})
	s1 := createTestSession(t, db, "C01", "thread-1")
	s2 := createTestSession(t, db, "C01", "thread-2")

	cs.WriteUserMessage(context.Background(), s1.ID, "alice", "s1 msg", "")
	cs.WriteUserMessage(context.Background(), s2.ID, "bob", "s2 msg", "")

	convos, _ := cs.LoadHistory(s1.ID)
	if len(convos) != 1 {
		t.Fatalf("count = %d, want 1", len(convos))
	}
	if convos[0].Content != "s1 msg" {
		t.Errorf("Content = %q, want %q", convos[0].Content, "s1 msg")
	}
}

// ---------------------------------------------------------------------------
// RecoverFromThread tests
// ---------------------------------------------------------------------------

func TestRecoverFromThread_DoltPrimary(t *testing.T) {
	db := openConvTestDB(t)
	cs, _ := NewConversationStore(ConversationStoreOpts{DB: db})
	session := createTestSession(t, db, "C01", "thread-1")

	cs.WriteUserMessage(context.Background(), session.ID, "alice", "msg 1", "")
	cs.WriteAssistantMessage(context.Background(), session.ID, "resp 1", "", nil)

	// Mark session completed so it's historic.
	now := time.Now()
	db.Model(session).Updates(map[string]interface{}{"status": "completed", "completed_at": now})

	convos, err := cs.RecoverFromThread(context.Background(), "C01", "thread-1")
	if err != nil {
		t.Fatalf("RecoverFromThread: %v", err)
	}
	if len(convos) != 2 {
		t.Fatalf("count = %d, want 2", len(convos))
	}
}

func TestRecoverFromThread_AdapterFallback(t *testing.T) {
	db := openConvTestDB(t)
	adapter := NewMockAdapter()
	adapter.Connect(context.Background())
	adapter.SetThreadHistory("C01", "thread-1", []ThreadMessage{
		{UserName: "alice", Text: "hello from slack", Timestamp: time.Now()},
		{UserName: "bot", Text: "hi there", Timestamp: time.Now()},
	})

	cs, _ := NewConversationStore(ConversationStoreOpts{DB: db, Adapter: adapter})

	// No Dolt records — should fall back to adapter.
	convos, err := cs.RecoverFromThread(context.Background(), "C01", "thread-1")
	if err != nil {
		t.Fatalf("RecoverFromThread: %v", err)
	}
	if len(convos) != 2 {
		t.Fatalf("count = %d, want 2", len(convos))
	}
	if convos[0].UserName != "alice" {
		t.Errorf("UserName = %q, want %q", convos[0].UserName, "alice")
	}
	if convos[0].Content != "hello from slack" {
		t.Errorf("Content = %q, want %q", convos[0].Content, "hello from slack")
	}
}

func TestRecoverFromThread_NoHistory(t *testing.T) {
	db := openConvTestDB(t)
	cs, _ := NewConversationStore(ConversationStoreOpts{DB: db})

	convos, err := cs.RecoverFromThread(context.Background(), "C01", "thread-unknown")
	if err != nil {
		t.Fatalf("RecoverFromThread: %v", err)
	}
	if len(convos) != 0 {
		t.Errorf("count = %d, want 0", len(convos))
	}
}

func TestRecoverFromThread_LookbackFilter(t *testing.T) {
	db := openConvTestDB(t)
	cs, _ := NewConversationStore(ConversationStoreOpts{
		DB:                   db,
		RecoveryLookbackDays: 7,
	})

	// Create an old session outside the lookback window.
	oldTime := time.Now().AddDate(0, 0, -30)
	oldSession := &models.DispatchSession{
		Source:           "telegraph",
		UserName:         "alice",
		PlatformThreadID: "thread-1",
		ChannelID:        "C01",
		Status:           "completed",
		CarsCreated:      "[]",
		LastHeartbeat:    oldTime,
		CompletedAt:      &oldTime,
	}
	db.Create(oldSession)
	// Manually set CreatedAt to old date (GORM auto-sets it).
	db.Model(oldSession).Update("created_at", oldTime)

	db.Create(&models.TelegraphConversation{
		SessionID: oldSession.ID,
		Sequence:  1,
		Role:      "user",
		UserName:  "alice",
		Content:   "old message",
	})

	// Old session should be filtered out by 7-day lookback.
	convos, err := cs.RecoverFromThread(context.Background(), "C01", "thread-1")
	if err != nil {
		t.Fatalf("RecoverFromThread: %v", err)
	}
	if len(convos) != 0 {
		t.Errorf("count = %d, want 0 (old session filtered)", len(convos))
	}

	// Create a recent session within the lookback window.
	recentSession := createTestSession(t, db, "C01", "thread-1")
	db.Model(recentSession).Update("status", "completed")
	db.Create(&models.TelegraphConversation{
		SessionID: recentSession.ID,
		Sequence:  1,
		Role:      "user",
		UserName:  "alice",
		Content:   "recent message",
	})

	convos, err = cs.RecoverFromThread(context.Background(), "C01", "thread-1")
	if err != nil {
		t.Fatalf("RecoverFromThread: %v", err)
	}
	if len(convos) != 1 {
		t.Fatalf("count = %d, want 1 (only recent)", len(convos))
	}
	if convos[0].Content != "recent message" {
		t.Errorf("Content = %q, want %q", convos[0].Content, "recent message")
	}
}

// ---------------------------------------------------------------------------
// TurnCount tests
// ---------------------------------------------------------------------------

func TestTurnCount(t *testing.T) {
	db := openConvTestDB(t)
	cs, _ := NewConversationStore(ConversationStoreOpts{DB: db})
	session := createTestSession(t, db, "C01", "thread-1")

	count, _ := cs.TurnCount(session.ID)
	if count != 0 {
		t.Errorf("initial count = %d, want 0", count)
	}

	cs.WriteUserMessage(context.Background(), session.ID, "alice", "hello", "")
	cs.WriteAssistantMessage(context.Background(), session.ID, "hi", "", nil)

	count, _ = cs.TurnCount(session.ID)
	if count != 2 {
		t.Errorf("count = %d, want 2", count)
	}
}

// ---------------------------------------------------------------------------
// RecoverFromThread with multiple sessions
// ---------------------------------------------------------------------------

func TestRecoverFromThread_MultipleSessions(t *testing.T) {
	db := openConvTestDB(t)
	cs, _ := NewConversationStore(ConversationStoreOpts{DB: db})

	// Create two completed sessions on the same thread.
	now := time.Now()
	s1 := createTestSession(t, db, "C01", "thread-1")
	db.Model(s1).Updates(map[string]interface{}{"status": "completed", "completed_at": now})
	db.Create(&models.TelegraphConversation{SessionID: s1.ID, Sequence: 1, Role: "user", Content: "first session"})

	s2 := createTestSession(t, db, "C01", "thread-1")
	db.Model(s2).Updates(map[string]interface{}{"status": "completed", "completed_at": now})
	db.Create(&models.TelegraphConversation{SessionID: s2.ID, Sequence: 1, Role: "user", Content: "second session"})

	convos, err := cs.RecoverFromThread(context.Background(), "C01", "thread-1")
	if err != nil {
		t.Fatalf("RecoverFromThread: %v", err)
	}
	// Should return conversations from both sessions.
	if len(convos) != 2 {
		t.Fatalf("count = %d, want 2", len(convos))
	}
}
