package main

import (
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/zulandar/railyard/internal/car"
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

	// Transition to done.
	if err := car.Update(gormDB, carID, map[string]interface{}{
		"status": "done",
	}); err != nil {
		return fmt.Errorf("complete car %s: %w", carID, err)
	}

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
