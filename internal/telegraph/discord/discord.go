// Package discord implements the telegraph Adapter for Discord using the Gateway WebSocket.
package discord

import (
	"context"
	"fmt"
	"log"
	"math"
	"sync"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/zulandar/railyard/internal/telegraph"
)

const (
	// maxRetries is the max number of retries for rate-limited API calls.
	maxRetries = 3
	// baseBackoff is the initial backoff duration for reconnection.
	baseBackoff = 2 * time.Second
	// maxBackoff caps the exponential backoff for reconnection.
	maxBackoff = 2 * time.Minute
	// maxReconnectAttempts limits reconnection retries before giving up.
	maxReconnectAttempts = 10
	// defaultPageSize is the default number of messages per page for history.
	defaultPageSize = 100
)

// session abstracts the discordgo.Session methods we use, enabling test mocks.
type session interface {
	Open() error
	Close() error
	Channel(channelID string) (*discordgo.Channel, error)
	ChannelMessageSend(channelID, content string, options ...discordgo.RequestOption) (*discordgo.Message, error)
	ChannelMessageSendEmbed(channelID string, embed *discordgo.MessageEmbed, options ...discordgo.RequestOption) (*discordgo.Message, error)
	ChannelMessageSendComplex(channelID string, data *discordgo.MessageSend, options ...discordgo.RequestOption) (*discordgo.Message, error)
	MessageThreadStartComplex(channelID, messageID string, data *discordgo.ThreadStart) (*discordgo.Channel, error)
	ChannelMessages(channelID string, limit int, beforeID, afterID, aroundID string, options ...discordgo.RequestOption) ([]*discordgo.Message, error)
	AddHandler(handler interface{}) func()
}

// realSession wraps *discordgo.Session to implement the session interface.
type realSession struct {
	s *discordgo.Session
}

func (r *realSession) Open() error { return r.s.Open() }
func (r *realSession) Close() error {
	return r.s.Close()
}
func (r *realSession) Channel(channelID string) (*discordgo.Channel, error) {
	return r.s.State.Channel(channelID)
}
func (r *realSession) ChannelMessageSend(channelID, content string, options ...discordgo.RequestOption) (*discordgo.Message, error) {
	return r.s.ChannelMessageSend(channelID, content, options...)
}
func (r *realSession) ChannelMessageSendEmbed(channelID string, embed *discordgo.MessageEmbed, options ...discordgo.RequestOption) (*discordgo.Message, error) {
	return r.s.ChannelMessageSendEmbed(channelID, embed, options...)
}
func (r *realSession) ChannelMessageSendComplex(channelID string, data *discordgo.MessageSend, options ...discordgo.RequestOption) (*discordgo.Message, error) {
	return r.s.ChannelMessageSendComplex(channelID, data, options...)
}
func (r *realSession) MessageThreadStartComplex(channelID, messageID string, data *discordgo.ThreadStart) (*discordgo.Channel, error) {
	return r.s.MessageThreadStartComplex(channelID, messageID, data)
}
func (r *realSession) ChannelMessages(channelID string, limit int, beforeID, afterID, aroundID string, options ...discordgo.RequestOption) ([]*discordgo.Message, error) {
	return r.s.ChannelMessages(channelID, limit, beforeID, afterID, aroundID, options...)
}
func (r *realSession) AddHandler(handler interface{}) func() {
	return r.s.AddHandler(handler)
}

// Adapter implements telegraph.Adapter for Discord via the Gateway WebSocket.
type Adapter struct {
	sess          session
	botToken      string
	channelID     string // default channel for messages
	botUserID     string
	mu            sync.Mutex
	connected     bool
	closed        bool
	inbound       chan telegraph.InboundMessage
	cancelFunc    context.CancelFunc
	removeHandler func()
	baseBackoff   time.Duration
	maxBackoff    time.Duration
	maxReconnect  int
}

// AdapterOpts holds parameters for creating a Discord Adapter.
type AdapterOpts struct {
	BotToken  string // Discord bot token
	ChannelID string // default channel to post to
	// For testing: inject a mock session instead of real Discord API.
	Session session
}

// New creates a Discord Adapter.
func New(opts AdapterOpts) (*Adapter, error) {
	if opts.Session == nil && opts.BotToken == "" {
		return nil, fmt.Errorf("discord: bot token is required")
	}

	a := &Adapter{
		botToken:     opts.BotToken,
		channelID:    opts.ChannelID,
		inbound:      make(chan telegraph.InboundMessage, 100),
		baseBackoff:  baseBackoff,
		maxBackoff:   maxBackoff,
		maxReconnect: maxReconnectAttempts,
	}

	if opts.Session != nil {
		a.sess = opts.Session
	}

	return a, nil
}

// Connect establishes the Discord Gateway WebSocket connection.
func (a *Adapter) Connect(ctx context.Context) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.closed {
		return fmt.Errorf("discord: adapter already closed")
	}
	if a.connected {
		return nil
	}

	// Create real session if not injected (production path).
	if a.sess == nil {
		dg, err := discordgo.New("Bot " + a.botToken)
		if err != nil {
			return fmt.Errorf("discord: create session: %w", err)
		}
		dg.Identify.Intents = discordgo.IntentsGuildMessages | discordgo.IntentsMessageContent
		a.sess = &realSession{s: dg}
	}

	// Register Ready handler to capture bot user ID on connect/reconnect.
	a.sess.AddHandler(func(_ *discordgo.Session, r *discordgo.Ready) {
		a.mu.Lock()
		a.botUserID = r.User.ID
		a.mu.Unlock()
		log.Printf("discord: connected as %s (ID: %s)", r.User.Username, r.User.ID)
	})

	// Register Disconnect handler — discordgo handles reconnection automatically,
	// but we log it for observability.
	a.sess.AddHandler(func(_ *discordgo.Session, d *discordgo.Disconnect) {
		log.Printf("discord: gateway disconnected, discordgo will auto-reconnect")
	})

	// Register Resumed handler for reconnection awareness.
	a.sess.AddHandler(func(_ *discordgo.Session, r *discordgo.Resumed) {
		log.Printf("discord: gateway session resumed")
	})

	if err := a.sess.Open(); err != nil {
		return fmt.Errorf("discord: open gateway: %w", err)
	}

	a.connected = true
	return nil
}

// Listen returns a channel of inbound messages from Discord. Registers a
// message handler on the Gateway session. Must be called after Connect.
func (a *Adapter) Listen(ctx context.Context) (<-chan telegraph.InboundMessage, error) {
	a.mu.Lock()
	if !a.connected {
		a.mu.Unlock()
		return nil, fmt.Errorf("discord: not connected")
	}
	a.mu.Unlock()

	listenCtx, cancel := context.WithCancel(ctx)
	a.mu.Lock()
	a.cancelFunc = cancel
	a.mu.Unlock()

	// Register message handler.
	remove := a.sess.AddHandler(func(_ *discordgo.Session, m *discordgo.MessageCreate) {
		a.handleMessage(m)
	})
	a.mu.Lock()
	a.removeHandler = remove
	a.mu.Unlock()

	// Close inbound channel when context is cancelled.
	go func() {
		<-listenCtx.Done()
	}()

	return a.inbound, nil
}

// Send delivers a message to Discord. Translates OutboundMessage to Discord Embeds.
func (a *Adapter) Send(ctx context.Context, msg telegraph.OutboundMessage) error {
	a.mu.Lock()
	if !a.connected {
		a.mu.Unlock()
		return fmt.Errorf("discord: not connected")
	}
	a.mu.Unlock()

	// In Discord, threads are channels. If ThreadID is set, send there directly.
	channelID := msg.ThreadID
	if channelID == "" {
		channelID = msg.ChannelID
	}
	if channelID == "" {
		channelID = a.channelID
	}
	if channelID == "" {
		return fmt.Errorf("discord: no channel specified")
	}

	// Build the message.
	data := buildMessageSend(msg)

	err := a.retryOnRateLimit(ctx, func() error {
		_, sendErr := a.sess.ChannelMessageSendComplex(channelID, data)
		return sendErr
	})
	if err != nil {
		return fmt.Errorf("discord: send message: %w", err)
	}
	return nil
}

// ThreadHistory retrieves messages from a Discord thread channel.
// Discord threads are actual channel objects with their own IDs, so threadID
// is the channel ID of the thread.
func (a *Adapter) ThreadHistory(ctx context.Context, channelID, threadID string, limit int) ([]telegraph.ThreadMessage, error) {
	a.mu.Lock()
	if !a.connected {
		a.mu.Unlock()
		return nil, fmt.Errorf("discord: not connected")
	}
	a.mu.Unlock()

	// In Discord, threadID IS the channel ID of the thread.
	targetChannel := threadID
	if targetChannel == "" {
		targetChannel = channelID
	}

	var allMsgs []telegraph.ThreadMessage
	beforeID := ""

	pageSize := defaultPageSize
	if limit > 0 && limit < pageSize {
		pageSize = limit
	}

	for {
		var msgs []*discordgo.Message
		err := a.retryOnRateLimit(ctx, func() error {
			var apiErr error
			msgs, apiErr = a.sess.ChannelMessages(targetChannel, pageSize, beforeID, "", "")
			return apiErr
		})
		if err != nil {
			return nil, fmt.Errorf("discord: channel messages: %w", err)
		}

		if len(msgs) == 0 {
			break
		}

		for _, m := range msgs {
			allMsgs = append(allMsgs, telegraph.ThreadMessage{
				UserID:    m.Author.ID,
				UserName:  m.Author.Username,
				Text:      m.Content,
				Timestamp: m.Timestamp,
			})
		}

		if limit > 0 && len(allMsgs) >= limit {
			allMsgs = allMsgs[:limit]
			break
		}

		// Paginate backwards: use the last message ID as the "before" cursor.
		beforeID = msgs[len(msgs)-1].ID

		if len(msgs) < pageSize {
			break // no more pages
		}
	}

	return allMsgs, nil
}

// Close gracefully shuts down the adapter connection.
func (a *Adapter) Close() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.closed {
		return nil
	}
	a.closed = true
	a.connected = false
	if a.cancelFunc != nil {
		a.cancelFunc()
	}
	if a.removeHandler != nil {
		a.removeHandler()
	}
	close(a.inbound)
	if a.sess != nil {
		return a.sess.Close()
	}
	return nil
}

// BotUserID returns the bot's Discord user ID (available after Connect and first message).
func (a *Adapter) BotUserID() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.botUserID
}

// SetBotUserID sets the bot user ID (used for self-message filtering).
func (a *Adapter) SetBotUserID(id string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.botUserID = id
}

// handleMessage converts a Discord message event to an InboundMessage.
func (a *Adapter) handleMessage(m *discordgo.MessageCreate) {
	if m.Author == nil {
		return
	}

	// Filter bot self-messages.
	a.mu.Lock()
	botID := a.botUserID
	a.mu.Unlock()

	if m.Author.ID == botID {
		return
	}

	// Filter bot messages.
	if m.Author.Bot {
		return
	}

	// Determine thread context. In Discord, threads are channels — a message's
	// ChannelID is the thread ID if it was sent inside a thread. We look up the
	// channel from the state cache to detect this and resolve the parent channel.
	channelID := m.ChannelID
	threadID := ""

	if ch, err := a.sess.Channel(m.ChannelID); err == nil && ch.IsThread() {
		channelID = ch.ParentID
		threadID = m.ChannelID
	}

	ts, _ := discordgo.SnowflakeTimestamp(m.ID)

	a.inbound <- telegraph.InboundMessage{
		Platform:  "discord",
		ChannelID: channelID,
		ThreadID:  threadID,
		UserID:    m.Author.ID,
		UserName:  m.Author.Username,
		Text:      m.Content,
		Timestamp: ts,
	}
}

// buildMessageSend translates an OutboundMessage into a Discord MessageSend.
func buildMessageSend(msg telegraph.OutboundMessage) *discordgo.MessageSend {
	data := &discordgo.MessageSend{
		Content: msg.Text,
	}

	if len(msg.Events) > 0 {
		for _, evt := range msg.Events {
			data.Embeds = append(data.Embeds, eventToEmbed(evt))
		}
	}

	return data
}

// eventToEmbed converts a FormattedEvent to a Discord Embed.
func eventToEmbed(evt telegraph.FormattedEvent) *discordgo.MessageEmbed {
	embed := &discordgo.MessageEmbed{
		Title:       evt.Title,
		Description: evt.Body,
	}

	if evt.Color != "" {
		embed.Color = parseHexColor(evt.Color)
	}

	for _, f := range evt.Fields {
		embed.Fields = append(embed.Fields, &discordgo.MessageEmbedField{
			Name:   f.Name,
			Value:  f.Value,
			Inline: f.Short,
		})
	}

	return embed
}

// parseHexColor converts a hex color string (e.g. "#36a64f") to an int.
func parseHexColor(hex string) int {
	if len(hex) > 0 && hex[0] == '#' {
		hex = hex[1:]
	}
	var color int
	for _, c := range hex {
		color <<= 4
		switch {
		case c >= '0' && c <= '9':
			color |= int(c - '0')
		case c >= 'a' && c <= 'f':
			color |= int(c-'a') + 10
		case c >= 'A' && c <= 'F':
			color |= int(c-'A') + 10
		}
	}
	return color
}

// retryOnRateLimit calls fn and retries with exponential backoff on Discord
// rate limit errors. It respects context cancellation.
func (a *Adapter) retryOnRateLimit(ctx context.Context, fn func() error) error {
	for attempt := 0; attempt <= maxRetries; attempt++ {
		err := fn()
		if err == nil {
			return nil
		}

		// Check if it's a rate limit error.
		restErr, ok := err.(*discordgo.RESTError)
		if !ok || restErr.Response == nil || restErr.Response.StatusCode != 429 {
			return err // not a rate limit error
		}

		if attempt == maxRetries {
			return err
		}

		wait := time.Duration(math.Pow(2, float64(attempt))) * a.baseBackoff
		if wait > a.maxBackoff {
			wait = a.maxBackoff
		}

		log.Printf("discord: rate limited (attempt %d/%d) — retrying in %v",
			attempt+1, maxRetries, wait)

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(wait):
		}
	}
	return nil // unreachable
}

// CreateThread creates a Discord thread from a message.
func (a *Adapter) CreateThread(ctx context.Context, channelID, messageID, name string) (string, error) {
	a.mu.Lock()
	if !a.connected {
		a.mu.Unlock()
		return "", fmt.Errorf("discord: not connected")
	}
	a.mu.Unlock()

	var thread *discordgo.Channel
	err := a.retryOnRateLimit(ctx, func() error {
		var apiErr error
		thread, apiErr = a.sess.MessageThreadStartComplex(channelID, messageID, &discordgo.ThreadStart{
			Name:                name,
			AutoArchiveDuration: 1440, // 24 hours
			Type:                discordgo.ChannelTypeGuildPublicThread,
		})
		return apiErr
	})
	if err != nil {
		return "", fmt.Errorf("discord: create thread: %w", err)
	}
	return thread.ID, nil
}
