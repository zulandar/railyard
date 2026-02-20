package yardmaster

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/zulandar/railyard/internal/config"
	"github.com/zulandar/railyard/internal/models"
)

func TestRunDaemon_NilDB(t *testing.T) {
	cfg := testConfig(config.TrackConfig{Name: "backend", Language: "go"})
	err := RunDaemon(context.Background(), nil, cfg, "railyard.yaml", "/tmp", time.Second, nil)
	if err == nil {
		t.Fatal("expected error for nil db")
	}
	if !strings.Contains(err.Error(), "db is required") {
		t.Errorf("error = %q, want to contain %q", err.Error(), "db is required")
	}
}

func TestRunDaemon_NilConfig(t *testing.T) {
	err := RunDaemon(context.Background(), nil, nil, "railyard.yaml", "/tmp", time.Second, nil)
	if err == nil {
		t.Fatal("expected error")
	}
	// DB check comes first.
	if !strings.Contains(err.Error(), "db is required") {
		t.Errorf("error = %q", err)
	}
}

func TestRunDaemon_EmptyRepoDir(t *testing.T) {
	err := RunDaemon(context.Background(), nil, nil, "railyard.yaml", "", time.Second, nil)
	if err == nil {
		t.Fatal("expected error")
	}
	// DB check comes first.
	if !strings.Contains(err.Error(), "db is required") {
		t.Errorf("error = %q", err)
	}
}

func TestYardmasterID(t *testing.T) {
	if YardmasterID != "yardmaster" {
		t.Errorf("YardmasterID = %q, want %q", YardmasterID, "yardmaster")
	}
}

func TestDefaultPollInterval(t *testing.T) {
	if defaultPollInterval != 30*time.Second {
		t.Errorf("defaultPollInterval = %v, want 30s", defaultPollInterval)
	}
}

func TestSleepWithContext_Cancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately.

	start := time.Now()
	sleepWithContext(ctx, 10*time.Second)
	elapsed := time.Since(start)

	if elapsed > time.Second {
		t.Errorf("sleepWithContext should return immediately on cancelled ctx, took %v", elapsed)
	}
}

func TestSleepWithContext_ShortDuration(t *testing.T) {
	start := time.Now()
	sleepWithContext(context.Background(), 50*time.Millisecond)
	elapsed := time.Since(start)

	if elapsed < 40*time.Millisecond {
		t.Errorf("sleepWithContext returned too early: %v", elapsed)
	}
	if elapsed > time.Second {
		t.Errorf("sleepWithContext took too long: %v", elapsed)
	}
}

func TestMaxTestFailures(t *testing.T) {
	if maxTestFailures != 2 {
		t.Errorf("maxTestFailures = %d, want 2", maxTestFailures)
	}
}

// ---------------------------------------------------------------------------
// sweepOpenEpics tests
// ---------------------------------------------------------------------------

func TestSweepOpenEpics_ClosesCompletedEpic(t *testing.T) {
	db := testDB(t)

	epicID := "epic-sweep1"
	db.Create(&models.Car{ID: epicID, Type: "epic", Status: "open", Track: "backend", Title: "Test Epic"})
	db.Create(&models.Car{ID: "child-sw1", Type: "task", Status: "merged", Track: "backend", ParentID: &epicID})
	db.Create(&models.Car{ID: "child-sw2", Type: "task", Status: "done", Track: "backend", ParentID: &epicID})

	var buf bytes.Buffer
	if err := sweepOpenEpics(db, &buf); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var epic models.Car
	db.First(&epic, "id = ?", epicID)
	if epic.Status != "done" {
		t.Errorf("epic status = %q, want %q", epic.Status, "done")
	}
	if !strings.Contains(buf.String(), "auto-closing epic") {
		t.Errorf("output = %q, want to mention auto-closing", buf.String())
	}
}

func TestSweepOpenEpics_SkipsEpicWithPendingChildren(t *testing.T) {
	db := testDB(t)

	epicID := "epic-sweep2"
	db.Create(&models.Car{ID: epicID, Type: "epic", Status: "open", Track: "backend"})
	db.Create(&models.Car{ID: "child-sw3", Type: "task", Status: "merged", Track: "backend", ParentID: &epicID})
	db.Create(&models.Car{ID: "child-sw4", Type: "task", Status: "in_progress", Track: "backend", ParentID: &epicID})

	var buf bytes.Buffer
	if err := sweepOpenEpics(db, &buf); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var epic models.Car
	db.First(&epic, "id = ?", epicID)
	if epic.Status != "open" {
		t.Errorf("epic status = %q, want %q", epic.Status, "open")
	}
}

func TestSweepOpenEpics_SkipsEmptyEpic(t *testing.T) {
	db := testDB(t)

	db.Create(&models.Car{ID: "epic-sweep3", Type: "epic", Status: "open", Track: "backend"})

	var buf bytes.Buffer
	if err := sweepOpenEpics(db, &buf); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var epic models.Car
	db.First(&epic, "id = ?", "epic-sweep3")
	if epic.Status != "open" {
		t.Errorf("empty epic should stay open, got %q", epic.Status)
	}
}

// ---------------------------------------------------------------------------
// processInbox drain tests
// ---------------------------------------------------------------------------

func TestProcessInbox_StaleDrainIgnored(t *testing.T) {
	db := testDB(t)

	// Simulate a drain broadcast sent 10 minutes ago (before yardmaster started).
	staleDrain := models.Message{
		FromAgent: "orchestrator",
		ToAgent:   "broadcast",
		Subject:   "drain",
		Body:      "Railyard shutting down.",
	}
	db.Create(&staleDrain)
	// Backdate the CreatedAt to before startup.
	db.Model(&models.Message{}).Where("id = ?", staleDrain.ID).
		Update("created_at", time.Now().Add(-10*time.Minute))

	startedAt := time.Now()
	var buf bytes.Buffer
	draining, err := processInbox(context.Background(), db, nil, "", "", startedAt, &buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if draining {
		t.Fatal("should NOT drain on stale message")
	}
	if !strings.Contains(buf.String(), "stale drain message") {
		t.Errorf("output = %q, want to mention stale drain", buf.String())
	}
}

func TestProcessInbox_FreshDrainHonored(t *testing.T) {
	db := testDB(t)

	// Yardmaster started 5 minutes ago.
	startedAt := time.Now().Add(-5 * time.Minute)

	// Fresh drain sent just now (after startup).
	freshDrain := models.Message{
		FromAgent: "orchestrator",
		ToAgent:   "broadcast",
		Subject:   "drain",
		Body:      "Railyard shutting down.",
	}
	db.Create(&freshDrain)

	var buf bytes.Buffer
	draining, err := processInbox(context.Background(), db, nil, "", "", startedAt, &buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !draining {
		t.Fatal("should drain on fresh message")
	}
}

func TestProcessInbox_EmptyInbox(t *testing.T) {
	db := testDB(t)

	startedAt := time.Now()
	var buf bytes.Buffer
	draining, err := processInbox(context.Background(), db, nil, "", "", startedAt, &buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if draining {
		t.Fatal("should NOT drain on empty inbox")
	}
}
