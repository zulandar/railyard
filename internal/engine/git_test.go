package engine

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// initTestRepo creates a bare git repo with one commit, returns the working directory.
func initTestRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()

	for _, args := range [][]string{
		{"git", "init", "-b", "main"},
		{"git", "config", "user.name", "Test"},
		{"git", "config", "user.email", "test@test.com"},
	} {
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%v: %s\n%s", args, err, out)
		}
	}

	// Create an initial commit so "main" branch exists.
	readme := filepath.Join(dir, "README.md")
	if err := os.WriteFile(readme, []byte("# test\n"), 0644); err != nil {
		t.Fatal(err)
	}

	for _, args := range [][]string{
		{"git", "add", "."},
		{"git", "commit", "-m", "initial"},
		{"git", "branch", "-M", "main"},
	} {
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%v: %s\n%s", args, err, out)
		}
	}

	return dir
}

// currentBranch returns the current branch name for a repo.
func currentBranch(t *testing.T, dir string) string {
	t.Helper()
	cmd := exec.Command("git", "rev-parse", "--abbrev-ref", "HEAD")
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("rev-parse: %s\n%s", err, out)
	}
	return strings.TrimSpace(string(out))
}

// addCommit creates a file and commits it.
func addCommit(t *testing.T, dir, msg string) {
	t.Helper()
	f := filepath.Join(dir, msg+".txt")
	if err := os.WriteFile(f, []byte(msg), 0644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"git", "add", "."},
		{"git", "commit", "-m", msg},
	} {
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%v: %s\n%s", args, err, out)
		}
	}
}

// --- DetectBaseBranch tests ---

func TestDetectBaseBranch_CurrentBranch(t *testing.T) {
	dir := initTestRepo(t)
	// On main branch — should detect "main".
	got := DetectBaseBranch(dir, "")
	if got != "main" {
		t.Errorf("DetectBaseBranch = %q, want %q", got, "main")
	}
}

func TestDetectBaseBranch_NonMainBranch(t *testing.T) {
	dir := initTestRepo(t)
	// Create and checkout a different branch.
	cmd := exec.Command("git", "checkout", "-b", "develop")
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("checkout -b develop: %s\n%s", err, out)
	}

	got := DetectBaseBranch(dir, "")
	if got != "develop" {
		t.Errorf("DetectBaseBranch = %q, want %q", got, "develop")
	}
}

func TestDetectBaseBranch_DetachedHEAD_FallsBackToConfig(t *testing.T) {
	dir := initTestRepo(t)
	// Detach HEAD.
	cmd := exec.Command("git", "checkout", "--detach", "HEAD")
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("detach HEAD: %s\n%s", err, out)
	}

	got := DetectBaseBranch(dir, "develop")
	if got != "develop" {
		t.Errorf("DetectBaseBranch = %q, want %q (config fallback)", got, "develop")
	}
}

func TestDetectBaseBranch_DetachedHEAD_NoConfig_FallsBackToOriginHEAD(t *testing.T) {
	// Create a bare repo as remote.
	bareDir := t.TempDir()
	run := func(d string, args ...string) {
		t.Helper()
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = d
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%v: %s\n%s", args, err, out)
		}
	}
	run(bareDir, "git", "init", "--bare", "-b", "trunk")

	// Create a working repo with that remote.
	dir := initTestRepo(t)
	run(dir, "git", "remote", "add", "origin", bareDir)
	run(dir, "git", "push", "-u", "origin", "main")

	// Set origin/HEAD to point to a branch called "trunk" in the bare repo.
	// First create "trunk" branch in the bare repo by pushing to it.
	run(dir, "git", "push", "origin", "main:trunk")
	// Now set origin/HEAD locally.
	run(dir, "git", "remote", "set-head", "origin", "trunk")

	// Detach HEAD so step 1 fails.
	run(dir, "git", "checkout", "--detach", "HEAD")

	got := DetectBaseBranch(dir, "")
	if got != "trunk" {
		t.Errorf("DetectBaseBranch = %q, want %q (origin/HEAD fallback)", got, "trunk")
	}
}

func TestDetectBaseBranch_FinalFallbackMain(t *testing.T) {
	// No repo dir, no config — should return "main".
	got := DetectBaseBranch("", "")
	if got != "main" {
		t.Errorf("DetectBaseBranch = %q, want %q (final fallback)", got, "main")
	}
}

func TestDetectBaseBranch_DetachedHEAD_NoRemote_FallsBackToMain(t *testing.T) {
	dir := initTestRepo(t)
	// Detach HEAD, no remote configured.
	cmd := exec.Command("git", "checkout", "--detach", "HEAD")
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("detach HEAD: %s\n%s", err, out)
	}

	got := DetectBaseBranch(dir, "")
	if got != "main" {
		t.Errorf("DetectBaseBranch = %q, want %q (no remote, final fallback)", got, "main")
	}
}

// --- EnsureWorktree tests ---

func TestEnsureWorktree_CreatesClaudeIgnore(t *testing.T) {
	dir := initTestRepo(t)

	wtDir, err := EnsureWorktree(dir, "eng-test0001")
	if err != nil {
		t.Fatalf("EnsureWorktree: %v", err)
	}

	ignoreFile := filepath.Join(wtDir, ".claudeignore")
	data, err := os.ReadFile(ignoreFile)
	if err != nil {
		t.Fatalf("expected .claudeignore to exist: %v", err)
	}

	content := string(data)
	for _, want := range []string{"railyard.yaml", ".beads/", ".railyard/"} {
		if !strings.Contains(content, want) {
			t.Errorf(".claudeignore missing %q, got:\n%s", want, content)
		}
	}
}

func TestEnsureWorktree_ReusedStillHasClaudeIgnore(t *testing.T) {
	dir := initTestRepo(t)

	wtDir, err := EnsureWorktree(dir, "eng-test0002")
	if err != nil {
		t.Fatalf("first EnsureWorktree: %v", err)
	}

	// Remove the ignore file to simulate a stale worktree without one.
	os.Remove(filepath.Join(wtDir, ".claudeignore"))

	// Second call reuses the existing worktree and should recreate .claudeignore.
	wtDir2, err := EnsureWorktree(dir, "eng-test0002")
	if err != nil {
		t.Fatalf("second EnsureWorktree: %v", err)
	}
	if wtDir2 != wtDir {
		t.Errorf("worktree path changed: %q → %q", wtDir, wtDir2)
	}

	if _, err := os.Stat(filepath.Join(wtDir2, ".claudeignore")); err != nil {
		t.Fatalf("expected .claudeignore after reuse: %v", err)
	}
}

// --- ResetWorktree tests ---

func TestResetWorktree_EmptyDir(t *testing.T) {
	err := ResetWorktree("", "")
	if err == nil {
		t.Fatal("expected error for empty dir")
	}
	if !strings.Contains(err.Error(), "worktree directory is required") {
		t.Errorf("error = %q, want to contain %q", err.Error(), "worktree directory is required")
	}
}

func TestResetWorktree_CleansUpDirtyState(t *testing.T) {
	dir := initTestRepo(t)

	// Create a worktree.
	wtDir, err := EnsureWorktree(dir, "eng-reset001")
	if err != nil {
		t.Fatalf("EnsureWorktree: %v", err)
	}

	// Make the worktree dirty: create a branch, add uncommitted files.
	run := func(d string, args ...string) {
		t.Helper()
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = d
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%v: %s\n%s", args, err, out)
		}
	}
	run(wtDir, "git", "checkout", "-b", "dirty-branch")
	os.WriteFile(filepath.Join(wtDir, "untracked.txt"), []byte("junk"), 0644)
	os.WriteFile(filepath.Join(wtDir, "README.md"), []byte("modified\n"), 0644)

	// Reset should succeed.
	if err := ResetWorktree(wtDir, ""); err != nil {
		t.Fatalf("ResetWorktree: %v", err)
	}

	// Verify: HEAD should be detached (not on dirty-branch).
	branch := currentBranch(t, wtDir)
	if branch != "HEAD" {
		t.Errorf("expected detached HEAD, got branch %q", branch)
	}

	// Verify: untracked file should be gone.
	if _, err := os.Stat(filepath.Join(wtDir, "untracked.txt")); !os.IsNotExist(err) {
		t.Error("expected untracked.txt to be removed")
	}

	// Verify: modified file should be restored.
	data, err := os.ReadFile(filepath.Join(wtDir, "README.md"))
	if err != nil {
		t.Fatalf("read README.md: %v", err)
	}
	if string(data) != "# test\n" {
		t.Errorf("README.md = %q, want original content", string(data))
	}
}

func TestResetWorktree_UpdatesToLatestMain(t *testing.T) {
	dir := initTestRepo(t)

	// Create a worktree.
	wtDir, err := EnsureWorktree(dir, "eng-reset002")
	if err != nil {
		t.Fatalf("EnsureWorktree: %v", err)
	}

	// Advance main in the parent repo (simulates yardmaster merging another car).
	run := func(d string, args ...string) {
		t.Helper()
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = d
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%v: %s\n%s", args, err, out)
		}
	}
	run(dir, "git", "checkout", "main")
	os.WriteFile(filepath.Join(dir, "newfile.txt"), []byte("from main\n"), 0644)
	run(dir, "git", "add", "newfile.txt")
	run(dir, "git", "commit", "-m", "advance main")

	// Get the main HEAD hash.
	getHash := exec.Command("git", "rev-parse", "main")
	getHash.Dir = dir
	mainHash, _ := getHash.CombinedOutput()

	// Reset the worktree — should pick up the new main commit.
	if err := ResetWorktree(wtDir, ""); err != nil {
		t.Fatalf("ResetWorktree: %v", err)
	}

	// Verify: worktree HEAD should match main.
	getWtHash := exec.Command("git", "rev-parse", "HEAD")
	getWtHash.Dir = wtDir
	wtHash, _ := getWtHash.CombinedOutput()

	if strings.TrimSpace(string(wtHash)) != strings.TrimSpace(string(mainHash)) {
		t.Errorf("worktree HEAD = %s, want main HEAD = %s",
			strings.TrimSpace(string(wtHash)), strings.TrimSpace(string(mainHash)))
	}

	// Verify: new file from main should be present.
	if _, err := os.Stat(filepath.Join(wtDir, "newfile.txt")); err != nil {
		t.Errorf("expected newfile.txt from advanced main: %v", err)
	}
}

func TestResetWorktree_ThenCreateBranch(t *testing.T) {
	dir := initTestRepo(t)

	// Create a worktree.
	wtDir, err := EnsureWorktree(dir, "eng-reset003")
	if err != nil {
		t.Fatalf("EnsureWorktree: %v", err)
	}

	// Simulate a dirty worktree from previous car.
	run := func(d string, args ...string) {
		t.Helper()
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = d
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%v: %s\n%s", args, err, out)
		}
	}
	run(wtDir, "git", "checkout", "-b", "ry/old/car")
	os.WriteFile(filepath.Join(wtDir, "leftover.txt"), []byte("old work"), 0644)
	run(wtDir, "git", "add", ".")
	run(wtDir, "git", "commit", "-m", "old car work")

	// Reset then branch — the full flow the engine does.
	if err := ResetWorktree(wtDir, ""); err != nil {
		t.Fatalf("ResetWorktree: %v", err)
	}
	if err := CreateBranch(wtDir, "ry/new/car", ""); err != nil {
		t.Fatalf("CreateBranch after reset: %v", err)
	}

	got := currentBranch(t, wtDir)
	if got != "ry/new/car" {
		t.Errorf("branch = %q, want %q", got, "ry/new/car")
	}

	// Old car's file should not be present (was on old branch, not main).
	if _, err := os.Stat(filepath.Join(wtDir, "leftover.txt")); !os.IsNotExist(err) {
		t.Error("expected leftover.txt from old car to be gone")
	}
}

// --- CreateBranch tests ---

func TestCreateBranch(t *testing.T) {
	dir := initTestRepo(t)

	if err := CreateBranch(dir, "ry/alice/backend/car-abc12", ""); err != nil {
		t.Fatalf("CreateBranch: %v", err)
	}

	got := currentBranch(t, dir)
	if got != "ry/alice/backend/car-abc12" {
		t.Errorf("branch = %q, want %q", got, "ry/alice/backend/car-abc12")
	}
}

func TestCreateBranch_AlreadyExists(t *testing.T) {
	dir := initTestRepo(t)

	// Create the branch first time.
	if err := CreateBranch(dir, "ry/test/branch", ""); err != nil {
		t.Fatalf("first CreateBranch: %v", err)
	}

	// Switch back to main.
	cmd := exec.Command("git", "checkout", "main")
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("checkout main: %s\n%s", err, out)
	}

	// Create again — should checkout existing.
	if err := CreateBranch(dir, "ry/test/branch", ""); err != nil {
		t.Fatalf("second CreateBranch: %v", err)
	}

	got := currentBranch(t, dir)
	if got != "ry/test/branch" {
		t.Errorf("branch = %q, want %q", got, "ry/test/branch")
	}
}

func TestCreateBranch_EmptyName(t *testing.T) {
	err := CreateBranch("/tmp", "", "")
	if err == nil {
		t.Fatal("expected error for empty branch name")
	}
	if !strings.Contains(err.Error(), "branch name is required") {
		t.Errorf("error = %q, want to contain %q", err.Error(), "branch name is required")
	}
}

func TestCreateBranch_EmptyRepoDir(t *testing.T) {
	err := CreateBranch("", "some-branch", "")
	if err == nil {
		t.Fatal("expected error for empty repo dir")
	}
	if !strings.Contains(err.Error(), "repo directory is required") {
		t.Errorf("error = %q, want to contain %q", err.Error(), "repo directory is required")
	}
}

func TestCreateBranch_BadDir(t *testing.T) {
	err := CreateBranch("/nonexistent/path", "some-branch", "")
	if err == nil {
		t.Fatal("expected error for bad directory")
	}
}

func TestResetWorktree_NonMainBaseBranch(t *testing.T) {
	dir := initTestRepo(t)

	// Create a "develop" branch with different content.
	run := func(d string, args ...string) {
		t.Helper()
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = d
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%v: %s\n%s", args, err, out)
		}
	}
	run(dir, "git", "checkout", "-b", "develop")
	os.WriteFile(filepath.Join(dir, "develop.txt"), []byte("develop content\n"), 0644)
	run(dir, "git", "add", ".")
	run(dir, "git", "commit", "-m", "develop commit")
	run(dir, "git", "checkout", "main")

	// Create a worktree.
	wtDir, err := EnsureWorktree(dir, "eng-base001")
	if err != nil {
		t.Fatalf("EnsureWorktree: %v", err)
	}

	// Reset to "develop" base branch.
	if err := ResetWorktree(wtDir, "develop"); err != nil {
		t.Fatalf("ResetWorktree(develop): %v", err)
	}

	// Verify develop.txt exists (we're at develop, not main).
	if _, err := os.Stat(filepath.Join(wtDir, "develop.txt")); err != nil {
		t.Error("expected develop.txt after resetting to develop branch")
	}
}

func TestCreateBranch_FromNonMainBase(t *testing.T) {
	dir := initTestRepo(t)

	// Create a "develop" branch with a unique file.
	run := func(d string, args ...string) {
		t.Helper()
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = d
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%v: %s\n%s", args, err, out)
		}
	}
	run(dir, "git", "checkout", "-b", "develop")
	os.WriteFile(filepath.Join(dir, "develop.txt"), []byte("develop content\n"), 0644)
	run(dir, "git", "add", ".")
	run(dir, "git", "commit", "-m", "develop commit")
	run(dir, "git", "checkout", "main")

	// Branch from develop.
	if err := CreateBranch(dir, "ry/alice/feat-x", "develop"); err != nil {
		t.Fatalf("CreateBranch from develop: %v", err)
	}

	got := currentBranch(t, dir)
	if got != "ry/alice/feat-x" {
		t.Errorf("branch = %q, want %q", got, "ry/alice/feat-x")
	}

	// Verify develop.txt is present (branched from develop, not main).
	if _, err := os.Stat(filepath.Join(dir, "develop.txt")); err != nil {
		t.Error("expected develop.txt from develop base branch")
	}
}

// --- PushBranch tests ---

func TestPushBranch_EmptyName(t *testing.T) {
	err := PushBranch("/tmp", "")
	if err == nil {
		t.Fatal("expected error for empty branch name")
	}
	if !strings.Contains(err.Error(), "branch name is required") {
		t.Errorf("error = %q, want to contain %q", err.Error(), "branch name is required")
	}
}

func TestPushBranch_EmptyRepoDir(t *testing.T) {
	err := PushBranch("", "some-branch")
	if err == nil {
		t.Fatal("expected error for empty repo dir")
	}
	if !strings.Contains(err.Error(), "repo directory is required") {
		t.Errorf("error = %q, want to contain %q", err.Error(), "repo directory is required")
	}
}

func TestPushBranch_NoRemote(t *testing.T) {
	dir := initTestRepo(t)

	err := PushBranch(dir, "main")
	if err == nil {
		t.Fatal("expected error when no remote configured")
	}
	// Should mention attempt 2 since it retries once.
	if !strings.Contains(err.Error(), "attempt 2") {
		t.Errorf("error = %q, want to contain %q", err.Error(), "attempt 2")
	}
}

func TestPushBranch_WithRemote(t *testing.T) {
	// Create a bare repo to act as remote.
	bareDir := t.TempDir()
	cmd := exec.Command("git", "init", "--bare", "-b", "main")
	cmd.Dir = bareDir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init --bare: %s\n%s", err, out)
	}

	// Create a working repo with the bare as remote.
	dir := initTestRepo(t)
	remote := exec.Command("git", "remote", "add", "origin", bareDir)
	remote.Dir = dir
	if out, err := remote.CombinedOutput(); err != nil {
		t.Fatalf("git remote add: %s\n%s", err, out)
	}

	if err := PushBranch(dir, "main"); err != nil {
		t.Fatalf("PushBranch: %v", err)
	}
}

// --- RecentCommits tests ---

func TestRecentCommits(t *testing.T) {
	dir := initTestRepo(t)
	addCommit(t, dir, "second commit")
	addCommit(t, dir, "third commit")

	commits, err := RecentCommits(dir, "main", 3)
	if err != nil {
		t.Fatalf("RecentCommits: %v", err)
	}
	if len(commits) != 3 {
		t.Fatalf("got %d commits, want 3", len(commits))
	}
	// Most recent first.
	if !strings.Contains(commits[0], "third commit") {
		t.Errorf("commits[0] = %q, want to contain %q", commits[0], "third commit")
	}
	if !strings.Contains(commits[1], "second commit") {
		t.Errorf("commits[1] = %q, want to contain %q", commits[1], "second commit")
	}
	if !strings.Contains(commits[2], "initial") {
		t.Errorf("commits[2] = %q, want to contain %q", commits[2], "initial")
	}
}

func TestRecentCommits_LimitN(t *testing.T) {
	dir := initTestRepo(t)
	addCommit(t, dir, "second")
	addCommit(t, dir, "third")

	commits, err := RecentCommits(dir, "main", 1)
	if err != nil {
		t.Fatalf("RecentCommits: %v", err)
	}
	if len(commits) != 1 {
		t.Fatalf("got %d commits, want 1", len(commits))
	}
}

func TestRecentCommits_ZeroN(t *testing.T) {
	dir := initTestRepo(t)
	commits, err := RecentCommits(dir, "main", 0)
	if err != nil {
		t.Fatalf("RecentCommits: %v", err)
	}
	if commits != nil {
		t.Errorf("expected nil for n=0, got %v", commits)
	}
}

func TestRecentCommits_NegativeN(t *testing.T) {
	dir := initTestRepo(t)
	commits, err := RecentCommits(dir, "main", -1)
	if err != nil {
		t.Fatalf("RecentCommits: %v", err)
	}
	if commits != nil {
		t.Errorf("expected nil for n=-1, got %v", commits)
	}
}

func TestRecentCommits_EmptyBranch(t *testing.T) {
	_, err := RecentCommits("/tmp", "", 5)
	if err == nil {
		t.Fatal("expected error for empty branch name")
	}
	if !strings.Contains(err.Error(), "branch name is required") {
		t.Errorf("error = %q, want to contain %q", err.Error(), "branch name is required")
	}
}

func TestRecentCommits_EmptyRepoDir(t *testing.T) {
	_, err := RecentCommits("", "main", 5)
	if err == nil {
		t.Fatal("expected error for empty repo dir")
	}
	if !strings.Contains(err.Error(), "repo directory is required") {
		t.Errorf("error = %q, want to contain %q", err.Error(), "repo directory is required")
	}
}

func TestRecentCommits_BadBranch(t *testing.T) {
	dir := initTestRepo(t)
	_, err := RecentCommits(dir, "nonexistent-branch", 5)
	if err == nil {
		t.Fatal("expected error for nonexistent branch")
	}
}

func TestRecentCommits_MoreThanAvailable(t *testing.T) {
	dir := initTestRepo(t)
	// Repo has exactly 1 commit ("initial").
	commits, err := RecentCommits(dir, "main", 10)
	if err != nil {
		t.Fatalf("RecentCommits: %v", err)
	}
	if len(commits) != 1 {
		t.Fatalf("got %d commits, want 1 (only 1 exists)", len(commits))
	}
}

func TestRecentCommits_OnBranch(t *testing.T) {
	dir := initTestRepo(t)

	// Create a branch and add a commit to it.
	if err := CreateBranch(dir, "feature-branch", ""); err != nil {
		t.Fatalf("CreateBranch: %v", err)
	}
	addCommit(t, dir, "branch commit")

	commits, err := RecentCommits(dir, "feature-branch", 2)
	if err != nil {
		t.Fatalf("RecentCommits: %v", err)
	}
	if len(commits) != 2 {
		t.Fatalf("got %d commits, want 2", len(commits))
	}
	if !strings.Contains(commits[0], "branch commit") {
		t.Errorf("commits[0] = %q, want to contain %q", commits[0], "branch commit")
	}
}

// --- ChangedFiles tests ---

func TestChangedFiles_EmptyRepoDir(t *testing.T) {
	_, err := ChangedFiles("")
	if err == nil {
		t.Fatal("expected error for empty repo dir")
	}
	if !strings.Contains(err.Error(), "repo directory is required") {
		t.Errorf("error = %q, want to contain %q", err.Error(), "repo directory is required")
	}
}

func TestChangedFiles_NoChanges(t *testing.T) {
	dir := initTestRepo(t)
	files, err := ChangedFiles(dir)
	if err != nil {
		t.Fatalf("ChangedFiles: %v", err)
	}
	if files != nil {
		t.Errorf("expected nil for no changes, got %v", files)
	}
}

func TestChangedFiles_WithChanges(t *testing.T) {
	dir := initTestRepo(t)

	// Modify an existing file.
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("modified\n"), 0644); err != nil {
		t.Fatal(err)
	}

	files, err := ChangedFiles(dir)
	if err != nil {
		t.Fatalf("ChangedFiles: %v", err)
	}
	if len(files) != 1 {
		t.Fatalf("got %d files, want 1", len(files))
	}
	if files[0] != "README.md" {
		t.Errorf("files[0] = %q, want %q", files[0], "README.md")
	}
}

func TestChangedFiles_MultipleFiles(t *testing.T) {
	dir := initTestRepo(t)

	// Modify existing and create new (tracked) files.
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("modified\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "new.txt"), []byte("new file\n"), 0644); err != nil {
		t.Fatal(err)
	}
	// Stage the new file so git diff HEAD sees it.
	cmd := exec.Command("git", "add", "new.txt")
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git add: %s\n%s", err, out)
	}

	files, err := ChangedFiles(dir)
	if err != nil {
		t.Fatalf("ChangedFiles: %v", err)
	}
	if len(files) != 2 {
		t.Fatalf("got %d files, want 2: %v", len(files), files)
	}
}

// --- CommitsAheadOfBase tests ---

func TestCommitsAheadOfBase_EmptyRepoDir(t *testing.T) {
	_, err := CommitsAheadOfBase("", "main")
	if err == nil {
		t.Fatal("expected error for empty repo dir")
	}
}

func TestCommitsAheadOfBase_ZeroCommits(t *testing.T) {
	dir := initTestRepo(t)

	// Create a branch from main — zero commits ahead.
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%v: %s\n%s", args, err, out)
		}
	}
	run("git", "checkout", "-b", "feature")

	count, err := CommitsAheadOfBase(dir, "main")
	if err != nil {
		t.Fatalf("CommitsAheadOfBase: %v", err)
	}
	if count != 0 {
		t.Errorf("count = %d, want 0", count)
	}
}

func TestCommitsAheadOfBase_WithCommits(t *testing.T) {
	dir := initTestRepo(t)

	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%v: %s\n%s", args, err, out)
		}
	}
	run("git", "checkout", "-b", "feature")
	if err := os.WriteFile(filepath.Join(dir, "feature.txt"), []byte("work\n"), 0644); err != nil {
		t.Fatal(err)
	}
	run("git", "add", ".")
	run("git", "commit", "-m", "feature work")

	count, err := CommitsAheadOfBase(dir, "main")
	if err != nil {
		t.Fatalf("CommitsAheadOfBase: %v", err)
	}
	if count != 1 {
		t.Errorf("count = %d, want 1", count)
	}
}

func TestCommitsAheadOfBase_DefaultBase(t *testing.T) {
	dir := initTestRepo(t)

	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%v: %s\n%s", args, err, out)
		}
	}
	run("git", "checkout", "-b", "feature")
	if err := os.WriteFile(filepath.Join(dir, "f.txt"), []byte("x\n"), 0644); err != nil {
		t.Fatal(err)
	}
	run("git", "add", ".")
	run("git", "commit", "-m", "work")

	// Empty baseBranch defaults to "main".
	count, err := CommitsAheadOfBase(dir, "")
	if err != nil {
		t.Fatalf("CommitsAheadOfBase: %v", err)
	}
	if count != 1 {
		t.Errorf("count = %d, want 1", count)
	}
}

// --- AutoCommitIfDirty tests ---

func TestAutoCommitIfDirty_EmptyRepoDir(t *testing.T) {
	_, err := AutoCommitIfDirty("", "msg")
	if err == nil {
		t.Fatal("expected error for empty repo dir")
	}
}

func TestAutoCommitIfDirty_CleanWorktree(t *testing.T) {
	dir := initTestRepo(t)

	committed, err := AutoCommitIfDirty(dir, "test")
	if err != nil {
		t.Fatalf("AutoCommitIfDirty: %v", err)
	}
	if committed {
		t.Error("expected no commit on clean worktree")
	}
}

func TestAutoCommitIfDirty_WithUncommittedChanges(t *testing.T) {
	dir := initTestRepo(t)

	// Create a dirty file.
	if err := os.WriteFile(filepath.Join(dir, "dirty.txt"), []byte("uncommitted\n"), 0644); err != nil {
		t.Fatal(err)
	}

	committed, err := AutoCommitIfDirty(dir, "auto-save")
	if err != nil {
		t.Fatalf("AutoCommitIfDirty: %v", err)
	}
	if !committed {
		t.Fatal("expected commit on dirty worktree")
	}

	// Verify commit message.
	cmd := exec.Command("git", "log", "-1", "--format=%s")
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git log: %v", err)
	}
	if strings.TrimSpace(string(out)) != "auto-save" {
		t.Errorf("commit message = %q, want %q", strings.TrimSpace(string(out)), "auto-save")
	}

	// Verify worktree is now clean.
	committed2, err := AutoCommitIfDirty(dir, "should-not-run")
	if err != nil {
		t.Fatalf("second AutoCommitIfDirty: %v", err)
	}
	if committed2 {
		t.Error("expected no commit after auto-commit cleaned worktree")
	}
}

func TestAutoCommitIfDirty_DefaultMessage(t *testing.T) {
	dir := initTestRepo(t)

	if err := os.WriteFile(filepath.Join(dir, "new.txt"), []byte("data\n"), 0644); err != nil {
		t.Fatal(err)
	}

	committed, err := AutoCommitIfDirty(dir, "")
	if err != nil {
		t.Fatalf("AutoCommitIfDirty: %v", err)
	}
	if !committed {
		t.Fatal("expected commit")
	}

	cmd := exec.Command("git", "log", "-1", "--format=%s")
	cmd.Dir = dir
	out, _ := cmd.CombinedOutput()
	if !strings.Contains(string(out), "railyard: auto-commit uncommitted work") {
		t.Errorf("expected default message, got %q", strings.TrimSpace(string(out)))
	}
}

func TestAutoCommitIfDirty_StagedAndUntracked(t *testing.T) {
	dir := initTestRepo(t)

	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%v: %s\n%s", args, err, out)
		}
	}

	// Staged change.
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("modified\n"), 0644); err != nil {
		t.Fatal(err)
	}
	run("git", "add", "README.md")

	// Untracked file.
	if err := os.WriteFile(filepath.Join(dir, "untracked.txt"), []byte("new\n"), 0644); err != nil {
		t.Fatal(err)
	}

	committed, err := AutoCommitIfDirty(dir, "mixed changes")
	if err != nil {
		t.Fatalf("AutoCommitIfDirty: %v", err)
	}
	if !committed {
		t.Fatal("expected commit")
	}

	// Both files should be in the commit.
	cmd := exec.Command("git", "diff", "--name-only", "HEAD~1", "HEAD")
	cmd.Dir = dir
	out, _ := cmd.CombinedOutput()
	if !strings.Contains(string(out), "README.md") || !strings.Contains(string(out), "untracked.txt") {
		t.Errorf("expected both files in commit, got: %s", out)
	}
}

// --- EnsureDispatchWorktree tests ---

func TestEnsureDispatchWorktree(t *testing.T) {
	dir := initTestRepo(t)

	wtDir, err := EnsureDispatchWorktree(dir)
	if err != nil {
		t.Fatalf("EnsureDispatchWorktree: %v", err)
	}

	// Verify path.
	expected := filepath.Join(dir, ".railyard", "dispatch")
	if wtDir != expected {
		t.Errorf("wtDir = %q, want %q", wtDir, expected)
	}

	// Verify .claudeignore exists.
	if _, err := os.Stat(filepath.Join(wtDir, ".claudeignore")); err != nil {
		t.Errorf("expected .claudeignore in dispatch worktree: %v", err)
	}

	// Verify railyard.yaml symlink (create source first).
	os.WriteFile(filepath.Join(dir, "railyard.yaml"), []byte("owner: test\n"), 0644)
	wtDir2, err := EnsureDispatchWorktree(dir)
	if err != nil {
		t.Fatalf("second EnsureDispatchWorktree: %v", err)
	}
	if wtDir2 != wtDir {
		t.Errorf("reuse path changed: %q → %q", wtDir, wtDir2)
	}
	linkTarget, err := os.Readlink(filepath.Join(wtDir2, "railyard.yaml"))
	if err != nil {
		t.Fatalf("expected railyard.yaml symlink: %v", err)
	}
	if linkTarget != filepath.Join(dir, "railyard.yaml") {
		t.Errorf("symlink target = %q, want %q", linkTarget, filepath.Join(dir, "railyard.yaml"))
	}
}

func TestEnsureDispatchWorktree_EmptyDir(t *testing.T) {
	_, err := EnsureDispatchWorktree("")
	if err == nil {
		t.Fatal("expected error for empty dir")
	}
}

// --- EnsureYardmasterWorktree tests ---

func TestEnsureYardmasterWorktree(t *testing.T) {
	dir := initTestRepo(t)

	wtDir, err := EnsureYardmasterWorktree(dir)
	if err != nil {
		t.Fatalf("EnsureYardmasterWorktree: %v", err)
	}

	expected := filepath.Join(dir, ".railyard", "yardmaster")
	if wtDir != expected {
		t.Errorf("wtDir = %q, want %q", wtDir, expected)
	}

	// Verify reuse.
	wtDir2, err := EnsureYardmasterWorktree(dir)
	if err != nil {
		t.Fatalf("second EnsureYardmasterWorktree: %v", err)
	}
	if wtDir2 != wtDir {
		t.Errorf("reuse path changed: %q → %q", wtDir, wtDir2)
	}
}

func TestEnsureYardmasterWorktree_EmptyDir(t *testing.T) {
	_, err := EnsureYardmasterWorktree("")
	if err == nil {
		t.Fatal("expected error for empty dir")
	}
}

// --- SyncWorktreeToBranch tests ---

func TestSyncWorktreeToBranch(t *testing.T) {
	dir := initTestRepo(t)

	// Create a "develop" branch with a different file.
	run := func(d string, args ...string) {
		t.Helper()
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = d
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%v: %s\n%s", args, err, out)
		}
	}
	run(dir, "git", "checkout", "-b", "develop")
	os.WriteFile(filepath.Join(dir, "develop.txt"), []byte("develop branch\n"), 0644)
	run(dir, "git", "add", ".")
	run(dir, "git", "commit", "-m", "develop commit")
	run(dir, "git", "checkout", "main")

	// Create a worktree.
	wtDir, err := EnsureYardmasterWorktree(dir)
	if err != nil {
		t.Fatalf("EnsureYardmasterWorktree: %v", err)
	}

	// Sync to develop branch.
	if err := SyncWorktreeToBranch(wtDir, "develop", dir); err != nil {
		t.Fatalf("SyncWorktreeToBranch: %v", err)
	}

	// Verify develop.txt exists.
	if _, err := os.Stat(filepath.Join(wtDir, "develop.txt")); err != nil {
		t.Errorf("expected develop.txt after sync: %v", err)
	}

	// Sync back to main.
	if err := SyncWorktreeToBranch(wtDir, "main", dir); err != nil {
		t.Fatalf("SyncWorktreeToBranch main: %v", err)
	}

	// develop.txt should be gone.
	if _, err := os.Stat(filepath.Join(wtDir, "develop.txt")); !os.IsNotExist(err) {
		t.Error("expected develop.txt to be gone after sync to main")
	}
}

func TestSyncWorktreeToBranch_EmptyDir(t *testing.T) {
	err := SyncWorktreeToBranch("", "main", "")
	if err == nil {
		t.Fatal("expected error for empty dir")
	}
}

func TestSyncWorktreeToBranch_EmptyBranchDefaultsToMain(t *testing.T) {
	dir := initTestRepo(t)
	wtDir, err := EnsureYardmasterWorktree(dir)
	if err != nil {
		t.Fatalf("EnsureYardmasterWorktree: %v", err)
	}

	// Should not error with empty branch (defaults to "main").
	if err := SyncWorktreeToBranch(wtDir, "", dir); err != nil {
		t.Fatalf("SyncWorktreeToBranch empty branch: %v", err)
	}
}

func TestResetWorktree_PreservesRailyardFiles(t *testing.T) {
	dir := initTestRepo(t)

	wtDir, err := EnsureWorktree(dir, "eng-preserve01")
	if err != nil {
		t.Fatalf("EnsureWorktree: %v", err)
	}

	// Create files that should be preserved and one that should not.
	os.WriteFile(filepath.Join(wtDir, ".claudeignore"), []byte("railyard.yaml\n"), 0644)
	os.WriteFile(filepath.Join(wtDir, ".mcp.json"), []byte(`{"mcpServers":{}}`), 0644)
	os.WriteFile(filepath.Join(wtDir, "junk.txt"), []byte("delete me"), 0644)

	if err := ResetWorktree(wtDir, ""); err != nil {
		t.Fatalf("ResetWorktree: %v", err)
	}

	// .claudeignore should be preserved (excluded from clean, then re-written).
	if _, err := os.Stat(filepath.Join(wtDir, ".claudeignore")); err != nil {
		t.Error("expected .claudeignore to survive git clean")
	}

	// .mcp.json should be preserved (excluded from clean).
	if _, err := os.Stat(filepath.Join(wtDir, ".mcp.json")); err != nil {
		t.Error("expected .mcp.json to survive git clean")
	}

	// junk.txt should be removed.
	if _, err := os.Stat(filepath.Join(wtDir, "junk.txt")); !os.IsNotExist(err) {
		t.Error("expected junk.txt to be removed by git clean")
	}
}

// --- CheckoutExistingBranch tests ---

func TestCheckoutExistingBranch(t *testing.T) {
	bareDir := t.TempDir()
	parentDir := t.TempDir()

	run := func(dir string, args ...string) {
		t.Helper()
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = dir
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("%v in %s: %s", args, dir, out)
		}
	}

	// Create bare remote.
	run(bareDir, "git", "init", "--bare", "-b", "main")

	// Clone, commit, push.
	run(parentDir, "git", "clone", bareDir, "repo")
	repoDir := filepath.Join(parentDir, "repo")
	run(repoDir, "git", "config", "user.email", "test@test.com")
	run(repoDir, "git", "config", "user.name", "test")
	run(repoDir, "git", "commit", "--allow-empty", "-m", "init")
	run(repoDir, "git", "push", "origin", "main")

	// Create a feature branch, add a file, push it.
	run(repoDir, "git", "checkout", "-b", "ry/backend/car-abc")
	os.WriteFile(filepath.Join(repoDir, "feature.txt"), []byte("work"), 0644)
	run(repoDir, "git", "add", "feature.txt")
	run(repoDir, "git", "commit", "-m", "feature work")
	run(repoDir, "git", "push", "origin", "ry/backend/car-abc")

	// Go back to main.
	run(repoDir, "git", "checkout", "main")

	// Now checkout the existing branch.
	if err := CheckoutExistingBranch(repoDir, "ry/backend/car-abc"); err != nil {
		t.Fatalf("CheckoutExistingBranch: %v", err)
	}

	// Verify we're on the right branch.
	got := currentBranch(t, repoDir)
	if got != "ry/backend/car-abc" {
		t.Errorf("branch = %q, want %q", got, "ry/backend/car-abc")
	}

	// Verify the feature file exists.
	if _, err := os.Stat(filepath.Join(repoDir, "feature.txt")); err != nil {
		t.Error("feature.txt should exist on the checked-out branch")
	}
}

func TestCheckoutExistingBranch_EmptyDir(t *testing.T) {
	err := CheckoutExistingBranch("", "ry/test")
	if err == nil {
		t.Fatal("expected error for empty dir")
	}
}

func TestCheckoutExistingBranch_EmptyBranch(t *testing.T) {
	err := CheckoutExistingBranch("/tmp", "")
	if err == nil {
		t.Fatal("expected error for empty branch")
	}
}

func TestRemoteBranchExists_True(t *testing.T) {
	bareDir := t.TempDir()
	parentDir := t.TempDir()

	run := func(dir string, args ...string) {
		t.Helper()
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = dir
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("%v in %s: %s", args, dir, out)
		}
	}

	run(bareDir, "git", "init", "--bare", "-b", "main")
	run(parentDir, "git", "clone", bareDir, "repo")
	repoDir := filepath.Join(parentDir, "repo")
	run(repoDir, "git", "config", "user.email", "test@test.com")
	run(repoDir, "git", "config", "user.name", "test")
	run(repoDir, "git", "commit", "--allow-empty", "-m", "init")
	run(repoDir, "git", "push", "origin", "main")
	run(repoDir, "git", "checkout", "-b", "ry/feature")
	run(repoDir, "git", "commit", "--allow-empty", "-m", "feature")
	run(repoDir, "git", "push", "origin", "ry/feature")
	run(repoDir, "git", "checkout", "main")

	if !RemoteBranchExists(repoDir, "ry/feature") {
		t.Error("expected RemoteBranchExists to return true for pushed branch")
	}
}

func TestRemoteBranchExists_False(t *testing.T) {
	bareDir := t.TempDir()
	parentDir := t.TempDir()

	run := func(dir string, args ...string) {
		t.Helper()
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = dir
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("%v in %s: %s", args, dir, out)
		}
	}

	run(bareDir, "git", "init", "--bare", "-b", "main")
	run(parentDir, "git", "clone", bareDir, "repo")
	repoDir := filepath.Join(parentDir, "repo")
	run(repoDir, "git", "config", "user.email", "test@test.com")
	run(repoDir, "git", "config", "user.name", "test")
	run(repoDir, "git", "commit", "--allow-empty", "-m", "init")
	run(repoDir, "git", "push", "origin", "main")

	if RemoteBranchExists(repoDir, "ry/nonexistent") {
		t.Error("expected RemoteBranchExists to return false for non-existent branch")
	}
}

func TestCheckoutExistingBranch_DeletedRemote(t *testing.T) {
	bareDir := t.TempDir()
	parentDir := t.TempDir()

	run := func(dir string, args ...string) {
		t.Helper()
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = dir
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("%v in %s: %s", args, dir, out)
		}
	}

	run(bareDir, "git", "init", "--bare", "-b", "main")
	run(parentDir, "git", "clone", bareDir, "repo")
	repoDir := filepath.Join(parentDir, "repo")
	run(repoDir, "git", "config", "user.email", "test@test.com")
	run(repoDir, "git", "config", "user.name", "test")
	run(repoDir, "git", "commit", "--allow-empty", "-m", "init")
	run(repoDir, "git", "push", "origin", "main")

	// Create, push, then delete remote branch.
	run(repoDir, "git", "checkout", "-b", "ry/deleted")
	run(repoDir, "git", "commit", "--allow-empty", "-m", "will be deleted")
	run(repoDir, "git", "push", "origin", "ry/deleted")
	run(repoDir, "git", "checkout", "main")
	run(repoDir, "git", "push", "origin", "--delete", "ry/deleted")

	// CheckoutExistingBranch should fail because remote branch is gone.
	err := CheckoutExistingBranch(repoDir, "ry/deleted")
	if err == nil {
		t.Fatal("expected error when remote branch was deleted")
	}
}

func TestSyncWorktreeToBranch_PreservesRailyardFiles(t *testing.T) {
	dir := initTestRepo(t)

	wtDir, err := EnsureYardmasterWorktree(dir)
	if err != nil {
		t.Fatalf("EnsureYardmasterWorktree: %v", err)
	}

	// Create files that should be preserved and one that should not.
	os.WriteFile(filepath.Join(wtDir, ".claudeignore"), []byte("railyard.yaml\n"), 0644)
	os.WriteFile(filepath.Join(wtDir, ".mcp.json"), []byte(`{"mcpServers":{}}`), 0644)
	os.WriteFile(filepath.Join(wtDir, "junk.txt"), []byte("delete me"), 0644)

	// Create railyard.yaml in repo root for symlink test.
	os.WriteFile(filepath.Join(dir, "railyard.yaml"), []byte("owner: test\n"), 0644)

	if err := SyncWorktreeToBranch(wtDir, "main", dir); err != nil {
		t.Fatalf("SyncWorktreeToBranch: %v", err)
	}

	// .claudeignore should be present (excluded from clean, then re-written).
	if _, err := os.Stat(filepath.Join(wtDir, ".claudeignore")); err != nil {
		t.Error("expected .claudeignore to survive sync")
	}

	// .mcp.json should be preserved (excluded from clean).
	if _, err := os.Stat(filepath.Join(wtDir, ".mcp.json")); err != nil {
		t.Error("expected .mcp.json to survive sync")
	}

	// junk.txt should be removed.
	if _, err := os.Stat(filepath.Join(wtDir, "junk.txt")); !os.IsNotExist(err) {
		t.Error("expected junk.txt to be removed by sync")
	}

	// railyard.yaml symlink should exist in worktree.
	linkTarget, err := os.Readlink(filepath.Join(wtDir, "railyard.yaml"))
	if err != nil {
		t.Fatalf("expected railyard.yaml symlink: %v", err)
	}
	if linkTarget != filepath.Join(dir, "railyard.yaml") {
		t.Errorf("symlink target = %q, want %q", linkTarget, filepath.Join(dir, "railyard.yaml"))
	}
}

// --- EnsureRailyardIgnore tests ---

func TestEnsureRailyardIgnore_AddsMissingEntries(t *testing.T) {
	dir := t.TempDir()
	// Initialize a git repo so git rev-parse works.
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%v failed: %s: %v", args, out, err)
		}
	}
	run("git", "init")

	if err := EnsureRailyardIgnore(dir); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	excludePath := filepath.Join(dir, ".git", "info", "exclude")
	data, err := os.ReadFile(excludePath)
	if err != nil {
		t.Fatalf("read exclude: %v", err)
	}
	content := string(data)

	for _, entry := range railyardIgnoreEntries {
		if !strings.Contains(content, entry) {
			t.Errorf("exclude missing entry %q", entry)
		}
	}
}

func TestEnsureRailyardIgnore_Idempotent(t *testing.T) {
	dir := t.TempDir()
	cmd := exec.Command("git", "init")
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init: %s: %v", out, err)
	}

	// Run twice.
	if err := EnsureRailyardIgnore(dir); err != nil {
		t.Fatalf("first call: %v", err)
	}
	if err := EnsureRailyardIgnore(dir); err != nil {
		t.Fatalf("second call: %v", err)
	}

	excludePath := filepath.Join(dir, ".git", "info", "exclude")
	data, err := os.ReadFile(excludePath)
	if err != nil {
		t.Fatalf("read exclude: %v", err)
	}

	// Each entry should appear exactly once.
	for _, entry := range railyardIgnoreEntries {
		count := 0
		for _, line := range strings.Split(string(data), "\n") {
			if strings.TrimSpace(line) == entry {
				count++
			}
		}
		if count != 1 {
			t.Errorf("entry %q appears %d times, want 1", entry, count)
		}
	}
}

func TestEnsureRailyardIgnore_PreservesExisting(t *testing.T) {
	dir := t.TempDir()
	cmd := exec.Command("git", "init")
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init: %s: %v", out, err)
	}

	// Write existing entries to the exclude file.
	excludePath := filepath.Join(dir, ".git", "info", "exclude")
	os.MkdirAll(filepath.Dir(excludePath), 0755)
	existing := "node_modules/\n.mcp.json\n"
	if err := os.WriteFile(excludePath, []byte(existing), 0644); err != nil {
		t.Fatal(err)
	}

	if err := EnsureRailyardIgnore(dir); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	data, err := os.ReadFile(excludePath)
	if err != nil {
		t.Fatalf("read exclude: %v", err)
	}
	content := string(data)

	// .mcp.json was already present — should not be duplicated.
	count := 0
	for _, line := range strings.Split(content, "\n") {
		if strings.TrimSpace(line) == ".mcp.json" {
			count++
		}
	}
	if count != 1 {
		t.Errorf(".mcp.json appears %d times, want 1", count)
	}
	// node_modules/ should still be there.
	if !strings.Contains(content, "node_modules/") {
		t.Error("existing entry node_modules/ was lost")
	}
	// Other railyard entries should be added.
	if !strings.Contains(content, ".claude") {
		t.Error("missing .claude entry")
	}
}
