package telegraph

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/zulandar/railyard/internal/models"
	"gorm.io/gorm"
)

// Default configuration values for ConversationStore.
const (
	DefaultMaxTurnsPerSession   = 100
	DefaultRecoveryLookbackDays = 30
)

// ConversationStore handles dual-write persistence and recovery of dispatch
// conversation history. Every message is written to Dolt (primary) and
// optionally echoed to the chat platform via the adapter.
type ConversationStore struct {
	db                   *gorm.DB
	adapter              Adapter
	maxTurnsPerSession   int
	recoveryLookbackDays int
}

// ConversationStoreOpts holds parameters for creating a ConversationStore.
type ConversationStoreOpts struct {
	DB                   *gorm.DB
	Adapter              Adapter // optional; enables dual-write to chat platform
	MaxTurnsPerSession   int     // defaults to DefaultMaxTurnsPerSession
	RecoveryLookbackDays int     // defaults to DefaultRecoveryLookbackDays
}

// NewConversationStore creates a ConversationStore.
func NewConversationStore(opts ConversationStoreOpts) (*ConversationStore, error) {
	if opts.DB == nil {
		return nil, fmt.Errorf("telegraph: conversation store: db is required")
	}
	maxTurns := opts.MaxTurnsPerSession
	if maxTurns <= 0 {
		maxTurns = DefaultMaxTurnsPerSession
	}
	lookback := opts.RecoveryLookbackDays
	if lookback <= 0 {
		lookback = DefaultRecoveryLookbackDays
	}
	return &ConversationStore{
		db:                   opts.DB,
		adapter:              opts.Adapter,
		maxTurnsPerSession:   maxTurns,
		recoveryLookbackDays: lookback,
	}, nil
}

// WriteUserMessage records a user message in the conversation. It writes to
// Dolt and, if an adapter is configured, sends the message text to the chat
// platform thread. Returns ErrMaxTurnsExceeded if the session has reached
// the maximum number of turns.
func (cs *ConversationStore) WriteUserMessage(ctx context.Context, sessionID uint, userName, text, platformMsgID string) error {
	seq, err := cs.nextSequence(sessionID)
	if err != nil {
		return err
	}
	if seq > cs.maxTurnsPerSession {
		return fmt.Errorf("telegraph: max turns exceeded (%d) for session %d", cs.maxTurnsPerSession, sessionID)
	}

	conv := models.TelegraphConversation{
		SessionID:     sessionID,
		Sequence:      seq,
		Role:          "user",
		UserName:      userName,
		Content:       text,
		PlatformMsgID: platformMsgID,
	}
	if err := cs.db.Create(&conv).Error; err != nil {
		return fmt.Errorf("telegraph: write user message: %w", err)
	}

	// Dual-write: echo to adapter if configured. This is best-effort — a
	// failure here does not roll back the Dolt write.
	if cs.adapter != nil {
		session := cs.lookupSession(sessionID)
		if session != nil {
			cs.adapter.Send(ctx, OutboundMessage{
				ChannelID: session.ChannelID,
				ThreadID:  session.PlatformThreadID,
				Text:      fmt.Sprintf("[%s] %s", userName, text),
			})
		}
	}

	return nil
}

// WriteAssistantMessage records an assistant response in the conversation.
// It writes to Dolt and, if an adapter is configured, sends the response to
// the chat platform thread.
func (cs *ConversationStore) WriteAssistantMessage(ctx context.Context, sessionID uint, text, platformMsgID string, carsReferenced []string) error {
	seq, err := cs.nextSequence(sessionID)
	if err != nil {
		return err
	}
	if seq > cs.maxTurnsPerSession {
		return fmt.Errorf("telegraph: max turns exceeded (%d) for session %d", cs.maxTurnsPerSession, sessionID)
	}

	carsJSON := "[]"
	if len(carsReferenced) > 0 {
		carsJSON = `["` + strings.Join(carsReferenced, `","`) + `"]`
	}

	conv := models.TelegraphConversation{
		SessionID:      sessionID,
		Sequence:       seq,
		Role:           "assistant",
		Content:        text,
		PlatformMsgID:  platformMsgID,
		CarsReferenced: carsJSON,
	}
	if err := cs.db.Create(&conv).Error; err != nil {
		return fmt.Errorf("telegraph: write assistant message: %w", err)
	}

	// Dual-write to adapter.
	if cs.adapter != nil {
		session := cs.lookupSession(sessionID)
		if session != nil {
			cs.adapter.Send(ctx, OutboundMessage{
				ChannelID: session.ChannelID,
				ThreadID:  session.PlatformThreadID,
				Text:      text,
			})
		}
	}

	return nil
}

// LoadHistory returns the full conversation history for a session, ordered
// by sequence number.
func (cs *ConversationStore) LoadHistory(sessionID uint) ([]models.TelegraphConversation, error) {
	var convos []models.TelegraphConversation
	result := cs.db.Where("session_id = ?", sessionID).
		Order("sequence").Find(&convos)
	if result.Error != nil {
		return nil, fmt.Errorf("telegraph: load history: %w", result.Error)
	}
	return convos, nil
}

// RecoverFromThread retrieves conversation history for a thread/channel,
// falling back to the adapter's ThreadHistory when no Dolt records exist.
// Only sessions within the lookback window are included.
func (cs *ConversationStore) RecoverFromThread(ctx context.Context, channelID, threadID string) ([]models.TelegraphConversation, error) {
	cutoff := time.Now().AddDate(0, 0, -cs.recoveryLookbackDays)

	var convos []models.TelegraphConversation
	result := cs.db.Where("session_id IN (?)",
		cs.db.Model(&models.DispatchSession{}).
			Select("id").
			Where("platform_thread_id = ? AND channel_id = ? AND created_at >= ?",
				threadID, channelID, cutoff),
	).Order("session_id, sequence").Find(&convos)

	if result.Error != nil {
		return nil, fmt.Errorf("telegraph: recover from thread: %w", result.Error)
	}

	if len(convos) > 0 {
		return convos, nil
	}

	// Fallback: adapter thread history — convert to TelegraphConversation.
	if cs.adapter != nil {
		msgs, err := cs.adapter.ThreadHistory(ctx, channelID, threadID, 50)
		if err != nil {
			return nil, fmt.Errorf("telegraph: adapter thread history: %w", err)
		}
		for i, m := range msgs {
			convos = append(convos, models.TelegraphConversation{
				Sequence:  i + 1,
				Role:      "user",
				UserName:  m.UserName,
				Content:   m.Text,
				CreatedAt: m.Timestamp,
			})
		}
	}

	return convos, nil
}

// TurnCount returns the number of messages in a session.
func (cs *ConversationStore) TurnCount(sessionID uint) (int, error) {
	var count int64
	result := cs.db.Model(&models.TelegraphConversation{}).
		Where("session_id = ?", sessionID).Count(&count)
	if result.Error != nil {
		return 0, fmt.Errorf("telegraph: turn count: %w", result.Error)
	}
	return int(count), nil
}

// nextSequence returns the next sequence number for a session.
func (cs *ConversationStore) nextSequence(sessionID uint) (int, error) {
	var maxSeq int
	result := cs.db.Model(&models.TelegraphConversation{}).
		Where("session_id = ?", sessionID).
		Select("COALESCE(MAX(sequence), 0)").Scan(&maxSeq)
	if result.Error != nil {
		return 0, fmt.Errorf("telegraph: next sequence: %w", result.Error)
	}
	return maxSeq + 1, nil
}

// lookupSession fetches a DispatchSession by ID (cached-friendly query).
func (cs *ConversationStore) lookupSession(sessionID uint) *models.DispatchSession {
	var session models.DispatchSession
	if err := cs.db.First(&session, sessionID).Error; err != nil {
		return nil
	}
	return &session
}
