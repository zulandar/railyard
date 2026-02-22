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
	maxTestFailures     = 2 // deprecated: use cfg.Stall.MaxSwitchFailures instead
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

	// Create the yardmaster worktree so switch operations don't disturb the
	// primary repo. Falls back to repoDir if worktree creation fails.
	ymDir, ymErr := engine.EnsureYardmasterWorktree(repoDir)
	if ymErr != nil {
		log.Printf("yardmaster: worktree setup warning: %v (using repo dir)", ymErr)
		ymDir = repoDir
	} else {
		fmt.Fprintf(out, "Yardmaster worktree ready at %s\n", ymDir)
	}

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
		if err := handleCompletedCars(ctx, db, cfg, repoDir, ymDir, out); err != nil {
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
// ymDir is the yardmaster worktree where switch operations happen; repoDir is
// the primary repo (used for engine worktree detachment).
func handleCompletedCars(ctx context.Context, db *gorm.DB, cfg *config.Config, repoDir, ymDir string, out io.Writer) error {
	cars, err := car.List(db, car.ListFilters{Status: "done"})
	if err != nil {
		return err
	}

	for _, c := range cars {
		fmt.Fprintf(out, "Completed car %s (%s) — switching\n", c.ID, c.Title)

		// Reset the yardmaster worktree to the car's base branch before each
		// switch so we start from a clean state.
		baseBranch := c.BaseBranch
		if baseBranch == "" {
			baseBranch = "main"
		}
		if ymDir != repoDir {
			if err := engine.SyncWorktreeToBranch(ymDir, baseBranch); err != nil {
				log.Printf("reset yardmaster worktree for %s: %v", c.ID, err)
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
			RepoDir:        ymDir,
			PrimaryRepoDir: repoDir,
			BaseBranch:     baseBranch,
			PreTestCommand: preTestCommand,
			TestCommand:    testCommand,
			RequirePR:      cfg.RequirePR,
		})

		// Handle any failure — write a categorized progress note and check
		// whether we've hit the escalation threshold.
		failCategory := SwitchFailNone
		if result != nil {
			failCategory = result.FailureCategory
		}

		if err != nil {
			log.Printf("switch car %s: %v", c.ID, err)

			if failCategory != SwitchFailNone {
				writeProgressNote(db, c.ID, YardmasterID,
					fmt.Sprintf("switch:%s: %v", failCategory, err))
			}

			maybeSwitchEscalate(ctx, db, cfg, c.ID, failCategory, err, out)
			continue
		}

		// Test failures return result with nil error but FailureCategory set.
		if failCategory != SwitchFailNone {
			writeProgressNote(db, c.ID, YardmasterID,
				fmt.Sprintf("switch:%s: %v", failCategory, result.Error))
			maybeSwitchEscalate(ctx, db, cfg, c.ID, failCategory, result.Error, out)
		}

		if result.PRCreated {
			fmt.Fprintf(out, "Car %s draft PR created: %s\n", c.ID, result.PRUrl)
		} else if result.Merged {
			if result.AlreadyMerged {
				fmt.Fprintf(out, "Car %s already merged (branch %s was ancestor of %s)\n", c.ID, result.Branch, baseBranch)
			} else {
				fmt.Fprintf(out, "Car %s merged and pushed (branch %s)\n", c.ID, result.Branch)
			}

			// Clean up the completing engine's overlay (non-fatal).
			if c.Assignee != "" {
				if err := engine.CleanupOverlay(c.Assignee, cfg); err != nil {
					log.Printf("overlay cleanup for %s: %v", c.Assignee, err)
				}
			}

			commitHash := getHeadCommit(ymDir)
			if err := CreateReindexJob(db, c.Track, commitHash); err != nil {
				log.Printf("create reindex job for %s: %v", c.Track, err)
			}
		} else if !result.TestsPassed {
			fmt.Fprintf(out, "Car %s tests failed — blocked\n", c.ID)
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
// their base branch (e.g., via a monolithic epic commit) and updates their
// status to "merged". Checks against origin/{baseBranch} (the remote truth)
// to avoid false positives from local-only merges that were never pushed.
// Cars are grouped by base branch so each group is checked against the correct target.
func reconcileStaleCars(db *gorm.DB, repoDir string, out io.Writer) error {
	// Fetch first to get current remote state.
	if err := gitFetch(repoDir); err != nil {
		return fmt.Errorf("reconcile fetch: %w", err)
	}

	// Find active cars with branches.
	var activeCars []models.Car
	if err := db.Where("status IN ? AND branch != ''",
		[]string{"open", "ready", "claimed", "in_progress"}).
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
			log.Printf("reconcile: skip base %s: %v", base, err)
			continue
		}

		for _, c := range cars {
			if mergedBranches[c.Branch] {
				fmt.Fprintf(out, "Reconciled car %s (%s) — branch %s already merged into %s\n", c.ID, c.Title, c.Branch, base)
				db.Model(&models.Car{}).Where("id = ?", c.ID).Updates(map[string]interface{}{
					"status":       "merged",
					"completed_at": now,
				})
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
// Deprecated: use countRecentSwitchFailures for the generalized counter.
func countRecentFailures(db *gorm.DB, carID string) int {
	var count int64
	db.Model(&models.CarProgress{}).
		Where("car_id = ? AND note LIKE ?", carID, "%test%fail%").
		Count(&count)
	return int(count)
}

// countRecentSwitchFailures counts all switch-categorized failure progress
// notes for a car. Each note has the form "switch:<category>: <details>".
func countRecentSwitchFailures(db *gorm.DB, carID string) int {
	var count int64
	db.Model(&models.CarProgress{}).
		Where("car_id = ? AND note LIKE ?", carID, "switch:%").
		Count(&count)
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
func maybeSwitchEscalate(ctx context.Context, db *gorm.DB, cfg *config.Config, carID string, cat SwitchFailureCategory, switchErr error, out io.Writer) {
	// Infrastructure failures escalate immediately — no threshold needed.
	// The human message was already sent by Switch(); here we also escalate
	// to Claude for a suggested action.
	if cat == SwitchFailInfra {
		reason := switchFailureReason(cat)
		fmt.Fprintf(out, "Car %s infra failure (%s) — escalating immediately\n", carID, reason)

		go func(carID, reason string) {
			res, escErr := EscalateToClaude(ctx, EscalateOpts{
				CarID:   carID,
				Reason:  reason,
				Details: fmt.Sprintf("Infrastructure test failure for car %s. The test command failed due to environment issues (missing dependencies, broken Docker, misconfigured commands), not code problems. Latest: %v", carID, switchErr),
				DB:      db,
			})
			if escErr != nil {
				log.Printf("escalation error for %s: %v", carID, escErr)
				return
			}
			handleEscalateResult(db, "", carID, res, out)
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

	reason := switchFailureReason(cat)
	fmt.Fprintf(out, "Car %s has %d switch failures (%s) — escalating\n", carID, failures, reason)

	go func(carID string, failCount int, reason string) {
		res, escErr := EscalateToClaude(ctx, EscalateOpts{
			CarID:   carID,
			Reason:  reason,
			Details: fmt.Sprintf("Car %s has failed %d times. Latest: %v", carID, failCount, switchErr),
			DB:      db,
		})
		if escErr != nil {
			log.Printf("escalation error for %s: %v", carID, escErr)
			return
		}
		handleEscalateResult(db, "", carID, res, out)
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

// sleepWithContext sleeps for duration d, returning early if ctx is cancelled.
func sleepWithContext(ctx context.Context, d time.Duration) {
	select {
	case <-ctx.Done():
	case <-time.After(d):
	}
}
