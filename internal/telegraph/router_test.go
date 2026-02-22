package telegraph

import (
	"bytes"
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/zulandar/railyard/internal/models"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func openRouterTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	if err := db.AutoMigrate(
		&models.Car{},
		&models.CarDep{},
		&models.CarProgress{},
		&models.Engine{},
		&models.Message{},
		&models.Track{},
		&models.DispatchSession{},
		&models.TelegraphConversation{},
	); err != nil {
		t.Fatalf("auto migrate: %v", err)
	}
	return db
}

func setupRouter(t *testing.T, db *gorm.DB, botUserID string) (*Router, *MockAdapter, *mockSpawner) {
	t.Helper()
	adapter := NewMockAdapter()
	adapter.Connect(context.Background())
	spawner := &mockSpawner{}

	sm, err := NewSessionManager(SessionManagerOpts{
		DB:      db,
		Adapter: adapter,
		Spawner: spawner,
	})
	if err != nil {
		t.Fatalf("new session manager: %v", err)
	}

	cmdHandler, err := NewCommandHandler(CommandHandlerOpts{DB: db})
	if err != nil {
		t.Fatalf("new command handler: %v", err)
	}

	var out bytes.Buffer
	router, err := NewRouter(RouterOpts{
		SessionMgr: sm,
		CmdHandler: cmdHandler,
		Adapter:    adapter,
		BotUserID:  botUserID,
		Out:        &out,
	})
	if err != nil {
		t.Fatalf("new router: %v", err)
	}
	return router, adapter, spawner
}

// --- NewRouter tests ---

func TestNewRouter_NilSessionMgr(t *testing.T) {
	_, err := NewRouter(RouterOpts{
		CmdHandler: &CommandHandler{},
		Adapter:    NewMockAdapter(),
	})
	if err == nil {
		t.Fatal("expected error for nil session manager")
	}
}

func TestNewRouter_NilCmdHandler(t *testing.T) {
	db := openRouterTestDB(t)
	sm, _ := NewSessionManager(SessionManagerOpts{
		DB:      db,
		Spawner: &mockSpawner{},
	})
	_, err := NewRouter(RouterOpts{
		SessionMgr: sm,
		Adapter:    NewMockAdapter(),
	})
	if err == nil {
		t.Fatal("expected error for nil command handler")
	}
}

func TestNewRouter_NilAdapter(t *testing.T) {
	db := openRouterTestDB(t)
	sm, _ := NewSessionManager(SessionManagerOpts{
		DB:      db,
		Spawner: &mockSpawner{},
	})
	ch, _ := NewCommandHandler(CommandHandlerOpts{DB: db})
	_, err := NewRouter(RouterOpts{
		SessionMgr: sm,
		CmdHandler: ch,
	})
	if err == nil {
		t.Fatal("expected error for nil adapter")
	}
}

func TestNewRouter_Success(t *testing.T) {
	db := openRouterTestDB(t)
	router, _, _ := setupRouter(t, db, "bot-123")
	if router == nil {
		t.Fatal("expected non-nil router")
	}
}

// --- Self-message filtering ---

func TestHandle_IgnoresSelfMessage(t *testing.T) {
	db := openRouterTestDB(t)
	router, adapter, _ := setupRouter(t, db, "bot-123")

	router.Handle(context.Background(), InboundMessage{
		UserID:    "bot-123",
		UserName:  "railyard-bot",
		ChannelID: "C1",
		Text:      "@railyard deploy",
	})

	// Bot self-message should be ignored — no outbound messages.
	if adapter.SentCount() != 0 {
		t.Errorf("expected 0 sent messages for self-message, got %d", adapter.SentCount())
	}
}

func TestHandle_EmptyBotID_NoFiltering(t *testing.T) {
	db := openRouterTestDB(t)
	router, adapter, _ := setupRouter(t, db, "")

	// With empty bot ID, messages should not be filtered.
	router.Handle(context.Background(), InboundMessage{
		UserID:    "user-1",
		UserName:  "alice",
		ChannelID: "C1",
		Text:      "!ry help",
	})

	// Should handle as command.
	if adapter.SentCount() == 0 {
		t.Error("expected command response, got 0 sent messages")
	}
}

// --- Command routing ---

func TestHandle_CommandRouting(t *testing.T) {
	db := openRouterTestDB(t)
	router, adapter, _ := setupRouter(t, db, "bot-123")

	router.Handle(context.Background(), InboundMessage{
		UserID:    "user-1",
		UserName:  "alice",
		ChannelID: "C1",
		Text:      "!ry status",
	})

	if adapter.SentCount() != 1 {
		t.Fatalf("expected 1 sent message for command, got %d", adapter.SentCount())
	}
	msg, _ := adapter.LastSent()
	if msg.ChannelID != "C1" {
		t.Errorf("response channel = %q, want C1", msg.ChannelID)
	}
}

func TestHandle_CommandInThread(t *testing.T) {
	db := openRouterTestDB(t)
	router, adapter, _ := setupRouter(t, db, "bot-123")

	router.Handle(context.Background(), InboundMessage{
		UserID:    "user-1",
		UserName:  "alice",
		ChannelID: "C1",
		ThreadID:  "T1",
		Text:      "!ry help",
	})

	if adapter.SentCount() != 1 {
		t.Fatalf("expected 1 sent message, got %d", adapter.SentCount())
	}
	msg, _ := adapter.LastSent()
	// Response should be sent to the same thread.
	if msg.ChannelID != "C1" {
		t.Errorf("response channel = %q, want C1", msg.ChannelID)
	}
	if msg.ThreadID != "T1" {
		t.Errorf("response thread = %q, want T1", msg.ThreadID)
	}
}

func TestHandle_BareCommandPrefix(t *testing.T) {
	db := openRouterTestDB(t)
	router, adapter, _ := setupRouter(t, db, "bot-123")

	router.Handle(context.Background(), InboundMessage{
		UserID:    "user-1",
		ChannelID: "C1",
		Text:      "!ry",
	})

	if adapter.SentCount() != 1 {
		t.Fatalf("expected help response, got %d", adapter.SentCount())
	}
	msg, _ := adapter.LastSent()
	if msg.Text == "" {
		t.Error("expected non-empty help response")
	}
}

// --- Thread reply with active session ---

func TestHandle_ThreadReplyActiveSession(t *testing.T) {
	db := openRouterTestDB(t)
	router, _, spawner := setupRouter(t, db, "bot-123")

	ctx := context.Background()

	// Create an active session.
	router.sessionMgr.NewSession(ctx, "telegraph", "alice", "T1", "C1")

	// Route a message in the same thread.
	router.Handle(ctx, InboundMessage{
		UserID:    "user-1",
		UserName:  "alice",
		ChannelID: "C1",
		ThreadID:  "T1",
		Text:      "create a new task",
	})

	proc := spawner.lastProcess()
	if proc == nil {
		t.Fatal("expected a spawned process")
	}
	sent := proc.sentMessages()
	if len(sent) != 1 {
		t.Fatalf("expected 1 message sent to process, got %d", len(sent))
	}
	if sent[0] != "create a new task" {
		t.Errorf("sent = %q, want %q", sent[0], "create a new task")
	}
}

// --- Thread reply with historic session ---

func TestHandle_ThreadReplyHistoricSession(t *testing.T) {
	db := openRouterTestDB(t)
	router, _, spawner := setupRouter(t, db, "bot-123")

	ctx := context.Background()

	// Create a completed session in the DB.
	now := time.Now()
	db.Create(&models.DispatchSession{
		Source:           "telegraph",
		UserName:         "alice",
		PlatformThreadID: "T2",
		ChannelID:        "C1",
		Status:           "completed",
		LastHeartbeat:    now,
		CompletedAt:      &now,
	})

	// Send a message in that thread.
	router.Handle(ctx, InboundMessage{
		UserID:    "user-1",
		UserName:  "alice",
		ChannelID: "C1",
		ThreadID:  "T2",
		Text:      "resume work on this",
	})

	// Should have spawned a process (resume + route).
	if len(spawner.processes) == 0 {
		t.Fatal("expected process to be spawned for resume")
	}
}

// --- @mention creates new session ---

func TestHandle_MentionCreatesNewSession(t *testing.T) {
	db := openRouterTestDB(t)
	router, _, spawner := setupRouter(t, db, "bot-123")

	router.Handle(context.Background(), InboundMessage{
		UserID:    "user-1",
		UserName:  "bob",
		ChannelID: "C1",
		Text:      "@railyard create a bug ticket",
	})

	if len(spawner.processes) == 0 {
		t.Fatal("expected process to be spawned for @mention")
	}
}

func TestHandle_MentionInThread(t *testing.T) {
	db := openRouterTestDB(t)
	router, _, spawner := setupRouter(t, db, "bot-123")

	router.Handle(context.Background(), InboundMessage{
		UserID:    "user-1",
		UserName:  "bob",
		ChannelID: "C1",
		ThreadID:  "T3",
		Text:      "@railyard what's the status?",
	})

	if len(spawner.processes) == 0 {
		t.Fatal("expected process to be spawned for @mention in thread")
	}
}

// --- Follow-up message in channel resumes after process exits ---

func TestHandle_FollowUpInThreadResumesSession(t *testing.T) {
	db := openRouterTestDB(t)
	router, adapter, spawner := setupRouter(t, db, "bot-123")

	ctx := context.Background()

	// 1. Initial @mention in a channel (no thread) — creates a thread via StartThread
	//    and spawns a session keyed by channelID:threadID.
	router.Handle(ctx, InboundMessage{
		UserID:    "user-1",
		UserName:  "alice",
		ChannelID: "C1",
		Text:      "@railyard what about car-111ab?",
	})

	if len(spawner.processes) != 1 {
		t.Fatalf("expected 1 process, got %d", len(spawner.processes))
	}

	// The MockAdapter's StartThread returns "thread-1".
	threadID := "thread-1"

	// 2. Simulate process exit (one-shot model: Claude responds and exits).
	proc := spawner.lastProcess()
	close(proc.recvCh) // EOF on output (relayOutput finishes)
	proc.Close()       // signal done (monitorProcess cleans up)

	// Give monitorProcess goroutine time to clean up.
	time.Sleep(50 * time.Millisecond)

	// Verify the session was cleaned up from memory.
	if router.sessionMgr.HasSession("C1", threadID) {
		t.Fatal("expected session to be removed from memory after process exit")
	}

	// Verify it exists as a historic session (completed in DB).
	if !router.sessionMgr.HasHistoricSession("C1", threadID) {
		t.Fatal("expected historic session in DB after process exit")
	}

	// Clear sent messages from the initial exchange.
	adapter.mu.Lock()
	adapter.sent = nil
	adapter.mu.Unlock()

	// 3. Follow-up message in the thread (Discord sends threadID for thread messages).
	router.Handle(ctx, InboundMessage{
		UserID:    "user-1",
		UserName:  "alice",
		ChannelID: "C1",
		ThreadID:  threadID,
		Text:      "go ahead and have yardmaster work on it",
	})

	// Should have resumed: spawner now has 2 processes.
	if len(spawner.processes) != 2 {
		t.Fatalf("expected 2 processes (original + resumed), got %d", len(spawner.processes))
	}

	// Ack should have been sent to the thread.
	all := adapter.AllSent()
	if len(all) == 0 {
		t.Fatal("expected ack message on resume")
	}
	if all[0].ThreadID != threadID {
		t.Errorf("ack threadID = %q, want %q", all[0].ThreadID, threadID)
	}
}

// --- Unknown/unhandled message ---

func TestHandle_IgnoresUnknownMessage(t *testing.T) {
	db := openRouterTestDB(t)
	var out bytes.Buffer
	adapter := NewMockAdapter()
	adapter.Connect(context.Background())
	spawner := &mockSpawner{}

	sm, _ := NewSessionManager(SessionManagerOpts{
		DB:      db,
		Spawner: spawner,
	})
	ch, _ := NewCommandHandler(CommandHandlerOpts{DB: db})
	router, _ := NewRouter(RouterOpts{
		SessionMgr: sm,
		CmdHandler: ch,
		Adapter:    adapter,
		BotUserID:  "bot-123",
		Out:        &out,
	})

	router.Handle(context.Background(), InboundMessage{
		UserID:    "user-1",
		UserName:  "bob",
		ChannelID: "C1",
		Text:      "hello everyone",
	})

	// No sessions spawned, no commands executed.
	if len(spawner.processes) != 0 {
		t.Errorf("expected 0 processes for unknown message, got %d", len(spawner.processes))
	}
	if adapter.SentCount() != 0 {
		t.Errorf("expected 0 sent messages for unknown message, got %d", adapter.SentCount())
	}
	if out.Len() == 0 {
		t.Error("expected log output for ignored message")
	}
}

// --- Helper function tests ---

func TestIsCommand(t *testing.T) {
	tests := []struct {
		text string
		want bool
	}{
		{"!ry status", true},
		{"!ry", true},
		{"!ry car list", true},
		{"!ryExtra", false},
		{"ry status", false},
		{"hello !ry", false},
		{"", false},
	}
	for _, tt := range tests {
		got := isCommand(tt.text)
		if got != tt.want {
			t.Errorf("isCommand(%q) = %v, want %v", tt.text, got, tt.want)
		}
	}
}

func TestResolveThreadID(t *testing.T) {
	tests := []struct {
		channelID, threadID, want string
	}{
		{"C1", "T1", "T1"},
		{"C1", "", "C1"},
		{"C1", "C1", "C1"},
	}
	for _, tt := range tests {
		got := resolveThreadID(tt.channelID, tt.threadID)
		if got != tt.want {
			t.Errorf("resolveThreadID(%q, %q) = %q, want %q", tt.channelID, tt.threadID, got, tt.want)
		}
	}
}

func TestIsMention(t *testing.T) {
	tests := []struct {
		text string
		want bool
	}{
		{"@railyard create task", true},
		{"hey @bot", true},
		{"hello world", false},
		{"email@example.com", true},
		{"", false},
	}
	for _, tt := range tests {
		got := isMention(tt.text)
		if got != tt.want {
			t.Errorf("isMention(%q) = %v, want %v", tt.text, got, tt.want)
		}
	}
}

func TestIsSelfMessage(t *testing.T) {
	db := openRouterTestDB(t)
	router, _, _ := setupRouter(t, db, "bot-123")

	tests := []struct {
		userID string
		want   bool
	}{
		{"bot-123", true},
		{"user-1", false},
		{"", false},
	}
	for _, tt := range tests {
		got := router.isSelfMessage(InboundMessage{UserID: tt.userID})
		if got != tt.want {
			t.Errorf("isSelfMessage(userID=%q) = %v, want %v", tt.userID, got, tt.want)
		}
	}
}

// --- @mention with command routes to command handler ---

func TestHandle_DiscordMentionWithCommand(t *testing.T) {
	db := openRouterTestDB(t)
	router, adapter, spawner := setupRouter(t, db, "bot-123")

	// Simulate Discord @mention: "<@1475033217449857074> status"
	router.Handle(context.Background(), InboundMessage{
		UserID:    "user-1",
		UserName:  "alice",
		ChannelID: "C1",
		Text:      "<@1475033217449857074> status",
	})

	// Should route to command handler, not spawn a session.
	if len(spawner.processes) != 0 {
		t.Errorf("expected 0 spawned processes, got %d", len(spawner.processes))
	}
	if adapter.SentCount() != 1 {
		t.Fatalf("expected 1 command response, got %d", adapter.SentCount())
	}
}

func TestHandle_DiscordNickMentionWithCommand(t *testing.T) {
	db := openRouterTestDB(t)
	router, adapter, spawner := setupRouter(t, db, "bot-123")

	// Discord nickname mention format: <@!ID>
	router.Handle(context.Background(), InboundMessage{
		UserID:    "user-1",
		UserName:  "alice",
		ChannelID: "C1",
		Text:      "<@!1475033217449857074> car list",
	})

	if len(spawner.processes) != 0 {
		t.Errorf("expected 0 spawned processes, got %d", len(spawner.processes))
	}
	if adapter.SentCount() != 1 {
		t.Fatalf("expected 1 command response, got %d", adapter.SentCount())
	}
}

func TestHandle_MentionWithNonCommand_SpawnsSession(t *testing.T) {
	db := openRouterTestDB(t)
	router, _, spawner := setupRouter(t, db, "bot-123")

	// @mention with non-command text should still try to spawn a session.
	router.Handle(context.Background(), InboundMessage{
		UserID:    "user-1",
		UserName:  "bob",
		ChannelID: "C1",
		Text:      "<@1475033217449857074> create a bug ticket",
	})

	if len(spawner.processes) == 0 {
		t.Fatal("expected process to be spawned for @mention with non-command text")
	}
}

func TestExtractMentionCommand(t *testing.T) {
	db := openRouterTestDB(t)
	router, _, _ := setupRouter(t, db, "bot-123")

	tests := []struct {
		text string
		want string
	}{
		{"<@123456> status", "status"},
		{"<@!123456> status", "status"},
		{"<@123456> car list --track backend", "car list --track backend"},
		{"<@123456> engine list", "engine list"},
		{"<@123456> help", "help"},
		{"<@123456> create a bug ticket", ""},
		{"<@123456>", ""},
		{"hello world", ""},
		{"!ry status", ""},
		{"", ""},
	}
	for _, tt := range tests {
		got := router.extractMentionCommand(tt.text)
		if got != tt.want {
			t.Errorf("extractMentionCommand(%q) = %q, want %q", tt.text, got, tt.want)
		}
	}
}

// --- Ack message tests ---

func TestHandle_AckOnNewSession(t *testing.T) {
	db := openRouterTestDB(t)
	router, adapter, _ := setupRouter(t, db, "bot-123")

	router.Handle(context.Background(), InboundMessage{
		UserID:    "user-1",
		UserName:  "bob",
		ChannelID: "C1",
		Text:      "@railyard close out the completed epic",
	})

	// First sent message should be an ack from the ackPhrases list.
	all := adapter.AllSent()
	if len(all) == 0 {
		t.Fatal("expected at least 1 sent message (ack)")
	}
	ack := all[0]
	if ack.ChannelID != "C1" {
		t.Errorf("ack channel = %q, want C1", ack.ChannelID)
	}
	found := false
	for _, phrase := range ackPhrases {
		if ack.Text == phrase {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("ack text %q is not in ackPhrases", ack.Text)
	}
}

func TestHandle_AckOnResumeSession(t *testing.T) {
	db := openRouterTestDB(t)
	router, adapter, _ := setupRouter(t, db, "bot-123")

	// Create a completed session in the DB.
	now := time.Now()
	db.Create(&models.DispatchSession{
		Source:           "telegraph",
		UserName:         "alice",
		PlatformThreadID: "T2",
		ChannelID:        "C1",
		Status:           "completed",
		LastHeartbeat:    now,
		CompletedAt:      &now,
	})

	router.Handle(context.Background(), InboundMessage{
		UserID:    "user-1",
		UserName:  "alice",
		ChannelID: "C1",
		ThreadID:  "T2",
		Text:      "let's pick this back up",
	})

	all := adapter.AllSent()
	if len(all) == 0 {
		t.Fatal("expected at least 1 sent message (ack)")
	}
	ack := all[0]
	if ack.ThreadID != "T2" {
		t.Errorf("ack thread = %q, want T2", ack.ThreadID)
	}
	found := false
	for _, phrase := range ackPhrases {
		if ack.Text == phrase {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("ack text %q is not in ackPhrases", ack.Text)
	}
}

func TestHandle_AckOnActiveSession(t *testing.T) {
	db := openRouterTestDB(t)
	router, adapter, _ := setupRouter(t, db, "bot-123")

	ctx := context.Background()
	router.sessionMgr.NewSession(ctx, "telegraph", "alice", "T1", "C1")

	// Clear any messages from session creation.
	adapter.mu.Lock()
	adapter.sent = nil
	adapter.mu.Unlock()

	router.Handle(ctx, InboundMessage{
		UserID:    "user-1",
		UserName:  "alice",
		ChannelID: "C1",
		ThreadID:  "T1",
		Text:      "do the thing",
	})

	all := adapter.AllSent()
	if len(all) == 0 {
		t.Fatal("expected at least 1 sent message (ack)")
	}
	found := false
	for _, phrase := range ackPhrases {
		if all[0].Text == phrase {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("ack text %q is not in ackPhrases", all[0].Text)
	}
}

func TestNextAck_CyclesThroughAllPhrases(t *testing.T) {
	db := openRouterTestDB(t)
	router, _, _ := setupRouter(t, db, "bot-123")

	// Draw exactly len(ackPhrases) acks — should see every phrase exactly once.
	seen := make(map[string]int)
	for i := 0; i < len(ackPhrases); i++ {
		phrase := router.nextAck()
		seen[phrase]++
	}
	for _, phrase := range ackPhrases {
		if seen[phrase] != 1 {
			t.Errorf("phrase %q seen %d times in first cycle, want 1", phrase, seen[phrase])
		}
	}

	// Draw another full cycle — should again see every phrase exactly once.
	seen2 := make(map[string]int)
	for i := 0; i < len(ackPhrases); i++ {
		phrase := router.nextAck()
		seen2[phrase]++
	}
	for _, phrase := range ackPhrases {
		if seen2[phrase] != 1 {
			t.Errorf("phrase %q seen %d times in second cycle, want 1", phrase, seen2[phrase])
		}
	}
}

func TestHandle_TopLevelMentionCreatesThread(t *testing.T) {
	db := openRouterTestDB(t)
	router, adapter, spawner := setupRouter(t, db, "bot-123")

	// Top-level @mention (no thread) should create a thread via StartThread.
	router.Handle(context.Background(), InboundMessage{
		UserID:    "user-1",
		UserName:  "bob",
		ChannelID: "C1",
		Text:      "@railyard close out the epic",
	})

	if len(spawner.processes) == 0 {
		t.Fatal("expected process to be spawned")
	}

	// Session should be keyed by C1:thread-1, not C1:C1.
	if !router.sessionMgr.HasSession("C1", "thread-1") {
		t.Error("expected session keyed by thread-1")
	}
	if router.sessionMgr.HasSession("C1", "C1") {
		t.Error("session should NOT be keyed by C1 (channel fallback)")
	}

	// The ack text should have been sent to the channel (via StartThread).
	all := adapter.AllSent()
	if len(all) == 0 {
		t.Fatal("expected at least 1 sent message")
	}
	if all[0].ChannelID != "C1" {
		t.Errorf("ack channel = %q, want C1", all[0].ChannelID)
	}
}

func TestHandle_InThreadMentionDoesNotCreateNewThread(t *testing.T) {
	db := openRouterTestDB(t)
	router, adapter, spawner := setupRouter(t, db, "bot-123")

	// @mention inside an existing thread should NOT create another thread.
	router.Handle(context.Background(), InboundMessage{
		UserID:    "user-1",
		UserName:  "bob",
		ChannelID: "C1",
		ThreadID:  "T5",
		Text:      "@railyard close out the epic",
	})

	if len(spawner.processes) == 0 {
		t.Fatal("expected process to be spawned")
	}

	// Session should use the existing thread ID, not create a new one.
	if !router.sessionMgr.HasSession("C1", "T5") {
		t.Error("expected session keyed by T5 (existing thread)")
	}

	// Ack should be sent to the existing thread.
	all := adapter.AllSent()
	if len(all) == 0 {
		t.Fatal("expected at least 1 sent message (ack)")
	}
	if all[0].ThreadID != "T5" {
		t.Errorf("ack threadID = %q, want T5", all[0].ThreadID)
	}

	// Should only have created 1 thread total (from the mock counter).
	adapter.mu.Lock()
	tc := adapter.threadCounter
	adapter.mu.Unlock()
	if tc != 0 {
		t.Errorf("threadCounter = %d, want 0 (no thread creation for in-thread mentions)", tc)
	}
}

func TestHandle_NoAckOnCommand(t *testing.T) {
	db := openRouterTestDB(t)
	router, adapter, _ := setupRouter(t, db, "bot-123")

	router.Handle(context.Background(), InboundMessage{
		UserID:    "user-1",
		UserName:  "alice",
		ChannelID: "C1",
		Text:      "!ry status",
	})

	// Command responses should NOT have an ack prefix — just the command output.
	all := adapter.AllSent()
	if len(all) != 1 {
		t.Fatalf("expected 1 sent message, got %d", len(all))
	}
	for _, phrase := range ackPhrases {
		if all[0].Text == phrase {
			t.Errorf("command response should not be an ack phrase, got %q", all[0].Text)
		}
	}
}

// --- Command takes priority over session routing ---

func TestHandle_CommandTakesPriorityOverSession(t *testing.T) {
	db := openRouterTestDB(t)
	router, adapter, _ := setupRouter(t, db, "bot-123")

	ctx := context.Background()

	// Create an active session in thread T1.
	router.sessionMgr.NewSession(ctx, "telegraph", "alice", "T1", "C1")

	// Send a command in the same thread — should route to command handler,
	// not to the session.
	router.Handle(ctx, InboundMessage{
		UserID:    "user-1",
		UserName:  "alice",
		ChannelID: "C1",
		ThreadID:  "T1",
		Text:      "!ry status",
	})

	// Command should produce a response.
	if adapter.SentCount() < 1 {
		t.Error("expected command response even in active session thread")
	}
}

func TestHandle_LongCommandResponseIsChunked(t *testing.T) {
	db := openRouterTestDB(t)

	// Create enough cars to make "car list" exceed 2000 chars.
	for i := 0; i < 50; i++ {
		db.Create(&models.Car{
			ID:       fmt.Sprintf("car-%03d", i),
			Title:    fmt.Sprintf("A sufficiently long car title to inflate the response size number %d", i),
			Status:   "open",
			Track:    "backend",
			Priority: 2,
		})
	}

	router, adapter, _ := setupRouter(t, db, "bot-123")

	router.Handle(context.Background(), InboundMessage{
		UserID:    "user-1",
		UserName:  "alice",
		ChannelID: "C1",
		Text:      "!ry car list",
	})

	// Should send multiple messages, all within 2000 chars.
	if adapter.SentCount() < 2 {
		t.Fatalf("expected multiple chunks, got %d message(s)", adapter.SentCount())
	}
	for i, msg := range adapter.AllSent() {
		if len(msg.Text) > 2000 {
			t.Errorf("chunk %d is %d chars, exceeds 2000", i, len(msg.Text))
		}
		if msg.ChannelID != "C1" {
			t.Errorf("chunk %d channel = %q, want C1", i, msg.ChannelID)
		}
	}
}
