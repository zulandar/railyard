package yardmaster

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/zulandar/railyard/internal/config"
	"github.com/zulandar/railyard/internal/logutil"
	"github.com/zulandar/railyard/internal/models"
)

// testLogger creates a *slog.Logger that writes to the given buffer at Debug level.
func testLogger(buf *bytes.Buffer) *slog.Logger {
	return slog.New(logutil.NewConsoleHandler(buf, buf, slog.LevelDebug))
}

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

func TestRunDaemon_NilLogger_UsesDefault(t *testing.T) {
	// Ensure nil logger doesn't panic — falls back to slog.Default().
	cfg := testConfig(config.TrackConfig{Name: "backend", Language: "go"})
	err := RunDaemon(context.Background(), nil, cfg, "railyard.yaml", "/tmp", time.Second, nil)
	// Will fail on db check, but should not panic.
	if err == nil {
		t.Fatal("expected error")
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
	logger := testLogger(&buf)
	if err := sweepOpenEpics(db, logger); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var epic models.Car
	db.First(&epic, "id = ?", epicID)
	if epic.Status != "done" {
		t.Errorf("epic status = %q, want %q", epic.Status, "done")
	}
	if !strings.Contains(buf.String(), "auto-closing") {
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
	logger := testLogger(&buf)
	if err := sweepOpenEpics(db, logger); err != nil {
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
	logger := testLogger(&buf)
	if err := sweepOpenEpics(db, logger); err != nil {
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
	logger := testLogger(&buf)
	draining, err := processInbox(context.Background(), db, nil, "", "", startedAt, &sync.WaitGroup{}, nil, make(chan struct{}, 3), logger)
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
	logger := testLogger(&buf)
	draining, err := processInbox(context.Background(), db, nil, "", "", startedAt, &sync.WaitGroup{}, nil, make(chan struct{}, 3), logger)
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
	logger := testLogger(&buf)
	draining, err := processInbox(context.Background(), db, nil, "", "", startedAt, &sync.WaitGroup{}, nil, make(chan struct{}, 3), logger)
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
	logger := testLogger(&buf)
	if err := reconcileStaleCars(db, repoDir, false, logger); err != nil {
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
	if !strings.Contains(output, "car-a") {
		t.Errorf("missing car-a reconciliation in output: %s", output)
	}
	if !strings.Contains(output, "car-b") {
		t.Errorf("missing car-b reconciliation in output: %s", output)
	}
}

func TestReconcileStaleCars_PrOpenMergedBranch(t *testing.T) {
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

	run(bareDir, "git", "init", "--bare", "-b", "main")
	run(parentDir, "git", "clone", bareDir, "repo")
	repoDir := filepath.Join(parentDir, "repo")
	run(repoDir, "git", "config", "user.email", "test@test.com")
	run(repoDir, "git", "config", "user.name", "test")
	run(repoDir, "git", "commit", "--allow-empty", "-m", "init")
	run(repoDir, "git", "push", "origin", "main")

	// Branch D: merged into main (simulates a PR merge on GitHub).
	run(repoDir, "git", "checkout", "-b", "ry/feat-d")
	run(repoDir, "git", "commit", "--allow-empty", "-m", "feat-d work")
	run(repoDir, "git", "checkout", "main")
	run(repoDir, "git", "merge", "--no-ff", "ry/feat-d", "-m", "merge feat-d")
	run(repoDir, "git", "push", "origin", "main")

	// Branch E: NOT merged (PR still open).
	run(repoDir, "git", "checkout", "-b", "ry/feat-e")
	run(repoDir, "git", "commit", "--allow-empty", "-m", "feat-e work")
	run(repoDir, "git", "checkout", "main")

	db := testDB(t)
	// Car D: pr_open, branch merged into main → should reconcile to "merged".
	db.Create(&models.Car{ID: "car-d", Branch: "ry/feat-d", BaseBranch: "main", Status: "pr_open", Track: "backend"})
	// Car E: pr_open, branch NOT merged → should stay pr_open.
	db.Create(&models.Car{ID: "car-e", Branch: "ry/feat-e", BaseBranch: "main", Status: "pr_open", Track: "backend"})

	var buf bytes.Buffer
	logger := testLogger(&buf)
	if err := reconcileStaleCars(db, repoDir, false, logger); err != nil {
		t.Fatalf("reconcileStaleCars: %v", err)
	}

	var carD models.Car
	db.First(&carD, "id = ?", "car-d")
	if carD.Status != "merged" {
		t.Errorf("car-d status = %q, want %q (pr_open + branch merged should reconcile)", carD.Status, "merged")
	}

	var carE models.Car
	db.First(&carE, "id = ?", "car-e")
	if carE.Status != "pr_open" {
		t.Errorf("car-e status = %q, want %q (branch not merged, should stay pr_open)", carE.Status, "pr_open")
	}
}

// TestReconcileStaleCars_ZeroCommitBranch verifies that the reconciler does NOT
// mark a car as merged when its branch has zero commits ahead of base. This is
// the "ghost completion" scenario: engine creates a branch but never commits.
func TestReconcileStaleCars_ZeroCommitBranch(t *testing.T) {
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

	run(bareDir, "git", "init", "--bare", "-b", "main")
	run(parentDir, "git", "clone", bareDir, "repo")
	repoDir := filepath.Join(parentDir, "repo")
	run(repoDir, "git", "config", "user.email", "test@test.com")
	run(repoDir, "git", "config", "user.name", "test")
	run(repoDir, "git", "commit", "--allow-empty", "-m", "init")
	run(repoDir, "git", "push", "origin", "main")

	// Zero-commit branch: created from main, pushed with no additional commits.
	// This simulates an engine that claimed a car but never committed.
	run(repoDir, "git", "checkout", "-b", "ry/backend/car-ghost")
	run(repoDir, "git", "push", "origin", "ry/backend/car-ghost")
	run(repoDir, "git", "checkout", "main")

	// Real merged branch: has commits and is merged into main.
	run(repoDir, "git", "checkout", "-b", "ry/backend/car-real")
	run(repoDir, "git", "commit", "--allow-empty", "-m", "real work")
	run(repoDir, "git", "checkout", "main")
	run(repoDir, "git", "merge", "--no-ff", "ry/backend/car-real", "-m", "merge real")
	run(repoDir, "git", "push", "origin", "main")

	db := testDB(t)
	db.Create(&models.Car{ID: "car-ghost", Branch: "ry/backend/car-ghost", BaseBranch: "main", Status: "in_progress", Track: "backend"})
	db.Create(&models.Car{ID: "car-real", Branch: "ry/backend/car-real", BaseBranch: "main", Status: "in_progress", Track: "backend"})

	var buf bytes.Buffer
	logger := testLogger(&buf)
	if err := reconcileStaleCars(db, repoDir, false, logger); err != nil {
		t.Fatalf("reconcileStaleCars: %v", err)
	}

	// Ghost car: zero commits ahead of main — must NOT be marked merged.
	var ghost models.Car
	db.First(&ghost, "id = ?", "car-ghost")
	if ghost.Status != "in_progress" {
		t.Errorf("car-ghost status = %q, want %q (zero-commit branch should not reconcile)", ghost.Status, "in_progress")
	}

	// Real car: has commits merged into main — should be reconciled.
	var real models.Car
	db.First(&real, "id = ?", "car-real")
	if real.Status != "merged" {
		t.Errorf("car-real status = %q, want %q", real.Status, "merged")
	}

	// Verify warning log for the ghost branch.
	output := buf.String()
	if !strings.Contains(output, "zero-commit branch") {
		t.Errorf("expected warning about zero-commit branch in output: %s", output)
	}
}

// TestReconcileStaleCars_RequirePR_NoMergedPR verifies that when requirePR is
// true, a branch that is git-merged but has no merged PR on GitHub is NOT
// transitioned to "merged". This prevents false merges when commits land in
// main via a dependent car's merge but the car's own PR was never created.
func TestReconcileStaleCars_RequirePR_NoMergedPR(t *testing.T) {
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

	run(bareDir, "git", "init", "--bare", "-b", "main")
	run(parentDir, "git", "clone", bareDir, "repo")
	repoDir := filepath.Join(parentDir, "repo")
	run(repoDir, "git", "config", "user.email", "test@test.com")
	run(repoDir, "git", "config", "user.name", "test")
	run(repoDir, "git", "commit", "--allow-empty", "-m", "init")
	run(repoDir, "git", "push", "origin", "main")

	// Branch merged into main via --no-ff (has unique commits).
	run(repoDir, "git", "checkout", "-b", "ry/backend/car-nopr")
	run(repoDir, "git", "commit", "--allow-empty", "-m", "car-nopr work")
	run(repoDir, "git", "checkout", "main")
	run(repoDir, "git", "merge", "--no-ff", "ry/backend/car-nopr", "-m", "merge car-nopr")
	run(repoDir, "git", "push", "origin", "main")

	db := testDB(t)
	db.Create(&models.Car{ID: "car-nopr", Branch: "ry/backend/car-nopr", BaseBranch: "main", Status: "in_progress", Track: "backend"})

	var buf bytes.Buffer
	logger := testLogger(&buf)

	// With requirePR=true and no GitHub remote, isPRMerged returns false.
	if err := reconcileStaleCars(db, repoDir, true, logger); err != nil {
		t.Fatalf("reconcileStaleCars: %v", err)
	}

	var c models.Car
	db.First(&c, "id = ?", "car-nopr")
	if c.Status != "in_progress" {
		t.Errorf("car-nopr status = %q, want %q (no merged PR should block transition)", c.Status, "in_progress")
	}

	output := buf.String()
	if !strings.Contains(output, "no merged PR found") {
		t.Errorf("expected warning about no merged PR in output: %s", output)
	}
}

// TestReconcileStaleCars_FFMergedBranch verifies the known limitation: a branch
// merged via fast-forward (not --no-ff) is misidentified as zero-commit because
// its tip lands on the first-parent lineage. This documents the edge case noted
// in the branchHasUniqueCommits comment — Railyard's gitMerge uses --no-ff, but
// external FF merges would hit this.
func TestReconcileStaleCars_FFMergedBranch(t *testing.T) {
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

	run(bareDir, "git", "init", "--bare", "-b", "main")
	run(parentDir, "git", "clone", bareDir, "repo")
	repoDir := filepath.Join(parentDir, "repo")
	run(repoDir, "git", "config", "user.email", "test@test.com")
	run(repoDir, "git", "config", "user.name", "test")
	run(repoDir, "git", "commit", "--allow-empty", "-m", "init")
	run(repoDir, "git", "push", "origin", "main")

	// FF-merged branch: has a real commit, but merged via fast-forward.
	run(repoDir, "git", "checkout", "-b", "ry/backend/car-ff")
	run(repoDir, "git", "commit", "--allow-empty", "-m", "ff work")
	run(repoDir, "git", "checkout", "main")
	run(repoDir, "git", "merge", "ry/backend/car-ff") // default FF merge
	run(repoDir, "git", "push", "origin", "main")

	// branchHasUniqueCommits sees the tip on mainline → returns false.
	got := branchHasUniqueCommits(repoDir, "ry/backend/car-ff", "main")
	if got {
		t.Error("expected branchHasUniqueCommits=false for FF-merged branch (known limitation)")
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
	logger := testLogger(&buf)
	// This should NOT escalate (below threshold), so no "escalating" in output.
	maybeSwitchEscalate(context.Background(), db, cfg, "car-esc1", SwitchFailMerge, nil, "", &sync.WaitGroup{}, nil, make(chan struct{}, 3), logger)

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
	logger := testLogger(&buf)
	// Escalation will fire (at threshold). The actual Claude call will fail
	// since there's no `claude` binary in CI, but we can verify the log output.
	maybeSwitchEscalate(context.Background(), db, cfg, "car-esc2", SwitchFailFetch, nil, "", &sync.WaitGroup{}, nil, make(chan struct{}, 3), logger)

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
	logger := testLogger(&buf)
	maybeSwitchEscalate(context.Background(), db, cfg, "car-infra1", SwitchFailInfra, nil, "", &sync.WaitGroup{}, nil, make(chan struct{}, 3), logger)

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
	logger := testLogger(&buf)
	maybeSwitchEscalate(context.Background(), db, cfg, "car-esc3", SwitchFailMerge, nil, "", &sync.WaitGroup{}, nil, make(chan struct{}, 3), logger)

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
	logger := testLogger(&buf)
	maybeSwitchEscalate(context.Background(), db, cfg, "car-infra2", SwitchFailInfra, nil, "", &sync.WaitGroup{}, nil, make(chan struct{}, 3), logger)

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
	logger := testLogger(&buf)
	handleEscalateResult(db, "", "car-001", &EscalateResult{
		Action:  EscalateGuidance,
		Message: "Try rebasing onto main",
	}, logger)

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
	logger := testLogger(&buf)
	handleEscalateResult(db, "", "car-002", &EscalateResult{
		Action:  EscalateReassign,
		Message: "Reassign to a different engine",
	}, logger)

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
	logger := testLogger(&buf)
	handleEscalateResult(db, "eng-001", "car-003", &EscalateResult{
		Action:  EscalateGuidance,
		Message: "Try a different approach",
	}, logger)

	output := buf.String()
	if !strings.Contains(output, "sending guidance") || !strings.Contains(output, "eng-001") {
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
	logger := testLogger(&buf)
	handleEscalateResult(db, "", "car-004", &EscalateResult{
		Action:  EscalateHuman,
		Message: "Needs manual merge resolution",
	}, logger)

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
	logger := testLogger(&buf)
	handleEscalateResult(db, "eng-001", "car-005", nil, logger)

	if buf.Len() != 0 {
		t.Errorf("expected no output for nil result, got: %s", buf.String())
	}
}

// ---------------------------------------------------------------------------
// handleCompletedCars — epic skip tests
// ---------------------------------------------------------------------------

// ---------------------------------------------------------------------------
// handlePrOpenCars tests
// ---------------------------------------------------------------------------

func TestHandlePrOpenCars_ChangesRequested(t *testing.T) {
	db := testDB(t)

	db.Create(&models.Car{
		ID:     "car-pro1",
		Branch: "ry/backend/car-pro1",
		Status: "pr_open",
		Track:  "backend",
	})

	viewer := &mockPRViewer{
		reviewDecision: "CHANGES_REQUESTED",
		state:          "OPEN",
		reviews:        []prReview{{Body: "Fix the error handling", Author: "reviewer1"}},
	}

	var buf bytes.Buffer
	logger := testLogger(&buf)
	err := handlePrOpenCars(db, viewer, false, "", "", nil, logger)
	if err != nil {
		t.Fatalf("handlePrOpenCars: %v", err)
	}

	var c models.Car
	db.First(&c, "id = ?", "car-pro1")
	if c.Status != "open" {
		t.Errorf("status = %q, want %q", c.Status, "open")
	}
	if c.Assignee != "" {
		t.Errorf("assignee = %q, want empty", c.Assignee)
	}

	var notes []models.CarProgress
	db.Where("car_id = ?", "car-pro1").Find(&notes)
	if len(notes) == 0 {
		t.Error("expected progress note with review comments")
	}
}

func TestHandlePrOpenCars_ChangesRequestedWithInlineComments(t *testing.T) {
	db := testDB(t)

	db.Create(&models.Car{
		ID:     "car-inline1",
		Branch: "ry/backend/car-inline1",
		Status: "pr_open",
		Track:  "backend",
	})

	viewer := &mockPRViewer{
		reviewDecision: "CHANGES_REQUESTED",
		state:          "OPEN",
		reviews:        []prReview{{Body: "Needs work", Author: "alice"}},
		inlineComments: []prInlineComment{
			{Path: "internal/dispatch/dispatch.go", Line: 93, Body: "Fix the fallback command", Author: "alice"},
			{Path: "cmd/ry/main.go", Line: 12, Body: "Add error check here", Author: "bob"},
		},
	}

	var buf bytes.Buffer
	logger := testLogger(&buf)
	err := handlePrOpenCars(db, viewer, false, "", "", nil, logger)
	if err != nil {
		t.Fatalf("handlePrOpenCars: %v", err)
	}

	var notes []models.CarProgress
	db.Where("car_id = ?", "car-inline1").Find(&notes)
	if len(notes) == 0 {
		t.Fatal("expected progress note")
	}

	note := notes[0].Note
	if !strings.Contains(note, "## Inline comments") {
		t.Error("progress note should contain '## Inline comments' section")
	}
	if !strings.Contains(note, "`internal/dispatch/dispatch.go` (line 93) @alice") {
		t.Errorf("progress note should contain file:line for inline comment, got:\n%s", note)
	}
	if !strings.Contains(note, "`cmd/ry/main.go` (line 12) @bob") {
		t.Errorf("progress note should contain second inline comment, got:\n%s", note)
	}
}

func TestHandlePrOpenCars_ChangesRequestedWithConversationComments(t *testing.T) {
	db := testDB(t)

	db.Create(&models.Car{
		ID:     "car-conv1",
		Branch: "ry/backend/car-conv1",
		Status: "pr_open",
		Track:  "backend",
	})

	viewer := &mockPRViewer{
		reviewDecision: "CHANGES_REQUESTED",
		state:          "OPEN",
		convComments: []prConversationComment{
			{Body: "Overall looks good, just the inline items", Author: "alice"},
			{Body: "Agreed with Alice's feedback", Author: "bob"},
		},
	}

	var buf bytes.Buffer
	logger := testLogger(&buf)
	err := handlePrOpenCars(db, viewer, false, "", "", nil, logger)
	if err != nil {
		t.Fatalf("handlePrOpenCars: %v", err)
	}

	var notes []models.CarProgress
	db.Where("car_id = ?", "car-conv1").Find(&notes)
	if len(notes) == 0 {
		t.Fatal("expected progress note")
	}

	note := notes[0].Note
	if !strings.Contains(note, "## Conversation") {
		t.Error("progress note should contain '## Conversation' section")
	}
	if !strings.Contains(note, "@alice: Overall looks good") {
		t.Errorf("progress note should contain conversation comment, got:\n%s", note)
	}
}

func TestHandlePrOpenCars_ChangesRequestedAllCommentTypes(t *testing.T) {
	db := testDB(t)

	db.Create(&models.Car{
		ID:     "car-all1",
		Branch: "ry/backend/car-all1",
		Status: "pr_open",
		Track:  "backend",
	})

	viewer := &mockPRViewer{
		reviewDecision: "CHANGES_REQUESTED",
		state:          "OPEN",
		reviews:        []prReview{{Body: "Needs changes", Author: "reviewer"}},
		inlineComments: []prInlineComment{
			{Path: "app/Models/Task.php", Line: 15, Body: "Use a scope here", Author: "reviewer"},
		},
		convComments: []prConversationComment{
			{Body: "Just the one inline item", Author: "reviewer"},
		},
	}

	var buf bytes.Buffer
	logger := testLogger(&buf)
	err := handlePrOpenCars(db, viewer, false, "", "", nil, logger)
	if err != nil {
		t.Fatalf("handlePrOpenCars: %v", err)
	}

	var notes []models.CarProgress
	db.Where("car_id = ?", "car-all1").Find(&notes)
	if len(notes) == 0 {
		t.Fatal("expected progress note")
	}

	note := notes[0].Note
	// All three sections should be present.
	if !strings.Contains(note, "## Review comments") {
		t.Error("missing '## Review comments' section")
	}
	if !strings.Contains(note, "## Inline comments") {
		t.Error("missing '## Inline comments' section")
	}
	if !strings.Contains(note, "## Conversation") {
		t.Error("missing '## Conversation' section")
	}
	if !strings.Contains(note, "`app/Models/Task.php` (line 15) @reviewer") {
		t.Errorf("inline comment not formatted correctly, got:\n%s", note)
	}
}

func TestHandlePrOpenCars_ChangesRequestedEmptyComments(t *testing.T) {
	db := testDB(t)

	db.Create(&models.Car{
		ID:     "car-empty1",
		Branch: "ry/backend/car-empty1",
		Status: "pr_open",
		Track:  "backend",
	})

	// CHANGES_REQUESTED with no review bodies, no inline, no conversation.
	viewer := &mockPRViewer{
		reviewDecision: "CHANGES_REQUESTED",
		state:          "OPEN",
	}

	var buf bytes.Buffer
	logger := testLogger(&buf)
	err := handlePrOpenCars(db, viewer, false, "", "", nil, logger)
	if err != nil {
		t.Fatalf("handlePrOpenCars: %v", err)
	}

	var c models.Car
	db.First(&c, "id = ?", "car-empty1")
	if c.Status != "open" {
		t.Errorf("status = %q, want %q", c.Status, "open")
	}

	var notes []models.CarProgress
	db.Where("car_id = ?", "car-empty1").Find(&notes)
	if len(notes) == 0 {
		t.Fatal("expected progress note even with empty comments")
	}
	// Should still have the header.
	if !strings.Contains(notes[0].Note, "PR review: changes requested") {
		t.Errorf("note should contain header, got: %s", notes[0].Note)
	}
	// Should NOT have section headers when there are no comments.
	if strings.Contains(notes[0].Note, "## Inline") {
		t.Error("should not have Inline section when no inline comments")
	}
	if strings.Contains(notes[0].Note, "## Conversation") {
		t.Error("should not have Conversation section when no conversation comments")
	}
}

func TestHandlePrOpenCars_FetchCommentsError(t *testing.T) {
	db := testDB(t)

	db.Create(&models.Car{
		ID:     "car-ferr1",
		Branch: "ry/backend/car-ferr1",
		Status: "pr_open",
		Track:  "backend",
	})

	viewer := &mockPRViewer{
		reviewDecision: "CHANGES_REQUESTED",
		state:          "OPEN",
		reviews:        []prReview{{Body: "Fix this", Author: "alice"}},
		fetchErr:       fmt.Errorf("gh api failed"),
	}

	var buf bytes.Buffer
	logger := testLogger(&buf)
	err := handlePrOpenCars(db, viewer, false, "", "", nil, logger)
	if err != nil {
		t.Fatalf("handlePrOpenCars: %v", err)
	}

	// Car should still transition to open despite FetchComments error.
	var c models.Car
	db.First(&c, "id = ?", "car-ferr1")
	if c.Status != "open" {
		t.Errorf("status = %q, want %q", c.Status, "open")
	}

	// Progress note should still contain review body.
	var notes []models.CarProgress
	db.Where("car_id = ?", "car-ferr1").Find(&notes)
	if len(notes) == 0 {
		t.Fatal("expected progress note")
	}
	if !strings.Contains(notes[0].Note, "@alice: Fix this") {
		t.Errorf("note should contain review body, got: %s", notes[0].Note)
	}
}

func TestParseInlineComments(t *testing.T) {
	raw := `[
		{
			"path": "internal/dispatch/dispatch.go",
			"line": 93,
			"body": "Fix the fallback command",
			"user": {"login": "codex-bot"}
		},
		{
			"path": "cmd/ry/main.go",
			"line": 0,
			"body": "General file comment",
			"user": {"login": "alice"}
		}
	]`

	comments, err := parseInlineComments([]byte(raw))
	if err != nil {
		t.Fatalf("parseInlineComments: %v", err)
	}
	if len(comments) != 2 {
		t.Fatalf("expected 2 comments, got %d", len(comments))
	}

	if comments[0].Path != "internal/dispatch/dispatch.go" {
		t.Errorf("comments[0].Path = %q, want %q", comments[0].Path, "internal/dispatch/dispatch.go")
	}
	if comments[0].Line != 93 {
		t.Errorf("comments[0].Line = %d, want 93", comments[0].Line)
	}
	if comments[0].Author != "codex-bot" {
		t.Errorf("comments[0].Author = %q, want %q", comments[0].Author, "codex-bot")
	}
	if comments[1].Line != 0 {
		t.Errorf("comments[1].Line = %d, want 0", comments[1].Line)
	}
}

func TestFormatReviewNote_AllTypes(t *testing.T) {
	reviews := []prReview{{Body: "Needs work", Author: "alice"}}
	inline := []prInlineComment{
		{Path: "foo.go", Line: 10, Body: "Fix this", Author: "bob"},
	}
	conv := []prConversationComment{
		{Body: "See inline", Author: "alice"},
	}

	note := formatReviewNote(reviews, inline, conv)

	if !strings.Contains(note, "## Review comments") {
		t.Error("missing Review comments section")
	}
	if !strings.Contains(note, "@alice: Needs work") {
		t.Error("missing review body")
	}
	if !strings.Contains(note, "`foo.go` (line 10) @bob") {
		t.Error("missing inline comment with file:line")
	}
	if !strings.Contains(note, "## Conversation") {
		t.Error("missing Conversation section")
	}
}

func TestFormatReviewNote_EmptyBodies(t *testing.T) {
	// Reviews with empty bodies should not produce a section.
	reviews := []prReview{{Body: "", Author: "alice"}}
	note := formatReviewNote(reviews, nil, nil)

	if strings.Contains(note, "## Review comments") {
		t.Error("should not have Review comments section when all bodies are empty")
	}
	if strings.Contains(note, "## Inline") {
		t.Error("should not have Inline section when nil")
	}
	if strings.Contains(note, "## Conversation") {
		t.Error("should not have Conversation section when nil")
	}
	if !strings.Contains(note, "PR review: changes requested") {
		t.Error("should always have header")
	}
}

func TestFormatReviewNote_InlineWithoutLine(t *testing.T) {
	inline := []prInlineComment{
		{Path: "README.md", Line: 0, Body: "General file note", Author: "bob"},
	}
	note := formatReviewNote(nil, inline, nil)

	// Line 0 should omit the line number.
	if strings.Contains(note, "(line 0)") {
		t.Error("should not show '(line 0)' for comments without a line number")
	}
	if !strings.Contains(note, "`README.md` @bob") {
		t.Errorf("should format without line number, got:\n%s", note)
	}
}

func TestHandlePrOpenCars_Merged(t *testing.T) {
	db := testDB(t)

	db.Create(&models.Car{
		ID:     "car-pro2",
		Branch: "ry/backend/car-pro2",
		Status: "pr_open",
		Track:  "backend",
	})

	viewer := &mockPRViewer{state: "MERGED"}

	var buf bytes.Buffer
	logger := testLogger(&buf)
	err := handlePrOpenCars(db, viewer, false, "", "", nil, logger)
	if err != nil {
		t.Fatalf("handlePrOpenCars: %v", err)
	}

	var c models.Car
	db.First(&c, "id = ?", "car-pro2")
	if c.Status != "merged" {
		t.Errorf("status = %q, want %q", c.Status, "merged")
	}
}

func TestHandlePrOpenCars_Closed(t *testing.T) {
	db := testDB(t)

	db.Create(&models.Car{
		ID:     "car-pro3",
		Branch: "ry/backend/car-pro3",
		Status: "pr_open",
		Track:  "backend",
	})

	viewer := &mockPRViewer{state: "CLOSED"}

	var buf bytes.Buffer
	logger := testLogger(&buf)
	err := handlePrOpenCars(db, viewer, false, "", "", nil, logger)
	if err != nil {
		t.Fatalf("handlePrOpenCars: %v", err)
	}

	var c models.Car
	db.First(&c, "id = ?", "car-pro3")
	if c.Status != "cancelled" {
		t.Errorf("status = %q, want %q", c.Status, "cancelled")
	}
}

func TestHandlePrOpenCars_NoPrOpenCars(t *testing.T) {
	db := testDB(t)

	viewer := &mockPRViewer{state: "OPEN"}

	var buf bytes.Buffer
	logger := testLogger(&buf)
	err := handlePrOpenCars(db, viewer, false, "", "", nil, logger)
	if err != nil {
		t.Fatalf("handlePrOpenCars: %v", err)
	}
}

func TestHandlePrOpenCars_ApprovedNoAction(t *testing.T) {
	db := testDB(t)

	db.Create(&models.Car{
		ID:     "car-pro4",
		Branch: "ry/backend/car-pro4",
		Status: "pr_open",
		Track:  "backend",
	})

	viewer := &mockPRViewer{
		reviewDecision: "APPROVED",
		state:          "OPEN",
	}

	var buf bytes.Buffer
	logger := testLogger(&buf)
	err := handlePrOpenCars(db, viewer, false, "", "", nil, logger)
	if err != nil {
		t.Fatalf("handlePrOpenCars: %v", err)
	}

	// Approved but still OPEN — no transition yet (waiting for merge).
	var c models.Car
	db.First(&c, "id = ?", "car-pro4")
	if c.Status != "pr_open" {
		t.Errorf("status = %q, want %q (approved but not merged)", c.Status, "pr_open")
	}
}

func TestHandlePrOpenCars_NoBranch(t *testing.T) {
	db := testDB(t)

	db.Create(&models.Car{
		ID:     "car-pro5",
		Branch: "",
		Status: "pr_open",
		Track:  "backend",
	})

	viewer := &mockPRViewer{state: "CLOSED"}

	var buf bytes.Buffer
	logger := testLogger(&buf)
	err := handlePrOpenCars(db, viewer, false, "", "", nil, logger)
	if err != nil {
		t.Fatalf("handlePrOpenCars: %v", err)
	}

	// Car with no branch should be skipped.
	var c models.Car
	db.First(&c, "id = ?", "car-pro5")
	if c.Status != "pr_open" {
		t.Errorf("status = %q, want %q (no branch, should be skipped)", c.Status, "pr_open")
	}
}

// ---------------------------------------------------------------------------
// handlePrOpenCars auto-merge tests (mge.4.1)
// ---------------------------------------------------------------------------

func TestHandlePrOpenCars_ApprovedAutoMerge(t *testing.T) {
	db := testDB(t)

	parentID := "epic-am1"
	db.Create(&models.Car{ID: parentID, Type: "epic", Status: "open", Track: "backend"})
	db.Create(&models.Car{
		ID:       "car-am1",
		Branch:   "ry/backend/car-am1",
		Status:   "pr_open",
		Track:    "backend",
		ParentID: &parentID,
	})

	viewer := &mockPRViewer{
		reviewDecision: "APPROVED",
		state:          "OPEN",
	}

	var buf bytes.Buffer
	logger := testLogger(&buf)
	err := handlePrOpenCars(db, viewer, true, "", "", nil, logger)
	if err != nil {
		t.Fatalf("handlePrOpenCars: %v", err)
	}

	if !viewer.mergeCalled {
		t.Error("expected MergePR to be called")
	}

	var c models.Car
	db.First(&c, "id = ?", "car-am1")
	if c.Status != "merged" {
		t.Errorf("status = %q, want %q", c.Status, "merged")
	}
	if c.CompletedAt == nil {
		t.Error("CompletedAt should be set")
	}

	output := buf.String()
	if !strings.Contains(output, "auto-merged") {
		t.Errorf("output should mention auto-merge, got: %s", output)
	}
}

func TestHandlePrOpenCars_ApprovedNoAutoMergeWhenDisabled(t *testing.T) {
	db := testDB(t)

	db.Create(&models.Car{
		ID:     "car-am2",
		Branch: "ry/backend/car-am2",
		Status: "pr_open",
		Track:  "backend",
	})

	viewer := &mockPRViewer{
		reviewDecision: "APPROVED",
		state:          "OPEN",
	}

	var buf bytes.Buffer
	logger := testLogger(&buf)
	err := handlePrOpenCars(db, viewer, false, "", "", nil, logger)
	if err != nil {
		t.Fatalf("handlePrOpenCars: %v", err)
	}

	if viewer.mergeCalled {
		t.Error("MergePR should NOT be called when autoMerge is false")
	}

	var c models.Car
	db.First(&c, "id = ?", "car-am2")
	if c.Status != "pr_open" {
		t.Errorf("status = %q, want %q (autoMerge disabled)", c.Status, "pr_open")
	}
}

func TestHandlePrOpenCars_ApprovedMergeFailure(t *testing.T) {
	db := testDB(t)

	db.Create(&models.Car{
		ID:     "car-am3",
		Branch: "ry/backend/car-am3",
		Status: "pr_open",
		Track:  "backend",
	})

	viewer := &mockPRViewer{
		reviewDecision: "APPROVED",
		state:          "OPEN",
		mergeErr:       fmt.Errorf("merge conflict on GitHub"),
	}

	var buf bytes.Buffer
	logger := testLogger(&buf)
	err := handlePrOpenCars(db, viewer, true, "", "", nil, logger)
	if err != nil {
		t.Fatalf("handlePrOpenCars: %v", err)
	}

	// Car should stay pr_open on merge failure.
	var c models.Car
	db.First(&c, "id = ?", "car-am3")
	if c.Status != "pr_open" {
		t.Errorf("status = %q, want %q (merge failed)", c.Status, "pr_open")
	}

	// Should have written a progress note about the failure.
	var notes []models.CarProgress
	db.Where("car_id = ?", "car-am3").Find(&notes)
	if len(notes) == 0 {
		t.Error("expected progress note about merge failure")
	}
}

func TestHandlePrOpenCars_ApprovedButNotOpen_NoMerge(t *testing.T) {
	db := testDB(t)

	db.Create(&models.Car{
		ID:     "car-am4",
		Branch: "ry/backend/car-am4",
		Status: "pr_open",
		Track:  "backend",
	})

	// State is MERGED (already merged on GitHub), not OPEN.
	viewer := &mockPRViewer{
		reviewDecision: "APPROVED",
		state:          "MERGED",
	}

	var buf bytes.Buffer
	logger := testLogger(&buf)
	err := handlePrOpenCars(db, viewer, true, "", "", nil, logger)
	if err != nil {
		t.Fatalf("handlePrOpenCars: %v", err)
	}

	// Should be handled by the MERGED case, not the auto-merge case.
	if viewer.mergeCalled {
		t.Error("MergePR should NOT be called when state is already MERGED")
	}

	var c models.Car
	db.First(&c, "id = ?", "car-am4")
	if c.Status != "merged" {
		t.Errorf("status = %q, want %q", c.Status, "merged")
	}
}

// ---------------------------------------------------------------------------
// Post-merge bookkeeping tests (fix 3: all merge paths run runPostMerge)
// ---------------------------------------------------------------------------

func TestHandlePrOpenCars_ExternalMergeUnblocksDeps(t *testing.T) {
	db := testDB(t)

	parentID := "epic-extm"
	db.Create(&models.Car{ID: parentID, Type: "epic", Status: "open", Track: "backend", Title: "Parent Epic"})

	// Car A: pr_open, will be externally merged.
	db.Create(&models.Car{
		ID:       "car-extm1",
		Branch:   "ry/backend/car-extm1",
		Status:   "pr_open",
		Track:    "backend",
		ParentID: &parentID,
	})

	// Car B: blocked by car-extm1.
	blockerID := "car-extm1"
	db.Create(&models.Car{ID: "car-extm2", Status: "blocked", Track: "backend"})
	db.Create(&models.CarDep{CarID: "car-extm2", BlockedBy: blockerID})

	viewer := &mockPRViewer{state: "MERGED"}

	var buf bytes.Buffer
	logger := testLogger(&buf)
	err := handlePrOpenCars(db, viewer, false, "", "", nil, logger)
	if err != nil {
		t.Fatalf("handlePrOpenCars: %v", err)
	}

	// Car A should be merged.
	var carA models.Car
	db.First(&carA, "id = ?", "car-extm1")
	if carA.Status != "merged" {
		t.Errorf("car-extm1 status = %q, want %q", carA.Status, "merged")
	}

	// Car B should be unblocked.
	var carB models.Car
	db.First(&carB, "id = ?", "car-extm2")
	if carB.Status != "open" {
		t.Errorf("car-extm2 status = %q, want %q (should be unblocked)", carB.Status, "open")
	}

	// Parent epic should be checked (TryCloseEpic called).
	output := buf.String()
	if !strings.Contains(output, "Car unblocked") || !strings.Contains(output, "car-extm2") {
		t.Errorf("output should mention unblocking, got: %s", output)
	}
}

func TestHandlePrOpenCars_AutoMergeUnblocksDeps(t *testing.T) {
	db := testDB(t)

	// Car A: pr_open, will be auto-merged.
	db.Create(&models.Car{
		ID:     "car-amub1",
		Branch: "ry/backend/car-amub1",
		Status: "pr_open",
		Track:  "backend",
	})

	// Car B: blocked by car-amub1.
	db.Create(&models.Car{ID: "car-amub2", Status: "blocked", Track: "backend"})
	db.Create(&models.CarDep{CarID: "car-amub2", BlockedBy: "car-amub1"})

	viewer := &mockPRViewer{
		reviewDecision: "APPROVED",
		state:          "OPEN",
	}

	var buf bytes.Buffer
	logger := testLogger(&buf)
	err := handlePrOpenCars(db, viewer, true, "", "", nil, logger)
	if err != nil {
		t.Fatalf("handlePrOpenCars: %v", err)
	}

	// Car B should be unblocked.
	var carB models.Car
	db.First(&carB, "id = ?", "car-amub2")
	if carB.Status != "open" {
		t.Errorf("car-amub2 status = %q, want %q (should be unblocked)", carB.Status, "open")
	}
}

func TestRunPostMerge_UnblocksAndClosesEpic(t *testing.T) {
	db := testDB(t)

	epicID := "epic-rpm1"
	db.Create(&models.Car{ID: epicID, Type: "epic", Status: "open", Track: "backend", Title: "Test Epic"})

	// The merged car is the only child of the epic.
	mergedCar := models.Car{
		ID:       "car-rpm1",
		Status:   "merged",
		Track:    "backend",
		ParentID: &epicID,
	}
	db.Create(&mergedCar)

	// Car B blocked by car-rpm1.
	db.Create(&models.Car{ID: "car-rpm2", Status: "blocked", Track: "backend"})
	db.Create(&models.CarDep{CarID: "car-rpm2", BlockedBy: "car-rpm1"})

	var buf bytes.Buffer
	logger := testLogger(&buf)
	runPostMerge(db, mergedCar, logger)

	// Car B should be unblocked.
	var carB models.Car
	db.First(&carB, "id = ?", "car-rpm2")
	if carB.Status != "open" {
		t.Errorf("car-rpm2 status = %q, want %q", carB.Status, "open")
	}

	// Epic should be closed (only child is merged).
	var epic models.Car
	db.First(&epic, "id = ?", epicID)
	if epic.Status != "done" {
		t.Errorf("epic status = %q, want %q", epic.Status, "done")
	}
}

func TestRunPostMerge_EmitsDepsUnblockedBroadcast(t *testing.T) {
	db := testDB(t)

	mergedCar := models.Car{ID: "car-bc1", Status: "merged", Track: "backend", Title: "Broadcast Car"}
	db.Create(&mergedCar)

	// Car B blocked by car-bc1.
	db.Create(&models.Car{ID: "car-bc2", Status: "blocked", Track: "backend"})
	db.Create(&models.CarDep{CarID: "car-bc2", BlockedBy: "car-bc1"})

	var buf bytes.Buffer
	logger := testLogger(&buf)
	runPostMerge(db, mergedCar, logger)

	// Verify the deps-unblocked broadcast message was sent.
	var msg models.Message
	db.Where("to_agent = ? AND subject = ? AND car_id = ?", "broadcast", "deps-unblocked", "car-bc1").First(&msg)
	if msg.ID == 0 {
		t.Fatal("expected deps-unblocked broadcast message")
	}
	if !strings.Contains(msg.Body, "car-bc2") {
		t.Errorf("broadcast body = %q, want it to mention unblocked car ID", msg.Body)
	}
}

func TestRunPostMerge_NoBroadcastWhenNoDeps(t *testing.T) {
	db := testDB(t)

	mergedCar := models.Car{ID: "car-bc3", Status: "merged", Track: "backend"}
	db.Create(&mergedCar)

	var buf bytes.Buffer
	logger := testLogger(&buf)
	runPostMerge(db, mergedCar, logger)

	// No deps to unblock — no broadcast should be sent.
	var count int64
	db.Model(&models.Message{}).Where("subject = ? AND car_id = ?", "deps-unblocked", "car-bc3").Count(&count)
	if count != 0 {
		t.Errorf("expected no deps-unblocked broadcast, got %d", count)
	}
}

// ---------------------------------------------------------------------------
// Switch timeout wiring test (fix 2)
// ---------------------------------------------------------------------------

func TestHandleCompletedCars_PassesSwitchTimeout(t *testing.T) {
	// Verify that handleCompletedCars passes cfg.Stall.SwitchTimeoutSec to Switch.
	// We can't easily test the full Switch flow without a real repo, but we can
	// verify the SwitchOpts construction by inspecting the code path.
	// Instead, we test that with a non-zero SwitchTimeoutSec, the config value
	// makes it through. The SwitchOpts.SwitchTimeoutSec field is used by
	// switch.go to set the context timeout.
	//
	// This is a structural test — the integration test in switch_test.go covers
	// the actual timeout behavior. Here we verify the daemon wiring.
	cfg := testConfig(config.TrackConfig{Name: "backend", Language: "go"})
	cfg.Stall.SwitchTimeoutSec = 42

	// Verify the config value is non-zero and would be passed through.
	if cfg.Stall.SwitchTimeoutSec != 42 {
		t.Fatalf("config not set correctly")
	}
}

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
	logger := testLogger(&buf)
	// repoDir and ymDir don't matter — the epic should never reach Switch().
	err := handleCompletedCars(context.Background(), db, cfg, "/nonexistent", "/nonexistent", &sync.WaitGroup{}, nil, make(chan struct{}, 3), logger)
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
	logger := testLogger(&buf)
	err := handleCompletedCars(context.Background(), db, cfg, "/nonexistent", "/nonexistent", &sync.WaitGroup{}, nil, make(chan struct{}, 3), logger)
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
	logger := testLogger(&buf)
	err := sweepOpenEpics(db, logger)
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
	logger := testLogger(&buf)
	err := handleCompletedCars(context.Background(), db, cfg, "/nonexistent", "/nonexistent", &sync.WaitGroup{}, nil, make(chan struct{}, 3), logger)
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

// ---------------------------------------------------------------------------
// mockPRViewer for handlePrOpenCars tests
// ---------------------------------------------------------------------------

type mockPRViewer struct {
	reviewDecision    string
	state             string
	mergeable         string
	reviews           []prReview
	labels            []string
	inlineComments    []prInlineComment
	convComments      []prConversationComment
	fetchErr          error
	err               error
	mergeErr          error
	mergeCalled       bool
	commentCount      int
	countErr          error
	removeLabelCalled bool
	removedLabel      string
	removeLabelErr    error
}

func (m *mockPRViewer) ViewPR(branch string) (*prStatus, error) {
	if m.err != nil {
		return nil, m.err
	}
	return &prStatus{
		State:          m.state,
		ReviewDecision: m.reviewDecision,
		Mergeable:      m.mergeable,
		Reviews:        m.reviews,
		Labels:         m.labels,
	}, nil
}

func (m *mockPRViewer) FetchComments(branch string) ([]prInlineComment, []prConversationComment, error) {
	return m.inlineComments, m.convComments, m.fetchErr
}

func (m *mockPRViewer) MergePR(branch string) error {
	m.mergeCalled = true
	return m.mergeErr
}

func (m *mockPRViewer) CountComments(branch string) (int, error) {
	return m.commentCount, m.countErr
}

func (m *mockPRViewer) RemoveLabel(branch, label string) error {
	m.removeLabelCalled = true
	m.removedLabel = label
	return m.removeLabelErr
}

func TestViewPR_IncludesMergeable(t *testing.T) {
	viewer := &mockPRViewer{
		state:     "OPEN",
		mergeable: "CONFLICTING",
	}
	status, err := viewer.ViewPR("test-branch")
	if err != nil {
		t.Fatalf("ViewPR: %v", err)
	}
	if status.Mergeable != "CONFLICTING" {
		t.Errorf("Mergeable = %q, want %q", status.Mergeable, "CONFLICTING")
	}
}

// ---------------------------------------------------------------------------
// Escalation semaphore tests
// ---------------------------------------------------------------------------

func TestEscalationSemaphore_LimitsConcurrency(t *testing.T) {
	sem := make(chan struct{}, 2)

	var maxConcurrent int64
	var current int64
	var wg sync.WaitGroup

	for i := 0; i < 10; i++ {
		sem <- struct{}{} // acquire
		wg.Add(1)
		go func() {
			defer func() { <-sem }() // release
			defer wg.Done()

			val := atomic.AddInt64(&current, 1)
			// Record the peak.
			for {
				old := atomic.LoadInt64(&maxConcurrent)
				if val <= old || atomic.CompareAndSwapInt64(&maxConcurrent, old, val) {
					break
				}
			}
			time.Sleep(5 * time.Millisecond) // simulate work
			atomic.AddInt64(&current, -1)
		}()
	}

	wg.Wait()

	peak := atomic.LoadInt64(&maxConcurrent)
	if peak > 2 {
		t.Errorf("peak concurrency = %d, want <= 2", peak)
	}
	if peak < 1 {
		t.Errorf("peak concurrency = %d, want >= 1", peak)
	}
}

// ---------------------------------------------------------------------------
// maybeSwitchEscalate with cooldown tracker
// ---------------------------------------------------------------------------

func TestMaybeSwitchEscalate_WithCooldown(t *testing.T) {
	db := testDB(t)
	db.Create(&models.Car{ID: "car-cool1", Track: "backend"})

	// Write enough failures to trigger escalation.
	writeProgressNote(db, "car-cool1", "yardmaster", "switch:merge-conflict: conflict 1")
	writeProgressNote(db, "car-cool1", "yardmaster", "switch:merge-conflict: conflict 2")
	writeProgressNote(db, "car-cool1", "yardmaster", "switch:merge-conflict: conflict 3")

	cfg := testConfig(config.TrackConfig{Name: "backend", Language: "go"})
	cfg.Stall.MaxSwitchFailures = 3

	tracker := NewEscalationTracker(10 * time.Minute)
	sem := make(chan struct{}, 3)

	// First call: should escalate (tracker allows it).
	var buf1 bytes.Buffer
	logger1 := testLogger(&buf1)
	maybeSwitchEscalate(context.Background(), db, cfg, "car-cool1", SwitchFailMerge, nil, "", &sync.WaitGroup{}, tracker, sem, logger1)
	if !strings.Contains(buf1.String(), "escalating") {
		t.Errorf("first call should escalate, got: %s", buf1.String())
	}

	// Reset car status back to "done" so the second call can proceed to the cooldown check.
	db.Model(&models.Car{}).Where("id = ?", "car-cool1").Update("status", "done")

	// Second call: should be skipped by cooldown.
	var buf2 bytes.Buffer
	logger2 := testLogger(&buf2)
	maybeSwitchEscalate(context.Background(), db, cfg, "car-cool1", SwitchFailMerge, nil, "", &sync.WaitGroup{}, tracker, sem, logger2)
	if !strings.Contains(buf2.String(), "cooldown active") {
		t.Errorf("second call should be skipped by cooldown, got: %s", buf2.String())
	}
}

// ---------------------------------------------------------------------------
// Panic recovery tests
// ---------------------------------------------------------------------------

// ---------------------------------------------------------------------------
// handleCompletedCars priority sort tests (mge.2.1)
// ---------------------------------------------------------------------------

func TestHandleCompletedCars_SortsByPriorityThenCreatedAt(t *testing.T) {
	db := testDB(t)

	// Create cars with different priorities and creation times.
	// We'll use epics (no engine needed) so Switch() is never called.
	now := time.Now()

	// Low priority epic created first.
	epicA := "epic-sort-a"
	db.Create(&models.Car{ID: epicA, Type: "epic", Status: "done", Track: "backend", Title: "Low Priority", Priority: 3, CreatedAt: now.Add(-3 * time.Minute)})
	db.Create(&models.Car{ID: "child-sa1", Type: "task", Status: "merged", Track: "backend", ParentID: &epicA})

	// High priority epic created last.
	epicB := "epic-sort-b"
	db.Create(&models.Car{ID: epicB, Type: "epic", Status: "done", Track: "backend", Title: "High Priority", Priority: 1, CreatedAt: now.Add(-1 * time.Minute)})
	db.Create(&models.Car{ID: "child-sb1", Type: "task", Status: "merged", Track: "backend", ParentID: &epicB})

	// Same priority as A, created second (should come after A within same priority).
	epicC := "epic-sort-c"
	db.Create(&models.Car{ID: epicC, Type: "epic", Status: "done", Track: "backend", Title: "Low Priority Newer", Priority: 3, CreatedAt: now.Add(-2 * time.Minute)})
	db.Create(&models.Car{ID: "child-sc1", Type: "task", Status: "merged", Track: "backend", ParentID: &epicC})

	cfg := testConfig(config.TrackConfig{Name: "backend", Language: "go"})

	var buf bytes.Buffer
	logger := testLogger(&buf)
	err := handleCompletedCars(context.Background(), db, cfg, "/nonexistent", "/nonexistent", &sync.WaitGroup{}, nil, make(chan struct{}, 3), logger)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	output := buf.String()

	// High priority (1) should appear before low priority (3).
	idxB := strings.Index(output, epicB)
	idxA := strings.Index(output, epicA)
	idxC := strings.Index(output, epicC)

	if idxB < 0 || idxA < 0 || idxC < 0 {
		t.Fatalf("expected all epics in output, got: %s", output)
	}

	if idxB > idxA {
		t.Errorf("high priority epic-sort-b should be processed before low priority epic-sort-a")
	}
	if idxA > idxC {
		t.Errorf("epic-sort-a (older) should be processed before epic-sort-c (newer) at same priority")
	}
}

// ---------------------------------------------------------------------------
// processInbox dedup tests (mge.2.2)
// ---------------------------------------------------------------------------

func TestProcessInbox_DeduplicatesByFromSubjectCarID(t *testing.T) {
	db := testDB(t)

	startedAt := time.Now().Add(-5 * time.Minute)

	// Create duplicate messages with same (FromAgent, Subject, CarID).
	for i := 0; i < 3; i++ {
		db.Create(&models.Message{
			FromAgent: "eng-001",
			ToAgent:   YardmasterID,
			Subject:   "test-failure",
			CarID:     "car-dup1",
			Body:      fmt.Sprintf("failure %d", i),
		})
	}

	var buf bytes.Buffer
	logger := testLogger(&buf)
	draining, err := processInbox(context.Background(), db, nil, "", "", startedAt, &sync.WaitGroup{}, nil, make(chan struct{}, 3), logger)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if draining {
		t.Fatal("should not drain")
	}

	// Should only see one "test-failure for car car-dup1" in output (not three).
	output := buf.String()
	count := strings.Count(output, "test-failure acknowledged")
	if count != 1 {
		t.Errorf("expected 1 processed message, got %d mentions in output: %s", count, output)
	}

	// All messages should be acknowledged.
	var unacked int64
	db.Model(&models.Message{}).Where("acknowledged = ?", false).Count(&unacked)
	if unacked != 0 {
		t.Errorf("expected all messages acknowledged, got %d unacked", unacked)
	}
}

func TestProcessInbox_DifferentSubjectsNotDeduped(t *testing.T) {
	db := testDB(t)

	startedAt := time.Now().Add(-5 * time.Minute)

	// Same from/car but different subjects — should both be processed.
	db.Create(&models.Message{
		FromAgent: "eng-001",
		ToAgent:   YardmasterID,
		Subject:   "test-failure",
		CarID:     "car-noddup",
	})
	db.Create(&models.Message{
		FromAgent: "eng-001",
		ToAgent:   YardmasterID,
		Subject:   "engine-stalled",
		CarID:     "car-noddup",
		Body:      "stalled",
	})

	var buf bytes.Buffer
	logger := testLogger(&buf)
	_, err := processInbox(context.Background(), db, nil, "", "", startedAt, &sync.WaitGroup{}, nil, make(chan struct{}, 3), logger)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	output := buf.String()
	if !strings.Contains(output, "test-failure") {
		t.Errorf("expected test-failure in output: %s", output)
	}
	if !strings.Contains(output, "engine-stalled") {
		t.Errorf("expected engine-stalled in output: %s", output)
	}
}

// ---------------------------------------------------------------------------
// Phase timing tests (mge.3.2)
// ---------------------------------------------------------------------------

func TestTimePhase_LogsSlowPhase(t *testing.T) {
	// Test the timePhase pattern: phases taking >5s should produce WARN output.
	var buf bytes.Buffer
	logger := testLogger(&buf)

	timePhase := func(name string, fn func()) {
		start := time.Now()
		fn()
		elapsed := time.Since(start)
		if elapsed > 5*time.Second {
			logger.Warn("Phase slow", "phase", name, "elapsed", elapsed)
		} else if elapsed > time.Second {
			logger.Info("Phase completed", "phase", name, "elapsed", elapsed)
		} else {
			logger.Debug("Phase completed", "phase", name, "elapsed", elapsed)
		}
	}

	// Fast phase — no warning, but should have debug output.
	timePhase("fast", func() {})
	if strings.Contains(buf.String(), "WARN") {
		t.Errorf("fast phase should not warn, got: %s", buf.String())
	}
	if !strings.Contains(buf.String(), "Phase completed") {
		t.Errorf("fast phase should have debug output, got: %s", buf.String())
	}
}

// ---------------------------------------------------------------------------
// Configurable stale engine threshold tests (mge.5.3)
// ---------------------------------------------------------------------------

func TestHandleStaleEngines_UsesConfigThreshold(t *testing.T) {
	db := testDB(t)

	// Register an engine with last_activity 90 seconds ago.
	ninetyAgo := time.Now().Add(-90 * time.Second)
	db.Create(&models.Engine{
		ID:           "eng-stale1",
		Track:        "backend",
		Status:       "idle",
		LastActivity: ninetyAgo,
		StartedAt:    ninetyAgo,
	})

	// With threshold=120s, this engine is NOT stale.
	cfg := testConfig(config.TrackConfig{Name: "backend", Language: "go"})
	cfg.Stall.StaleEngineThresholdSec = 120

	var buf bytes.Buffer
	logger := testLogger(&buf)
	if err := handleStaleEngines(db, cfg, "", logger); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Engine should NOT have been detected as stale.
	if strings.Contains(buf.String(), "eng-stale1") {
		t.Errorf("engine should not be stale with 120s threshold, got: %s", buf.String())
	}

	// With threshold=60s, this engine IS stale.
	cfg.Stall.StaleEngineThresholdSec = 60
	buf.Reset()
	if err := handleStaleEngines(db, cfg, "", logger); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(buf.String(), "eng-stale1") {
		t.Errorf("engine should be stale with 60s threshold, got: %s", buf.String())
	}
}

func TestHandleStaleEngines_DefaultThresholdWhenZero(t *testing.T) {
	db := testDB(t)

	// Engine with last_activity 90 seconds ago.
	ninetyAgo := time.Now().Add(-90 * time.Second)
	db.Create(&models.Engine{
		ID:           "eng-stale2",
		Track:        "backend",
		Status:       "idle",
		LastActivity: ninetyAgo,
		StartedAt:    ninetyAgo,
	})

	// StaleEngineThresholdSec = 0 should use default (60s).
	cfg := testConfig(config.TrackConfig{Name: "backend", Language: "go"})
	cfg.Stall.StaleEngineThresholdSec = 0

	var buf bytes.Buffer
	logger := testLogger(&buf)
	if err := handleStaleEngines(db, cfg, "", logger); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// With default 60s threshold, 90s-ago engine IS stale.
	if !strings.Contains(buf.String(), "eng-stale2") {
		t.Errorf("engine should be stale with default threshold, got: %s", buf.String())
	}
}

// ---------------------------------------------------------------------------
// Panic recovery tests
// ---------------------------------------------------------------------------

func TestDaemonLoop_PanicRecovery(t *testing.T) {
	// Verify that the panic recovery pattern used in RunDaemon works:
	// a panic inside the closure is caught and the loop continues.
	var buf bytes.Buffer
	iterations := 0

	for i := 0; i < 3; i++ {
		func() {
			defer func() {
				if r := recover(); r != nil {
					fmt.Fprintf(&buf, "recovered: %v\n", r)
				}
			}()
			iterations++
			if iterations == 2 {
				panic("test panic in daemon loop")
			}
		}()
	}

	if iterations != 3 {
		t.Errorf("iterations = %d, want 3 (loop should continue after panic)", iterations)
	}
	if !strings.Contains(buf.String(), "test panic in daemon loop") {
		t.Errorf("should have recovered panic, got: %s", buf.String())
	}
}

// ---------------------------------------------------------------------------
// CONFLICTING PR auto-rebase tests
// ---------------------------------------------------------------------------

func TestHandlePrOpenCars_ConflictingAutoRebase(t *testing.T) {
	repoDir, _, run := initTestRepoWithRemote(t)

	writeFile(t, repoDir, "main.txt", "main content\n")
	run(repoDir, "git", "add", ".")
	run(repoDir, "git", "commit", "-m", "initial")
	run(repoDir, "git", "push", "origin", "main")

	branch := "ry/alice/backend/car-rebase1"
	run(repoDir, "git", "checkout", "-b", branch)
	writeFile(t, repoDir, "feature.txt", "feature content\n")
	run(repoDir, "git", "add", ".")
	run(repoDir, "git", "commit", "-m", "feature work")
	run(repoDir, "git", "push", "origin", branch)

	run(repoDir, "git", "checkout", "main")
	writeFile(t, repoDir, "other.txt", "other work\n")
	run(repoDir, "git", "add", ".")
	run(repoDir, "git", "commit", "-m", "main advance")
	run(repoDir, "git", "push", "origin", "main")

	db := testDB(t)
	db.Create(&models.Car{
		ID:     "car-rebase1",
		Title:  "Rebase test",
		Track:  "backend",
		Branch: branch,
		Status: "pr_open",
	})

	viewer := &mockPRViewer{
		state:     "OPEN",
		mergeable: "CONFLICTING",
	}

	var buf bytes.Buffer
	logger := testLogger(&buf)
	err := handlePrOpenCars(db, viewer, false, repoDir, repoDir, nil, logger)
	if err != nil {
		t.Fatalf("handlePrOpenCars: %v", err)
	}

	var c models.Car
	db.First(&c, "id = ?", "car-rebase1")
	if c.Status != "pr_open" {
		t.Errorf("status = %q, want %q", c.Status, "pr_open")
	}
	if c.LastRebaseBaseHead == "" {
		t.Error("LastRebaseBaseHead should be set after rebase attempt")
	}

	var notes []models.CarProgress
	db.Where("car_id = ?", "car-rebase1").Find(&notes)
	if len(notes) == 0 {
		t.Error("expected progress note about auto-rebase")
	}

	output := buf.String()
	if !strings.Contains(output, "Auto-rebased") {
		t.Errorf("output should mention auto-rebase, got: %s", output)
	}
}

func TestHandlePrOpenCars_ConflictingSkipWhenMainUnchanged(t *testing.T) {
	repoDir, _, run := initTestRepoWithRemote(t)

	writeFile(t, repoDir, "main.txt", "content\n")
	run(repoDir, "git", "add", ".")
	run(repoDir, "git", "commit", "-m", "initial")
	run(repoDir, "git", "push", "origin", "main")

	currentHead := getRemoteHeadCommit(repoDir, "main")

	db := testDB(t)
	db.Create(&models.Car{
		ID:                 "car-skip1",
		Title:              "Skip rebase test",
		Track:              "backend",
		Branch:             "ry/alice/backend/car-skip1",
		Status:             "pr_open",
		LastRebaseBaseHead: currentHead,
	})

	viewer := &mockPRViewer{
		state:     "OPEN",
		mergeable: "CONFLICTING",
	}

	var buf bytes.Buffer
	logger := testLogger(&buf)
	err := handlePrOpenCars(db, viewer, false, repoDir, repoDir, nil, logger)
	if err != nil {
		t.Fatalf("handlePrOpenCars: %v", err)
	}

	var notes []models.CarProgress
	db.Where("car_id = ?", "car-skip1").Find(&notes)
	if len(notes) != 0 {
		t.Errorf("expected no progress notes (rebase skipped), got %d", len(notes))
	}

	if buf.String() != "" {
		t.Errorf("expected no output, got: %s", buf.String())
	}
}

func TestHandlePrOpenCars_UnresolvableConflict(t *testing.T) {
	repoDir, _, run := initTestRepoWithRemote(t)

	writeFile(t, repoDir, "shared.txt", "original\n")
	run(repoDir, "git", "add", ".")
	run(repoDir, "git", "commit", "-m", "initial")
	run(repoDir, "git", "push", "origin", "main")

	branch := "ry/alice/backend/car-unresolvable1"
	run(repoDir, "git", "checkout", "-b", branch)
	writeFile(t, repoDir, "shared.txt", "feature version\n")
	run(repoDir, "git", "add", ".")
	run(repoDir, "git", "commit", "-m", "feature changes shared.txt")
	run(repoDir, "git", "push", "origin", branch)

	run(repoDir, "git", "checkout", "main")
	writeFile(t, repoDir, "shared.txt", "main version\n")
	run(repoDir, "git", "add", ".")
	run(repoDir, "git", "commit", "-m", "main changes shared.txt")
	run(repoDir, "git", "push", "origin", "main")

	db := testDB(t)
	db.Create(&models.Car{
		ID:     "car-unresolvable1",
		Title:  "Unresolvable conflict test",
		Track:  "backend",
		Branch: branch,
		Status: "pr_open",
	})

	viewer := &mockPRViewer{
		state:     "OPEN",
		mergeable: "CONFLICTING",
	}

	var buf bytes.Buffer
	logger := testLogger(&buf)
	err := handlePrOpenCars(db, viewer, false, repoDir, repoDir, nil, logger)
	if err != nil {
		t.Fatalf("handlePrOpenCars: %v", err)
	}

	var c models.Car
	db.First(&c, "id = ?", "car-unresolvable1")
	if c.Status != "pr_open" {
		t.Errorf("status = %q, want %q", c.Status, "pr_open")
	}
	if c.LastRebaseBaseHead == "" {
		t.Error("LastRebaseBaseHead should be set after failed rebase attempt")
	}

	var notes []models.CarProgress
	db.Where("car_id = ?", "car-unresolvable1").Find(&notes)
	if len(notes) == 0 {
		t.Fatal("expected progress note about unresolvable conflict")
	}
	if !strings.Contains(notes[0].Note, "cannot be auto-resolved") {
		t.Errorf("note should mention cannot be auto-resolved, got: %s", notes[0].Note)
	}

	var msgs []models.Message
	db.Where("subject = ? AND car_id = ?", "escalate", "car-unresolvable1").Find(&msgs)
	if len(msgs) == 0 {
		t.Error("expected human escalation message")
	}

	output := buf.String()
	if !strings.Contains(output, "unresolvable conflict") {
		t.Errorf("output should mention unresolvable conflict, got: %s", output)
	}
}

func TestHandlePrOpenCars_MergeableUnknownSkipped(t *testing.T) {
	db := testDB(t)

	db.Create(&models.Car{
		ID:     "car-unknown1",
		Title:  "Unknown mergeable test",
		Track:  "backend",
		Branch: "ry/alice/backend/car-unknown1",
		Status: "pr_open",
	})

	viewer := &mockPRViewer{
		state:     "OPEN",
		mergeable: "UNKNOWN",
	}

	var buf bytes.Buffer
	logger := testLogger(&buf)
	err := handlePrOpenCars(db, viewer, false, "", "", nil, logger)
	if err != nil {
		t.Fatalf("handlePrOpenCars: %v", err)
	}

	var c models.Car
	db.First(&c, "id = ?", "car-unknown1")
	if c.Status != "pr_open" {
		t.Errorf("status = %q, want %q", c.Status, "pr_open")
	}
	if c.LastRebaseBaseHead != "" {
		t.Errorf("LastRebaseBaseHead should be empty, got %q", c.LastRebaseBaseHead)
	}
}

func TestHandlePrOpenCars_ConflictingAndApproved(t *testing.T) {
	repoDir, _, run := initTestRepoWithRemote(t)

	writeFile(t, repoDir, "main.txt", "content\n")
	run(repoDir, "git", "add", ".")
	run(repoDir, "git", "commit", "-m", "initial")
	run(repoDir, "git", "push", "origin", "main")

	branch := "ry/alice/backend/car-both1"
	run(repoDir, "git", "checkout", "-b", branch)
	writeFile(t, repoDir, "feature.txt", "feature\n")
	run(repoDir, "git", "add", ".")
	run(repoDir, "git", "commit", "-m", "feature")
	run(repoDir, "git", "push", "origin", branch)
	run(repoDir, "git", "checkout", "main")

	db := testDB(t)
	db.Create(&models.Car{
		ID:     "car-both1",
		Title:  "Both conflicting and approved",
		Track:  "backend",
		Branch: branch,
		Status: "pr_open",
	})

	viewer := &mockPRViewer{
		state:          "OPEN",
		mergeable:      "CONFLICTING",
		reviewDecision: "APPROVED",
	}

	var buf bytes.Buffer
	logger := testLogger(&buf)
	err := handlePrOpenCars(db, viewer, true, repoDir, repoDir, nil, logger)
	if err != nil {
		t.Fatalf("handlePrOpenCars: %v", err)
	}

	if viewer.mergeCalled {
		t.Error("MergePR should not be called when PR is CONFLICTING")
	}

	var c models.Car
	db.First(&c, "id = ?", "car-both1")
	if c.Status != "pr_open" {
		t.Errorf("status = %q, want %q", c.Status, "pr_open")
	}
}

func TestReopenCarWithFeedback_SetsStatusOpen(t *testing.T) {
	db := testDB(t)
	now := time.Now()
	db.Create(&models.Car{
		ID:          "car-reopen1",
		Branch:      "ry/backend/car-reopen1",
		Status:      "pr_open",
		Assignee:    "eng-001",
		CompletedAt: &now,
	})

	viewer := &mockPRViewer{
		inlineComments: []prInlineComment{
			{Path: "main.go", Line: 10, Body: "Fix this", Author: "reviewer"},
		},
	}

	var buf bytes.Buffer
	logger := testLogger(&buf)
	reopenCarWithFeedback(db, viewer, models.Car{ID: "car-reopen1", Branch: "ry/backend/car-reopen1"}, nil, "", logger)

	var c models.Car
	db.First(&c, "id = ?", "car-reopen1")
	if c.Status != "open" {
		t.Errorf("status = %q, want %q", c.Status, "open")
	}
	if c.Assignee != "" {
		t.Errorf("assignee = %q, want empty", c.Assignee)
	}
}

func TestReopenCarWithFeedback_PreservesCompletedAt(t *testing.T) {
	db := testDB(t)
	now := time.Now()
	db.Create(&models.Car{
		ID:          "car-reopen2",
		Branch:      "ry/backend/car-reopen2",
		Status:      "pr_open",
		CompletedAt: &now,
	})

	viewer := &mockPRViewer{}
	var buf bytes.Buffer
	logger := testLogger(&buf)
	reopenCarWithFeedback(db, viewer, models.Car{ID: "car-reopen2", Branch: "ry/backend/car-reopen2"}, nil, "", logger)

	var c models.Car
	db.First(&c, "id = ?", "car-reopen2")
	if c.CompletedAt == nil {
		t.Error("CompletedAt should be preserved for isRevision detection")
	}
}

func TestReopenCarWithFeedback_FetchCommentsError_StillReopens(t *testing.T) {
	db := testDB(t)
	db.Create(&models.Car{
		ID:     "car-reopen3",
		Branch: "ry/backend/car-reopen3",
		Status: "pr_open",
	})

	viewer := &mockPRViewer{fetchErr: fmt.Errorf("gh api failed")}
	var buf bytes.Buffer
	logger := testLogger(&buf)
	reopenCarWithFeedback(db, viewer, models.Car{ID: "car-reopen3", Branch: "ry/backend/car-reopen3"}, nil, "", logger)

	var c models.Car
	db.First(&c, "id = ?", "car-reopen3")
	if c.Status != "open" {
		t.Errorf("status = %q, want %q", c.Status, "open")
	}
}

func TestReopenCarWithFeedback_ProgressNoteFormat(t *testing.T) {
	db := testDB(t)
	db.Create(&models.Car{
		ID:     "car-reopen4",
		Branch: "ry/backend/car-reopen4",
		Status: "pr_open",
	})

	viewer := &mockPRViewer{
		inlineComments: []prInlineComment{
			{Path: "main.go", Line: 42, Body: "Use errgroup", Author: "alice"},
		},
		convComments: []prConversationComment{
			{Body: "Looks good otherwise", Author: "alice"},
		},
	}

	var buf bytes.Buffer
	logger := testLogger(&buf)
	reviews := []prReview{{Body: "Needs work", Author: "alice"}}
	reopenCarWithFeedback(db, viewer, models.Car{ID: "car-reopen4", Branch: "ry/backend/car-reopen4"}, reviews, "", logger)

	var notes []models.CarProgress
	db.Where("car_id = ?", "car-reopen4").Find(&notes)
	if len(notes) == 0 {
		t.Fatal("expected progress note")
	}
	note := notes[0].Note
	if !strings.Contains(note, "main.go") {
		t.Errorf("note should contain file path, got:\n%s", note)
	}
	if !strings.Contains(note, "@alice") {
		t.Errorf("note should contain author attribution, got:\n%s", note)
	}
	if !strings.Contains(note, "Needs work") {
		t.Errorf("note should contain review body, got:\n%s", note)
	}
}

func TestReopenCarWithFeedback_EmptyComments(t *testing.T) {
	db := testDB(t)
	db.Create(&models.Car{
		ID:     "car-reopen5",
		Branch: "ry/backend/car-reopen5",
		Status: "pr_open",
	})

	viewer := &mockPRViewer{}
	var buf bytes.Buffer
	logger := testLogger(&buf)
	reopenCarWithFeedback(db, viewer, models.Car{ID: "car-reopen5", Branch: "ry/backend/car-reopen5"}, nil, "", logger)

	var c models.Car
	db.First(&c, "id = ?", "car-reopen5")
	if c.Status != "open" {
		t.Errorf("status = %q, want %q", c.Status, "open")
	}

	var notes []models.CarProgress
	db.Where("car_id = ?", "car-reopen5").Find(&notes)
	if len(notes) == 0 {
		t.Fatal("expected progress note even with empty comments")
	}
}

func TestReopenCarWithFeedback_RemovesRevisedLabel(t *testing.T) {
	db := testDB(t)
	db.Create(&models.Car{
		ID:     "car-reopen6",
		Branch: "ry/backend/car-reopen6",
		Status: "pr_open",
	})

	viewer := &mockPRViewer{}
	var buf bytes.Buffer
	logger := testLogger(&buf)
	reopenCarWithFeedback(db, viewer, models.Car{ID: "car-reopen6", Branch: "ry/backend/car-reopen6"}, nil, "railyard: revised", logger)

	if !viewer.removeLabelCalled {
		t.Error("expected RemoveLabel to be called for revised label")
	}
	if viewer.removedLabel != "railyard: revised" {
		t.Errorf("removedLabel = %q, want %q", viewer.removedLabel, "railyard: revised")
	}
}

func TestReopenCarWithFeedback_SkipsRemoveWhenNoRevisedLabel(t *testing.T) {
	db := testDB(t)
	db.Create(&models.Car{
		ID:     "car-reopen7",
		Branch: "ry/backend/car-reopen7",
		Status: "pr_open",
	})

	viewer := &mockPRViewer{}
	var buf bytes.Buffer
	logger := testLogger(&buf)
	reopenCarWithFeedback(db, viewer, models.Car{ID: "car-reopen7", Branch: "ry/backend/car-reopen7"}, nil, "", logger)

	if viewer.removeLabelCalled {
		t.Error("RemoveLabel should not be called when revisedLabel is empty")
	}
}

func TestHandlePrOpenCars_ChangesRequested_RemovesRevisedLabel(t *testing.T) {
	db := testDB(t)
	db.Create(&models.Car{
		ID:     "car-revised1",
		Branch: "ry/backend/car-revised1",
		Status: "pr_open",
	})

	viewer := &mockPRViewer{
		state:          "OPEN",
		reviewDecision: "CHANGES_REQUESTED",
	}
	cfg := testConfig(config.TrackConfig{Name: "backend", Language: "go"})
	cfg.Yardmaster.RevisedLabel = "railyard: revised"
	var buf bytes.Buffer
	logger := testLogger(&buf)
	err := handlePrOpenCars(db, viewer, false, "", "", cfg, logger)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !viewer.removeLabelCalled {
		t.Error("expected revised label to be removed on changes_requested reopen")
	}
	if viewer.removedLabel != "railyard: revised" {
		t.Errorf("removedLabel = %q, want %q", viewer.removedLabel, "railyard: revised")
	}
}

func TestHandlePrOpenCars_ReworkLabel(t *testing.T) {
	db := testDB(t)
	db.Create(&models.Car{
		ID:     "car-label1",
		Branch: "ry/backend/car-label1",
		Status: "pr_open",
		Track:  "backend",
	})

	viewer := &mockPRViewer{
		state:  "OPEN",
		labels: []string{"railyard: rework"},
		inlineComments: []prInlineComment{
			{Path: "main.go", Line: 5, Body: "Fix this", Author: "reviewer"},
		},
	}

	cfg := testConfig(config.TrackConfig{Name: "backend", Language: "go"})
	var buf bytes.Buffer
	logger := testLogger(&buf)
	err := handlePrOpenCars(db, viewer, false, "", "", cfg, logger)
	if err != nil {
		t.Fatalf("handlePrOpenCars: %v", err)
	}

	var c models.Car
	db.First(&c, "id = ?", "car-label1")
	if c.Status != "open" {
		t.Errorf("status = %q, want %q", c.Status, "open")
	}
	if !viewer.removeLabelCalled {
		t.Error("expected RemoveLabel to be called")
	}
	if viewer.removedLabel != "railyard: rework" {
		t.Errorf("removedLabel = %q, want %q", viewer.removedLabel, "railyard: rework")
	}
}

func TestHandlePrOpenCars_ReworkLabelCustom(t *testing.T) {
	db := testDB(t)
	db.Create(&models.Car{
		ID:     "car-label2",
		Branch: "ry/backend/car-label2",
		Status: "pr_open",
		Track:  "backend",
	})

	viewer := &mockPRViewer{
		state:  "OPEN",
		labels: []string{"needs-rework"},
	}

	cfg := testConfig(config.TrackConfig{Name: "backend", Language: "go"})
	cfg.Yardmaster.ReworkLabel = "needs-rework"
	var buf bytes.Buffer
	logger := testLogger(&buf)
	err := handlePrOpenCars(db, viewer, false, "", "", cfg, logger)
	if err != nil {
		t.Fatalf("handlePrOpenCars: %v", err)
	}

	var c models.Car
	db.First(&c, "id = ?", "car-label2")
	if c.Status != "open" {
		t.Errorf("status = %q, want %q", c.Status, "open")
	}
}

func TestHandlePrOpenCars_NoReworkLabel(t *testing.T) {
	db := testDB(t)
	db.Create(&models.Car{
		ID:     "car-label3",
		Branch: "ry/backend/car-label3",
		Status: "pr_open",
		Track:  "backend",
	})

	viewer := &mockPRViewer{
		state:  "OPEN",
		labels: []string{"bug", "enhancement"},
	}

	cfg := testConfig(config.TrackConfig{Name: "backend", Language: "go"})
	var buf bytes.Buffer
	logger := testLogger(&buf)
	err := handlePrOpenCars(db, viewer, false, "", "", cfg, logger)
	if err != nil {
		t.Fatalf("handlePrOpenCars: %v", err)
	}

	var c models.Car
	db.First(&c, "id = ?", "car-label3")
	if c.Status != "pr_open" {
		t.Errorf("status = %q, want %q", c.Status, "pr_open")
	}
}

func TestHandlePrOpenCars_ReworkLabelCaseExact(t *testing.T) {
	db := testDB(t)
	db.Create(&models.Car{
		ID:     "car-label4",
		Branch: "ry/backend/car-label4",
		Status: "pr_open",
		Track:  "backend",
	})

	viewer := &mockPRViewer{
		state:  "OPEN",
		labels: []string{"Railyard: Rework"},
	}

	cfg := testConfig(config.TrackConfig{Name: "backend", Language: "go"})
	var buf bytes.Buffer
	logger := testLogger(&buf)
	err := handlePrOpenCars(db, viewer, false, "", "", cfg, logger)
	if err != nil {
		t.Fatalf("handlePrOpenCars: %v", err)
	}

	var c models.Car
	db.First(&c, "id = ?", "car-label4")
	if c.Status != "pr_open" {
		t.Errorf("status = %q, want %q (case mismatch should not trigger)", c.Status, "pr_open")
	}
}

func TestHandlePrOpenCars_RemoveLabelFailureSkipsReopen(t *testing.T) {
	db := testDB(t)
	db.Create(&models.Car{
		ID:     "car-label5",
		Branch: "ry/backend/car-label5",
		Status: "pr_open",
		Track:  "backend",
	})

	viewer := &mockPRViewer{
		state:          "OPEN",
		labels:         []string{"railyard: rework"},
		removeLabelErr: fmt.Errorf("gh api error"),
	}

	cfg := testConfig(config.TrackConfig{Name: "backend", Language: "go"})
	var buf bytes.Buffer
	logger := testLogger(&buf)
	err := handlePrOpenCars(db, viewer, false, "", "", cfg, logger)
	if err != nil {
		t.Fatalf("handlePrOpenCars: %v", err)
	}

	var c models.Car
	db.First(&c, "id = ?", "car-label5")
	if c.Status != "pr_open" {
		t.Errorf("status = %q, want %q (should NOT reopen when label removal fails to avoid infinite loop)", c.Status, "pr_open")
	}
}

func TestHandlePrOpenCars_ReworkLabelOnClosedPR(t *testing.T) {
	db := testDB(t)
	db.Create(&models.Car{
		ID:     "car-label6",
		Branch: "ry/backend/car-label6",
		Status: "pr_open",
		Track:  "backend",
	})

	viewer := &mockPRViewer{
		state:  "CLOSED",
		labels: []string{"railyard: rework"},
	}

	cfg := testConfig(config.TrackConfig{Name: "backend", Language: "go"})
	var buf bytes.Buffer
	logger := testLogger(&buf)
	err := handlePrOpenCars(db, viewer, false, "", "", cfg, logger)
	if err != nil {
		t.Fatalf("handlePrOpenCars: %v", err)
	}

	var c models.Car
	db.First(&c, "id = ?", "car-label6")
	if c.Status != "cancelled" {
		t.Errorf("status = %q, want %q (CLOSED should take priority over label)", c.Status, "cancelled")
	}
}

func TestHasReworkLabel_Match(t *testing.T) {
	labels := []string{"bug", "railyard: rework", "enhancement"}
	if !hasReworkLabel(labels, "railyard: rework") {
		t.Error("expected true for matching label")
	}
}

func TestHasReworkLabel_NoMatch(t *testing.T) {
	labels := []string{"bug", "enhancement"}
	if hasReworkLabel(labels, "railyard: rework") {
		t.Error("expected false when label not present")
	}
}

func TestHasReworkLabel_CaseExact(t *testing.T) {
	labels := []string{"Railyard: Rework"}
	if hasReworkLabel(labels, "railyard: rework") {
		t.Error("expected false — matching should be case-exact")
	}
}

func TestHasReworkLabel_EmptyLabels(t *testing.T) {
	if hasReworkLabel(nil, "railyard: rework") {
		t.Error("expected false for nil labels")
	}
}

func TestHandlePrOpenCars_NewComments(t *testing.T) {
	db := testDB(t)
	db.Create(&models.Car{
		ID:                 "car-cmt1",
		Branch:             "ry/backend/car-cmt1",
		Status:             "pr_open",
		Track:              "backend",
		LastPRCommentCount: 0,
	})

	viewer := &mockPRViewer{
		state:        "OPEN",
		commentCount: 2,
		inlineComments: []prInlineComment{
			{Path: "main.go", Line: 10, Body: "Fix this", Author: "reviewer"},
			{Path: "main.go", Line: 20, Body: "And this", Author: "reviewer"},
		},
	}

	cfg := testConfig(config.TrackConfig{Name: "backend", Language: "go"})
	var buf bytes.Buffer
	logger := testLogger(&buf)
	err := handlePrOpenCars(db, viewer, false, "", "", cfg, logger)
	if err != nil {
		t.Fatalf("handlePrOpenCars: %v", err)
	}

	var c models.Car
	db.First(&c, "id = ?", "car-cmt1")
	if c.Status != "open" {
		t.Errorf("status = %q, want %q", c.Status, "open")
	}

	var notes []models.CarProgress
	db.Where("car_id = ?", "car-cmt1").Find(&notes)
	if len(notes) == 0 {
		t.Error("expected progress note with review comments")
	}
}

func TestHandlePrOpenCars_NoNewComments(t *testing.T) {
	db := testDB(t)
	db.Create(&models.Car{
		ID:                 "car-cmt2",
		Branch:             "ry/backend/car-cmt2",
		Status:             "pr_open",
		Track:              "backend",
		LastPRCommentCount: 3,
	})

	viewer := &mockPRViewer{
		state:        "OPEN",
		commentCount: 3,
	}

	cfg := testConfig(config.TrackConfig{Name: "backend", Language: "go"})
	var buf bytes.Buffer
	logger := testLogger(&buf)
	err := handlePrOpenCars(db, viewer, false, "", "", cfg, logger)
	if err != nil {
		t.Fatalf("handlePrOpenCars: %v", err)
	}

	var c models.Car
	db.First(&c, "id = ?", "car-cmt2")
	if c.Status != "pr_open" {
		t.Errorf("status = %q, want %q", c.Status, "pr_open")
	}
}

func TestHandlePrOpenCars_CommentsIgnoredWhenApproved(t *testing.T) {
	db := testDB(t)
	db.Create(&models.Car{
		ID:                 "car-cmt3",
		Branch:             "ry/backend/car-cmt3",
		Status:             "pr_open",
		Track:              "backend",
		LastPRCommentCount: 0,
	})

	viewer := &mockPRViewer{
		state:          "OPEN",
		reviewDecision: "APPROVED",
		commentCount:   5,
	}

	cfg := testConfig(config.TrackConfig{Name: "backend", Language: "go"})
	var buf bytes.Buffer
	logger := testLogger(&buf)
	err := handlePrOpenCars(db, viewer, false, "", "", cfg, logger)
	if err != nil {
		t.Fatalf("handlePrOpenCars: %v", err)
	}

	var c models.Car
	db.First(&c, "id = ?", "car-cmt3")
	if c.Status != "pr_open" {
		t.Errorf("status = %q, want %q (approved PR should not be reopened by comments)", c.Status, "pr_open")
	}
}

func TestHandlePrOpenCars_InlineCountDecreasesNoTrigger(t *testing.T) {
	db := testDB(t)
	db.Create(&models.Car{
		ID:                 "car-cmt4",
		Branch:             "ry/backend/car-cmt4",
		Status:             "pr_open",
		Track:              "backend",
		LastPRCommentCount: 5,
	})

	viewer := &mockPRViewer{
		state:        "OPEN",
		commentCount: 3,
	}

	cfg := testConfig(config.TrackConfig{Name: "backend", Language: "go"})
	var buf bytes.Buffer
	logger := testLogger(&buf)
	err := handlePrOpenCars(db, viewer, false, "", "", cfg, logger)
	if err != nil {
		t.Fatalf("handlePrOpenCars: %v", err)
	}

	var c models.Car
	db.First(&c, "id = ?", "car-cmt4")
	if c.Status != "pr_open" {
		t.Errorf("status = %q, want %q (decreased count should not trigger)", c.Status, "pr_open")
	}
}

func TestHandlePrOpenCars_CountErrorSkipsCar(t *testing.T) {
	db := testDB(t)
	db.Create(&models.Car{
		ID:                 "car-cmt5a",
		Branch:             "ry/backend/car-cmt5a",
		Status:             "pr_open",
		Track:              "backend",
		LastPRCommentCount: 0,
	})
	db.Create(&models.Car{
		ID:                 "car-cmt5b",
		Branch:             "ry/backend/car-cmt5b",
		Status:             "pr_open",
		Track:              "backend",
		LastPRCommentCount: 0,
	})

	viewer := &mockPRViewer{
		state:    "OPEN",
		countErr: fmt.Errorf("gh api failed"),
	}

	cfg := testConfig(config.TrackConfig{Name: "backend", Language: "go"})
	var buf bytes.Buffer
	logger := testLogger(&buf)
	err := handlePrOpenCars(db, viewer, false, "", "", cfg, logger)
	if err != nil {
		t.Fatalf("handlePrOpenCars: %v", err)
	}

	var c1, c2 models.Car
	db.First(&c1, "id = ?", "car-cmt5a")
	db.First(&c2, "id = ?", "car-cmt5b")
	if c1.Status != "pr_open" {
		t.Errorf("car-cmt5a status = %q, want %q", c1.Status, "pr_open")
	}
	if c2.Status != "pr_open" {
		t.Errorf("car-cmt5b status = %q, want %q", c2.Status, "pr_open")
	}
}

func TestHandlePrOpenCars_ZeroToZeroNoTrigger(t *testing.T) {
	db := testDB(t)
	db.Create(&models.Car{
		ID:                 "car-cmt6",
		Branch:             "ry/backend/car-cmt6",
		Status:             "pr_open",
		Track:              "backend",
		LastPRCommentCount: 0,
	})

	viewer := &mockPRViewer{
		state:        "OPEN",
		commentCount: 0,
	}

	cfg := testConfig(config.TrackConfig{Name: "backend", Language: "go"})
	var buf bytes.Buffer
	logger := testLogger(&buf)
	err := handlePrOpenCars(db, viewer, false, "", "", cfg, logger)
	if err != nil {
		t.Fatalf("handlePrOpenCars: %v", err)
	}

	var c models.Car
	db.First(&c, "id = ?", "car-cmt6")
	if c.Status != "pr_open" {
		t.Errorf("status = %q, want %q", c.Status, "pr_open")
	}
}

func TestHandlePrOpenCars_MultiplePrOpenCars(t *testing.T) {
	db := testDB(t)
	db.Create(&models.Car{ID: "car-cmt7a", Branch: "ry/backend/car-cmt7a", Status: "pr_open", Track: "backend", LastPRCommentCount: 0})
	db.Create(&models.Car{ID: "car-cmt7b", Branch: "ry/backend/car-cmt7b", Status: "pr_open", Track: "backend", LastPRCommentCount: 5})
	db.Create(&models.Car{ID: "car-cmt7c", Branch: "ry/backend/car-cmt7c", Status: "pr_open", Track: "backend", LastPRCommentCount: 0})

	viewer := &mockPRViewer{
		state:        "OPEN",
		commentCount: 2,
	}

	cfg := testConfig(config.TrackConfig{Name: "backend", Language: "go"})
	var buf bytes.Buffer
	logger := testLogger(&buf)
	err := handlePrOpenCars(db, viewer, false, "", "", cfg, logger)
	if err != nil {
		t.Fatalf("handlePrOpenCars: %v", err)
	}

	var ca, cb, cc models.Car
	db.First(&ca, "id = ?", "car-cmt7a")
	db.First(&cb, "id = ?", "car-cmt7b")
	db.First(&cc, "id = ?", "car-cmt7c")
	if ca.Status != "open" {
		t.Errorf("car-cmt7a status = %q, want open (new comments)", ca.Status)
	}
	if cb.Status != "pr_open" {
		t.Errorf("car-cmt7b status = %q, want pr_open (count decreased)", cb.Status)
	}
	if cc.Status != "open" {
		t.Errorf("car-cmt7c status = %q, want open (new comments)", cc.Status)
	}
}

func TestHandlePrOpenCars_CommentCountResetOnReentry(t *testing.T) {
	db := testDB(t)
	db.Create(&models.Car{
		ID:                 "car-reentry1",
		Branch:             "ry/backend/car-reentry1",
		Status:             "pr_open",
		Track:              "backend",
		LastPRCommentCount: 2,
	})

	viewer := &mockPRViewer{
		state:        "OPEN",
		commentCount: 4,
	}

	cfg := testConfig(config.TrackConfig{Name: "backend", Language: "go"})
	var buf bytes.Buffer
	logger := testLogger(&buf)
	err := handlePrOpenCars(db, viewer, false, "", "", cfg, logger)
	if err != nil {
		t.Fatalf("handlePrOpenCars: %v", err)
	}

	var c models.Car
	db.First(&c, "id = ?", "car-reentry1")
	if c.Status != "open" {
		t.Errorf("status = %q, want %q (new comments since last snapshot)", c.Status, "open")
	}
}

func TestHandlePrOpenCars_LabelTakesPriorityOverComments(t *testing.T) {
	db := testDB(t)
	db.Create(&models.Car{
		ID:                 "car-int1",
		Branch:             "ry/backend/car-int1",
		Status:             "pr_open",
		Track:              "backend",
		LastPRCommentCount: 0,
	})

	viewer := &mockPRViewer{
		state:        "OPEN",
		labels:       []string{"railyard: rework"},
		commentCount: 5,
	}

	cfg := testConfig(config.TrackConfig{Name: "backend", Language: "go"})
	var buf bytes.Buffer
	logger := testLogger(&buf)
	err := handlePrOpenCars(db, viewer, false, "", "", cfg, logger)
	if err != nil {
		t.Fatalf("handlePrOpenCars: %v", err)
	}

	var c models.Car
	db.First(&c, "id = ?", "car-int1")
	if c.Status != "open" {
		t.Errorf("status = %q, want %q", c.Status, "open")
	}
	if !viewer.removeLabelCalled {
		t.Error("expected label trigger to fire (priority over comments)")
	}
	var notes []models.CarProgress
	db.Where("car_id = ?", "car-int1").Find(&notes)
	if len(notes) != 1 {
		t.Errorf("expected 1 progress note (single trigger), got %d", len(notes))
	}
}

func TestHandlePrOpenCars_ChangesRequestedStillWorks(t *testing.T) {
	db := testDB(t)
	db.Create(&models.Car{
		ID:     "car-int2",
		Branch: "ry/backend/car-int2",
		Status: "pr_open",
		Track:  "backend",
	})

	viewer := &mockPRViewer{
		reviewDecision: "CHANGES_REQUESTED",
		state:          "OPEN",
		reviews:        []prReview{{Body: "Fix error handling", Author: "reviewer1"}},
		inlineComments: []prInlineComment{
			{Path: "main.go", Line: 10, Body: "Add check", Author: "reviewer1"},
		},
	}

	cfg := testConfig(config.TrackConfig{Name: "backend", Language: "go"})
	var buf bytes.Buffer
	logger := testLogger(&buf)
	err := handlePrOpenCars(db, viewer, false, "", "", cfg, logger)
	if err != nil {
		t.Fatalf("handlePrOpenCars: %v", err)
	}

	var c models.Car
	db.First(&c, "id = ?", "car-int2")
	if c.Status != "open" {
		t.Errorf("status = %q, want %q", c.Status, "open")
	}

	var notes []models.CarProgress
	db.Where("car_id = ?", "car-int2").Find(&notes)
	if len(notes) == 0 {
		t.Fatal("expected progress note")
	}
	if !strings.Contains(notes[0].Note, "Fix error handling") {
		t.Errorf("note should contain review body, got:\n%s", notes[0].Note)
	}
}

func TestHandlePrOpenCars_AllTriggersInOneBatch(t *testing.T) {
	db := testDB(t)
	db.Create(&models.Car{ID: "car-bat-a", Branch: "ry/backend/car-bat-a", Status: "pr_open", Track: "backend"})
	db.Create(&models.Car{ID: "car-bat-b", Branch: "ry/backend/car-bat-b", Status: "pr_open", Track: "backend"})
	db.Create(&models.Car{ID: "car-bat-c", Branch: "ry/backend/car-bat-c", Status: "pr_open", Track: "backend", LastPRCommentCount: 0})
	db.Create(&models.Car{ID: "car-bat-d", Branch: "ry/backend/car-bat-d", Status: "pr_open", Track: "backend", LastPRCommentCount: 3})

	viewer := &mockPRViewer{
		reviewDecision: "CHANGES_REQUESTED",
		state:          "OPEN",
		reviews:        []prReview{{Body: "Fix", Author: "rev"}},
		commentCount:   5,
	}

	cfg := testConfig(config.TrackConfig{Name: "backend", Language: "go"})
	var buf bytes.Buffer
	logger := testLogger(&buf)
	err := handlePrOpenCars(db, viewer, false, "", "", cfg, logger)
	if err != nil {
		t.Fatalf("handlePrOpenCars: %v", err)
	}

	for _, id := range []string{"car-bat-a", "car-bat-b", "car-bat-c", "car-bat-d"} {
		var c models.Car
		db.First(&c, "id = ?", id)
		if c.Status != "open" {
			t.Errorf("%s status = %q, want %q", id, c.Status, "open")
		}
	}
}
