package main

import (
	"fmt"

	"github.com/spf13/cobra"
	"github.com/zulandar/railyard/internal/config"
	"github.com/zulandar/railyard/internal/dispatch"
)

func newDispatchCmd() *cobra.Command {
	var configPath string

	cmd := &cobra.Command{
		Use:   "dispatch",
		Short: "Start the Dispatch planner agent",
		Long:  "Starts an interactive Claude Code session with the Dispatch planner prompt. Use this to decompose feature requests into structured car plans.",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runDispatch(cmd, configPath)
		},
	}

	cmd.Flags().StringVarP(&configPath, "config", "c", "railyard.yaml", "path to Railyard config file")
	return cmd
}

func runDispatch(cmd *cobra.Command, configPath string) error {
	cfg, err := config.Load(configPath)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	return dispatch.Start(dispatch.StartOpts{
		ConfigPath: configPath,
		Config:     cfg,
	})
}
