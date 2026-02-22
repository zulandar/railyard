package telegraph

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// MockAdapter implements Adapter and ThreadStarter for testing. It records
// sent messages and allows simulating inbound messages via SimulateInbound.
type MockAdapter struct {
	mu            sync.Mutex
	connected     bool
	closed        bool
	inbound       chan InboundMessage
	sent          []OutboundMessage
	history       map[string][]ThreadMessage // key: "channelID:threadID"
	botUserID     string
	threadCounter int // incremented for each StartThread call
}

// BotUserID returns the configured bot user ID (implements BotUserIDer).
func (m *MockAdapter) BotUserID() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.botUserID
}

// SetBotUserID sets the bot user ID for testing.
func (m *MockAdapter) SetBotUserID(id string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.botUserID = id
}

// NewMockAdapter creates a MockAdapter with a buffered inbound channel.
func NewMockAdapter() *MockAdapter {
	return &MockAdapter{
		inbound: make(chan InboundMessage, 100),
		history: make(map[string][]ThreadMessage),
	}
}

// Connect marks the adapter as connected.
func (m *MockAdapter) Connect(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return fmt.Errorf("mock adapter: already closed")
	}
	m.connected = true
	return nil
}

// Listen returns the inbound message channel. Must be called after Connect.
func (m *MockAdapter) Listen(ctx context.Context) (<-chan InboundMessage, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.connected {
		return nil, fmt.Errorf("mock adapter: not connected")
	}
	return m.inbound, nil
}

// Send records the outbound message.
func (m *MockAdapter) Send(ctx context.Context, msg OutboundMessage) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.connected {
		return fmt.Errorf("mock adapter: not connected")
	}
	m.sent = append(m.sent, msg)
	return nil
}

// ThreadHistory returns pre-configured history for a channel/thread pair.
func (m *MockAdapter) ThreadHistory(ctx context.Context, channelID, threadID string, limit int) ([]ThreadMessage, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := channelID + ":" + threadID
	msgs := m.history[key]
	if limit > 0 && limit < len(msgs) {
		msgs = msgs[len(msgs)-limit:]
	}
	return msgs, nil
}

// Close shuts down the mock adapter and closes the inbound channel.
func (m *MockAdapter) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return nil
	}
	m.closed = true
	m.connected = false
	close(m.inbound)
	return nil
}

// StartThread implements ThreadStarter. It records the ack reply as sent and
// returns the messageID as the thread ID (simulating thread creation from
// the user's message).
func (m *MockAdapter) StartThread(ctx context.Context, channelID, messageID, replyText, threadName string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.connected {
		return "", fmt.Errorf("mock adapter: not connected")
	}
	m.threadCounter++
	threadID := fmt.Sprintf("thread-%d", m.threadCounter)
	m.sent = append(m.sent, OutboundMessage{
		ChannelID: channelID,
		ThreadID:  threadID,
		Text:      replyText,
	})
	return threadID, nil
}

// --- Test helpers ---

// SimulateInbound sends a message into the inbound channel as if it came
// from the chat platform. Safe to call from any goroutine.
func (m *MockAdapter) SimulateInbound(msg InboundMessage) {
	if msg.Timestamp.IsZero() {
		msg.Timestamp = time.Now()
	}
	m.inbound <- msg
}

// LastSent returns the most recently sent outbound message.
// Returns zero value and false if no messages have been sent.
func (m *MockAdapter) LastSent() (OutboundMessage, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.sent) == 0 {
		return OutboundMessage{}, false
	}
	return m.sent[len(m.sent)-1], true
}

// SentCount returns the number of outbound messages sent.
func (m *MockAdapter) SentCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.sent)
}

// SetThreadHistory pre-populates thread history for testing ThreadHistory calls.
func (m *MockAdapter) SetThreadHistory(channelID, threadID string, msgs []ThreadMessage) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.history[channelID+":"+threadID] = msgs
}

// AllSent returns a copy of all sent outbound messages.
func (m *MockAdapter) AllSent() []OutboundMessage {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]OutboundMessage, len(m.sent))
	copy(out, m.sent)
	return out
}
