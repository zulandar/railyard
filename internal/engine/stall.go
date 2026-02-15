package engine

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/zulandar/railyard/internal/messaging"
	"github.com/zulandar/railyard/internal/models"
	"gorm.io/gorm"
)

// Default stall detection thresholds.
const (
	DefaultStdoutTimeout    = 120 * time.Second
	DefaultRepeatedErrorMax = 3
	DefaultMaxClearCycles   = 5
)

// StallConfig holds configurable thresholds for stall detection.
type StallConfig struct {
	StdoutTimeout    time.Duration
	RepeatedErrorMax int
	MaxClearCycles   int
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
	beadID   string

	mu           sync.Mutex
	lastStdoutAt time.Time
	recentLines  []string // rolling window of recent output lines (up to 100)
	lastSnippet  string   // last chunk of output for context
	cycle        int
	stopped      bool

	stallCh chan StallReason
}

// NewStallDetector creates a StallDetector with the given thresholds.
// It registers an onWrite callback on the session's stdout logWriter
// to track output activity.
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

	sd := &StallDetector{
		cfg:          cfg,
		engineID:     sess.EngineID,
		beadID:       sess.BeadID,
		lastStdoutAt: time.Now(),
		stallCh:      make(chan StallReason, 1),
	}

	// Register callback on stdout to track activity and scan for repeated errors.
	sess.stdout.onWrite = func(p []byte) {
		sd.observeOutput(p)
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
// bead status to blocked, and sends a message to the yardmaster.
func HandleStall(db *gorm.DB, engineID, beadID string, reason StallReason) error {
	// Update engine status to stalled.
	if err := db.Model(&models.Engine{}).Where("id = ?", engineID).
		Update("status", StatusStalled).Error; err != nil {
		return fmt.Errorf("engine: mark stalled %s: %w", engineID, err)
	}

	// Update bead status to blocked.
	if err := db.Model(&models.Bead{}).Where("id = ?", beadID).
		Update("status", "blocked").Error; err != nil {
		return fmt.Errorf("engine: mark bead blocked %s: %w", beadID, err)
	}

	// Build message body with context.
	body := fmt.Sprintf("Engine: %s\nBead: %s\nStall type: %s\nReason: %s",
		engineID, beadID, reason.Type, reason.Detail)
	if reason.Snippet != "" {
		body += fmt.Sprintf("\nLast output:\n%s", reason.Snippet)
	}

	// Send message to yardmaster.
	_, err := messaging.Send(db, engineID, "yardmaster", "engine-stalled", body, messaging.SendOpts{
		BeadID:   beadID,
		Priority: "urgent",
	})
	if err != nil {
		return fmt.Errorf("engine: send stall message: %w", err)
	}

	return nil
}

// observeOutput is called on each stdout Write. It records the timestamp
// and scans for repeated error lines.
func (sd *StallDetector) observeOutput(p []byte) {
	sd.mu.Lock()
	defer sd.mu.Unlock()

	if sd.stopped {
		return
	}

	sd.lastStdoutAt = time.Now()

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
	checkInterval := sd.cfg.StdoutTimeout / 4
	if checkInterval < time.Second {
		checkInterval = time.Second
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
			elapsed := time.Since(sd.lastStdoutAt)
			snippet := sd.lastSnippet
			sd.mu.Unlock()

			if elapsed >= sd.cfg.StdoutTimeout {
				sd.mu.Lock()
				if !sd.stopped {
					sd.emitStall(StallReason{
						Type:    "stdout_timeout",
						Detail:  fmt.Sprintf("no stdout for %s (threshold %s)", elapsed.Round(time.Second), sd.cfg.StdoutTimeout),
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
