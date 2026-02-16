package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"
	"github.com/zulandar/railyard/internal/yardmaster"
)

func newYardmasterCmd() *cobra.Command {
	var configPath string

	cmd := &cobra.Command{
		Use:   "yardmaster",
		Short: "Start the Yardmaster supervisor daemon",
		Long:  "Starts the yardmaster supervisor daemon loop. The yardmaster monitors engines, merges branches, handles stalls, and manages dependencies.",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runYardmaster(cmd, configPath)
		},
	}

	cmd.Flags().StringVarP(&configPath, "config", "c", "railyard.yaml", "path to Railyard config file")
	return cmd
}

func runYardmaster(cmd *cobra.Command, configPath string) error {
	cfg, gormDB, err := connectFromConfig(configPath)
	if err != nil {
		return err
	}

	repoDir, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("get working directory: %w", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		sig := <-sigCh
		fmt.Fprintf(cmd.OutOrStdout(), "\nReceived %s, shutting down...\n", sig)
		cancel()
	}()

	return yardmaster.Start(ctx, yardmaster.StartOpts{
		ConfigPath: configPath,
		Config:     cfg,
		DB:         gormDB,
		RepoDir:    repoDir,
		Out:        cmd.OutOrStdout(),
	})
}

func newSwitchCmd() *cobra.Command {
	var (
		configPath string
		dryRun     bool
	)

	cmd := &cobra.Command{
		Use:   "switch <car-id>",
		Short: "Merge a completed car's branch to main",
		Long:  "Runs the switch flow: fetch branch, run tests, merge to main if tests pass. Use --dry-run to run tests without merging.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSwitch(cmd, configPath, args[0], dryRun)
		},
	}

	cmd.Flags().StringVarP(&configPath, "config", "c", "railyard.yaml", "path to Railyard config file")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "run tests without merging")
	return cmd
}

func runSwitch(cmd *cobra.Command, configPath, carID string, dryRun bool) error {
	cfg, gormDB, err := connectFromConfig(configPath)
	if err != nil {
		return err
	}

	repoDir, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("get working directory: %w", err)
	}

	// Look up the car's track to get the configured test command.
	var testCommand string
	var car struct{ Track string }
	if err := gormDB.Table("cars").Select("track").Where("id = ?", carID).Scan(&car).Error; err == nil {
		for _, t := range cfg.Tracks {
			if t.Name == car.Track {
				testCommand = t.TestCommand
				break
			}
		}
	}

	result, err := yardmaster.Switch(gormDB, carID, yardmaster.SwitchOpts{
		RepoDir:     repoDir,
		DryRun:      dryRun,
		TestCommand: testCommand,
	})
	if err != nil {
		return err
	}

	out := cmd.OutOrStdout()
	if result.TestsPassed {
		fmt.Fprintf(out, "Tests passed for car %s\n", carID)
	} else {
		fmt.Fprintf(out, "Tests failed for car %s:\n%s\n", carID, result.TestOutput)
	}

	if result.Merged {
		fmt.Fprintf(out, "Merged branch %s to main\n", result.Branch)
	} else if dryRun {
		fmt.Fprintf(out, "Dry run — branch %s not merged\n", result.Branch)
	}

	return nil
}
