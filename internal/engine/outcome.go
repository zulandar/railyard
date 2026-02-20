package engine

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/zulandar/railyard/internal/models"
	"gorm.io/gorm"
)

// CompletionOpts holds parameters for handling a successful car completion.
type CompletionOpts struct {
	RepoDir   string
	SessionID string
	Note      string // final completion summary
}

// HandleCompletion processes a successful car completion. The car must already
// be marked done (by the agent via ry complete). This function pushes the branch,
// writes a final progress note, and returns the engine to idle.
func HandleCompletion(db *gorm.DB, car *models.Car, engine *models.Engine, opts CompletionOpts) error {
	if car == nil {
		return fmt.Errorf("engine: car is required")
	}
	if engine == nil {
		return fmt.Errorf("engine: engine is required")
	}
	if opts.RepoDir == "" {
		return fmt.Errorf("engine: repoDir is required")
	}

	// Push the branch.
	if car.Branch != "" {
		if err := PushBranch(opts.RepoDir, car.Branch); err != nil {
			return fmt.Errorf("engine: completion push: %w", err)
		}
	}

	// Write final progress note.
	note := opts.Note
	if note == "" {
		note = "Car completed successfully."
	}

	if err := db.Create(&models.CarProgress{
		CarID:        car.ID,
		EngineID:     engine.ID,
		SessionID:    opts.SessionID,
		Note:         note,
		FilesChanged: "[]",
		CreatedAt:    time.Now(),
	}).Error; err != nil {
		return fmt.Errorf("engine: write completion progress: %w", err)
	}

	// Set engine back to idle, clear current car.
	if err := db.Model(&models.Engine{}).Where("id = ?", engine.ID).Updates(map[string]interface{}{
		"status":      StatusIdle,
		"current_car": "",
		"session_id":  "",
	}).Error; err != nil {
		return fmt.Errorf("engine: reset engine to idle: %w", err)
	}

	return nil
}

// ClearCycleOpts holds parameters for handling a /clear cycle.
type ClearCycleOpts struct {
	RepoDir   string
	SessionID string
	Cycle     int
	Note      string // what was done; auto-generated if empty
}

// HandleClearCycle processes a mid-task /clear. The agent exited but the car
// is not done. Writes a progress note with files changed and keeps the car
// assigned to this engine for re-claim on the next daemon loop iteration.
func HandleClearCycle(db *gorm.DB, car *models.Car, engine *models.Engine, opts ClearCycleOpts) error {
	if car == nil {
		return fmt.Errorf("engine: car is required")
	}
	if engine == nil {
		return fmt.Errorf("engine: engine is required")
	}
	if opts.Cycle <= 0 {
		return fmt.Errorf("engine: cycle must be positive")
	}

	// Capture files changed since last commit.
	filesJSON := "[]"
	if opts.RepoDir != "" {
		files, err := ChangedFiles(opts.RepoDir)
		if err == nil && len(files) > 0 {
			data, _ := json.Marshal(files)
			filesJSON = string(data)
		}
	}

	note := opts.Note
	if note == "" {
		note = fmt.Sprintf("Clear cycle %d — agent exited, car not yet complete.", opts.Cycle)
	}

	// Write progress note.
	if err := db.Create(&models.CarProgress{
		CarID:        car.ID,
		Cycle:        opts.Cycle,
		EngineID:     engine.ID,
		SessionID:    opts.SessionID,
		Note:         note,
		FilesChanged: filesJSON,
		CreatedAt:    time.Now(),
	}).Error; err != nil {
		return fmt.Errorf("engine: write clear cycle progress: %w", err)
	}

	// Keep car assigned — do not release to pool.
	// The car stays in its current status (claimed/in_progress) with the same assignee.
	// The daemon loop will re-spawn the agent on this car.

	return nil
}
