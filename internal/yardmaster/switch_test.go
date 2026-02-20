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

// --- CreateReindexJob validation tests ---

func TestCreateReindexJob_NilDB(t *testing.T) {
	err := CreateReindexJob(nil, "backend", "abc123")
	if err == nil {
		t.Fatal("expected error for nil db")
	}
	if !strings.Contains(err.Error(), "db is required") {
		t.Errorf("error = %q", err)
	}
}

func TestCreateReindexJob_EmptyTrack(t *testing.T) {
	err := CreateReindexJob(nil, "", "abc123")
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

	err := gitPush(repoDir)
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
	if err := gitPush(repoDir); err != nil {
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
	wtDir := filepath.Join(repoDir, "engines", engineID)
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
	wtDir := filepath.Join(repoDir, "engines", engineID)
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

	body := buildPRBody(db, &c, "/nonexistent") // repoDir doesn't matter — git diff will fail gracefully

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

	body := buildPRBody(db, &c, "/nonexistent")

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
	body := buildPRBody(nil, &c, "/nonexistent")
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

	if !isAncestor(repoDir, "feature") {
		t.Error("isAncestor should be true when feature is behind main")
	}
}

func TestIsAncestor_FalseWhenBranchAhead(t *testing.T) {
	repoDir, run := initTestRepo(t)

	// Create a feature branch with a new commit.
	run("git", "checkout", "-b", "feature")
	run("git", "commit", "--allow-empty", "-m", "feature work")
	run("git", "checkout", "main")

	if isAncestor(repoDir, "feature") {
		t.Error("isAncestor should be false when feature has commits not in main")
	}
}

func TestIsAncestor_TrueWhenSameCommit(t *testing.T) {
	repoDir, run := initTestRepo(t)

	// Branch at same point as main, no divergence.
	run("git", "checkout", "-b", "feature")
	run("git", "checkout", "main")

	if !isAncestor(repoDir, "feature") {
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

// --- runTests pre-test and no-test-files tests ---

func TestRunTests_PreTestCommand(t *testing.T) {
	repoDir, run := initTestRepo(t)

	run("git", "checkout", "-b", "feature")
	run("git", "checkout", "main")

	// Pre-test creates a marker file; test command checks it exists.
	markerPath := filepath.Join(repoDir, "marker.txt")
	preTest := "echo pre-test-ran > " + markerPath
	testCmd := "test -f " + markerPath

	output, err := runTests(repoDir, "feature", preTest, testCmd)
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
	_, err := runTests(repoDir, "feature", "false", "echo should-not-run")
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

	output, err := runTests(repoDir, "feature", "", testCmd)
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

	output, err := runTests(repoDir, "feature", "", testCmd)
	if err != nil {
		t.Fatalf("runTests should treat 'No tests found' as pass, got error: %v", err)
	}
	if !strings.Contains(output, "No tests found") {
		t.Errorf("output should contain the pattern, got: %s", output)
	}
}

func TestRunTests_RealTestFailure(t *testing.T) {
	repoDir, run := initTestRepo(t)

	run("git", "checkout", "-b", "feature")
	run("git", "checkout", "main")

	// A real failure that doesn't match any no-test patterns.
	testCmd := `echo "FAIL: TestSomething" && exit 1`

	_, err := runTests(repoDir, "feature", "", testCmd)
	if err == nil {
		t.Fatal("expected error for real test failure")
	}
	if !strings.Contains(err.Error(), "tests failed") {
		t.Errorf("error = %q, want to contain %q", err.Error(), "tests failed")
	}
}
