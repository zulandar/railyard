package yardmaster

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
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
	if opts.RepoDir != "" || opts.DryRun {
		t.Error("zero-value SwitchOpts should have empty fields")
	}
}

func TestSwitchResult_ZeroValue(t *testing.T) {
	r := SwitchResult{}
	if r.CarID != "" || r.Branch != "" || r.TestsPassed || r.Merged {
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

// --- TryCloseEpic validation tests ---

func TestTryCloseEpic_NilDB(t *testing.T) {
	// Should not panic with nil DB.
	TryCloseEpic(nil, "epic-001")
}

func TestTryCloseEpic_EmptyID(t *testing.T) {
	// Should not panic with empty ID.
	TryCloseEpic(nil, "")
}
