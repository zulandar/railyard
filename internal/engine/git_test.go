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
	for _, want := range []string{"railyard.yaml", ".beads/", "engines/"} {
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

// --- CreateBranch tests ---

func TestCreateBranch(t *testing.T) {
	dir := initTestRepo(t)

	if err := CreateBranch(dir, "ry/alice/backend/car-abc12"); err != nil {
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
	if err := CreateBranch(dir, "ry/test/branch"); err != nil {
		t.Fatalf("first CreateBranch: %v", err)
	}

	// Switch back to main.
	cmd := exec.Command("git", "checkout", "main")
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("checkout main: %s\n%s", err, out)
	}

	// Create again — should checkout existing.
	if err := CreateBranch(dir, "ry/test/branch"); err != nil {
		t.Fatalf("second CreateBranch: %v", err)
	}

	got := currentBranch(t, dir)
	if got != "ry/test/branch" {
		t.Errorf("branch = %q, want %q", got, "ry/test/branch")
	}
}

func TestCreateBranch_EmptyName(t *testing.T) {
	err := CreateBranch("/tmp", "")
	if err == nil {
		t.Fatal("expected error for empty branch name")
	}
	if !strings.Contains(err.Error(), "branch name is required") {
		t.Errorf("error = %q, want to contain %q", err.Error(), "branch name is required")
	}
}

func TestCreateBranch_EmptyRepoDir(t *testing.T) {
	err := CreateBranch("", "some-branch")
	if err == nil {
		t.Fatal("expected error for empty repo dir")
	}
	if !strings.Contains(err.Error(), "repo directory is required") {
		t.Errorf("error = %q, want to contain %q", err.Error(), "repo directory is required")
	}
}

func TestCreateBranch_BadDir(t *testing.T) {
	err := CreateBranch("/nonexistent/path", "some-branch")
	if err == nil {
		t.Fatal("expected error for bad directory")
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
	if err := CreateBranch(dir, "feature-branch"); err != nil {
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
