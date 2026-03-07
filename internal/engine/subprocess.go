package engine

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os/exec"
	"regexp"
	"sync"
	"syscall"
	"time"

	"github.com/zulandar/railyard/internal/models"
	"gorm.io/gorm"
)

// SpawnOpts holds parameters for spawning an agent subprocess.
type SpawnOpts struct {
	EngineID       string
	CarID          string
	ContextPayload string
	WorkDir        string // working directory for the agent
	ClaudeBinary   string // path to claude binary, default "claude" (legacy; prefer ProviderName)
	ProviderName   string // agent provider name (e.g., "claude", "codex"); defaults to "claude"
}

// Session represents a running claude subprocess.
type Session struct {
	ID       string
	EngineID string
	CarID    string
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
	carID     string
	direction string // "out" or "err"

	mu      sync.Mutex
	buf     bytes.Buffer
	writeFn func(models.AgentLog) error
	parseFn func(string) UsageStats // provider-specific output parser (nil for stderr)
	onWrite func([]byte)            // optional callback invoked on each Write
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

// SpawnAgent spawns an agent CLI subprocess with the given context payload.
// It resolves the provider from opts.ProviderName (defaulting to "claude")
// and delegates command building and output parsing to the provider.
func SpawnAgent(ctx context.Context, db *gorm.DB, opts SpawnOpts) (*Session, error) {
	if opts.EngineID == "" {
		return nil, fmt.Errorf("engine: engineID is required")
	}
	if opts.CarID == "" {
		return nil, fmt.Errorf("engine: carID is required")
	}
	if opts.ContextPayload == "" {
		return nil, fmt.Errorf("engine: contextPayload is required")
	}

	// Resolve provider (default to claude for backward compatibility).
	providerName := opts.ProviderName
	if providerName == "" {
		providerName = "claude"
	}
	provider, err := GetProvider(providerName)
	if err != nil {
		return nil, fmt.Errorf("engine: resolve provider: %w", err)
	}

	sessionID, err := GenerateSessionID()
	if err != nil {
		return nil, err
	}

	cmd, cancel := provider.BuildCommand(ctx, opts)

	parseFn := provider.ParseOutput
	stdoutWriter := newLogWriter(db, opts.EngineID, sessionID, opts.CarID, "out", parseFn)
	stderrWriter := newLogWriter(db, opts.EngineID, sessionID, opts.CarID, "err", nil)

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
		CarID:    opts.CarID,
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
		"-p", "Begin working on your assigned car. Follow the instructions in the system prompt.",
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
// parseFn is the provider-specific output parser (pass nil for stderr).
func newLogWriter(db *gorm.DB, engineID, sessionID, carID, direction string, parseFn func(string) UsageStats) *logWriter {
	return &logWriter{
		engineID:  engineID,
		sessionID: sessionID,
		carID:     carID,
		direction: direction,
		parseFn:   parseFn,
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

	content := redactSecrets(w.buf.String())
	w.buf.Reset()

	log := models.AgentLog{
		EngineID:  w.engineID,
		SessionID: w.sessionID,
		CarID:     w.carID,
		Direction: w.direction,
		Content:   content,
		CreatedAt: time.Now(),
	}

	if w.parseFn != nil {
		usage := w.parseFn(content)
		log.InputTokens = usage.InputTokens
		log.OutputTokens = usage.OutputTokens
		log.TokenCount = usage.InputTokens + usage.OutputTokens
		if usage.Model != "" {
			log.Model = usage.Model
		}
	}

	return w.writeFn(log)
}

// Close performs a final flush.
func (w *logWriter) Close() error {
	return w.Flush()
}

// secretPatterns matches common secret formats for redaction before DB storage.
var secretPatterns = []*regexp.Regexp{
	regexp.MustCompile(`sk-[a-zA-Z0-9_\-]{20,}`),          // OpenAI/Anthropic API keys
	regexp.MustCompile(`ghp_[a-zA-Z0-9]{36}`),             // GitHub PATs
	regexp.MustCompile(`gho_[a-zA-Z0-9]{36}`),             // GitHub OAuth tokens
	regexp.MustCompile(`ghs_[a-zA-Z0-9]{36}`),             // GitHub app tokens
	regexp.MustCompile(`github_pat_[a-zA-Z0-9_]{60,}`),    // GitHub fine-grained PATs
	regexp.MustCompile(`xoxb-[a-zA-Z0-9-]+`),              // Slack bot tokens
	regexp.MustCompile(`xoxp-[a-zA-Z0-9-]+`),              // Slack user tokens
	regexp.MustCompile(`xapp-[a-zA-Z0-9-]+`),              // Slack app tokens
	regexp.MustCompile(`AKIA[0-9A-Z]{16}`),                // AWS access keys
	regexp.MustCompile(`AIza[0-9A-Za-z_\-]{35}`),          // Google/Gemini API keys
	regexp.MustCompile(`Bearer [a-zA-Z0-9._\-]{20,}`),     // Bearer tokens
	regexp.MustCompile(`(\w+):([^@\s]{8,})@[a-zA-Z0-9.]`), // user:password@host in DSNs
}

// redactSecrets strips known secret patterns from log content before storage.
func redactSecrets(content string) string {
	for _, pat := range secretPatterns {
		content = pat.ReplaceAllString(content, "[REDACTED]")
	}
	return content
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
