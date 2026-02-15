package main

import (
	"fmt"
	"time"

	"github.com/spf13/cobra"
	"github.com/zulandar/railyard/internal/orchestration"
)

func newStopCmd() *cobra.Command {
	var (
		configPath string
		timeout    time.Duration
	)

	cmd := &cobra.Command{
		Use:   "stop",
		Short: "Stop the Railyard orchestration",
		Long:  "Gracefully shuts down the Railyard tmux session. Sends drain broadcast, waits for engines to finish, then kills the session.",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runStop(cmd, configPath, timeout)
		},
	}

	cmd.Flags().StringVarP(&configPath, "config", "c", "railyard.yaml", "path to Railyard config file")
	cmd.Flags().DurationVar(&timeout, "timeout", 60*time.Second, "max wait for graceful shutdown")
	return cmd
}

func runStop(cmd *cobra.Command, configPath string, timeout time.Duration) error {
	_, gormDB, err := connectFromConfig(configPath)
	if err != nil {
		return err
	}

	if err := orchestration.Stop(orchestration.StopOpts{
		DB:      gormDB,
		Timeout: timeout,
	}); err != nil {
		return err
	}

	fmt.Fprintf(cmd.OutOrStdout(), "Railyard stopped.\n")
	return nil
}
