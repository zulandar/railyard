package yardmaster

import (
	"context"
	"fmt"
	"io"
	"log"
	"os/exec"
	"strings"
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
	maxTestFailures     = 2
)

// RunDaemon runs the yardmaster daemon loop. It registers the yardmaster in the
// engines table, starts a heartbeat, and loops through inbox processing, stale
// engine detection, completed car switching, and blocked car unblocking.
func RunDaemon(ctx context.Context, db *gorm.DB, cfg *config.Config, configPath, repoDir string, pollInterval time.Duration, out io.Writer) error {
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
	if out == nil {
		out = io.Discard
	}

	startedAt := time.Now()
	if err := registerYardmaster(db); err != nil {
		return fmt.Errorf("yardmaster: register: %w", err)
	}
	fmt.Fprintf(out, "Yardmaster registered (id=%s)\n", YardmasterID)

	hbErrCh := engine.StartHeartbeat(ctx, db, YardmasterID, engine.DefaultHeartbeatInterval)

	fmt.Fprintf(out, "Yardmaster daemon starting (poll every %s)...\n", pollInterval)

	defer func() {
		fmt.Fprintf(out, "Yardmaster deregistering...\n")
		if err := engine.Deregister(db, YardmasterID); err != nil {
			log.Printf("yardmaster deregister error: %v", err)
		}
		fmt.Fprintf(out, "Yardmaster stopped.\n")
	}()

	rbState := &rebalanceState{lastTrackMoveAt: make(map[string]time.Time)}

	for {
		select {
		case <-ctx.Done():
			return nil
		case err := <-hbErrCh:
			return fmt.Errorf("yardmaster: heartbeat: %w", err)
		default:
		}

		// Phase 1: Process inbox.
		draining, err := processInbox(ctx, db, cfg, configPath, repoDir, startedAt, out)
		if err != nil {
			log.Printf("yardmaster inbox error: %v", err)
		}
		if draining {
			fmt.Fprintf(out, "Drain received, shutting down...\n")
			return nil
		}

		// Phase 2: Handle stale engines.
		if err := handleStaleEngines(db, cfg, configPath, out); err != nil {
			log.Printf("yardmaster stale engines error: %v", err)
		}

		// Phase 3: Handle completed cars.
		if err := handleCompletedCars(ctx, db, cfg, repoDir, out); err != nil {
			log.Printf("yardmaster completed cars error: %v", err)
		}

		// Phase 4: Handle blocked cars (safety-net sweep).
		if err := handleBlockedCars(db, out); err != nil {
			log.Printf("yardmaster blocked cars error: %v", err)
		}

		// Phase 4b: Sweep open epics whose children may all be complete.
		if err := sweepOpenEpics(db, out); err != nil {
			log.Printf("yardmaster sweep open epics error: %v", err)
		}

		// Phase 5: Reconcile stale cars whose branches are already merged.
		if err := reconcileStaleCars(db, repoDir, out); err != nil {
			log.Printf("yardmaster reconcile error: %v", err)
		}

		// Phase 6: Rebalance idle engines to busy tracks.
		if err := rebalanceEngines(db, cfg, configPath, rbState, out); err != nil {
			log.Printf("yardmaster rebalance error: %v", err)
		}

		sleepWithContext(ctx, pollInterval)
	}
}

// registerYardmaster creates or updates the yardmaster engine record.
func registerYardmaster(db *gorm.DB) error {
	now := time.Now()
	eng := models.Engine{
		ID:           YardmasterID,
		Track:        "*",
		Role:         "yardmaster",
		Status:       engine.StatusIdle,
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
		"track":         "*",
		"started_at":    now,
		"last_activity": now,
	}).Error
}

// processInbox drains the yardmaster inbox, classifying and handling each message.
// Returns true if a drain message was received (yardmaster should shut down).
// startedAt is when this yardmaster instance started; drain messages older than
// this are stale leftovers from a previous shutdown and are silently acked.
func processInbox(ctx context.Context, db *gorm.DB, cfg *config.Config, configPath, repoDir string, startedAt time.Time, out io.Writer) (draining bool, err error) {
	msgs, err := messaging.Inbox(db, YardmasterID)
	if err != nil {
		return false, err
	}

	for _, msg := range msgs {
		subject := strings.ToLower(msg.Subject)

		switch {
		case subject == "drain":
			if msg.CreatedAt.Before(startedAt) {
				fmt.Fprintf(out, "Inbox: stale drain message (from %s) — ignoring\n", msg.CreatedAt.Format(time.RFC3339))
				ackMsg(db, msg)
				continue
			}
			ackMsg(db, msg)
			return true, nil

		case subject == "engine-stalled":
			fmt.Fprintf(out, "Inbox: engine-stalled from %s — %s\n", msg.FromAgent, msg.Body)
			if msg.CarID != "" {
				writeProgressNote(db, msg.CarID, msg.FromAgent, fmt.Sprintf("Engine stalled: %s", msg.Body))
			}
			// Restart the stalled engine to spawn a replacement.
			if msg.FromAgent != "" && msg.FromAgent != YardmasterID {
				if err := orchestration.RestartEngine(db, configPath, msg.FromAgent, nil); err != nil {
					log.Printf("restart stalled engine %s: %v", msg.FromAgent, err)
					fmt.Fprintf(out, "Failed to restart stalled engine %s: %v\n", msg.FromAgent, err)
				} else {
					fmt.Fprintf(out, "Restarted stalled engine %s\n", msg.FromAgent)
				}
			}
			ackMsg(db, msg)

		case subject == "help" || subject == "stuck":
			fmt.Fprintf(out, "Inbox: %s from %s (car %s) — escalating to Claude\n", subject, msg.FromAgent, msg.CarID)
			go func(m models.Message) {
				result, escErr := EscalateToClaude(ctx, EscalateOpts{
					CarID:    m.CarID,
					EngineID: m.FromAgent,
					Reason:   m.Subject,
					Details:  m.Body,
					DB:       db,
				})
				if escErr != nil {
					log.Printf("escalation error: %v", escErr)
					return
				}
				handleEscalateResult(db, m.FromAgent, m.CarID, result, out)
			}(msg)
			ackMsg(db, msg)

		case subject == "test-failure":
			fmt.Fprintf(out, "Inbox: test-failure for car %s — acknowledged\n", msg.CarID)
			ackMsg(db, msg)

		case subject == "restart-engine":
			handleRestartEngine(ctx, db, cfg, configPath, msg, out)
			ackMsg(db, msg)

		case subject == "retry-merge":
			handleRetryMerge(db, msg, out)
			ackMsg(db, msg)

		case subject == "requeue-car":
			handleRequeueCar(db, msg, out)
			ackMsg(db, msg)

		case subject == "nudge-engine":
			handleNudgeEngine(db, msg, out)
			ackMsg(db, msg)

		case subject == "unblock-car":
			handleUnblockCar(db, msg, out)
			ackMsg(db, msg)

		case subject == "close-epic":
			handleCloseEpic(db, msg, out)
			ackMsg(db, msg)

		case subject == "reassignment" || subject == "deps-unblocked" || subject == "epic-closed":
			ackMsg(db, msg)

		case strings.Contains(subject, "done") || strings.Contains(subject, "complete"):
			fmt.Fprintf(out, "Inbox: engine %s sent %q — engines should use `ry complete`, not messages. Acknowledged.\n", msg.FromAgent, msg.Subject)
			ackMsg(db, msg)

		default:
			fmt.Fprintf(out, "Inbox: unknown subject %q from %s — acknowledged\n", msg.Subject, msg.FromAgent)
			ackMsg(db, msg)
		}
	}

	return false, nil
}

// ackMsg acknowledges a message, using broadcast ack for broadcast messages.
func ackMsg(db *gorm.DB, msg models.Message) {
	if msg.ToAgent == "broadcast" {
		if err := messaging.AcknowledgeBroadcast(db, msg.ID, YardmasterID); err != nil {
			log.Printf("broadcast ack error (msg %d): %v", msg.ID, err)
		}
	} else {
		if err := messaging.Acknowledge(db, msg.ID); err != nil {
			log.Printf("ack error (msg %d): %v", msg.ID, err)
		}
	}
}

// handleStaleEngines detects engines with stale heartbeats, reassigns their cars,
// and restarts the engines.
func handleStaleEngines(db *gorm.DB, cfg *config.Config, configPath string, out io.Writer) error {
	stale, err := StaleEngines(db)
	if err != nil {
		return err
	}

	for _, eng := range stale {
		if eng.ID == YardmasterID {
			continue
		}

		// Clean up dead engine's overlay before restart (non-fatal).
		if err := engine.CleanupOverlay(eng.ID, cfg); err != nil {
			log.Printf("overlay cleanup for stale engine %s: %v", eng.ID, err)
		}

		if eng.CurrentCar != "" {
			fmt.Fprintf(out, "Stale engine %s has car %s — reassigning and restarting\n", eng.ID, eng.CurrentCar)
			if err := ReassignCar(db, eng.CurrentCar, eng.ID, "stale heartbeat"); err != nil {
				log.Printf("reassign car %s from %s: %v", eng.CurrentCar, eng.ID, err)
			}
		} else {
			fmt.Fprintf(out, "Stale engine %s (idle) — restarting\n", eng.ID)
			db.Model(&models.Engine{}).Where("id = ?", eng.ID).Update("status", engine.StatusDead)
		}

		// Restart the engine to spawn a replacement on the same track.
		if err := orchestration.RestartEngine(db, configPath, eng.ID, nil); err != nil {
			log.Printf("restart stale engine %s: %v", eng.ID, err)
			fmt.Fprintf(out, "Failed to restart engine %s: %v\n", eng.ID, err)
		}
	}

	return nil
}

// handleCompletedCars finds cars with status "done" and runs the switch flow.
// Switch() marks cars as "merged" after successful merge, so they won't reappear.
func handleCompletedCars(ctx context.Context, db *gorm.DB, cfg *config.Config, repoDir string, out io.Writer) error {
	cars, err := car.List(db, car.ListFilters{Status: "done"})
	if err != nil {
		return err
	}

	anyMerged := false

	for _, c := range cars {
		fmt.Fprintf(out, "Completed car %s (%s) — switching\n", c.ID, c.Title)

		var testCommand string
		for _, t := range cfg.Tracks {
			if t.Name == c.Track {
				testCommand = t.TestCommand
				break
			}
		}

		result, err := Switch(db, c.ID, SwitchOpts{
			RepoDir:     repoDir,
			TestCommand: testCommand,
			RequirePR:   cfg.RequirePR,
		})
		if err != nil {
			log.Printf("switch car %s: %v", c.ID, err)

			failures := countRecentFailures(db, c.ID)
			if failures >= maxTestFailures {
				fmt.Fprintf(out, "Car %s failed tests %d times — escalating\n", c.ID, failures)
				go func(carID string, failCount int) {
					res, escErr := EscalateToClaude(ctx, EscalateOpts{
						CarID:   carID,
						Reason:  "repeated-test-failure",
						Details: fmt.Sprintf("Car %s has failed tests %d times", carID, failCount),
						DB:      db,
					})
					if escErr != nil {
						log.Printf("escalation error for %s: %v", carID, escErr)
						return
					}
					handleEscalateResult(db, "", carID, res, out)
				}(c.ID, failures)
			}
			continue
		}

		if result.PRCreated {
			fmt.Fprintf(out, "Car %s draft PR created: %s\n", c.ID, result.PRUrl)
		} else if result.Merged {
			anyMerged = true
			fmt.Fprintf(out, "Car %s merged (branch %s)\n", c.ID, result.Branch)

			// Clean up the completing engine's overlay (non-fatal).
			if c.Assignee != "" {
				if err := engine.CleanupOverlay(c.Assignee, cfg); err != nil {
					log.Printf("overlay cleanup for %s: %v", c.Assignee, err)
				}
			}

			commitHash := getHeadCommit(repoDir)
			if err := CreateReindexJob(db, c.Track, commitHash); err != nil {
				log.Printf("create reindex job for %s: %v", c.Track, err)
			}
		} else if !result.TestsPassed {
			fmt.Fprintf(out, "Car %s tests failed — blocked\n", c.ID)
		}
	}

	// Push main to remote once after all merges in this cycle.
	if anyMerged {
		if err := gitPush(repoDir); err != nil {
			log.Printf("push main to remote: %v", err)
			fmt.Fprintf(out, "Failed to push main to remote: %v\n", err)
		} else {
			fmt.Fprintf(out, "Pushed main to remote\n")
		}
	}

	return nil
}

// handleBlockedCars is a safety-net sweep that tries to unblock cars whose
// dependencies may have resolved outside the normal switch flow.
func handleBlockedCars(db *gorm.DB, out io.Writer) error {
	for _, status := range []string{"done", "merged"} {
		completedCars, err := car.List(db, car.ListFilters{Status: status})
		if err != nil {
			return err
		}

		for _, c := range completedCars {
			unblocked, err := UnblockDeps(db, c.ID)
			if err != nil {
				log.Printf("unblock deps for %s: %v", c.ID, err)
				continue
			}
			for _, u := range unblocked {
				fmt.Fprintf(out, "Unblocked car %s (dependency %s resolved)\n", u.ID, c.ID)
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
func sweepOpenEpics(db *gorm.DB, out io.Writer) error {
	openEpics, err := car.List(db, car.ListFilters{Status: "open", Type: "epic"})
	if err != nil {
		return err
	}

	for _, e := range openEpics {
		var remaining int64
		db.Model(&models.Car{}).
			Where("parent_id = ? AND status NOT IN ?", e.ID, []string{"done", "merged", "cancelled"}).
			Count(&remaining)

		if remaining == 0 {
			// Double-check the epic has at least one child (don't close empty epics).
			var total int64
			db.Model(&models.Car{}).Where("parent_id = ?", e.ID).Count(&total)
			if total > 0 {
				fmt.Fprintf(out, "Sweep: auto-closing epic %s (%s) — all children complete\n", e.ID, e.Title)
				TryCloseEpic(db, e.ID)
			}
		}
	}

	return nil
}

// reconcileStaleCars detects cars whose branches have already been merged to
// main (e.g., via a monolithic epic commit) and updates their status to "merged".
func reconcileStaleCars(db *gorm.DB, repoDir string, out io.Writer) error {
	// Get all branches merged into main.
	cmd := exec.Command("git", "branch", "-a", "--merged", "main")
	cmd.Dir = repoDir
	output, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("git branch --merged: %w", err)
	}

	mergedBranches := make(map[string]bool)
	for _, line := range strings.Split(string(output), "\n") {
		branch := strings.TrimSpace(line)
		branch = strings.TrimPrefix(branch, "* ")
		branch = strings.TrimPrefix(branch, "remotes/origin/")
		if branch != "" {
			mergedBranches[branch] = true
		}
	}

	// Find active cars whose branches are already merged.
	var activeCars []models.Car
	if err := db.Where("status IN ? AND branch != ''",
		[]string{"open", "ready", "claimed", "in_progress"}).
		Find(&activeCars).Error; err != nil {
		return fmt.Errorf("query active cars: %w", err)
	}

	now := time.Now()
	for _, c := range activeCars {
		if mergedBranches[c.Branch] {
			fmt.Fprintf(out, "Reconciled car %s (%s) — branch %s already merged\n", c.ID, c.Title, c.Branch)
			db.Model(&models.Car{}).Where("id = ?", c.ID).Updates(map[string]interface{}{
				"status":       "merged",
				"completed_at": now,
			})
		}
	}

	return nil
}

// handleEscalateResult acts on the decision returned by Claude escalation.
func handleEscalateResult(db *gorm.DB, engineID, carID string, result *EscalateResult, out io.Writer) {
	if result == nil {
		return
	}

	switch result.Action {
	case EscalateReassign:
		fmt.Fprintf(out, "Escalation: reassigning car %s\n", carID)
		if engineID != "" {
			ReassignCar(db, carID, engineID, "escalation: "+result.Message)
		}
	case EscalateGuidance:
		fmt.Fprintf(out, "Escalation: sending guidance to %s\n", engineID)
		if engineID != "" {
			messaging.Send(db, YardmasterID, engineID, "guidance", result.Message,
				messaging.SendOpts{CarID: carID})
		}
	case EscalateHuman:
		fmt.Fprintf(out, "Escalation: alerting human — %s\n", result.Message)
		messaging.Send(db, YardmasterID, "human", "escalate", result.Message,
			messaging.SendOpts{CarID: carID, Priority: "urgent"})
	case EscalateRetry:
		fmt.Fprintf(out, "Escalation: retry for car %s\n", carID)
	case EscalateSkip:
		fmt.Fprintf(out, "Escalation: skip for car %s\n", carID)
	}
}

// countRecentFailures counts test-failure progress notes for a car.
func countRecentFailures(db *gorm.DB, carID string) int {
	var count int64
	db.Model(&models.CarProgress{}).
		Where("car_id = ? AND note LIKE ?", carID, "%test%fail%").
		Count(&count)
	return int(count)
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

// sleepWithContext sleeps for duration d, returning early if ctx is cancelled.
func sleepWithContext(ctx context.Context, d time.Duration) {
	select {
	case <-ctx.Done():
	case <-time.After(d):
	}
}
