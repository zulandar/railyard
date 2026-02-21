package engine

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// DetectBaseBranch returns the base branch to use for new cars.
// Fallback chain:
//  1. git symbolic-ref --short HEAD on repoDir (current branch)
//  2. defaultBranch parameter (from config) if non-empty
//  3. origin/HEAD target (remote default branch)
//  4. "main" as final fallback
func DetectBaseBranch(repoDir, defaultBranch string) string {
	// Step 1: current branch via symbolic-ref.
	if repoDir != "" {
		cmd := exec.Command("git", "symbolic-ref", "--short", "HEAD")
		cmd.Dir = repoDir
		if out, err := cmd.CombinedOutput(); err == nil {
			if branch := strings.TrimSpace(string(out)); branch != "" {
				return branch
			}
		}
	}

	// Step 2: explicit config default.
	if defaultBranch != "" {
		return defaultBranch
	}

	// Step 3: remote default branch via origin/HEAD.
	if repoDir != "" {
		cmd := exec.Command("git", "symbolic-ref", "refs/remotes/origin/HEAD")
		cmd.Dir = repoDir
		if out, err := cmd.CombinedOutput(); err == nil {
			// Output is like "refs/remotes/origin/main" — extract the branch name.
			ref := strings.TrimSpace(string(out))
			if i := strings.LastIndex(ref, "/"); i >= 0 {
				if branch := ref[i+1:]; branch != "" {
					return branch
				}
			}
		}
	}

	// Step 4: final fallback.
	return "main"
}

// EnsureWorktree creates a git worktree at .railyard/engines/<engineID> if it doesn't exist.
// Returns the absolute path to the worktree directory.
func EnsureWorktree(repoDir, engineID string) (string, error) {
	wtDir := filepath.Join(repoDir, ".railyard", "engines", engineID)

	// If worktree directory already exists (stale from crash), reuse it.
	if _, err := os.Stat(wtDir); err == nil {
		writeClaudeIgnore(wtDir)
		return wtDir, nil
	}

	if err := os.MkdirAll(filepath.Join(repoDir, ".railyard", "engines"), 0755); err != nil {
		return "", fmt.Errorf("engine: create engines dir: %w", err)
	}

	cmd := exec.Command("git", "worktree", "add", "--detach", wtDir)
	cmd.Dir = repoDir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("engine: create worktree %q: %s", engineID, strings.TrimSpace(string(out)))
	}

	writeClaudeIgnore(wtDir)
	return wtDir, nil
}

// writeClaudeIgnore writes a .claudeignore file to the worktree so the
// Claude Code agent doesn't see Railyard orchestration files (config,
// beads, other engine worktrees) that could confuse it during work.
func writeClaudeIgnore(wtDir string) {
	const ignoreContent = `# Railyard orchestration files — not part of the project
railyard.yaml
.beads/
.railyard/
`
	os.WriteFile(filepath.Join(wtDir, ".claudeignore"), []byte(ignoreContent), 0644)
}

// RemoveWorktree removes an engine's git worktree.
func RemoveWorktree(repoDir, engineID string) error {
	wtPath := filepath.Join(".railyard", "engines", engineID)
	cmd := exec.Command("git", "worktree", "remove", "--force", wtPath)
	cmd.Dir = repoDir
	out, err := cmd.CombinedOutput()
	if err != nil {
		// Non-fatal if worktree doesn't exist.
		if strings.Contains(string(out), "is not a working tree") {
			return nil
		}
		return fmt.Errorf("engine: remove worktree %q: %s", engineID, strings.TrimSpace(string(out)))
	}
	return nil
}

// CleanupWorktrees removes all engine worktrees and prunes stale entries.
func CleanupWorktrees(repoDir string) error {
	enginesDir := filepath.Join(repoDir, ".railyard", "engines")
	entries, err := os.ReadDir(enginesDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("engine: read engines dir: %w", err)
	}

	for _, e := range entries {
		if e.IsDir() && strings.HasPrefix(e.Name(), "eng-") {
			_ = RemoveWorktree(repoDir, e.Name())
		}
	}

	// Prune orphaned worktree entries.
	cmd := exec.Command("git", "worktree", "prune")
	cmd.Dir = repoDir
	cmd.CombinedOutput() //nolint:errcheck

	return nil
}

// ResetWorktree resets an engine's worktree to a clean state at origin/main
// (or local main if no remote). This must be called before CreateBranch when
// starting a new car to avoid merge conflicts from stale state.
func ResetWorktree(wtDir string) error {
	if wtDir == "" {
		return fmt.Errorf("engine: worktree directory is required")
	}

	// Step 1: Fetch origin to get latest refs. Non-fatal if no remote.
	fetch := exec.Command("git", "fetch", "origin")
	fetch.Dir = wtDir
	fetch.CombinedOutput() // ignore error — local-only repos have no remote

	// Step 2: Detach HEAD so we're not on any branch.
	detach := exec.Command("git", "checkout", "--detach", "HEAD")
	detach.Dir = wtDir
	if out, err := detach.CombinedOutput(); err != nil {
		return fmt.Errorf("engine: detach HEAD: %s: %w", strings.TrimSpace(string(out)), err)
	}

	// Step 3: Remove untracked files and directories.
	clean := exec.Command("git", "clean", "-fd")
	clean.Dir = wtDir
	if out, err := clean.CombinedOutput(); err != nil {
		return fmt.Errorf("engine: git clean: %s: %w", strings.TrimSpace(string(out)), err)
	}

	// Step 4: Hard reset to origin/main (fall back to local main).
	target := "origin/main"
	checkRef := exec.Command("git", "rev-parse", "--verify", "origin/main")
	checkRef.Dir = wtDir
	if _, err := checkRef.CombinedOutput(); err != nil {
		target = "main"
	}

	reset := exec.Command("git", "reset", "--hard", target)
	reset.Dir = wtDir
	if out, err := reset.CombinedOutput(); err != nil {
		return fmt.Errorf("engine: reset to %s: %s: %w", target, strings.TrimSpace(string(out)), err)
	}

	return nil
}

// CreateBranch creates a new git branch from main, or checks out an existing one.
// The repoDir parameter specifies the git repository working directory.
func CreateBranch(repoDir, branchName string) error {
	if branchName == "" {
		return fmt.Errorf("engine: branch name is required")
	}
	if repoDir == "" {
		return fmt.Errorf("engine: repo directory is required")
	}

	// Try to create a new branch from main.
	cmd := exec.Command("git", "checkout", "-b", branchName, "main")
	cmd.Dir = repoDir
	out, err := cmd.CombinedOutput()
	if err == nil {
		return nil
	}

	// If branch already exists, check it out instead.
	if strings.Contains(string(out), "already exists") {
		checkout := exec.Command("git", "checkout", branchName)
		checkout.Dir = repoDir
		if checkoutOut, checkoutErr := checkout.CombinedOutput(); checkoutErr != nil {
			return fmt.Errorf("engine: checkout existing branch %q: %s", branchName, strings.TrimSpace(string(checkoutOut)))
		}
		return nil
	}

	return fmt.Errorf("engine: create branch %q: %s", branchName, strings.TrimSpace(string(out)))
}

// PushBranch pushes a branch to origin, retrying once on failure.
func PushBranch(repoDir, branchName string) error {
	if branchName == "" {
		return fmt.Errorf("engine: branch name is required")
	}
	if repoDir == "" {
		return fmt.Errorf("engine: repo directory is required")
	}

	var lastErr error
	for attempt := range 2 {
		cmd := exec.Command("git", "push", "origin", branchName)
		cmd.Dir = repoDir
		out, err := cmd.CombinedOutput()
		if err == nil {
			return nil
		}
		lastErr = fmt.Errorf("engine: push branch %q (attempt %d): %s", branchName, attempt+1, strings.TrimSpace(string(out)))

		if attempt == 0 {
			time.Sleep(time.Second)
		}
	}
	return lastErr
}

// RecentCommits returns the last n commits on the given branch as one-line strings.
func RecentCommits(repoDir, branchName string, n int) ([]string, error) {
	if branchName == "" {
		return nil, fmt.Errorf("engine: branch name is required")
	}
	if repoDir == "" {
		return nil, fmt.Errorf("engine: repo directory is required")
	}
	if n <= 0 {
		return nil, nil
	}

	cmd := exec.Command("git", "log", "--oneline", fmt.Sprintf("-%d", n), branchName)
	cmd.Dir = repoDir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("engine: recent commits on %q: %s", branchName, strings.TrimSpace(string(out)))
	}

	raw := strings.TrimSpace(string(out))
	if raw == "" {
		return nil, nil
	}
	return strings.Split(raw, "\n"), nil
}

// ChangedFiles returns the list of files with uncommitted changes (staged + unstaged).
func ChangedFiles(repoDir string) ([]string, error) {
	if repoDir == "" {
		return nil, fmt.Errorf("engine: repo directory is required")
	}

	cmd := exec.Command("git", "diff", "--name-only", "HEAD")
	cmd.Dir = repoDir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("engine: changed files: %s", strings.TrimSpace(string(out)))
	}

	raw := strings.TrimSpace(string(out))
	if raw == "" {
		return nil, nil
	}
	return strings.Split(raw, "\n"), nil
}
