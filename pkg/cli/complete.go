package cli

import (
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/zulandar/railyard/internal/car"
	"github.com/zulandar/railyard/internal/engine"
	"github.com/zulandar/railyard/internal/models"
)

// completableStatuses are the car statuses ry complete may transition to done.
// claimed is included for engines that died before marking the car
// in_progress (railyard-rsy).
var completableStatuses = map[string]bool{
	"claimed":     true,
	"in_progress": true,
}

func newCompleteCmd() *cobra.Command {
	var configPath string

	cmd := &cobra.Command{
		Use:   "complete <car-id> <summary>",
		Short: "Mark a car as done",
		Long:  "Marks a car as done, sets completed_at, and writes a final progress note. Called by the agent from within a Claude Code session.",
		Args:  cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			carID := args[0]
			summary := strings.Join(args[1:], " ")
			return runComplete(cmd, configPath, carID, summary)
		},
	}

	cmd.Flags().StringVarP(&configPath, "config", "c", "railyard.yaml", "path to Railyard config file")
	return cmd
}

func runComplete(cmd *cobra.Command, configPath, carID, summary string) error {
	_, gormDB, err := connectFromConfig(configPath)
	if err != nil {
		return err
	}

	// Verify the car exists and is in a completable state.
	b, err := car.Get(gormDB, carID)
	if err != nil {
		return err
	}

	// Fail fast on non-completable statuses before any git work. This read is
	// advisory (clear early error); the authoritative guard is the conditional
	// UPDATE below (railyard-41w).
	if !completableStatuses[b.Status] {
		return fmt.Errorf("complete rejected: car %s is %q — only claimed or in_progress cars can be completed (it may have been reassigned or already merged)", carID, b.Status)
	}

	// Guard: reject completion if branch has zero commits ahead of base.
	// ry complete runs inside the engine's worktree, so use cwd.
	baseBranch := b.BaseBranch
	if baseBranch == "" {
		baseBranch = "main"
	}
	cwd, wdErr := os.Getwd()
	if wdErr != nil {
		return fmt.Errorf("complete rejected: cannot determine working directory: %w", wdErr)
	}

	// Fetch origin so origin/<baseBranch> is current. Without this, a stale
	// origin/main can make main's own recent commits look like branch work,
	// letting a zero-commit branch slip past the guard.
	fetch := exec.Command("git", "fetch", "origin")
	fetch.Dir = cwd
	fetch.CombinedOutput() // best-effort; CommitsAheadOfBase falls back to local ref

	count, cErr := engine.CommitsAheadOfBase(cwd, baseBranch)
	if cErr != nil {
		return fmt.Errorf("complete rejected: cannot verify commits ahead of %s: %w", baseBranch, cErr)
	}
	if count == 0 {
		slog.Warn("ry complete: rejected, zero commits ahead", "car", carID, "base_branch", baseBranch)
		return fmt.Errorf("complete rejected: branch has zero commits ahead of %s — you must commit your work before completing", baseBranch)
	}

	slog.Info("ry complete: marking car done",
		"car", carID,
		"commits_ahead", count,
		"base_branch", baseBranch,
	)

	// Push branch to remote BEFORE setting status to "done". This ensures the
	// yardmaster never sees a "done" car whose branch hasn't been pushed yet.
	if b.Branch != "" {
		if pushErr := engine.PushBranch(cwd, b.Branch); pushErr != nil {
			return fmt.Errorf("complete rejected: push branch %s failed: %w", b.Branch, pushErr)
		}
		slog.Info("ry complete: branch pushed", "car", carID, "branch", b.Branch)
	}

	// Transition to done. Conditional UPDATE + RowsAffected (not read-then-
	// write) so a car reassigned or merged after the check above can never be
	// flipped back to done — the yardmaster must only see done cars that came
	// from an active status (railyard-41w).
	result := gormDB.Model(&models.Car{}).
		Where("id = ? AND status IN ?", carID, []string{"claimed", "in_progress"}).
		Updates(map[string]interface{}{
			"status":       "done",
			"completed_at": time.Now(),
		})
	if result.Error != nil {
		return fmt.Errorf("complete car %s: %w", carID, result.Error)
	}
	if result.RowsAffected == 0 {
		cur, getErr := car.Get(gormDB, carID)
		status := "unknown"
		if getErr == nil {
			status = cur.Status
		}
		return fmt.Errorf("complete rejected: car %s moved to %q during completion — only claimed or in_progress cars can be completed", carID, status)
	}

	slog.Info("ry complete: car marked done", "car", carID, "summary", summary)

	// Write final progress note.
	if err := gormDB.Create(&models.CarProgress{
		CarID:        carID,
		Note:         summary,
		FilesChanged: "[]",
		CreatedAt:    time.Now(),
	}).Error; err != nil {
		return fmt.Errorf("write completion note for %s: %w", carID, err)
	}

	out := cmd.OutOrStdout()
	fmt.Fprintf(out, "Car %s marked done: %s\n", b.ID, b.Title)
	return nil
}

func newProgressCmd() *cobra.Command {
	var configPath string

	cmd := &cobra.Command{
		Use:   "progress <car-id> <note>",
		Short: "Write a progress note for a car",
		Long:  "Writes a progress note to car_progress without changing the car's status. Used before /clear to preserve context for the next cycle.",
		Args:  cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			carID := args[0]
			note := strings.Join(args[1:], " ")
			return runProgress(cmd, configPath, carID, note)
		},
	}

	cmd.Flags().StringVarP(&configPath, "config", "c", "railyard.yaml", "path to Railyard config file")
	return cmd
}

func runProgress(cmd *cobra.Command, configPath, carID, note string) error {
	_, gormDB, err := connectFromConfig(configPath)
	if err != nil {
		return err
	}

	// Verify the car exists.
	b, err := car.Get(gormDB, carID)
	if err != nil {
		return err
	}

	// Write progress note.
	if err := gormDB.Create(&models.CarProgress{
		CarID:        carID,
		Note:         note,
		FilesChanged: "[]",
		CreatedAt:    time.Now(),
	}).Error; err != nil {
		return fmt.Errorf("write progress note for %s: %w", carID, err)
	}

	out := cmd.OutOrStdout()
	fmt.Fprintf(out, "Progress note written for car %s: %s\n", b.ID, b.Title)
	return nil
}
