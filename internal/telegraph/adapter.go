// Package telegraph bridges Railyard events to chat platforms (Slack, Discord, etc.).
package telegraph

import (
	"context"
	"time"
)

// Adapter is the interface that platform-specific implementations must satisfy.
// Each adapter handles connection management, message sending/receiving, and
// thread history retrieval for a single chat platform.
type Adapter interface {
	// Connect establishes a connection to the chat platform.
	Connect(ctx context.Context) error

	// Listen returns a channel of inbound messages from the platform.
	// The channel is closed when the context is cancelled or the adapter
	// is closed. Listen must only be called after Connect.
	Listen(ctx context.Context) (<-chan InboundMessage, error)

	// Send delivers an outbound message to the platform.
	Send(ctx context.Context, msg OutboundMessage) error

	// ThreadHistory retrieves recent messages from a thread.
	ThreadHistory(ctx context.Context, channelID, threadID string, limit int) ([]ThreadMessage, error)

	// Close gracefully shuts down the adapter connection.
	Close() error
}

// InboundMessage represents a message received from the chat platform.
type InboundMessage struct {
	Platform  string    // e.g. "slack", "discord"
	ChannelID string    // platform-specific channel identifier
	ThreadID  string    // thread/conversation identifier (empty if top-level)
	MessageID string    // platform-specific message ID (Slack: ts, Discord: message snowflake)
	UserID    string    // platform-specific user identifier
	UserName  string    // human-readable username
	Text      string    // raw message text
	Timestamp time.Time // when the message was sent
}

// OutboundMessage represents a message to be sent to the chat platform.
type OutboundMessage struct {
	ChannelID string           // target channel
	ThreadID  string           // thread to reply in (empty for new top-level message)
	Text      string           // message text (platform-native formatting)
	Events    []FormattedEvent // structured event attachments
}

// FormattedEvent represents a Railyard event formatted for display in chat.
type FormattedEvent struct {
	Title    string  // event headline (e.g. "Car backend-42 merged")
	Body     string  // detail text
	Severity string  // "info", "warning", "error", "success"
	Color    string  // sidebar color hint (e.g. "#36a64f" for success)
	Fields   []Field // key-value metadata pairs
}

// Field is a key-value pair displayed in an event attachment.
type Field struct {
	Name  string
	Value string
	Short bool // hint: render side-by-side with another field
}

// BotUserIDer is an optional interface that adapters can implement to
// expose the bot's own user ID. This enables self-message filtering.
type BotUserIDer interface {
	BotUserID() string
}

// ThreadStarter is an optional interface that adapters can implement to
// support creating threads from messages. When a top-level @mention triggers
// a new dispatch session, the router uses this to create a thread from the
// user's original message and send the ack as the first reply.
type ThreadStarter interface {
	// StartThread creates a thread from an existing message and sends the
	// first reply (ack text) in that thread. messageID is the platform-specific
	// ID of the message to thread from (Slack: ts, Discord: snowflake).
	// Returns the thread ID that subsequent messages should be sent to.
	StartThread(ctx context.Context, channelID, messageID, replyText, threadName string) (threadID string, err error)
}

// ThreadMessage represents a single message within a thread history.
type ThreadMessage struct {
	UserID    string
	UserName  string
	Text      string
	Timestamp time.Time
}
