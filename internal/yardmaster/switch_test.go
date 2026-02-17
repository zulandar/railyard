package yardmaster

import (
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
	if r.CarID != "" || r.Branch != "" || r.TestsPassed || r.Merged || r.PRCreated || r.PRUrl != "" {
		t.Error("zero-value SwitchResult should have empty/false fields")
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

	run("git", "init")
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
	run(bareDir, "git", "init", "--bare")

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

	run(repoDir, "git", "init")
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

	run(repoDir, "git", "init")
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
