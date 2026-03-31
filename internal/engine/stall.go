package engine

import (
	"context"
	"fmt"
	"log"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/zulandar/railyard/internal/messaging"
	"github.com/zulandar/railyard/internal/models"
	"gorm.io/gorm"
)

// Default stall detection thresholds.
const (
	DefaultStdoutTimeout      = 120 * time.Second
	DefaultRepeatedErrorMax   = 3
	DefaultMaxClearCycles     = 5
	DefaultMaxAliveExtensions = 3
)

// StallConfig holds configurable thresholds for stall detection.
type StallConfig struct {
	StdoutTimeout      time.Duration
	RepeatedErrorMax   int
	MaxClearCycles     int
	MaxAliveExtensions int // max consecutive process-alive deferrals before stall fires (0 = use default)
}

// StallReason describes why a stall was detected.
type StallReason struct {
	Type    string // "stdout_timeout", "repeated_error", "excessive_cycles"
	Detail  string
	Snippet string // last output snippet for context
}

// StallDetector monitors a subprocess for stall conditions and escalates
// to the yardmaster when a stall is detected.
type StallDetector struct {
	cfg      StallConfig
	engineID string
	carID    string

	mu              sync.Mutex
	lastActivityAt  time.Time
	recentLines     []string // rolling window of recent output lines (up to 100)
	lastSnippet     string   // last chunk of output for context
	cycle           int
	aliveExtensions int // consecutive process-alive deferrals with no real output
	stopped         bool

	stallCh chan StallReason

	isProcessAlive func() bool // optional; returns true if subprocess PID is alive
}

// NewStallDetector creates a StallDetector with the given thresholds.
// It registers onWrite callbacks on both stdout and stderr to track
// output activity. Any write to either stream resets the activity timer.
func NewStallDetector(sess *Session, cfg StallConfig) *StallDetector {
	if cfg.StdoutTimeout <= 0 {
		cfg.StdoutTimeout = DefaultStdoutTimeout
	}
	if cfg.RepeatedErrorMax <= 0 {
		cfg.RepeatedErrorMax = DefaultRepeatedErrorMax
	}
	if cfg.MaxClearCycles <= 0 {
		cfg.MaxClearCycles = DefaultMaxClearCycles
	}
	if cfg.MaxAliveExtensions <= 0 {
		cfg.MaxAliveExtensions = DefaultMaxAliveExtensions
	}

	sd := &StallDetector{
		cfg:            cfg,
		engineID:       sess.EngineID,
		carID:          sess.CarID,
		lastActivityAt: time.Now(),
		stallCh:        make(chan StallReason, 1),
	}

	// Register callback on stdout to track activity and scan for repeated errors.
	sess.stdout.onWrite = func(p []byte) {
		sd.observeOutput(p)
	}

	// Register callback on stderr too — many tools (go test, compilers) write to stderr.
	// Stderr only resets the activity timer; repeated-error scanning stays stdout-only.
	sess.stderr.onWrite = func(p []byte) {
		sd.touchActivity()
	}

	// Set up process-alive checker using the subprocess PID.
	sd.isProcessAlive = func() bool {
		if sess.cmd == nil || sess.cmd.Process == nil {
			return false
		}
		return sess.cmd.Process.Signal(syscall.Signal(0)) == nil
	}

	return sd
}

// Start begins monitoring in a background goroutine. It checks for stdout
// timeout periodically. The goroutine exits when ctx is cancelled or a stall
// is detected.
func (sd *StallDetector) Start(ctx context.Context) {
	go sd.monitor(ctx)
}

// Stalled returns a channel that receives a StallReason when a stall is detected.
func (sd *StallDetector) Stalled() <-chan StallReason {
	return sd.stallCh
}

// SetCycle updates the current clear-cycle count and checks the threshold.
func (sd *StallDetector) SetCycle(cycle int) {
	sd.mu.Lock()
	defer sd.mu.Unlock()

	if sd.stopped {
		return
	}

	sd.cycle = cycle
	if cycle > sd.cfg.MaxClearCycles {
		sd.emitStall(StallReason{
			Type:    "excessive_cycles",
			Detail:  fmt.Sprintf("clear cycle count %d exceeds threshold %d", cycle, sd.cfg.MaxClearCycles),
			Snippet: sd.lastSnippet,
		})
	}
}

// Stop prevents the detector from emitting further stall events.
func (sd *StallDetector) Stop() {
	sd.mu.Lock()
	defer sd.mu.Unlock()
	sd.stopped = true
}

// HandleStall processes a detected stall: updates engine status to stalled,
// car status to blocked, and sends a message to the yardmaster.
// repoDir and branch are used to push the branch before the DB transaction
// so work survives worktree cleanup.
func HandleStall(db *gorm.DB, engineID, carID string, reason StallReason, repoDir, branch string) error {
	// Push branch to remote so work survives worktree cleanup.
	if branch != "" && repoDir != "" {
		if err := PushBranch(repoDir, branch); err != nil {
			log.Printf("engine: stall push warning (non-fatal): %v", err)
		}
	}

	return db.Transaction(func(tx *gorm.DB) error {
		// Update engine status to stalled.
		result := tx.Model(&models.Engine{}).Where("id = ?", engineID).
			Update("status", StatusStalled)
		if result.Error != nil {
			return fmt.Errorf("engine: mark stalled %s: %w", engineID, result.Error)
		}
		if result.RowsAffected == 0 {
			return fmt.Errorf("engine: engine %s not found", engineID)
		}

		// Update car status to blocked.
		result = tx.Model(&models.Car{}).Where("id = ?", carID).
			Updates(map[string]interface{}{
				"status":         "blocked",
				"blocked_reason": models.BlockedReasonStalled,
			})
		if result.Error != nil {
			return fmt.Errorf("engine: mark car blocked %s: %w", carID, result.Error)
		}
		if result.RowsAffected == 0 {
			return fmt.Errorf("engine: car %s not found", carID)
		}

		// Build message body with context.
		body := fmt.Sprintf("Engine: %s\nCar: %s\nStall type: %s\nReason: %s",
			engineID, carID, reason.Type, reason.Detail)
		if reason.Snippet != "" {
			body += fmt.Sprintf("\nLast output:\n%s", reason.Snippet)
		}

		// Send message to yardmaster.
		if _, err := messaging.Send(tx, engineID, "yardmaster", "engine-stalled", body, messaging.SendOpts{
			CarID:    carID,
			Priority: "urgent",
		}); err != nil {
			return fmt.Errorf("engine: send stall message: %w", err)
		}

		return nil
	})
}

// observeOutput is called on each stdout Write. It records the timestamp,
// updates the snippet, and scans for repeated error lines.
func (sd *StallDetector) observeOutput(p []byte) {
	sd.mu.Lock()
	defer sd.mu.Unlock()

	if sd.stopped {
		return
	}

	sd.lastActivityAt = time.Now()
	sd.aliveExtensions = 0

	chunk := string(p)

	// Keep last snippet for context (truncate to 500 chars).
	if len(chunk) > 500 {
		sd.lastSnippet = chunk[len(chunk)-500:]
	} else {
		sd.lastSnippet = chunk
	}

	// Split into lines and add to rolling window.
	lines := strings.Split(chunk, "\n")
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		sd.recentLines = append(sd.recentLines, trimmed)
	}

	// Keep window bounded.
	if len(sd.recentLines) > 100 {
		sd.recentLines = sd.recentLines[len(sd.recentLines)-100:]
	}

	// Check for repeated error lines.
	if repeated := sd.findRepeatedLine(); repeated != "" {
		sd.emitStall(StallReason{
			Type:    "repeated_error",
			Detail:  fmt.Sprintf("line repeated %d times: %s", sd.cfg.RepeatedErrorMax, repeated),
			Snippet: sd.lastSnippet,
		})
	}
}

// touchActivity resets the activity timer without doing any error scanning.
// Used by the stderr callback so that stderr writes keep the detector alive
// but do not contribute to repeated-error detection.
func (sd *StallDetector) touchActivity() {
	sd.mu.Lock()
	defer sd.mu.Unlock()
	if !sd.stopped {
		sd.lastActivityAt = time.Now()
		sd.aliveExtensions = 0
	}
}

// findRepeatedLine checks whether any line appears RepeatedErrorMax times
// in the recent window. Returns the repeated line or empty string.
func (sd *StallDetector) findRepeatedLine() string {
	counts := make(map[string]int)
	for _, line := range sd.recentLines {
		counts[line]++
		if counts[line] >= sd.cfg.RepeatedErrorMax {
			return line
		}
	}
	return ""
}

// monitor periodically checks for stdout timeout.
func (sd *StallDetector) monitor(ctx context.Context) {
	// Check at 1/4 of the timeout interval for responsiveness.
	// Floor is 50ms to allow short timeouts in tests, but cap the floor at 1s
	// for normal (>= 4s) timeouts.
	checkInterval := sd.cfg.StdoutTimeout / 4
	floor := time.Second
	if sd.cfg.StdoutTimeout < 4*time.Second {
		floor = 50 * time.Millisecond
	}
	if checkInterval < floor {
		checkInterval = floor
	}

	ticker := time.NewTicker(checkInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			sd.mu.Lock()
			if sd.stopped {
				sd.mu.Unlock()
				return
			}
			elapsed := time.Since(sd.lastActivityAt)
			snippet := sd.lastSnippet
			sd.mu.Unlock()

			if elapsed >= sd.cfg.StdoutTimeout {
				sd.mu.Lock()
				if !sd.stopped {
					if sd.isProcessAlive != nil && sd.isProcessAlive() && sd.aliveExtensions < sd.cfg.MaxAliveExtensions {
						// Process is alive and under extension limit — extend timeout, don't fire stall.
						sd.aliveExtensions++
						sd.lastActivityAt = time.Now()
						sd.mu.Unlock()
						continue
					}
					detail := fmt.Sprintf("no output for %s (threshold %s) and process is not alive", elapsed.Round(time.Second), sd.cfg.StdoutTimeout)
					if sd.aliveExtensions >= sd.cfg.MaxAliveExtensions {
						detail = fmt.Sprintf("no output for %d consecutive checks (alive-extension limit %d exceeded)", sd.aliveExtensions+1, sd.cfg.MaxAliveExtensions)
					}
					sd.emitStall(StallReason{
						Type:    "stdout_timeout",
						Detail:  detail,
						Snippet: snippet,
					})
				}
				sd.mu.Unlock()
				return
			}
		}
	}
}

// emitStall sends a stall reason (non-blocking) and marks detector as stopped.
// Must be called with sd.mu held.
func (sd *StallDetector) emitStall(reason StallReason) {
	if sd.stopped {
		return
	}
	sd.stopped = true
	select {
	case sd.stallCh <- reason:
	default:
	}
}
