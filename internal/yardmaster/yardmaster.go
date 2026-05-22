// Package yardmaster implements the supervisor agent.
package yardmaster

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/zulandar/railyard/internal/config"
	"github.com/zulandar/railyard/internal/events"
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
	// Bus is the optional plugin event bus. When non-nil, the daemon publishes
	// lifecycle events (YardmasterAction, CarMerged, MergeFailed) to it. A nil
	// bus disables publishing — existing callers in cmd/ry/ that omit this
	// field continue to work unchanged.
	Bus events.Bus
	// PluginStatus, when non-nil, is the source for GET /plugins/status
	// served by the embedded HealthServer. *pluginhost.Host satisfies it.
	// nil disables the route (handler returns an empty Snapshot).
	PluginStatus StatusProvider
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

	return RunDaemonWithBus(ctx, opts.DB, opts.Config, opts.ConfigPath, opts.RepoDir, opts.PollInterval, logger, opts.Bus, opts.PluginStatus)
}
