package yardmaster

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zulandar/railyard/internal/models"
)

// --- Switch validation tests ---

func TestSwitch_NilDB(t *testing.T) {
	_, err := Switch(nil, "car-001", SwitchOpts{RepoDir: "/tmp"})
	if err == nil {
		t.Fatal("expected error for nil db")
	}
	if !strings.Contains(err.Error(), "db is required") {
		t.Errorf("error = %q", err)
	}
}

func TestSwitch_EmptyCarID(t *testing.T) {
	_, err := Switch(nil, "", SwitchOpts{RepoDir: "/tmp"})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestSwitch_EmptyRepoDir(t *testing.T) {
	_, err := Switch(nil, "car-001", SwitchOpts{})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestSwitchOpts_ZeroValue(t *testing.T) {
	opts := SwitchOpts{}
	if opts.RepoDir != "" || opts.DryRun || opts.RequirePR {
		t.Error("zero-value SwitchOpts should have empty fields")
	}
}

func TestSwitchResult_ZeroValue(t *testing.T) {
	r := SwitchResult{}
	if r.CarID != "" || r.Branch != "" || r.TestsPassed || r.Merged || r.AlreadyMerged || r.PRCreated || r.PRUrl != "" || r.FailureCategory != SwitchFailNone {
		t.Error("zero-value SwitchResult should have empty/false fields")
	}
}

// --- classifyTestFailure tests ---

func TestClassifyTestFailure_ExitCode127(t *testing.T) {
	// Simulate exit code 127 (command not found).
	cmd := exec.Command("sh", "-c", "exit 127")
	err := cmd.Run()
	cat := classifyTestFailure(err, "")
	if cat != SwitchFailInfra {
		t.Errorf("exit 127: got %q, want %q", cat, SwitchFailInfra)
	}
}

func TestClassifyTestFailure_ExitCode126(t *testing.T) {
	cmd := exec.Command("sh", "-c", "exit 126")
	err := cmd.Run()
	cat := classifyTestFailure(err, "")
	if cat != SwitchFailInfra {
		t.Errorf("exit 126: got %q, want %q", cat, SwitchFailInfra)
	}
}

func TestClassifyTestFailure_InfraPatterns(t *testing.T) {
	tests := []struct {
		name   string
		output string
	}{
		{"command not found", "sh: vitest: command not found"},
		{"permission denied", "sh: ./run_tests.sh: Permission denied"},
		{"docker daemon", "Cannot connect to the Docker daemon"},
		{"connection refused", "connect ECONNREFUSED 127.0.0.1:5432"},
		{"no config", "Error: No configuration file provided"},
		{"not installed", "Error: jest is not installed"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := fmt.Errorf("tests failed: exit status 1")
			cat := classifyTestFailure(err, tt.output)
			if cat != SwitchFailInfra {
				t.Errorf("output %q: got %q, want %q", tt.output, cat, SwitchFailInfra)
			}
		})
	}
}

func TestClassifyTestFailure_CodeTestFailure(t *testing.T) {
	err := fmt.Errorf("tests failed: exit status 1")
	output := "--- FAIL: TestSomething (0.00s)\n    expected 42 but got 0\nFAIL\texit status 1"
	cat := classifyTestFailure(err, output)
	if cat != SwitchFailTest {
		t.Errorf("code test failure: got %q, want %q", cat, SwitchFailTest)
	}
}

func TestClassifyTestFailure_ExitCode128(t *testing.T) {
	// Exit code 128 is a git fatal error (e.g. branch already checked out).
	cmd := exec.Command("sh", "-c", "exit 128")
	err := cmd.Run()
	cat := classifyTestFailure(err, "")
	if cat != SwitchFailInfra {
		t.Errorf("exit 128: got %q, want %q", cat, SwitchFailInfra)
	}
}

func TestClassifyTestFailure_AlreadyCheckedOut(t *testing.T) {
	err := fmt.Errorf("tests failed: exit status 1")
	output := "fatal: 'ry/alice/backend/car-001' is already checked out at '/repo/.railyard/engines/eng-001'"
	cat := classifyTestFailure(err, output)
	if cat != SwitchFailInfra {
		t.Errorf("already checked out: got %q, want %q", cat, SwitchFailInfra)
	}
}

func TestTruncateOutput_Short(t *testing.T) {
	out := truncateOutput("hello", 100)
	if out != "hello" {
		t.Errorf("got %q, want %q", out, "hello")
	}
}

func TestTruncateOutput_Long(t *testing.T) {
	out := truncateOutput("abcdefghij", 5)
	if !strings.HasPrefix(out, "abcde") || !strings.Contains(out, "truncated") {
		t.Errorf("got %q, want truncated output", out)
	}
}

func TestSwitchFailInfra_Constant(t *testing.T) {
	if SwitchFailInfra != "infra-failed" {
		t.Errorf("SwitchFailInfra = %q, want %q", SwitchFailInfra, "infra-failed")
	}
}

// --- UnblockDeps validation tests ---

func TestUnblockDeps_NilDB(t *testing.T) {
	_, err := UnblockDeps(nil, "car-001")
	if err == nil {
		t.Fatal("expected error for nil db")
	}
	if !strings.Contains(err.Error(), "db is required") {
		t.Errorf("error = %q", err)
	}
}

func TestUnblockDeps_EmptyCarID(t *testing.T) {
	_, err := UnblockDeps(nil, "")
	if err == nil {
		t.Fatal("expected error")
	}
}

// --- gitPush tests ---

func TestGitPush_NoRemote(t *testing.T) {
	// gitPush should return an error when there's no remote configured.
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

	err := gitPush(repoDir, "main")
	if err == nil {
		t.Fatal("expected error when no remote is configured")
	}
	if !strings.Contains(err.Error(), "git push") {
		t.Errorf("error = %q, want to contain %q", err.Error(), "git push")
	}
}

func TestGitPush_WithRemote(t *testing.T) {
	// Set up a bare remote and a local clone, then verify push works.
	bareDir := t.TempDir()
	run := func(dir string, args ...string) {
		t.Helper()
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = dir
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("%v failed: %s: %v", args, out, err)
		}
	}

	// Create bare remote repo.
	run(bareDir, "git", "init", "--bare", "-b", "main")

	// Clone it to get a local repo with origin set up.
	parentDir := t.TempDir()
	run(parentDir, "git", "clone", bareDir, "repo")
	repoDir := filepath.Join(parentDir, "repo")
	run(repoDir, "git", "config", "user.email", "test@test.com")
	run(repoDir, "git", "config", "user.name", "test")
	run(repoDir, "git", "commit", "--allow-empty", "-m", "init")
	run(repoDir, "git", "push", "origin", "main")

	// Make a local commit.
	run(repoDir, "git", "commit", "--allow-empty", "-m", "feature work")

	// Push using our helper.
	if err := gitPush(repoDir, "main"); err != nil {
		t.Fatalf("gitPush failed: %v", err)
	}

	// Verify the remote has the new commit.
	cmd := exec.Command("git", "log", "--oneline", "main")
	cmd.Dir = bareDir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git log on bare failed: %s: %v", out, err)
	}
	if !strings.Contains(string(out), "feature work") {
		t.Errorf("remote missing pushed commit, got: %s", out)
	}
}

// --- detachEngineWorktree tests ---

func TestDetachEngineWorktree_NoWorktreeDir(t *testing.T) {
	// Should not panic when the worktree directory doesn't exist.
	detachEngineWorktree("/nonexistent/repo", "eng-999")
}

func TestDetachEngineWorktree_DetachesCheckedOutBranch(t *testing.T) {
	// Set up a real git repo with a worktree to test the detach.
	repoDir := t.TempDir()

	// Initialize main repo with a commit.
	run := func(dir string, args ...string) {
		t.Helper()
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = dir
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("%v failed: %s: %v", args, out, err)
		}
	}

	run(repoDir, "git", "init", "-b", "main")
	run(repoDir, "git", "config", "user.email", "test@test.com")
	run(repoDir, "git", "config", "user.name", "test")
	run(repoDir, "git", "commit", "--allow-empty", "-m", "init")

	// Create a feature branch.
	run(repoDir, "git", "checkout", "-b", "ry/alice/backend/car-001")
	run(repoDir, "git", "commit", "--allow-empty", "-m", "feature work")
	run(repoDir, "git", "checkout", "main")

	// Create worktree for the engine on the feature branch.
	engineID := "eng-001"
	wtDir := filepath.Join(repoDir, ".railyard", "engines", engineID)
	os.MkdirAll(filepath.Dir(wtDir), 0o755)
	run(repoDir, "git", "worktree", "add", wtDir, "ry/alice/backend/car-001")

	// Verify the branch is checked out in the worktree.
	cmd := exec.Command("git", "symbolic-ref", "--short", "HEAD")
	cmd.Dir = wtDir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("symbolic-ref failed: %s: %v", out, err)
	}
	if !strings.Contains(string(out), "ry/alice/backend/car-001") {
		t.Fatalf("worktree not on expected branch, got %q", string(out))
	}

	// Detach the worktree.
	detachEngineWorktree(repoDir, engineID)

	// Now we should be able to checkout the branch in the main repo.
	cmd = exec.Command("git", "checkout", "ry/alice/backend/car-001")
	cmd.Dir = repoDir
	out, err = cmd.CombinedOutput()
	if err != nil {
		t.Errorf("checkout after detach failed: %s: %v", out, err)
	}

	// Clean up: checkout main and remove worktree.
	run(repoDir, "git", "checkout", "main")
	exec.Command("git", "worktree", "remove", "--force", wtDir).Run()
}

func TestDetachEngineWorktree_AlreadyDetached(t *testing.T) {
	// Should not error when HEAD is already detached.
	repoDir := t.TempDir()

	run := func(dir string, args ...string) {
		t.Helper()
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = dir
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("%v failed: %s: %v", args, out, err)
		}
	}

	run(repoDir, "git", "init", "-b", "main")
	run(repoDir, "git", "config", "user.email", "test@test.com")
	run(repoDir, "git", "config", "user.name", "test")
	run(repoDir, "git", "commit", "--allow-empty", "-m", "init")
	run(repoDir, "git", "checkout", "-b", "feature")
	run(repoDir, "git", "commit", "--allow-empty", "-m", "work")
	run(repoDir, "git", "checkout", "main")

	engineID := "eng-002"
	wtDir := filepath.Join(repoDir, ".railyard", "engines", engineID)
	os.MkdirAll(filepath.Dir(wtDir), 0o755)
	run(repoDir, "git", "worktree", "add", wtDir, "feature")

	// Detach once.
	detachEngineWorktree(repoDir, engineID)
	// Detach again — should not panic or error.
	detachEngineWorktree(repoDir, engineID)

	exec.Command("git", "worktree", "remove", "--force", wtDir).Run()
}

// --- buildPRBody tests ---

func TestBuildPRBody_FullCar(t *testing.T) {
	db := testDB(t)
	c := models.Car{
		ID:          "car-pr1",
		Title:       "Add JWT middleware",
		Description: "Add JWT middleware for protected routes.",
		Track:       "backend",
		Branch:      "ry/alice/backend/car-pr1",
		Priority:    1,
		Assignee:    "eng-abc",
		Acceptance:  "- Rejects expired tokens\n- Valid tokens populate context",
		DesignNotes: "Uses existing auth package.",
	}
	db.Create(&c)

	db.Create(&models.CarProgress{
		CarID:    "car-pr1",
		EngineID: "eng-abc",
		Note:     "Created middleware with token extraction",
	})
	db.Create(&models.CarProgress{
		CarID:    "car-pr1",
		EngineID: "eng-abc",
		Note:     "Added test coverage for all token states",
	})

	body := buildPRBody(db, &c, "/nonexistent", "main") // repoDir doesn't matter — git diff will fail gracefully

	// Summary section.
	if !strings.Contains(body, "## Summary") {
		t.Error("missing Summary section")
	}
	if !strings.Contains(body, "Add JWT middleware for protected routes.") {
		t.Error("missing car description in summary")
	}

	// Acceptance.
	if !strings.Contains(body, "## Acceptance Criteria") {
		t.Error("missing Acceptance Criteria section")
	}
	if !strings.Contains(body, "Rejects expired tokens") {
		t.Error("missing acceptance content")
	}

	// Design Notes.
	if !strings.Contains(body, "## Design Notes") {
		t.Error("missing Design Notes section")
	}
	if !strings.Contains(body, "Uses existing auth package.") {
		t.Error("missing design notes content")
	}

	// Progress.
	if !strings.Contains(body, "## Progress") {
		t.Error("missing Progress section")
	}
	if !strings.Contains(body, "[eng-abc] Created middleware") {
		t.Error("missing progress note")
	}
	if !strings.Contains(body, "[eng-abc] Added test coverage") {
		t.Error("missing second progress note")
	}

	// Metadata footer.
	if !strings.Contains(body, "Car: car-pr1") {
		t.Error("missing car ID in metadata")
	}
	if !strings.Contains(body, "Track: backend") {
		t.Error("missing track in metadata")
	}
	if !strings.Contains(body, "Engine: eng-abc") {
		t.Error("missing engine in metadata")
	}
	if !strings.Contains(body, "Branch: ry/alice/backend/car-pr1") {
		t.Error("missing branch in metadata")
	}
}

func TestBuildPRBody_MinimalCar(t *testing.T) {
	db := testDB(t)
	c := models.Car{
		ID:       "car-pr2",
		Title:    "Fix typo",
		Track:    "docs",
		Branch:   "ry/alice/docs/car-pr2",
		Priority: 3,
	}
	db.Create(&c)

	body := buildPRBody(db, &c, "/nonexistent", "main")

	// Should use title as summary when description is empty.
	if !strings.Contains(body, "Fix typo") {
		t.Error("missing title in summary")
	}

	// Should NOT have acceptance or design sections.
	if strings.Contains(body, "## Acceptance Criteria") {
		t.Error("should not have acceptance section for empty acceptance")
	}
	if strings.Contains(body, "## Design Notes") {
		t.Error("should not have design section for empty design notes")
	}

	// Should NOT have progress section.
	if strings.Contains(body, "## Progress") {
		t.Error("should not have progress section with no notes")
	}

	// Should have metadata.
	if !strings.Contains(body, "Car: car-pr2") {
		t.Error("missing car ID in metadata")
	}
	if !strings.Contains(body, "Priority: P3") {
		t.Error("missing priority in metadata")
	}

	// No engine assigned.
	if strings.Contains(body, "Engine:") {
		t.Error("should not have engine when no assignee")
	}
}

func TestBuildPRBody_NilDB(t *testing.T) {
	c := models.Car{
		ID:     "car-pr3",
		Title:  "Something",
		Track:  "backend",
		Branch: "ry/alice/backend/car-pr3",
	}

	// Should not panic with nil DB — just no progress section.
	body := buildPRBody(nil, &c, "/nonexistent", "main")
	if !strings.Contains(body, "## Summary") {
		t.Error("missing Summary section")
	}
	if strings.Contains(body, "## Progress") {
		t.Error("should not have progress with nil db")
	}
}

// --- TryCloseEpic validation tests ---

func TestTryCloseEpic_NilDB(t *testing.T) {
	// Should not panic with nil DB.
	TryCloseEpic(nil, "epic-001")
}

func TestTryCloseEpic_EmptyID(t *testing.T) {
	// Should not panic with empty ID.
	TryCloseEpic(nil, "")
}

func TestTryCloseEpic_ClosesWhenAllChildrenMerged(t *testing.T) {
	db := testDB(t)

	epicID := "epic-close1"
	db.Create(&models.Car{ID: epicID, Type: "epic", Status: "open", Track: "backend"})
	db.Create(&models.Car{ID: "child-1", Type: "task", Status: "merged", Track: "backend", ParentID: &epicID})
	db.Create(&models.Car{ID: "child-2", Type: "task", Status: "merged", Track: "backend", ParentID: &epicID})

	TryCloseEpic(db, epicID)

	var epic models.Car
	db.First(&epic, "id = ?", epicID)
	if epic.Status != "done" {
		t.Errorf("epic status = %q, want %q", epic.Status, "done")
	}
}

func TestTryCloseEpic_DoesNotCloseWithPendingChildren(t *testing.T) {
	db := testDB(t)

	epicID := "epic-open1"
	db.Create(&models.Car{ID: epicID, Type: "epic", Status: "open", Track: "backend"})
	db.Create(&models.Car{ID: "child-3", Type: "task", Status: "merged", Track: "backend", ParentID: &epicID})
	db.Create(&models.Car{ID: "child-4", Type: "task", Status: "open", Track: "backend", ParentID: &epicID})

	TryCloseEpic(db, epicID)

	var epic models.Car
	db.First(&epic, "id = ?", epicID)
	if epic.Status != "open" {
		t.Errorf("epic status = %q, want %q (child still open)", epic.Status, "open")
	}
}

func TestTryCloseEpic_ClosesBlockedEpicWhenAllChildrenDone(t *testing.T) {
	// Simulates the bug scenario: epic was blocked, gets unblocked,
	// but all children are already merged. TryCloseEpic should close it
	// regardless of the epic's own status.
	db := testDB(t)

	epicID := "epic-blocked1"
	db.Create(&models.Car{ID: epicID, Type: "epic", Status: "open", Track: "backend"})
	db.Create(&models.Car{ID: "child-5", Type: "task", Status: "done", Track: "backend", ParentID: &epicID})
	db.Create(&models.Car{ID: "child-6", Type: "task", Status: "merged", Track: "frontend", ParentID: &epicID})
	db.Create(&models.Car{ID: "child-7", Type: "task", Status: "cancelled", Track: "backend", ParentID: &epicID})

	TryCloseEpic(db, epicID)

	var epic models.Car
	db.First(&epic, "id = ?", epicID)
	if epic.Status != "done" {
		t.Errorf("epic status = %q, want %q", epic.Status, "done")
	}
}

// --- SwitchFailureCategory tests ---

func TestSwitchFailureCategory_Values(t *testing.T) {
	tests := []struct {
		cat  SwitchFailureCategory
		want string
	}{
		{SwitchFailNone, ""},
		{SwitchFailFetch, "fetch-failed"},
		{SwitchFailPreTest, "pre-test-failed"},
		{SwitchFailTest, "test-failed"},
		{SwitchFailMerge, "merge-conflict"},
		{SwitchFailPush, "push-failed"},
		{SwitchFailPR, "pr-failed"},
	}

	for _, tt := range tests {
		if string(tt.cat) != tt.want {
			t.Errorf("category %q != %q", tt.cat, tt.want)
		}
	}
}

func TestSwitch_FetchFailureSetsCategory(t *testing.T) {
	// Switch with a valid DB and car but a nonexistent repoDir triggers
	// a git fetch failure, which should set FailureCategory.
	db := testDB(t)
	db.Create(&models.Car{
		ID:     "car-fc1",
		Title:  "Test",
		Track:  "backend",
		Branch: "ry/alice/backend/car-fc1",
		Status: "done",
	})

	result, err := Switch(db, "car-fc1", SwitchOpts{
		RepoDir:     "/nonexistent/repo/path",
		TestCommand: "true",
	})
	if err == nil {
		t.Fatal("expected error from fetch failure")
	}
	if result.FailureCategory != SwitchFailFetch {
		t.Errorf("FailureCategory = %q, want %q", result.FailureCategory, SwitchFailFetch)
	}
}

// --- isAncestor tests ---

// initTestRepo creates a git repo with an initial commit on main and returns
// its path. The run helper executes git commands in that repo.
func initTestRepo(t *testing.T) (string, func(args ...string)) {
	t.Helper()
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
	return repoDir, run
}

func TestIsAncestor_TrueWhenBranchBehindMain(t *testing.T) {
	repoDir, run := initTestRepo(t)

	// Create a feature branch at the current commit.
	run("git", "checkout", "-b", "feature")
	run("git", "checkout", "main")

	// Advance main past the feature branch.
	run("git", "commit", "--allow-empty", "-m", "main advance")

	if !isAncestor(repoDir, "feature", "main") {
		t.Error("isAncestor should be true when feature is behind main")
	}
}

func TestIsAncestor_FalseWhenBranchAhead(t *testing.T) {
	repoDir, run := initTestRepo(t)

	// Create a feature branch with a new commit.
	run("git", "checkout", "-b", "feature")
	run("git", "commit", "--allow-empty", "-m", "feature work")
	run("git", "checkout", "main")

	if isAncestor(repoDir, "feature", "main") {
		t.Error("isAncestor should be false when feature has commits not in main")
	}
}

func TestIsAncestor_TrueWhenSameCommit(t *testing.T) {
	repoDir, run := initTestRepo(t)

	// Branch at same point as main, no divergence.
	run("git", "checkout", "-b", "feature")
	run("git", "checkout", "main")

	if !isAncestor(repoDir, "feature", "main") {
		t.Error("isAncestor should be true when feature and main are at the same commit")
	}
}

// --- Switch ancestor skip integration test ---

func TestSwitch_SkipsMergeWhenAlreadyAncestor(t *testing.T) {
	// Set up: a parent car whose branch is already merged into main
	// (simulating a child car that included parent's commits merging first).
	repoDir, run := initTestRepo(t)

	// Create a feature branch with a commit.
	run("git", "checkout", "-b", "ry/alice/backend/parent-001")
	run("git", "commit", "--allow-empty", "-m", "parent feature work")
	run("git", "checkout", "main")

	// Merge the branch into main (simulating the child's merge including it).
	run("git", "merge", "--no-ff", "ry/alice/backend/parent-001", "-m", "merge child (includes parent)")

	// Set up DB with the car.
	db := testDB(t)
	db.Create(&models.Car{
		ID:     "parent-001",
		Title:  "Parent feature",
		Track:  "backend",
		Branch: "ry/alice/backend/parent-001",
		Status: "done",
	})

	result, err := Switch(db, "parent-001", SwitchOpts{
		RepoDir:     repoDir,
		TestCommand: "true", // always passes
	})
	if err != nil {
		t.Fatalf("Switch returned error: %v", err)
	}

	if !result.Merged {
		t.Error("expected Merged=true")
	}
	if !result.AlreadyMerged {
		t.Error("expected AlreadyMerged=true")
	}
	if !result.TestsPassed {
		t.Error("expected TestsPassed=true")
	}

	// Verify the car was marked as merged in the DB.
	var car models.Car
	db.First(&car, "id = ?", "parent-001")
	if car.Status != "merged" {
		t.Errorf("car status = %q, want %q", car.Status, "merged")
	}
	if car.CompletedAt == nil {
		t.Error("car completed_at should be set")
	}
}

// --- Switch full merge integration tests (with remote) ---

// initTestRepoWithRemote creates a git repo with a bare remote and returns
// the local repo dir, bare remote dir, and a run helper.
func initTestRepoWithRemote(t *testing.T) (repoDir, bareDir string, run func(dir string, args ...string)) {
	t.Helper()
	bareDir = t.TempDir()
	run = func(dir string, args ...string) {
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

	// Clone to get local repo with origin.
	parentDir := t.TempDir()
	run(parentDir, "git", "clone", bareDir, "repo")
	repoDir = filepath.Join(parentDir, "repo")
	run(repoDir, "git", "config", "user.email", "test@test.com")
	run(repoDir, "git", "config", "user.name", "test")
	run(repoDir, "git", "commit", "--allow-empty", "-m", "init")
	run(repoDir, "git", "push", "origin", "main")
	return repoDir, bareDir, run
}

func TestSwitch_FullMerge_PushesToRemote(t *testing.T) {
	repoDir, bareDir, run := initTestRepoWithRemote(t)

	// Create a feature branch with a commit.
	run(repoDir, "git", "checkout", "-b", "ry/alice/backend/car-fm1")
	run(repoDir, "git", "commit", "--allow-empty", "-m", "feature work")
	run(repoDir, "git", "checkout", "main")

	// Set up DB with the car.
	db := testDB(t)
	db.Create(&models.Car{
		ID:     "car-fm1",
		Title:  "Full merge test",
		Track:  "backend",
		Branch: "ry/alice/backend/car-fm1",
		Status: "done",
	})

	result, err := Switch(db, "car-fm1", SwitchOpts{
		RepoDir:     repoDir,
		TestCommand: "true", // always passes
	})
	if err != nil {
		t.Fatalf("Switch returned error: %v", err)
	}

	if !result.Merged {
		t.Error("expected Merged=true")
	}
	if result.AlreadyMerged {
		t.Error("expected AlreadyMerged=false for direct merge")
	}
	if !result.TestsPassed {
		t.Error("expected TestsPassed=true")
	}

	// Verify the car is marked as merged in DB.
	var car models.Car
	db.First(&car, "id = ?", "car-fm1")
	if car.Status != "merged" {
		t.Errorf("car status = %q, want %q", car.Status, "merged")
	}

	// Verify the remote has the merge commit.
	cmd := exec.Command("git", "log", "--oneline", "main")
	cmd.Dir = bareDir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git log on bare failed: %s: %v", out, err)
	}
	if !strings.Contains(string(out), "Switch: merge") {
		t.Errorf("remote missing merge commit, got: %s", out)
	}
}

func TestSwitch_MergeRevertsOnPushFailure(t *testing.T) {
	// Create a repo with NO remote — push will fail.
	repoDir, run := initTestRepo(t)

	// Create a feature branch with a commit.
	run("git", "checkout", "-b", "ry/alice/backend/car-pf1")
	run("git", "commit", "--allow-empty", "-m", "feature work")
	run("git", "checkout", "main")

	// Record pre-merge HEAD.
	cmd := exec.Command("git", "rev-parse", "HEAD")
	cmd.Dir = repoDir
	preHead, _ := cmd.Output()
	preMergeHead := strings.TrimSpace(string(preHead))

	// Set up DB with the car.
	db := testDB(t)
	db.Create(&models.Car{
		ID:     "car-pf1",
		Title:  "Push failure test",
		Track:  "backend",
		Branch: "ry/alice/backend/car-pf1",
		Status: "done",
	})

	result, err := Switch(db, "car-pf1", SwitchOpts{
		RepoDir:     repoDir,
		TestCommand: "true",
	})

	// Should return error with push-failed category.
	if err == nil {
		t.Fatal("expected error when push fails")
	}
	if result.FailureCategory != SwitchFailPush {
		t.Errorf("FailureCategory = %q, want %q", result.FailureCategory, SwitchFailPush)
	}
	if result.Merged {
		t.Error("Merged should be false when push fails")
	}

	// Verify car was NOT marked as merged.
	var car models.Car
	db.First(&car, "id = ?", "car-pf1")
	if car.Status == "merged" {
		t.Error("car should NOT be marked merged when push fails")
	}

	// Verify local main was reset (merge undone).
	cmd = exec.Command("git", "rev-parse", "HEAD")
	cmd.Dir = repoDir
	postHead, _ := cmd.Output()
	postMergeHead := strings.TrimSpace(string(postHead))

	if postMergeHead != preMergeHead {
		t.Errorf("local main should be reset to pre-merge HEAD\npre:  %s\npost: %s", preMergeHead, postMergeHead)
	}
}

func TestGitResetToCommit(t *testing.T) {
	repoDir, run := initTestRepo(t)

	// Record initial HEAD.
	cmd := exec.Command("git", "rev-parse", "HEAD")
	cmd.Dir = repoDir
	initialHead, _ := cmd.Output()
	initial := strings.TrimSpace(string(initialHead))

	// Add a commit.
	run("git", "commit", "--allow-empty", "-m", "extra commit")

	// Verify we've moved forward.
	cmd = exec.Command("git", "rev-parse", "HEAD")
	cmd.Dir = repoDir
	newHead, _ := cmd.Output()
	if strings.TrimSpace(string(newHead)) == initial {
		t.Fatal("HEAD should have advanced")
	}

	// Reset to initial.
	gitResetToCommit(repoDir, initial)

	// Verify we're back.
	cmd = exec.Command("git", "rev-parse", "HEAD")
	cmd.Dir = repoDir
	afterReset, _ := cmd.Output()
	if strings.TrimSpace(string(afterReset)) != initial {
		t.Errorf("after reset: HEAD = %q, want %q", strings.TrimSpace(string(afterReset)), initial)
	}
}

// --- runTests pre-test and no-test-files tests ---

func TestRunTests_PreTestCommand(t *testing.T) {
	repoDir, run := initTestRepo(t)

	run("git", "checkout", "-b", "feature")
	run("git", "checkout", "main")

	// Pre-test creates a marker file; test command checks it exists.
	markerPath := filepath.Join(repoDir, "marker.txt")
	preTest := "echo pre-test-ran > " + markerPath
	testCmd := "test -f " + markerPath

	output, err := runTests(repoDir, "feature", "main", preTest, testCmd)
	if err != nil {
		t.Fatalf("runTests failed: %v\noutput: %s", err, output)
	}

	// Verify we're back on main.
	cmd := exec.Command("git", "symbolic-ref", "--short", "HEAD")
	cmd.Dir = repoDir
	out, _ := cmd.Output()
	if strings.TrimSpace(string(out)) != "main" {
		t.Errorf("not on main after runTests, on %q", strings.TrimSpace(string(out)))
	}
}

func TestRunTests_PreTestFailure(t *testing.T) {
	repoDir, run := initTestRepo(t)

	run("git", "checkout", "-b", "feature")
	run("git", "checkout", "main")

	// Pre-test fails; test command should never run.
	_, err := runTests(repoDir, "feature", "main", "false", "echo should-not-run")
	if err == nil {
		t.Fatal("expected error when pre-test fails")
	}
	if !strings.Contains(err.Error(), "pre-test command failed") {
		t.Errorf("error = %q, want to contain %q", err.Error(), "pre-test command failed")
	}

	// Verify we're back on main despite the failure.
	cmd := exec.Command("git", "symbolic-ref", "--short", "HEAD")
	cmd.Dir = repoDir
	out, _ := cmd.Output()
	if strings.TrimSpace(string(out)) != "main" {
		t.Errorf("not on main after pre-test failure, on %q", strings.TrimSpace(string(out)))
	}
}

func TestRunTests_NoTestFilesPattern(t *testing.T) {
	repoDir, run := initTestRepo(t)

	run("git", "checkout", "-b", "feature")
	run("git", "checkout", "main")

	// Simulate "no test files" by echoing the pattern and exiting non-zero.
	testCmd := `echo "no test files" && exit 1`

	output, err := runTests(repoDir, "feature", "main", "", testCmd)
	if err != nil {
		t.Fatalf("runTests should treat 'no test files' as pass, got error: %v", err)
	}
	if !strings.Contains(output, "no test files") {
		t.Errorf("output should contain the pattern, got: %s", output)
	}
}

func TestRunTests_NoTestsFoundPattern(t *testing.T) {
	repoDir, run := initTestRepo(t)

	run("git", "checkout", "-b", "feature")
	run("git", "checkout", "main")

	testCmd := `echo "No tests found" && exit 1`

	output, err := runTests(repoDir, "feature", "main", "", testCmd)
	if err != nil {
		t.Fatalf("runTests should treat 'No tests found' as pass, got error: %v", err)
	}
	if !strings.Contains(output, "No tests found") {
		t.Errorf("output should contain the pattern, got: %s", output)
	}
}

// --- PrimaryRepoDir and worktree isolation tests ---

func TestSwitch_PrimaryRepoDirUsedForDetach(t *testing.T) {
	// Verify that when PrimaryRepoDir is set, detachEngineWorktree uses it
	// (not RepoDir) to find engine worktrees.
	opts := SwitchOpts{
		RepoDir:        "/some/worktree/path",
		PrimaryRepoDir: "/primary/repo/path",
	}
	if opts.PrimaryRepoDir != "/primary/repo/path" {
		t.Errorf("PrimaryRepoDir = %q, want %q", opts.PrimaryRepoDir, "/primary/repo/path")
	}
	// When PrimaryRepoDir is empty, fallback to RepoDir.
	opts2 := SwitchOpts{RepoDir: "/some/path"}
	detachDir := opts2.PrimaryRepoDir
	if detachDir == "" {
		detachDir = opts2.RepoDir
	}
	if detachDir != "/some/path" {
		t.Errorf("fallback detachDir = %q, want %q", detachDir, "/some/path")
	}
}

func TestSwitch_InWorktree_MergePushes(t *testing.T) {
	// Integration test: create a yardmaster worktree and run Switch in it
	// while the primary repo stays on main (untouched).
	repoDir, bareDir, run := initTestRepoWithRemote(t)

	// Create a feature branch with a commit.
	run(repoDir, "git", "checkout", "-b", "ry/alice/backend/car-wt1")
	run(repoDir, "git", "commit", "--allow-empty", "-m", "worktree feature")
	run(repoDir, "git", "checkout", "main")

	// Create a yardmaster worktree (simulating what RunDaemon does).
	ymDir := filepath.Join(repoDir, ".railyard", "yardmaster")
	os.MkdirAll(filepath.Join(repoDir, ".railyard"), 0o755)
	run(repoDir, "git", "worktree", "add", "--detach", ymDir)

	// Reset worktree to main (simulating SyncWorktreeToBranch).
	run(ymDir, "git", "reset", "--hard", "main")

	// Set up DB with the car.
	db := testDB(t)
	db.Create(&models.Car{
		ID:     "car-wt1",
		Title:  "Worktree merge test",
		Track:  "backend",
		Branch: "ry/alice/backend/car-wt1",
		Status: "done",
	})

	result, err := Switch(db, "car-wt1", SwitchOpts{
		RepoDir:        ymDir,
		PrimaryRepoDir: repoDir,
		TestCommand:    "true",
	})
	if err != nil {
		t.Fatalf("Switch in worktree returned error: %v", err)
	}

	if !result.Merged {
		t.Error("expected Merged=true")
	}
	if !result.TestsPassed {
		t.Error("expected TestsPassed=true")
	}

	// Verify the remote has the merge commit.
	cmd := exec.Command("git", "log", "--oneline", "main")
	cmd.Dir = bareDir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git log on bare failed: %s: %v", out, err)
	}
	if !strings.Contains(string(out), "Switch: merge") {
		t.Errorf("remote missing merge commit, got: %s", out)
	}

	// Verify the primary repo's HEAD is still on main (untouched).
	cmd = exec.Command("git", "symbolic-ref", "--short", "HEAD")
	cmd.Dir = repoDir
	out, err = cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("symbolic-ref failed: %s: %v", out, err)
	}
	if strings.TrimSpace(string(out)) != "main" {
		t.Errorf("primary repo HEAD = %q, want %q (should be untouched)", strings.TrimSpace(string(out)), "main")
	}

	// Clean up worktree.
	exec.Command("git", "worktree", "remove", "--force", ymDir).Run()
}

func TestRunTests_RealTestFailure(t *testing.T) {
	repoDir, run := initTestRepo(t)

	run("git", "checkout", "-b", "feature")
	run("git", "checkout", "main")

	// A real failure that doesn't match any no-test patterns.
	testCmd := `echo "FAIL: TestSomething" && exit 1`

	_, err := runTests(repoDir, "feature", "main", "", testCmd)
	if err == nil {
		t.Fatal("expected error for real test failure")
	}
	if !strings.Contains(err.Error(), "tests failed") {
		t.Errorf("error = %q, want to contain %q", err.Error(), "tests failed")
	}
}

func TestRunTests_BranchCheckedOutInOtherWorktree(t *testing.T) {
	// When the branch is checked out in another worktree, runTests should
	// still work by using a detached HEAD checkout fallback.
	repoDir, run := initTestRepo(t)

	// Create a feature branch.
	run("git", "checkout", "-b", "feature-wt")
	run("git", "commit", "--allow-empty", "-m", "feature work")
	run("git", "checkout", "main")

	// Create a second worktree that has the branch checked out.
	wtDir := filepath.Join(t.TempDir(), "other-wt")
	run("git", "worktree", "add", wtDir, "feature-wt")

	// runTests should handle this gracefully — the branch is locked by another worktree.
	output, err := runTests(repoDir, "feature-wt", "main", "", "true")
	if err != nil {
		t.Fatalf("runTests should handle worktree collision, got: %v\noutput: %s", err, output)
	}

	// Clean up worktree.
	exec.Command("git", "-C", repoDir, "worktree", "remove", "--force", wtDir).Run()
}

// writeFile is a test helper that writes content to a file in the repo.
func writeFile(t *testing.T, repoDir, name, content string) {
	t.Helper()
	path := filepath.Join(repoDir, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestGitMergeAbort(t *testing.T) {
	repoDir, run := initTestRepo(t)

	// Create a branch that will conflict with main.
	run("git", "checkout", "-b", "feature")
	writeFile(t, repoDir, "file.txt", "feature content\n")
	run("git", "add", "file.txt")
	run("git", "commit", "-m", "feature adds file")
	run("git", "checkout", "main")
	writeFile(t, repoDir, "file.txt", "main content\n")
	run("git", "add", "file.txt")
	run("git", "commit", "-m", "main adds file")

	// Start a merge that will conflict.
	cmd := exec.Command("git", "merge", "feature")
	cmd.Dir = repoDir
	cmd.CombinedOutput() // ignore error — we want the conflict

	// Abort should clean up.
	gitMergeAbort(repoDir)

	// Verify clean state after abort.
	statusCmd := exec.Command("git", "status", "--porcelain")
	statusCmd.Dir = repoDir
	out, _ := statusCmd.Output()
	if strings.TrimSpace(string(out)) != "" {
		t.Errorf("expected clean state after abort, got: %s", out)
	}
}

func TestGetConflictFiles(t *testing.T) {
	repoDir, run := initTestRepo(t)

	// Create conflicting branches.
	run("git", "checkout", "-b", "feature")
	writeFile(t, repoDir, "a.txt", "feature-a\n")
	writeFile(t, repoDir, "b.txt", "feature-b\n")
	run("git", "add", ".")
	run("git", "commit", "-m", "feature adds files")
	run("git", "checkout", "main")
	writeFile(t, repoDir, "a.txt", "main-a\n")
	writeFile(t, repoDir, "b.txt", "main-b\n")
	run("git", "add", ".")
	run("git", "commit", "-m", "main adds files")

	// Start conflicting merge.
	cmd := exec.Command("git", "merge", "feature")
	cmd.Dir = repoDir
	cmd.CombinedOutput()

	files := getConflictFiles(repoDir)
	if len(files) != 2 {
		t.Fatalf("expected 2 conflict files, got %d: %v", len(files), files)
	}

	// Clean up.
	gitMergeAbort(repoDir)
}

func TestGetConflictContext(t *testing.T) {
	repoDir, run := initTestRepo(t)

	run("git", "checkout", "-b", "feature")
	writeFile(t, repoDir, "file.txt", "feature content\n")
	run("git", "add", "file.txt")
	run("git", "commit", "-m", "feature")
	run("git", "checkout", "main")
	writeFile(t, repoDir, "file.txt", "main content\n")
	run("git", "add", "file.txt")
	run("git", "commit", "-m", "main")

	cmd := exec.Command("git", "merge", "feature")
	cmd.Dir = repoDir
	cmd.CombinedOutput()

	ctx := getConflictContext(repoDir, []string{"file.txt"})
	if ctx == "" {
		t.Error("expected non-empty conflict context")
	}
	if !strings.Contains(ctx, "file.txt") {
		t.Error("conflict context should mention the file name")
	}

	gitMergeAbort(repoDir)
}

func TestTryResolveConflict_CleanRebase(t *testing.T) {
	repoDir, run := initTestRepo(t)

	// Create base file on main.
	writeFile(t, repoDir, "shared.txt", "line1\nline2\nline3\n")
	run("git", "add", ".")
	run("git", "commit", "-m", "base file")

	// Branch and modify end of file (append).
	run("git", "checkout", "-b", "feature")
	writeFile(t, repoDir, "shared.txt", "line1\nline2\nline3\nfeature-line4\n")
	run("git", "add", ".")
	run("git", "commit", "-m", "feature appends line")

	// Advance main with different non-overlapping change (prepend).
	run("git", "checkout", "main")
	writeFile(t, repoDir, "shared.txt", "main-line0\nline1\nline2\nline3\n")
	run("git", "add", ".")
	run("git", "commit", "-m", "main prepends line")

	// Start a merge that conflicts (both modified shared.txt from same base).
	mergeCmd := exec.Command("git", "merge", "--no-ff", "feature", "-m", "test merge")
	mergeCmd.Dir = repoDir
	mergeCmd.CombinedOutput() // will conflict

	// Try to resolve via rebase.
	resolved, err := tryResolveConflict(repoDir, "feature", "main")
	if err != nil {
		t.Fatalf("tryResolveConflict error: %v", err)
	}
	if !resolved {
		t.Error("expected resolved=true for cleanly rebaseable conflict")
	}

	// Verify we can merge cleanly now.
	mergeCmd = exec.Command("git", "merge", "--no-ff", "feature", "-m", "test merge after rebase")
	mergeCmd.Dir = repoDir
	out, err := mergeCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("merge after rebase should succeed: %s: %v", out, err)
	}
}

func TestTryResolveConflict_Unresolvable(t *testing.T) {
	repoDir, run := initTestRepo(t)

	// Create base file.
	writeFile(t, repoDir, "config.go", "package config\n\nvar Name = \"base\"\n")
	run("git", "add", ".")
	run("git", "commit", "-m", "base config")

	// Branch and replace content.
	run("git", "checkout", "-b", "feature")
	writeFile(t, repoDir, "config.go", "package config\n\nvar Name = \"feature\"\n")
	run("git", "add", ".")
	run("git", "commit", "-m", "feature changes Name")

	// Main also replaces content (true conflict — same line).
	run("git", "checkout", "main")
	writeFile(t, repoDir, "config.go", "package config\n\nvar Name = \"main\"\n")
	run("git", "add", ".")
	run("git", "commit", "-m", "main changes Name")

	// Start failing merge.
	mergeCmd := exec.Command("git", "merge", "--no-ff", "feature", "-m", "test merge")
	mergeCmd.Dir = repoDir
	mergeCmd.CombinedOutput()

	resolved, err := tryResolveConflict(repoDir, "feature", "main")
	if resolved {
		t.Error("expected resolved=false for true same-line conflict")
	}
	if err == nil {
		t.Error("expected error with conflict context")
	}
	if err != nil && !strings.Contains(err.Error(), "config.go") {
		t.Errorf("error should mention conflicting file, got: %v", err)
	}

	// Verify repo is in a clean state (rebase was aborted).
	statusCmd := exec.Command("git", "status", "--porcelain")
	statusCmd.Dir = repoDir
	out, _ := statusCmd.Output()
	status := strings.TrimSpace(string(out))
	if status != "" {
		t.Errorf("expected clean state after abort, got: %s", status)
	}
}

func TestIsOnlyGoModConflict(t *testing.T) {
	tests := []struct {
		files []string
		want  bool
	}{
		{[]string{"go.mod"}, true},
		{[]string{"go.sum"}, true},
		{[]string{"go.mod", "go.sum"}, true},
		{[]string{"go.mod", "main.go"}, false},
		{[]string{"config.go"}, false},
		{nil, false},
		{[]string{}, false},
	}
	for _, tt := range tests {
		got := isOnlyGoModConflict(tt.files)
		if got != tt.want {
			t.Errorf("isOnlyGoModConflict(%v) = %v, want %v", tt.files, got, tt.want)
		}
	}
}

func TestSwitch_MergeConflict_RebaseResolves(t *testing.T) {
	repoDir, bareDir, run := initTestRepoWithRemote(t)

	// Create base file on main.
	writeFile(t, repoDir, "shared.txt", "line1\nline2\nline3\n")
	run(repoDir, "git", "add", ".")
	run(repoDir, "git", "commit", "-m", "base file")
	run(repoDir, "git", "push", "origin", "main")

	// Create feature branch that appends a line.
	run(repoDir, "git", "checkout", "-b", "ry/alice/backend/car-cr1")
	writeFile(t, repoDir, "shared.txt", "line1\nline2\nline3\nfeature-line4\n")
	run(repoDir, "git", "add", ".")
	run(repoDir, "git", "commit", "-m", "feature appends line")

	// Advance main with a non-overlapping change (prepend a line).
	run(repoDir, "git", "checkout", "main")
	writeFile(t, repoDir, "shared.txt", "main-line0\nline1\nline2\nline3\n")
	run(repoDir, "git", "add", ".")
	run(repoDir, "git", "commit", "-m", "main prepends line")
	run(repoDir, "git", "push", "origin", "main")

	db := testDB(t)
	db.Create(&models.Car{
		ID:     "car-cr1",
		Title:  "Conflict resolution test",
		Track:  "backend",
		Branch: "ry/alice/backend/car-cr1",
		Status: "done",
	})

	result, err := Switch(db, "car-cr1", SwitchOpts{
		RepoDir:     repoDir,
		TestCommand: "true",
	})
	if err != nil {
		t.Fatalf("Switch returned error: %v (conflict details: %s)", err, result.ConflictDetails)
	}
	if !result.Merged {
		t.Error("expected Merged=true after rebase-resolved conflict")
	}

	// Verify the remote has the merge commit.
	cmd := exec.Command("git", "log", "--oneline", "main")
	cmd.Dir = bareDir
	out, _ := cmd.CombinedOutput()
	if !strings.Contains(string(out), "Switch: merge") {
		t.Errorf("remote missing merge commit, got: %s", out)
	}
}

func TestSwitch_MergeConflict_UnresolvableEscalates(t *testing.T) {
	repoDir, run := initTestRepo(t)

	// Create base file.
	writeFile(t, repoDir, "config.go", "package config\n\nvar Name = \"base\"\n")
	run("git", "add", ".")
	run("git", "commit", "-m", "base config")

	// Branch replaces content.
	run("git", "checkout", "-b", "ry/alice/backend/car-ue1")
	writeFile(t, repoDir, "config.go", "package config\n\nvar Name = \"feature\"\n")
	run("git", "add", ".")
	run("git", "commit", "-m", "feature changes Name")

	// Main also replaces content (true same-line conflict).
	run("git", "checkout", "main")
	writeFile(t, repoDir, "config.go", "package config\n\nvar Name = \"main\"\n")
	run("git", "add", ".")
	run("git", "commit", "-m", "main changes Name")

	db := testDB(t)
	db.Create(&models.Car{
		ID:     "car-ue1",
		Title:  "Unresolvable conflict test",
		Track:  "backend",
		Branch: "ry/alice/backend/car-ue1",
		Status: "done",
	})

	result, err := Switch(db, "car-ue1", SwitchOpts{
		RepoDir:     repoDir,
		TestCommand: "true",
	})

	// Should fail with merge-conflict category.
	if err == nil {
		t.Fatal("expected error for unresolvable conflict")
	}
	if result.FailureCategory != SwitchFailMerge {
		t.Errorf("FailureCategory = %q, want %q", result.FailureCategory, SwitchFailMerge)
	}

	// ConflictDetails should contain the conflicting file name.
	if result.ConflictDetails == "" {
		t.Error("expected ConflictDetails to be populated")
	}
	if !strings.Contains(result.ConflictDetails, "config.go") {
		t.Errorf("ConflictDetails should mention config.go, got: %s", result.ConflictDetails)
	}

	// Verify the repo is in a clean state (no leftover rebase/merge state).
	statusCmd := exec.Command("git", "status", "--porcelain")
	statusCmd.Dir = repoDir
	out, _ := statusCmd.Output()
	if strings.TrimSpace(string(out)) != "" {
		t.Errorf("expected clean repo state, got: %s", out)
	}
}

func TestResolveGoModConflict(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go not in PATH")
	}

	repoDir, run := initTestRepo(t)

	// Create a base Go module.
	writeFile(t, repoDir, "go.mod", "module example.com/test\n\ngo 1.21\n")
	writeFile(t, repoDir, "main.go", "package main\n\nfunc main() {}\n")
	run("git", "add", ".")
	run("git", "commit", "-m", "base module")

	// Feature branch adds a dependency (simulated by adding a require line).
	run("git", "checkout", "-b", "feature")
	writeFile(t, repoDir, "go.mod", "module example.com/test\n\ngo 1.21\n\nrequire golang.org/x/text v0.14.0\n")
	writeFile(t, repoDir, "go.sum", "")
	run("git", "add", ".")
	run("git", "commit", "-m", "feature adds dependency")

	// Main also modifies go.mod (adds a different require).
	run("git", "checkout", "main")
	writeFile(t, repoDir, "go.mod", "module example.com/test\n\ngo 1.21\n\nrequire golang.org/x/sys v0.17.0\n")
	writeFile(t, repoDir, "go.sum", "")
	run("git", "add", ".")
	run("git", "commit", "-m", "main adds different dependency")

	// Start rebase — this should conflict on go.mod.
	checkout := exec.Command("git", "checkout", "feature")
	checkout.Dir = repoDir
	checkout.CombinedOutput()

	rebase := exec.Command("git", "rebase", "main")
	rebase.Dir = repoDir
	rebase.CombinedOutput() // will conflict

	files := getConflictFiles(repoDir)
	if !isOnlyGoModConflict(files) {
		t.Fatalf("expected only go.mod conflict, got: %v", files)
	}

	err := resolveGoModConflict(repoDir)
	if err != nil {
		t.Fatalf("resolveGoModConflict failed: %v", err)
	}

	// Verify rebase completed (not in rebase state).
	statusCmd := exec.Command("git", "status")
	statusCmd.Dir = repoDir
	out, _ := statusCmd.Output()
	if strings.Contains(string(out), "rebase in progress") {
		t.Error("expected rebase to be complete after go.mod resolution")
	}
}

// ---------------------------------------------------------------------------
// UnblockDeps error handling tests
// ---------------------------------------------------------------------------

func TestUnblockDeps_ReturnsErrorOnCountFailure(t *testing.T) {
	// Create a DB with cars and deps, then drop the cars table to make
	// the JOIN in the Count() query fail. UnblockDeps should return an error
	// instead of silently defaulting otherBlockers to 0 and unblocking.
	db := testDB(t)

	// Car A depends on both Car B and Car C.
	db.Create(&models.Car{ID: "car-a", Status: "blocked", Track: "backend"})
	db.Create(&models.Car{ID: "car-b", Status: "merged", Track: "backend"})
	db.Create(&models.Car{ID: "car-c", Status: "open", Track: "backend"}) // NOT resolved
	db.Create(&models.CarDep{CarID: "car-a", BlockedBy: "car-b"})
	db.Create(&models.CarDep{CarID: "car-a", BlockedBy: "car-c"})

	// Drop the cars table to make the JOIN query fail.
	db.Exec("DROP TABLE cars")

	_, err := UnblockDeps(db, "car-b")
	if err == nil {
		t.Fatal("expected error when Count query fails due to missing table")
	}
}

func TestUnblockDeps_DoesNotUnblockWhenOtherBlockersQueryFails(t *testing.T) {
	// Even if the deps query succeeds, a Count failure for other blockers
	// should NOT cause the car to be unblocked (fail-safe).
	db := testDB(t)

	db.Create(&models.Car{ID: "car-x", Status: "blocked", Track: "backend"})
	db.Create(&models.Car{ID: "car-y", Status: "merged", Track: "backend"})
	db.Create(&models.Car{ID: "car-z", Status: "open", Track: "backend"})
	db.Create(&models.CarDep{CarID: "car-x", BlockedBy: "car-y"})
	db.Create(&models.CarDep{CarID: "car-x", BlockedBy: "car-z"})

	// Unblocking car-y should NOT unblock car-x because car-z is still open.
	// To verify error handling: drop the cars table after creating deps.
	db.Exec("DROP TABLE cars")

	unblocked, _ := UnblockDeps(db, "car-y")
	if len(unblocked) > 0 {
		t.Errorf("should not have unblocked any cars when query fails, got %d", len(unblocked))
	}
}

func TestSwitch_UpdatesLocalRefAfterPush(t *testing.T) {
	// After Switch pushes a merge to the remote, the local tracking ref
	// (origin/<baseBranch>) must be updated so the next sibling merge
	// starts from the correct commit.
	repoDir, bareDir, run := initTestRepoWithRemote(t)

	// Create a feature branch with a commit that adds a file.
	run(repoDir, "git", "checkout", "-b", "ry/alice/backend/car-sib1")
	if err := os.WriteFile(filepath.Join(repoDir, "shared.txt"), []byte("from sib1"), 0o644); err != nil {
		t.Fatal(err)
	}
	run(repoDir, "git", "add", "shared.txt")
	run(repoDir, "git", "commit", "-m", "sib1: add shared.txt")
	run(repoDir, "git", "checkout", "main")

	// Set up DB.
	db := testDB(t)
	db.Create(&models.Car{
		ID:     "car-sib1",
		Title:  "Sibling 1",
		Track:  "backend",
		Branch: "ry/alice/backend/car-sib1",
		Status: "done",
	})

	// Merge sibling 1.
	result, err := Switch(db, "car-sib1", SwitchOpts{
		RepoDir:     repoDir,
		TestCommand: "true",
	})
	if err != nil {
		t.Fatalf("Switch sib1 failed: %v", err)
	}
	if !result.Merged {
		t.Fatal("sib1 should be merged")
	}

	// Verify origin/main is updated locally (not stale).
	cmd := exec.Command("git", "rev-parse", "origin/main")
	cmd.Dir = repoDir
	localOrigin, _ := cmd.Output()

	cmd = exec.Command("git", "rev-parse", "main")
	cmd.Dir = bareDir
	remoteMain, _ := cmd.Output()

	localRef := strings.TrimSpace(string(localOrigin))
	remoteRef := strings.TrimSpace(string(remoteMain))

	if localRef != remoteRef {
		t.Errorf("origin/main not updated after push\nlocal origin/main:  %s\nremote main:        %s", localRef, remoteRef)
	}
}

func TestGetConflictFiles_DuringRebaseConflict(t *testing.T) {
	// getConflictFiles must detect conflicts during a rebase, not just merges.
	repoDir, run := initTestRepo(t)

	// Create conflicting branches.
	run("git", "checkout", "-b", "base-branch")
	if err := os.WriteFile(filepath.Join(repoDir, "conflict.txt"), []byte("base content"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("git", "add", "conflict.txt")
	run("git", "commit", "-m", "base: add conflict.txt")

	run("git", "checkout", "main")
	run("git", "checkout", "-b", "feature-branch")
	if err := os.WriteFile(filepath.Join(repoDir, "conflict.txt"), []byte("feature content"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("git", "add", "conflict.txt")
	run("git", "commit", "-m", "feature: add conflict.txt")

	// Attempt rebase — will conflict.
	rebase := exec.Command("git", "rebase", "base-branch")
	rebase.Dir = repoDir
	rebase.CombinedOutput() // expected to fail

	// getConflictFiles should find conflict.txt.
	files := getConflictFiles(repoDir)
	if len(files) == 0 {
		t.Fatal("getConflictFiles returned 0 files during rebase conflict")
	}
	found := false
	for _, f := range files {
		if f == "conflict.txt" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("conflict.txt not in conflict files: %v", files)
	}

	// Clean up.
	gitRebaseAbort(repoDir)
}
