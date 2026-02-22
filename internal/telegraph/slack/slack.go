// Package slack implements the telegraph Adapter for Slack using Socket Mode.
package slack

import (
	"context"
	"errors"
	"fmt"
	"log"
	"math"
	"strconv"
	"strings"
	"sync"
	"time"

	slackapi "github.com/slack-go/slack"
	"github.com/slack-go/slack/slackevents"
	"github.com/slack-go/slack/socketmode"
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
)

// slackClient abstracts the Slack API methods we use, enabling test mocks.
type slackClient interface {
	AuthTest() (*slackapi.AuthTestResponse, error)
	PostMessage(channelID string, options ...slackapi.MsgOption) (string, string, error)
	GetConversationReplies(params *slackapi.GetConversationRepliesParameters) ([]slackapi.Message, bool, string, error)
	GetUserInfo(userID string) (*slackapi.User, error)
}

// socketClient abstracts the Socket Mode client methods we use.
type socketClient interface {
	Run() error
	EventsChan() chan socketmode.Event
	Ack(req socketmode.Request, payload ...interface{})
}

// realSocketClient wraps *socketmode.Client to implement socketClient.
type realSocketClient struct {
	client *socketmode.Client
}

func (r *realSocketClient) Run() error                        { return r.client.Run() }
func (r *realSocketClient) EventsChan() chan socketmode.Event { return r.client.Events }
func (r *realSocketClient) Ack(req socketmode.Request, payload ...interface{}) {
	r.client.Ack(req, payload...)
}

// Adapter implements telegraph.Adapter for Slack Socket Mode.
type Adapter struct {
	client       slackClient
	socket       socketClient
	botUserID    string
	appToken     string
	botToken     string
	channelID    string // default channel for messages without explicit channel
	mu           sync.Mutex
	connected    bool
	closed       bool
	inbound      chan telegraph.InboundMessage
	cancelFunc   context.CancelFunc
	baseBackoff  time.Duration // reconnection base backoff (default: baseBackoff const)
	maxBackoff   time.Duration // reconnection max backoff (default: maxBackoff const)
	maxReconnect int           // max reconnection attempts (default: maxReconnectAttempts)
}

// AdapterOpts holds parameters for creating a Slack Adapter.
type AdapterOpts struct {
	AppToken  string // xapp-... Slack app-level token for Socket Mode
	BotToken  string // xoxb-... Slack bot token
	ChannelID string // default channel to post to
	// For testing: inject mock clients instead of real Slack API.
	Client slackClient
	Socket socketClient
}

// New creates a Slack Adapter.
func New(opts AdapterOpts) (*Adapter, error) {
	if opts.Client == nil && opts.BotToken == "" {
		return nil, fmt.Errorf("slack: bot token is required")
	}
	if opts.Socket == nil && opts.AppToken == "" {
		return nil, fmt.Errorf("slack: app token is required for socket mode")
	}

	a := &Adapter{
		appToken:     opts.AppToken,
		botToken:     opts.BotToken,
		channelID:    opts.ChannelID,
		inbound:      make(chan telegraph.InboundMessage, 100),
		baseBackoff:  baseBackoff,
		maxBackoff:   maxBackoff,
		maxReconnect: maxReconnectAttempts,
	}

	if opts.Client != nil {
		a.client = opts.Client
	}
	if opts.Socket != nil {
		a.socket = opts.Socket
	}

	return a, nil
}

// Connect establishes the Socket Mode WebSocket connection.
func (a *Adapter) Connect(ctx context.Context) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.closed {
		return fmt.Errorf("slack: adapter already closed")
	}
	if a.connected {
		return nil
	}

	// Create real clients if not injected (production path).
	if a.client == nil {
		api := slackapi.New(a.botToken, slackapi.OptionAppLevelToken(a.appToken))
		a.client = api
		a.socket = &realSocketClient{client: socketmode.New(api)}
	}

	// Get bot user ID for self-message filtering.
	auth, err := a.client.AuthTest()
	if err != nil {
		return fmt.Errorf("slack: auth test: %w", err)
	}
	a.botUserID = auth.UserID

	a.connected = true
	return nil
}

// Listen returns a channel of inbound messages. Starts the Socket Mode
// event pump in a background goroutine. Must be called after Connect.
func (a *Adapter) Listen(ctx context.Context) (<-chan telegraph.InboundMessage, error) {
	a.mu.Lock()
	if !a.connected {
		a.mu.Unlock()
		return nil, fmt.Errorf("slack: not connected")
	}
	a.mu.Unlock()

	listenCtx, cancel := context.WithCancel(ctx)
	a.mu.Lock()
	a.cancelFunc = cancel
	a.mu.Unlock()

	// Start socket mode in background with reconnection logic.
	go a.runWithReconnect(listenCtx)

	// Pump events from socket mode to inbound channel.
	go a.pumpEvents(listenCtx)

	return a.inbound, nil
}

// Send delivers a message to Slack. Translates OutboundMessage to Block Kit.
func (a *Adapter) Send(ctx context.Context, msg telegraph.OutboundMessage) error {
	a.mu.Lock()
	if !a.connected {
		a.mu.Unlock()
		return fmt.Errorf("slack: not connected")
	}
	a.mu.Unlock()

	channelID := msg.ChannelID
	if channelID == "" {
		channelID = a.channelID
	}
	if channelID == "" {
		return fmt.Errorf("slack: no channel specified")
	}

	options := buildMessageOptions(msg)

	err := retryOnRateLimit(ctx, func() error {
		_, _, postErr := a.client.PostMessage(channelID, options...)
		return postErr
	})
	if err != nil {
		return fmt.Errorf("slack: post message: %w", err)
	}
	return nil
}

// ThreadHistory retrieves messages from a Slack thread using conversations.replies.
// It paginates through all replies using cursor-based pagination and handles
// Slack rate limits with exponential backoff.
func (a *Adapter) ThreadHistory(ctx context.Context, channelID, threadID string, limit int) ([]telegraph.ThreadMessage, error) {
	a.mu.Lock()
	if !a.connected {
		a.mu.Unlock()
		return nil, fmt.Errorf("slack: not connected")
	}
	a.mu.Unlock()

	var allMsgs []telegraph.ThreadMessage
	cursor := ""

	// Use a per-page size that won't exceed the total limit.
	pageSize := 200
	if limit > 0 && limit < pageSize {
		pageSize = limit
	}

	for {
		params := &slackapi.GetConversationRepliesParameters{
			ChannelID: channelID,
			Timestamp: threadID,
			Limit:     pageSize,
			Cursor:    cursor,
		}

		var msgs []slackapi.Message
		var hasMore bool
		var nextCursor string

		err := retryOnRateLimit(ctx, func() error {
			var apiErr error
			msgs, hasMore, nextCursor, apiErr = a.client.GetConversationReplies(params)
			return apiErr
		})
		if err != nil {
			return nil, fmt.Errorf("slack: conversation replies: %w", err)
		}

		for _, m := range msgs {
			allMsgs = append(allMsgs, telegraph.ThreadMessage{
				UserID:    m.User,
				UserName:  a.resolveUserName(m.User),
				Text:      m.Text,
				Timestamp: parseSlackTimestamp(m.Timestamp),
			})
		}

		if !hasMore || nextCursor == "" {
			break
		}
		cursor = nextCursor

		if limit > 0 && len(allMsgs) >= limit {
			allMsgs = allMsgs[:limit]
			break
		}
	}

	return allMsgs, nil
}

// Close shuts down the adapter and closes the inbound channel.
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
	close(a.inbound)
	return nil
}

// BotUserID returns the bot's Slack user ID (available after Connect).
func (a *Adapter) BotUserID() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.botUserID
}

// runWithReconnect runs the Socket Mode client and retries with exponential
// backoff when Run() returns an error (e.g., reconnection failure).
func (a *Adapter) runWithReconnect(ctx context.Context) {
	for attempt := 0; attempt < a.maxReconnect; attempt++ {
		err := a.socket.Run()
		if err == nil {
			return // clean shutdown
		}

		// Check if we're shutting down.
		select {
		case <-ctx.Done():
			return
		default:
		}

		wait := time.Duration(math.Pow(2, float64(attempt))) * a.baseBackoff
		if wait > a.maxBackoff {
			wait = a.maxBackoff
		}

		log.Printf("slack: socket mode disconnected (attempt %d/%d): %v — reconnecting in %v",
			attempt+1, a.maxReconnect, err, wait)

		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
	}
	log.Printf("slack: socket mode exhausted %d reconnection attempts, giving up", a.maxReconnect)
}

// pumpEvents reads Socket Mode events and converts them to InboundMessages.
func (a *Adapter) pumpEvents(ctx context.Context) {
	events := a.socket.EventsChan()
	for {
		select {
		case <-ctx.Done():
			return
		case evt, ok := <-events:
			if !ok {
				return
			}
			a.handleSocketEvent(evt)
		}
	}
}

// handleSocketEvent processes a single Socket Mode event.
func (a *Adapter) handleSocketEvent(evt socketmode.Event) {
	switch evt.Type {
	case socketmode.EventTypeEventsAPI:
		eventsAPIEvent, ok := evt.Data.(slackevents.EventsAPIEvent)
		if !ok {
			return
		}
		// Acknowledge the event.
		if evt.Request != nil {
			a.socket.Ack(*evt.Request)
		}
		a.handleEventsAPI(eventsAPIEvent)

	case socketmode.EventTypeConnecting:
		log.Printf("slack: connecting to Socket Mode...")

	case socketmode.EventTypeConnected:
		log.Printf("slack: connected to Socket Mode")

	case socketmode.EventTypeConnectionError:
		log.Printf("slack: connection error: %v", evt.Data)

	case socketmode.EventTypeDisconnect:
		log.Printf("slack: server requested disconnect, will reconnect")
	}
}

// handleEventsAPI processes Events API callbacks.
func (a *Adapter) handleEventsAPI(event slackevents.EventsAPIEvent) {
	switch event.Type {
	case slackevents.CallbackEvent:
		innerEvent := event.InnerEvent
		switch ev := innerEvent.Data.(type) {
		case *slackevents.MessageEvent:
			a.handleMessage(ev)
		case *slackevents.AppMentionEvent:
			a.handleAppMention(ev)
		}
	}
}

// handleMessage converts a Slack message event to an InboundMessage.
func (a *Adapter) handleMessage(ev *slackevents.MessageEvent) {
	// Filter bot self-messages.
	if ev.User == a.botUserID {
		return
	}
	// Filter bot messages and message subtypes (edits, deletes, etc.).
	if ev.BotID != "" || ev.SubType != "" {
		return
	}

	a.inbound <- telegraph.InboundMessage{
		Platform:  "slack",
		ChannelID: ev.Channel,
		ThreadID:  ev.ThreadTimeStamp,
		UserID:    ev.User,
		UserName:  a.resolveUserName(ev.User),
		Text:      ev.Text,
		Timestamp: parseSlackTimestamp(ev.TimeStamp),
	}
}

// handleAppMention converts a Slack @mention event to an InboundMessage.
func (a *Adapter) handleAppMention(ev *slackevents.AppMentionEvent) {
	// Filter self-mentions (shouldn't happen but be safe).
	if ev.User == a.botUserID {
		return
	}

	a.inbound <- telegraph.InboundMessage{
		Platform:  "slack",
		ChannelID: ev.Channel,
		ThreadID:  ev.ThreadTimeStamp,
		UserID:    ev.User,
		UserName:  a.resolveUserName(ev.User),
		Text:      ev.Text,
		Timestamp: parseSlackTimestamp(ev.TimeStamp),
	}
}

// resolveUserName looks up a user's display name. Falls back to user ID.
func (a *Adapter) resolveUserName(userID string) string {
	if userID == "" {
		return ""
	}
	user, err := a.client.GetUserInfo(userID)
	if err != nil {
		return userID
	}
	if user.Profile.DisplayName != "" {
		return user.Profile.DisplayName
	}
	return user.RealName
}

// buildMessageOptions translates an OutboundMessage into Slack MsgOptions.
func buildMessageOptions(msg telegraph.OutboundMessage) []slackapi.MsgOption {
	var options []slackapi.MsgOption

	// Thread reply.
	if msg.ThreadID != "" {
		options = append(options, slackapi.MsgOptionTS(msg.ThreadID))
	}

	// If there are formatted events, build Block Kit attachments.
	if len(msg.Events) > 0 {
		var attachments []slackapi.Attachment
		for _, evt := range msg.Events {
			attachments = append(attachments, eventToAttachment(evt))
		}
		options = append(options, slackapi.MsgOptionAttachments(attachments...))
		// Use text as fallback.
		if msg.Text != "" {
			options = append(options, slackapi.MsgOptionText(msg.Text, false))
		}
	} else {
		options = append(options, slackapi.MsgOptionText(msg.Text, false))
	}

	return options
}

// eventToAttachment converts a FormattedEvent to a Slack Attachment.
func eventToAttachment(evt telegraph.FormattedEvent) slackapi.Attachment {
	att := slackapi.Attachment{
		Title:    evt.Title,
		Text:     evt.Body,
		Color:    evt.Color,
		Fallback: evt.Title,
	}

	for _, f := range evt.Fields {
		att.Fields = append(att.Fields, slackapi.AttachmentField{
			Title: f.Name,
			Value: f.Value,
			Short: f.Short,
		})
	}

	return att
}

// retryOnRateLimit calls fn and retries with backoff on Slack rate limit errors.
// It respects context cancellation and the RetryAfter duration from Slack.
func retryOnRateLimit(ctx context.Context, fn func() error) error {
	for attempt := 0; attempt <= maxRetries; attempt++ {
		err := fn()
		if err == nil {
			return nil
		}

		var rle *slackapi.RateLimitedError
		if !errors.As(err, &rle) {
			return err // not a rate limit error, don't retry
		}

		if attempt == maxRetries {
			return err
		}

		wait := rle.RetryAfter
		if wait <= 0 {
			wait = time.Duration(math.Pow(2, float64(attempt))) * time.Second
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(wait):
		}
	}
	return nil // unreachable
}

// parseSlackTimestamp converts a Slack timestamp (e.g., "1234567890.123456")
// to a time.Time.
func parseSlackTimestamp(ts string) time.Time {
	parts := strings.SplitN(ts, ".", 2)
	if len(parts) == 0 {
		return time.Time{}
	}
	sec, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		return time.Time{}
	}
	return time.Unix(sec, 0)
}
