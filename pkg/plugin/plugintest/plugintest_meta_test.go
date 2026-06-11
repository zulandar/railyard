package plugintest_test

import (
	"context"
	"errors"
	"testing"

	"github.com/zulandar/railyard/pkg/plugin"
	"github.com/zulandar/railyard/pkg/plugin/plugintest"
)

// TestFakeHostSubscribeWithMeta exercises the meta-aware subscription
// surface (railyard-77h.10): SubscribeWithMeta records a handler that
// DriveEventWithMeta fires with the supplied EventMeta, and a plain
// DriveEvent delivers a zero EventMeta to the same handler.
func TestFakeHostSubscribeWithMeta(t *testing.T) {
	fh := &plugintest.FakeHost{}

	var (
		calls    int
		lastMeta plugin.EventMeta
		lastID   string
	)
	unsub := fh.SubscribeWithMeta(plugin.CarCreated, func(topic plugin.EventType, payload any, meta plugin.EventMeta) {
		calls++
		lastMeta = meta
		if ev, ok := payload.(plugin.CarCreatedEvent); ok {
			lastID = ev.CarID
		}
	})

	n := fh.DriveEventWithMeta(plugin.CarCreated, plugin.CarCreatedEvent{CarID: "c-1"}, plugin.EventMeta{Seq: 7, Dropped: 3})
	if n != 1 {
		t.Fatalf("DriveEventWithMeta invoked %d handlers, want 1", n)
	}
	if calls != 1 {
		t.Fatalf("handler called %d times, want 1", calls)
	}
	if lastID != "c-1" {
		t.Errorf("payload CarID = %q, want c-1", lastID)
	}
	if lastMeta.Seq != 7 || lastMeta.Dropped != 3 {
		t.Errorf("meta = %+v, want {Seq:7 Dropped:3}", lastMeta)
	}

	// Plain DriveEvent delivers a zero EventMeta to a meta handler.
	fh.DriveEvent(plugin.CarCreated, plugin.CarCreatedEvent{CarID: "c-2"})
	if lastMeta.Seq != 0 || lastMeta.Dropped != 0 {
		t.Errorf("DriveEvent should deliver zero meta, got %+v", lastMeta)
	}

	// After unsubscribe, no further deliveries.
	unsub()
	if got := fh.DriveEventWithMeta(plugin.CarCreated, plugin.CarCreatedEvent{CarID: "c-3"}, plugin.EventMeta{Seq: 9}); got != 0 {
		t.Errorf("after unsubscribe DriveEventWithMeta invoked %d, want 0", got)
	}
}

// TestFakeHostEmit records Emit calls and honors EmitErr (railyard-77h.9).
func TestFakeHostEmit(t *testing.T) {
	fh := &plugintest.FakeHost{}

	if err := fh.Emit(context.Background(), "trainmaster.synced", map[string]any{"n": 1}); err != nil {
		t.Fatalf("Emit: %v", err)
	}
	emits := fh.Emits()
	if len(emits) != 1 {
		t.Fatalf("Emits len = %d, want 1", len(emits))
	}
	if emits[0].Topic != "trainmaster.synced" {
		t.Errorf("emit topic = %q, want trainmaster.synced", emits[0].Topic)
	}
	if emits[0].Payload["n"] != 1 {
		t.Errorf("emit payload = %v, want n=1", emits[0].Payload)
	}

	// EmitErr is injected and still records the attempt.
	fh.EmitErr = errors.New("boom")
	if err := fh.Emit(context.Background(), "trainmaster.failed", nil); err == nil {
		t.Error("expected injected EmitErr")
	}
	if len(fh.Emits()) != 2 {
		t.Errorf("Emits should record even on error; got %d", len(fh.Emits()))
	}
}
