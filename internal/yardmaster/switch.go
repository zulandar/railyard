package yardmaster

import (
	"fmt"
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
	RepoDir        string // working directory
	DryRun         bool   // run tests but don't merge
	PreTestCommand string // command to run before tests (e.g. "go mod vendor", "npm install")
	TestCommand    string // per-track test command (e.g. "go test ./...", "phpunit", "npm test")
	RequirePR      bool   // create a draft PR instead of direct merge
}

// SwitchResult contains the outcome of a switch operation.
type SwitchResult struct {
	CarID         string
	Branch        string
	TestsPassed   bool
	TestOutput    string
	Merged        bool
	AlreadyMerged bool // true when the branch was already an ancestor of main
	PRCreated     bool
	PRUrl         string
	Error         error
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

	result := &SwitchResult{
		CarID:  carID,
		Branch: car.Branch,
	}

	// Fetch the branch.
	if err := gitFetch(opts.RepoDir); err != nil {
		result.Error = fmt.Errorf("fetch: %w", err)
		return result, result.Error
	}

	// Detach the engine worktree so the branch can be checked out in the main repo.
	if car.Assignee != "" {
		detachEngineWorktree(opts.RepoDir, car.Assignee)
	}

	// Run tests on the branch (unless skip_tests is set on the car).
	if car.SkipTests {
		result.TestsPassed = true
		result.TestOutput = "tests skipped (skip_tests=true on car)"
	} else {
		testOutput, testErr := runTests(opts.RepoDir, car.Branch, opts.PreTestCommand, opts.TestCommand)
		result.TestOutput = testOutput

		if testErr != nil {
			result.TestsPassed = false
			// Set car status to blocked and notify.
			db.Model(&models.Car{}).Where("id = ?", carID).Update("status", "blocked")
			if car.Assignee != "" {
				messaging.Send(db, "yardmaster", car.Assignee, "test-failure",
					fmt.Sprintf("Tests failed for car %s on branch %s:\n%s", carID, car.Branch, testOutput),
					messaging.SendOpts{CarID: carID, Priority: "urgent"},
				)
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
	if isAncestor(opts.RepoDir, car.Branch) {
		result.Merged = true
		result.AlreadyMerged = true

		now := time.Now()
		db.Model(&models.Car{}).Where("id = ?", carID).Updates(map[string]interface{}{
			"status":       "merged",
			"completed_at": now,
		})

		// Run the same post-merge logic as a normal merge.
		unblocked, _ := UnblockDeps(db, carID)
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
			result.Error = fmt.Errorf("push branch: %w", err)
			return result, result.Error
		}

		// Build the PR body from the car record and progress notes.
		prBody := buildPRBody(db, &car, opts.RepoDir)

		prURL, err := createDraftPR(opts.RepoDir, car.Title, prBody, car.Branch)
		if err != nil {
			result.Error = fmt.Errorf("create PR: %w", err)
			return result, result.Error
		}

		result.PRCreated = true
		result.PRUrl = prURL

		// Mark car as pr_open — not merged yet, waiting for human review.
		now := time.Now()
		db.Model(&models.Car{}).Where("id = ?", carID).Updates(map[string]interface{}{
			"status":       "pr_open",
			"completed_at": now,
		})

		return result, nil
	}

	// Merge to main.
	if err := gitMerge(opts.RepoDir, car.Branch); err != nil {
		result.Error = fmt.Errorf("merge: %w", err)
		return result, result.Error
	}

	result.Merged = true

	// Mark car as merged so it won't be re-processed.
	now := time.Now()
	db.Model(&models.Car{}).Where("id = ?", carID).Updates(map[string]interface{}{
		"status":       "merged",
		"completed_at": now,
	})

	// Unblock cross-track dependencies.
	unblocked, _ := UnblockDeps(db, carID)
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
		db.Model(&models.CarDep{}).
			Where("car_id = ? AND blocked_by != ?", dep.CarID, carID).
			Joins("JOIN cars ON cars.id = car_deps.blocked_by AND cars.status NOT IN ('done', 'merged')").
			Count(&otherBlockers)

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

// runTests checks out the branch and runs the test suite.
func runTests(repoDir, branch, preTestCommand, testCommand string) (string, error) {
	// Checkout the branch.
	checkout := exec.Command("git", "checkout", branch)
	checkout.Dir = repoDir
	if out, err := checkout.CombinedOutput(); err != nil {
		return string(out), fmt.Errorf("git checkout %s: %w", branch, err)
	}

	// Run pre-test command if configured (e.g. "go mod vendor", "npm install").
	if preTestCommand != "" {
		preCmd := exec.Command("sh", "-c", preTestCommand)
		preCmd.Dir = repoDir
		if out, err := preCmd.CombinedOutput(); err != nil {
			// Checkout main again before returning.
			backToMain := exec.Command("git", "checkout", "main")
			backToMain.Dir = repoDir
			backToMain.CombinedOutput()
			return string(out), fmt.Errorf("pre-test command failed: %w", err)
		}
	}

	// Run the track's configured test command, defaulting to "go test ./...".
	if testCommand == "" {
		testCommand = "go test ./..."
	}
	testCmd := exec.Command("sh", "-c", testCommand)
	testCmd.Dir = repoDir

	out, err := testCmd.CombinedOutput()
	output := string(out)

	// Checkout main again regardless.
	backToMain := exec.Command("git", "checkout", "main")
	backToMain.Dir = repoDir
	backToMain.CombinedOutput()

	if err != nil {
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

// isAncestor returns true if the given branch is already fully contained
// in main (i.e., all its commits are reachable from main). This happens
// when a dependent car's merge already included this branch's changes.
func isAncestor(repoDir, branch string) bool {
	cmd := exec.Command("git", "merge-base", "--is-ancestor", branch, "main")
	cmd.Dir = repoDir
	return cmd.Run() == nil
}

// gitMerge merges the branch into main.
func gitMerge(repoDir, branch string) error {
	// Checkout main.
	checkout := exec.Command("git", "checkout", "main")
	checkout.Dir = repoDir
	if out, err := checkout.CombinedOutput(); err != nil {
		return fmt.Errorf("git checkout main: %s: %w", string(out), err)
	}

	// Merge the branch with co-author trailer for Railyard attribution.
	msg := fmt.Sprintf("Switch: merge %s to main\n\nCo-Authored-By: Railyard Yardmaster <railyard-yardmaster@noreply>", branch)
	merge := exec.Command("git", "merge", "--no-ff", branch, "-m", msg)
	merge.Dir = repoDir
	if out, err := merge.CombinedOutput(); err != nil {
		return fmt.Errorf("git merge %s: %s: %w", branch, string(out), err)
	}

	return nil
}

// gitPush pushes the current branch to the remote.
func gitPush(repoDir string) error {
	cmd := exec.Command("git", "push", "origin", "main")
	cmd.Dir = repoDir
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git push: %s: %w", string(out), err)
	}
	return nil
}

// detachEngineWorktree detaches HEAD in the engine's worktree so the branch
// can be checked out elsewhere. This is a best-effort operation — if the
// worktree doesn't exist or is already detached, the error is silently ignored.
func detachEngineWorktree(repoDir, engineID string) {
	wtDir := filepath.Join(repoDir, "engines", engineID)
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
	db.Model(&models.Car{}).
		Where("parent_id = ? AND status NOT IN ?", epicID, []string{"done", "merged", "cancelled"}).
		Count(&remaining)

	if remaining > 0 {
		return
	}

	// All children are done/cancelled — close the epic.
	now := time.Now()
	db.Model(&models.Car{}).Where("id = ?", epicID).Updates(map[string]interface{}{
		"status":       "done",
		"completed_at": now,
	})

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
func buildPRBody(db *gorm.DB, car *models.Car, repoDir string) string {
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
	diffStat := gitDiffStat(repoDir, car.Branch)
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

// gitDiffStat returns the diff --stat between main and the given branch.
func gitDiffStat(repoDir, branch string) string {
	cmd := exec.Command("git", "diff", "--stat", "main..."+branch)
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

// CreateReindexJob inserts a reindex_jobs row after a successful merge.
func CreateReindexJob(db *gorm.DB, track, commitHash string) error {
	if db == nil {
		return fmt.Errorf("yardmaster: db is required")
	}
	if track == "" {
		return fmt.Errorf("yardmaster: track is required")
	}

	job := models.ReindexJob{
		Track:         track,
		TriggerCommit: commitHash,
		Status:        "pending",
		CreatedAt:     time.Now(),
	}
	if err := db.Create(&job).Error; err != nil {
		return fmt.Errorf("yardmaster: create reindex job: %w", err)
	}
	return nil
}
