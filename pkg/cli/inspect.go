package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strconv"
	"syscall"

	"github.com/spf13/cobra"
	"github.com/zulandar/railyard/internal/inspect"
	"github.com/zulandar/railyard/internal/logutil"
)

func newInspectCmd() *cobra.Command {
	var (
		configPath string
		logLevel   string
	)

	cmd := &cobra.Command{
		Use:   "inspect",
		Short: "Start the Inspection Pit PR review daemon",
		Long:  "Starts the Inspection Pit daemon that automatically reviews pull requests using AI. Posts inline comments via the GitHub PR review API.",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runInspect(cmd, configPath, logLevel)
		},
	}

	cmd.Flags().StringVarP(&configPath, "config", "c", "railyard.yaml", "path to Railyard config file")
	cmd.Flags().StringVar(&logLevel, "log-level", "", "log level (debug, info, warn, error; env LOG_LEVEL)")
	cmd.AddCommand(newInspectReviewCmd())
	return cmd
}

func runInspect(cmd *cobra.Command, configPath, logLevel string) error {
	level := logutil.ParseLevel(os.Getenv("LOG_LEVEL"), logLevel)
	logger := logutil.NewLogger(cmd.OutOrStdout(), cmd.ErrOrStderr(), level)
	slog.SetDefault(logger)

	cfg, gormDB, err := connectFromConfig(configPath)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		sig := <-sigCh
		logger.Info("Received signal, shutting down", "signal", sig.String())
		cancel()
	}()

	return inspect.Start(ctx, inspect.StartOpts{
		ConfigPath: configPath,
		Config:     cfg,
		DB:         gormDB,
		Logger:     logger,
	})
}

func newInspectReviewCmd() *cobra.Command {
	var configPath string

	cmd := &cobra.Command{
		Use:   "review <pr-number>",
		Short: "Run one-shot review on a single PR (stub)",
		Long:  "One-shot review mode — validates config and PR number. Full single-PR review pipeline is not yet implemented; use the daemon for automated reviews.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runInspectReview(cmd, configPath, args[0])
		},
	}

	cmd.Flags().StringVarP(&configPath, "config", "c", "railyard.yaml", "path to Railyard config file")
	return cmd
}

func runInspectReview(cmd *cobra.Command, configPath, prArg string) error {
	prNumber, err := strconv.Atoi(prArg)
	if err != nil {
		return fmt.Errorf("invalid PR number %q: %w", prArg, err)
	}

	cfg, _, err := connectFromConfig(configPath)
	if err != nil {
		return err
	}

	if !cfg.Inspect.Enabled {
		return fmt.Errorf("inspect: inspect.enabled is not true in config")
	}

	out := cmd.OutOrStdout()
	fmt.Fprintf(out, "Reviewing PR #%d...\n", prNumber)
	fmt.Fprintf(out, "One-shot review mode — full implementation in daemon\n")
	return nil
}
