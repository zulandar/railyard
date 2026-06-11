package plugintest_test

import (
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
