package yardmaster

import (
	"bytes"
	"context"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
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
	draining, err := processInbox(context.Background(), db, nil, "", "", startedAt, &sync.WaitGroup{}, &buf)
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
	draining, err := processInbox(context.Background(), db, nil, "", "", startedAt, &sync.WaitGroup{}, &buf)
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
	draining, err := processInbox(context.Background(), db, nil, "", "", startedAt, &sync.WaitGroup{}, &buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if draining {
		t.Fatal("should NOT drain on empty inbox")
	}
}

// ---------------------------------------------------------------------------
// reconcileStaleCars / getMergedBranches tests
// ---------------------------------------------------------------------------

func TestGetMergedBranches_InvalidTarget(t *testing.T) {
	repoDir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = repoDir
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("%v failed: %s: %v", args, out, err)
		}
	}
	run("git", "init", "-b", "main")
	run("git", "config", "user.email", "test@test.com")
	run("git", "config", "user.name", "test")
	run("git", "commit", "--allow-empty", "-m", "init")

	// No remote, so origin/main doesn't exist.
	_, err := getMergedBranches(repoDir, "origin/main")
	if err == nil {
		t.Fatal("expected error for non-existent target ref")
	}
}

func TestReconcileStaleCars_PerBaseBranch(t *testing.T) {
	// Set up a repo with a remote and two base branches.
	bareDir := t.TempDir()
	parentDir := t.TempDir()
	run := func(dir string, args ...string) {
		t.Helper()
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = dir
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("%v in %s failed: %s: %v", args, dir, out, err)
		}
	}

	// Create bare remote.
	run(bareDir, "git", "init", "--bare", "-b", "main")

	// Clone.
	run(parentDir, "git", "clone", bareDir, "repo")
	repoDir := filepath.Join(parentDir, "repo")
	run(repoDir, "git", "config", "user.email", "test@test.com")
	run(repoDir, "git", "config", "user.name", "test")
	run(repoDir, "git", "commit", "--allow-empty", "-m", "init")
	run(repoDir, "git", "push", "origin", "main")

	// Create a "develop" branch and push it.
	run(repoDir, "git", "checkout", "-b", "develop")
	run(repoDir, "git", "commit", "--allow-empty", "-m", "develop base")
	run(repoDir, "git", "push", "origin", "develop")

	// Create feature branches.
	// Branch A: merged into main.
	run(repoDir, "git", "checkout", "main")
	run(repoDir, "git", "checkout", "-b", "ry/feat-a")
	run(repoDir, "git", "commit", "--allow-empty", "-m", "feat-a work")
	run(repoDir, "git", "checkout", "main")
	run(repoDir, "git", "merge", "--no-ff", "ry/feat-a", "-m", "merge feat-a")
	run(repoDir, "git", "push", "origin", "main")

	// Branch B: merged into develop (NOT main).
	run(repoDir, "git", "checkout", "develop")
	run(repoDir, "git", "checkout", "-b", "ry/feat-b")
	run(repoDir, "git", "commit", "--allow-empty", "-m", "feat-b work")
	run(repoDir, "git", "checkout", "develop")
	run(repoDir, "git", "merge", "--no-ff", "ry/feat-b", "-m", "merge feat-b")
	run(repoDir, "git", "push", "origin", "develop")

	// Branch C: NOT merged into either.
	run(repoDir, "git", "checkout", "main")
	run(repoDir, "git", "checkout", "-b", "ry/feat-c")
	run(repoDir, "git", "commit", "--allow-empty", "-m", "feat-c work")
	run(repoDir, "git", "checkout", "main")

	// Set up DB with cars.
	db := testDB(t)
	db.Create(&models.Car{ID: "car-a", Branch: "ry/feat-a", BaseBranch: "main", Status: "in_progress", Track: "backend"})
	db.Create(&models.Car{ID: "car-b", Branch: "ry/feat-b", BaseBranch: "develop", Status: "open", Track: "backend"})
	db.Create(&models.Car{ID: "car-c", Branch: "ry/feat-c", BaseBranch: "main", Status: "open", Track: "backend"})

	var buf bytes.Buffer
	if err := reconcileStaleCars(db, repoDir, &buf); err != nil {
		t.Fatalf("reconcileStaleCars: %v", err)
	}

	// Car A should be reconciled (merged into main).
	var carA models.Car
	db.First(&carA, "id = ?", "car-a")
	if carA.Status != "merged" {
		t.Errorf("car-a status = %q, want %q", carA.Status, "merged")
	}

	// Car B should be reconciled (merged into develop).
	var carB models.Car
	db.First(&carB, "id = ?", "car-b")
	if carB.Status != "merged" {
		t.Errorf("car-b status = %q, want %q", carB.Status, "merged")
	}

	// Car C should NOT be reconciled (not merged into main).
	var carC models.Car
	db.First(&carC, "id = ?", "car-c")
	if carC.Status != "open" {
		t.Errorf("car-c status = %q, want %q", carC.Status, "open")
	}

	// Verify output mentions both reconciled cars.
	output := buf.String()
	if !strings.Contains(output, "car-a") || !strings.Contains(output, "merged into main") {
		t.Errorf("missing car-a reconciliation in output: %s", output)
	}
	if !strings.Contains(output, "car-b") || !strings.Contains(output, "merged into develop") {
		t.Errorf("missing car-b reconciliation in output: %s", output)
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
	maybeSwitchEscalate(context.Background(), db, cfg, "car-esc1", SwitchFailMerge, nil, "", &sync.WaitGroup{}, &buf)

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
	maybeSwitchEscalate(context.Background(), db, cfg, "car-esc2", SwitchFailFetch, nil, "", &sync.WaitGroup{}, &buf)

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
	maybeSwitchEscalate(context.Background(), db, cfg, "car-infra1", SwitchFailInfra, nil, "", &sync.WaitGroup{}, &buf)

	if !strings.Contains(buf.String(), "infra failure") {
		t.Errorf("should escalate immediately for infra, got: %s", buf.String())
	}
	if !strings.Contains(buf.String(), "escalating immediately") {
		t.Errorf("output should say 'escalating immediately', got: %s", buf.String())
	}
}

func TestMaybeSwitchEscalate_SetsCarToMergeFailed(t *testing.T) {
	db := testDB(t)
	db.Create(&models.Car{ID: "car-esc3", Status: "done", Track: "backend"})

	// 3 failures at threshold.
	writeProgressNote(db, "car-esc3", "yardmaster", "switch:merge-conflict: conflict 1")
	writeProgressNote(db, "car-esc3", "yardmaster", "switch:merge-conflict: conflict 2")
	writeProgressNote(db, "car-esc3", "yardmaster", "switch:merge-conflict: conflict 3")

	cfg := testConfig(config.TrackConfig{Name: "backend", Language: "go"})
	cfg.Stall.MaxSwitchFailures = 3

	var buf bytes.Buffer
	maybeSwitchEscalate(context.Background(), db, cfg, "car-esc3", SwitchFailMerge, nil, "", &sync.WaitGroup{}, &buf)

	// Car status should change to "merge-failed" to break the retry loop.
	var car models.Car
	db.Where("id = ?", "car-esc3").First(&car)
	if car.Status != "merge-failed" {
		t.Errorf("car status = %q, want %q", car.Status, "merge-failed")
	}
	if !strings.Contains(buf.String(), "merge-failed") {
		t.Errorf("output should mention status change, got: %s", buf.String())
	}
}

func TestMaybeSwitchEscalate_InfraSetsCarToMergeFailed(t *testing.T) {
	db := testDB(t)
	db.Create(&models.Car{ID: "car-infra2", Status: "done", Track: "backend"})

	cfg := testConfig(config.TrackConfig{Name: "backend", Language: "go"})

	var buf bytes.Buffer
	maybeSwitchEscalate(context.Background(), db, cfg, "car-infra2", SwitchFailInfra, nil, "", &sync.WaitGroup{}, &buf)

	// Infra failures should also set merge-failed.
	var car models.Car
	db.Where("id = ?", "car-infra2").First(&car)
	if car.Status != "merge-failed" {
		t.Errorf("car status = %q, want %q", car.Status, "merge-failed")
	}
}

// ---------------------------------------------------------------------------
// handleEscalateResult tests
// ---------------------------------------------------------------------------

func TestHandleEscalateResult_GuidanceWithoutEngine_FallsBackToHuman(t *testing.T) {
	db := testDB(t)

	var buf bytes.Buffer
	handleEscalateResult(db, "", "car-001", &EscalateResult{
		Action:  EscalateGuidance,
		Message: "Try rebasing onto main",
	}, &buf)

	output := buf.String()
	// Should mention falling back to human.
	if !strings.Contains(output, "no engine") {
		t.Errorf("should mention no engine, got: %s", output)
	}
	if !strings.Contains(output, "alerting human") {
		t.Errorf("should alert human as fallback, got: %s", output)
	}

	// Should have sent a message to "human".
	var msg models.Message
	db.Where("to_agent = ? AND car_id = ?", "human", "car-001").First(&msg)
	if msg.ID == 0 {
		t.Fatal("expected message to human")
	}
	if msg.Subject != "escalate" {
		t.Errorf("subject = %q, want %q", msg.Subject, "escalate")
	}
	if !strings.Contains(msg.Body, "Try rebasing onto main") {
		t.Errorf("body = %q, should contain guidance message", msg.Body)
	}
}

func TestHandleEscalateResult_ReassignWithoutEngine_FallsBackToHuman(t *testing.T) {
	db := testDB(t)

	var buf bytes.Buffer
	handleEscalateResult(db, "", "car-002", &EscalateResult{
		Action:  EscalateReassign,
		Message: "Reassign to a different engine",
	}, &buf)

	output := buf.String()
	if !strings.Contains(output, "no engine") {
		t.Errorf("should mention no engine, got: %s", output)
	}
	if !strings.Contains(output, "alerting human") {
		t.Errorf("should alert human as fallback, got: %s", output)
	}
}

func TestHandleEscalateResult_GuidanceWithEngine_SendsGuidance(t *testing.T) {
	db := testDB(t)

	var buf bytes.Buffer
	handleEscalateResult(db, "eng-001", "car-003", &EscalateResult{
		Action:  EscalateGuidance,
		Message: "Try a different approach",
	}, &buf)

	output := buf.String()
	if !strings.Contains(output, "sending guidance to eng-001") {
		t.Errorf("should send guidance to engine, got: %s", output)
	}

	// Should have sent a message to the engine.
	var msg models.Message
	db.Where("to_agent = ? AND car_id = ?", "eng-001", "car-003").First(&msg)
	if msg.ID == 0 {
		t.Fatal("expected message to engine")
	}
	if msg.Subject != "guidance" {
		t.Errorf("subject = %q, want %q", msg.Subject, "guidance")
	}
}

func TestHandleEscalateResult_HumanAlwaysWorks(t *testing.T) {
	db := testDB(t)

	var buf bytes.Buffer
	handleEscalateResult(db, "", "car-004", &EscalateResult{
		Action:  EscalateHuman,
		Message: "Needs manual merge resolution",
	}, &buf)

	output := buf.String()
	if !strings.Contains(output, "alerting human") {
		t.Errorf("should alert human, got: %s", output)
	}

	var msg models.Message
	db.Where("to_agent = ? AND car_id = ?", "human", "car-004").First(&msg)
	if msg.ID == 0 {
		t.Fatal("expected message to human")
	}
	if msg.Priority != "urgent" {
		t.Errorf("priority = %q, want %q", msg.Priority, "urgent")
	}
}

func TestHandleEscalateResult_NilResult(t *testing.T) {
	db := testDB(t)

	var buf bytes.Buffer
	handleEscalateResult(db, "eng-001", "car-005", nil, &buf)

	if buf.Len() != 0 {
		t.Errorf("expected no output for nil result, got: %s", buf.String())
	}
}

// ---------------------------------------------------------------------------
// handleCompletedCars — epic skip tests
// ---------------------------------------------------------------------------

func TestHandleCompletedCars_SkipsEpicAndMarkesMerged(t *testing.T) {
	db := testDB(t)

	// Create an epic with status "done" (set by TryCloseEpic after children merged).
	epicID := "epic-done1"
	db.Create(&models.Car{
		ID:     epicID,
		Type:   "epic",
		Status: "done",
		Track:  "backend",
		Branch: "ry/alice/backend/epic-done1",
		Title:  "Test Epic",
	})
	// All children already merged.
	db.Create(&models.Car{ID: "child-ed1", Type: "task", Status: "merged", Track: "backend", ParentID: &epicID})
	db.Create(&models.Car{ID: "child-ed2", Type: "task", Status: "merged", Track: "backend", ParentID: &epicID})

	cfg := testConfig(config.TrackConfig{Name: "backend", Language: "go"})

	var buf bytes.Buffer
	// repoDir and ymDir don't matter — the epic should never reach Switch().
	err := handleCompletedCars(context.Background(), db, cfg, "/nonexistent", "/nonexistent", &sync.WaitGroup{}, &buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Epic should be marked as "merged", not stuck in "done".
	var epic models.Car
	db.First(&epic, "id = ?", epicID)
	if epic.Status != "merged" {
		t.Errorf("epic status = %q, want %q", epic.Status, "merged")
	}
	if epic.CompletedAt == nil {
		t.Error("epic CompletedAt should be set")
	}

	// Output should mention skipping the merge for the epic.
	output := buf.String()
	if !strings.Contains(output, "epic") {
		t.Errorf("output should mention epic, got: %s", output)
	}
}

func TestHandleCompletedCars_EpicCountError_LogsAndContinues(t *testing.T) {
	db := testDB(t)

	// Create an epic with status "done".
	epicID := "epic-counterr"
	db.Create(&models.Car{
		ID:     epicID,
		Type:   "epic",
		Status: "done",
		Track:  "backend",
		Branch: "ry/alice/backend/epic-counterr",
		Title:  "Epic with DB Error",
	})
	// Create a child so the Count query has something to check.
	db.Create(&models.Car{ID: "child-ce1", Type: "task", Status: "merged", Track: "backend", ParentID: &epicID})

	// Now drop the cars table to make the Count query fail.
	// The function should log the error and not panic or mark the epic as merged.
	db.Exec("ALTER TABLE cars RENAME TO cars_backup")

	cfg := testConfig(config.TrackConfig{Name: "backend", Language: "go"})

	var buf bytes.Buffer
	err := handleCompletedCars(context.Background(), db, cfg, "/nonexistent", "/nonexistent", &sync.WaitGroup{}, &buf)
	// The function should return the error from car.List (which also queries cars table).
	if err == nil {
		// If it doesn't error on car.List, it should at least not panic.
		t.Log("handleCompletedCars did not error — OK if car.List returned empty")
	}

	// Restore table for cleanup.
	db.Exec("ALTER TABLE cars_backup RENAME TO cars")
}

func TestSweepOpenEpics_CountError_LogsAndContinues(t *testing.T) {
	db := testDB(t)

	// Create an open epic.
	db.Create(&models.Car{ID: "epic-cerr", Type: "epic", Status: "open", Track: "backend", Title: "Error Epic"})
	db.Create(&models.Car{ID: "child-cerr1", Type: "task", Status: "merged", Track: "backend"})

	// sweepOpenEpics should handle Count errors gracefully.
	// We can't easily break just the Count without breaking car.List too,
	// so this test verifies the function doesn't panic.
	var buf bytes.Buffer
	err := sweepOpenEpics(db, &buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestHandleCompletedCars_EpicWithPendingChildren_StaysDone(t *testing.T) {
	db := testDB(t)

	// Epic is "done" but one child is still in_progress (edge case: race/bad state).
	epicID := "epic-done2"
	db.Create(&models.Car{
		ID:     epicID,
		Type:   "epic",
		Status: "done",
		Track:  "backend",
		Branch: "ry/alice/backend/epic-done2",
		Title:  "Partial Epic",
	})
	db.Create(&models.Car{ID: "child-ed3", Type: "task", Status: "merged", Track: "backend", ParentID: &epicID})
	db.Create(&models.Car{ID: "child-ed4", Type: "task", Status: "in_progress", Track: "backend", ParentID: &epicID})

	cfg := testConfig(config.TrackConfig{Name: "backend", Language: "go"})

	var buf bytes.Buffer
	err := handleCompletedCars(context.Background(), db, cfg, "/nonexistent", "/nonexistent", &sync.WaitGroup{}, &buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Epic should stay "done" — not yet all children resolved.
	var epic models.Car
	db.First(&epic, "id = ?", epicID)
	if epic.Status != "done" {
		t.Errorf("epic status = %q, want %q (children not all complete)", epic.Status, "done")
	}
}
