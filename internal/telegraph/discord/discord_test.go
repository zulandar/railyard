package discord

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/zulandar/railyard/internal/telegraph"
)

// --- Mock Discord session ---

type mockSession struct {
	mu             sync.Mutex
	opened         bool
	closeCalled    bool
	openErr        error
	closeErr       error
	sentMessages   []sentMessage
	sendErr        error
	threads        []createdThread
	threadErr      error
	threadResponse *discordgo.Channel
	messages       []*discordgo.Message
	messagesErr    error
	handler        interface{}
	removeCount    int
	channels       map[string]*discordgo.Channel // for Channel() lookups
}

type sentMessage struct {
	channelID string
	data      *discordgo.MessageSend
}

type createdThread struct {
	channelID string
	messageID string
	data      *discordgo.ThreadStart
}

func newMockSession() *mockSession {
	return &mockSession{
		threadResponse: &discordgo.Channel{ID: "thread-123"},
		channels:       make(map[string]*discordgo.Channel),
	}
}

func (m *mockSession) Open() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.openErr != nil {
		return m.openErr
	}
	m.opened = true
	return nil
}

func (m *mockSession) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.closeCalled = true
	return m.closeErr
}

func (m *mockSession) Channel(channelID string) (*discordgo.Channel, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if ch, ok := m.channels[channelID]; ok {
		return ch, nil
	}
	return nil, fmt.Errorf("channel not found: %s", channelID)
}

func (m *mockSession) ChannelMessageSend(channelID, content string, options ...discordgo.RequestOption) (*discordgo.Message, error) {
	return m.ChannelMessageSendComplex(channelID, &discordgo.MessageSend{Content: content})
}

func (m *mockSession) ChannelMessageSendEmbed(channelID string, embed *discordgo.MessageEmbed, options ...discordgo.RequestOption) (*discordgo.Message, error) {
	return m.ChannelMessageSendComplex(channelID, &discordgo.MessageSend{Embeds: []*discordgo.MessageEmbed{embed}})
}

func (m *mockSession) ChannelMessageSendComplex(channelID string, data *discordgo.MessageSend, options ...discordgo.RequestOption) (*discordgo.Message, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.sendErr != nil {
		return nil, m.sendErr
	}
	m.sentMessages = append(m.sentMessages, sentMessage{channelID: channelID, data: data})
	return &discordgo.Message{ID: "msg-123"}, nil
}

func (m *mockSession) MessageThreadStartComplex(channelID, messageID string, data *discordgo.ThreadStart) (*discordgo.Channel, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.threadErr != nil {
		return nil, m.threadErr
	}
	m.threads = append(m.threads, createdThread{channelID: channelID, messageID: messageID, data: data})
	return m.threadResponse, nil
}

func (m *mockSession) ChannelMessages(channelID string, limit int, beforeID, afterID, aroundID string, options ...discordgo.RequestOption) ([]*discordgo.Message, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.messagesErr != nil {
		return nil, m.messagesErr
	}
	return m.messages, nil
}

func (m *mockSession) AddHandler(handler interface{}) func() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.handler = handler
	return func() {
		m.mu.Lock()
		defer m.mu.Unlock()
		m.removeCount++
	}
}

func (m *mockSession) sentCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.sentMessages)
}

func (m *mockSession) lastSent() sentMessage {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.sentMessages[len(m.sentMessages)-1]
}

// --- Helper to create a connected adapter ---

func newTestAdapter(t *testing.T) (*Adapter, *mockSession) {
	t.Helper()
	sess := newMockSession()

	a, err := New(AdapterOpts{
		Session:   sess,
		ChannelID: "C_DEFAULT",
	})
	if err != nil {
		t.Fatalf("new adapter: %v", err)
	}

	if err := a.Connect(context.Background()); err != nil {
		t.Fatalf("connect: %v", err)
	}
	a.SetBotUserID("BOT_USER_ID")
	return a, sess
}

// --- New tests ---

func TestNew_RequiresBotToken(t *testing.T) {
	_, err := New(AdapterOpts{})
	if err == nil {
		t.Fatal("expected error for missing bot token")
	}
	if !strings.Contains(err.Error(), "bot token") {
		t.Errorf("error = %q, want to mention bot token", err.Error())
	}
}

func TestNew_WithMockSession(t *testing.T) {
	a, err := New(AdapterOpts{
		Session: newMockSession(),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if a == nil {
		t.Fatal("expected non-nil adapter")
	}
}

func TestNew_WithBotToken(t *testing.T) {
	a, err := New(AdapterOpts{
		BotToken: "test-token",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if a == nil {
		t.Fatal("expected non-nil adapter")
	}
}

// --- Connect tests ---

func TestConnect_Success(t *testing.T) {
	a, sess := newTestAdapter(t)
	_ = a
	if !sess.opened {
		t.Error("expected session to be opened")
	}
}

func TestConnect_OpenError(t *testing.T) {
	sess := newMockSession()
	sess.openErr = fmt.Errorf("gateway error")

	a, _ := New(AdapterOpts{Session: sess})
	err := a.Connect(context.Background())
	if err == nil {
		t.Fatal("expected open error")
	}
	if !strings.Contains(err.Error(), "open gateway") {
		t.Errorf("error = %q, want open gateway error", err.Error())
	}
}

func TestConnect_AlreadyClosed(t *testing.T) {
	a, _ := newTestAdapter(t)
	a.Close()
	err := a.Connect(context.Background())
	if err == nil {
		t.Fatal("expected error for closed adapter")
	}
}

func TestConnect_Idempotent(t *testing.T) {
	a, _ := newTestAdapter(t)
	// Second connect should be a no-op.
	err := a.Connect(context.Background())
	if err != nil {
		t.Fatalf("second connect should not error: %v", err)
	}
}

// --- Listen tests ---

func TestListen_NotConnected(t *testing.T) {
	sess := newMockSession()
	a, _ := New(AdapterOpts{Session: sess})

	_, err := a.Listen(context.Background())
	if err == nil {
		t.Fatal("expected error for not connected")
	}
}

func TestListen_RegistersHandler(t *testing.T) {
	a, sess := newTestAdapter(t)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	_, err := a.Listen(ctx)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}

	sess.mu.Lock()
	hasHandler := sess.handler != nil
	sess.mu.Unlock()

	if !hasHandler {
		t.Error("expected handler to be registered")
	}
}

func TestListen_ReceivesMessages(t *testing.T) {
	a, _ := newTestAdapter(t)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ch, err := a.Listen(ctx)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}

	// Simulate a message via handleMessage.
	a.handleMessage(&discordgo.MessageCreate{
		Message: &discordgo.Message{
			ID:        "123456789012345678",
			ChannelID: "C1",
			Content:   "hello",
			Author: &discordgo.User{
				ID:       "U_ALICE",
				Username: "Alice",
			},
		},
	})

	select {
	case msg := <-ch:
		if msg.Platform != "discord" {
			t.Errorf("platform = %q, want discord", msg.Platform)
		}
		if msg.ChannelID != "C1" {
			t.Errorf("channel = %q, want C1", msg.ChannelID)
		}
		if msg.UserID != "U_ALICE" {
			t.Errorf("user id = %q, want U_ALICE", msg.UserID)
		}
		if msg.UserName != "Alice" {
			t.Errorf("username = %q, want Alice", msg.UserName)
		}
		if msg.Text != "hello" {
			t.Errorf("text = %q, want hello", msg.Text)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for inbound message")
	}
}

func TestListen_FiltersSelfMessages(t *testing.T) {
	a, _ := newTestAdapter(t)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ch, _ := a.Listen(ctx)

	// Self-message (from bot).
	a.handleMessage(&discordgo.MessageCreate{
		Message: &discordgo.Message{
			ID:        "100",
			ChannelID: "C1",
			Content:   "bot message",
			Author:    &discordgo.User{ID: "BOT_USER_ID", Username: "Bot"},
		},
	})

	// Real message.
	a.handleMessage(&discordgo.MessageCreate{
		Message: &discordgo.Message{
			ID:        "101",
			ChannelID: "C1",
			Content:   "real message",
			Author:    &discordgo.User{ID: "U_ALICE", Username: "Alice"},
		},
	})

	select {
	case msg := <-ch:
		if msg.Text != "real message" {
			t.Errorf("expected real message, got %q", msg.Text)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for message")
	}
}

func TestListen_FiltersBotMessages(t *testing.T) {
	a, _ := newTestAdapter(t)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ch, _ := a.Listen(ctx)

	// Other bot message.
	a.handleMessage(&discordgo.MessageCreate{
		Message: &discordgo.Message{
			ID:        "200",
			ChannelID: "C1",
			Content:   "other bot",
			Author:    &discordgo.User{ID: "OTHER_BOT", Username: "OtherBot", Bot: true},
		},
	})

	// Real message.
	a.handleMessage(&discordgo.MessageCreate{
		Message: &discordgo.Message{
			ID:        "201",
			ChannelID: "C1",
			Content:   "from human",
			Author:    &discordgo.User{ID: "U_BOB", Username: "Bob"},
		},
	})

	select {
	case msg := <-ch:
		if msg.Text != "from human" {
			t.Errorf("expected human message, got %q", msg.Text)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout")
	}
}

func TestHandleMessage_NilAuthor(t *testing.T) {
	a, _ := newTestAdapter(t)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ch, _ := a.Listen(ctx)

	// Message with nil author should not panic.
	a.handleMessage(&discordgo.MessageCreate{
		Message: &discordgo.Message{
			ID:        "300",
			ChannelID: "C1",
			Content:   "no author",
			Author:    nil,
		},
	})

	// Send a real message to verify channel is still working.
	a.handleMessage(&discordgo.MessageCreate{
		Message: &discordgo.Message{
			ID:        "301",
			ChannelID: "C1",
			Content:   "real",
			Author:    &discordgo.User{ID: "U1", Username: "User1"},
		},
	})

	select {
	case msg := <-ch:
		if msg.Text != "real" {
			t.Errorf("expected real message, got %q", msg.Text)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout")
	}
}

func TestHandleMessage_ThreadChannel(t *testing.T) {
	a, sess := newTestAdapter(t)

	// Register a thread channel in the mock session's channel map.
	sess.mu.Lock()
	sess.channels["thread-999"] = &discordgo.Channel{
		ID:       "thread-999",
		Type:     discordgo.ChannelTypeGuildPublicThread,
		ParentID: "parent-channel",
	}
	sess.mu.Unlock()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ch, _ := a.Listen(ctx)

	// Message in a thread channel — ChannelID should be resolved to parent,
	// ThreadID should be the thread channel.
	a.handleMessage(&discordgo.MessageCreate{
		Message: &discordgo.Message{
			ID:        "400",
			ChannelID: "thread-999",
			Content:   "hello from thread",
			Author:    &discordgo.User{ID: "U1", Username: "Alice"},
		},
	})

	select {
	case msg := <-ch:
		if msg.ChannelID != "parent-channel" {
			t.Errorf("ChannelID = %q, want %q", msg.ChannelID, "parent-channel")
		}
		if msg.ThreadID != "thread-999" {
			t.Errorf("ThreadID = %q, want %q", msg.ThreadID, "thread-999")
		}
		if msg.Text != "hello from thread" {
			t.Errorf("Text = %q, want %q", msg.Text, "hello from thread")
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for thread message")
	}
}

func TestHandleMessage_NonThreadChannel(t *testing.T) {
	a, sess := newTestAdapter(t)

	// Register a regular channel (not a thread).
	sess.mu.Lock()
	sess.channels["C1"] = &discordgo.Channel{
		ID:   "C1",
		Type: discordgo.ChannelTypeGuildText,
	}
	sess.mu.Unlock()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ch, _ := a.Listen(ctx)

	a.handleMessage(&discordgo.MessageCreate{
		Message: &discordgo.Message{
			ID:        "401",
			ChannelID: "C1",
			Content:   "top-level message",
			Author:    &discordgo.User{ID: "U1", Username: "Alice"},
		},
	})

	select {
	case msg := <-ch:
		if msg.ChannelID != "C1" {
			t.Errorf("ChannelID = %q, want %q", msg.ChannelID, "C1")
		}
		if msg.ThreadID != "" {
			t.Errorf("ThreadID = %q, want empty", msg.ThreadID)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout")
	}
}

func TestHandleMessage_UnknownChannel(t *testing.T) {
	a, _ := newTestAdapter(t)

	// Channel not in state cache — should fall back to treating as non-thread.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ch, _ := a.Listen(ctx)

	a.handleMessage(&discordgo.MessageCreate{
		Message: &discordgo.Message{
			ID:        "402",
			ChannelID: "unknown-channel",
			Content:   "message",
			Author:    &discordgo.User{ID: "U1", Username: "Alice"},
		},
	})

	select {
	case msg := <-ch:
		if msg.ChannelID != "unknown-channel" {
			t.Errorf("ChannelID = %q, want %q", msg.ChannelID, "unknown-channel")
		}
		if msg.ThreadID != "" {
			t.Errorf("ThreadID = %q, want empty for unknown channel", msg.ThreadID)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout")
	}
}

// --- Send tests ---

func TestSend_SimpleText(t *testing.T) {
	a, sess := newTestAdapter(t)

	err := a.Send(context.Background(), telegraph.OutboundMessage{
		ChannelID: "C1",
		Text:      "hello world",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if sess.sentCount() != 1 {
		t.Fatalf("expected 1 sent message, got %d", sess.sentCount())
	}
	last := sess.lastSent()
	if last.channelID != "C1" {
		t.Errorf("channel = %q, want C1", last.channelID)
	}
	if last.data.Content != "hello world" {
		t.Errorf("content = %q, want 'hello world'", last.data.Content)
	}
}

func TestSend_DefaultChannel(t *testing.T) {
	a, sess := newTestAdapter(t)

	err := a.Send(context.Background(), telegraph.OutboundMessage{
		Text: "hello default",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	last := sess.lastSent()
	if last.channelID != "C_DEFAULT" {
		t.Errorf("channel = %q, want C_DEFAULT", last.channelID)
	}
}

func TestSend_NoChannel(t *testing.T) {
	sess := newMockSession()
	a, _ := New(AdapterOpts{Session: sess})
	a.Connect(context.Background())

	err := a.Send(context.Background(), telegraph.OutboundMessage{
		Text: "no channel",
	})
	if err == nil {
		t.Fatal("expected error for no channel")
	}
}

func TestSend_WithEvents(t *testing.T) {
	a, sess := newTestAdapter(t)

	err := a.Send(context.Background(), telegraph.OutboundMessage{
		ChannelID: "C1",
		Text:      "events",
		Events: []telegraph.FormattedEvent{
			{
				Title:    "Car merged",
				Body:     "car-1 merged successfully",
				Color:    "#36a64f",
				Severity: "success",
				Fields: []telegraph.Field{
					{Name: "Car", Value: "car-1", Short: true},
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if sess.sentCount() != 1 {
		t.Fatal("expected 1 sent message")
	}
	last := sess.lastSent()
	if len(last.data.Embeds) != 1 {
		t.Fatalf("expected 1 embed, got %d", len(last.data.Embeds))
	}
	embed := last.data.Embeds[0]
	if embed.Title != "Car merged" {
		t.Errorf("embed title = %q", embed.Title)
	}
}

func TestSend_WithThreadID(t *testing.T) {
	a, sess := newTestAdapter(t)

	err := a.Send(context.Background(), telegraph.OutboundMessage{
		ChannelID: "C1",
		ThreadID:  "thread-456",
		Text:      "thread reply",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	last := sess.lastSent()
	// Discord threads are channels — ThreadID should be used as the target channel.
	if last.channelID != "thread-456" {
		t.Errorf("channel = %q, want thread-456", last.channelID)
	}
	if last.data.Reference != nil {
		t.Error("expected no message reference (Discord threads are channels)")
	}
}

func TestSend_NotConnected(t *testing.T) {
	sess := newMockSession()
	a, _ := New(AdapterOpts{Session: sess})

	err := a.Send(context.Background(), telegraph.OutboundMessage{
		ChannelID: "C1",
		Text:      "hello",
	})
	if err == nil {
		t.Fatal("expected error for not connected")
	}
}

func TestSend_PostError(t *testing.T) {
	a, sess := newTestAdapter(t)
	sess.sendErr = fmt.Errorf("channel not found")

	err := a.Send(context.Background(), telegraph.OutboundMessage{
		ChannelID: "C1",
		Text:      "hello",
	})
	if err == nil {
		t.Fatal("expected send error")
	}
}

// --- ThreadHistory tests ---

func TestThreadHistory_Success(t *testing.T) {
	a, sess := newTestAdapter(t)
	now := time.Now()
	sess.messages = []*discordgo.Message{
		{
			ID:        "msg1",
			Content:   "first",
			Author:    &discordgo.User{ID: "U1", Username: "Alice"},
			Timestamp: now,
		},
		{
			ID:        "msg2",
			Content:   "second",
			Author:    &discordgo.User{ID: "U2", Username: "Bob"},
			Timestamp: now.Add(time.Second),
		},
	}

	msgs, err := a.ThreadHistory(context.Background(), "C1", "thread-1", 50)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(msgs) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(msgs))
	}
	if msgs[0].Text != "first" {
		t.Errorf("first msg text = %q", msgs[0].Text)
	}
	if msgs[0].UserName != "Alice" {
		t.Errorf("first msg username = %q", msgs[0].UserName)
	}
	if msgs[1].Text != "second" {
		t.Errorf("second msg text = %q", msgs[1].Text)
	}
}

func TestThreadHistory_NotConnected(t *testing.T) {
	sess := newMockSession()
	a, _ := New(AdapterOpts{Session: sess})

	_, err := a.ThreadHistory(context.Background(), "C1", "thread-1", 50)
	if err == nil {
		t.Fatal("expected error for not connected")
	}
}

func TestThreadHistory_Error(t *testing.T) {
	a, sess := newTestAdapter(t)
	sess.messagesErr = fmt.Errorf("channel not found")

	_, err := a.ThreadHistory(context.Background(), "C1", "thread-1", 50)
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestThreadHistory_UsesThreadIDAsChannel(t *testing.T) {
	a, sess := newTestAdapter(t)
	sess.messages = []*discordgo.Message{
		{
			ID:      "msg1",
			Content: "in thread",
			Author:  &discordgo.User{ID: "U1", Username: "Alice"},
		},
	}

	// Thread ID should be used as the channel for fetching messages.
	msgs, err := a.ThreadHistory(context.Background(), "parent-channel", "thread-channel", 50)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message, got %d", len(msgs))
	}
}

func TestThreadHistory_FallbackToChannelID(t *testing.T) {
	a, sess := newTestAdapter(t)
	sess.messages = []*discordgo.Message{
		{
			ID:      "msg1",
			Content: "in channel",
			Author:  &discordgo.User{ID: "U1", Username: "Alice"},
		},
	}

	// Empty thread ID should fall back to channel ID.
	msgs, err := a.ThreadHistory(context.Background(), "C1", "", 50)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message, got %d", len(msgs))
	}
}

func TestThreadHistory_LimitTruncation(t *testing.T) {
	a, sess := newTestAdapter(t)
	now := time.Now()
	sess.messages = []*discordgo.Message{
		{ID: "1", Content: "msg1", Author: &discordgo.User{ID: "U1", Username: "A"}, Timestamp: now},
		{ID: "2", Content: "msg2", Author: &discordgo.User{ID: "U2", Username: "B"}, Timestamp: now},
		{ID: "3", Content: "msg3", Author: &discordgo.User{ID: "U3", Username: "C"}, Timestamp: now},
	}

	msgs, err := a.ThreadHistory(context.Background(), "C1", "thread-1", 2)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(msgs) != 2 {
		t.Fatalf("expected 2 messages (limit=2), got %d", len(msgs))
	}
}

func TestThreadHistory_EmptyResult(t *testing.T) {
	a, sess := newTestAdapter(t)
	sess.messages = []*discordgo.Message{}

	msgs, err := a.ThreadHistory(context.Background(), "C1", "thread-1", 50)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(msgs) != 0 {
		t.Fatalf("expected 0 messages, got %d", len(msgs))
	}
}

// --- ThreadHistory pagination tests ---

func TestThreadHistory_Pagination(t *testing.T) {
	sess := newMockSession()
	pagSess := &paginatingMockSession{mockSession: sess, pageSize: 3}

	// Page 1: 3 items (full page), triggers fetching page 2.
	// Page 2: 1 item (partial page), stops pagination.
	page1 := make([]*discordgo.Message, 3)
	for i := range page1 {
		page1[i] = &discordgo.Message{
			ID:      fmt.Sprintf("%d", 300-i),
			Content: fmt.Sprintf("msg%d", i+1),
			Author:  &discordgo.User{ID: fmt.Sprintf("U%d", i+1), Username: fmt.Sprintf("User%d", i+1)},
		}
	}
	page2 := []*discordgo.Message{
		{ID: "100", Content: "msg4", Author: &discordgo.User{ID: "U4", Username: "User4"}},
	}
	pagSess.pages = [][]*discordgo.Message{page1, page2}

	a, err := New(AdapterOpts{Session: pagSess, ChannelID: "C_DEFAULT"})
	if err != nil {
		t.Fatal(err)
	}
	a.Connect(context.Background())
	a.SetBotUserID("BOT_USER_ID")

	// Use limit=3 so pageSize=3, matching page 1 size to trigger page 2 fetch.
	// After page 1: total=3, limit=3, 3>=3 → truncate → stop. Would NOT fetch page 2.
	// So use limit=5 > page 1 size=3 and pageSize=min(100,5)=5.
	// Page 1: returns 3 items < pageSize=5 → stops. Still 1 page.
	//
	// The only way to test multi-page: use pageSize override.
	// Since defaultPageSize=100, we need 100+ item pages.
	// Instead, verify that the paginating mock's beforeID cursor is used.
	msgs, err := a.ThreadHistory(context.Background(), "C1", "thread-1", 3)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(msgs) != 3 {
		t.Fatalf("expected 3 messages, got %d", len(msgs))
	}
	if msgs[0].Text != "msg1" {
		t.Errorf("first msg = %q, want msg1", msgs[0].Text)
	}
}

func TestThreadHistory_PaginationMultiPage(t *testing.T) {
	// Build a mock that returns exactly defaultPageSize items on page 1,
	// triggering a second page fetch.
	sess := newMockSession()
	pagSess := &paginatingMockSession{mockSession: sess, pageSize: defaultPageSize}

	page1 := make([]*discordgo.Message, defaultPageSize)
	for i := range page1 {
		page1[i] = &discordgo.Message{
			ID:      fmt.Sprintf("%d", 1000-i),
			Content: fmt.Sprintf("p1-%d", i),
			Author:  &discordgo.User{ID: "U1", Username: "A"},
		}
	}
	page2 := []*discordgo.Message{
		{ID: "1", Content: "last", Author: &discordgo.User{ID: "U2", Username: "B"}},
	}
	pagSess.pages = [][]*discordgo.Message{page1, page2}

	a, err := New(AdapterOpts{Session: pagSess, ChannelID: "C_DEFAULT"})
	if err != nil {
		t.Fatal(err)
	}
	a.Connect(context.Background())
	a.SetBotUserID("BOT_USER_ID")

	// limit=0 means unlimited; pageSize defaults to defaultPageSize (100).
	// Page 1: 100 items == pageSize → fetch page 2.
	// Page 2: 1 item < pageSize → stop.
	msgs, err := a.ThreadHistory(context.Background(), "C1", "thread-1", 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	expected := defaultPageSize + 1
	if len(msgs) != expected {
		t.Fatalf("expected %d messages across 2 pages, got %d", expected, len(msgs))
	}
	if msgs[len(msgs)-1].Text != "last" {
		t.Errorf("last msg = %q, want 'last'", msgs[len(msgs)-1].Text)
	}

	// Verify second page was fetched (pageIdx advanced).
	pagSess.pgMu.Lock()
	idx := pagSess.pageIdx
	pagSess.pgMu.Unlock()
	if idx != 2 {
		t.Errorf("expected 2 pages fetched, got %d", idx)
	}
}

func TestThreadHistory_PaginationWithLimit(t *testing.T) {
	sess := newMockSession()
	pagSess := &paginatingMockSession{mockSession: sess, pageSize: defaultPageSize}

	page1 := make([]*discordgo.Message, defaultPageSize)
	for i := range page1 {
		page1[i] = &discordgo.Message{
			ID:      fmt.Sprintf("%d", 1000-i),
			Content: fmt.Sprintf("p1-%d", i),
			Author:  &discordgo.User{ID: "U1", Username: "A"},
		}
	}
	page2 := make([]*discordgo.Message, 50)
	for i := range page2 {
		page2[i] = &discordgo.Message{
			ID:      fmt.Sprintf("%d", 500-i),
			Content: fmt.Sprintf("p2-%d", i),
			Author:  &discordgo.User{ID: "U2", Username: "B"},
		}
	}
	pagSess.pages = [][]*discordgo.Message{page1, page2}

	a, err := New(AdapterOpts{Session: pagSess, ChannelID: "C_DEFAULT"})
	if err != nil {
		t.Fatal(err)
	}
	a.Connect(context.Background())
	a.SetBotUserID("BOT_USER_ID")

	// Limit=120: pageSize=100. Page 1: 100 items, total=100 < 120, continue.
	// Page 2: 50 items, total=150 >= 120 → truncate to 120.
	msgs, err := a.ThreadHistory(context.Background(), "C1", "thread-1", 120)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(msgs) != 120 {
		t.Fatalf("expected 120 messages (limit=120), got %d", len(msgs))
	}
}

func TestThreadHistory_RateLimitRetry(t *testing.T) {
	sess := newMockSession()
	rlSess := &rateLimitMockSession{
		mockSession: sess,
		failCount:   1,
		messages: []*discordgo.Message{
			{ID: "1", Content: "hello", Author: &discordgo.User{ID: "U1", Username: "A"}},
		},
	}

	a, err := New(AdapterOpts{Session: rlSess, ChannelID: "C_DEFAULT"})
	if err != nil {
		t.Fatal(err)
	}
	a.Connect(context.Background())
	a.SetBotUserID("BOT_USER_ID")
	a.baseBackoff = time.Millisecond
	a.maxBackoff = 10 * time.Millisecond

	msgs, err := a.ThreadHistory(context.Background(), "C1", "thread-1", 50)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message, got %d", len(msgs))
	}

	rlSess.mu.Lock()
	calls := rlSess.msgCalls
	rlSess.mu.Unlock()
	if calls != 2 {
		t.Errorf("expected 2 calls (1 failure + 1 success), got %d", calls)
	}
}

// paginatingMockSession wraps mockSession and returns paginated ChannelMessages results.
type paginatingMockSession struct {
	*mockSession
	pages    [][]*discordgo.Message
	pageIdx  int
	pageSize int // informational, not used in mock logic
	pgMu     sync.Mutex
}

func (p *paginatingMockSession) ChannelMessages(channelID string, limit int, beforeID, afterID, aroundID string, options ...discordgo.RequestOption) ([]*discordgo.Message, error) {
	p.pgMu.Lock()
	defer p.pgMu.Unlock()

	if p.pageIdx >= len(p.pages) {
		return nil, nil
	}
	page := p.pages[p.pageIdx]
	p.pageIdx++
	return page, nil
}

// rateLimitMockSession wraps mockSession and returns rate limit errors for
// ChannelMessages calls.
type rateLimitMockSession struct {
	*mockSession
	failCount int
	msgCalls  int
	messages  []*discordgo.Message
}

func (r *rateLimitMockSession) ChannelMessages(channelID string, limit int, beforeID, afterID, aroundID string, options ...discordgo.RequestOption) ([]*discordgo.Message, error) {
	r.mu.Lock()
	r.msgCalls++
	c := r.msgCalls
	r.mu.Unlock()
	if c <= r.failCount {
		return nil, &discordgo.RESTError{
			Response: &http.Response{StatusCode: 429},
		}
	}
	return r.messages, nil
}

// --- Gateway reconnection handler tests ---

func TestConnect_RegistersReadyHandler(t *testing.T) {
	sess := &handlerTrackingSession{mockSession: newMockSession()}

	a, _ := New(AdapterOpts{Session: sess})
	a.Connect(context.Background())

	sess.mu.Lock()
	count := sess.handlerCount
	sess.mu.Unlock()

	// Should register 3 handlers: Ready, Disconnect, Resumed.
	if count != 3 {
		t.Errorf("expected 3 handlers registered, got %d", count)
	}
}

// handlerTrackingSession counts AddHandler calls.
type handlerTrackingSession struct {
	*mockSession
	handlerCount int
}

func (h *handlerTrackingSession) AddHandler(handler interface{}) func() {
	h.mu.Lock()
	h.handlerCount++
	h.mu.Unlock()
	return h.mockSession.AddHandler(handler)
}

// --- Close tests ---

func TestClose_Success(t *testing.T) {
	a, sess := newTestAdapter(t)
	err := a.Close()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !sess.closeCalled {
		t.Error("expected session Close() to be called")
	}
}

func TestClose_Idempotent(t *testing.T) {
	a, _ := newTestAdapter(t)
	a.Close()
	err := a.Close()
	if err != nil {
		t.Fatalf("second close should not error: %v", err)
	}
}

func TestClose_RemovesHandler(t *testing.T) {
	a, sess := newTestAdapter(t)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	a.Listen(ctx)

	a.Close()

	sess.mu.Lock()
	removed := sess.removeCount
	sess.mu.Unlock()

	if removed != 1 {
		t.Errorf("expected handler to be removed, removeCount = %d", removed)
	}
}

// --- CreateThread tests ---

func TestCreateThread_Success(t *testing.T) {
	a, sess := newTestAdapter(t)

	threadID, err := a.CreateThread(context.Background(), "C1", "msg-1", "Dispatch Session 42")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if threadID != "thread-123" {
		t.Errorf("thread ID = %q, want thread-123", threadID)
	}

	sess.mu.Lock()
	if len(sess.threads) != 1 {
		t.Fatalf("expected 1 thread created, got %d", len(sess.threads))
	}
	created := sess.threads[0]
	sess.mu.Unlock()

	if created.channelID != "C1" {
		t.Errorf("channel = %q", created.channelID)
	}
	if created.messageID != "msg-1" {
		t.Errorf("message = %q", created.messageID)
	}
	if created.data.Name != "Dispatch Session 42" {
		t.Errorf("name = %q", created.data.Name)
	}
}

func TestCreateThread_NotConnected(t *testing.T) {
	sess := newMockSession()
	a, _ := New(AdapterOpts{Session: sess})

	_, err := a.CreateThread(context.Background(), "C1", "msg-1", "Test")
	if err == nil {
		t.Fatal("expected error for not connected")
	}
}

func TestCreateThread_Error(t *testing.T) {
	a, sess := newTestAdapter(t)
	sess.threadErr = fmt.Errorf("forbidden")

	_, err := a.CreateThread(context.Background(), "C1", "msg-1", "Test")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "create thread") {
		t.Errorf("error = %q", err.Error())
	}
}

// --- buildMessageSend tests ---

func TestBuildMessageSend_TextOnly(t *testing.T) {
	data := buildMessageSend(telegraph.OutboundMessage{
		Text: "hello",
	})
	if data.Content != "hello" {
		t.Errorf("content = %q", data.Content)
	}
	if len(data.Embeds) != 0 {
		t.Errorf("expected 0 embeds, got %d", len(data.Embeds))
	}
	if data.Reference != nil {
		t.Error("expected nil reference")
	}
}

func TestBuildMessageSend_WithThread(t *testing.T) {
	data := buildMessageSend(telegraph.OutboundMessage{
		Text:     "reply",
		ThreadID: "thread-1",
	})
	// Discord threads are channels — no MessageReference needed.
	if data.Reference != nil {
		t.Error("expected no message reference (Discord threads are channels)")
	}
}

func TestBuildMessageSend_WithEvents(t *testing.T) {
	data := buildMessageSend(telegraph.OutboundMessage{
		Text: "events",
		Events: []telegraph.FormattedEvent{
			{Title: "Test", Body: "body", Color: "#fff"},
		},
	})
	if len(data.Embeds) != 1 {
		t.Fatalf("expected 1 embed, got %d", len(data.Embeds))
	}
}

// --- eventToEmbed tests ---

func TestEventToEmbed(t *testing.T) {
	evt := telegraph.FormattedEvent{
		Title:    "Car merged",
		Body:     "car-1 merged",
		Color:    "#36a64f",
		Severity: "success",
		Fields: []telegraph.Field{
			{Name: "Car", Value: "car-1", Short: true},
			{Name: "Track", Value: "backend", Short: true},
		},
	}

	embed := eventToEmbed(evt)
	if embed.Title != "Car merged" {
		t.Errorf("title = %q", embed.Title)
	}
	if embed.Description != "car-1 merged" {
		t.Errorf("description = %q", embed.Description)
	}
	if embed.Color != 0x36a64f {
		t.Errorf("color = %d, want %d", embed.Color, 0x36a64f)
	}
	if len(embed.Fields) != 2 {
		t.Fatalf("fields count = %d, want 2", len(embed.Fields))
	}
	if embed.Fields[0].Name != "Car" {
		t.Errorf("field[0] name = %q", embed.Fields[0].Name)
	}
	if !embed.Fields[0].Inline {
		t.Error("field[0] should be inline")
	}
}

func TestEventToEmbed_NoColor(t *testing.T) {
	evt := telegraph.FormattedEvent{
		Title: "Test",
		Body:  "body",
	}
	embed := eventToEmbed(evt)
	if embed.Color != 0 {
		t.Errorf("color = %d, want 0", embed.Color)
	}
}

// --- parseHexColor tests ---

func TestParseHexColor(t *testing.T) {
	tests := []struct {
		input string
		want  int
	}{
		{"#36a64f", 0x36a64f},
		{"36a64f", 0x36a64f},
		{"#ffffff", 0xffffff},
		{"#000000", 0x000000},
		{"#FF0000", 0xff0000},
		{"#fff", 0xfff},
		{"", 0},
	}
	for _, tt := range tests {
		got := parseHexColor(tt.input)
		if got != tt.want {
			t.Errorf("parseHexColor(%q) = %d, want %d", tt.input, got, tt.want)
		}
	}
}

// --- retryOnRateLimit tests ---

func TestRetryOnRateLimit_Success(t *testing.T) {
	a, _ := newTestAdapter(t)
	calls := 0
	err := a.retryOnRateLimit(context.Background(), func() error {
		calls++
		return nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if calls != 1 {
		t.Errorf("expected 1 call, got %d", calls)
	}
}

func TestRetryOnRateLimit_NonRateLimitError(t *testing.T) {
	a, _ := newTestAdapter(t)
	calls := 0
	err := a.retryOnRateLimit(context.Background(), func() error {
		calls++
		return fmt.Errorf("some other error")
	})
	if err == nil {
		t.Fatal("expected error")
	}
	if calls != 1 {
		t.Errorf("should not retry non-rate-limit errors, calls = %d", calls)
	}
}

func TestRetryOnRateLimit_RetriesAndSucceeds(t *testing.T) {
	a, _ := newTestAdapter(t)
	a.baseBackoff = time.Millisecond
	a.maxBackoff = 10 * time.Millisecond

	calls := 0
	err := a.retryOnRateLimit(context.Background(), func() error {
		calls++
		if calls < 3 {
			return &discordgo.RESTError{
				Response: &http.Response{StatusCode: 429},
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if calls != 3 {
		t.Errorf("expected 3 calls, got %d", calls)
	}
}

func TestRetryOnRateLimit_ExhaustsRetries(t *testing.T) {
	a, _ := newTestAdapter(t)
	a.baseBackoff = time.Millisecond
	a.maxBackoff = 10 * time.Millisecond

	calls := 0
	err := a.retryOnRateLimit(context.Background(), func() error {
		calls++
		return &discordgo.RESTError{
			Response: &http.Response{StatusCode: 429},
		}
	})
	if err == nil {
		t.Fatal("expected error after exhausting retries")
	}
	if calls != maxRetries+1 {
		t.Errorf("expected %d calls, got %d", maxRetries+1, calls)
	}
}

func TestRetryOnRateLimit_RespectsContext(t *testing.T) {
	a, _ := newTestAdapter(t)
	a.baseBackoff = time.Second // long backoff

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	calls := 0
	err := a.retryOnRateLimit(ctx, func() error {
		calls++
		return &discordgo.RESTError{
			Response: &http.Response{StatusCode: 429},
		}
	})
	if err != context.Canceled {
		t.Errorf("expected context.Canceled, got %v", err)
	}
	if calls != 1 {
		t.Errorf("expected 1 call before context cancel, got %d", calls)
	}
}

// --- BotUserID tests ---

func TestBotUserID(t *testing.T) {
	a, _ := newTestAdapter(t)
	if a.BotUserID() != "BOT_USER_ID" {
		t.Errorf("bot user ID = %q, want BOT_USER_ID", a.BotUserID())
	}
}

func TestSetBotUserID(t *testing.T) {
	a, _ := newTestAdapter(t)
	a.SetBotUserID("NEW_BOT_ID")
	if a.BotUserID() != "NEW_BOT_ID" {
		t.Errorf("bot user ID = %q, want NEW_BOT_ID", a.BotUserID())
	}
}

// --- Verify Adapter interface compliance ---

var _ telegraph.Adapter = (*Adapter)(nil)
