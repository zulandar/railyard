package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/spf13/cobra"
	"github.com/zulandar/railyard/internal/events"
	"github.com/zulandar/railyard/internal/logutil"
	"github.com/zulandar/railyard/internal/yardmaster"
)

func newYardmasterCmd() *cobra.Command {
	var (
		configPath string
		logLevel   string
	)

	cmd := &cobra.Command{
		Use:   "yardmaster",
		Short: "Start the Yardmaster supervisor daemon",
		Long:  "Starts the yardmaster supervisor daemon loop. The yardmaster monitors engines, merges branches, handles stalls, and manages dependencies.",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runYardmaster(cmd, configPath, logLevel)
		},
	}

	cmd.Flags().StringVarP(&configPath, "config", "c", "railyard.yaml", "path to Railyard config file")
	cmd.Flags().StringVar(&logLevel, "log-level", "", "log level (debug, info, warn, error; env LOG_LEVEL)")
	return cmd
}

func runYardmaster(cmd *cobra.Command, configPath, logLevel string) error {
	level := logutil.ParseLevel(os.Getenv("LOG_LEVEL"), logLevel)
	logger := logutil.NewLogger(cmd.OutOrStdout(), cmd.ErrOrStderr(), level)
	slog.SetDefault(logger)

	cfg, gormDB, err := connectFromConfig(configPath)
	if err != nil {
		return err
	}

	// Sync embedded CocoIndex scripts so overlay cleanup works.
	if err := ensureCocoIndexScripts(cfg.CocoIndex.ScriptsPath); err != nil {
		logger.Warn("Cocoindex scripts sync warning", "error", err)
	}

	repoDir, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("get working directory: %w", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Construct the event bus and plugin host BEFORE any subsystem starts so
	// plugin Init runs before the yardmaster daemon claims state. The OSS
	// binary registers zero plugins, so host.Init / host.Start / host.Stop
	// are effective no-ops there.
	bus := events.NewBusWithLogger(logger)
	host := buildPluginHost(cfg, gormDB, bus)
	host.Init(ctx)
	host.Start(ctx)

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		sig := <-sigCh
		logger.Info("Received signal, shutting down", "signal", sig.String())
		cancel()
	}()

	startErr := yardmaster.Start(ctx, yardmaster.StartOpts{
		ConfigPath: configPath,
		Config:     cfg,
		DB:         gormDB,
		RepoDir:    repoDir,
		Logger:     logger,
		Bus:        bus,
	})

	// Stop plugins after the supervisor loop returns. host.Stop owns its own
	// per-plugin 5-second drain bound (see internal/pluginhost.stopDrainTimeout);
	// we give the parent context room to honor that without imposing a tighter
	// outer deadline.
	stopCtx, stopCancel := context.WithTimeout(context.Background(), 30*time.Second)
	host.Stop(stopCtx)
	stopCancel()

	return startErr
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

	// Look up the car's track and base branch.
	var testCommand, preTestCommand, baseBranch string
	var car struct {
		Track      string
		BaseBranch string
	}
	if err := gormDB.Table("cars").Select("track, base_branch").Where("id = ?", carID).Scan(&car).Error; err == nil {
		baseBranch = car.BaseBranch
		for _, t := range cfg.Tracks {
			if t.Name == car.Track {
				preTestCommand = t.PreTestCommand
				testCommand = t.TestCommand
				break
			}
		}
	}

	result, err := yardmaster.Switch(gormDB, carID, yardmaster.SwitchOpts{
		RepoDir:        repoDir,
		BaseBranch:     baseBranch,
		DryRun:         dryRun,
		PreTestCommand: preTestCommand,
		TestCommand:    testCommand,
		ConfigPath:     configPath,
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
		target := baseBranch
		if target == "" {
			target = "main"
		}
		fmt.Fprintf(out, "Merged branch %s to %s\n", result.Branch, target)
	} else if dryRun {
		fmt.Fprintf(out, "Dry run — branch %s not merged\n", result.Branch)
	}

	return nil
}
