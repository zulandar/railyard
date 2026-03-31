package main

import (
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/zulandar/railyard/internal/car"
	"github.com/zulandar/railyard/internal/engine"
	"github.com/zulandar/railyard/internal/models"
)

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

	// Transition to done.
	if err := car.Update(gormDB, carID, map[string]interface{}{
		"status": "done",
	}); err != nil {
		return fmt.Errorf("complete car %s: %w", carID, err)
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
