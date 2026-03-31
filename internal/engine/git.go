package engine

import (
	"fmt"
	"log"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// cleanExcludes lists untracked files that git clean should preserve in worktrees.
var cleanExcludes = []string{".claudeignore", ".mcp.json"}

// gitCleanArgs returns the arguments for git clean that exclude railyard files.
func gitCleanArgs() []string {
	args := []string{"clean", "-fd"}
	for _, e := range cleanExcludes {
		args = append(args, "-e", e)
	}
	return args
}

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
	if err := os.WriteFile(filepath.Join(wtDir, ".claudeignore"), []byte(ignoreContent), 0644); err != nil {
		log.Printf("write .claudeignore in %s: %v", wtDir, err)
	}
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

// ResetWorktree resets an engine's worktree to a clean state at origin/{baseBranch}
// (or local {baseBranch} if no remote). This must be called before CreateBranch when
// starting a new car to avoid merge conflicts from stale state.
// If baseBranch is empty, defaults to "main".
func ResetWorktree(wtDir, baseBranch string) error {
	if wtDir == "" {
		return fmt.Errorf("engine: worktree directory is required")
	}
	if baseBranch == "" {
		baseBranch = "main"
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

	// Step 3: Remove untracked files and directories (preserving railyard files).
	clean := exec.Command("git", gitCleanArgs()...)
	clean.Dir = wtDir
	if out, err := clean.CombinedOutput(); err != nil {
		return fmt.Errorf("engine: git clean: %s: %w", strings.TrimSpace(string(out)), err)
	}

	// Step 4: Hard reset to origin/{baseBranch} (fall back to local {baseBranch}).
	target := "origin/" + baseBranch
	checkRef := exec.Command("git", "rev-parse", "--verify", target)
	checkRef.Dir = wtDir
	if _, err := checkRef.CombinedOutput(); err != nil {
		target = baseBranch
	}

	reset := exec.Command("git", "reset", "--hard", target)
	reset.Dir = wtDir
	if out, err := reset.CombinedOutput(); err != nil {
		return fmt.Errorf("engine: reset to %s: %s: %w", target, strings.TrimSpace(string(out)), err)
	}

	writeClaudeIgnore(wtDir)
	return nil
}

// CreateBranch creates a new git branch from baseBranch (when non-empty), or
// from HEAD when baseBranch is empty. Checks out an existing branch if it
// already exists.
// The repoDir parameter specifies the git repository working directory.
func CreateBranch(repoDir, branchName, baseBranch string) error {
	if branchName == "" {
		return fmt.Errorf("engine: branch name is required")
	}
	if repoDir == "" {
		return fmt.Errorf("engine: repo directory is required")
	}
	args := []string{"checkout", "-b", branchName}
	if baseBranch != "" {
		args = append(args, baseBranch)
	}
	cmd := exec.Command("git", args...)
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

// CommitsAheadOfBase returns the number of commits on HEAD that are not in baseBranch.
// It tries origin/{baseBranch} first, falling back to local {baseBranch}.
func CommitsAheadOfBase(repoDir, baseBranch string) (int, error) {
	if repoDir == "" {
		return 0, fmt.Errorf("engine: repo directory is required")
	}
	if baseBranch == "" {
		baseBranch = "main"
	}

	// Prefer origin ref for accuracy.
	target := "origin/" + baseBranch
	check := exec.Command("git", "rev-parse", "--verify", target)
	check.Dir = repoDir
	if _, err := check.CombinedOutput(); err != nil {
		target = baseBranch
	}

	cmd := exec.Command("git", "rev-list", "--count", target+"..HEAD")
	cmd.Dir = repoDir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return 0, fmt.Errorf("engine: rev-list count: %s", strings.TrimSpace(string(out)))
	}

	var count int
	if _, err := fmt.Sscanf(strings.TrimSpace(string(out)), "%d", &count); err != nil {
		return 0, fmt.Errorf("engine: parse rev-list count: %w", err)
	}
	return count, nil
}

// AutoCommitIfDirty stages all changes (including untracked files) and commits
// them with the given message. Returns true if a commit was created, false if
// the worktree was clean. This is a safety net for preserving work when an
// engine session exits without committing.
func AutoCommitIfDirty(repoDir, message string) (bool, error) {
	if repoDir == "" {
		return false, fmt.Errorf("engine: repo directory is required")
	}

	// Check for any changes (staged, unstaged, or untracked).
	status := exec.Command("git", "status", "--porcelain")
	status.Dir = repoDir
	out, err := status.CombinedOutput()
	if err != nil {
		return false, fmt.Errorf("engine: git status: %s", strings.TrimSpace(string(out)))
	}
	if strings.TrimSpace(string(out)) == "" {
		return false, nil // clean worktree
	}

	// Stage everything.
	add := exec.Command("git", "add", "-A")
	add.Dir = repoDir
	if addOut, addErr := add.CombinedOutput(); addErr != nil {
		return false, fmt.Errorf("engine: git add: %s", strings.TrimSpace(string(addOut)))
	}

	// Commit.
	if message == "" {
		message = "railyard: auto-commit uncommitted work"
	}
	commit := exec.Command("git", "commit", "-m", message)
	commit.Dir = repoDir
	if commitOut, commitErr := commit.CombinedOutput(); commitErr != nil {
		// "nothing to commit" is not an error — race with clean check.
		if strings.Contains(string(commitOut), "nothing to commit") {
			return false, nil
		}
		return false, fmt.Errorf("engine: git commit: %s", strings.TrimSpace(string(commitOut)))
	}
	return true, nil
}

// EnsureDispatchWorktree creates a persistent git worktree at .railyard/dispatch/
// for the dispatcher agent. Returns the absolute path to the worktree directory.
// If the worktree already exists, it is reused.
func EnsureDispatchWorktree(repoDir string) (string, error) {
	if repoDir == "" {
		return "", fmt.Errorf("engine: repo directory is required")
	}

	wtDir := filepath.Join(repoDir, ".railyard", "dispatch")

	// Reuse existing worktree.
	if _, err := os.Stat(wtDir); err == nil {
		writeClaudeIgnore(wtDir)
		symlinkRailyardYaml(repoDir, wtDir)
		return wtDir, nil
	}

	if err := os.MkdirAll(filepath.Join(repoDir, ".railyard"), 0755); err != nil {
		return "", fmt.Errorf("engine: create .railyard dir: %w", err)
	}

	cmd := exec.Command("git", "worktree", "add", "--detach", wtDir)
	cmd.Dir = repoDir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("engine: create dispatch worktree: %s", strings.TrimSpace(string(out)))
	}

	writeClaudeIgnore(wtDir)
	symlinkRailyardYaml(repoDir, wtDir)
	return wtDir, nil
}

// RemoveDispatchWorktree removes the dispatcher's git worktree.
func RemoveDispatchWorktree(repoDir string) error {
	wtPath := filepath.Join(".railyard", "dispatch")
	cmd := exec.Command("git", "worktree", "remove", "--force", wtPath)
	cmd.Dir = repoDir
	out, err := cmd.CombinedOutput()
	if err != nil {
		if strings.Contains(string(out), "is not a working tree") {
			return nil
		}
		return fmt.Errorf("engine: remove dispatch worktree: %s", strings.TrimSpace(string(out)))
	}
	return nil
}

// EnsureYardmasterWorktree creates a persistent git worktree at .railyard/yardmaster/
// for the yardmaster agent. Returns the absolute path to the worktree directory.
func EnsureYardmasterWorktree(repoDir string) (string, error) {
	if repoDir == "" {
		return "", fmt.Errorf("engine: repo directory is required")
	}

	wtDir := filepath.Join(repoDir, ".railyard", "yardmaster")

	// Reuse existing worktree.
	if _, err := os.Stat(wtDir); err == nil {
		return wtDir, nil
	}

	if err := os.MkdirAll(filepath.Join(repoDir, ".railyard"), 0755); err != nil {
		return "", fmt.Errorf("engine: create .railyard dir: %w", err)
	}

	cmd := exec.Command("git", "worktree", "add", "--detach", wtDir)
	cmd.Dir = repoDir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("engine: create yardmaster worktree: %s", strings.TrimSpace(string(out)))
	}

	return wtDir, nil
}

// RemoveYardmasterWorktree removes the yardmaster's git worktree.
func RemoveYardmasterWorktree(repoDir string) error {
	wtPath := filepath.Join(".railyard", "yardmaster")
	cmd := exec.Command("git", "worktree", "remove", "--force", wtPath)
	cmd.Dir = repoDir
	out, err := cmd.CombinedOutput()
	if err != nil {
		if strings.Contains(string(out), "is not a working tree") {
			return nil
		}
		return fmt.Errorf("engine: remove yardmaster worktree: %s", strings.TrimSpace(string(out)))
	}
	return nil
}

// SyncWorktreeToBranch resets a worktree to match a given branch.
// Fetches first, then resets to origin/{branch} (falls back to local {branch}).
// repoDir is the main repository root, used to re-create the railyard.yaml symlink.
func SyncWorktreeToBranch(wtDir, branch, repoDir string) error {
	if wtDir == "" {
		return fmt.Errorf("engine: worktree directory is required")
	}
	if branch == "" {
		branch = "main"
	}

	// Fetch latest.
	fetch := exec.Command("git", "fetch", "origin")
	fetch.Dir = wtDir
	fetch.CombinedOutput() // ignore error — no remote is fine

	// Detach HEAD first.
	detach := exec.Command("git", "checkout", "--detach", "HEAD")
	detach.Dir = wtDir
	detach.CombinedOutput() // ignore error

	// Clean untracked files (preserving railyard files).
	clean := exec.Command("git", gitCleanArgs()...)
	clean.Dir = wtDir
	clean.CombinedOutput() // ignore error

	// Reset to origin/{branch}, fall back to local branch.
	target := "origin/" + branch
	checkRef := exec.Command("git", "rev-parse", "--verify", target)
	checkRef.Dir = wtDir
	if _, err := checkRef.CombinedOutput(); err != nil {
		target = branch
	}

	reset := exec.Command("git", "reset", "--hard", target)
	reset.Dir = wtDir
	if out, err := reset.CombinedOutput(); err != nil {
		return fmt.Errorf("engine: sync worktree to %s: %s: %w", target, strings.TrimSpace(string(out)), err)
	}

	writeClaudeIgnore(wtDir)
	if repoDir != "" {
		symlinkRailyardYaml(repoDir, wtDir)
	}
	return nil
}

// RemoteBranchExists returns true if the given branch exists on origin.
// It runs git fetch first to ensure refs are up to date.
func RemoteBranchExists(wtDir, branch string) bool {
	fetch := exec.Command("git", "fetch", "origin")
	fetch.Dir = wtDir
	if _, err := fetch.CombinedOutput(); err != nil {
		return false
	}
	check := exec.Command("git", "ls-remote", "--exit-code", "--heads", "origin", branch)
	check.Dir = wtDir
	return check.Run() == nil
}

// CheckoutExistingBranch fetches origin and checks out an existing remote branch.
// Used for revision cars that already have a pushed branch with prior work.
func CheckoutExistingBranch(wtDir, branch string) error {
	if wtDir == "" {
		return fmt.Errorf("engine: worktree directory is required")
	}
	if branch == "" {
		return fmt.Errorf("engine: branch name is required")
	}

	// Fetch to get latest remote refs.
	fetch := exec.Command("git", "fetch", "origin")
	fetch.Dir = wtDir
	if out, err := fetch.CombinedOutput(); err != nil {
		return fmt.Errorf("engine: fetch origin: %s", strings.TrimSpace(string(out)))
	}

	// Verify the branch still exists on the remote.
	check := exec.Command("git", "ls-remote", "--exit-code", "--heads", "origin", branch)
	check.Dir = wtDir
	if err := check.Run(); err != nil {
		return fmt.Errorf("engine: remote branch %q no longer exists on origin", branch)
	}

	// Try checking out the local branch if it exists.
	checkout := exec.Command("git", "checkout", branch)
	checkout.Dir = wtDir
	if _, err := checkout.CombinedOutput(); err != nil {
		// Branch doesn't exist locally — create from remote tracking branch.
		checkoutRemote := exec.Command("git", "checkout", "-b", branch, "origin/"+branch)
		checkoutRemote.Dir = wtDir
		if rOut, rErr := checkoutRemote.CombinedOutput(); rErr != nil {
			return fmt.Errorf("engine: checkout existing branch %q: %s", branch, strings.TrimSpace(string(rOut)))
		}
	}

	// Pull latest changes on the branch.
	pull := exec.Command("git", "pull", "--ff-only", "origin", branch)
	pull.Dir = wtDir
	pull.CombinedOutput() // Non-fatal — branch may already be up to date.

	return nil
}

// railyardIgnoreEntries are paths that railyard generates at runtime and must
// not be committed by AutoCommitIfDirty. EnsureRailyardIgnore adds any missing
// entries to .gitignore in the given directory.
var railyardIgnoreEntries = []string{
	".mcp.json",
	".claude",
	".railyard/",
	".claudeignore",
	".beads/",
}

// EnsureRailyardIgnore ensures that railyard runtime files are listed in the
// repo's .gitignore so AutoCommitIfDirty does not commit them. Idempotent —
// only appends entries that are missing. Creates .gitignore if it doesn't exist.
func EnsureRailyardIgnore(repoDir string) error {
	gitignorePath := filepath.Join(repoDir, ".gitignore")

	// Read existing entries.
	existing := make(map[string]bool)
	if data, err := os.ReadFile(gitignorePath); err == nil {
		for _, line := range strings.Split(string(data), "\n") {
			trimmed := strings.TrimSpace(line)
			if trimmed != "" && !strings.HasPrefix(trimmed, "#") {
				existing[trimmed] = true
			}
		}
	}

	// Collect missing entries.
	var missing []string
	for _, entry := range railyardIgnoreEntries {
		if !existing[entry] {
			missing = append(missing, entry)
		}
	}

	if len(missing) == 0 {
		return nil
	}

	// Append missing entries.
	f, err := os.OpenFile(gitignorePath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return fmt.Errorf("engine: open .gitignore: %w", err)
	}
	defer f.Close()

	block := "\n# Railyard runtime (auto-added)\n"
	for _, entry := range missing {
		block += entry + "\n"
	}
	if _, err := f.WriteString(block); err != nil {
		return fmt.Errorf("engine: write .gitignore: %w", err)
	}

	slog.Info("engine: added missing entries to .gitignore", "count", len(missing), "entries", missing)
	return nil
}

// symlinkRailyardYaml creates a symlink to railyard.yaml in the worktree
// so the dispatcher can find the config.
func symlinkRailyardYaml(repoDir, wtDir string) {
	linkPath := filepath.Join(wtDir, "railyard.yaml")
	target := filepath.Join(repoDir, "railyard.yaml")

	// Remove stale symlink if present.
	os.Remove(linkPath)

	// Only create if source exists.
	if _, err := os.Stat(target); err == nil {
		os.Symlink(target, linkPath)
	}
}
