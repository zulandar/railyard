package main

import (
	"bufio"
	"fmt"
	"strings"

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
	cmd.AddCommand(newDBResetCmd())
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

func newDBResetCmd() *cobra.Command {
	var (
		configPath string
		dbName     string
		yes        bool
		force      bool
	)

	cmd := &cobra.Command{
		Use:   "reset",
		Short: "Drop and re-initialize the Railyard database",
		Long: `Drops the Railyard database and optionally re-creates it from config.

By default, reads the config file to determine the database name, drops it,
then re-initializes (migrate + seed). With --database, drops the named
database without re-init.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runDBReset(cmd, configPath, dbName, yes || force)
		},
	}

	cmd.Flags().StringVarP(&configPath, "config", "c", "railyard.yaml", "path to Railyard config file")
	cmd.Flags().StringVar(&dbName, "database", "", "explicit database name (skip re-init)")
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "skip confirmation prompt")
	cmd.Flags().BoolVar(&force, "force", false, "skip confirmation prompt (alias for --yes)")
	return cmd
}

func runDBReset(cmd *cobra.Command, configPath, dbName string, skipConfirm bool) error {
	out := cmd.OutOrStdout()

	// Determine database name and whether to re-init.
	var cfg *config.Config
	reinit := false

	if dbName != "" {
		// Explicit database name — drop only, no re-init.
	} else {
		// Load config to get database name.
		var err error
		cfg, err = config.Load(configPath)
		if err != nil {
			return fmt.Errorf("load config: %w", err)
		}
		dbName = cfg.Dolt.Database
		reinit = true
		fmt.Fprintf(out, "Loaded config for owner %q from %s\n", cfg.Owner, configPath)
	}

	// Confirm with the user.
	if !skipConfirm {
		if !confirmReset(cmd, dbName) {
			fmt.Fprintln(out, "Aborted.")
			return nil
		}
	}

	// Connect to Dolt admin.
	host := "127.0.0.1"
	port := 3306
	if cfg != nil {
		host = cfg.Dolt.Host
		port = cfg.Dolt.Port
	}

	adminDB, err := db.ConnectAdmin(host, port)
	if err != nil {
		return fmt.Errorf("connect to Dolt at %s:%d: %w", host, port, err)
	}
	fmt.Fprintf(out, "Connected to Dolt at %s:%d\n", host, port)

	// Drop the database.
	if err := db.DropDatabase(adminDB, dbName); err != nil {
		return err
	}
	fmt.Fprintf(out, "Dropped database %s\n", dbName)

	if !reinit {
		fmt.Fprintln(out, "\nDatabase dropped successfully.")
		return nil
	}

	// Re-init: create, migrate, seed.
	if err := db.CreateDatabase(adminDB, dbName); err != nil {
		return err
	}
	fmt.Fprintf(out, "Database %s re-created\n", dbName)

	gormDB, err := db.Connect(host, port, dbName)
	if err != nil {
		return fmt.Errorf("connect to %s: %w", dbName, err)
	}

	if err := db.AutoMigrate(gormDB); err != nil {
		return err
	}
	models := db.AllModels()
	fmt.Fprintf(out, "Migrated %d tables\n", len(models))

	if err := db.SeedTracks(gormDB, cfg.Tracks); err != nil {
		return err
	}
	fmt.Fprintf(out, "Seeded %d tracks:", len(cfg.Tracks))
	for _, t := range cfg.Tracks {
		fmt.Fprintf(out, " %s", t.Name)
	}
	fmt.Fprintln(out)

	if err := db.SeedConfig(gormDB, cfg); err != nil {
		return err
	}
	fmt.Fprintf(out, "Configuration written for owner %q\n", cfg.Owner)

	fmt.Fprintln(out, "\nRailyard database reset and re-initialized successfully.")
	return nil
}

func confirmReset(cmd *cobra.Command, dbName string) bool {
	out := cmd.OutOrStdout()
	in := cmd.InOrStdin()

	fmt.Fprintf(out, "WARNING: This will permanently delete all data in database %q.\n", dbName)
	fmt.Fprintln(out, "This action cannot be undone.")
	fmt.Fprintln(out)
	fmt.Fprint(out, "Type \"yes\" to confirm: ")

	scanner := bufio.NewScanner(in)
	if scanner.Scan() {
		return strings.TrimSpace(scanner.Text()) == "yes"
	}
	return false
}
