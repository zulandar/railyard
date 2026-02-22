package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"github.com/zulandar/railyard/internal/config"
	"github.com/zulandar/railyard/internal/db"
	"github.com/zulandar/railyard/internal/orchestration"
)

func newStartCmd() *cobra.Command {
	var (
		configPath    string
		engines       int
		withTelegraph bool
	)

	cmd := &cobra.Command{
		Use:   "start",
		Short: "Start the Railyard orchestration",
		Long:  "Creates a tmux session with Yardmaster and N engine agents. Use --telegraph to include Telegraph. Start Dispatch separately with 'ry dispatch'.",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runStart(cmd, configPath, engines, withTelegraph)
		},
	}

	cmd.Flags().StringVarP(&configPath, "config", "c", "railyard.yaml", "path to Railyard config file")
	cmd.Flags().IntVar(&engines, "engines", 0, "number of engines (default: sum of track engine_slots)")
	cmd.Flags().BoolVar(&withTelegraph, "telegraph", false, "include Telegraph chat bridge pane")
	return cmd
}

func runStart(cmd *cobra.Command, configPath string, engines int, withTelegraph bool) error {
	// Warn if old engines/ layout is present without .railyard/.
	checkMigrationNeeded(cmd)

	cfg, err := config.Load(configPath)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	gormDB, err := db.Connect(cfg.Dolt.Host, cfg.Dolt.Port, cfg.Dolt.Database)
	if err != nil {
		return fmt.Errorf("connect to %s: %w", cfg.Dolt.Database, err)
	}

	// Enable telegraph if --telegraph flag set or config has telegraph section.
	telegraph := withTelegraph || cfg.Telegraph.Platform != ""

	result, err := orchestration.Start(orchestration.StartOpts{
		Config:     cfg,
		ConfigPath: configPath,
		DB:         gormDB,
		Engines:    engines,
		Telegraph:  telegraph,
	})
	if err != nil {
		return err
	}

	out := cmd.OutOrStdout()
	fmt.Fprintf(out, "Railyard started (session: %s)\n", result.Session)
	fmt.Fprintf(out, "  Yardmaster:  %s\n", result.YardmasterPane)
	if result.TelegraphPane != "" {
		fmt.Fprintf(out, "  Telegraph:   %s\n", result.TelegraphPane)
	}
	fmt.Fprintf(out, "  Engines:     %d\n", len(result.EnginePanes))
	for _, ep := range result.EnginePanes {
		fmt.Fprintf(out, "    %s → %s\n", ep.PaneID, ep.Track)
	}
	fmt.Fprintf(out, "\nAttach with: tmux attach -t %s\n", result.Session)
	fmt.Fprintf(out, "Start Dispatch separately: ry dispatch --config %s\n", configPath)
	return nil
}

// checkMigrationNeeded prints a warning if the repo uses the old engines/ layout
// without a .railyard/ directory. Does not block startup.
func checkMigrationNeeded(cmd *cobra.Command) {
	if _, err := os.Stat("engines"); err != nil {
		return // no engines/ dir — nothing to migrate
	}
	if _, err := os.Stat(".railyard"); err == nil {
		return // already migrated
	}
	fmt.Fprintln(cmd.ErrOrStderr(),
		"Warning: Railyard directory layout has changed. Run 'ry migrate' to update.")
}
