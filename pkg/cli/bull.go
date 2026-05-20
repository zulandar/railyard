package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"strconv"
	"syscall"

	"github.com/spf13/cobra"
	"github.com/zulandar/railyard/internal/bull"
)

func newBullCmd() *cobra.Command {
	var configPath string

	cmd := &cobra.Command{
		Use:   "bull",
		Short: "Start the Bull GitHub issue triage daemon",
		Long:  "Starts the Bull daemon that monitors GitHub Issues, triages them via heuristic filters and AI analysis, creates draft cars, and maintains bidirectional status sync.",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runBull(cmd, configPath)
		},
	}

	cmd.Flags().StringVarP(&configPath, "config", "c", "railyard.yaml", "path to Railyard config file")
	cmd.AddCommand(newBullTriageCmd())
	return cmd
}

func runBull(cmd *cobra.Command, configPath string) error {
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
		fmt.Fprintf(cmd.OutOrStdout(), "\nReceived %s, shutting down...\n", sig)
		cancel()
	}()

	return bull.Start(ctx, bull.StartOpts{
		ConfigPath: configPath,
		Config:     cfg,
		DB:         gormDB,
		Out:        cmd.OutOrStdout(),
	})
}

func newBullTriageCmd() *cobra.Command {
	var configPath string

	cmd := &cobra.Command{
		Use:   "triage <issue-number>",
		Short: "Run one-shot triage on a single GitHub issue",
		Long:  "Fetches the specified GitHub issue and runs the full triage pipeline (heuristic filter, AI classification, car creation or rejection).",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runBullTriage(cmd, configPath, args[0])
		},
	}

	cmd.Flags().StringVarP(&configPath, "config", "c", "railyard.yaml", "path to Railyard config file")
	return cmd
}

func runBullTriage(cmd *cobra.Command, configPath, issueArg string) error {
	issueNumber, err := strconv.Atoi(issueArg)
	if err != nil {
		return fmt.Errorf("invalid issue number %q: %w", issueArg, err)
	}

	cfg, _, err := connectFromConfig(configPath)
	if err != nil {
		return err
	}

	if !cfg.Bull.Enabled {
		return fmt.Errorf("bull: bull.enabled is not true in config")
	}

	out := cmd.OutOrStdout()
	client, err := bull.NewClient(cfg.Owner, cfg.Repo, cfg.Bull)
	if err != nil {
		return fmt.Errorf("bull: %w", err)
	}

	ctx := context.Background()
	issue, err := client.GetIssue(ctx, issueNumber)
	if err != nil {
		return fmt.Errorf("fetch issue #%d: %w", issueNumber, err)
	}

	fmt.Fprintf(out, "Issue #%d: %s\n", issue.GetNumber(), issue.GetTitle())

	// Heuristic pre-filter.
	filterResult := bull.FilterIssue(issue, cfg.Bull.Labels.Ignore, nil)
	if !filterResult.Pass {
		fmt.Fprintf(out, "Filtered: %s\n", filterResult.Reason)
		return nil
	}

	fmt.Fprintf(out, "Passed heuristic filter — AI triage would run next (no AI configured in one-shot mode)\n")
	return nil
}
