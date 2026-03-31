package yardmaster

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/zulandar/railyard/internal/messaging"
	"github.com/zulandar/railyard/internal/models"
	"gorm.io/gorm"
)

// gitMu serialises all git operations in the switch/merge flow so that
// concurrent daemon goroutines (e.g. escalation + merge) cannot corrupt
// the yardmaster worktree.
var gitMu sync.Mutex

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
	CommentCounter   func(branch string) (int, error) // nil-safe; returns non-author inline comment count for pr_open snapshot
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

	// Serialize git operations to prevent worktree corruption.
	gitMu.Lock()
	defer gitMu.Unlock()

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

	slog.Info("Switch: starting merge pipeline",
		"car", carID,
		"branch", car.Branch,
		"base_branch", baseBranch,
		"assignee", car.Assignee,
		"skip_tests", car.SkipTests,
	)

	// Fetch the branch.
	if err := gitFetch(opts.RepoDir); err != nil {
		result.FailureCategory = SwitchFailFetch
		result.Error = fmt.Errorf("fetch: %w", err)
		return result, result.Error
	}

	slog.Debug("Switch: fetch complete", "car", carID)

	// Detach the engine worktree so the branch can be checked out.
	// Engine worktrees live under the primary repo, not the yardmaster worktree.
	if car.Assignee != "" {
		detachDir := opts.PrimaryRepoDir
		if detachDir == "" {
			detachDir = opts.RepoDir
		}
		detachEngineWorktree(detachDir, car.Assignee)
		slog.Debug("Switch: engine worktree detached", "car", carID, "assignee", car.Assignee)
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

		slog.Info("Switch: running tests",
			"car", carID,
			"branch", car.Branch,
			"test_command", opts.TestCommand,
			"pre_test_command", opts.PreTestCommand,
			"timeout_sec", timeoutSec,
		)

		testOutput, testErr := runTests(ctx, opts.RepoDir, car.Branch, baseBranch, opts.PreTestCommand, opts.TestCommand)
		result.TestOutput = testOutput

		if testErr != nil {
			result.TestsPassed = false

			if strings.Contains(testErr.Error(), "pre-test command failed") {
				result.FailureCategory = SwitchFailPreTest
			} else {
				result.FailureCategory = classifyTestFailure(testErr, testOutput)
			}

			slog.Warn("Switch: tests failed",
				"car", carID,
				"category", result.FailureCategory,
				"error", testErr,
			)

			if result.FailureCategory == SwitchFailInfra {
				// Infrastructure failure — set merge-failed, escalate to human.
				if dbErr := db.Model(&models.Car{}).Where("id = ?", carID).Updates(map[string]interface{}{
					"status":         "merge-failed",
					"blocked_reason": "",
				}).Error; dbErr != nil {
					slog.Error("update car to merge-failed", "car", carID, "error", dbErr)
				}
				messaging.Send(db, "yardmaster", "human", "infra-test-failure",
					fmt.Sprintf("Infrastructure test failure for car %s (%s) on branch %s:\n%s",
						carID, car.Track, car.Branch, truncateOutput(testOutput, 500)),
					messaging.SendOpts{CarID: carID, Priority: "urgent"},
				)
			} else {
				// Code test failure — set blocked, notify engine.
				if dbErr := db.Model(&models.Car{}).Where("id = ?", carID).Updates(map[string]interface{}{
					"status":         "blocked",
					"blocked_reason": models.BlockedReasonTestFailed,
				}).Error; dbErr != nil {
					slog.Error("update car to blocked", "car", carID, "error", dbErr)
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
		slog.Info("Switch: tests passed", "car", carID)
	}

	if opts.DryRun {
		return result, nil
	}

	// If the branch has no unique diff vs main (e.g. a dependent car's merge
	// already included this branch's commits), skip the merge.
	if isBranchMerged(opts.RepoDir, car.Branch, baseBranch) {
		slog.Info("Switch: branch already merged (ancestor of base)",
			"car", carID,
			"branch", car.Branch,
			"base_branch", baseBranch,
		)
		deleteRemoteBranch(opts.RepoDir, car.Branch)
		result.Merged = true
		result.AlreadyMerged = true

		now := time.Now()
		if dbErr := db.Model(&models.Car{}).Where("id = ?", carID).Updates(map[string]interface{}{
			"status":       "merged",
			"completed_at": now,
		}).Error; dbErr != nil {
			slog.Error("update car to merged (already-ancestor)", "car", carID, "error", dbErr)
		}

		// Run the same post-merge logic as a normal merge.
		unblocked, ubErr := UnblockDeps(db, carID)
		if ubErr != nil {
			slog.Error("unblock deps (already-ancestor)", "car", carID, "error", ubErr)
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

	slog.Debug("Switch: branch has unique commits, proceeding to merge/PR", "car", carID, "require_pr", opts.RequirePR)

	if opts.RequirePR {
		// Push the branch to origin so a PR can reference it.
		if err := gitPushBranch(opts.RepoDir, car.Branch); err != nil {
			result.FailureCategory = SwitchFailPush
			result.Error = fmt.Errorf("push branch: %w", err)
			return result, result.Error
		}

		// Check if a PR already exists for this branch (rework cycle).
		existingURL, existsErr := getExistingPR(opts.RepoDir, car.Branch)

		var prURL string
		if existsErr == nil && existingURL != "" {
			// PR exists — update body with latest progress notes.
			prBody := buildPRBody(db, &car, opts.RepoDir, baseBranch)
			if updErr := updatePRBody(opts.RepoDir, car.Branch, prBody); updErr != nil {
				slog.Warn("Update PR body failed", "car", carID, "error", updErr)
			}
			prURL = existingURL
			slog.Info("Switch: existing PR updated with new commits",
				"car", carID, "branch", car.Branch, "pr_url", prURL)
		} else {
			// No existing PR — create a new draft.
			prBody := buildPRBody(db, &car, opts.RepoDir, baseBranch)
			var createErr error
			prURL, createErr = createDraftPR(opts.RepoDir, car.Title, prBody, car.Branch)
			if createErr != nil {
				result.FailureCategory = SwitchFailPR
				result.Error = fmt.Errorf("create PR: %w", createErr)
				return result, result.Error
			}
			slog.Info("Switch: draft PR created",
				"car", carID, "branch", car.Branch, "pr_url", prURL)
		}

		result.PRCreated = true
		result.PRUrl = prURL

		// Snapshot current inline comment count for feedback detection.
		commentCount := 0
		if opts.CommentCounter != nil {
			if cnt, cntErr := opts.CommentCounter(car.Branch); cntErr == nil {
				commentCount = cnt
			} else {
				slog.Warn("Count comments for snapshot", "car", carID, "error", cntErr)
			}
		}

		// Mark car as pr_open — not merged yet, waiting for human review.
		now := time.Now()
		if dbErr := db.Model(&models.Car{}).Where("id = ?", carID).Updates(map[string]interface{}{
			"status":                "pr_open",
			"completed_at":          now,
			"last_pr_comment_count": commentCount,
		}).Error; dbErr != nil {
			slog.Error("update car to pr_open", "car", carID, "error", dbErr)
		}

		return result, nil
	}

	// Save pre-merge HEAD so we can undo the merge if push fails.
	preMergeHead := getHeadCommit(opts.RepoDir)

	// Merge to the base branch.
	slog.Debug("Switch: attempting merge", "car", carID, "branch", car.Branch, "base_branch", baseBranch)
	if err := gitMerge(opts.RepoDir, car.Branch, baseBranch); err != nil {
		// Attempt conflict resolution: abort failed merge, rebase branch, retry.
		resolved, resolveErr := tryResolveConflict(opts.RepoDir, car.Branch, baseBranch)
		slog.Debug("Switch: conflict resolution attempted", "car", carID, "resolved", resolved)
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
	slog.Debug("Switch: pushing merge to remote", "car", carID, "base_branch", baseBranch)
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
	slog.Debug("Switch: feature branch deleted from remote", "car", carID, "branch", car.Branch)

	result.Merged = true
	slog.Info("Switch: merged and pushed",
		"car", carID,
		"branch", car.Branch,
		"base_branch", baseBranch,
	)

	// Mark car as merged — push succeeded, safe to update status.
	now := time.Now()
	if dbErr := db.Model(&models.Car{}).Where("id = ?", carID).Updates(map[string]interface{}{
		"status":       "merged",
		"completed_at": now,
	}).Error; dbErr != nil {
		slog.Error("update car to merged", "car", carID, "error", dbErr)
	}

	// Unblock cross-track dependencies.
	unblocked, ubErr := UnblockDeps(db, carID)
	if ubErr != nil {
		slog.Error("unblock deps", "car", carID, "error", ubErr)
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

		slog.Debug("UnblockDeps: evaluated dependency",
			"car", dep.CarID,
			"resolved_dep", carID,
			"other_blockers", otherBlockers,
		)

		if otherBlockers == 0 {
			// Load the car to check BlockedReason before deciding target status.
			var b models.Car
			if err := db.First(&b, "id = ?", dep.CarID).Error; err != nil {
				continue
			}
			if b.Status != "blocked" {
				continue
			}

			// Test-failure blocks transition to "done" so the merge pipeline
			// retries (the dependency that caused the failure is now merged).
			// All other blocks transition to "open" for fresh engine work.
			targetStatus := "open"
			if b.BlockedReason == models.BlockedReasonTestFailed {
				targetStatus = "done"
			}

			slog.Info("UnblockDeps: transitioning car",
				"car", dep.CarID,
				"dependency", carID,
				"blocked_reason", b.BlockedReason,
				"from", "blocked",
				"to", targetStatus,
			)

			result := db.Model(&models.Car{}).Where("id = ? AND status = ?", dep.CarID, "blocked").
				Updates(map[string]interface{}{
					"status":         targetStatus,
					"blocked_reason": "",
				})

			if result.RowsAffected > 0 {
				b.Status = targetStatus
				b.BlockedReason = ""
				unblocked = append(unblocked, b)
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
	// Discard any uncommitted changes before switching branches.
	gitCleanWorkingTree(repoDir)
	slog.Debug("runTests: cleaned working tree", "branch", branch)

	// Checkout the branch (worktree-safe: fall back to detached HEAD).
	checkoutMethod := "direct"
	checkout := exec.Command("git", "checkout", branch)
	checkout.Dir = repoDir
	if out, err := checkout.CombinedOutput(); err != nil {
		// Fallback: detach at origin/<branch> (handles worktree collision).
		checkoutMethod = "detached-origin"
		detach := exec.Command("git", "checkout", "--detach", "origin/"+branch)
		detach.Dir = repoDir
		if dOut, dErr := detach.CombinedOutput(); dErr != nil {
			// Last resort: detach at local branch ref.
			checkoutMethod = "detached-local"
			last := exec.Command("git", "checkout", "--detach", branch)
			last.Dir = repoDir
			if lOut, lErr := last.CombinedOutput(); lErr != nil {
				return string(out) + "\n" + string(dOut) + "\n" + string(lOut),
					fmt.Errorf("git checkout %s: %w", branch, err)
			}
		}
	}
	slog.Debug("runTests: checked out branch", "branch", branch, "method", checkoutMethod)

	// Run pre-test command if configured (e.g. "go mod vendor", "npm install").
	if preTestCommand != "" {
		slog.Debug("runTests: running pre-test command", "command", preTestCommand)
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
		slog.Debug("runTests: pre-test command succeeded")
	}

	// Run the track's configured test command.
	if testCommand == "" {
		slog.Warn("no test_command configured for track; skipping tests")
		checkoutBase(repoDir, baseBranch)
		return "", nil
	}
	slog.Debug("runTests: executing test command", "command", testCommand)
	testCmd := exec.CommandContext(ctx, "sh", "-c", testCommand)
	testCmd.Dir = repoDir

	out, err := testCmd.CombinedOutput()
	output := string(out)

	// Return to base branch regardless.
	checkoutBase(repoDir, baseBranch)
	slog.Debug("runTests: returned to base branch", "base_branch", baseBranch)

	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return output, fmt.Errorf("switch timeout exceeded")
		}
		// Check for "no tests" patterns — treat as pass.
		for _, pat := range noTestPatterns {
			if strings.Contains(output, pat) {
				slog.Debug("runTests: no-test pattern matched, treating as pass", "pattern", pat)
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
		slog.Warn("delete remote branch failed (non-fatal)", "branch", branch, "output", string(out), "error", err)
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

// gitCleanWorkingTree discards all uncommitted changes and removes untracked
// files and directories. This prevents stale modifications or generated files
// (e.g. from test runs) from blocking subsequent git checkout or merge operations.
func gitCleanWorkingTree(repoDir string) {
	// Reset tracked files to HEAD.
	reset := exec.Command("git", "checkout", "--", ".")
	reset.Dir = repoDir
	reset.CombinedOutput() // best-effort

	// Remove untracked files and directories.
	clean := exec.Command("git", "clean", "-fd")
	clean.Dir = repoDir
	clean.CombinedOutput() // best-effort
}

// isBranchMerged returns true if the branch has no unique changes relative to
// baseBranch. This replaces a plain is-ancestor check which had a false-positive:
// a branch freshly created from main (with no commits) would satisfy is-ancestor
// immediately and be auto-closed even though its intended changes never landed.
//
// The check uses "git diff <baseBranch>...<branch>" (three-dot diff) which shows
// only the changes introduced on branch since it diverged from baseBranch. An
// empty diff means every change on the branch is already present in baseBranch —
// either because a dependent car's merge pulled them in, or because the branch
// was truly merged.
func isBranchMerged(repoDir, branch, baseBranch string) bool {
	cmd := exec.Command("git", "diff", "--quiet", baseBranch+"..."+branch)
	cmd.Dir = repoDir
	return cmd.Run() == nil
}

// gitMerge merges the branch into the base branch.
// Uses checkoutBase which handles worktree mode (detached HEAD fallback).
func gitMerge(repoDir, branch, baseBranch string) error {
	// Discard any uncommitted changes left by tests or prior operations.
	// The yardmaster repo should always have a clean working tree before merge.
	gitCleanWorkingTree(repoDir)

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

// getRemoteHeadCommit returns the commit SHA of origin/<baseBranch>.
// Returns empty string on error (branch doesn't exist, no remote, etc).
func getRemoteHeadCommit(repoDir, baseBranch string) string {
	cmd := exec.Command("git", "rev-parse", "origin/"+baseBranch)
	cmd.Dir = repoDir
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// gitForcePushBranch force-pushes a branch to origin using --force-with-lease
// to avoid overwriting unexpected remote changes.
func gitForcePushBranch(repoDir, branch string) error {
	cmd := exec.Command("git", "push", "--force-with-lease", "origin", branch)
	cmd.Dir = repoDir
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("force push %s: %s: %w", branch, string(out), err)
	}
	return nil
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
		slog.Error("TryCloseEpic: count remaining children", "epic", epicID, "error", err)
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
		slog.Error("TryCloseEpic: update epic to done", "epic", epicID, "error", err)
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

// getExistingPR checks if a PR already exists for the given branch and returns its URL.
func getExistingPR(repoDir, branch string) (string, error) {
	cmd := exec.Command("gh", "pr", "view", branch, "--json", "url", "-q", ".url")
	cmd.Dir = repoDir
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("gh pr view %s: %w", branch, err)
	}
	return strings.TrimSpace(string(out)), nil
}

// updatePRBody updates the body of an existing PR for the given branch.
func updatePRBody(repoDir, branch, body string) error {
	cmd := exec.Command("gh", "pr", "edit", branch, "--body", body)
	cmd.Dir = repoDir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("gh pr edit %s: %s: %w", branch, string(out), err)
	}
	return nil
}
