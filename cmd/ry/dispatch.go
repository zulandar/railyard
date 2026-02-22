package main

import (
	"fmt"
	"log"
	"os"
	"os/user"
	"time"

	"github.com/spf13/cobra"
	"github.com/zulandar/railyard/internal/config"
	"github.com/zulandar/railyard/internal/db"
	"github.com/zulandar/railyard/internal/dispatch"
	"github.com/zulandar/railyard/internal/models"
	"github.com/zulandar/railyard/internal/telegraph"
	"gorm.io/gorm"
)

func newDispatchCmd() *cobra.Command {
	var configPath string

	cmd := &cobra.Command{
		Use:   "dispatch",
		Short: "Start the Dispatch planner agent",
		Long:  "Starts an interactive Claude Code session with the Dispatch planner prompt. Acquires a dispatch lock to prevent concurrent sessions.",
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

	gormDB, err := db.Connect(cfg.Dolt.Host, cfg.Dolt.Port, cfg.Dolt.Database)
	if err != nil {
		return fmt.Errorf("connect to %s: %w", cfg.Dolt.Database, err)
	}

	// Auto-migrate dispatch session table.
	if err := gormDB.AutoMigrate(&models.DispatchSession{}); err != nil {
		return fmt.Errorf("migrate dispatch_sessions: %w", err)
	}

	// Determine the heartbeat timeout from config (or use default).
	timeout := telegraph.DefaultHeartbeatTimeout
	if cfg.Telegraph.Platform != "" && cfg.Telegraph.DispatchLock.HeartbeatTimeoutSec > 0 {
		timeout = time.Duration(cfg.Telegraph.DispatchLock.HeartbeatTimeoutSec) * time.Second
	}

	// Determine heartbeat interval.
	heartbeatInterval := 30 * time.Second
	if cfg.Telegraph.Platform != "" && cfg.Telegraph.DispatchLock.HeartbeatIntervalSec > 0 {
		heartbeatInterval = time.Duration(cfg.Telegraph.DispatchLock.HeartbeatIntervalSec) * time.Second
	}

	// Resolve current user name.
	userName := "local"
	if u, err := user.Current(); err == nil && u.Username != "" {
		userName = u.Username
	}

	// Acquire dispatch lock.
	session, err := telegraph.AcquireLock(gormDB, "local", userName, "local", "local", timeout)
	if err != nil {
		return fmt.Errorf("dispatch: %w (another dispatch session is active — use Telegraph or wait)", err)
	}

	out := cmd.OutOrStdout()
	fmt.Fprintf(out, "Dispatch lock acquired (session %d, user %s)\n", session.ID, userName)

	// Start heartbeat in background.
	stopHeartbeat := startHeartbeat(gormDB, session.ID, heartbeatInterval)
	defer stopHeartbeat()

	// Release lock when dispatch exits.
	defer func() {
		if err := telegraph.ReleaseLock(gormDB, session.ID); err != nil {
			log.Printf("dispatch: release lock: %v", err)
		} else {
			fmt.Fprintf(out, "Dispatch lock released (session %d)\n", session.ID)
		}
	}()

	repoDir, _ := os.Getwd()

	return dispatch.Start(dispatch.StartOpts{
		ConfigPath: configPath,
		Config:     cfg,
		RepoDir:    repoDir,
	})
}

// startHeartbeat runs a background goroutine that refreshes the dispatch lock
// heartbeat at the given interval. Returns a stop function.
func startHeartbeat(gormDB *gorm.DB, sessionID uint, interval time.Duration) func() {
	done := make(chan struct{})
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-ticker.C:
				if err := telegraph.Heartbeat(gormDB, sessionID); err != nil {
					log.Printf("dispatch: heartbeat: %v", err)
				}
			}
		}
	}()
	return func() { close(done) }
}
