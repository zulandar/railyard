package main

import (
	"bytes"
	"database/sql"
	"strings"
	"testing"
	"time"

	"github.com/zulandar/railyard/internal/config"
	"github.com/zulandar/railyard/internal/db"
	"github.com/zulandar/railyard/internal/models"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// telegraphTestDB creates an in-memory SQLite database with all Railyard
// tables migrated, suitable for testing functions that accept *gorm.DB.
func telegraphTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	gormDB, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	if err := db.AutoMigrate(gormDB); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return gormDB
}

// ---------------------------------------------------------------------------
// runTelegraphSessionsList tests
// ---------------------------------------------------------------------------

func TestRunTelegraphSessionsList_Empty(t *testing.T) {
	gormDB := telegraphTestDB(t)
	var buf bytes.Buffer

	if err := runTelegraphSessionsList(gormDB, &buf); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	out := buf.String()
	if !strings.Contains(out, "No telegraph sessions found.") {
		t.Errorf("expected empty message, got: %s", out)
	}
}

func TestRunTelegraphSessionsList_WithSessions(t *testing.T) {
	gormDB := telegraphTestDB(t)

	now := time.Now().Truncate(time.Second)

	sessions := []models.DispatchSession{
		{
			Source:    "telegraph",
			Status:    "active",
			UserName:  "alice",
			ChannelID: "C001",
			CreatedAt: now.Add(-1 * time.Hour),
		},
		{
			Source:    "telegraph",
			Status:    "completed",
			UserName:  "bob",
			ChannelID: "C002",
			CreatedAt: now,
		},
		{
			// Non-telegraph session — should NOT appear.
			Source:    "local",
			Status:    "active",
			UserName:  "charlie",
			ChannelID: "C003",
			CreatedAt: now,
		},
	}
	for i := range sessions {
		if err := gormDB.Create(&sessions[i]).Error; err != nil {
			t.Fatalf("seed session %d: %v", i, err)
		}
	}

	var buf bytes.Buffer
	if err := runTelegraphSessionsList(gormDB, &buf); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	out := buf.String()

	// Should show the count header for 2 telegraph sessions.
	if !strings.Contains(out, "Telegraph Sessions (2)") {
		t.Errorf("expected header with count 2, got: %s", out)
	}

	// Should contain column headers.
	for _, col := range []string{"ID", "STATUS", "USER", "CHANNEL", "CREATED"} {
		if !strings.Contains(out, col) {
			t.Errorf("expected column header %q in output", col)
		}
	}

	// Should contain session data.
	if !strings.Contains(out, "alice") {
		t.Error("expected alice in output")
	}
	if !strings.Contains(out, "bob") {
		t.Error("expected bob in output")
	}

	// The non-telegraph session must not appear.
	if strings.Contains(out, "charlie") {
		t.Error("local session (charlie) should not appear in telegraph listing")
	}

	// "bob" session (created later) should appear before "alice" (DESC order).
	bobIdx := strings.Index(out, "bob")
	aliceIdx := strings.Index(out, "alice")
	if bobIdx > aliceIdx {
		t.Error("expected descending order: bob before alice")
	}
}

func TestRunTelegraphSessionsList_ClosedDB(t *testing.T) {
	gormDB := telegraphTestDB(t)

	// Close the underlying sql.DB to provoke a query error.
	sqlDB, err := gormDB.DB()
	if err != nil {
		t.Fatalf("get sql.DB: %v", err)
	}
	sqlDB.Close()

	var buf bytes.Buffer
	err = runTelegraphSessionsList(gormDB, &buf)
	if err == nil {
		t.Fatal("expected error on closed db")
	}
	if !strings.Contains(err.Error(), "query sessions") {
		t.Errorf("error = %q, want to contain 'query sessions'", err.Error())
	}
}

// ---------------------------------------------------------------------------
// createAdapter tests
// ---------------------------------------------------------------------------

func TestCreateAdapter_UnknownPlatform(t *testing.T) {
	cfg := &config.Config{
		Telegraph: config.TelegraphConfig{
			Platform: "unknown",
		},
	}

	_, err := createAdapter(cfg)
	if err == nil {
		t.Fatal("expected error for unknown platform")
	}
	if !strings.Contains(err.Error(), "unsupported platform") {
		t.Errorf("error = %q, want to contain 'unsupported platform'", err.Error())
	}
}

func TestCreateAdapter_EmptyPlatform(t *testing.T) {
	cfg := &config.Config{
		Telegraph: config.TelegraphConfig{
			Platform: "",
		},
	}

	_, err := createAdapter(cfg)
	if err == nil {
		t.Fatal("expected error for empty platform")
	}
	if !strings.Contains(err.Error(), "unsupported platform") {
		t.Errorf("error = %q, want to contain 'unsupported platform'", err.Error())
	}
}

func TestCreateAdapter_SlackMissingTokens(t *testing.T) {
	cfg := &config.Config{
		Telegraph: config.TelegraphConfig{
			Platform: "slack",
			Slack: config.SlackConfig{
				AppToken: "",
				BotToken: "",
			},
		},
	}

	_, err := createAdapter(cfg)
	if err == nil {
		t.Fatal("expected error for slack without tokens")
	}
	// The slack adapter returns "slack: bot token is required" when BotToken is empty.
	if !strings.Contains(err.Error(), "bot token") {
		t.Errorf("error = %q, want to contain 'bot token'", err.Error())
	}
}

func TestCreateAdapter_DiscordMissingToken(t *testing.T) {
	cfg := &config.Config{
		Telegraph: config.TelegraphConfig{
			Platform: "discord",
			Discord: config.DiscordConfig{
				BotToken: "",
			},
		},
	}

	_, err := createAdapter(cfg)
	if err == nil {
		t.Fatal("expected error for discord without token")
	}
	// The discord adapter returns "discord: bot token is required" when BotToken is empty.
	if !strings.Contains(err.Error(), "bot token") {
		t.Errorf("error = %q, want to contain 'bot token'", err.Error())
	}
}

// Ensure the sql import is used (referenced by the closed-db test).
var _ *sql.DB
