package slack

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	slackapi "github.com/slack-go/slack"
	"github.com/slack-go/slack/slackevents"
	"github.com/slack-go/slack/socketmode"
	"github.com/zulandar/railyard/internal/telegraph"
)

// --- Mock Slack client ---

type mockSlackClient struct {
	mu       sync.Mutex
	authResp *slackapi.AuthTestResponse
	authErr  error
	posted   []postedMessage
	postErr  error
	replies  []slackapi.Message
	hasMore  bool
	cursor   string
	replyErr error
	users    map[string]*slackapi.User
}

type postedMessage struct {
	channelID string
	options   []slackapi.MsgOption
}

func newMockSlackClient() *mockSlackClient {
	return &mockSlackClient{
		authResp: &slackapi.AuthTestResponse{UserID: "U_BOT_123"},
		users:    make(map[string]*slackapi.User),
	}
}

func (m *mockSlackClient) AuthTest() (*slackapi.AuthTestResponse, error) {
	return m.authResp, m.authErr
}

func (m *mockSlackClient) PostMessage(channelID string, options ...slackapi.MsgOption) (string, string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.postErr != nil {
		return "", "", m.postErr
	}
	m.posted = append(m.posted, postedMessage{channelID: channelID, options: options})
	return channelID, "1234567890.123456", nil
}

func (m *mockSlackClient) GetConversationReplies(params *slackapi.GetConversationRepliesParameters) ([]slackapi.Message, bool, string, error) {
	if m.replyErr != nil {
		return nil, false, "", m.replyErr
	}
	return m.replies, m.hasMore, m.cursor, nil
}

func (m *mockSlackClient) GetUserInfo(userID string) (*slackapi.User, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if u, ok := m.users[userID]; ok {
		return u, nil
	}
	return nil, fmt.Errorf("user not found: %s", userID)
}

func (m *mockSlackClient) postedCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.posted)
}

func (m *mockSlackClient) lastPosted() postedMessage {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.posted[len(m.posted)-1]
}

// --- Mock Socket Mode client ---

type mockSocketClient struct {
	events  chan socketmode.Event
	acked   []socketmode.Request
	mu      sync.Mutex
	running bool
	done    chan struct{}
}

func newMockSocketClient() *mockSocketClient {
	return &mockSocketClient{
		events: make(chan socketmode.Event, 100),
		done:   make(chan struct{}),
	}
}

func (m *mockSocketClient) Run() error {
	m.mu.Lock()
	m.running = true
	m.mu.Unlock()
	// Block until done is closed (don't consume from events).
	<-m.done
	return nil
}

func (m *mockSocketClient) EventsChan() chan socketmode.Event {
	return m.events
}

func (m *mockSocketClient) Ack(req socketmode.Request, payload ...interface{}) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.acked = append(m.acked, req)
}

func (m *mockSocketClient) ackedCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.acked)
}

// --- Helper to create a connected adapter ---

func newTestAdapter(t *testing.T) (*Adapter, *mockSlackClient, *mockSocketClient) {
	t.Helper()
	client := newMockSlackClient()
	socket := newMockSocketClient()

	a, err := New(AdapterOpts{
		Client:    client,
		Socket:    socket,
		ChannelID: "C_DEFAULT",
	})
	if err != nil {
		t.Fatalf("new adapter: %v", err)
	}

	if err := a.Connect(context.Background()); err != nil {
		t.Fatalf("connect: %v", err)
	}
	return a, client, socket
}

// --- New tests ---

func TestNew_RequiresBotToken(t *testing.T) {
	_, err := New(AdapterOpts{AppToken: "xapp-test"})
	if err == nil {
		t.Fatal("expected error for missing bot token")
	}
}

func TestNew_RequiresAppToken(t *testing.T) {
	_, err := New(AdapterOpts{BotToken: "xoxb-test"})
	if err == nil {
		t.Fatal("expected error for missing app token")
	}
}

func TestNew_WithMocks(t *testing.T) {
	a, err := New(AdapterOpts{
		Client: newMockSlackClient(),
		Socket: newMockSocketClient(),
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
	a, _, _ := newTestAdapter(t)
	if a.BotUserID() != "U_BOT_123" {
		t.Errorf("bot user ID = %q, want U_BOT_123", a.BotUserID())
	}
}

func TestConnect_AuthError(t *testing.T) {
	client := newMockSlackClient()
	client.authErr = fmt.Errorf("invalid token")
	socket := newMockSocketClient()

	a, _ := New(AdapterOpts{Client: client, Socket: socket})
	err := a.Connect(context.Background())
	if err == nil {
		t.Fatal("expected auth error")
	}
	if !strings.Contains(err.Error(), "auth test") {
		t.Errorf("error = %q, want auth test error", err.Error())
	}
}

func TestConnect_AlreadyClosed(t *testing.T) {
	a, _, _ := newTestAdapter(t)
	a.Close()
	err := a.Connect(context.Background())
	if err == nil {
		t.Fatal("expected error for closed adapter")
	}
}

func TestConnect_Idempotent(t *testing.T) {
	a, _, _ := newTestAdapter(t)
	// Second connect should be a no-op.
	err := a.Connect(context.Background())
	if err != nil {
		t.Fatalf("second connect should not error: %v", err)
	}
}

// --- Listen tests ---

func TestListen_NotConnected(t *testing.T) {
	client := newMockSlackClient()
	socket := newMockSocketClient()
	a, _ := New(AdapterOpts{Client: client, Socket: socket})

	_, err := a.Listen(context.Background())
	if err == nil {
		t.Fatal("expected error for not connected")
	}
}

func TestListen_ReceivesMessages(t *testing.T) {
	a, _, socket := newTestAdapter(t)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ch, err := a.Listen(ctx)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}

	// Send a message event through the socket.
	socket.events <- socketmode.Event{
		Type: socketmode.EventTypeEventsAPI,
		Data: slackevents.EventsAPIEvent{
			Type: slackevents.CallbackEvent,
			InnerEvent: slackevents.EventsAPIInnerEvent{
				Data: &slackevents.MessageEvent{
					User:      "U_ALICE",
					Channel:   "C1",
					Text:      "hello",
					TimeStamp: "1700000000.000001",
				},
			},
		},
		Request: &socketmode.Request{EnvelopeID: "env-1"},
	}

	select {
	case msg := <-ch:
		if msg.Platform != "slack" {
			t.Errorf("platform = %q, want slack", msg.Platform)
		}
		if msg.ChannelID != "C1" {
			t.Errorf("channel = %q, want C1", msg.ChannelID)
		}
		if msg.UserID != "U_ALICE" {
			t.Errorf("user id = %q, want U_ALICE", msg.UserID)
		}
		if msg.Text != "hello" {
			t.Errorf("text = %q, want hello", msg.Text)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for inbound message")
	}
}

func TestListen_FiltersSelfMessages(t *testing.T) {
	a, _, socket := newTestAdapter(t)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ch, _ := a.Listen(ctx)

	// Send a message from the bot itself.
	socket.events <- socketmode.Event{
		Type: socketmode.EventTypeEventsAPI,
		Data: slackevents.EventsAPIEvent{
			Type: slackevents.CallbackEvent,
			InnerEvent: slackevents.EventsAPIInnerEvent{
				Data: &slackevents.MessageEvent{
					User:      "U_BOT_123",
					Channel:   "C1",
					Text:      "bot message",
					TimeStamp: "1700000000.000001",
				},
			},
		},
		Request: &socketmode.Request{EnvelopeID: "env-2"},
	}

	// Send a real user message after.
	socket.events <- socketmode.Event{
		Type: socketmode.EventTypeEventsAPI,
		Data: slackevents.EventsAPIEvent{
			Type: slackevents.CallbackEvent,
			InnerEvent: slackevents.EventsAPIInnerEvent{
				Data: &slackevents.MessageEvent{
					User:      "U_ALICE",
					Channel:   "C1",
					Text:      "real message",
					TimeStamp: "1700000001.000001",
				},
			},
		},
		Request: &socketmode.Request{EnvelopeID: "env-3"},
	}

	select {
	case msg := <-ch:
		// First message received should be the real one.
		if msg.Text != "real message" {
			t.Errorf("expected real message, got %q", msg.Text)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for message")
	}
}

func TestListen_FiltersBotMessages(t *testing.T) {
	a, _, socket := newTestAdapter(t)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ch, _ := a.Listen(ctx)

	// Send a bot message (has BotID set).
	socket.events <- socketmode.Event{
		Type: socketmode.EventTypeEventsAPI,
		Data: slackevents.EventsAPIEvent{
			Type: slackevents.CallbackEvent,
			InnerEvent: slackevents.EventsAPIInnerEvent{
				Data: &slackevents.MessageEvent{
					User:      "U_OTHER_BOT",
					BotID:     "B123",
					Channel:   "C1",
					Text:      "other bot message",
					TimeStamp: "1700000000.000001",
				},
			},
		},
		Request: &socketmode.Request{EnvelopeID: "env-4"},
	}

	// Send a real message after.
	socket.events <- socketmode.Event{
		Type: socketmode.EventTypeEventsAPI,
		Data: slackevents.EventsAPIEvent{
			Type: slackevents.CallbackEvent,
			InnerEvent: slackevents.EventsAPIInnerEvent{
				Data: &slackevents.MessageEvent{
					User:      "U_BOB",
					Channel:   "C1",
					Text:      "from bob",
					TimeStamp: "1700000001.000001",
				},
			},
		},
		Request: &socketmode.Request{EnvelopeID: "env-5"},
	}

	select {
	case msg := <-ch:
		if msg.Text != "from bob" {
			t.Errorf("expected real message, got %q", msg.Text)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout")
	}
}

func TestListen_HandlesAppMention(t *testing.T) {
	a, _, socket := newTestAdapter(t)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ch, _ := a.Listen(ctx)

	socket.events <- socketmode.Event{
		Type: socketmode.EventTypeEventsAPI,
		Data: slackevents.EventsAPIEvent{
			Type: slackevents.CallbackEvent,
			InnerEvent: slackevents.EventsAPIInnerEvent{
				Data: &slackevents.AppMentionEvent{
					User:            "U_ALICE",
					Channel:         "C1",
					Text:            "<@U_BOT_123> status",
					TimeStamp:       "1700000000.000001",
					ThreadTimeStamp: "1699999999.000001",
				},
			},
		},
		Request: &socketmode.Request{EnvelopeID: "env-6"},
	}

	select {
	case msg := <-ch:
		if msg.Platform != "slack" {
			t.Errorf("platform = %q", msg.Platform)
		}
		if msg.UserID != "U_ALICE" {
			t.Errorf("user = %q", msg.UserID)
		}
		if !strings.Contains(msg.Text, "status") {
			t.Errorf("text = %q", msg.Text)
		}
		if msg.ThreadID != "1699999999.000001" {
			t.Errorf("thread = %q", msg.ThreadID)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout")
	}
}

func TestListen_AcksEventsAPIEvents(t *testing.T) {
	a, _, socket := newTestAdapter(t)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	a.Listen(ctx)

	socket.events <- socketmode.Event{
		Type: socketmode.EventTypeEventsAPI,
		Data: slackevents.EventsAPIEvent{
			Type: slackevents.CallbackEvent,
			InnerEvent: slackevents.EventsAPIInnerEvent{
				Data: &slackevents.MessageEvent{
					User:      "U_ALICE",
					Channel:   "C1",
					Text:      "hello",
					TimeStamp: "1700000000.000001",
				},
			},
		},
		Request: &socketmode.Request{EnvelopeID: "env-7"},
	}

	time.Sleep(100 * time.Millisecond)
	if socket.ackedCount() != 1 {
		t.Errorf("expected 1 ack, got %d", socket.ackedCount())
	}
}

// --- Send tests ---

func TestSend_SimpleText(t *testing.T) {
	a, client, _ := newTestAdapter(t)

	err := a.Send(context.Background(), telegraph.OutboundMessage{
		ChannelID: "C1",
		Text:      "hello world",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if client.postedCount() != 1 {
		t.Fatalf("expected 1 posted message, got %d", client.postedCount())
	}
	last := client.lastPosted()
	if last.channelID != "C1" {
		t.Errorf("channel = %q, want C1", last.channelID)
	}
}

func TestSend_DefaultChannel(t *testing.T) {
	a, client, _ := newTestAdapter(t)

	err := a.Send(context.Background(), telegraph.OutboundMessage{
		Text: "hello default",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	last := client.lastPosted()
	if last.channelID != "C_DEFAULT" {
		t.Errorf("channel = %q, want C_DEFAULT", last.channelID)
	}
}

func TestSend_NoChannel(t *testing.T) {
	client := newMockSlackClient()
	socket := newMockSocketClient()
	a, _ := New(AdapterOpts{Client: client, Socket: socket})
	a.Connect(context.Background())

	err := a.Send(context.Background(), telegraph.OutboundMessage{
		Text: "no channel",
	})
	if err == nil {
		t.Fatal("expected error for no channel")
	}
}

func TestSend_WithEvents(t *testing.T) {
	a, client, _ := newTestAdapter(t)

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
	if client.postedCount() != 1 {
		t.Fatal("expected 1 posted message")
	}
}

func TestSend_NotConnected(t *testing.T) {
	client := newMockSlackClient()
	socket := newMockSocketClient()
	a, _ := New(AdapterOpts{Client: client, Socket: socket})

	err := a.Send(context.Background(), telegraph.OutboundMessage{
		ChannelID: "C1",
		Text:      "hello",
	})
	if err == nil {
		t.Fatal("expected error for not connected")
	}
}

func TestSend_PostError(t *testing.T) {
	a, client, _ := newTestAdapter(t)
	client.postErr = fmt.Errorf("rate limited")

	err := a.Send(context.Background(), telegraph.OutboundMessage{
		ChannelID: "C1",
		Text:      "hello",
	})
	if err == nil {
		t.Fatal("expected post error")
	}
}

// --- ThreadHistory tests ---

func TestThreadHistory_Success(t *testing.T) {
	a, client, _ := newTestAdapter(t)
	client.replies = []slackapi.Message{
		{Msg: slackapi.Msg{User: "U_ALICE", Text: "first", Timestamp: "1700000000.000001"}},
		{Msg: slackapi.Msg{User: "U_BOB", Text: "second", Timestamp: "1700000001.000001"}},
	}

	msgs, err := a.ThreadHistory(context.Background(), "C1", "1700000000.000001", 50)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(msgs) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(msgs))
	}
	if msgs[0].Text != "first" {
		t.Errorf("first msg text = %q", msgs[0].Text)
	}
	if msgs[1].Text != "second" {
		t.Errorf("second msg text = %q", msgs[1].Text)
	}
}

func TestThreadHistory_NotConnected(t *testing.T) {
	client := newMockSlackClient()
	socket := newMockSocketClient()
	a, _ := New(AdapterOpts{Client: client, Socket: socket})

	_, err := a.ThreadHistory(context.Background(), "C1", "123", 50)
	if err == nil {
		t.Fatal("expected error for not connected")
	}
}

func TestThreadHistory_Error(t *testing.T) {
	a, client, _ := newTestAdapter(t)
	client.replyErr = fmt.Errorf("channel not found")

	_, err := a.ThreadHistory(context.Background(), "C1", "123", 50)
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestThreadHistory_ResolvesUserNames(t *testing.T) {
	a, client, _ := newTestAdapter(t)
	client.users["U_ALICE"] = &slackapi.User{
		Profile: slackapi.UserProfile{DisplayName: "Alice"},
	}
	client.replies = []slackapi.Message{
		{Msg: slackapi.Msg{User: "U_ALICE", Text: "hello", Timestamp: "1700000000.000001"}},
	}

	msgs, _ := a.ThreadHistory(context.Background(), "C1", "1700000000.000001", 50)
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message, got %d", len(msgs))
	}
	if msgs[0].UserName != "Alice" {
		t.Errorf("username = %q, want Alice", msgs[0].UserName)
	}
}

// --- Close tests ---

func TestClose_Success(t *testing.T) {
	a, _, _ := newTestAdapter(t)
	err := a.Close()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestClose_Idempotent(t *testing.T) {
	a, _, _ := newTestAdapter(t)
	a.Close()
	err := a.Close()
	if err != nil {
		t.Fatalf("second close should not error: %v", err)
	}
}

// --- buildMessageOptions tests ---

func TestBuildMessageOptions_TextOnly(t *testing.T) {
	opts := buildMessageOptions(telegraph.OutboundMessage{
		Text: "hello",
	})
	if len(opts) != 1 {
		t.Errorf("expected 1 option, got %d", len(opts))
	}
}

func TestBuildMessageOptions_WithThread(t *testing.T) {
	opts := buildMessageOptions(telegraph.OutboundMessage{
		Text:     "reply",
		ThreadID: "1234.5678",
	})
	if len(opts) != 2 {
		t.Errorf("expected 2 options (text + thread), got %d", len(opts))
	}
}

func TestBuildMessageOptions_WithEvents(t *testing.T) {
	opts := buildMessageOptions(telegraph.OutboundMessage{
		Text: "events",
		Events: []telegraph.FormattedEvent{
			{Title: "Test", Body: "body", Color: "#fff"},
		},
	})
	// Should have: text + attachments.
	if len(opts) != 2 {
		t.Errorf("expected 2 options, got %d", len(opts))
	}
}

// --- eventToAttachment tests ---

func TestEventToAttachment(t *testing.T) {
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

	att := eventToAttachment(evt)
	if att.Title != "Car merged" {
		t.Errorf("title = %q", att.Title)
	}
	if att.Text != "car-1 merged" {
		t.Errorf("text = %q", att.Text)
	}
	if att.Color != "#36a64f" {
		t.Errorf("color = %q", att.Color)
	}
	if len(att.Fields) != 2 {
		t.Errorf("fields count = %d, want 2", len(att.Fields))
	}
	if att.Fields[0].Title != "Car" {
		t.Errorf("field[0] title = %q", att.Fields[0].Title)
	}
	if att.Fields[0].Short != true {
		t.Error("field[0] should be short")
	}
}

// --- parseSlackTimestamp tests ---

func TestParseSlackTimestamp(t *testing.T) {
	tests := []struct {
		ts   string
		want int64
	}{
		{"1700000000.000001", 1700000000},
		{"1234567890.123456", 1234567890},
		{"", 0},
		{"invalid", 0},
	}
	for _, tt := range tests {
		got := parseSlackTimestamp(tt.ts)
		if tt.want == 0 && !got.IsZero() {
			t.Errorf("parseSlackTimestamp(%q) = %v, want zero", tt.ts, got)
		} else if tt.want != 0 && got.Unix() != tt.want {
			t.Errorf("parseSlackTimestamp(%q) = %d, want %d", tt.ts, got.Unix(), tt.want)
		}
	}
}

// --- resolveUserName tests ---

func TestResolveUserName_DisplayName(t *testing.T) {
	a, client, _ := newTestAdapter(t)
	client.users["U1"] = &slackapi.User{
		Profile: slackapi.UserProfile{DisplayName: "Alice"},
	}
	name := a.resolveUserName("U1")
	if name != "Alice" {
		t.Errorf("name = %q, want Alice", name)
	}
}

func TestResolveUserName_RealName(t *testing.T) {
	a, client, _ := newTestAdapter(t)
	client.users["U1"] = &slackapi.User{
		RealName: "Alice Smith",
		Profile:  slackapi.UserProfile{},
	}
	name := a.resolveUserName("U1")
	if name != "Alice Smith" {
		t.Errorf("name = %q, want Alice Smith", name)
	}
}

func TestResolveUserName_FallbackToID(t *testing.T) {
	a, _, _ := newTestAdapter(t)
	name := a.resolveUserName("U_UNKNOWN")
	if name != "U_UNKNOWN" {
		t.Errorf("name = %q, want U_UNKNOWN", name)
	}
}

func TestResolveUserName_EmptyID(t *testing.T) {
	a, _, _ := newTestAdapter(t)
	name := a.resolveUserName("")
	if name != "" {
		t.Errorf("name = %q, want empty", name)
	}
}

// --- handleMessage subtype filtering ---

func TestHandleMessage_FiltersSubtypes(t *testing.T) {
	a, _, socket := newTestAdapter(t)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ch, _ := a.Listen(ctx)

	// Send a message with subtype (e.g., message_changed).
	socket.events <- socketmode.Event{
		Type: socketmode.EventTypeEventsAPI,
		Data: slackevents.EventsAPIEvent{
			Type: slackevents.CallbackEvent,
			InnerEvent: slackevents.EventsAPIInnerEvent{
				Data: &slackevents.MessageEvent{
					User:      "U_ALICE",
					Channel:   "C1",
					Text:      "edited",
					SubType:   "message_changed",
					TimeStamp: "1700000000.000001",
				},
			},
		},
		Request: &socketmode.Request{EnvelopeID: "env-8"},
	}

	// Send a normal message after.
	socket.events <- socketmode.Event{
		Type: socketmode.EventTypeEventsAPI,
		Data: slackevents.EventsAPIEvent{
			Type: slackevents.CallbackEvent,
			InnerEvent: slackevents.EventsAPIInnerEvent{
				Data: &slackevents.MessageEvent{
					User:      "U_ALICE",
					Channel:   "C1",
					Text:      "normal",
					TimeStamp: "1700000001.000001",
				},
			},
		},
		Request: &socketmode.Request{EnvelopeID: "env-9"},
	}

	select {
	case msg := <-ch:
		if msg.Text != "normal" {
			t.Errorf("expected normal message, got %q (subtype message should be filtered)", msg.Text)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout")
	}
}

// --- handleAppMention self-mention filtering ---

func TestHandleAppMention_FiltersSelfMention(t *testing.T) {
	a, _, socket := newTestAdapter(t)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ch, _ := a.Listen(ctx)

	// Self-mention.
	socket.events <- socketmode.Event{
		Type: socketmode.EventTypeEventsAPI,
		Data: slackevents.EventsAPIEvent{
			Type: slackevents.CallbackEvent,
			InnerEvent: slackevents.EventsAPIInnerEvent{
				Data: &slackevents.AppMentionEvent{
					User:      "U_BOT_123",
					Channel:   "C1",
					Text:      "self mention",
					TimeStamp: "1700000000.000001",
				},
			},
		},
		Request: &socketmode.Request{EnvelopeID: "env-10"},
	}

	// Real mention.
	socket.events <- socketmode.Event{
		Type: socketmode.EventTypeEventsAPI,
		Data: slackevents.EventsAPIEvent{
			Type: slackevents.CallbackEvent,
			InnerEvent: slackevents.EventsAPIInnerEvent{
				Data: &slackevents.AppMentionEvent{
					User:      "U_ALICE",
					Channel:   "C1",
					Text:      "real mention",
					TimeStamp: "1700000001.000001",
				},
			},
		},
		Request: &socketmode.Request{EnvelopeID: "env-11"},
	}

	select {
	case msg := <-ch:
		if msg.Text != "real mention" {
			t.Errorf("expected real mention, got %q", msg.Text)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout")
	}
}

// --- retryOnRateLimit tests ---

func TestRetryOnRateLimit_Success(t *testing.T) {
	calls := 0
	err := retryOnRateLimit(context.Background(), func() error {
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
	calls := 0
	err := retryOnRateLimit(context.Background(), func() error {
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
	calls := 0
	err := retryOnRateLimit(context.Background(), func() error {
		calls++
		if calls < 3 {
			return &slackapi.RateLimitedError{RetryAfter: time.Millisecond}
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
	calls := 0
	err := retryOnRateLimit(context.Background(), func() error {
		calls++
		return &slackapi.RateLimitedError{RetryAfter: time.Millisecond}
	})
	if err == nil {
		t.Fatal("expected error after exhausting retries")
	}
	// maxRetries+1 total calls (initial + retries).
	if calls != maxRetries+1 {
		t.Errorf("expected %d calls, got %d", maxRetries+1, calls)
	}
}

func TestRetryOnRateLimit_RespectsContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	calls := 0
	err := retryOnRateLimit(ctx, func() error {
		calls++
		return &slackapi.RateLimitedError{RetryAfter: time.Second}
	})
	if err != context.Canceled {
		t.Errorf("expected context.Canceled, got %v", err)
	}
	if calls != 1 {
		t.Errorf("expected 1 call before context cancel, got %d", calls)
	}
}

func TestRetryOnRateLimit_UsesDefaultBackoff(t *testing.T) {
	// When RetryAfter is 0, should use exponential backoff (very short for test).
	calls := 0
	err := retryOnRateLimit(context.Background(), func() error {
		calls++
		if calls < 2 {
			return &slackapi.RateLimitedError{RetryAfter: 0}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if calls != 2 {
		t.Errorf("expected 2 calls, got %d", calls)
	}
}

// --- Send rate limiting tests ---

func TestSend_RetriesOnRateLimit(t *testing.T) {
	a, client, _ := newTestAdapter(t)

	calls := 0
	client.mu.Lock()
	origPostErr := client.postErr
	client.mu.Unlock()
	_ = origPostErr

	// We need to make PostMessage return rate limit then succeed.
	// Override the mock to track calls.
	rateLimitClient := &rateLimitMockClient{
		inner:       client,
		failCount:   2,
		rateLimited: true,
	}
	a.client = rateLimitClient
	_ = calls

	err := a.Send(context.Background(), telegraph.OutboundMessage{
		ChannelID: "C1",
		Text:      "hello",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if rateLimitClient.calls != 3 {
		t.Errorf("expected 3 calls (2 failures + 1 success), got %d", rateLimitClient.calls)
	}
}

// rateLimitMockClient wraps a mock client and returns rate limit errors for the first N calls.
type rateLimitMockClient struct {
	inner       *mockSlackClient
	mu          sync.Mutex
	calls       int
	failCount   int
	rateLimited bool
}

func (r *rateLimitMockClient) AuthTest() (*slackapi.AuthTestResponse, error) {
	return r.inner.AuthTest()
}

func (r *rateLimitMockClient) PostMessage(channelID string, options ...slackapi.MsgOption) (string, string, error) {
	r.mu.Lock()
	r.calls++
	c := r.calls
	r.mu.Unlock()
	if c <= r.failCount {
		return "", "", &slackapi.RateLimitedError{RetryAfter: time.Millisecond}
	}
	return r.inner.PostMessage(channelID, options...)
}

func (r *rateLimitMockClient) GetConversationReplies(params *slackapi.GetConversationRepliesParameters) ([]slackapi.Message, bool, string, error) {
	r.mu.Lock()
	r.calls++
	c := r.calls
	r.mu.Unlock()
	if c <= r.failCount {
		return nil, false, "", &slackapi.RateLimitedError{RetryAfter: time.Millisecond}
	}
	return r.inner.GetConversationReplies(params)
}

func (r *rateLimitMockClient) GetUserInfo(userID string) (*slackapi.User, error) {
	return r.inner.GetUserInfo(userID)
}

// --- ThreadHistory rate limiting tests ---

func TestThreadHistory_RetriesOnRateLimit(t *testing.T) {
	a, client, _ := newTestAdapter(t)

	client.replies = []slackapi.Message{
		{Msg: slackapi.Msg{User: "U_ALICE", Text: "hello", Timestamp: "1700000000.000001"}},
	}

	rlClient := &rateLimitMockClient{
		inner:     client,
		failCount: 1,
	}
	a.client = rlClient

	msgs, err := a.ThreadHistory(context.Background(), "C1", "1700000000.000001", 50)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message, got %d", len(msgs))
	}
	if rlClient.calls != 2 {
		t.Errorf("expected 2 calls (1 failure + 1 success), got %d", rlClient.calls)
	}
}

// --- ThreadHistory pagination tests ---

func TestThreadHistory_Pagination(t *testing.T) {
	a, _, _ := newTestAdapter(t)

	// Create a mock that returns paginated results.
	pagClient := &paginatingMockClient{
		pages: [][]slackapi.Message{
			{
				{Msg: slackapi.Msg{User: "U1", Text: "msg1", Timestamp: "1700000000.000001"}},
				{Msg: slackapi.Msg{User: "U2", Text: "msg2", Timestamp: "1700000001.000001"}},
			},
			{
				{Msg: slackapi.Msg{User: "U3", Text: "msg3", Timestamp: "1700000002.000001"}},
			},
		},
		users: make(map[string]*slackapi.User),
	}
	a.client = pagClient

	msgs, err := a.ThreadHistory(context.Background(), "C1", "1700000000.000001", 50)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(msgs) != 3 {
		t.Fatalf("expected 3 messages across 2 pages, got %d", len(msgs))
	}
	if msgs[0].Text != "msg1" || msgs[2].Text != "msg3" {
		t.Errorf("messages out of order: %v", msgs)
	}
}

func TestThreadHistory_LimitTruncation(t *testing.T) {
	a, _, _ := newTestAdapter(t)

	pagClient := &paginatingMockClient{
		pages: [][]slackapi.Message{
			{
				{Msg: slackapi.Msg{User: "U1", Text: "msg1", Timestamp: "1700000000.000001"}},
				{Msg: slackapi.Msg{User: "U2", Text: "msg2", Timestamp: "1700000001.000001"}},
			},
			{
				{Msg: slackapi.Msg{User: "U3", Text: "msg3", Timestamp: "1700000002.000001"}},
			},
		},
		users: make(map[string]*slackapi.User),
	}
	a.client = pagClient

	msgs, err := a.ThreadHistory(context.Background(), "C1", "1700000000.000001", 2)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(msgs) != 2 {
		t.Fatalf("expected 2 messages (limit=2), got %d", len(msgs))
	}
}

// paginatingMockClient simulates cursor-based pagination.
type paginatingMockClient struct {
	pages    [][]slackapi.Message
	pageIdx  int
	mu       sync.Mutex
	users    map[string]*slackapi.User
	authResp *slackapi.AuthTestResponse
}

func (p *paginatingMockClient) AuthTest() (*slackapi.AuthTestResponse, error) {
	if p.authResp != nil {
		return p.authResp, nil
	}
	return &slackapi.AuthTestResponse{UserID: "U_BOT"}, nil
}

func (p *paginatingMockClient) PostMessage(channelID string, options ...slackapi.MsgOption) (string, string, error) {
	return channelID, "ts", nil
}

func (p *paginatingMockClient) GetConversationReplies(params *slackapi.GetConversationRepliesParameters) ([]slackapi.Message, bool, string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.pageIdx >= len(p.pages) {
		return nil, false, "", nil
	}
	page := p.pages[p.pageIdx]
	p.pageIdx++
	hasMore := p.pageIdx < len(p.pages)
	cursor := ""
	if hasMore {
		cursor = fmt.Sprintf("cursor_%d", p.pageIdx)
	}
	return page, hasMore, cursor, nil
}

func (p *paginatingMockClient) GetUserInfo(userID string) (*slackapi.User, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if u, ok := p.users[userID]; ok {
		return u, nil
	}
	return nil, fmt.Errorf("user not found: %s", userID)
}

// --- runWithReconnect tests ---

func TestRunWithReconnect_CleanShutdown(t *testing.T) {
	socket := newMockSocketClient()

	a, err := New(AdapterOpts{
		Client: newMockSlackClient(),
		Socket: socket,
	})
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan struct{})
	go func() {
		a.runWithReconnect(ctx)
		close(done)
	}()

	// Let Run() complete cleanly.
	close(socket.done)

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for runWithReconnect to finish")
	}
	cancel()
}

func TestRunWithReconnect_RetriesOnError(t *testing.T) {
	// Create a socket that fails Run() a few times then succeeds.
	socket := &failingSocketClient{
		failCount: 2,
		events:    make(chan socketmode.Event, 10),
	}

	a, err := New(AdapterOpts{
		Client: newMockSlackClient(),
		Socket: socket,
	})
	if err != nil {
		t.Fatal(err)
	}
	// Use fast backoff for test.
	a.baseBackoff = time.Millisecond
	a.maxBackoff = 10 * time.Millisecond

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan struct{})
	go func() {
		a.runWithReconnect(ctx)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("timeout: runWithReconnect should finish after retries succeed")
	}

	socket.mu.Lock()
	calls := socket.runCalls
	socket.mu.Unlock()

	if calls != 3 {
		t.Errorf("expected 3 Run() calls (2 failures + 1 success), got %d", calls)
	}
}

func TestRunWithReconnect_StopsOnContextCancel(t *testing.T) {
	socket := &failingSocketClient{
		failCount: 100, // always fail
		events:    make(chan socketmode.Event, 10),
	}

	a, err := New(AdapterOpts{
		Client: newMockSlackClient(),
		Socket: socket,
	})
	if err != nil {
		t.Fatal(err)
	}
	// Use fast backoff for test.
	a.baseBackoff = time.Millisecond
	a.maxBackoff = 10 * time.Millisecond

	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan struct{})
	go func() {
		a.runWithReconnect(ctx)
		close(done)
	}()

	// Give it time to fail once, then cancel.
	time.Sleep(50 * time.Millisecond)
	cancel()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("timeout: runWithReconnect should stop on context cancel")
	}
}

// failingSocketClient fails Run() a specified number of times before succeeding.
type failingSocketClient struct {
	mu        sync.Mutex
	runCalls  int
	failCount int
	events    chan socketmode.Event
}

func (f *failingSocketClient) Run() error {
	f.mu.Lock()
	f.runCalls++
	n := f.runCalls
	f.mu.Unlock()

	if n <= f.failCount {
		return fmt.Errorf("connection failed (attempt %d)", n)
	}
	return nil
}

func (f *failingSocketClient) EventsChan() chan socketmode.Event {
	return f.events
}

func (f *failingSocketClient) Ack(req socketmode.Request, payload ...interface{}) {}

// --- Connection event handling tests ---

func TestHandleSocketEvent_ConnectionEvents(t *testing.T) {
	a, _, _ := newTestAdapter(t)

	// These should not panic and should be handled gracefully.
	a.handleSocketEvent(socketmode.Event{Type: socketmode.EventTypeConnecting})
	a.handleSocketEvent(socketmode.Event{Type: socketmode.EventTypeConnected})
	a.handleSocketEvent(socketmode.Event{Type: socketmode.EventTypeConnectionError, Data: "test error"})
	a.handleSocketEvent(socketmode.Event{Type: socketmode.EventTypeDisconnect})
}

// --- Verify Adapter interface compliance ---

var _ telegraph.Adapter = (*Adapter)(nil)
