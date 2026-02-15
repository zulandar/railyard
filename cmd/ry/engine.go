package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"
	"github.com/zulandar/railyard/internal/bead"
	"github.com/zulandar/railyard/internal/config"
	"github.com/zulandar/railyard/internal/db"
	"github.com/zulandar/railyard/internal/engine"
	"github.com/zulandar/railyard/internal/messaging"
	"github.com/zulandar/railyard/internal/models"
	"github.com/zulandar/railyard/internal/orchestration"
	"gorm.io/gorm"
)

const defaultPollInterval = 5 * time.Second

func newEngineCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "engine",
		Short: "Engine daemon commands",
	}

	cmd.AddCommand(newEngineStartCmd())
	cmd.AddCommand(newEngineScaleCmd())
	cmd.AddCommand(newEngineListCmd())
	cmd.AddCommand(newEngineRestartCmd())
	return cmd
}

func newEngineStartCmd() *cobra.Command {
	var (
		configPath   string
		track        string
		pollInterval time.Duration
	)

	cmd := &cobra.Command{
		Use:   "start",
		Short: "Start the engine daemon",
		Long:  "Starts the engine daemon loop: claims beads, spawns Claude Code, monitors subprocess, handles outcomes.",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runEngineStart(cmd, configPath, track, pollInterval)
		},
	}

	cmd.Flags().StringVarP(&configPath, "config", "c", "railyard.yaml", "path to Railyard config file")
	cmd.Flags().StringVarP(&track, "track", "t", "", "track to work on (required)")
	cmd.Flags().DurationVar(&pollInterval, "poll-interval", defaultPollInterval, "interval between claim attempts")
	_ = cmd.MarkFlagRequired("track")
	return cmd
}

func runEngineStart(cmd *cobra.Command, configPath, track string, pollInterval time.Duration) error {
	out := cmd.OutOrStdout()

	cfg, err := config.Load(configPath)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	// Validate that the track exists in config.
	var trackCfg *config.TrackConfig
	for i := range cfg.Tracks {
		if cfg.Tracks[i].Name == track {
			trackCfg = &cfg.Tracks[i]
			break
		}
	}
	if trackCfg == nil {
		return fmt.Errorf("track %q not found in config", track)
	}

	gormDB, err := db.Connect(cfg.Dolt.Host, cfg.Dolt.Port, cfg.Dolt.Database)
	if err != nil {
		return fmt.Errorf("connect to %s: %w", cfg.Dolt.Database, err)
	}

	// Load the track model from DB for context rendering.
	var trackModel models.Track
	if err := gormDB.Where("name = ?", track).First(&trackModel).Error; err != nil {
		return fmt.Errorf("load track %q from database: %w", track, err)
	}

	// Register the engine.
	eng, err := engine.Register(gormDB, engine.RegisterOpts{Track: track})
	if err != nil {
		return fmt.Errorf("register engine: %w", err)
	}
	fmt.Fprintf(out, "Engine %s registered on track %q\n", eng.ID, track)

	// Set up context with signal handling for clean shutdown.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		sig := <-sigCh
		fmt.Fprintf(out, "\nReceived %s, shutting down...\n", sig)
		cancel()
	}()

	// Start heartbeat.
	hbErrCh := engine.StartHeartbeat(ctx, gormDB, eng.ID, engine.DefaultHeartbeatInterval)

	// Build stall config from config file.
	stallCfg := engine.StallConfig{
		StdoutTimeout:    time.Duration(cfg.Stall.StdoutTimeoutSec) * time.Second,
		RepeatedErrorMax: cfg.Stall.RepeatedErrorMax,
		MaxClearCycles:   cfg.Stall.MaxClearCycles,
	}

	// Determine working directory (repo root).
	repoDir, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("get working directory: %w", err)
	}

	fmt.Fprintf(out, "Engine %s starting daemon loop (poll every %s)...\n", eng.ID, pollInterval)

	cycle := 0

	for {
		select {
		case <-ctx.Done():
			fmt.Fprintf(out, "Engine %s deregistering...\n", eng.ID)
			if err := engine.Deregister(gormDB, eng.ID); err != nil {
				log.Printf("deregister error: %v", err)
			}
			fmt.Fprintf(out, "Engine %s stopped.\n", eng.ID)
			return nil
		case err := <-hbErrCh:
			fmt.Fprintf(out, "Heartbeat error: %v\n", err)
			return fmt.Errorf("heartbeat: %w", err)
		default:
		}

		// Process inbox — check for yardmaster instructions.
		instructions, inboxErr := engine.ProcessInbox(gormDB, eng.ID)
		if inboxErr != nil {
			log.Printf("inbox error: %v", inboxErr)
		}

		// Handle pause instruction.
		if engine.ShouldPause(instructions) {
			fmt.Fprintf(out, "Paused by yardmaster. Waiting for resume...\n")
			for {
				sleepWithContext(ctx, pollInterval)
				if ctx.Err() != nil {
					break
				}
				resumeInst, _ := engine.ProcessInbox(gormDB, eng.ID)
				if engine.HasResume(resumeInst) {
					fmt.Fprintf(out, "Resumed by yardmaster.\n")
					break
				}
			}
			continue
		}

		// Handle abort instruction for current bead.
		if eng.CurrentBead != "" && engine.ShouldAbort(instructions, eng.CurrentBead) {
			fmt.Fprintf(out, "Abort instruction received for bead %s\n", eng.CurrentBead)
			gormDB.Model(&models.Engine{}).Where("id = ?", eng.ID).Updates(map[string]interface{}{
				"current_bead": "",
				"status":       engine.StatusIdle,
			})
			eng.CurrentBead = ""
			cycle = 0
			continue
		}

		// Try to claim a bead (or re-claim current if mid-cycle).
		claimed, err := claimOrReclaim(gormDB, eng, track)
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				// No ready beads — sleep and retry.
				sleepWithContext(ctx, pollInterval)
				continue
			}
			log.Printf("claim error: %v", err)
			sleepWithContext(ctx, pollInterval)
			continue
		}

		cycle++
		fmt.Fprintf(out, "[cycle %d] Claimed bead %s: %s\n", cycle, claimed.ID, claimed.Title)

		// Render context.
		progress, _ := loadProgress(gormDB, claimed.ID)
		messages, _ := loadMessages(gormDB, eng.ID)
		commits, _ := engine.RecentCommits(repoDir, claimed.Branch, 10)

		contextPayload, err := engine.RenderContext(engine.ContextInput{
			Bead:          claimed,
			Track:         &trackModel,
			Config:        cfg,
			Progress:      progress,
			Messages:      messages,
			RecentCommits: commits,
		})
		if err != nil {
			log.Printf("render context error: %v", err)
			sleepWithContext(ctx, pollInterval)
			continue
		}

		// Create git branch.
		if err := engine.CreateBranch(repoDir, claimed.Branch); err != nil {
			log.Printf("create branch error: %v", err)
			sleepWithContext(ctx, pollInterval)
			continue
		}

		// Spawn Claude Code.
		sess, err := engine.SpawnAgent(ctx, gormDB, engine.SpawnOpts{
			EngineID:       eng.ID,
			BeadID:         claimed.ID,
			ContextPayload: contextPayload,
			WorkDir:        repoDir,
		})
		if err != nil {
			log.Printf("spawn error: %v", err)
			sleepWithContext(ctx, pollInterval)
			continue
		}

		fmt.Fprintf(out, "[cycle %d] Spawned session %s (PID %d)\n", cycle, sess.ID, sess.PID)

		// Start stall detection.
		sd := engine.NewStallDetector(sess, stallCfg)
		sd.SetCycle(cycle)
		sd.Start(ctx)

		// Monitor: wait for subprocess exit or stall.
		outcome := monitorSession(ctx, sess, sd)
		sd.Stop()

		switch outcome.kind {
		case outcomeCompleted:
			fmt.Fprintf(out, "[cycle %d] Bead %s completed\n", cycle, claimed.ID)
			if err := engine.HandleCompletion(gormDB, claimed, eng, engine.CompletionOpts{
				RepoDir:   repoDir,
				SessionID: sess.ID,
			}); err != nil {
				log.Printf("completion handling error: %v", err)
			}
			// Reset cycle for next bead.
			cycle = 0

		case outcomeClear:
			fmt.Fprintf(out, "[cycle %d] Agent exited (clear cycle), will re-claim\n", cycle)
			if err := engine.HandleClearCycle(gormDB, claimed, eng, engine.ClearCycleOpts{
				RepoDir:   repoDir,
				SessionID: sess.ID,
				Cycle:     cycle,
			}); err != nil {
				log.Printf("clear cycle handling error: %v", err)
			}

		case outcomeStall:
			fmt.Fprintf(out, "[cycle %d] Stall detected: %s\n", cycle, outcome.stallReason.Detail)
			if err := engine.HandleStall(gormDB, eng.ID, claimed.ID, outcome.stallReason); err != nil {
				log.Printf("stall handling error: %v", err)
			}
			// Reset cycle — bead is now blocked, engine should move on.
			cycle = 0

		case outcomeCancelled:
			fmt.Fprintf(out, "[cycle %d] Cancelled, shutting down\n", cycle)
			if err := engine.Deregister(gormDB, eng.ID); err != nil {
				log.Printf("deregister error: %v", err)
			}
			return nil
		}

		sleepWithContext(ctx, pollInterval)
	}
}

// outcomeKind describes how a subprocess session ended.
type outcomeKind int

const (
	outcomeCompleted  outcomeKind = iota // bead marked done
	outcomeClear                         // agent exited, bead not done
	outcomeStall                         // stall detected
	outcomeCancelled                     // context cancelled (shutdown)
)

type sessionOutcome struct {
	kind        outcomeKind
	stallReason engine.StallReason
}

// monitorSession waits for the subprocess to exit, a stall, or context cancellation.
func monitorSession(ctx context.Context, sess *engine.Session, sd *engine.StallDetector) sessionOutcome {
	select {
	case <-ctx.Done():
		return sessionOutcome{kind: outcomeCancelled}

	case reason := <-sd.Stalled():
		return sessionOutcome{kind: outcomeStall, stallReason: reason}

	case err := <-sess.Done():
		if err != nil {
			// Non-zero exit — treat as clear cycle (agent exited without completing).
			return sessionOutcome{kind: outcomeClear}
		}
		// Zero exit — agent finished. Could be completion or normal exit.
		// We treat zero exit as completion (the agent calls ry complete before exiting).
		return sessionOutcome{kind: outcomeCompleted}
	}
}

// claimOrReclaim either claims a new bead or re-claims the engine's current bead.
func claimOrReclaim(gormDB *gorm.DB, eng *models.Engine, track string) (*models.Bead, error) {
	// Check if engine already has a bead assigned (re-claim after clear cycle).
	if eng.CurrentBead != "" {
		b, err := bead.Get(gormDB, eng.CurrentBead)
		if err == nil && b.Status != "done" && b.Status != "cancelled" {
			return b, nil
		}
		// Clear stale current_bead.
		gormDB.Model(&models.Engine{}).Where("id = ?", eng.ID).Update("current_bead", "")
		eng.CurrentBead = ""
	}

	return engine.ClaimBead(gormDB, eng.ID, track)
}

// loadProgress retrieves progress notes for a bead.
func loadProgress(gormDB *gorm.DB, beadID string) ([]models.BeadProgress, error) {
	var progress []models.BeadProgress
	err := gormDB.Where("bead_id = ?", beadID).Order("created_at ASC").Find(&progress).Error
	return progress, err
}

// loadMessages retrieves unacknowledged messages for an engine.
func loadMessages(gormDB *gorm.DB, engineID string) ([]models.Message, error) {
	return messaging.Inbox(gormDB, engineID)
}

// sleepWithContext sleeps for the given duration but returns early if ctx is cancelled.
func sleepWithContext(ctx context.Context, d time.Duration) {
	select {
	case <-ctx.Done():
	case <-time.After(d):
	}
}

func newEngineScaleCmd() *cobra.Command {
	var (
		configPath string
		track      string
		count      int
	)

	cmd := &cobra.Command{
		Use:   "scale",
		Short: "Scale engine count for a track",
		Long:  "Adjusts the number of engines running on a specific track. Scale up creates new tmux panes; scale down drains newest engines first.",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runEngineScale(cmd, configPath, track, count)
		},
	}

	cmd.Flags().StringVarP(&configPath, "config", "c", "railyard.yaml", "path to Railyard config file")
	cmd.Flags().StringVar(&track, "track", "", "track to scale (required)")
	cmd.Flags().IntVar(&count, "count", 0, "desired engine count (required)")
	_ = cmd.MarkFlagRequired("track")
	_ = cmd.MarkFlagRequired("count")
	return cmd
}

func runEngineScale(cmd *cobra.Command, configPath, track string, count int) error {
	cfg, gormDB, err := connectFromConfig(configPath)
	if err != nil {
		return err
	}

	result, err := orchestration.Scale(orchestration.ScaleOpts{
		DB:         gormDB,
		Config:     cfg,
		ConfigPath: configPath,
		Track:      track,
		Count:      count,
	})
	if err != nil {
		return err
	}

	out := cmd.OutOrStdout()
	fmt.Fprintf(out, "Track %s: %d → %d engines\n", result.Track, result.Previous, result.Current)
	if len(result.PanesCreated) > 0 {
		fmt.Fprintf(out, "  Created %d new engine panes\n", len(result.PanesCreated))
	}
	if len(result.PanesKilled) > 0 {
		fmt.Fprintf(out, "  Removed %d engines\n", len(result.PanesKilled))
	}
	return nil
}

func newEngineListCmd() *cobra.Command {
	var (
		configPath   string
		track        string
		statusFilter string
	)

	cmd := &cobra.Command{
		Use:   "list",
		Short: "List engines",
		Long:  "Displays all engines with ID, track, status, current bead, last activity, and uptime.",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runEngineList(cmd, configPath, track, statusFilter)
		},
	}

	cmd.Flags().StringVarP(&configPath, "config", "c", "railyard.yaml", "path to Railyard config file")
	cmd.Flags().StringVar(&track, "track", "", "filter by track")
	cmd.Flags().StringVar(&statusFilter, "status", "", "filter by status")
	return cmd
}

func runEngineList(cmd *cobra.Command, configPath, track, statusFilter string) error {
	_, gormDB, err := connectFromConfig(configPath)
	if err != nil {
		return err
	}

	engines, err := orchestration.ListEngines(orchestration.EngineListOpts{
		DB:     gormDB,
		Track:  track,
		Status: statusFilter,
	})
	if err != nil {
		return err
	}

	out := cmd.OutOrStdout()
	if len(engines) == 0 {
		fmt.Fprintln(out, "No engines found.")
		return nil
	}

	w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "ID\tTRACK\tSTATUS\tCURRENT BEAD\tLAST ACTIVITY\tUPTIME")
	for _, e := range engines {
		bead := e.CurrentBead
		if bead == "" {
			bead = "-"
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n",
			e.ID, e.Track, e.Status, bead,
			e.LastActivity.Format("15:04:05"),
			formatUptime(e.Uptime))
	}
	w.Flush()
	return nil
}

func newEngineRestartCmd() *cobra.Command {
	var configPath string

	cmd := &cobra.Command{
		Use:   "restart <engine-id>",
		Short: "Restart an engine",
		Long:  "Restart an engine: kills it and creates a new one on the same track with a new ID.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runEngineRestart(cmd, configPath, args[0])
		},
	}

	cmd.Flags().StringVarP(&configPath, "config", "c", "railyard.yaml", "path to Railyard config file")
	return cmd
}

func runEngineRestart(cmd *cobra.Command, configPath, engineID string) error {
	_, gormDB, err := connectFromConfig(configPath)
	if err != nil {
		return err
	}

	if err := orchestration.RestartEngine(gormDB, configPath, engineID, nil); err != nil {
		return err
	}

	fmt.Fprintf(cmd.OutOrStdout(), "Engine %s restarted.\n", engineID)
	return nil
}

// formatUptime formats a duration as "Xh Ym" or "Ym Zs".
func formatUptime(d time.Duration) string {
	h := int(d.Hours())
	m := int(d.Minutes()) % 60
	s := int(d.Seconds()) % 60
	if h > 0 {
		return fmt.Sprintf("%dh %dm", h, m)
	}
	return fmt.Sprintf("%dm %ds", m, s)
}
