package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"
	"github.com/zulandar/railyard/internal/car"
	"github.com/zulandar/railyard/internal/config"
	"github.com/zulandar/railyard/internal/db"
	"github.com/zulandar/railyard/internal/engine"
	_ "github.com/zulandar/railyard/internal/engine/providers" // register agent providers
	"github.com/zulandar/railyard/internal/logutil"
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
	cmd.AddCommand(newEngineDrainCmd())
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
		logLevel     string
	)

	cmd := &cobra.Command{
		Use:   "start",
		Short: "Start the engine daemon",
		Long:  "Starts the engine daemon loop: claims cars, spawns Claude Code, monitors subprocess, handles outcomes.",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runEngineStart(cmd, configPath, track, pollInterval, logLevel)
		},
	}

	cmd.Flags().StringVarP(&configPath, "config", "c", "railyard.yaml", "path to Railyard config file")
	cmd.Flags().StringVarP(&track, "track", "t", "", "track to work on (required)")
	cmd.Flags().DurationVar(&pollInterval, "poll-interval", defaultPollInterval, "interval between claim attempts")
	cmd.Flags().StringVar(&logLevel, "log-level", "", "log level (debug, info, warn, error; env LOG_LEVEL)")
	_ = cmd.MarkFlagRequired("track")
	return cmd
}

func runEngineStart(cmd *cobra.Command, configPath, track string, pollInterval time.Duration, logLevel string) error {
	level := logutil.ParseLevel(os.Getenv("LOG_LEVEL"), logLevel)
	logger := logutil.NewLogger(cmd.OutOrStdout(), cmd.ErrOrStderr(), level)

	cfg, err := config.Load(configPath)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	// Sync embedded CocoIndex scripts to disk so overlay/MCP operations
	// find them without requiring a prior 'ry cocoindex init'.
	if err := ensureCocoIndexScripts(cfg.CocoIndex.ScriptsPath); err != nil {
		logger.Warn("Cocoindex scripts sync warning", "error", err)
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

	gormDB, err := db.Connect(cfg.Database.Host, cfg.Database.Port, cfg.Database.Database, cfg.Database.Username, cfg.Database.Password)
	if err != nil {
		return fmt.Errorf("connect to %s: %w", cfg.Database.Database, err)
	}

	// Ensure schema is up to date (adds any new columns from model changes).
	if err := db.AutoMigrate(gormDB); err != nil {
		return fmt.Errorf("auto-migrate: %w", err)
	}

	// Seed tracks and config so fresh installs work without a separate db-init step.
	if err := db.SeedTracks(gormDB, cfg.Tracks, os.Stderr); err != nil {
		return fmt.Errorf("seed tracks: %w", err)
	}
	if err := db.SeedConfig(gormDB, cfg, os.Stderr); err != nil {
		return fmt.Errorf("seed config: %w", err)
	}

	// Load the track model from DB for context rendering.
	var trackModel models.Track
	if err := gormDB.Where("name = ?", track).First(&trackModel).Error; err != nil {
		return fmt.Errorf("load track %q from database: %w", track, err)
	}

	// Resolve agent provider from track config.
	providerName := trackCfg.AgentProvider
	if providerName == "" {
		providerName = cfg.AgentProvider
	}
	if providerName == "" {
		providerName = "claude"
	}

	// Register the engine.
	eng, err := engine.Register(gormDB, engine.RegisterOpts{Track: track, Provider: providerName})
	if err != nil {
		return fmt.Errorf("register engine: %w", err)
	}
	logger.Info("Engine registered", "engine", eng.ID, "track", track, "provider", providerName)

	// Set up context with signal handling for clean shutdown.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		sig := <-sigCh
		logger.Info("Received signal, shutting down", "signal", sig.String())
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

	// Create a dedicated git worktree for this engine.
	workDir, err := engine.EnsureWorktree(repoDir, eng.ID)
	if err != nil {
		return fmt.Errorf("setup worktree: %w", err)
	}

	logger.Info("Engine starting daemon loop", "engine", eng.ID, "poll", pollInterval)

	cycle := 0
	var lastIdleLog time.Time
	var claimTime time.Time

	type cycleStats struct {
		startedAt   time.Time
		completed   int
		stalled     int
		cleared     int
		totalTokens int
		totalDur    time.Duration
	}
	var cStats cycleStats

	for {
		select {
		case <-ctx.Done():
			logger.Info("Engine deregistering", "engine", eng.ID)
			pushInflightBranch(gormDB, eng, workDir)
			if err := engine.CleanupOverlay(eng.ID, cfg); err != nil {
				logger.Warn("Overlay cleanup warning", "error", err)
			}
			if err := engine.Deregister(gormDB, eng.ID); err != nil {
				logger.Error("Deregister error", "error", err)
			}
			if err := engine.RemoveWorktree(repoDir, eng.ID); err != nil {
				logger.Warn("Remove worktree error", "error", err)
			}
			logger.Info("Engine stopped", "engine", eng.ID)
			return nil
		case err := <-hbErrCh:
			logger.Error("Heartbeat error", "error", err)
			return fmt.Errorf("heartbeat: %w", err)
		default:
		}

		// Process inbox — check for yardmaster instructions.
		instructions, inboxErr := engine.ProcessInbox(gormDB, eng.ID)
		if inboxErr != nil {
			logger.Error("Inbox error", "error", inboxErr)
		}

		// Handle pause instruction.
		if engine.ShouldPause(instructions) {
			logger.Info("Paused by yardmaster, waiting for resume")
			for {
				sleepWithContext(ctx, pollInterval)
				if ctx.Err() != nil {
					break
				}
				resumeInst, _ := engine.ProcessInbox(gormDB, eng.ID)
				if engine.HasResume(resumeInst) {
					logger.Info("Resumed by yardmaster")
					break
				}
			}
			continue
		}

		// Handle abort instruction for current car.
		if eng.CurrentCar != "" && engine.ShouldAbort(instructions, eng.CurrentCar) {
			logger.Info("Abort instruction received", "car", eng.CurrentCar)
			gormDB.Model(&models.Engine{}).Where("id = ?", eng.ID).Updates(map[string]interface{}{
				"current_car": "",
				"status":      engine.StatusIdle,
			})
			eng.CurrentCar = ""
			cycle = 0
			continue
		}

		// Try to claim a car (or re-claim current if mid-cycle).
		claimed, err := claimOrReclaim(gormDB, eng, track)
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				// No ready cars — sleep and retry.
				if time.Since(lastIdleLog) >= 30*time.Second {
					logger.Info("No cars available, polling")
					lastIdleLog = time.Now()
				}
				sleepWithContext(ctx, pollInterval)
				continue
			}
			logger.Error("Claim error", "error", err)
			sleepWithContext(ctx, pollInterval)
			continue
		}

		cycle++
		lastIdleLog = time.Time{}
		claimTime = time.Now()
		if cStats.startedAt.IsZero() {
			cStats.startedAt = claimTime
		}
		cycleLog := logger.With("cycle", cycle)
		cycleLog.Info("Claimed car", "car", claimed.ID, "title", claimed.Title)

		// Render context.
		progress, _ := loadProgress(gormDB, claimed.ID)
		messages, _ := loadMessages(gormDB, eng.ID)
		commits, _ := engine.RecentCommits(workDir, claimed.Branch, 10)

		contextPayload, err := engine.RenderContext(engine.ContextInput{
			Car:           claimed,
			Track:         &trackModel,
			Config:        cfg,
			Progress:      progress,
			Messages:      messages,
			RecentCommits: commits,
			EngineID:      eng.ID,
		})
		if err != nil {
			logger.Error("Render context error", "error", err)
			sleepWithContext(ctx, pollInterval)
			continue
		}

		// Set up git branch — revision cars resume existing branch, new cars branch off base.
		isRevision := claimed.CompletedAt != nil && claimed.Branch != "" && engine.RemoteBranchExists(workDir, claimed.Branch)
		if isRevision {
			logger.Info("Revision car, checking out existing branch", "car", claimed.ID, "branch", claimed.Branch)
			if err := engine.CheckoutExistingBranch(workDir, claimed.Branch); err != nil {
				logger.Warn("Checkout existing branch error, falling back to new branch", "error", err)
				isRevision = false
			}
		}
		if !isRevision {
			// Reset worktree to clean state at the car's base branch before branching.
			if err := engine.ResetWorktree(workDir, claimed.BaseBranch); err != nil {
				logger.Error("Reset worktree error", "error", err)
				sleepWithContext(ctx, pollInterval)
				continue
			}

			// Create git branch from HEAD (ResetWorktree already set HEAD to origin/{baseBranch}).
			if err := engine.CreateBranch(workDir, claimed.Branch, ""); err != nil {
				logger.Error("Create branch error", "error", err)
				sleepWithContext(ctx, pollInterval)
				continue
			}
		}

		// Build overlay index and write MCP config (non-fatal).
		if cfg.CocoIndex.Overlay.Enabled {
			if overlayTable, err := engine.BuildOverlay(workDir, eng.ID, track, cfg); err != nil {
				logger.Warn("Overlay build warning", "error", err)
			} else if overlayTable != "" {
				gormDB.Model(&models.Engine{}).Where("id = ?", eng.ID).Update("overlay_table", overlayTable)
				eng.OverlayTable = overlayTable
			}
			if err := engine.WriteMCPConfig(workDir, eng.ID, track, cfg); err != nil {
				logger.Warn("MCP config warning", "error", err)
			}
		}

		// Spawn agent subprocess.
		sess, err := engine.SpawnAgent(ctx, gormDB, engine.SpawnOpts{
			EngineID:       eng.ID,
			CarID:          claimed.ID,
			ContextPayload: contextPayload,
			WorkDir:        workDir,
			ProviderName:   providerName,
		})
		if err != nil {
			logger.Error("Spawn error", "error", err)
			sleepWithContext(ctx, pollInterval)
			continue
		}

		cycleLog.Info("Spawned session", "session", sess.ID, "pid", sess.PID)

		// Start stall detection.
		sd := engine.NewStallDetector(sess, stallCfg)
		sd.SetCycle(cycle)
		sd.Start(ctx)

		// Monitor: wait for subprocess exit or stall.
		outcome := monitorSession(ctx, sess, sd, gormDB, claimed.ID)
		sd.Stop()

		switch outcome.kind {
		case outcomeCompleted:
			stats := queryCarOutcomeStats(gormDB, claimed.ID, claimed.Branch, claimed.BaseBranch, workDir, claimTime)
			cStats.completed++
			cStats.totalTokens += stats.totalTokens
			cStats.totalDur += stats.duration
			attrs := []any{"car", claimed.ID, "duration", stats.duration, "tokens", formatTokens(stats.totalTokens)}
			if stats.model != "" {
				attrs = append(attrs, "model", stats.model)
			}
			if stats.commits > 0 {
				attrs = append(attrs, "commits", stats.commits)
			}
			cycleLog.Info("Car completed", attrs...)
			if err := engine.HandleCompletion(gormDB, claimed, eng, engine.CompletionOpts{
				RepoDir:   workDir,
				SessionID: sess.ID,
			}); err != nil {
				logger.Error("Completion handling error", "car", claimed.ID, "error", err)
				handleCompletionFailure(gormDB, claimed.ID, eng.ID, sess.ID, err)
			}
			// Emit cycle summary and reset.
			if cycle > 0 && (cStats.completed+cStats.stalled) > 0 {
				total := cStats.completed + cStats.stalled
				logger.Info("Cycle complete",
					"cycle", cycle,
					"cars", total,
					"ok", cStats.completed,
					"stalled", cStats.stalled,
					"duration", cStats.totalDur,
					"tokens", formatTokens(cStats.totalTokens),
				)
				cStats = cycleStats{}
			}
			cycle = 0

		case outcomeClear:
			cStats.cleared++
			cycleLog.Debug("Agent exited, clear cycle, will re-claim")
			if err := engine.HandleClearCycle(gormDB, claimed, eng, engine.ClearCycleOpts{
				RepoDir:   workDir,
				SessionID: sess.ID,
				Cycle:     cycle,
			}); err != nil {
				logger.Error("Clear cycle handling error", "car", claimed.ID, "error", err)
			}

		case outcomeStall:
			stats := queryCarOutcomeStats(gormDB, claimed.ID, claimed.Branch, claimed.BaseBranch, workDir, claimTime)
			cStats.stalled++
			cStats.totalTokens += stats.totalTokens
			cStats.totalDur += stats.duration
			stallAttrs := []any{
				"car", claimed.ID,
				"reason", outcome.stallReason.Detail,
				"type", outcome.stallReason.Type,
				"duration", stats.duration,
				"tokens", formatTokens(stats.totalTokens),
			}
			if stats.model != "" {
				stallAttrs = append(stallAttrs, "model", stats.model)
			}
			if stats.commits > 0 {
				stallAttrs = append(stallAttrs, "commits", stats.commits)
			}
			cycleLog.Warn("Stall detected", stallAttrs...)
			if err := engine.HandleStall(gormDB, eng.ID, claimed.ID, outcome.stallReason, workDir, claimed.Branch); err != nil {
				logger.Error("Stall handling error", "car", claimed.ID, "error", err)
			}
			// Clear current_car so the engine doesn't re-claim the now-blocked car.
			gormDB.Model(&models.Engine{}).Where("id = ?", eng.ID).Update("current_car", "")
			eng.CurrentCar = ""
			// Emit cycle summary and reset.
			if cycle > 0 && (cStats.completed+cStats.stalled) > 0 {
				total := cStats.completed + cStats.stalled
				logger.Info("Cycle complete",
					"cycle", cycle,
					"cars", total,
					"ok", cStats.completed,
					"stalled", cStats.stalled,
					"duration", cStats.totalDur,
					"tokens", formatTokens(cStats.totalTokens),
				)
				cStats = cycleStats{}
			}
			// Reset cycle — car is now blocked, engine should move on.
			cycle = 0

		case outcomeCancelled:
			cycleLog.Info("Cancelled, shutting down")
			pushInflightBranch(gormDB, eng, workDir)
			if err := engine.CleanupOverlay(eng.ID, cfg); err != nil {
				logger.Warn("Overlay cleanup warning", "error", err)
			}
			if err := engine.Deregister(gormDB, eng.ID); err != nil {
				logger.Error("Deregister error", "error", err)
			}
			if err := engine.RemoveWorktree(repoDir, eng.ID); err != nil {
				logger.Warn("Remove worktree error", "error", err)
			}
			return nil
		}

		sleepWithContext(ctx, pollInterval)
	}
}

// formatTokens formats a token count as "1.2k" for counts >= 1000, or plain integer otherwise.
func formatTokens(n int) string {
	if n >= 1000 {
		return fmt.Sprintf("%.1fk", float64(n)/1000)
	}
	return strconv.Itoa(n)
}

// carOutcomeStats holds statistics about a completed or stalled car.
type carOutcomeStats struct {
	duration    time.Duration
	totalTokens int
	model       string
	commits     int
}

// queryCarOutcomeStats gathers duration, token totals, model, and commit count for a car.
func queryCarOutcomeStats(db *gorm.DB, carID, branch, baseBranch, workDir string, claimTime time.Time) carOutcomeStats {
	stats := carOutcomeStats{
		duration: time.Since(claimTime),
	}

	// Query token totals and model from agent_logs table.
	var tokenRow struct {
		TotalTokens int
		Model       string
	}
	db.Model(&models.AgentLog{}).
		Select("COALESCE(SUM(token_count), 0) as total_tokens, COALESCE(MAX(model), '') as model").
		Where("car_id = ?", carID).
		Scan(&tokenRow)
	stats.totalTokens = tokenRow.TotalTokens
	stats.model = tokenRow.Model

	// Count commits on branch relative to base using git rev-list.
	if branch != "" && baseBranch != "" && workDir != "" {
		revRange := "origin/" + baseBranch + ".." + branch
		cmd := exec.Command("git", "rev-list", "--count", revRange)
		cmd.Dir = workDir
		out, err := cmd.Output()
		if err == nil {
			if n, err := strconv.Atoi(strings.TrimSpace(string(out))); err == nil {
				stats.commits = n
			}
		}
	}

	return stats
}

func newEngineDrainCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "drain",
		Short: "Gracefully drain the engine process",
		Long:  "Sends SIGTERM to the engine process running in this container, triggering graceful shutdown. Used as a Kubernetes preStop lifecycle hook.",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runEngineDrain(cmd)
		},
	}
	return cmd
}

func runEngineDrain(cmd *cobra.Command) error {
	// Find the engine start process (our parent or sibling PID 1 process).
	// In a Kubernetes pod, the engine start command runs as PID 1.
	// The preStop hook runs in a separate exec, so we signal PID 1.
	pid := 1
	proc, err := os.FindProcess(pid)
	if err != nil {
		return fmt.Errorf("find engine process (PID %d): %w", pid, err)
	}
	if err := proc.Signal(syscall.SIGTERM); err != nil {
		return fmt.Errorf("send SIGTERM to PID %d: %w", pid, err)
	}
	fmt.Fprintf(cmd.OutOrStdout(), "Sent SIGTERM to PID %d for graceful drain\n", pid)
	return nil
}

// outcomeKind describes how a subprocess session ended.
type outcomeKind int

const (
	outcomeCompleted outcomeKind = iota // car marked done
	outcomeClear                        // agent exited, car not done
	outcomeStall                        // stall detected
	outcomeCancelled                    // context cancelled (shutdown)
)

type sessionOutcome struct {
	kind        outcomeKind
	stallReason engine.StallReason
}

// monitorSession waits for the subprocess to exit, a stall, or context cancellation.
// It verifies car status in the DB before returning outcomes.
func monitorSession(ctx context.Context, sess *engine.Session, sd *engine.StallDetector, db *gorm.DB, carID string) sessionOutcome {
	return monitorSessionWithDB(ctx, sess.Done(), sd.Stalled(), db, carID)
}

// monitorSessionWithDB is the testable core of monitorSession. It takes raw
// channels and a DB connection, and verifies car status before returning outcomes.
func monitorSessionWithDB(ctx context.Context, doneCh <-chan error, stallCh <-chan engine.StallReason, db *gorm.DB, carID string) sessionOutcome {
	select {
	case <-ctx.Done():
		return sessionOutcome{kind: outcomeCancelled}

	case reason := <-stallCh:
		// Before declaring stall, check if agent already finished.
		var c models.Car
		if dbErr := db.Select("status").First(&c, "id = ?", carID).Error; dbErr == nil && c.Status == "done" {
			slog.Info("engine: stall suppressed, car already done", "car", carID)
			return sessionOutcome{kind: outcomeCompleted}
		}
		return sessionOutcome{kind: outcomeStall, stallReason: reason}

	case err := <-doneCh:
		if err != nil {
			return sessionOutcome{kind: outcomeClear}
		}
		// Zero exit — verify the agent actually called ry complete.
		var c models.Car
		if dbErr := db.Select("status", "blocked_reason").First(&c, "id = ?", carID).Error; dbErr != nil {
			slog.Warn("engine: monitor could not load car status", "car", carID, "error", dbErr)
			return sessionOutcome{kind: outcomeClear}
		}
		if c.Status == "done" {
			return sessionOutcome{kind: outcomeCompleted}
		}
		slog.Warn("engine: agent exited cleanly but car not done",
			"car", carID,
			"status", c.Status,
			"blocked_reason", c.BlockedReason,
		)
		return sessionOutcome{kind: outcomeClear}
	}
}

// claimOrReclaim either claims a new car or re-claims the engine's current car.
func claimOrReclaim(gormDB *gorm.DB, eng *models.Engine, track string) (*models.Car, error) {
	// Check if engine already has a car assigned (re-claim after clear cycle).
	if eng.CurrentCar != "" {
		b, err := car.Get(gormDB, eng.CurrentCar)
		// Only re-claim if car is still actively workable (not done, cancelled, or blocked).
		if err == nil && b.Status != "done" && b.Status != "cancelled" && b.Status != "blocked" {
			return b, nil
		}
		// Clear stale current_car — car is in a terminal/blocked state.
		gormDB.Model(&models.Engine{}).Where("id = ?", eng.ID).Update("current_car", "")
		eng.CurrentCar = ""
	}

	return engine.ClaimCar(gormDB, eng.ID, track)
}

// loadProgress retrieves progress notes for a car.
func loadProgress(gormDB *gorm.DB, carID string) ([]models.CarProgress, error) {
	var progress []models.CarProgress
	err := gormDB.Where("car_id = ?", carID).Order("created_at ASC").Find(&progress).Error
	return progress, err
}

// loadMessages retrieves unacknowledged messages for an engine.
func loadMessages(gormDB *gorm.DB, engineID string) ([]models.Message, error) {
	return messaging.Inbox(gormDB, engineID)
}

// pushInflightBranch attempts to auto-commit and push the current car's branch
// before shutdown. Non-fatal: logs warning on failure.
func pushInflightBranch(gormDB *gorm.DB, eng *models.Engine, repoDir string) {
	if eng.CurrentCar == "" {
		return
	}
	var c models.Car
	if err := gormDB.Where("id = ?", eng.CurrentCar).First(&c).Error; err != nil {
		return
	}
	if c.Branch == "" {
		return
	}

	// Auto-commit uncommitted work before pushing so it isn't lost.
	if committed, err := engine.AutoCommitIfDirty(repoDir, "railyard: auto-commit on engine shutdown"); err != nil {
		slog.Warn("engine: auto-commit before shutdown push failed", "car", c.ID, "error", err)
	} else if committed {
		slog.Info("engine: auto-committed uncommitted changes", "car", c.ID)
	}

	engine.PushBranch(repoDir, c.Branch) //nolint:errcheck
}

// handleCompletionFailure sets a car to blocked and notifies the yardmaster
// when HandleCompletion fails (e.g., push failure). This prevents the car
// from sitting in "done" status with no code on the remote.
func handleCompletionFailure(db *gorm.DB, carID, engineID, sessionID string, completionErr error) {
	db.Model(&models.Car{}).Where("id = ?", carID).
		Updates(map[string]interface{}{
			"status":         "blocked",
			"blocked_reason": models.BlockedReasonCompletionFailed,
		})

	db.Create(&models.CarProgress{
		CarID:        carID,
		EngineID:     engineID,
		SessionID:    sessionID,
		Note:         fmt.Sprintf("Completion failed: %v", completionErr),
		FilesChanged: "[]",
		CreatedAt:    time.Now(),
	})

	messaging.Send(db, engineID, "yardmaster", "completion-failed",
		fmt.Sprintf("Car %s completion failed (push): %v", carID, completionErr),
		messaging.SendOpts{CarID: carID, Priority: "urgent"})
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
	if len(result.SessionsCreated) > 0 {
		fmt.Fprintf(out, "  Created %d new engine sessions\n", len(result.SessionsCreated))
	}
	if len(result.SessionsKilled) > 0 {
		fmt.Fprintf(out, "  Removed %d engines\n", len(result.SessionsKilled))
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
		Long:  "Displays all engines with ID, track, status, current car, last activity, and uptime.",
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
	fmt.Fprintln(w, "ID\tTRACK\tSTATUS\tPROVIDER\tCURRENT CAR\tLAST ACTIVITY\tUPTIME")
	for _, e := range engines {
		car := e.CurrentCar
		if car == "" {
			car = "-"
		}
		provider := e.Provider
		if provider == "" {
			provider = "claude"
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			e.ID, e.Track, e.Status, provider, car,
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
	cfg, gormDB, err := connectFromConfig(configPath)
	if err != nil {
		return err
	}

	if err := orchestration.RestartEngine(gormDB, cfg, configPath, engineID, nil); err != nil {
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
