package main

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/zulandar/railyard/internal/audit"
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
	cmd.AddCommand(newDBStartCmd())
	return cmd
}

func newDBInitCmd() *cobra.Command {
	var configPath string

	cmd := &cobra.Command{
		Use:   "init",
		Short: "Initialize the Railyard database",
		Long:  "Creates the database, migrates all tables, seeds tracks and configuration.",
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

	// Try connecting directly to the target database first (it may already
	// exist, e.g. created by the Helm init configmap in K8s).
	gormDB, directErr := db.Connect(cfg.Database.Host, cfg.Database.Port, cfg.Database.Database, cfg.Database.Username, cfg.Database.Password)
	if directErr == nil {
		fmt.Fprintf(out, "Connected to database at %s:%d\n", cfg.Database.Host, cfg.Database.Port)
		fmt.Fprintf(out, "Database %s already exists, skipping creation\n", cfg.Database.Database)
	} else {
		// Database doesn't exist yet — create it via admin connection.
		adminDB, err := db.ConnectAdmin(cfg.Database.Host, cfg.Database.Port, cfg.Database.Username, cfg.Database.Password)
		if err != nil {
			return fmt.Errorf("connect to database at %s:%d: %w", cfg.Database.Host, cfg.Database.Port, err)
		}
		fmt.Fprintf(out, "Connected to database at %s:%d\n", cfg.Database.Host, cfg.Database.Port)

		if err := db.CreateDatabase(adminDB, cfg.Database.Database); err != nil {
			return err
		}
		fmt.Fprintf(out, "Database %s ready\n", cfg.Database.Database)

		gormDB, err = db.Connect(cfg.Database.Host, cfg.Database.Port, cfg.Database.Database, cfg.Database.Username, cfg.Database.Password)
		if err != nil {
			return fmt.Errorf("connect to %s: %w", cfg.Database.Database, err)
		}
	}

	// Best-effort audit; do not fail init if audit logging fails.
	_ = audit.Log(gormDB, os.Stderr, "config.loaded", "system", configPath, map[string]interface{}{
		"owner":  cfg.Owner,
		"tracks": len(cfg.Tracks),
	})

	// AutoMigrate all tables
	if err := db.AutoMigrate(gormDB); err != nil {
		return err
	}
	models := db.AllModels()
	fmt.Fprintf(out, "Migrated %d tables\n", len(models))

	// Seed tracks
	if err := db.SeedTracks(gormDB, cfg.Tracks, os.Stderr); err != nil {
		return err
	}
	fmt.Fprintf(out, "Seeded %d tracks:", len(cfg.Tracks))
	for _, t := range cfg.Tracks {
		fmt.Fprintf(out, " %s", t.Name)
	}
	fmt.Fprintln(out)

	// Seed config
	if err := db.SeedConfig(gormDB, cfg, os.Stderr); err != nil {
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
		dbName = cfg.Database.Database
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

	// Connect to database admin.
	host := "127.0.0.1"
	port := 3306
	username := "root"
	password := ""
	if cfg != nil {
		host = cfg.Database.Host
		port = cfg.Database.Port
		username = cfg.Database.Username
		password = cfg.Database.Password
	}

	adminDB, err := db.ConnectAdmin(host, port, username, password)
	if err != nil {
		return fmt.Errorf("connect to database at %s:%d: %w", host, port, err)
	}
	fmt.Fprintf(out, "Connected to database at %s:%d\n", host, port)

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

	gormDB, err := db.Connect(host, port, dbName, username, password)
	if err != nil {
		return fmt.Errorf("connect to %s: %w", dbName, err)
	}

	// Best-effort audit; do not fail reset if audit logging fails.
	_ = audit.Log(gormDB, os.Stderr, "config.loaded", "system", configPath, map[string]interface{}{
		"owner":  cfg.Owner,
		"tracks": len(cfg.Tracks),
	})

	if err := db.AutoMigrate(gormDB); err != nil {
		return err
	}
	models := db.AllModels()
	fmt.Fprintf(out, "Migrated %d tables\n", len(models))

	if err := db.SeedTracks(gormDB, cfg.Tracks, os.Stderr); err != nil {
		return err
	}
	fmt.Fprintf(out, "Seeded %d tracks:", len(cfg.Tracks))
	for _, t := range cfg.Tracks {
		fmt.Fprintf(out, " %s", t.Name)
	}
	fmt.Fprintln(out)

	if err := db.SeedConfig(gormDB, cfg, os.Stderr); err != nil {
		return err
	}
	fmt.Fprintf(out, "Configuration written for owner %q\n", cfg.Owner)

	fmt.Fprintln(out, "\nRailyard database reset and re-initialized successfully.")
	return nil
}

func newDBStartCmd() *cobra.Command {
	var configPath string

	cmd := &cobra.Command{
		Use:   "start",
		Short: "Start the database server",
		Long: `Starts a local MySQL Docker container using the host/port from your config.
If the database is already running, reports success without starting another instance.
Useful after a WSL reboot or system restart when the database container has stopped.

Only manages local databases (127.0.0.1/localhost). For remote database hosts,
this command reports the connection error and exits without modifying local containers.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runDBStart(cmd, configPath)
		},
	}

	cmd.Flags().StringVarP(&configPath, "config", "c", "railyard.yaml", "path to Railyard config file")
	return cmd
}

const dbContainerName = "railyard-mysql"

// isLocalHost returns true if the host refers to the local machine.
func isLocalHost(host string) bool {
	return host == "127.0.0.1" || host == "localhost" || host == "::1" || host == ""
}

func runDBStart(cmd *cobra.Command, configPath string) error {
	out := cmd.OutOrStdout()

	cfg, err := config.Load(configPath)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	host := cfg.Database.Host
	port := cfg.Database.Port

	// Only manage local Docker containers. For remote hosts, the user is
	// responsible for ensuring the database is running.
	if !isLocalHost(host) {
		return fmt.Errorf("database host %s is not local — ry db start only manages local Docker containers.\nEnsure the remote database at %s:%d is running.", host, host, port)
	}

	// Local host: check if database is already running.
	if _, err := db.ConnectAdmin(host, port, cfg.Database.Username, cfg.Database.Password); err == nil {
		fmt.Fprintf(out, "Database is already running on %s:%d\n", host, port)
		return nil
	}

	dataDir := os.ExpandEnv("${HOME}/.railyard/mysql-data")

	// Ensure data directory exists.
	if err := os.MkdirAll(dataDir, 0755); err != nil {
		return fmt.Errorf("create data directory %s: %w", dataDir, err)
	}

	// Remove any stopped container with the same name.
	exec.Command("docker", "rm", "-f", dbContainerName).Run()

	// Start MySQL via Docker.
	// When a password is configured, set MYSQL_ROOT_PASSWORD so the
	// container's root account matches what the readiness check expects.
	var mysqlEnv string
	if cfg.Database.Password != "" {
		mysqlEnv = "MYSQL_ROOT_PASSWORD=" + cfg.Database.Password
	} else {
		mysqlEnv = "MYSQL_ALLOW_EMPTY_PASSWORD=yes"
	}
	args := []string{
		"run", "-d",
		"--name", dbContainerName,
		"-e", mysqlEnv,
		"-p", fmt.Sprintf("%d:3306", port),
		"-v", dataDir + ":/var/lib/mysql",
		"mysql:8.0",
	}
	dbCmd := exec.Command("docker", args...)
	cmdOut, err := dbCmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("start database container: %s: %w", strings.TrimSpace(string(cmdOut)), err)
	}

	fmt.Fprintf(out, "Starting MySQL container on %s:%d...\n", host, port)

	// Wait for readiness.
	for i := range 60 {
		time.Sleep(500 * time.Millisecond)
		if _, err := db.ConnectAdmin(host, port, cfg.Database.Username, cfg.Database.Password); err == nil {
			fmt.Fprintf(out, "Database is ready (took %dms)\n", (i+1)*500)
			fmt.Fprintf(out, "Container: %s (docker logs %s)\n", dbContainerName, dbContainerName)
			return nil
		}
	}

	return fmt.Errorf("database did not become ready within 30s — check: docker logs %s", dbContainerName)
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
