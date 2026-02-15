package main

import (
	"fmt"

	"github.com/spf13/cobra"
	"github.com/zulandar/railyard/internal/config"
	"github.com/zulandar/railyard/internal/db"
)

func newDBCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "db",
		Short: "Database management commands",
	}

	cmd.AddCommand(newDBInitCmd())
	return cmd
}

func newDBInitCmd() *cobra.Command {
	var configPath string

	cmd := &cobra.Command{
		Use:   "init",
		Short: "Initialize the Railyard database",
		Long:  "Creates the Dolt database, migrates all tables, seeds tracks and configuration.",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runDBInit(cmd, configPath)
		},
	}

	cmd.Flags().StringVarP(&configPath, "config", "c", "railyard.yaml", "path to Railyard config file")
	return cmd
}

func runDBInit(cmd *cobra.Command, configPath string) error {
	out := cmd.OutOrStdout()

	// Load config
	cfg, err := config.Load(configPath)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	fmt.Fprintf(out, "Loaded config for owner %q from %s\n", cfg.Owner, configPath)

	// Connect to Dolt server (admin, no database selected)
	adminDB, err := db.ConnectAdmin(cfg.Dolt.Host, cfg.Dolt.Port)
	if err != nil {
		return fmt.Errorf("connect to Dolt at %s:%d: %w", cfg.Dolt.Host, cfg.Dolt.Port, err)
	}
	fmt.Fprintf(out, "Connected to Dolt at %s:%d\n", cfg.Dolt.Host, cfg.Dolt.Port)

	// Create database
	if err := db.CreateDatabase(adminDB, cfg.Dolt.Database); err != nil {
		return err
	}
	fmt.Fprintf(out, "Database %s ready\n", cfg.Dolt.Database)

	// Connect to the railyard database
	gormDB, err := db.Connect(cfg.Dolt.Host, cfg.Dolt.Port, cfg.Dolt.Database)
	if err != nil {
		return fmt.Errorf("connect to %s: %w", cfg.Dolt.Database, err)
	}

	// AutoMigrate all tables
	if err := db.AutoMigrate(gormDB); err != nil {
		return err
	}
	models := db.AllModels()
	fmt.Fprintf(out, "Migrated %d tables\n", len(models))

	// Seed tracks
	if err := db.SeedTracks(gormDB, cfg.Tracks); err != nil {
		return err
	}
	fmt.Fprintf(out, "Seeded %d tracks:", len(cfg.Tracks))
	for _, t := range cfg.Tracks {
		fmt.Fprintf(out, " %s", t.Name)
	}
	fmt.Fprintln(out)

	// Seed config
	if err := db.SeedConfig(gormDB, cfg); err != nil {
		return err
	}
	fmt.Fprintf(out, "Configuration written for owner %q\n", cfg.Owner)

	fmt.Fprintln(out, "\nRailyard database initialized successfully.")
	return nil
}
