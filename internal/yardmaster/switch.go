package yardmaster

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/zulandar/railyard/internal/messaging"
	"github.com/zulandar/railyard/internal/models"
	"gorm.io/gorm"
)

// SwitchOpts holds parameters for the switch (merge) operation.
type SwitchOpts struct {
	RepoDir          string // working directory (yardmaster worktree when running via daemon)
	PrimaryRepoDir   string // primary repo directory (for engine worktree detachment; empty = use RepoDir)
	BaseBranch       string // target branch for merge (default "main"); used for worktree-safe operations
	DryRun           bool   // run tests but don't merge
	PreTestCommand   string // command to run before tests (e.g. "go mod vendor", "npm install")
	TestCommand      string // per-track test command (e.g. "go test ./...", "phpunit", "npm test")
	RequirePR        bool   // create a draft PR instead of direct merge
	SwitchTimeoutSec int    // max seconds for runTests (default 600 if 0)
}

// SwitchFailureCategory categorizes Switch errors so the daemon can track and
// escalate repeated failures by type.
type SwitchFailureCategory string

const (
	SwitchFailNone    SwitchFailureCategory = ""
	SwitchFailFetch   SwitchFailureCategory = "fetch-failed"
	SwitchFailPreTest SwitchFailureCategory = "pre-test-failed"
	SwitchFailTest    SwitchFailureCategory = "test-failed"
	SwitchFailInfra   SwitchFailureCategory = "infra-failed"
	SwitchFailMerge   SwitchFailureCategory = "merge-conflict"
	SwitchFailPush    SwitchFailureCategory = "push-failed"
	SwitchFailPR      SwitchFailureCategory = "pr-failed"
)

// SwitchResult contains the outcome of a switch operation.
type SwitchResult struct {
	CarID           string
	Branch          string
	TestsPassed     bool
	TestOutput      string
	Merged          bool
	AlreadyMerged   bool // true when the branch was already an ancestor of main
	PRCreated       bool
	PRUrl           string
	FailureCategory SwitchFailureCategory // set on error for categorized escalation
	ConflictDetails string                // conflict file list + diff context for escalation
	Error           error
}

// Switch performs the branch merge flow for a completed car:
// 1. Fetch the branch
// 2. Run the track's test suite
// 3. If tests pass and not dry-run: merge to main
// 4. If tests fail: set car status to blocked, notify engine
func Switch(db *gorm.DB, carID string, opts SwitchOpts) (*SwitchResult, error) {
	if db == nil {
		return nil, fmt.Errorf("yardmaster: db is required")
	}
	if carID == "" {
		return nil, fmt.Errorf("yardmaster: carID is required")
	}
	if opts.RepoDir == "" {
		return nil, fmt.Errorf("yardmaster: repoDir is required")
	}

	// Load the car.
	var car models.Car
	if err := db.First(&car, "id = ?", carID).Error; err != nil {
		return nil, fmt.Errorf("yardmaster: load car %s: %w", carID, err)
	}

	if car.Branch == "" {
		return nil, fmt.Errorf("yardmaster: car %s has no branch", carID)
	}

	baseBranch := opts.BaseBranch
	if baseBranch == "" {
		baseBranch = "main"
	}

	result := &SwitchResult{
		CarID:  carID,
		Branch: car.Branch,
	}

	// Fetch the branch.
	if err := gitFetch(opts.RepoDir); err != nil {
		result.FailureCategory = SwitchFailFetch
		result.Error = fmt.Errorf("fetch: %w", err)
		return result, result.Error
	}

	// Detach the engine worktree so the branch can be checked out.
	// Engine worktrees live under the primary repo, not the yardmaster worktree.
	if car.Assignee != "" {
		detachDir := opts.PrimaryRepoDir
		if detachDir == "" {
			detachDir = opts.RepoDir
		}
		detachEngineWorktree(detachDir, car.Assignee)
	}

	// Run tests on the branch (unless skip_tests is set on the car).
	if car.SkipTests {
		result.TestsPassed = true
		result.TestOutput = "tests skipped (skip_tests=true on car)"
	} else {
		timeoutSec := opts.SwitchTimeoutSec
		if timeoutSec == 0 {
			timeoutSec = 600
		}
		ctx, cancel := context.WithTimeout(context.Background(), time.Duration(timeoutSec)*time.Second)
		defer cancel()

		testOutput, testErr := runTests(ctx, opts.RepoDir, car.Branch, baseBranch, opts.PreTestCommand, opts.TestCommand)
		result.TestOutput = testOutput

		if testErr != nil {
			result.TestsPassed = false

			if strings.Contains(testErr.Error(), "pre-test command failed") {
				result.FailureCategory = SwitchFailPreTest
			} else {
				result.FailureCategory = classifyTestFailure(testErr, testOutput)
			}

			if result.FailureCategory == SwitchFailInfra {
				// Infrastructure failure — set merge-failed, escalate to human.
				if dbErr := db.Model(&models.Car{}).Where("id = ?", carID).Update("status", "merge-failed").Error; dbErr != nil {
					log.Printf("update car %s to merge-failed: %v", carID, dbErr)
				}
				messaging.Send(db, "yardmaster", "human", "infra-test-failure",
					fmt.Sprintf("Infrastructure test failure for car %s (%s) on branch %s:\n%s",
						carID, car.Track, car.Branch, truncateOutput(testOutput, 500)),
					messaging.SendOpts{CarID: carID, Priority: "urgent"},
				)
			} else {
				// Code test failure — set blocked, notify engine.
				if dbErr := db.Model(&models.Car{}).Where("id = ?", carID).Update("status", "blocked").Error; dbErr != nil {
					log.Printf("update car %s to blocked: %v", carID, dbErr)
				}
				if car.Assignee != "" {
					messaging.Send(db, "yardmaster", car.Assignee, "test-failure",
						fmt.Sprintf("Tests failed for car %s on branch %s:\n%s", carID, car.Branch, testOutput),
						messaging.SendOpts{CarID: carID, Priority: "urgent"},
					)
				}
			}

			result.Error = fmt.Errorf("tests failed: %w", testErr)
			return result, nil // return result without error — test failure is a normal outcome
		}

		result.TestsPassed = true
	}

	if opts.DryRun {
		return result, nil
	}

	// If the branch is already an ancestor of main (e.g. a dependent car's
	// merge already included this branch's commits), skip the merge.
	if isAncestor(opts.RepoDir, car.Branch, baseBranch) {
		deleteRemoteBranch(opts.RepoDir, car.Branch)
		result.Merged = true
		result.AlreadyMerged = true

		now := time.Now()
		if dbErr := db.Model(&models.Car{}).Where("id = ?", carID).Updates(map[string]interface{}{
			"status":       "merged",
			"completed_at": now,
		}).Error; dbErr != nil {
			log.Printf("update car %s to merged (already-ancestor): %v", carID, dbErr)
		}

		// Run the same post-merge logic as a normal merge.
		unblocked, ubErr := UnblockDeps(db, carID)
		if ubErr != nil {
			log.Printf("unblock deps for %s (already-ancestor): %v", carID, ubErr)
		}
		if len(unblocked) > 0 {
			titles := make([]string, len(unblocked))
			for i, b := range unblocked {
				titles[i] = b.ID
			}
			messaging.Send(db, "yardmaster", "broadcast", "deps-unblocked",
				fmt.Sprintf("Merged %s (already ancestor), unblocked: %s", carID, strings.Join(titles, ", ")),
				messaging.SendOpts{CarID: carID},
			)
			for _, u := range unblocked {
				if u.Type == "epic" {
					TryCloseEpic(db, u.ID)
				}
			}
		}

		if car.ParentID != nil && *car.ParentID != "" {
			TryCloseEpic(db, *car.ParentID)
		}

		return result, nil
	}

	if opts.RequirePR {
		// Push the branch to origin so a PR can reference it.
		if err := gitPushBranch(opts.RepoDir, car.Branch); err != nil {
			result.FailureCategory = SwitchFailPush
			result.Error = fmt.Errorf("push branch: %w", err)
			return result, result.Error
		}

		// Build the PR body from the car record and progress notes.
		prBody := buildPRBody(db, &car, opts.RepoDir, baseBranch)

		prURL, err := createDraftPR(opts.RepoDir, car.Title, prBody, car.Branch)
		if err != nil {
			result.FailureCategory = SwitchFailPR
			result.Error = fmt.Errorf("create PR: %w", err)
			return result, result.Error
		}

		result.PRCreated = true
		result.PRUrl = prURL

		// Mark car as pr_open — not merged yet, waiting for human review.
		now := time.Now()
		if dbErr := db.Model(&models.Car{}).Where("id = ?", carID).Updates(map[string]interface{}{
			"status":       "pr_open",
			"completed_at": now,
		}).Error; dbErr != nil {
			log.Printf("update car %s to pr_open: %v", carID, dbErr)
		}

		return result, nil
	}

	// Save pre-merge HEAD so we can undo the merge if push fails.
	preMergeHead := getHeadCommit(opts.RepoDir)

	// Merge to the base branch.
	if err := gitMerge(opts.RepoDir, car.Branch, baseBranch); err != nil {
		// Attempt conflict resolution: abort failed merge, rebase branch, retry.
		resolved, resolveErr := tryResolveConflict(opts.RepoDir, car.Branch, baseBranch)
		if !resolved {
			result.FailureCategory = SwitchFailMerge
			if resolveErr != nil {
				result.ConflictDetails = resolveErr.Error()
			}
			result.Error = fmt.Errorf("merge: %w", err)
			return result, result.Error
		}
		// Rebase succeeded — retry the merge (should be clean now).
		if retryErr := gitMerge(opts.RepoDir, car.Branch, baseBranch); retryErr != nil {
			result.FailureCategory = SwitchFailMerge
			// Capture conflict details from the failed retry merge.
			conflictFiles := getConflictFiles(opts.RepoDir)
			if len(conflictFiles) > 0 {
				result.ConflictDetails = getConflictContext(opts.RepoDir, conflictFiles)
			}
			gitMergeAbort(opts.RepoDir)
			result.Error = fmt.Errorf("merge after rebase: %w", retryErr)
			return result, result.Error
		}
	}

	// Push to remote before marking merged — the car should only be
	// considered merged once the code is confirmed on the remote.
	if err := gitPush(opts.RepoDir, baseBranch); err != nil {
		// Undo the local merge so the car will be retried next cycle.
		gitResetToCommit(opts.RepoDir, preMergeHead)
		result.FailureCategory = SwitchFailPush
		result.Error = fmt.Errorf("push after merge: %w", err)
		return result, result.Error
	}

	// Update local tracking ref so the next sibling merge starts from the
	// correct commit. Without this, origin/<baseBranch> remains stale and
	// sibling merges see add/add conflicts.
	gitFetchBranch(opts.RepoDir, baseBranch)

	deleteRemoteBranch(opts.RepoDir, car.Branch)

	result.Merged = true

	// Mark car as merged — push succeeded, safe to update status.
	now := time.Now()
	if dbErr := db.Model(&models.Car{}).Where("id = ?", carID).Updates(map[string]interface{}{
		"status":       "merged",
		"completed_at": now,
	}).Error; dbErr != nil {
		log.Printf("update car %s to merged: %v", carID, dbErr)
	}

	// Unblock cross-track dependencies.
	unblocked, ubErr := UnblockDeps(db, carID)
	if ubErr != nil {
		log.Printf("unblock deps for %s: %v", carID, ubErr)
	}
	if len(unblocked) > 0 {
		titles := make([]string, len(unblocked))
		for i, b := range unblocked {
			titles[i] = b.ID
		}
		messaging.Send(db, "yardmaster", "broadcast", "deps-unblocked",
			fmt.Sprintf("Merged %s, unblocked: %s", carID, strings.Join(titles, ", ")),
			messaging.SendOpts{CarID: carID},
		)
		// Auto-close any unblocked epics whose children are all complete.
		for _, u := range unblocked {
			if u.Type == "epic" {
				TryCloseEpic(db, u.ID)
			}
		}
	}

	// Auto-close parent epic if all children are done.
	if car.ParentID != nil && *car.ParentID != "" {
		TryCloseEpic(db, *car.ParentID)
	}

	return result, nil
}

// UnblockDeps finds cars that were blocked by the given car and transitions
// them from blocked to open. Returns the list of unblocked cars.
func UnblockDeps(db *gorm.DB, carID string) ([]models.Car, error) {
	if db == nil {
		return nil, fmt.Errorf("yardmaster: db is required")
	}
	if carID == "" {
		return nil, fmt.Errorf("yardmaster: carID is required")
	}

	// Find cars that depend on this car.
	var deps []models.CarDep
	if err := db.Where("blocked_by = ?", carID).Find(&deps).Error; err != nil {
		return nil, fmt.Errorf("yardmaster: find deps for %s: %w", carID, err)
	}

	var unblocked []models.Car
	for _, dep := range deps {
		// Check if this dependent has any OTHER unresolved blockers.
		var otherBlockers int64
		if err := db.Model(&models.CarDep{}).
			Where("car_id = ? AND blocked_by != ?", dep.CarID, carID).
			Joins("JOIN cars ON cars.id = car_deps.blocked_by").
			Where("cars.status NOT IN ?", models.ResolvedBlockerStatuses).
			Count(&otherBlockers).Error; err != nil {
			return nil, fmt.Errorf("yardmaster: count other blockers for %s: %w", dep.CarID, err)
		}

		if otherBlockers == 0 {
			// No other blockers — unblock this car (only if it's actually blocked).
			result := db.Model(&models.Car{}).Where("id = ? AND status = ?", dep.CarID, "blocked").
				Update("status", "open")

			if result.RowsAffected > 0 {
				var b models.Car
				if err := db.First(&b, "id = ?", dep.CarID).Error; err == nil {
					unblocked = append(unblocked, b)
				}
			}
		}
	}

	return unblocked, nil
}

// gitFetch runs git fetch in the repo directory.
func gitFetch(repoDir string) error {
	cmd := exec.Command("git", "fetch", "--all")
	cmd.Dir = repoDir
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git fetch: %s: %w", string(out), err)
	}
	return nil
}

// noTestPatterns are stdout patterns that indicate "no tests exist" rather
// than a real test failure. When the test command exits non-zero but its
// output matches one of these, we treat it as a pass.
var noTestPatterns = []string{
	"no test files",
	"No tests found",
	"No test suites found",
}

// infraPatterns are output patterns that indicate infrastructure failures
// rather than code test assertion failures.
var infraPatterns = []string{
	"command not found",
	"permission denied",
	"no such file or directory",
	"no configuration file",
	"service not running",
	"cannot connect",
	"connection refused",
	"econnrefused",
	"docker: error",
	"cannot connect to the docker daemon",
	"is the docker daemon running",
	"docker compose",
	"exec format error",
	"not installed",
	"module not found",
	"no such module",
	"already checked out",
}

// classifyTestFailure distinguishes infrastructure failures from code test
// failures by inspecting the error and output. Exit codes 126 (permission
// denied), 127 (command not found), and 128 (git fatal error) are always
// infrastructure. Otherwise the output is pattern-matched against known
// infrastructure signatures.
func classifyTestFailure(err error, output string) SwitchFailureCategory {
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		code := exitErr.ExitCode()
		if code == 127 || code == 126 || code == 128 {
			return SwitchFailInfra
		}
	}

	lower := strings.ToLower(output)
	for _, pat := range infraPatterns {
		if strings.Contains(lower, pat) {
			return SwitchFailInfra
		}
	}

	return SwitchFailTest
}

// truncateOutput returns at most maxLen bytes of output, appending a
// truncation notice if the output was trimmed.
func truncateOutput(output string, maxLen int) string {
	if len(output) <= maxLen {
		return output
	}
	return output[:maxLen] + "\n... (truncated)"
}

// runTests checks out the branch and runs the test suite.
// baseBranch is the branch to return to after tests (e.g. "main").
// The provided ctx controls the overall timeout for pre-test and test commands.
func runTests(ctx context.Context, repoDir, branch, baseBranch, preTestCommand, testCommand string) (string, error) {
	// Checkout the branch (worktree-safe: fall back to detached HEAD).
	checkout := exec.Command("git", "checkout", branch)
	checkout.Dir = repoDir
	if out, err := checkout.CombinedOutput(); err != nil {
		// Fallback: detach at origin/<branch> (handles worktree collision).
		detach := exec.Command("git", "checkout", "--detach", "origin/"+branch)
		detach.Dir = repoDir
		if dOut, dErr := detach.CombinedOutput(); dErr != nil {
			// Last resort: detach at local branch ref.
			last := exec.Command("git", "checkout", "--detach", branch)
			last.Dir = repoDir
			if lOut, lErr := last.CombinedOutput(); lErr != nil {
				return string(out) + "\n" + string(dOut) + "\n" + string(lOut),
					fmt.Errorf("git checkout %s: %w", branch, err)
			}
		}
	}

	// Run pre-test command if configured (e.g. "go mod vendor", "npm install").
	if preTestCommand != "" {
		preCmd := exec.CommandContext(ctx, "sh", "-c", preTestCommand)
		preCmd.Dir = repoDir
		if out, err := preCmd.CombinedOutput(); err != nil {
			checkoutBase(repoDir, baseBranch)
			if ctx.Err() == context.DeadlineExceeded {
				dl, _ := ctx.Deadline()
				timeout := time.Until(dl) + time.Since(dl) // reconstruct original timeout
				_ = timeout
				return string(out), fmt.Errorf("switch timeout exceeded during pre-test command")
			}
			return string(out), fmt.Errorf("pre-test command failed: %w", err)
		}
	}

	// Run the track's configured test command, defaulting to "go test ./...".
	if testCommand == "" {
		testCommand = "go test ./..."
	}
	testCmd := exec.CommandContext(ctx, "sh", "-c", testCommand)
	testCmd.Dir = repoDir

	out, err := testCmd.CombinedOutput()
	output := string(out)

	// Return to base branch regardless.
	checkoutBase(repoDir, baseBranch)

	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return output, fmt.Errorf("switch timeout exceeded")
		}
		// Check for "no tests" patterns — treat as pass.
		for _, pat := range noTestPatterns {
			if strings.Contains(output, pat) {
				return output, nil
			}
		}
		return output, fmt.Errorf("tests failed: %w", err)
	}

	return output, nil
}

// deleteRemoteBranch deletes a branch from the remote. Non-fatal — logs warning on failure.
func deleteRemoteBranch(repoDir, branch string) {
	cmd := exec.Command("git", "push", "origin", "--delete", branch)
	cmd.Dir = repoDir
	if out, err := cmd.CombinedOutput(); err != nil {
		log.Printf("yardmaster: delete remote branch %s: %s: %v (non-fatal)", branch, string(out), err)
	}
}

// checkoutBase switches back to the base branch after test/merge operations.
// In a worktree, "git checkout main" may fail because main is checked out
// in the primary repo. Falls back to detaching HEAD at origin/{baseBranch}.
func checkoutBase(repoDir, baseBranch string) {
	cmd := exec.Command("git", "checkout", baseBranch)
	cmd.Dir = repoDir
	if _, err := cmd.CombinedOutput(); err == nil {
		return
	}
	// Fallback: detach at origin/{baseBranch} (worktree-safe).
	detach := exec.Command("git", "checkout", "--detach", "origin/"+baseBranch)
	detach.Dir = repoDir
	if _, err := detach.CombinedOutput(); err != nil {
		// Last resort: detach at local {baseBranch} ref.
		last := exec.Command("git", "checkout", "--detach", baseBranch)
		last.Dir = repoDir
		last.CombinedOutput()
	}
}

// isAncestor returns true if the given branch is already fully contained
// in the base branch (i.e., all its commits are reachable from baseBranch).
// This happens when a dependent car's merge already included this branch's changes.
func isAncestor(repoDir, branch, baseBranch string) bool {
	cmd := exec.Command("git", "merge-base", "--is-ancestor", branch, baseBranch)
	cmd.Dir = repoDir
	return cmd.Run() == nil
}

// gitMerge merges the branch into the base branch.
// Uses checkoutBase which handles worktree mode (detached HEAD fallback).
func gitMerge(repoDir, branch, baseBranch string) error {
	// Checkout the base branch (worktree-safe).
	checkoutBase(repoDir, baseBranch)

	// Verify we're at the right commit (either on baseBranch or detached at it).
	// Merge the branch with co-author trailer for Railyard attribution.
	msg := fmt.Sprintf("Switch: merge %s to %s\n\nCo-Authored-By: Railyard Yardmaster <railyard-yardmaster@noreply>", branch, baseBranch)
	merge := exec.Command("git", "merge", "--no-ff", branch, "-m", msg)
	merge.Dir = repoDir
	if out, err := merge.CombinedOutput(); err != nil {
		return fmt.Errorf("git merge %s: %s: %w", branch, string(out), err)
	}

	return nil
}

// gitResetToCommit resets the current branch to the given commit hash.
// This is used to undo a local merge when the subsequent push fails.
func gitResetToCommit(repoDir, commitHash string) {
	cmd := exec.Command("git", "reset", "--hard", commitHash)
	cmd.Dir = repoDir
	cmd.CombinedOutput() // best-effort — error logged by caller
}

// gitMergeAbort aborts a failed merge, returning the repo to pre-merge state.
func gitMergeAbort(repoDir string) {
	cmd := exec.Command("git", "merge", "--abort")
	cmd.Dir = repoDir
	cmd.CombinedOutput() // best-effort
}

// gitRebaseAbort aborts a failed rebase, returning the repo to pre-rebase state.
func gitRebaseAbort(repoDir string) {
	cmd := exec.Command("git", "rebase", "--abort")
	cmd.Dir = repoDir
	cmd.CombinedOutput() // best-effort
}

// getConflictFiles returns the list of files with unresolved conflicts.
// Uses git ls-files --unmerged which works during both merge and rebase conflicts.
func getConflictFiles(repoDir string) []string {
	cmd := exec.Command("git", "ls-files", "--unmerged")
	cmd.Dir = repoDir
	out, err := cmd.Output()
	if err != nil {
		return nil
	}
	// ls-files --unmerged outputs lines like: "<mode> <hash> <stage>\t<path>"
	// Multiple lines per file (one per stage). Deduplicate by path.
	seen := make(map[string]bool)
	var files []string
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		// Split on tab — path is after the tab.
		parts := strings.SplitN(line, "\t", 2)
		if len(parts) < 2 {
			continue
		}
		path := strings.TrimSpace(parts[1])
		if path != "" && !seen[path] {
			seen[path] = true
			files = append(files, path)
		}
	}
	return files
}

// getConflictContext builds a human-readable summary of the conflict for
// escalation. Includes the file list and abbreviated conflict markers.
func getConflictContext(repoDir string, files []string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Conflicting files (%d):\n", len(files))
	for _, f := range files {
		fmt.Fprintf(&b, "  - %s\n", f)
	}
	// Show abbreviated conflict diff for each file (first 30 lines).
	for _, f := range files {
		cmd := exec.Command("git", "diff", "--", f)
		cmd.Dir = repoDir
		out, err := cmd.Output()
		if err != nil {
			continue
		}
		diff := string(out)
		lines := strings.Split(diff, "\n")
		if len(lines) > 30 {
			lines = append(lines[:30], "... (truncated)")
		}
		fmt.Fprintf(&b, "\n--- %s ---\n%s\n", f, strings.Join(lines, "\n"))
	}
	return b.String()
}

// gitRebaseBranch checks out the given branch and rebases it onto baseBranch.
// Returns nil on clean rebase. On conflict, returns an error (the rebase is
// left in progress so the caller can inspect/resolve conflicts).
func gitRebaseBranch(repoDir, branch, baseBranch string) error {
	// Checkout the branch.
	checkout := exec.Command("git", "checkout", branch)
	checkout.Dir = repoDir
	if out, err := checkout.CombinedOutput(); err != nil {
		return fmt.Errorf("checkout %s: %s: %w", branch, string(out), err)
	}

	// Rebase onto the base branch.
	rebase := exec.Command("git", "rebase", baseBranch)
	rebase.Dir = repoDir
	if out, err := rebase.CombinedOutput(); err != nil {
		return fmt.Errorf("rebase %s onto %s: %s: %w", branch, baseBranch, string(out), err)
	}

	return nil
}

// isOnlyGoModConflict returns true if the only conflicted files are go.mod
// and/or go.sum.
func isOnlyGoModConflict(files []string) bool {
	if len(files) == 0 {
		return false
	}
	for _, f := range files {
		if f != "go.mod" && f != "go.sum" {
			return false
		}
	}
	return true
}

// resolveGoModConflict auto-resolves go.mod/go.sum conflicts during a rebase
// by taking the base branch's version and running `go mod tidy` to regenerate
// from actual imports. Assumes rebase is in progress with go.mod/go.sum conflicts.
func resolveGoModConflict(repoDir string) error {
	// Take the upstream (base branch) version of go.mod and go.sum.
	for _, f := range []string{"go.mod", "go.sum"} {
		path := filepath.Join(repoDir, f)
		if _, err := os.Stat(path); err != nil {
			continue // file doesn't exist in this conflict
		}
		checkout := exec.Command("git", "checkout", "--theirs", f)
		checkout.Dir = repoDir
		if out, err := checkout.CombinedOutput(); err != nil {
			return fmt.Errorf("checkout --theirs %s: %s: %w", f, string(out), err)
		}
	}

	// Run go mod tidy to regenerate from actual imports.
	tidy := exec.Command("go", "mod", "tidy")
	tidy.Dir = repoDir
	if out, err := tidy.CombinedOutput(); err != nil {
		return fmt.Errorf("go mod tidy: %s: %w", string(out), err)
	}

	// Stage the resolved files.
	add := exec.Command("git", "add", "go.mod", "go.sum")
	add.Dir = repoDir
	if out, err := add.CombinedOutput(); err != nil {
		return fmt.Errorf("git add go.mod go.sum: %s: %w", string(out), err)
	}

	// Continue the rebase.
	cont := exec.Command("git", "-c", "core.editor=true", "rebase", "--continue")
	cont.Dir = repoDir
	if out, err := cont.CombinedOutput(); err != nil {
		return fmt.Errorf("rebase --continue: %s: %w", string(out), err)
	}

	return nil
}

// tryResolveConflict attempts to resolve a merge conflict by rebasing the
// car branch onto the current base branch. On success, returns (true, nil)
// and the caller can retry gitMerge(). On failure, aborts the rebase and
// returns (false, error) with conflict context for escalation.
func tryResolveConflict(repoDir, branch, baseBranch string) (bool, error) {
	// Abort the failed merge first.
	gitMergeAbort(repoDir)

	// Attempt to rebase the branch onto current base.
	if err := gitRebaseBranch(repoDir, branch, baseBranch); err != nil {
		// Rebase conflicted. Check what files are in conflict.
		conflictFiles := getConflictFiles(repoDir)

		if isOnlyGoModConflict(conflictFiles) {
			// Try go.mod/go.sum auto-resolution.
			if resolveErr := resolveGoModConflict(repoDir); resolveErr == nil {
				// Return to base branch for the merge.
				checkoutBase(repoDir, baseBranch)
				return true, nil
			}
			// go mod tidy failed — fall through to abort.
		}

		// Collect conflict context before aborting.
		ctx := getConflictContext(repoDir, conflictFiles)
		gitRebaseAbort(repoDir)

		// Return to base branch.
		checkoutBase(repoDir, baseBranch)

		return false, fmt.Errorf("unresolvable conflict:\n%s", ctx)
	}

	// Clean rebase — return to base branch for the merge.
	checkoutBase(repoDir, baseBranch)
	return true, nil
}

// gitPush pushes the current HEAD to the base branch on the remote.
// Uses HEAD:{baseBranch} refspec which works in both normal repos and worktrees.
func gitPush(repoDir, baseBranch string) error {
	cmd := exec.Command("git", "push", "origin", "HEAD:"+baseBranch)
	cmd.Dir = repoDir
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git push: %s: %w", string(out), err)
	}
	return nil
}

// gitFetchBranch fetches a single branch from origin to update the local
// tracking ref. This is called after push to ensure origin/<branch> reflects
// the just-pushed commit.
func gitFetchBranch(repoDir, branch string) {
	cmd := exec.Command("git", "fetch", "origin", branch)
	cmd.Dir = repoDir
	cmd.CombinedOutput() // best-effort — push already succeeded
}

// detachEngineWorktree detaches HEAD in the engine's worktree so the branch
// can be checked out elsewhere. This is a best-effort operation — if the
// worktree doesn't exist or is already detached, the error is silently ignored.
func detachEngineWorktree(repoDir, engineID string) {
	wtDir := filepath.Join(repoDir, ".railyard", "engines", engineID)
	if _, err := os.Stat(wtDir); err != nil {
		return // worktree directory doesn't exist
	}
	cmd := exec.Command("git", "checkout", "--detach", "HEAD")
	cmd.Dir = wtDir
	cmd.CombinedOutput() // ignore errors — already detached, empty repo, etc.
}

// TryCloseEpic checks if all children of an epic are done/cancelled and, if so,
// marks the epic as done. This is called after each successful merge to handle
// automatic epic completion.
func TryCloseEpic(db *gorm.DB, epicID string) {
	if db == nil || epicID == "" {
		return
	}

	var epic models.Car
	if err := db.First(&epic, "id = ?", epicID).Error; err != nil {
		return
	}
	if epic.Type != "epic" {
		return
	}

	// Count children that are NOT done, merged, or cancelled.
	var remaining int64
	if err := db.Model(&models.Car{}).
		Where("parent_id = ? AND status NOT IN ?", epicID, []string{"done", "merged", "cancelled"}).
		Count(&remaining).Error; err != nil {
		log.Printf("TryCloseEpic: count remaining children for %s: %v", epicID, err)
		return
	}

	if remaining > 0 {
		return
	}

	// All children are done/cancelled — close the epic.
	now := time.Now()
	if err := db.Model(&models.Car{}).Where("id = ?", epicID).Updates(map[string]interface{}{
		"status":       "done",
		"completed_at": now,
	}).Error; err != nil {
		log.Printf("TryCloseEpic: update epic %s to done: %v", epicID, err)
		return
	}

	messaging.Send(db, "yardmaster", "broadcast", "epic-closed",
		fmt.Sprintf("Epic %s (%s) auto-closed — all children complete", epicID, epic.Title),
		messaging.SendOpts{CarID: epicID},
	)

	// Recurse: if the epic itself has a parent epic, check that too.
	if epic.ParentID != nil && *epic.ParentID != "" {
		TryCloseEpic(db, *epic.ParentID)
	}
}

// gitPushBranch pushes a specific branch to the remote.
func gitPushBranch(repoDir, branch string) error {
	cmd := exec.Command("git", "push", "-u", "origin", branch)
	cmd.Dir = repoDir
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git push %s: %s: %w", branch, string(out), err)
	}
	return nil
}

// buildPRBody assembles a rich PR description from the car record and progress notes.
func buildPRBody(db *gorm.DB, car *models.Car, repoDir, baseBranch string) string {
	var b strings.Builder

	// Summary.
	b.WriteString("## Summary\n")
	if car.Description != "" {
		b.WriteString(car.Description)
	} else {
		b.WriteString(car.Title)
	}
	b.WriteString("\n\n")

	// Acceptance Criteria.
	if car.Acceptance != "" {
		b.WriteString("## Acceptance Criteria\n")
		b.WriteString(car.Acceptance)
		b.WriteString("\n\n")
	}

	// Design Notes.
	if car.DesignNotes != "" {
		b.WriteString("## Design Notes\n")
		b.WriteString(car.DesignNotes)
		b.WriteString("\n\n")
	}

	// What Changed — git diff --stat.
	diffStat := gitDiffStat(repoDir, car.Branch, baseBranch)
	if diffStat != "" {
		b.WriteString("## What Changed\n```\n")
		b.WriteString(diffStat)
		b.WriteString("```\n\n")
	}

	// Progress Notes.
	var progress []models.CarProgress
	if db != nil {
		db.Where("car_id = ?", car.ID).Order("created_at ASC").Find(&progress)
	}
	if len(progress) > 0 {
		b.WriteString("## Progress\n")
		for _, p := range progress {
			eng := p.EngineID
			if eng == "" {
				eng = p.SessionID
			}
			b.WriteString(fmt.Sprintf("- [%s] %s\n", eng, p.Note))
		}
		b.WriteString("\n")
	}

	// Metadata footer.
	b.WriteString("---\n")
	b.WriteString(fmt.Sprintf("Car: %s | Track: %s | Priority: P%d", car.ID, car.Track, car.Priority))
	if car.Assignee != "" {
		b.WriteString(fmt.Sprintf(" | Engine: %s", car.Assignee))
	}
	b.WriteString(fmt.Sprintf(" | Branch: %s\n", car.Branch))

	return b.String()
}

// gitDiffStat returns the diff --stat between the base branch and the given branch.
func gitDiffStat(repoDir, branch, baseBranch string) string {
	cmd := exec.Command("git", "diff", "--stat", baseBranch+"..."+branch)
	cmd.Dir = repoDir
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// createDraftPR creates a draft pull request using the gh CLI and returns the PR URL.
func createDraftPR(repoDir, title, body, branch string) (string, error) {
	cmd := exec.Command("gh", "pr", "create",
		"--draft",
		"--title", title,
		"--body", body,
		"--head", branch,
	)
	cmd.Dir = repoDir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("gh pr create: %s: %w", string(out), err)
	}
	return strings.TrimSpace(string(out)), nil
}
