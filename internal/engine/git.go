package engine

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// EnsureWorktree creates a git worktree at engines/<engineID> if it doesn't exist.
// Returns the absolute path to the worktree directory.
func EnsureWorktree(repoDir, engineID string) (string, error) {
	wtDir := filepath.Join(repoDir, "engines", engineID)

	// If worktree directory already exists (stale from crash), reuse it.
	if _, err := os.Stat(wtDir); err == nil {
		return wtDir, nil
	}

	if err := os.MkdirAll(filepath.Join(repoDir, "engines"), 0755); err != nil {
		return "", fmt.Errorf("engine: create engines dir: %w", err)
	}

	cmd := exec.Command("git", "worktree", "add", "--detach", wtDir)
	cmd.Dir = repoDir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("engine: create worktree %q: %s", engineID, strings.TrimSpace(string(out)))
	}

	return wtDir, nil
}

// RemoveWorktree removes an engine's git worktree.
func RemoveWorktree(repoDir, engineID string) error {
	wtPath := filepath.Join("engines", engineID)
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
	enginesDir := filepath.Join(repoDir, "engines")
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
