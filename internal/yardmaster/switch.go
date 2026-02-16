package yardmaster

import (
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/zulandar/railyard/internal/messaging"
	"github.com/zulandar/railyard/internal/models"
	"gorm.io/gorm"
)

// SwitchOpts holds parameters for the switch (merge) operation.
type SwitchOpts struct {
	RepoDir string // working directory
	DryRun  bool   // run tests but don't merge
}

// SwitchResult contains the outcome of a switch operation.
type SwitchResult struct {
	CarID     string
	Branch     string
	TestsPassed bool
	TestOutput string
	Merged     bool
	Error      error
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
		CarID: carID,
		Branch: car.Branch,
	}

	// Fetch the branch.
	if err := gitFetch(opts.RepoDir); err != nil {
		result.Error = fmt.Errorf("fetch: %w", err)
		return result, result.Error
	}

	// Run tests on the branch.
	testOutput, testErr := runTests(opts.RepoDir, car.Branch, car.Track)
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

	if opts.DryRun {
		return result, nil
	}

	// Merge to main.
	if err := gitMerge(opts.RepoDir, car.Branch); err != nil {
		result.Error = fmt.Errorf("merge: %w", err)
		return result, result.Error
	}

	result.Merged = true

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
			Joins("JOIN cars ON cars.id = car_deps.blocked_by AND cars.status != 'done'").
			Count(&otherBlockers)

		if otherBlockers == 0 {
			// No other blockers — unblock this car.
			db.Model(&models.Car{}).Where("id = ? AND status = ?", dep.CarID, "blocked").
				Update("status", "open")

			var b models.Car
			if err := db.First(&b, "id = ?", dep.CarID).Error; err == nil {
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

// runTests checks out the branch and runs the test suite.
func runTests(repoDir, branch, track string) (string, error) {
	// Checkout the branch.
	checkout := exec.Command("git", "checkout", branch)
	checkout.Dir = repoDir
	if out, err := checkout.CombinedOutput(); err != nil {
		return string(out), fmt.Errorf("git checkout %s: %w", branch, err)
	}

	// Run tests — use go test for go tracks, npm test for others.
	var testCmd *exec.Cmd
	testCmd = exec.Command("go", "test", "./...")
	testCmd.Dir = repoDir

	out, err := testCmd.CombinedOutput()
	output := string(out)

	// Checkout main again regardless.
	backToMain := exec.Command("git", "checkout", "main")
	backToMain.Dir = repoDir
	backToMain.CombinedOutput()

	if err != nil {
		return output, fmt.Errorf("tests failed: %w", err)
	}

	return output, nil
}

// gitMerge merges the branch into main.
func gitMerge(repoDir, branch string) error {
	// Checkout main.
	checkout := exec.Command("git", "checkout", "main")
	checkout.Dir = repoDir
	if out, err := checkout.CombinedOutput(); err != nil {
		return fmt.Errorf("git checkout main: %s: %w", string(out), err)
	}

	// Merge the branch.
	merge := exec.Command("git", "merge", "--no-ff", branch, "-m",
		fmt.Sprintf("Switch: merge %s to main", branch))
	merge.Dir = repoDir
	if out, err := merge.CombinedOutput(); err != nil {
		return fmt.Errorf("git merge %s: %s: %w", branch, string(out), err)
	}

	return nil
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
