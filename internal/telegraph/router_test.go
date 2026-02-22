package telegraph

import (
	"bytes"
	"context"
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
