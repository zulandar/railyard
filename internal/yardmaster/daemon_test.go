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

// ---------------------------------------------------------------------------
// countRecentSwitchFailures tests
// ---------------------------------------------------------------------------

func TestCountRecentSwitchFailures_Empty(t *testing.T) {
	db := testDB(t)
	db.Create(&models.Car{ID: "car-csf1", Track: "backend"})

	count := countRecentSwitchFailures(db, "car-csf1")
	if count != 0 {
		t.Errorf("count = %d, want 0", count)
	}
}

func TestCountRecentSwitchFailures_CountsCategorized(t *testing.T) {
	db := testDB(t)
	db.Create(&models.Car{ID: "car-csf2", Track: "backend"})

	// Write various categorized progress notes.
	writeProgressNote(db, "car-csf2", "yardmaster", "switch:merge-conflict: git merge failed")
	writeProgressNote(db, "car-csf2", "yardmaster", "switch:fetch-failed: network error")
	writeProgressNote(db, "car-csf2", "yardmaster", "switch:test-failed: FAIL TestFoo")

	count := countRecentSwitchFailures(db, "car-csf2")
	if count != 3 {
		t.Errorf("count = %d, want 3", count)
	}
}

func TestCountRecentSwitchFailures_IgnoresNonSwitch(t *testing.T) {
	db := testDB(t)
	db.Create(&models.Car{ID: "car-csf3", Track: "backend"})

	// Non-switch progress notes should be ignored.
	writeProgressNote(db, "car-csf3", "eng-001", "Implemented feature X")
	writeProgressNote(db, "car-csf3", "eng-001", "Engine stalled: timeout")
	// One switch note.
	writeProgressNote(db, "car-csf3", "yardmaster", "switch:push-failed: auth error")

	count := countRecentSwitchFailures(db, "car-csf3")
	if count != 1 {
		t.Errorf("count = %d, want 1", count)
	}
}

// ---------------------------------------------------------------------------
// switchFailureReason tests
// ---------------------------------------------------------------------------

func TestSwitchFailureReason_AllCategories(t *testing.T) {
	tests := []struct {
		cat  SwitchFailureCategory
		want string
	}{
		{SwitchFailFetch, "repeated-fetch-failure"},
		{SwitchFailPreTest, "repeated-pre-test-failure"},
		{SwitchFailTest, "repeated-test-failure"},
		{SwitchFailInfra, "infrastructure-test-failure"},
		{SwitchFailMerge, "repeated-merge-conflict"},
		{SwitchFailPush, "repeated-push-failure"},
		{SwitchFailPR, "repeated-pr-failure"},
		{SwitchFailNone, "repeated-switch-failure"},
	}

	for _, tt := range tests {
		got := switchFailureReason(tt.cat)
		if got != tt.want {
			t.Errorf("switchFailureReason(%q) = %q, want %q", tt.cat, got, tt.want)
		}
	}
}

// ---------------------------------------------------------------------------
// maybeSwitchEscalate tests (threshold behavior only — no real Claude call)
// ---------------------------------------------------------------------------

func TestMaybeSwitchEscalate_BelowThreshold(t *testing.T) {
	db := testDB(t)
	db.Create(&models.Car{ID: "car-esc1", Track: "backend"})

	// Only 2 failures, threshold is 3.
	writeProgressNote(db, "car-esc1", "yardmaster", "switch:merge-conflict: conflict 1")
	writeProgressNote(db, "car-esc1", "yardmaster", "switch:merge-conflict: conflict 2")

	cfg := testConfig(config.TrackConfig{Name: "backend", Language: "go"})
	cfg.Stall.MaxSwitchFailures = 3

	var buf bytes.Buffer
	// This should NOT escalate (below threshold), so no "escalating" in output.
	maybeSwitchEscalate(context.Background(), db, cfg, "car-esc1", SwitchFailMerge, nil, &buf)

	if strings.Contains(buf.String(), "escalating") {
		t.Errorf("should not escalate below threshold, got: %s", buf.String())
	}
}

func TestMaybeSwitchEscalate_AtThreshold(t *testing.T) {
	db := testDB(t)
	db.Create(&models.Car{ID: "car-esc2", Track: "backend"})

	// 3 failures, threshold is 3.
	writeProgressNote(db, "car-esc2", "yardmaster", "switch:fetch-failed: err 1")
	writeProgressNote(db, "car-esc2", "yardmaster", "switch:fetch-failed: err 2")
	writeProgressNote(db, "car-esc2", "yardmaster", "switch:fetch-failed: err 3")

	cfg := testConfig(config.TrackConfig{Name: "backend", Language: "go"})
	cfg.Stall.MaxSwitchFailures = 3

	var buf bytes.Buffer
	// Escalation will fire (at threshold). The actual Claude call will fail
	// since there's no `claude` binary in CI, but we can verify the log output.
	maybeSwitchEscalate(context.Background(), db, cfg, "car-esc2", SwitchFailFetch, nil, &buf)

	if !strings.Contains(buf.String(), "escalating") {
		t.Errorf("should escalate at threshold, got: %s", buf.String())
	}
	if !strings.Contains(buf.String(), "repeated-fetch-failure") {
		t.Errorf("output should mention failure reason, got: %s", buf.String())
	}
}

func TestMaybeSwitchEscalate_InfraEscalatesImmediately(t *testing.T) {
	db := testDB(t)
	db.Create(&models.Car{ID: "car-infra1", Track: "backend"})

	// NO prior failures — infra should escalate on first occurrence.
	cfg := testConfig(config.TrackConfig{Name: "backend", Language: "go"})
	cfg.Stall.MaxSwitchFailures = 3

	var buf bytes.Buffer
	maybeSwitchEscalate(context.Background(), db, cfg, "car-infra1", SwitchFailInfra, nil, &buf)

	if !strings.Contains(buf.String(), "infra failure") {
		t.Errorf("should escalate immediately for infra, got: %s", buf.String())
	}
	if !strings.Contains(buf.String(), "escalating immediately") {
		t.Errorf("output should say 'escalating immediately', got: %s", buf.String())
	}
}
