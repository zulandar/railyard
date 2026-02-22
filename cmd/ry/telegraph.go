package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"
	"github.com/zulandar/railyard/internal/config"
	"github.com/zulandar/railyard/internal/db"
	"github.com/zulandar/railyard/internal/dispatch"
	"github.com/zulandar/railyard/internal/engine"
	"github.com/zulandar/railyard/internal/orchestration"
	"github.com/zulandar/railyard/internal/telegraph"
	discordadapter "github.com/zulandar/railyard/internal/telegraph/discord"
	slackadapter "github.com/zulandar/railyard/internal/telegraph/slack"
)

const telegraphSessionName = "railyard-telegraph"

func newTelegraphCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "telegraph",
		Aliases: []string{"tg"},
		Short:   "Manage the Telegraph chat bridge",
		Long:    "Telegraph bridges Railyard events and dispatch to chat platforms (Slack, Discord).",
	}

	cmd.AddCommand(newTelegraphStartCmd())
	cmd.AddCommand(newTelegraphStatusCmd())
	cmd.AddCommand(newTelegraphStopCmd())
	return cmd
}

func newTelegraphStartCmd() *cobra.Command {
	var configPath string

	cmd := &cobra.Command{
		Use:   "start",
		Short: "Start the Telegraph daemon",
		Long:  "Connects to the configured chat platform, listens for commands, and posts Railyard events.",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runTelegraphStart(cmd, configPath)
		},
	}

	cmd.Flags().StringVarP(&configPath, "config", "c", "railyard.yaml", "path to Railyard config file")
	return cmd
}

func newTelegraphStatusCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show Telegraph daemon status",
		Long:  "Reports whether the Telegraph daemon is running and its connection state.",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runTelegraphStatus(cmd)
		},
	}
	return cmd
}

func newTelegraphStopCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "stop",
		Short: "Stop the Telegraph daemon",
		Long:  "Sends a shutdown signal to the Telegraph tmux session.",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runTelegraphStop(cmd)
		},
	}
	return cmd
}

// tmuxForTelegraph returns the tmux interface to use. Allows test override.
var tmuxForTelegraph func() orchestration.Tmux = func() orchestration.Tmux {
	return orchestration.DefaultTmux
}

func runTelegraphStart(cmd *cobra.Command, configPath string) error {
	cfg, err := config.Load(configPath)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	if cfg.Telegraph.Platform == "" {
		return fmt.Errorf("telegraph: no platform configured in %s (add telegraph.platform)", configPath)
	}

	gormDB, err := db.Connect(cfg.Dolt.Host, cfg.Dolt.Port, cfg.Dolt.Database)
	if err != nil {
		return fmt.Errorf("connect to %s: %w", cfg.Dolt.Database, err)
	}

	adapter, err := createAdapter(cfg)
	if err != nil {
		return err
	}

	// Set up dispatch spawner: render prompt, ensure worktree, write MCP config.
	out := cmd.OutOrStdout()
	var spawner telegraph.ProcessSpawner

	dispatchPrompt, err := dispatch.RenderPrompt(cfg)
	if err != nil {
		fmt.Fprintf(out, "telegraph: dispatch prompt render failed, dispatch disabled: %v\n", err)
	} else {
		// Determine repo directory (cwd).
		repoDir, wdErr := os.Getwd()
		if wdErr != nil {
			return fmt.Errorf("telegraph: getwd: %w", wdErr)
		}

		worktreeDir, wtErr := engine.EnsureDispatchWorktree(repoDir)
		if wtErr != nil {
			fmt.Fprintf(out, "telegraph: dispatch worktree failed, dispatch disabled: %v\n", wtErr)
		} else {
			baseBranch := engine.DetectBaseBranch(repoDir, cfg.DefaultBranch)
			if syncErr := engine.SyncWorktreeToBranch(worktreeDir, baseBranch); syncErr != nil {
				log.Printf("telegraph: sync worktree to %s: %v (continuing anyway)", baseBranch, syncErr)
			}

			// Write MCP config (non-fatal).
			if mcpErr := dispatch.WriteDispatchMCPConfig(worktreeDir, cfg); mcpErr != nil {
				log.Printf("telegraph: write dispatch MCP config: %v (continuing without MCP)", mcpErr)
			}

			spawner = &telegraph.ClaudeSpawner{
				SystemPrompt: dispatchPrompt,
				WorkDir:      worktreeDir,
			}
			fmt.Fprintf(out, "telegraph: dispatch enabled (worktree: %s)\n", worktreeDir)
		}
	}

	daemon, err := telegraph.NewDaemon(telegraph.DaemonOpts{
		DB:      gormDB,
		Config:  cfg,
		Adapter: adapter,
		Spawner: spawner,
		Out:     out,
	})
	if err != nil {
		return err
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Handle OS signals for graceful shutdown.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		cancel()
	}()

	return daemon.Run(ctx)
}

// createAdapter builds a platform adapter from the config.
func createAdapter(cfg *config.Config) (telegraph.Adapter, error) {
	switch cfg.Telegraph.Platform {
	case "slack":
		return slackadapter.New(slackadapter.AdapterOpts{
			AppToken:  cfg.Telegraph.Slack.AppToken,
			BotToken:  cfg.Telegraph.Slack.BotToken,
			ChannelID: cfg.Telegraph.Channel,
		})
	case "discord":
		return discordadapter.New(discordadapter.AdapterOpts{
			BotToken:  cfg.Telegraph.Discord.BotToken,
			ChannelID: cfg.Telegraph.Channel,
		})
	default:
		return nil, fmt.Errorf("telegraph: unsupported platform %q", cfg.Telegraph.Platform)
	}
}

func runTelegraphStatus(cmd *cobra.Command) error {
	tmux := tmuxForTelegraph()
	out := cmd.OutOrStdout()

	running := tmux.SessionExists(telegraphSessionName)
	if running {
		fmt.Fprintf(out, "Telegraph: RUNNING (session: %s)\n", telegraphSessionName)
	} else {
		fmt.Fprintf(out, "Telegraph: STOPPED\n")
	}
	return nil
}

func runTelegraphStop(cmd *cobra.Command) error {
	tmux := tmuxForTelegraph()
	out := cmd.OutOrStdout()

	if !tmux.SessionExists(telegraphSessionName) {
		return fmt.Errorf("telegraph: no telegraph session running")
	}

	// Send C-c to all panes in the telegraph session.
	panes, err := tmux.ListPanes(telegraphSessionName)
	if err == nil {
		for _, p := range panes {
			_ = tmux.SendSignal(p, "C-c")
		}
	}

	fmt.Fprintf(out, "Telegraph shutdown signal sent to session %s\n", telegraphSessionName)
	return nil
}
