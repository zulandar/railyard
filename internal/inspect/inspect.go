package inspect

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/zulandar/railyard/internal/config"
	"gorm.io/gorm"
)

// StartOpts holds the arguments for Start.
type StartOpts struct {
	ConfigPath string
	Config     *config.Config
	DB         *gorm.DB
	Logger     *slog.Logger
}

// Start validates configuration and launches the inspect daemon.
func Start(ctx context.Context, opts StartOpts) error {
	if opts.Config == nil {
		return fmt.Errorf("inspect: config is required")
	}
	if !opts.Config.Inspect.Enabled {
		return fmt.Errorf("inspect: not enabled in config")
	}
	if opts.DB == nil {
		return fmt.Errorf("inspect: database is required")
	}

	logger := opts.Logger
	if logger == nil {
		logger = slog.Default()
	}

	// Create GitHub client from config.
	ghClient, err := NewGitHubClient(opts.Config.Owner, opts.Config.Repo, opts.Config.Inspect)
	if err != nil {
		return fmt.Errorf("inspect: create github client: %w", err)
	}

	// Create store from DB.
	store := NewStore(opts.DB)

	// Create AI provider from config.
	ai, err := NewProviderAI(opts.Config.Inspect.AgentProvider)
	if err != nil {
		return fmt.Errorf("inspect: create AI provider: %w", err)
	}

	// Resolve bot login for comment filtering and review dismissal.
	botLogin, err := ghClient.GetBotLogin(ctx)
	if err != nil {
		logger.Warn("inspect: could not resolve bot login, comment counting will be inaccurate", "error", err)
	} else {
		logger.Info("inspect: resolved bot login", "login", botLogin)
	}

	// Get replica ID from hostname.
	replicaID, err := os.Hostname()
	if err != nil {
		replicaID = "unknown"
	}

	// Derive poll interval.
	pollInterval := time.Duration(opts.Config.Inspect.PollIntervalSec) * time.Second

	return RunDaemon(ctx, ghClient, store, DaemonOpts{
		Config:       opts.Config.Inspect,
		Tracks:       opts.Config.Tracks,
		ReplicaID:    replicaID,
		PollInterval: pollInterval,
		AI:           ai,
		BotLogin:     botLogin,
		Logger:       logger,
	})
}
