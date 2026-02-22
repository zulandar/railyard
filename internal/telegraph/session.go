package telegraph

import (
	"context"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/zulandar/railyard/internal/models"
	"gorm.io/gorm"
)

// processTimeout is the maximum time a Claude subprocess can run before being
// killed. This prevents hung processes from blocking sessions indefinitely.
const processTimeout = 5 * time.Minute

// ProcessSpawner abstracts subprocess creation for testability.
type ProcessSpawner interface {
	// Spawn starts a dispatch subprocess and returns a handle for I/O.
	Spawn(ctx context.Context, prompt string) (Process, error)
}

// Process represents a running dispatch subprocess with piped I/O.
type Process interface {
	// Send writes a message to the subprocess stdin.
	Send(msg string) error
	// Recv returns a channel that delivers subprocess output lines.
	Recv() <-chan string
	// Done returns a channel that closes when the process exits.
	Done() <-chan struct{}
	// Close terminates the subprocess.
	Close() error
}

// SessionManager manages dispatch sessions for Telegraph. It tracks active
// sessions by thread/channel, spawns subprocesses, routes messages, and
// resumes dead sessions from conversation history.
type SessionManager struct {
	db      *gorm.DB
	adapter Adapter
	spawner ProcessSpawner
	timeout time.Duration

	mu       sync.RWMutex
	sessions map[string]*activeSession // key: "channelID:threadID"
}

// activeSession pairs a DB session with a running process.
type activeSession struct {
	dbSession *models.DispatchSession
	process   Process
	cancel    context.CancelFunc
}

// SessionManagerOpts holds parameters for creating a SessionManager.
type SessionManagerOpts struct {
	DB               *gorm.DB
	Adapter          Adapter
	Spawner          ProcessSpawner
	HeartbeatTimeout time.Duration // defaults to DefaultHeartbeatTimeout
}

// NewSessionManager creates a SessionManager.
func NewSessionManager(opts SessionManagerOpts) (*SessionManager, error) {
	if opts.DB == nil {
		return nil, fmt.Errorf("telegraph: session manager: db is required")
	}
	if opts.Spawner == nil {
		return nil, fmt.Errorf("telegraph: session manager: spawner is required")
	}
	timeout := opts.HeartbeatTimeout
	if timeout <= 0 {
		timeout = DefaultHeartbeatTimeout
	}
	return &SessionManager{
		db:       opts.DB,
		adapter:  opts.Adapter,
		spawner:  opts.Spawner,
		timeout:  timeout,
		sessions: make(map[string]*activeSession),
	}, nil
}

// sessionKey builds the map key for a session.
func sessionKey(channelID, threadID string) string {
	return channelID + ":" + threadID
}

// NewSession acquires the dispatch lock and spawns a new subprocess.
// Returns the DispatchSession on success.
func (sm *SessionManager) NewSession(ctx context.Context, source, userName, threadID, channelID string) (*models.DispatchSession, error) {
	dbSession, err := AcquireLock(sm.db, source, userName, threadID, channelID, sm.timeout)
	if err != nil {
		return nil, err
	}

	procCtx, cancel := context.WithTimeout(ctx, processTimeout)
	proc, err := sm.spawner.Spawn(procCtx, "")
	if err != nil {
		cancel()
		ReleaseLock(sm.db, dbSession.ID)
		return nil, fmt.Errorf("telegraph: spawn dispatch: %w", err)
	}

	key := sessionKey(channelID, threadID)
	sm.mu.Lock()
	sm.sessions[key] = &activeSession{
		dbSession: dbSession,
		process:   proc,
		cancel:    cancel,
	}
	sm.mu.Unlock()

	log.Printf("telegraph: session %d spawned [ch=%s thread=%s user=%s]",
		dbSession.ID, channelID, threadID, userName)

	// Relay subprocess output back to the chat platform.
	go sm.relayOutput(ctx, channelID, threadID, dbSession.ID, proc)

	// Monitor process exit and clean up.
	go sm.monitorProcess(key, dbSession.ID, proc)

	return dbSession, nil
}

// Route sends a message to the active session for the given thread/channel.
// It also records the message in the conversation history.
func (sm *SessionManager) Route(ctx context.Context, channelID, threadID, userName, text string) error {
	key := sessionKey(channelID, threadID)
	sm.mu.RLock()
	as, ok := sm.sessions[key]
	sm.mu.RUnlock()

	if !ok {
		return fmt.Errorf("telegraph: no active session for %s", key)
	}

	// Record conversation in DB.
	var maxSeq int
	sm.db.Model(&models.TelegraphConversation{}).
		Where("session_id = ?", as.dbSession.ID).
		Select("COALESCE(MAX(sequence), 0)").Scan(&maxSeq)

	conv := models.TelegraphConversation{
		SessionID: as.dbSession.ID,
		Sequence:  maxSeq + 1,
		Role:      "user",
		UserName:  userName,
		Content:   text,
	}
	sm.db.Create(&conv)

	// Send to subprocess.
	if err := as.process.Send(text); err != nil {
		return fmt.Errorf("telegraph: route message: %w", err)
	}

	// Refresh heartbeat.
	Heartbeat(sm.db, as.dbSession.ID)

	return nil
}

// Resume re-hydrates a dead session from conversation history and spawns
// a fresh subprocess. The newMessage is the user's latest input that triggered
// the resume — it is appended to the recovery context and included in the
// one-shot prompt so the subprocess can respond to it immediately.
func (sm *SessionManager) Resume(ctx context.Context, channelID, threadID, userName, newMessage string) (*models.DispatchSession, error) {
	// Build recovery context from Dolt conversation history.
	recoveryPrompt, err := sm.buildRecoveryContext(channelID, threadID)
	if err != nil {
		return nil, fmt.Errorf("telegraph: build recovery context: %w", err)
	}

	// Append the new message that triggered the resume.
	if newMessage != "" {
		if recoveryPrompt != "" {
			recoveryPrompt += "\n"
		}
		recoveryPrompt += fmt.Sprintf("\n[user] %s: %s", userName, newMessage)
	}

	// Record the new message in conversation history.
	// (We do this before spawning so it's included if a subsequent resume occurs.)

	// Acquire a new lock for the resumed session.
	dbSession, err := AcquireLock(sm.db, "telegraph", userName, threadID, channelID, sm.timeout)
	if err != nil {
		return nil, err
	}

	procCtx, cancel := context.WithTimeout(ctx, processTimeout)
	proc, err := sm.spawner.Spawn(procCtx, recoveryPrompt)
	if err != nil {
		cancel()
		ReleaseLock(sm.db, dbSession.ID)
		return nil, fmt.Errorf("telegraph: spawn resumed dispatch: %w", err)
	}

	key := sessionKey(channelID, threadID)
	sm.mu.Lock()
	sm.sessions[key] = &activeSession{
		dbSession: dbSession,
		process:   proc,
		cancel:    cancel,
	}
	sm.mu.Unlock()

	log.Printf("telegraph: session %d resumed [ch=%s thread=%s user=%s] recovery_len=%d",
		dbSession.ID, channelID, threadID, userName, len(recoveryPrompt))

	// Record the new message in conversation history for future resumes.
	if newMessage != "" {
		var maxSeq int
		sm.db.Model(&models.TelegraphConversation{}).
			Where("session_id = ?", dbSession.ID).
			Select("COALESCE(MAX(sequence), 0)").Scan(&maxSeq)

		sm.db.Create(&models.TelegraphConversation{
			SessionID: dbSession.ID,
			Sequence:  maxSeq + 1,
			Role:      "user",
			UserName:  userName,
			Content:   newMessage,
		})
	}

	// Relay subprocess output back to the chat platform.
	go sm.relayOutput(ctx, channelID, threadID, dbSession.ID, proc)

	go sm.monitorProcess(key, dbSession.ID, proc)

	return dbSession, nil
}

// HasSession returns true if there is an active session for the thread/channel.
func (sm *SessionManager) HasSession(channelID, threadID string) bool {
	key := sessionKey(channelID, threadID)
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	_, ok := sm.sessions[key]
	return ok
}

// HasHistoricSession returns true if there is a completed or expired session
// in the database for the given thread/channel (candidate for Resume).
func (sm *SessionManager) HasHistoricSession(channelID, threadID string) bool {
	var count int64
	sm.db.Model(&models.DispatchSession{}).
		Where("platform_thread_id = ? AND channel_id = ? AND status IN ?",
			threadID, channelID, []string{"completed", "expired"}).
		Count(&count)
	return count > 0
}

// CloseSession releases the lock and cleans up an active session.
func (sm *SessionManager) CloseSession(channelID, threadID string) error {
	key := sessionKey(channelID, threadID)
	sm.mu.Lock()
	as, ok := sm.sessions[key]
	if !ok {
		sm.mu.Unlock()
		return fmt.Errorf("telegraph: no active session for %s", key)
	}
	delete(sm.sessions, key)
	sm.mu.Unlock()

	as.process.Close()
	as.cancel()
	return ReleaseLock(sm.db, as.dbSession.ID)
}

// monitorProcess watches for process exit and cleans up the session.
func (sm *SessionManager) monitorProcess(key string, sessionID uint, proc Process) {
	<-proc.Done()

	log.Printf("telegraph: session %d process exited, cleaning up [key=%s]", sessionID, key)

	sm.mu.Lock()
	delete(sm.sessions, key)
	sm.mu.Unlock()

	ReleaseLock(sm.db, sessionID)
}

// buildRecoveryContext constructs a recovery prompt from conversation history.
// Primary source: Dolt TelegraphConversation rows. Fallback: adapter.ThreadHistory().
func (sm *SessionManager) buildRecoveryContext(channelID, threadID string) (string, error) {
	// Try Dolt conversation history first.
	var convos []models.TelegraphConversation
	result := sm.db.Where("session_id IN (?)",
		sm.db.Model(&models.DispatchSession{}).
			Select("id").
			Where("platform_thread_id = ? AND channel_id = ?", threadID, channelID),
	).Order("session_id, sequence").Find(&convos)

	if result.Error != nil {
		return "", fmt.Errorf("query conversations: %w", result.Error)
	}

	if len(convos) > 0 {
		return formatConversationHistory(convos), nil
	}

	// Fallback: adapter thread history.
	if sm.adapter != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		msgs, err := sm.adapter.ThreadHistory(ctx, channelID, threadID, 50)
		if err == nil && len(msgs) > 0 {
			return formatThreadHistory(msgs), nil
		}
	}

	return "", nil
}

// formatConversationHistory builds a prompt from Dolt conversation rows.
func formatConversationHistory(convos []models.TelegraphConversation) string {
	var b strings.Builder
	b.WriteString("Previous conversation context:\n\n")
	for _, c := range convos {
		fmt.Fprintf(&b, "[%s] %s: %s\n", c.Role, c.UserName, c.Content)
	}
	return b.String()
}

// formatThreadHistory builds a prompt from adapter thread messages.
func formatThreadHistory(msgs []ThreadMessage) string {
	var b strings.Builder
	b.WriteString("Previous thread context (from chat platform):\n\n")
	for _, m := range msgs {
		fmt.Fprintf(&b, "%s: %s\n", m.UserName, m.Text)
	}
	return b.String()
}

// relayOutput reads all lines from a process's Recv channel, accumulates
// them, and sends the result to the chat platform via the adapter. It also
// records the assistant response in the conversation history.
func (sm *SessionManager) relayOutput(ctx context.Context, channelID, threadID string, sessionID uint, proc Process) {
	var buf strings.Builder
	for line := range proc.Recv() {
		if buf.Len() > 0 {
			buf.WriteByte('\n')
		}
		buf.WriteString(line)
	}

	text := strings.TrimSpace(buf.String())
	if text == "" {
		log.Printf("telegraph: relay session %d: no output from process", sessionID)
		return
	}
	log.Printf("telegraph: relay session %d: %d chars output", sessionID, len(text))

	// Record assistant response in conversation history.
	var maxSeq int
	sm.db.Model(&models.TelegraphConversation{}).
		Where("session_id = ?", sessionID).
		Select("COALESCE(MAX(sequence), 0)").Scan(&maxSeq)

	conv := models.TelegraphConversation{
		SessionID: sessionID,
		Sequence:  maxSeq + 1,
		Role:      "assistant",
		Content:   text,
	}
	sm.db.Create(&conv)

	// Send to chat platform in chunks (Discord 2000 char limit).
	if sm.adapter == nil {
		return
	}
	chunks := chunkMessage(text, 2000)
	for _, chunk := range chunks {
		if err := sm.adapter.Send(ctx, OutboundMessage{
			ChannelID: channelID,
			ThreadID:  threadID,
			Text:      chunk,
		}); err != nil {
			log.Printf("telegraph: relay session %d: send error: %v", sessionID, err)
		}
	}
}

// chunkMessage splits text into chunks of at most maxLen characters.
// It prefers breaking at newlines when possible.
func chunkMessage(text string, maxLen int) []string {
	if maxLen <= 0 {
		maxLen = 2000
	}
	if len(text) <= maxLen {
		return []string{text}
	}

	var chunks []string
	for len(text) > 0 {
		if len(text) <= maxLen {
			chunks = append(chunks, text)
			break
		}

		// Look for a newline in the second half of the chunk to break at.
		chunk := text[:maxLen]
		breakAt := -1
		half := maxLen / 2
		for i := maxLen - 1; i >= half; i-- {
			if chunk[i] == '\n' {
				breakAt = i
				break
			}
		}

		if breakAt >= 0 {
			chunks = append(chunks, text[:breakAt])
			text = text[breakAt+1:] // skip the newline
		} else {
			chunks = append(chunks, chunk)
			text = text[maxLen:]
		}
	}
	return chunks
}
