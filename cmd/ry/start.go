package main

import (
	"fmt"

	"github.com/spf13/cobra"
	"github.com/zulandar/railyard/internal/config"
	"github.com/zulandar/railyard/internal/db"
	"github.com/zulandar/railyard/internal/orchestration"
)

func newStartCmd() *cobra.Command {
	var (
		configPath string
		engines    int
	)

	cmd := &cobra.Command{
		Use:   "start",
		Short: "Start the Railyard orchestration",
		Long:  "Creates a tmux session with Dispatch, Yardmaster, and N engine agents. Engine count defaults to the sum of track engine_slots.",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runStart(cmd, configPath, engines)
		},
	}

	cmd.Flags().StringVarP(&configPath, "config", "c", "railyard.yaml", "path to Railyard config file")
	cmd.Flags().IntVar(&engines, "engines", 0, "number of engines (default: sum of track engine_slots)")
	return cmd
}

func runStart(cmd *cobra.Command, configPath string, engines int) error {
	cfg, err := config.Load(configPath)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	gormDB, err := db.Connect(cfg.Dolt.Host, cfg.Dolt.Port, cfg.Dolt.Database)
	if err != nil {
		return fmt.Errorf("connect to %s: %w", cfg.Dolt.Database, err)
	}

	result, err := orchestration.Start(orchestration.StartOpts{
		Config:     cfg,
		ConfigPath: configPath,
		DB:         gormDB,
		Engines:    engines,
	})
	if err != nil {
		return err
	}

	out := cmd.OutOrStdout()
	fmt.Fprintf(out, "Railyard started (session: %s)\n", result.Session)
	fmt.Fprintf(out, "  Dispatch:    %s\n", result.DispatchPane)
	fmt.Fprintf(out, "  Yardmaster:  %s\n", result.YardmasterPane)
	fmt.Fprintf(out, "  Engines:     %d\n", len(result.EnginePanes))
	for _, ep := range result.EnginePanes {
		fmt.Fprintf(out, "    %s → %s\n", ep.PaneID, ep.Track)
	}
	fmt.Fprintf(out, "\nAttach with: tmux attach -t %s\n", result.Session)
	return nil
}
