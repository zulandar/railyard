package main

import (
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/zulandar/railyard/internal/bead"
	"github.com/zulandar/railyard/internal/models"
)

func newCompleteCmd() *cobra.Command {
	var configPath string

	cmd := &cobra.Command{
		Use:   "complete <bead-id> <summary>",
		Short: "Mark a bead as done",
		Long:  "Marks a bead as done, sets completed_at, and writes a final progress note. Called by the agent from within a Claude Code session.",
		Args:  cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			beadID := args[0]
			summary := strings.Join(args[1:], " ")
			return runComplete(cmd, configPath, beadID, summary)
		},
	}

	cmd.Flags().StringVarP(&configPath, "config", "c", "railyard.yaml", "path to Railyard config file")
	return cmd
}

func runComplete(cmd *cobra.Command, configPath, beadID, summary string) error {
	_, gormDB, err := connectFromConfig(configPath)
	if err != nil {
		return err
	}

	// Verify the bead exists and is in a completable state.
	b, err := bead.Get(gormDB, beadID)
	if err != nil {
		return err
	}

	// Transition to done.
	if err := bead.Update(gormDB, beadID, map[string]interface{}{
		"status": "done",
	}); err != nil {
		return fmt.Errorf("complete bead %s: %w", beadID, err)
	}

	// Write final progress note.
	if err := gormDB.Create(&models.BeadProgress{
		BeadID:    beadID,
		Note:      summary,
		CreatedAt: time.Now(),
	}).Error; err != nil {
		return fmt.Errorf("write completion note for %s: %w", beadID, err)
	}

	out := cmd.OutOrStdout()
	fmt.Fprintf(out, "Bead %s marked done: %s\n", b.ID, b.Title)
	return nil
}

func newProgressCmd() *cobra.Command {
	var configPath string

	cmd := &cobra.Command{
		Use:   "progress <bead-id> <note>",
		Short: "Write a progress note for a bead",
		Long:  "Writes a progress note to bead_progress without changing the bead's status. Used before /clear to preserve context for the next cycle.",
		Args:  cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			beadID := args[0]
			note := strings.Join(args[1:], " ")
			return runProgress(cmd, configPath, beadID, note)
		},
	}

	cmd.Flags().StringVarP(&configPath, "config", "c", "railyard.yaml", "path to Railyard config file")
	return cmd
}

func runProgress(cmd *cobra.Command, configPath, beadID, note string) error {
	_, gormDB, err := connectFromConfig(configPath)
	if err != nil {
		return err
	}

	// Verify the bead exists.
	b, err := bead.Get(gormDB, beadID)
	if err != nil {
		return err
	}

	// Write progress note.
	if err := gormDB.Create(&models.BeadProgress{
		BeadID:    beadID,
		Note:      note,
		CreatedAt: time.Now(),
	}).Error; err != nil {
		return fmt.Errorf("write progress note for %s: %w", beadID, err)
	}

	out := cmd.OutOrStdout()
	fmt.Fprintf(out, "Progress note written for bead %s: %s\n", b.ID, b.Title)
	return nil
}
