package yardmaster

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os/exec"
	"runtime/debug"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/zulandar/railyard/internal/car"
	"github.com/zulandar/railyard/internal/config"
	"github.com/zulandar/railyard/internal/engine"
	"github.com/zulandar/railyard/internal/messaging"
	"github.com/zulandar/railyard/internal/models"
	"github.com/zulandar/railyard/internal/orchestration"
	"gorm.io/gorm"
)

const (
	// YardmasterID is the well-known engine ID for the yardmaster.
	YardmasterID        = "yardmaster"
	defaultPollInterval = 30 * time.Second
	maxTestFailures     = 2 // deprecated: use cfg.Stall.MaxSwitchFailures instead
)

// RunDaemon runs the yardmaster daemon loop. It registers the yardmaster in the
// engines table, starts a heartbeat, and loops through inbox processing, stale
// engine detection, completed car switching, and blocked car unblocking.
func RunDaemon(ctx context.Context, db *gorm.DB, cfg *config.Config, configPath, repoDir string, pollInterval time.Duration, logger *slog.Logger) error {
	if db == nil {
		return fmt.Errorf("yardmaster: db is required")
	}
	if cfg == nil {
		return fmt.Errorf("yardmaster: config is required")
	}
	if repoDir == "" {
		return fmt.Errorf("yardmaster: repoDir is required")
	}
	if pollInterval <= 0 {
		pollInterval = defaultPollInterval
	}
	if logger == nil {
		logger = slog.Default()
	}

	startedAt := time.Now()
	if err := registerYardmaster(db, cfg.AgentProvider); err != nil {
		return fmt.Errorf("yardmaster: register: %w", err)
	}
	logger.Info("Yardmaster registered", "id", YardmasterID)

	// Create the yardmaster worktree so switch operations don't disturb the
	// primary repo. Falls back to repoDir if worktree creation fails.
	ymDir, ymErr := engine.EnsureYardmasterWorktree(repoDir)
	if ymErr != nil {
		logger.Warn("Yardmaster worktree setup warning, using repo dir", "error", ymErr)
		ymDir = repoDir
	} else {
		logger.Info("Yardmaster worktree ready", "path", ymDir)
	}

	hbErrCh := engine.StartHeartbeat(ctx, db, YardmasterID, engine.DefaultHeartbeatInterval)

	hs := NewHealthServer(pollInterval)
	go func() {
		if err := StartHealthServer(ctx, cfg.Yardmaster.HealthPort, hs); err != nil {
			logger.Error("Health server error", "error", err)
		}
	}()

	logger.Info("Yardmaster daemon starting", "poll", pollInterval)

	defer func() {
		logger.Info("Yardmaster deregistering")
		if err := engine.Deregister(db, YardmasterID); err != nil {
			logger.Error("Yardmaster deregister error", "error", err)
		}
		logger.Info("Yardmaster stopped")
	}()

	rbState := &rebalanceState{lastTrackMoveAt: make(map[string]time.Time)}

	// Track background escalation goroutines so shutdown waits for them.
	var escWg sync.WaitGroup
	defer escWg.Wait()

	// Per-car escalation cooldown tracker.
	escTracker := NewEscalationTracker(time.Duration(cfg.Stall.EscalationCooldownSec) * time.Second)

	// Semaphore to limit concurrent escalation goroutines.
	escSem := make(chan struct{}, cfg.Stall.MaxConcurrentEscalations)

	for {
		select {
		case <-ctx.Done():
			return nil
		case err := <-hbErrCh:
			return fmt.Errorf("yardmaster: heartbeat: %w", err)
		default:
		}

		// Panic recovery: catch panics in the daemon loop body, log the
		// stack trace, and continue the loop rather than crashing.
		draining := func() (drain bool) {
			defer func() {
				if r := recover(); r != nil {
					logger.Error("PANIC recovered in daemon loop", "panic", r, "stack", string(debug.Stack()))
				}
			}()

			hs.RecordPoll()

			// timePhase uses tiered logging based on phase duration.
			timePhase := func(name string, fn func()) {
				start := time.Now()
				fn()
				elapsed := time.Since(start)
				if elapsed > 5*time.Second {
					logger.Warn("Phase slow", "phase", name, "elapsed", elapsed)
				} else if elapsed > time.Second {
					logger.Info("Phase completed", "phase", name, "elapsed", elapsed)
				} else {
					logger.Debug("Phase completed", "phase", name, "elapsed", elapsed)
				}
			}

			// Phase 1: Process inbox.
			var shouldDrain bool
			timePhase("inbox", func() {
				var pErr error
				shouldDrain, pErr = processInbox(ctx, db, cfg, configPath, repoDir, startedAt, &escWg, escTracker, escSem, logger)
				if pErr != nil {
					logger.Error("Inbox error", "error", pErr)
				}
			})
			if shouldDrain {
				return true
			}

			// Phase 2: Handle stale engines.
			timePhase("stale-engines", func() {
				if err := handleStaleEngines(db, cfg, configPath, logger); err != nil {
					logger.Error("Stale engines error", "error", err)
				}
			})

			// Phase 3: Handle completed cars.
			timePhase("completed-cars", func() {
				if err := handleCompletedCars(ctx, db, cfg, repoDir, ymDir, &escWg, escTracker, escSem, logger); err != nil {
					logger.Error("Completed cars error", "error", err)
				}
			})

			// Phase 4: Handle blocked cars (safety-net sweep).
			timePhase("blocked-cars", func() {
				if err := handleBlockedCars(db, logger); err != nil {
					logger.Error("Blocked cars error", "error", err)
				}
			})

			// Phase 4b: Sweep open epics whose children may all be complete.
			timePhase("sweep-epics", func() {
				if err := sweepOpenEpics(db, logger); err != nil {
					logger.Error("Sweep open epics error", "error", err)
				}
			})

			// Phase 5: Reconcile stale cars whose branches are already merged.
			timePhase("reconcile", func() {
				if err := reconcileStaleCars(db, repoDir, logger); err != nil {
					logger.Error("Reconcile error", "error", err)
				}
			})

			// Phase 5b: Poll pr_open cars for GitHub review feedback.
			timePhase("pr-review", func() {
				if cfg.RequirePR {
					prViewer := &ghPRViewer{repoDir: repoDir}
					if err := handlePrOpenCars(db, prViewer, cfg.Yardmaster.AutoMergeOnApproval, repoDir, ymDir, cfg, logger); err != nil {
						logger.Error("PR review error", "error", err)
					}
				}
			})

			// Phase 6: Rebalance idle engines to busy tracks.
			timePhase("rebalance", func() {
				if err := rebalanceEngines(db, cfg, configPath, rbState, logger); err != nil {
					logger.Error("Rebalance error", "error", err)
				}
			})

			return false
		}()

		if draining {
			logger.Info("Drain received, shutting down")
			return nil
		}

		sleepWithContext(ctx, pollInterval)
	}
}

// registerYardmaster creates or updates the yardmaster engine record.
func registerYardmaster(db *gorm.DB, providerName string) error {
	now := time.Now()
	if providerName == "" {
		providerName = "claude"
	}
	eng := models.Engine{
		ID:           YardmasterID,
		Track:        "*",
		Role:         "yardmaster",
		Status:       engine.StatusIdle,
		Provider:     providerName,
		StartedAt:    now,
		LastActivity: now,
	}

	var existing models.Engine
	result := db.Where("id = ?", YardmasterID).First(&existing)
	if result.Error != nil {
		return db.Create(&eng).Error
	}

	return db.Model(&models.Engine{}).Where("id = ?", YardmasterID).Updates(map[string]interface{}{
		"status":        engine.StatusIdle,
		"role":          "yardmaster",
		"provider":      providerName,
		"track":         "*",
		"started_at":    now,
		"last_activity": now,
	}).Error
}

// processInbox drains the yardmaster inbox, classifying and handling each message.
// Returns true if a drain message was received (yardmaster should shut down).
// startedAt is when this yardmaster instance started; drain messages older than
// this are stale leftovers from a previous shutdown and are silently acked.
func processInbox(ctx context.Context, db *gorm.DB, cfg *config.Config, configPath, repoDir string, startedAt time.Time, escWg *sync.WaitGroup, escTracker *EscalationTracker, escSem chan struct{}, logger *slog.Logger) (draining bool, err error) {
	msgs, err := messaging.Inbox(db, YardmasterID)
	if err != nil {
		return false, err
	}

	// Deduplicate messages by (FromAgent, Subject, CarID). Ack duplicates
	// without processing so repeated messages don't trigger duplicate work.
	type dedupKey struct{ From, Subject, CarID string }
	seen := make(map[dedupKey]bool, len(msgs))
	deduped := msgs[:0]
	for _, msg := range msgs {
		key := dedupKey{msg.FromAgent, strings.ToLower(msg.Subject), msg.CarID}
		if seen[key] {
			ackMsg(db, msg, logger)
			continue
		}
		seen[key] = true
		deduped = append(deduped, msg)
	}
	msgs = deduped

	for _, msg := range msgs {
		subject := strings.ToLower(msg.Subject)

		switch {
		case subject == "drain":
			if msg.CreatedAt.Before(startedAt) {
				logger.Info("Inbox: stale drain message, ignoring", "from_time", msg.CreatedAt.Format(time.RFC3339))
				ackMsg(db, msg, logger)
				continue
			}
			ackMsg(db, msg, logger)
			return true, nil

		case subject == "engine-stalled":
			logger.Info("Inbox: engine-stalled", "from", msg.FromAgent, "body", msg.Body)
			if msg.CarID != "" {
				writeProgressNote(db, msg.CarID, msg.FromAgent, fmt.Sprintf("Engine stalled: %s", msg.Body))
			}
			// Restart the stalled engine to spawn a replacement.
			if msg.FromAgent != "" && msg.FromAgent != YardmasterID {
				if err := orchestration.RestartEngine(db, cfg, configPath, msg.FromAgent, nil); err != nil {
					logger.Error("Failed to restart stalled engine", "engine", msg.FromAgent, "error", err)
				} else {
					logger.Info("Restarted stalled engine", "engine", msg.FromAgent)
				}
			}
			ackMsg(db, msg, logger)

		case subject == "help" || subject == "stuck":
			logger.Info("Inbox: escalating to agent", "subject", subject, "from", msg.FromAgent, "car", msg.CarID)
			if escTracker != nil && !escTracker.ShouldEscalate(msg.CarID) {
				logger.Info("Escalation skipped, cooldown active", "car", msg.CarID)
				ackMsg(db, msg, logger)
				continue
			}
			escSem <- struct{}{} // acquire semaphore
			escWg.Add(1)
			go func(m models.Message) {
				defer func() { <-escSem }() // release semaphore
				defer escWg.Done()
				result, escErr := EscalateToAgent(ctx, EscalateOpts{
					CarID:        m.CarID,
					EngineID:     m.FromAgent,
					Reason:       m.Subject,
					Details:      m.Body,
					DB:           db,
					ProviderName: cfg.AgentProvider,
				})
				if escErr != nil {
					logger.Error("Escalation error", "error", escErr)
					return
				}
				handleEscalateResult(db, m.FromAgent, m.CarID, result, logger)
			}(msg)
			ackMsg(db, msg, logger)

		case subject == "test-failure":
			logger.Info("Inbox: test-failure acknowledged", "car", msg.CarID)
			ackMsg(db, msg, logger)

		case subject == "restart-engine":
			handleRestartEngine(ctx, db, cfg, configPath, msg, logger)
			ackMsg(db, msg, logger)

		case subject == "retry-merge":
			handleRetryMerge(db, msg, logger)
			ackMsg(db, msg, logger)

		case subject == "requeue-car":
			handleRequeueCar(db, msg, logger)
			ackMsg(db, msg, logger)

		case subject == "nudge-engine":
			handleNudgeEngine(db, msg, logger)
			ackMsg(db, msg, logger)

		case subject == "unblock-car":
			handleUnblockCar(db, msg, logger)
			ackMsg(db, msg, logger)

		case subject == "close-epic":
			handleCloseEpic(db, msg, logger)
			ackMsg(db, msg, logger)

		case subject == "reassignment" || subject == "deps-unblocked" || subject == "epic-closed":
			ackMsg(db, msg, logger)

		case strings.Contains(subject, "done") || strings.Contains(subject, "complete"):
			logger.Warn("Inbox: engine sent completion message instead of using ry complete", "engine", msg.FromAgent, "subject", msg.Subject)
			ackMsg(db, msg, logger)

		default:
			logger.Info("Inbox: unknown subject acknowledged", "subject", msg.Subject, "from", msg.FromAgent)
			ackMsg(db, msg, logger)
		}
	}

	return false, nil
}

// ackMsg acknowledges a message, using broadcast ack for broadcast messages.
func ackMsg(db *gorm.DB, msg models.Message, logger *slog.Logger) {
	if msg.ToAgent == "broadcast" {
		if err := messaging.AcknowledgeBroadcast(db, msg.ID, YardmasterID); err != nil {
			logger.Error("Broadcast ack error", "msg", msg.ID, "error", err)
		}
	} else {
		if err := messaging.Acknowledge(db, msg.ID); err != nil {
			logger.Error("Ack error", "msg", msg.ID, "error", err)
		}
	}
}

// handleStaleEngines detects engines with stale heartbeats, reassigns their cars,
// and restarts the engines.
func handleStaleEngines(db *gorm.DB, cfg *config.Config, configPath string, logger *slog.Logger) error {
	threshold := DefaultStaleThreshold
	if cfg.Stall.StaleEngineThresholdSec > 0 {
		threshold = time.Duration(cfg.Stall.StaleEngineThresholdSec) * time.Second
	}
	stale, err := CheckEngineHealth(db, threshold)
	if err != nil {
		return err
	}

	for _, eng := range stale {
		if eng.ID == YardmasterID {
			continue
		}

		// Clean up dead engine's overlay before restart (non-fatal).
		if err := engine.CleanupOverlay(eng.ID, cfg); err != nil {
			logger.Warn("Overlay cleanup for stale engine", "engine", eng.ID, "error", err)
		}

		if eng.CurrentCar != "" {
			logger.Warn("Engine deregistered as stale", "engine", eng.ID, "car", eng.CurrentCar)
			if err := ReassignCar(db, eng.CurrentCar, eng.ID, "stale heartbeat"); err != nil {
				logger.Error("Reassign car from stale engine", "car", eng.CurrentCar, "engine", eng.ID, "error", err)
			}
		} else {
			logger.Warn("Engine deregistered as stale", "engine", eng.ID, "status", "idle")
			if err := db.Model(&models.Engine{}).Where("id = ?", eng.ID).Update("status", engine.StatusDead).Error; err != nil {
				logger.Error("Update stale engine to dead", "engine", eng.ID, "error", err)
			}
		}

		// Restart the engine to spawn a replacement on the same track.
		if err := orchestration.RestartEngine(db, cfg, configPath, eng.ID, nil); err != nil {
			logger.Error("Failed to restart engine", "engine", eng.ID, "error", err)
		}
	}

	return nil
}

// handleCompletedCars finds cars with status "done" and runs the switch flow.
// Switch() marks cars as "merged" after successful merge, so they won't reappear.
// ymDir is the yardmaster worktree where switch operations happen; repoDir is
// the primary repo (used for engine worktree detachment).
func handleCompletedCars(ctx context.Context, db *gorm.DB, cfg *config.Config, repoDir, ymDir string, escWg *sync.WaitGroup, escTracker *EscalationTracker, escSem chan struct{}, logger *slog.Logger) error {
	cars, err := car.List(db, car.ListFilters{Status: "done"})
	if err != nil {
		return err
	}

	// Sort by priority ASC (lower = higher priority), then CreatedAt ASC.
	sort.Slice(cars, func(i, j int) bool {
		if cars[i].Priority != cars[j].Priority {
			return cars[i].Priority < cars[j].Priority
		}
		return cars[i].CreatedAt.Before(cars[j].CreatedAt)
	})

	for _, c := range cars {
		// Epics are container cars — no engine ever commits to their branch.
		// Skip the merge and transition directly to merged when all children
		// are in a terminal state.
		if c.Type == "epic" {
			var remaining int64
			if err := db.Model(&models.Car{}).
				Where("parent_id = ? AND status NOT IN ?", c.ID, []string{"done", "merged", "cancelled"}).
				Count(&remaining).Error; err != nil {
				logger.Error("Count remaining children for epic", "epic", c.ID, "error", err)
				continue
			}
			if remaining > 0 {
				logger.Info("Epic has pending children, skipping", "epic", c.ID, "title", c.Title, "remaining", remaining)
				continue
			}
			now := time.Now()
			if err := db.Model(&models.Car{}).Where("id = ?", c.ID).Updates(map[string]interface{}{
				"status":       "merged",
				"completed_at": now,
			}).Error; err != nil {
				logger.Error("Update epic to merged", "epic", c.ID, "error", err)
				continue
			}
			logger.Info("Epic completed, all children done", "epic", c.ID, "title", c.Title)

			// Unblock cross-track dependencies and auto-close parent epics.
			unblocked, ubErr := UnblockDeps(db, c.ID)
			if ubErr != nil {
				logger.Error("Unblock deps for epic", "epic", c.ID, "error", ubErr)
			}
			for _, u := range unblocked {
				if u.Type == "epic" {
					TryCloseEpic(db, u.ID)
				}
			}
			if c.ParentID != nil && *c.ParentID != "" {
				TryCloseEpic(db, *c.ParentID)
			}
			continue
		}

		logger.Info("Car completed, switching", "car", c.ID, "title", c.Title)

		// Reset the yardmaster worktree to the car's base branch before each
		// switch so we start from a clean state.
		baseBranch := c.BaseBranch
		if baseBranch == "" {
			baseBranch = "main"
		}
		if ymDir != repoDir {
			if err := engine.SyncWorktreeToBranch(ymDir, baseBranch, repoDir); err != nil {
				logger.Warn("Reset yardmaster worktree", "car", c.ID, "error", err)
			}
		}

		var testCommand, preTestCommand string
		for _, t := range cfg.Tracks {
			if t.Name == c.Track {
				preTestCommand = t.PreTestCommand
				testCommand = t.TestCommand
				break
			}
		}

		result, err := Switch(db, c.ID, SwitchOpts{
			RepoDir:          ymDir,
			PrimaryRepoDir:   repoDir,
			BaseBranch:       baseBranch,
			PreTestCommand:   preTestCommand,
			TestCommand:      testCommand,
			RequirePR:        cfg.RequirePR,
			SwitchTimeoutSec: cfg.Stall.SwitchTimeoutSec,
		})

		// Handle any failure — write a categorized progress note and check
		// whether we've hit the escalation threshold.
		failCategory := SwitchFailNone
		if result != nil {
			failCategory = result.FailureCategory
		}

		if err != nil {
			logger.Error("Switch car failed", "car", c.ID, "error", err)

			if failCategory != SwitchFailNone {
				note := fmt.Sprintf("switch:%s: %v", failCategory, err)
				if result != nil && result.ConflictDetails != "" {
					note += "\n" + result.ConflictDetails
				}
				writeProgressNote(db, c.ID, YardmasterID, note)
			}

			conflictDetails := ""
			if result != nil {
				conflictDetails = result.ConflictDetails
			}
			maybeSwitchEscalate(ctx, db, cfg, c.ID, failCategory, err, conflictDetails, escWg, escTracker, escSem, logger)
			continue
		}

		// Test failures return result with nil error but FailureCategory set.
		if failCategory != SwitchFailNone {
			note := fmt.Sprintf("switch:%s: %v", failCategory, result.Error)
			if result.ConflictDetails != "" {
				note += "\n" + result.ConflictDetails
			}
			writeProgressNote(db, c.ID, YardmasterID, note)
			maybeSwitchEscalate(ctx, db, cfg, c.ID, failCategory, result.Error, result.ConflictDetails, escWg, escTracker, escSem, logger)
		}

		if result.PRCreated {
			logger.Info("Car state transition", "car", c.ID, "transition", "done->pr_open", "pr_url", result.PRUrl)
		} else if result.Merged {
			if result.AlreadyMerged {
				logger.Info("Car state transition", "car", c.ID, "transition", "done->merged", "branch", result.Branch, "already_merged", true)
			} else {
				logger.Info("Car state transition", "car", c.ID, "transition", "done->merged", "branch", result.Branch)
			}

			// Clean up the completing engine's overlay (non-fatal).
			if c.Assignee != "" {
				if err := engine.CleanupOverlay(c.Assignee, cfg); err != nil {
					logger.Warn("Overlay cleanup", "assignee", c.Assignee, "error", err)
				}
			}

		} else if !result.TestsPassed {
			logger.Warn("Car tests failed, blocked",
				"car", c.ID,
				"failure_category", failCategory,
				"test_output_tail", truncateSwitchLog(result.TestOutput, 200),
			)
		}
	}

	return nil
}

// handleBlockedCars is a safety-net sweep that tries to unblock cars whose
// dependencies may have resolved outside the normal switch flow.
func handleBlockedCars(db *gorm.DB, logger *slog.Logger) error {
	for _, status := range []string{"merged"} {
		completedCars, err := car.List(db, car.ListFilters{Status: status})
		if err != nil {
			return err
		}

		for _, c := range completedCars {
			logger.Debug("handleBlockedCars: checking deps for merged car", "car", c.ID)
			unblocked, err := UnblockDeps(db, c.ID)
			if err != nil {
				logger.Error("Unblock deps", "car", c.ID, "error", err)
				continue
			}
			for _, u := range unblocked {
				logger.Info("Car unblocked", "car", u.ID, "dependency", c.ID)
				// Auto-close epics whose children are all complete.
				if u.Type == "epic" {
					TryCloseEpic(db, u.ID)
				}
			}
		}
	}

	return nil
}

// sweepOpenEpics checks open epics whose children may all be complete and
// auto-closes them. This is a safety net for epics that missed the reactive
// TryCloseEpic call (e.g., timing issues, last child merged before check).
func sweepOpenEpics(db *gorm.DB, logger *slog.Logger) error {
	openEpics, err := car.List(db, car.ListFilters{Status: "open", Type: "epic"})
	if err != nil {
		return err
	}

	for _, e := range openEpics {
		var remaining int64
		if err := db.Model(&models.Car{}).
			Where("parent_id = ? AND status NOT IN ?", e.ID, []string{"done", "merged", "cancelled"}).
			Count(&remaining).Error; err != nil {
			logger.Error("Sweep: count remaining for epic", "epic", e.ID, "error", err)
			continue
		}

		if remaining == 0 {
			// Double-check the epic has at least one child (don't close empty epics).
			var total int64
			if err := db.Model(&models.Car{}).Where("parent_id = ?", e.ID).Count(&total).Error; err != nil {
				logger.Error("Sweep: count total children for epic", "epic", e.ID, "error", err)
				continue
			}
			if total > 0 {
				logger.Info("Epic progress: auto-closing, all children complete", "epic", e.ID, "title", e.Title)
				TryCloseEpic(db, e.ID)
			}
		}
	}

	return nil
}

// reconcileStaleCars detects cars whose branches have already been merged to
// their base branch (e.g., via a monolithic epic commit or a PR merged on
// GitHub) and updates their status to "merged". This includes pr_open cars
// whose PRs were merged externally. Checks against origin/{baseBranch} (the
// remote truth) to avoid false positives from local-only merges that were
// never pushed. Cars are grouped by base branch so each group is checked
// against the correct target.
func reconcileStaleCars(db *gorm.DB, repoDir string, logger *slog.Logger) error {
	// Fetch first to get current remote state.
	if err := gitFetch(repoDir); err != nil {
		return fmt.Errorf("reconcile fetch: %w", err)
	}

	// Find active cars with branches.
	var activeCars []models.Car
	if err := db.Where("status IN ? AND branch != ''",
		[]string{"open", "ready", "claimed", "in_progress", "pr_open"}).
		Find(&activeCars).Error; err != nil {
		return fmt.Errorf("query active cars: %w", err)
	}

	if len(activeCars) == 0 {
		return nil
	}

	// Group cars by base branch.
	carsByBase := make(map[string][]models.Car)
	for _, c := range activeCars {
		base := c.BaseBranch
		if base == "" {
			base = "main"
		}
		carsByBase[base] = append(carsByBase[base], c)
	}

	now := time.Now()
	for base, cars := range carsByBase {
		// Get branches merged into origin/{base}.
		mergedBranches, err := getMergedBranches(repoDir, "origin/"+base)
		if err != nil {
			logger.Warn("Reconcile: skip base", "base", base, "error", err)
			continue
		}

		for _, c := range cars {
			if mergedBranches[c.Branch] {
				// Guard: skip branches with zero commits ahead of base.
				// A zero-commit branch means the engine never committed work —
				// it should not be treated as merged.
				if !branchHasUniqueCommits(repoDir, c.Branch, base) {
					logger.Warn("Reconcile: skip zero-commit branch",
						"car", c.ID, "branch", c.Branch, "base", base)
					continue
				}
				logger.Info("Car state transition", "car", c.ID, "title", c.Title, "transition", "reconciled->merged", "branch", c.Branch, "base", base)
				if err := db.Model(&models.Car{}).Where("id = ?", c.ID).Updates(map[string]interface{}{
					"status":       "merged",
					"completed_at": now,
				}).Error; err != nil {
					logger.Error("Reconcile car to merged", "car", c.ID, "error", err)
					continue
				}
				runPostMerge(db, c, logger)
			}
		}
	}

	return nil
}

// getMergedBranches returns a set of branch names that are already merged
// into the given target ref (e.g. "origin/main").
func getMergedBranches(repoDir, target string) (map[string]bool, error) {
	cmd := exec.Command("git", "branch", "-a", "--merged", target)
	cmd.Dir = repoDir
	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git branch --merged %s: %w", target, err)
	}

	merged := make(map[string]bool)
	for _, line := range strings.Split(string(output), "\n") {
		branch := strings.TrimSpace(line)
		branch = strings.TrimPrefix(branch, "* ")
		branch = strings.TrimPrefix(branch, "remotes/origin/")
		if branch != "" {
			merged[branch] = true
		}
	}
	return merged, nil
}

// branchHasUniqueCommits returns true if the given branch has at least one
// unique commit (i.e., the branch was worked on). It detects zero-commit
// branches by checking whether the branch tip is on the base's first-parent
// (mainline) history. A zero-commit branch was created from base and never
// committed to, so its tip IS a mainline commit. A truly merged branch has
// its tip on a side branch that was brought in via merge — the tip is NOT
// on the first-parent lineage.
//
// NOTE: This relies on --no-ff merges (which Railyard's gitMerge enforces).
// Fast-forward or squash merges (e.g. via GitHub UI) would place the branch
// tip on the mainline, causing a false-negative. If external merge strategies
// become supported, this function needs a rev-list --count fallback.
//
// Checks origin/{branch} first (remote truth), falling back to local ref.
// Returns false (safe default) on any error.
func branchHasUniqueCommits(repoDir, branch, baseBranch string) bool {
	// Resolve branch ref: prefer origin.
	branchRef := "origin/" + branch
	check := exec.Command("git", "rev-parse", "--verify", branchRef)
	check.Dir = repoDir
	if _, err := check.CombinedOutput(); err != nil {
		branchRef = branch
	}

	// Resolve base ref: prefer origin.
	baseRef := "origin/" + baseBranch
	checkBase := exec.Command("git", "rev-parse", "--verify", baseRef)
	checkBase.Dir = repoDir
	if _, err := checkBase.CombinedOutput(); err != nil {
		baseRef = baseBranch
	}

	// Get branch tip commit hash.
	tipCmd := exec.Command("git", "rev-parse", branchRef)
	tipCmd.Dir = repoDir
	tipOut, err := tipCmd.CombinedOutput()
	if err != nil {
		return false
	}
	tip := strings.TrimSpace(string(tipOut))

	// Walk base's first-parent lineage (the mainline). If the branch tip
	// appears here, it was never on a side branch — zero-commit.
	// Limit to 500 commits for performance; older zero-commit branches
	// would pass through (safe: they'd just be re-checked next cycle).
	logCmd := exec.Command("git", "rev-list", "--first-parent", "--max-count=500", baseRef)
	logCmd.Dir = repoDir
	logOut, err := logCmd.CombinedOutput()
	if err != nil {
		return false
	}

	for _, line := range strings.Split(string(logOut), "\n") {
		if strings.TrimSpace(line) == tip {
			return false // tip is on mainline = zero-commit branch
		}
	}
	return true // tip was on a side branch = has unique work
}

// handleEscalateResult acts on the decision returned by Claude escalation.
func handleEscalateResult(db *gorm.DB, engineID, carID string, result *EscalateResult, logger *slog.Logger) {
	if result == nil {
		return
	}

	// When there's no engine (e.g. switch escalations), Guidance and Reassign
	// have no valid recipient. Fall back to EscalateHuman so the escalation
	// is never silently dropped.
	if engineID == "" && (result.Action == EscalateGuidance || result.Action == EscalateReassign) {
		logger.Warn("Escalation: no engine for action, alerting human instead", "action", string(result.Action), "car", carID)
		result = &EscalateResult{Action: EscalateHuman, Message: result.Message}
	}

	switch result.Action {
	case EscalateReassign:
		logger.Info("Escalation: reassigning car", "car", carID)
		ReassignCar(db, carID, engineID, "escalation: "+result.Message)
	case EscalateGuidance:
		logger.Info("Escalation: sending guidance", "engine", engineID)
		messaging.Send(db, YardmasterID, engineID, "guidance", result.Message,
			messaging.SendOpts{CarID: carID})
	case EscalateHuman:
		logger.Warn("Escalation: alerting human", "car", carID, "message", result.Message)
		messaging.Send(db, YardmasterID, "human", "escalate", result.Message,
			messaging.SendOpts{CarID: carID, Priority: "urgent"})
	case EscalateRetry:
		logger.Info("Escalation: retry", "car", carID)
	case EscalateSkip:
		logger.Info("Escalation: skip", "car", carID)
	}
}

// countRecentSwitchFailures counts all switch-categorized failure progress
// notes for a car. Each note has the form "switch:<category>: <details>".
func countRecentSwitchFailures(db *gorm.DB, carID string) int {
	var count int64
	if err := db.Model(&models.CarProgress{}).
		Where("car_id = ? AND note LIKE ?", carID, "switch:%").
		Count(&count).Error; err != nil {
		slog.Error("countRecentSwitchFailures", "car", carID, "error", err)
		return 0
	}
	return int(count)
}

// switchFailureReason maps a failure category to a human-readable escalation
// reason string for the Claude prompt.
func switchFailureReason(cat SwitchFailureCategory) string {
	switch cat {
	case SwitchFailFetch:
		return "repeated-fetch-failure"
	case SwitchFailPreTest:
		return "repeated-pre-test-failure"
	case SwitchFailTest:
		return "repeated-test-failure"
	case SwitchFailInfra:
		return "infrastructure-test-failure"
	case SwitchFailMerge:
		return "repeated-merge-conflict"
	case SwitchFailPush:
		return "repeated-push-failure"
	case SwitchFailPR:
		return "repeated-pr-failure"
	default:
		return "repeated-switch-failure"
	}
}

// maybeSwitchEscalate checks whether a car has exceeded the switch failure
// threshold and, if so, escalates to Claude with the failure category.
func maybeSwitchEscalate(ctx context.Context, db *gorm.DB, cfg *config.Config, carID string, cat SwitchFailureCategory, switchErr error, conflictDetails string, escWg *sync.WaitGroup, escTracker *EscalationTracker, escSem chan struct{}, logger *slog.Logger) {
	// Infrastructure failures escalate immediately — no threshold needed.
	// The human message was already sent by Switch(); here we also escalate
	// to Claude for a suggested action.
	if cat == SwitchFailInfra {
		reason := switchFailureReason(cat)
		logger.Warn("Car infra failure, escalating immediately", "car", carID, "reason", reason)

		// Move car out of "done" to stop the retry loop. Can be retried
		// via the "retry-merge" action after the underlying issue is resolved.
		if err := db.Model(&models.Car{}).Where("id = ?", carID).Update("status", "merge-failed").Error; err != nil {
			logger.Error("Update car to merge-failed (infra)", "car", carID, "error", err)
		}
		logger.Info("Car state transition", "car", carID, "transition", "done->merge-failed")

		escSem <- struct{}{} // acquire semaphore
		escWg.Add(1)
		go func(carID, reason string) {
			defer func() { <-escSem }() // release semaphore
			defer escWg.Done()
			res, escErr := EscalateToAgent(ctx, EscalateOpts{
				CarID:        carID,
				Reason:       reason,
				Details:      fmt.Sprintf("Infrastructure test failure for car %s. The test command failed due to environment issues (missing dependencies, broken Docker, misconfigured commands), not code problems. Latest: %v", carID, switchErr),
				DB:           db,
				ProviderName: cfg.AgentProvider,
			})
			if escErr != nil {
				logger.Error("Escalation error", "car", carID, "error", escErr)
				return
			}
			handleEscalateResult(db, "", carID, res, logger)
		}(carID, reason)
		return
	}

	maxFailures := cfg.Stall.MaxSwitchFailures
	if maxFailures <= 0 {
		maxFailures = 3
	}

	failures := countRecentSwitchFailures(db, carID)
	if failures < maxFailures {
		return
	}

	if escTracker != nil && !escTracker.ShouldEscalate(carID) {
		logger.Info("Car escalation skipped, cooldown active", "car", carID)
		return
	}

	reason := switchFailureReason(cat)
	logger.Warn("Car switch failures, escalating", "car", carID, "failures", failures, "reason", reason)

	// Move car out of "done" to stop the retry loop. Can be retried
	// via the "retry-merge" action after the underlying issue is resolved.
	if err := db.Model(&models.Car{}).Where("id = ?", carID).Update("status", "merge-failed").Error; err != nil {
		logger.Error("Update car to merge-failed", "car", carID, "error", err)
	}
	logger.Info("Car state transition", "car", carID, "transition", "done->merge-failed")

	escSem <- struct{}{} // acquire semaphore
	escWg.Add(1)
	go func(carID string, failCount int, reason string) {
		defer func() { <-escSem }() // release semaphore
		defer escWg.Done()
		res, escErr := EscalateToAgent(ctx, EscalateOpts{
			CarID:        carID,
			Reason:       reason,
			Details:      fmt.Sprintf("Car %s has failed %d times. Latest: %v\n%s", carID, failCount, switchErr, conflictDetails),
			DB:           db,
			ProviderName: cfg.AgentProvider,
		})
		if escErr != nil {
			logger.Error("Escalation error", "car", carID, "error", escErr)
			return
		}
		handleEscalateResult(db, "", carID, res, logger)
	}(carID, failures, reason)
}

// getHeadCommit returns the current HEAD commit hash, or empty string on error.
func getHeadCommit(repoDir string) string {
	cmd := exec.Command("git", "rev-parse", "HEAD")
	cmd.Dir = repoDir
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// prReview holds a single review comment from a PR.
type prReview struct {
	Body   string
	Author string
}

// prInlineComment holds a file-level review comment from a PR.
type prInlineComment struct {
	Path   string // file path (e.g. "internal/dispatch/dispatch.go")
	Line   int    // line number in the diff
	Body   string
	Author string
}

// prConversationComment holds a top-level PR conversation comment.
type prConversationComment struct {
	Body   string
	Author string
}

// prStatus holds the GitHub PR status for a branch.
type prStatus struct {
	State          string // OPEN, MERGED, CLOSED
	ReviewDecision string // APPROVED, CHANGES_REQUESTED, REVIEW_REQUIRED, ""
	Mergeable      string // MERGEABLE, CONFLICTING, UNKNOWN
	Reviews        []prReview
}

// PRViewer abstracts GitHub PR status lookups and merge operations for testability.
type PRViewer interface {
	ViewPR(branch string) (*prStatus, error)
	FetchComments(branch string) ([]prInlineComment, []prConversationComment, error)
	MergePR(branch string) error
}

// ghPRViewer implements PRViewer using the gh CLI.
type ghPRViewer struct {
	repoDir string // git working directory (gh infers repo from remote)
}

func (g *ghPRViewer) ViewPR(branch string) (*prStatus, error) {
	cmd := exec.Command("gh", "pr", "view", branch,
		"--json", "state,reviewDecision,reviews,mergeable")
	cmd.Dir = g.repoDir
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("gh pr view %s: %w", branch, err)
	}

	var result struct {
		State          string `json:"state"`
		ReviewDecision string `json:"reviewDecision"`
		Mergeable      string `json:"mergeable"`
		Reviews        []struct {
			Body   string `json:"body"`
			Author struct {
				Login string `json:"login"`
			} `json:"author"`
		} `json:"reviews"`
	}
	if err := json.Unmarshal(out, &result); err != nil {
		return nil, fmt.Errorf("parse gh pr view: %w", err)
	}

	ps := &prStatus{
		State:          result.State,
		ReviewDecision: result.ReviewDecision,
		Mergeable:      result.Mergeable,
	}
	for _, r := range result.Reviews {
		ps.Reviews = append(ps.Reviews, prReview{Body: r.Body, Author: r.Author.Login})
	}
	return ps, nil
}

func (g *ghPRViewer) FetchComments(branch string) ([]prInlineComment, []prConversationComment, error) {
	// Step 1: Get PR number and conversation comments in one call.
	cmd := exec.Command("gh", "pr", "view", branch,
		"--json", "number,comments")
	cmd.Dir = g.repoDir
	out, err := cmd.Output()
	if err != nil {
		return nil, nil, fmt.Errorf("gh pr view %s: %w", branch, err)
	}

	var prData struct {
		Number   int `json:"number"`
		Comments []struct {
			Body   string `json:"body"`
			Author struct {
				Login string `json:"login"`
			} `json:"author"`
		} `json:"comments"`
	}
	if err := json.Unmarshal(out, &prData); err != nil {
		return nil, nil, fmt.Errorf("parse gh pr view comments: %w", err)
	}

	var conversation []prConversationComment
	for _, c := range prData.Comments {
		conversation = append(conversation, prConversationComment{
			Body:   c.Body,
			Author: c.Author.Login,
		})
	}

	// Step 2: Fetch inline/line-level review comments via the REST API.
	apiPath := fmt.Sprintf("repos/{owner}/{repo}/pulls/%d/comments", prData.Number)
	cmd2 := exec.Command("gh", "api", apiPath)
	cmd2.Dir = g.repoDir
	out2, err := cmd2.Output()
	if err != nil {
		// Non-fatal: return what we have (conversation comments).
		slog.Warn("gh api error", "path", apiPath, "error", err)
		return nil, conversation, nil
	}

	inline, err := parseInlineComments(out2)
	if err != nil {
		slog.Warn("Parse inline comments error", "pr", prData.Number, "error", err)
		return nil, conversation, nil
	}

	return inline, conversation, nil
}

// parseInlineComments parses the JSON response from the GitHub pulls/comments API.
func parseInlineComments(data []byte) ([]prInlineComment, error) {
	var raw []struct {
		Path string `json:"path"`
		Line int    `json:"line"`
		Body string `json:"body"`
		User struct {
			Login string `json:"login"`
		} `json:"user"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("json: %w", err)
	}
	var comments []prInlineComment
	for _, r := range raw {
		comments = append(comments, prInlineComment{
			Path:   r.Path,
			Line:   r.Line,
			Body:   r.Body,
			Author: r.User.Login,
		})
	}
	return comments, nil
}

func (g *ghPRViewer) MergePR(branch string) error {
	cmd := exec.Command("gh", "pr", "merge", branch, "--merge", "--delete-branch")
	cmd.Dir = g.repoDir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("gh pr merge %s: %s: %w", branch, string(out), err)
	}
	return nil
}

// handlePrOpenCars polls pr_open cars for GitHub review status and transitions
// them based on the PR state: changes_requested → open, merged → merged, closed → cancelled.
// When autoMerge is true, APPROVED PRs are automatically merged via the viewer.
func handlePrOpenCars(db *gorm.DB, viewer PRViewer, autoMerge bool, repoDir, ymDir string, cfg *config.Config, logger *slog.Logger) error {
	prCars, err := car.List(db, car.ListFilters{Status: "pr_open"})
	if err != nil {
		return err
	}

	for _, c := range prCars {
		if c.Branch == "" {
			continue
		}

		// Resolve base branch for this car.
		baseBranch := c.BaseBranch
		if baseBranch == "" {
			if cfg != nil && cfg.DefaultBranch != "" {
				baseBranch = cfg.DefaultBranch
			} else {
				baseBranch = "main"
			}
		}

		status, err := viewer.ViewPR(c.Branch)
		if err != nil {
			logger.Error("PR status error", "car", c.ID, "error", err)
			continue
		}

		switch {
		case status.Mergeable == "CONFLICTING" && status.State == "OPEN":
			// Check if base branch has advanced since last rebase attempt.
			currentBaseHead := getRemoteHeadCommit(repoDir, baseBranch)
			if currentBaseHead == "" {
				continue // unable to determine remote HEAD, skip
			}
			if c.LastRebaseBaseHead == currentBaseHead {
				continue // base hasn't moved, skip
			}

			// Attempt rebase using existing conflict resolution pipeline.
			// Use ymDir (yardmaster worktree) to avoid mutating primary repo.
			resolved, resolveErr := tryResolveConflict(ymDir, c.Branch, baseBranch)
			if resolved {
				if pushErr := gitForcePushBranch(ymDir, c.Branch); pushErr != nil {
					logger.Error("Force push after rebase failed", "car", c.ID, "error", pushErr)
					writeProgressNote(db, c.ID, "yardmaster", fmt.Sprintf("Rebase succeeded but force push failed: %v", pushErr))
					// Don't record base HEAD so the next cycle retries.
					continue
				}
				writeProgressNote(db, c.ID, "yardmaster", "Auto-rebased branch onto updated "+baseBranch)
				logger.Info("Auto-rebased PR branch", "car", c.ID)
			} else {
				detail := ""
				if resolveErr != nil {
					detail = resolveErr.Error()
				}
				writeProgressNote(db, c.ID, "yardmaster",
					fmt.Sprintf("PR has merge conflict that cannot be auto-resolved:\n%s", detail))
				messaging.Send(db, YardmasterID, "human", "escalate",
					fmt.Sprintf("Car %s PR has unresolvable merge conflict", c.ID),
					messaging.SendOpts{CarID: c.ID, Priority: "normal"})
				logger.Warn("PR has unresolvable conflict, human notified", "car", c.ID)
			}

			// Record base HEAD so we don't retry until the base advances again.
			if err := db.Model(&models.Car{}).Where("id = ?", c.ID).
				Update("last_rebase_base_head", currentBaseHead).Error; err != nil {
				logger.Error("Update last_rebase_base_head", "car", c.ID, "error", err)
			}

		case status.State == "MERGED":
			now := time.Now()
			if err := db.Model(&models.Car{}).Where("id = ?", c.ID).Updates(map[string]interface{}{
				"status":       "merged",
				"completed_at": now,
			}).Error; err != nil {
				logger.Error("Update car to merged", "car", c.ID, "error", err)
				continue
			}
			logger.Info("PR merged", "car", c.ID, "transition", "pr_open->merged")
			runPostMerge(db, c, logger)

		case status.State == "CLOSED":
			if err := db.Model(&models.Car{}).Where("id = ?", c.ID).Update("status", "cancelled").Error; err != nil {
				logger.Error("Update car to cancelled", "car", c.ID, "error", err)
				continue
			}
			logger.Info("PR closed", "car", c.ID, "transition", "pr_open->cancelled")

		case autoMerge && status.ReviewDecision == "APPROVED" && status.State == "OPEN":
			if err := viewer.MergePR(c.Branch); err != nil {
				logger.Error("Auto-merge PR failed", "car", c.ID, "error", err)
				writeProgressNote(db, c.ID, "yardmaster", fmt.Sprintf("Auto-merge failed: %v", err))
				continue
			}
			now := time.Now()
			if err := db.Model(&models.Car{}).Where("id = ?", c.ID).Updates(map[string]interface{}{
				"status":       "merged",
				"completed_at": now,
			}).Error; err != nil {
				logger.Error("Update car to merged after auto-merge", "car", c.ID, "error", err)
				continue
			}
			logger.Info("PR approved and auto-merged", "car", c.ID)
			runPostMerge(db, c, logger)

		case status.ReviewDecision == "CHANGES_REQUESTED":
			if err := db.Model(&models.Car{}).Where("id = ?", c.ID).Updates(map[string]interface{}{
				"status":   "open",
				"assignee": "",
			}).Error; err != nil {
				logger.Error("Update car to open", "car", c.ID, "error", err)
				continue
			}

			// Fetch inline and conversation comments for richer feedback.
			inline, conversation, fetchErr := viewer.FetchComments(c.Branch)
			if fetchErr != nil {
				logger.Warn("Fetch comments error", "car", c.ID, "error", fetchErr)
			}

			note := formatReviewNote(status.Reviews, inline, conversation)
			writeProgressNote(db, c.ID, "yardmaster", note)
			logger.Info("PR changes requested", "car", c.ID, "transition", "pr_open->open")
		}
	}

	return nil
}

// formatReviewNote builds a structured progress note from all PR feedback types.
func formatReviewNote(reviews []prReview, inline []prInlineComment, conversation []prConversationComment) string {
	var b strings.Builder
	b.WriteString("PR review: changes requested\n")

	// Review bodies (top-level review summaries).
	hasReviews := false
	for _, r := range reviews {
		if r.Body != "" {
			if !hasReviews {
				b.WriteString("\n## Review comments\n")
				hasReviews = true
			}
			fmt.Fprintf(&b, "- @%s: %s\n", r.Author, r.Body)
		}
	}

	// Inline/line-level comments with file:line context.
	if len(inline) > 0 {
		b.WriteString("\n## Inline comments\n")
		for _, c := range inline {
			if c.Line > 0 {
				fmt.Fprintf(&b, "- `%s` (line %d) @%s:\n  %s\n", c.Path, c.Line, c.Author, c.Body)
			} else {
				fmt.Fprintf(&b, "- `%s` @%s:\n  %s\n", c.Path, c.Author, c.Body)
			}
		}
	}

	// Conversation comments (general PR discussion).
	if len(conversation) > 0 {
		b.WriteString("\n## Conversation\n")
		for _, c := range conversation {
			fmt.Fprintf(&b, "- @%s: %s\n", c.Author, c.Body)
		}
	}

	return b.String()
}

// runPostMerge performs dependency unblocking and parent epic closure after a
// car is marked as merged. All merge paths (normal switch, auto-merge,
// externally merged PR, reconciliation) should call this to ensure consistent
// post-merge behavior.
func runPostMerge(db *gorm.DB, c models.Car, logger *slog.Logger) {
	unblocked, ubErr := UnblockDeps(db, c.ID)
	if ubErr != nil {
		logger.Error("Unblock deps", "car", c.ID, "error", ubErr)
	}
	if len(unblocked) > 0 {
		titles := make([]string, len(unblocked))
		for i, u := range unblocked {
			titles[i] = u.ID
			logger.Info("Car unblocked", "car", u.ID, "dependency", c.ID)
			if u.Type == "epic" {
				TryCloseEpic(db, u.ID)
			}
		}
		messaging.Send(db, "yardmaster", "broadcast", "deps-unblocked",
			fmt.Sprintf("Merged %s, unblocked: %s", c.ID, strings.Join(titles, ", ")),
			messaging.SendOpts{CarID: c.ID},
		)
	}
	if c.ParentID != nil && *c.ParentID != "" {
		TryCloseEpic(db, *c.ParentID)
	}
}

// sleepWithContext sleeps for duration d, returning early if ctx is cancelled.
func sleepWithContext(ctx context.Context, d time.Duration) {
	select {
	case <-ctx.Done():
	case <-time.After(d):
	}
}

// truncateSwitchLog returns the last n bytes of s for compact log output.
func truncateSwitchLog(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return "..." + s[len(s)-n:]
}
