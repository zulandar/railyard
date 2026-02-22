package telegraph

import (
	"context"
	"testing"
	"time"
)

// Compile-time interface compliance checks.
var _ Adapter = (*MockAdapter)(nil)
var _ BotUserIDer = (*MockAdapter)(nil)

func TestMockAdapter_InterfaceCompliance(t *testing.T) {
	var a Adapter = NewMockAdapter()
	if a == nil {
		t.Fatal("MockAdapter should implement Adapter")
	}
}

func TestMockAdapter_ConnectAndClose(t *testing.T) {
	m := NewMockAdapter()
	ctx := context.Background()

	if err := m.Connect(ctx); err != nil {
		t.Fatalf("Connect: %v", err)
	}

	if err := m.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// Connect after close should fail.
	if err := m.Connect(ctx); err == nil {
		t.Fatal("Connect after Close should fail")
	}

	// Double close should be safe.
	if err := m.Close(); err != nil {
		t.Fatalf("double Close should succeed: %v", err)
	}
}

func TestMockAdapter_ListenRequiresConnect(t *testing.T) {
	m := NewMockAdapter()
	ctx := context.Background()

	_, err := m.Listen(ctx)
	if err == nil {
		t.Fatal("Listen before Connect should fail")
	}
}

func TestMockAdapter_SendRequiresConnect(t *testing.T) {
	m := NewMockAdapter()
	ctx := context.Background()

	err := m.Send(ctx, OutboundMessage{Text: "hello"})
	if err == nil {
		t.Fatal("Send before Connect should fail")
	}
}

func TestMockAdapter_SimulateInbound(t *testing.T) {
	m := NewMockAdapter()
	ctx := context.Background()

	if err := m.Connect(ctx); err != nil {
		t.Fatalf("Connect: %v", err)
	}

	ch, err := m.Listen(ctx)
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}

	m.SimulateInbound(InboundMessage{
		Platform:  "test",
		ChannelID: "C123",
		UserID:    "U456",
		UserName:  "alice",
		Text:      "hello world",
	})

	select {
	case msg := <-ch:
		if msg.Text != "hello world" {
			t.Errorf("Text = %q, want %q", msg.Text, "hello world")
		}
		if msg.Platform != "test" {
			t.Errorf("Platform = %q, want %q", msg.Platform, "test")
		}
		if msg.UserName != "alice" {
			t.Errorf("UserName = %q, want %q", msg.UserName, "alice")
		}
		if msg.Timestamp.IsZero() {
			t.Error("Timestamp should be set automatically")
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for inbound message")
	}
}

func TestMockAdapter_SendAndLastSent(t *testing.T) {
	m := NewMockAdapter()
	ctx := context.Background()

	if err := m.Connect(ctx); err != nil {
		t.Fatalf("Connect: %v", err)
	}

	// No messages sent yet.
	_, ok := m.LastSent()
	if ok {
		t.Fatal("LastSent should return false when no messages sent")
	}
	if m.SentCount() != 0 {
		t.Errorf("SentCount = %d, want 0", m.SentCount())
	}

	// Send first message.
	msg1 := OutboundMessage{ChannelID: "C1", Text: "first"}
	if err := m.Send(ctx, msg1); err != nil {
		t.Fatalf("Send: %v", err)
	}

	if m.SentCount() != 1 {
		t.Errorf("SentCount = %d, want 1", m.SentCount())
	}
	last, ok := m.LastSent()
	if !ok {
		t.Fatal("LastSent should return true")
	}
	if last.Text != "first" {
		t.Errorf("LastSent.Text = %q, want %q", last.Text, "first")
	}

	// Send second message.
	msg2 := OutboundMessage{ChannelID: "C1", Text: "second"}
	if err := m.Send(ctx, msg2); err != nil {
		t.Fatalf("Send: %v", err)
	}

	if m.SentCount() != 2 {
		t.Errorf("SentCount = %d, want 2", m.SentCount())
	}
	last, _ = m.LastSent()
	if last.Text != "second" {
		t.Errorf("LastSent.Text = %q, want %q", last.Text, "second")
	}
}

func TestMockAdapter_AllSent(t *testing.T) {
	m := NewMockAdapter()
	ctx := context.Background()

	if err := m.Connect(ctx); err != nil {
		t.Fatalf("Connect: %v", err)
	}

	m.Send(ctx, OutboundMessage{Text: "a"})
	m.Send(ctx, OutboundMessage{Text: "b"})
	m.Send(ctx, OutboundMessage{Text: "c"})

	all := m.AllSent()
	if len(all) != 3 {
		t.Fatalf("AllSent len = %d, want 3", len(all))
	}
	if all[0].Text != "a" || all[1].Text != "b" || all[2].Text != "c" {
		t.Errorf("AllSent = %v", all)
	}

	// Verify returned slice is a copy (modifying it doesn't affect internal state).
	all[0].Text = "modified"
	orig := m.AllSent()
	if orig[0].Text != "a" {
		t.Error("AllSent should return a copy")
	}
}

func TestMockAdapter_ThreadHistory(t *testing.T) {
	m := NewMockAdapter()
	ctx := context.Background()

	now := time.Now()
	m.SetThreadHistory("C1", "T1", []ThreadMessage{
		{UserID: "U1", UserName: "alice", Text: "msg1", Timestamp: now.Add(-2 * time.Minute)},
		{UserID: "U2", UserName: "bob", Text: "msg2", Timestamp: now.Add(-time.Minute)},
		{UserID: "U1", UserName: "alice", Text: "msg3", Timestamp: now},
	})

	// Get all messages.
	msgs, err := m.ThreadHistory(ctx, "C1", "T1", 0)
	if err != nil {
		t.Fatalf("ThreadHistory: %v", err)
	}
	if len(msgs) != 3 {
		t.Fatalf("len = %d, want 3", len(msgs))
	}

	// Get with limit.
	msgs, err = m.ThreadHistory(ctx, "C1", "T1", 2)
	if err != nil {
		t.Fatalf("ThreadHistory with limit: %v", err)
	}
	if len(msgs) != 2 {
		t.Fatalf("len = %d, want 2", len(msgs))
	}
	if msgs[0].Text != "msg2" {
		t.Errorf("first msg = %q, want %q (should be most recent N)", msgs[0].Text, "msg2")
	}

	// Non-existent thread returns empty.
	msgs, err = m.ThreadHistory(ctx, "C999", "T999", 0)
	if err != nil {
		t.Fatalf("ThreadHistory empty: %v", err)
	}
	if len(msgs) != 0 {
		t.Errorf("expected empty, got %d", len(msgs))
	}
}

func TestMockAdapter_SendWithEvents(t *testing.T) {
	m := NewMockAdapter()
	ctx := context.Background()

	if err := m.Connect(ctx); err != nil {
		t.Fatalf("Connect: %v", err)
	}

	msg := OutboundMessage{
		ChannelID: "C1",
		Text:      "Build complete",
		Events: []FormattedEvent{
			{
				Title: "Car backend-42 merged",
				Body:  "All tests passed",
				Color: "#36a64f",
				Fields: []Field{
					{Name: "Track", Value: "backend", Short: true},
					{Name: "Branch", Value: "ry/feat-42", Short: true},
				},
			},
		},
	}

	if err := m.Send(ctx, msg); err != nil {
		t.Fatalf("Send: %v", err)
	}

	last, ok := m.LastSent()
	if !ok {
		t.Fatal("expected sent message")
	}
	if len(last.Events) != 1 {
		t.Fatalf("Events len = %d, want 1", len(last.Events))
	}
	if last.Events[0].Title != "Car backend-42 merged" {
		t.Errorf("Event.Title = %q", last.Events[0].Title)
	}
	if len(last.Events[0].Fields) != 2 {
		t.Errorf("Event.Fields len = %d, want 2", len(last.Events[0].Fields))
	}
}

func TestMockAdapter_CloseClosesInbound(t *testing.T) {
	m := NewMockAdapter()
	ctx := context.Background()

	if err := m.Connect(ctx); err != nil {
		t.Fatalf("Connect: %v", err)
	}

	ch, err := m.Listen(ctx)
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}

	if err := m.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// Channel should be closed.
	select {
	case _, ok := <-ch:
		if ok {
			t.Fatal("channel should be closed after Close()")
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for channel close")
	}
}
