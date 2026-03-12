package main

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/zulandar/railyard/internal/models"
)

// ---------------------------------------------------------------------------
// 1. Logs with --raw flag
// ---------------------------------------------------------------------------

func TestRunLogs_RawMode(t *testing.T) {
	gormDB := mockTestDB(t)
	cleanup := withMockDB(t, gormDB)
	defer cleanup()

	now := time.Now()
	gormDB.Create(&models.AgentLog{EngineID: "eng-1", CarID: "car-1", SessionID: "sess-1", Direction: "in", Content: "user prompt text", CreatedAt: now})
	gormDB.Create(&models.AgentLog{EngineID: "eng-1", CarID: "car-1", SessionID: "sess-1", Direction: "out", Content: "assistant response text", CreatedAt: now})

	out, err := execCmd(t, []string{"logs", "--raw", "--config", "test.yaml"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Raw mode prints the full content on its own line, preceded by a --- header with timestamp.
	for _, want := range []string{"user prompt text", "assistant response text", "---", "eng-1", "car-1"} {
		if !strings.Contains(out, want) {
			t.Errorf("expected output to contain %q, got:\n%s", want, out)
		}
	}
	// Raw mode should include direction indicators.
	if !strings.Contains(out, "in") {
		t.Errorf("expected output to contain direction 'in', got:\n%s", out)
	}
	if !strings.Contains(out, "out") {
		t.Errorf("expected output to contain direction 'out', got:\n%s", out)
	}
}

// ---------------------------------------------------------------------------
// 2. Logs with --lines flag
// ---------------------------------------------------------------------------

func TestRunLogs_LinesLimit(t *testing.T) {
	gormDB := mockTestDB(t)
	cleanup := withMockDB(t, gormDB)
	defer cleanup()

	now := time.Now()
	for i := 1; i <= 5; i++ {
		gormDB.Create(&models.AgentLog{
			EngineID:  "eng-1",
			CarID:     "car-1",
			SessionID: "sess-1",
			Direction: "in",
			Content:   fmt.Sprintf("entry-%d", i),
			CreatedAt: now.Add(time.Duration(i) * time.Second),
		})
	}

	out, err := execCmd(t, []string{"logs", "--lines", "2", "--raw", "--config", "test.yaml"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// With --lines 2, only the last 2 entries (entry-4 and entry-5) should appear.
	if !strings.Contains(out, "entry-5") {
		t.Errorf("expected output to contain 'entry-5', got:\n%s", out)
	}
	if !strings.Contains(out, "entry-4") {
		t.Errorf("expected output to contain 'entry-4', got:\n%s", out)
	}
	// Earlier entries should NOT appear.
	if strings.Contains(out, "entry-1") {
		t.Errorf("expected output NOT to contain 'entry-1', got:\n%s", out)
	}
	if strings.Contains(out, "entry-2") {
		t.Errorf("expected output NOT to contain 'entry-2', got:\n%s", out)
	}
	if strings.Contains(out, "entry-3") {
		t.Errorf("expected output NOT to contain 'entry-3', got:\n%s", out)
	}
}

// ---------------------------------------------------------------------------
// 3. Logs with --session filter
// ---------------------------------------------------------------------------

func TestRunLogs_FilterBySession(t *testing.T) {
	gormDB := mockTestDB(t)
	cleanup := withMockDB(t, gormDB)
	defer cleanup()

	now := time.Now()
	gormDB.Create(&models.AgentLog{EngineID: "eng-1", CarID: "car-1", SessionID: "sess-abc", Direction: "in", Content: "session abc entry", CreatedAt: now})
	gormDB.Create(&models.AgentLog{EngineID: "eng-1", CarID: "car-1", SessionID: "sess-def", Direction: "in", Content: "session def entry", CreatedAt: now})

	out, err := execCmd(t, []string{"logs", "--session", "sess-abc", "--raw", "--config", "test.yaml"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(out, "session abc entry") {
		t.Errorf("expected output to contain 'session abc entry', got:\n%s", out)
	}
	if strings.Contains(out, "session def entry") {
		t.Errorf("expected output NOT to contain 'session def entry', got:\n%s", out)
	}
}

// ---------------------------------------------------------------------------
// 4. Car list with status filter (tokens and cycles always shown)
// ---------------------------------------------------------------------------

func TestRunCarList_TokensWithStatusFilter(t *testing.T) {
	gormDB := mockTestDB(t)
	cleanup := withMockDB(t, gormDB)
	defer cleanup()

	now := time.Now()
	gormDB.Create(&models.Car{ID: "car-open-tok", Title: "Open Token Car", Status: "open", Track: "backend", Priority: 2, CreatedAt: now, UpdatedAt: now})
	gormDB.Create(&models.Car{ID: "car-done-tok", Title: "Done Token Car", Status: "done", Track: "backend", Priority: 1, CreatedAt: now, UpdatedAt: now})
	gormDB.Create(&models.AgentLog{CarID: "car-open-tok", EngineID: "eng-1", Direction: "out", InputTokens: 1000, OutputTokens: 500, TokenCount: 1500, Model: "claude-3", CreatedAt: now})
	gormDB.Create(&models.AgentLog{CarID: "car-done-tok", EngineID: "eng-1", Direction: "out", InputTokens: 2000, OutputTokens: 800, TokenCount: 2800, Model: "claude-3", CreatedAt: now})

	out, err := execCmd(t, []string{"car", "list", "--status", "open", "--config", "test.yaml"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should contain the TOKENS column header.
	if !strings.Contains(out, "TOKENS") {
		t.Errorf("expected output to contain 'TOKENS' header, got:\n%s", out)
	}
	// Should contain the open car.
	if !strings.Contains(out, "car-open-tok") {
		t.Errorf("expected output to contain 'car-open-tok', got:\n%s", out)
	}
	// Should NOT contain the done car.
	if strings.Contains(out, "car-done-tok") {
		t.Errorf("expected output NOT to contain 'car-done-tok', got:\n%s", out)
	}
}

// ---------------------------------------------------------------------------
// 5. Engine list with --status filter
// ---------------------------------------------------------------------------

func TestRunEngineList_FilterByStatus(t *testing.T) {
	gormDB := mockTestDB(t)
	cleanup := withMockDB(t, gormDB)
	defer cleanup()

	now := time.Now()
	gormDB.Create(&models.Engine{ID: "eng-work", Track: "backend", Status: "working", CurrentCar: "car-1", StartedAt: now, LastActivity: now})
	gormDB.Create(&models.Engine{ID: "eng-idle", Track: "backend", Status: "idle", StartedAt: now, LastActivity: now})
	gormDB.Create(&models.Engine{ID: "eng-dead", Track: "backend", Status: "dead", StartedAt: now, LastActivity: now})

	out, err := execCmd(t, []string{"engine", "list", "--status", "working", "--config", "test.yaml"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(out, "eng-work") {
		t.Errorf("expected output to contain 'eng-work', got:\n%s", out)
	}
	if strings.Contains(out, "eng-idle") {
		t.Errorf("expected output NOT to contain 'eng-idle', got:\n%s", out)
	}
	if strings.Contains(out, "eng-dead") {
		t.Errorf("expected output NOT to contain 'eng-dead', got:\n%s", out)
	}
}

// ---------------------------------------------------------------------------
// 6. Car dep list with both blockers and dependents
// ---------------------------------------------------------------------------

func TestRunCarDepList_BothDirections(t *testing.T) {
	gormDB := mockTestDB(t)
	cleanup := withMockDB(t, gormDB)
	defer cleanup()

	now := time.Now()
	gormDB.Create(&models.Car{ID: "car-a-dir", Title: "Car A", Status: "open", Track: "backend", CreatedAt: now, UpdatedAt: now})
	gormDB.Create(&models.Car{ID: "car-b-dir", Title: "Car B", Status: "open", Track: "backend", CreatedAt: now, UpdatedAt: now})
	gormDB.Create(&models.Car{ID: "car-c-dir", Title: "Car C", Status: "open", Track: "backend", CreatedAt: now, UpdatedAt: now})

	// B is blocked by A (blocker direction for B).
	gormDB.Create(&models.CarDep{CarID: "car-b-dir", BlockedBy: "car-a-dir", DepType: "blocks"})
	// C is blocked by B (dependent direction for B).
	gormDB.Create(&models.CarDep{CarID: "car-c-dir", BlockedBy: "car-b-dir", DepType: "blocks"})

	out, err := execCmd(t, []string{"car", "dep", "list", "car-b-dir", "--config", "test.yaml"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// B should show "Blocked by:" section listing car-a-dir.
	if !strings.Contains(out, "Blocked by:") {
		t.Errorf("expected output to contain 'Blocked by:', got:\n%s", out)
	}
	if !strings.Contains(out, "car-a-dir") {
		t.Errorf("expected output to contain 'car-a-dir', got:\n%s", out)
	}

	// B should show "Blocks:" section listing car-c-dir.
	if !strings.Contains(out, "Blocks:") {
		t.Errorf("expected output to contain 'Blocks:', got:\n%s", out)
	}
	if !strings.Contains(out, "car-c-dir") {
		t.Errorf("expected output to contain 'car-c-dir', got:\n%s", out)
	}
}

// ---------------------------------------------------------------------------
// 7. Car update with multiple flags
// ---------------------------------------------------------------------------

func TestRunCarUpdate_MultipleFlags(t *testing.T) {
	gormDB := mockTestDB(t)
	cleanup := withMockDB(t, gormDB)
	defer cleanup()

	now := time.Now()
	gormDB.Create(&models.Car{ID: "car-multi", Title: "Multi Update", Status: "open", Track: "backend", Priority: 3, CreatedAt: now, UpdatedAt: now})

	out, err := execCmd(t, []string{
		"car", "update", "car-multi",
		"--status", "ready",
		"--assignee", "eng-1",
		"--priority", "1",
		"--description", "new desc",
		"--config", "test.yaml",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "Updated car car-multi") {
		t.Errorf("expected 'Updated car car-multi', got:\n%s", out)
	}

	// Verify all fields changed in DB.
	var c models.Car
	if err := gormDB.First(&c, "id = ?", "car-multi").Error; err != nil {
		t.Fatalf("fetch car: %v", err)
	}
	if c.Status != "ready" {
		t.Errorf("status = %q, want %q", c.Status, "ready")
	}
	if c.Assignee != "eng-1" {
		t.Errorf("assignee = %q, want %q", c.Assignee, "eng-1")
	}
	if c.Priority != 1 {
		t.Errorf("priority = %d, want %d", c.Priority, 1)
	}
	if c.Description != "new desc" {
		t.Errorf("description = %q, want %q", c.Description, "new desc")
	}
}

// ---------------------------------------------------------------------------
// 8. Message ack with --broadcast and --agent
// ---------------------------------------------------------------------------

func TestMessageAck_BroadcastWithAgent(t *testing.T) {
	gormDB := mockTestDB(t)
	cleanup := withMockDB(t, gormDB)
	defer cleanup()

	msg := models.Message{
		FromAgent: "human", ToAgent: "*",
		Subject: "broadcast announcement", Body: "hello everyone",
		Priority: "normal", CreatedAt: time.Now(),
	}
	gormDB.Create(&msg)

	out, err := execCmd(t, []string{
		"message", "ack", fmt.Sprintf("%d", msg.ID),
		"--broadcast", "--agent", "eng-1",
		"--config", "test.yaml",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "Acknowledged message") {
		t.Errorf("expected 'Acknowledged message', got:\n%s", out)
	}

	// Verify broadcast acknowledgment record exists.
	var ack models.BroadcastAck
	if err := gormDB.Where("message_id = ? AND agent_id = ?", msg.ID, "eng-1").First(&ack).Error; err != nil {
		t.Fatalf("expected broadcast ack record, got error: %v", err)
	}
}

// ---------------------------------------------------------------------------
// 9. Car publish --recursive
// ---------------------------------------------------------------------------

func TestRunCarPublish_Recursive(t *testing.T) {
	gormDB := mockTestDB(t)
	cleanup := withMockDB(t, gormDB)
	defer cleanup()

	now := time.Now()
	epicID := "car-repub"
	gormDB.Create(&models.Car{ID: epicID, Title: "Draft Epic", Status: "draft", Type: "epic", Track: "backend", Priority: 2, CreatedAt: now, UpdatedAt: now})
	gormDB.Create(&models.Car{ID: "car-repub-c1", Title: "Draft Child", Status: "draft", Track: "backend", ParentID: &epicID, Priority: 2, CreatedAt: now, UpdatedAt: now})

	out, err := execCmd(t, []string{"car", "publish", "car-repub", "--recursive", "--config", "test.yaml"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(out, "Published") {
		t.Errorf("expected output to contain 'Published', got:\n%s", out)
	}
	// Should publish at least 2 cars (the epic and its child).
	if !strings.Contains(out, "2 car(s)") {
		t.Errorf("expected output to contain '2 car(s)', got:\n%s", out)
	}

	// Verify both cars are now open in DB.
	var epic models.Car
	if err := gormDB.First(&epic, "id = ?", epicID).Error; err != nil {
		t.Fatalf("fetch epic: %v", err)
	}
	if epic.Status != "open" {
		t.Errorf("epic status = %q, want %q", epic.Status, "open")
	}

	var child models.Car
	if err := gormDB.First(&child, "id = ?", "car-repub-c1").Error; err != nil {
		t.Fatalf("fetch child: %v", err)
	}
	if child.Status != "open" {
		t.Errorf("child status = %q, want %q", child.Status, "open")
	}
}
