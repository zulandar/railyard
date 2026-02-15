package engine

import (
	"fmt"
	"os/exec"
	"strings"
	"time"
)

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
