package engine

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os/exec"
	"sync"
	"syscall"
	"time"

	"github.com/zulandar/railyard/internal/models"
	"gorm.io/gorm"
)

// SpawnOpts holds parameters for spawning a claude subprocess.
type SpawnOpts struct {
	EngineID       string
	BeadID         string
	ContextPayload string
	WorkDir        string // working directory for claude
	ClaudeBinary   string // path to claude binary, default "claude"
}

// Session represents a running claude subprocess.
type Session struct {
	ID       string
	EngineID string
	BeadID   string
	PID      int

	cmd    *exec.Cmd
	cancel context.CancelFunc
	waitCh chan error // buffered(1), receives exit result
	stdout *logWriter
	stderr *logWriter
}

// logWriter implements io.Writer, buffering output and periodically flushing
// to agent_logs via an injected writeFn.
type logWriter struct {
	engineID  string
	sessionID string
	beadID    string
	direction string // "out" or "err"

	mu      sync.Mutex
	buf     bytes.Buffer
	writeFn func(models.AgentLog) error
	onWrite func([]byte) // optional callback invoked on each Write
}

// DefaultFlushInterval is the interval between periodic log flushes.
const DefaultFlushInterval = 5 * time.Second

// GenerateSessionID creates a unique session ID in sess-xxxxxxxx format (8-char hex).
func GenerateSessionID() (string, error) {
	b := make([]byte, 4)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("engine: generate session ID: %w", err)
	}
	return "sess-" + hex.EncodeToString(b), nil
}

// SpawnAgent spawns a claude CLI subprocess with the given context payload.
func SpawnAgent(ctx context.Context, db *gorm.DB, opts SpawnOpts) (*Session, error) {
	if opts.EngineID == "" {
		return nil, fmt.Errorf("engine: engineID is required")
	}
	if opts.BeadID == "" {
		return nil, fmt.Errorf("engine: beadID is required")
	}
	if opts.ContextPayload == "" {
		return nil, fmt.Errorf("engine: contextPayload is required")
	}

	sessionID, err := GenerateSessionID()
	if err != nil {
		return nil, err
	}

	cmd, cancel := buildCommand(ctx, opts)

	stdoutWriter := newLogWriter(db, opts.EngineID, sessionID, opts.BeadID, "out")
	stderrWriter := newLogWriter(db, opts.EngineID, sessionID, opts.BeadID, "err")

	cmd.Stdout = stdoutWriter
	cmd.Stderr = stderrWriter

	if err := cmd.Start(); err != nil {
		cancel()
		return nil, fmt.Errorf("engine: start claude: %w", err)
	}

	waitCh := make(chan error, 1)

	// Flush goroutine context: cancelled when process exits.
	flushCtx, flushCancel := context.WithCancel(ctx)

	startFlusher(flushCtx, stdoutWriter, DefaultFlushInterval)
	startFlusher(flushCtx, stderrWriter, DefaultFlushInterval)

	// Wait goroutine: waits for process, final flushes, sends result.
	go func() {
		waitErr := cmd.Wait()
		flushCancel()
		stdoutWriter.Close()
		stderrWriter.Close()
		waitCh <- waitErr
	}()

	// Update engine's session ID.
	if err := db.Model(&models.Engine{}).Where("id = ?", opts.EngineID).
		Update("session_id", sessionID).Error; err != nil {
		// Non-fatal: log capture still works even if this fails.
		// The caller can check engine state separately.
	}

	return &Session{
		ID:       sessionID,
		EngineID: opts.EngineID,
		BeadID:   opts.BeadID,
		PID:      cmd.Process.Pid,
		cmd:      cmd,
		cancel:   cancel,
		waitCh:   waitCh,
		stdout:   stdoutWriter,
		stderr:   stderrWriter,
	}, nil
}

// Wait blocks until the subprocess exits and returns its error (if any).
func (s *Session) Wait() error {
	return <-s.waitCh
}

// Done returns a channel that receives the subprocess exit result.
func (s *Session) Done() <-chan error {
	return s.waitCh
}

// buildCommand constructs the exec.Cmd for the claude CLI.
func buildCommand(ctx context.Context, opts SpawnOpts) (*exec.Cmd, context.CancelFunc) {
	ctx, cancel := context.WithCancel(ctx)

	binary := opts.ClaudeBinary
	if binary == "" {
		binary = "claude"
	}

	cmd := exec.CommandContext(ctx, binary,
		"--dangerously-skip-permissions",
		"--verbose",
		"--output-format", "stream-json",
		"--system-prompt", opts.ContextPayload,
		"-p", "Begin working on your assigned bead. Follow the instructions in the system prompt.",
	)

	if opts.WorkDir != "" {
		cmd.Dir = opts.WorkDir
	}

	cmd.Cancel = func() error {
		return cmd.Process.Signal(syscall.SIGTERM)
	}
	cmd.WaitDelay = 10 * time.Second

	return cmd, cancel
}

// newLogWriter creates a logWriter that flushes to the DB via db.Create.
func newLogWriter(db *gorm.DB, engineID, sessionID, beadID, direction string) *logWriter {
	return &logWriter{
		engineID:  engineID,
		sessionID: sessionID,
		beadID:    beadID,
		direction: direction,
		writeFn: func(log models.AgentLog) error {
			return db.Create(&log).Error
		},
	}
}

// Write appends bytes to the internal buffer (implements io.Writer).
func (w *logWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	n, err := w.buf.Write(p)
	if w.onWrite != nil {
		w.onWrite(p)
	}
	return n, err
}

// Flush writes accumulated buffer contents to agent_logs and resets the buffer.
func (w *logWriter) Flush() error {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.buf.Len() == 0 {
		return nil
	}

	content := w.buf.String()
	w.buf.Reset()

	return w.writeFn(models.AgentLog{
		EngineID:  w.engineID,
		SessionID: w.sessionID,
		BeadID:    w.beadID,
		Direction: w.direction,
		Content:   content,
		CreatedAt: time.Now(),
	})
}

// Close performs a final flush.
func (w *logWriter) Close() error {
	return w.Flush()
}

// startFlusher launches a goroutine that periodically flushes the logWriter.
func startFlusher(ctx context.Context, w *logWriter, interval time.Duration) {
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				w.Flush()
			}
		}
	}()
}
