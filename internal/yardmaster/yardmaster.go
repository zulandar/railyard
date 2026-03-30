// Package yardmaster implements the supervisor agent.
package yardmaster

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/zulandar/railyard/internal/config"
	"gorm.io/gorm"
)

// StartOpts holds parameters for starting the Yardmaster daemon.
type StartOpts struct {
	ConfigPath   string
	Config       *config.Config
	DB           *gorm.DB
	RepoDir      string
	PollInterval time.Duration // default 30s
	Logger       *slog.Logger  // default slog.Default()
}

// Start launches the yardmaster daemon loop. It validates options, then
// delegates to RunDaemon which handles registration, heartbeat, and the
// four-phase monitoring loop.
func Start(ctx context.Context, opts StartOpts) error {
	if opts.Config == nil {
		return fmt.Errorf("yardmaster: config is required")
	}
	if len(opts.Config.Tracks) == 0 {
		return fmt.Errorf("yardmaster: at least one track must be configured")
	}
	if opts.DB == nil {
		return fmt.Errorf("yardmaster: db is required")
	}
	if opts.RepoDir == "" {
		return fmt.Errorf("yardmaster: repoDir is required")
	}

	logger := opts.Logger
	if logger == nil {
		logger = slog.Default()
	}

	return RunDaemon(ctx, opts.DB, opts.Config, opts.ConfigPath, opts.RepoDir, opts.PollInterval, logger)
}
