package main

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/zulandar/railyard/internal/models"
)

// ---------------------------------------------------------------------------
// 1. Message send command
// ---------------------------------------------------------------------------

func TestMessageSend_Success(t *testing.T) {
	gormDB := mockTestDB(t)
	cleanup := withMockDB(t, gormDB)
	defer cleanup()

	out, err := execCmd(t, []string{
		"message", "send",
		"--from", "eng-1",
		"--to", "human",
		"--subject", "test",
		"--body", "hello",
		"--config", "test.yaml",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "Sent message") {
		t.Errorf("expected output to contain 'Sent message', got:\n%s", out)
	}
	if !strings.Contains(out, "to human") {
		t.Errorf("expected output to contain 'to human', got:\n%s", out)
	}
}

func TestMessageSend_MissingFlags(t *testing.T) {
	_, err := execCmd(t, []string{"message", "send", "--config", "test.yaml"})
	if err == nil {
		t.Fatal("expected error for missing required flags")
	}
}

// ---------------------------------------------------------------------------
// 2. Message inbox command
// ---------------------------------------------------------------------------

func TestMessageInbox_Empty(t *testing.T) {
	gormDB := mockTestDB(t)
	cleanup := withMockDB(t, gormDB)
	defer cleanup()

	out, err := execCmd(t, []string{"inbox", "--agent", "eng-1", "--config", "test.yaml"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "No messages for eng-1") {
		t.Errorf("expected 'No messages for eng-1', got:\n%s", out)
	}
}

func TestMessageInbox_WithMessages(t *testing.T) {
	gormDB := mockTestDB(t)
	cleanup := withMockDB(t, gormDB)
	defer cleanup()

	gormDB.Create(&models.Message{
		FromAgent: "human", ToAgent: "eng-1",
		Subject: "do stuff", Body: "please do stuff",
		Priority: "normal", CreatedAt: time.Now(),
	})
	gormDB.Create(&models.Message{
		FromAgent: "human", ToAgent: "eng-1",
		Subject: "urgent", Body: "do now",
		Priority: "urgent", CreatedAt: time.Now(),
	})

	out, err := execCmd(t, []string{"inbox", "--agent", "eng-1", "--config", "test.yaml"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	for _, want := range []string{"ID", "FROM", "SUBJECT", "do stuff"} {
		if !strings.Contains(out, want) {
			t.Errorf("expected output to contain %q, got:\n%s", want, out)
		}
	}
}

func TestMessageInbox_MissingAgent(t *testing.T) {
	_, err := execCmd(t, []string{"inbox", "--config", "test.yaml"})
	if err == nil {
		t.Fatal("expected error for missing --agent flag")
	}
}

// ---------------------------------------------------------------------------
// 3. Message ack command
// ---------------------------------------------------------------------------

func TestMessageAck_Success(t *testing.T) {
	gormDB := mockTestDB(t)
	cleanup := withMockDB(t, gormDB)
	defer cleanup()

	msg := models.Message{
		FromAgent: "human", ToAgent: "eng-1",
		Subject: "test", Body: "body",
		Priority: "normal", CreatedAt: time.Now(),
	}
	gormDB.Create(&msg)

	out, err := execCmd(t, []string{
		"message", "ack", fmt.Sprintf("%d", msg.ID),
		"--config", "test.yaml",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "Acknowledged message") {
		t.Errorf("expected 'Acknowledged message', got:\n%s", out)
	}
}

func TestMessageAck_InvalidID(t *testing.T) {
	gormDB := mockTestDB(t)
	cleanup := withMockDB(t, gormDB)
	defer cleanup()

	_, err := execCmd(t, []string{
		"message", "ack", "notanumber",
		"--config", "test.yaml",
	})
	if err == nil {
		t.Fatal("expected error for invalid message ID")
	}
	if !strings.Contains(err.Error(), "invalid message ID") {
		t.Errorf("expected error to contain 'invalid message ID', got: %v", err)
	}
}

func TestMessageAck_BroadcastNoAgent(t *testing.T) {
	gormDB := mockTestDB(t)
	cleanup := withMockDB(t, gormDB)
	defer cleanup()

	_, err := execCmd(t, []string{
		"message", "ack", "1",
		"--broadcast",
		"--config", "test.yaml",
	})
	if err == nil {
		t.Fatal("expected error for --broadcast without --agent")
	}
	if !strings.Contains(err.Error(), "--agent is required") {
		t.Errorf("expected error to contain '--agent is required', got: %v", err)
	}
}

// ---------------------------------------------------------------------------
// 4. Message thread command
// ---------------------------------------------------------------------------

func TestMessageThread_Empty(t *testing.T) {
	gormDB := mockTestDB(t)
	cleanup := withMockDB(t, gormDB)
	defer cleanup()

	out, err := execCmd(t, []string{
		"message", "thread", "999",
		"--config", "test.yaml",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "No messages in thread 999") {
		t.Errorf("expected 'No messages in thread 999', got:\n%s", out)
	}
}

func TestMessageThread_WithMessages(t *testing.T) {
	gormDB := mockTestDB(t)
	cleanup := withMockDB(t, gormDB)
	defer cleanup()

	threadID := uint(1)
	gormDB.Create(&models.Message{
		FromAgent: "human", ToAgent: "eng-1",
		Subject: "thread msg", Body: "first",
		ThreadID: &threadID, Priority: "normal",
		CreatedAt: time.Now(),
	})
	gormDB.Create(&models.Message{
		FromAgent: "eng-1", ToAgent: "human",
		Subject: "re: thread msg", Body: "reply",
		ThreadID: &threadID, Priority: "normal",
		CreatedAt: time.Now().Add(time.Minute),
	})

	out, err := execCmd(t, []string{
		"message", "thread", "1",
		"--config", "test.yaml",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	for _, want := range []string{"human", "eng-1", "thread msg"} {
		if !strings.Contains(out, want) {
			t.Errorf("expected output to contain %q, got:\n%s", want, out)
		}
	}
}

func TestMessageThread_InvalidID(t *testing.T) {
	gormDB := mockTestDB(t)
	cleanup := withMockDB(t, gormDB)
	defer cleanup()

	_, err := execCmd(t, []string{
		"message", "thread", "abc",
		"--config", "test.yaml",
	})
	if err == nil {
		t.Fatal("expected error for invalid thread ID")
	}
	if !strings.Contains(err.Error(), "invalid thread ID") {
		t.Errorf("expected error to contain 'invalid thread ID', got: %v", err)
	}
}

// ---------------------------------------------------------------------------
// 5. runStatus command (no --watch mode)
// ---------------------------------------------------------------------------

func TestRunStatus_NoWatch(t *testing.T) {
	gormDB := mockTestDB(t)
	cleanup := withMockDB(t, gormDB)
	defer cleanup()

	now := time.Now()
	gormDB.Create(&models.Engine{
		ID: "eng-1", Track: "backend", Status: "working",
		CurrentCar: "car-1", StartedAt: now, LastActivity: now,
	})
	gormDB.Create(&models.Car{
		ID: "car-1", Title: "Test", Status: "in_progress",
		Track: "backend", Priority: 2, CreatedAt: now, UpdatedAt: now,
	})

	out, err := execCmd(t, []string{"status", "--config", "test.yaml"})
	if err != nil {
		// orchestration.Status calls tmux.ListSessions which may fail
		// if tmux is not installed. Skip rather than fail.
		t.Skipf("status command failed (tmux may not be available): %v", err)
	}
	if out == "" {
		t.Error("expected non-empty output from status command")
	}
}
