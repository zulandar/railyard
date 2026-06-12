package pluginhost

import (
	"context"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/structpb"

	"github.com/zulandar/railyard/internal/config"
	"github.com/zulandar/railyard/internal/events"
	protov1 "github.com/zulandar/railyard/pkg/plugin/proto/v1"
)

// emitHostFixture builds a host whose plugin "pa" is allowed to publish
// the given topics (railyard-77h.9).
func emitHostFixture(t *testing.T, publish []string) (*Host, events.Bus) {
	t.Helper()
	bus := events.NewBus()
	t.Cleanup(func() {
		if c, ok := bus.(interface{ Close() }); ok {
			c.Close()
		}
	})
	cfg := &config.Config{
		Plugins: config.PluginsConfig{
			Enabled: []string{"pa"},
			Settings: map[string]config.PluginSettings{
				"pa": {Allow: config.AllowConfig{Publish: publish}},
			},
		},
	}
	return NewHost(Dependencies{Bus: bus, Cfg: cfg}), bus
}

func mustStruct(t *testing.T, m map[string]any) *structpb.Struct {
	t.Helper()
	s, err := structpb.NewStruct(m)
	if err != nil {
		t.Fatalf("structpb: %v", err)
	}
	return s
}

// TestEmitEvent_NamespaceEnforced rejects a topic not prefixed with the
// caller's own connection-bound name, regardless of allow.publish.
func TestEmitEvent_NamespaceEnforced(t *testing.T) {
	host, _ := emitHostFixture(t, []string{"*"})
	hs := newHostService(host, "pa")
	_, err := hs.EmitEvent(context.Background(), &protov1.EmitEventRequest{
		Topic:   "pb.thing", // someone else's namespace
		Payload: mustStruct(t, map[string]any{"x": "y"}),
	})
	if status.Code(err) != codes.PermissionDenied {
		t.Fatalf("EmitEvent cross-namespace err = %v, want PermissionDenied", err)
	}
}

// TestEmitEvent_DenyByDefault rejects publishing when allow.publish is
// empty, even for the plugin's own namespace.
func TestEmitEvent_DenyByDefault(t *testing.T) {
	host, _ := emitHostFixture(t, nil) // no publish entries
	hs := newHostService(host, "pa")
	_, err := hs.EmitEvent(context.Background(), &protov1.EmitEventRequest{
		Topic:   "pa.thing",
		Payload: mustStruct(t, map[string]any{"x": "y"}),
	})
	if status.Code(err) != codes.PermissionDenied {
		t.Fatalf("EmitEvent deny-by-default err = %v, want PermissionDenied", err)
	}
}

// TestEmitEvent_AllowedPublishesToBus delivers an allowed, correctly
// namespaced event onto the bus with its map payload intact.
func TestEmitEvent_AllowedPublishesToBus(t *testing.T) {
	host, bus := emitHostFixture(t, []string{"pa.*"})
	hs := newHostService(host, "pa")

	got := make(chan map[string]any, 1)
	bus.Subscribe("pa.thing", func(payload any) {
		if m, ok := payload.(map[string]any); ok {
			got <- m
		}
	})

	_, err := hs.EmitEvent(context.Background(), &protov1.EmitEventRequest{
		Topic:   "pa.thing",
		Payload: mustStruct(t, map[string]any{"hello": "world"}),
	})
	if err != nil {
		t.Fatalf("EmitEvent allowed err = %v, want nil", err)
	}

	select {
	case m := <-got:
		if m["hello"] != "world" {
			t.Errorf("payload = %v, want hello=world", m)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("bus subscriber never received the emitted event")
	}
}

// TestEmitEvent_NoBus surfaces Unavailable when the host has no bus.
func TestEmitEvent_NoBus(t *testing.T) {
	cfg := &config.Config{
		Plugins: config.PluginsConfig{
			Enabled:  []string{"pa"},
			Settings: map[string]config.PluginSettings{"pa": {Allow: config.AllowConfig{Publish: []string{"*"}}}},
		},
	}
	host := NewHost(Dependencies{Cfg: cfg}) // no Bus
	hs := newHostService(host, "pa")
	_, err := hs.EmitEvent(context.Background(), &protov1.EmitEventRequest{
		Topic:   "pa.thing",
		Payload: mustStruct(t, map[string]any{"x": "y"}),
	})
	if status.Code(err) != codes.Unavailable {
		t.Fatalf("EmitEvent no-bus err = %v, want Unavailable", err)
	}
}

// TestSubscribeDeliversDynamicEvent proves a plugin-published dynamic
// event (map payload under a namespaced topic) flows through the
// Subscribe stream as an Event carrying topic_name + the custom Struct
// arm (railyard-77h.9).
func TestSubscribeDeliversDynamicEvent(t *testing.T) {
	bus := events.NewBus()
	defer bus.(interface{ Close() }).Close()
	cfg := &config.Config{
		Plugins: config.PluginsConfig{
			Enabled: []string{"pb"},
			Settings: map[string]config.PluginSettings{
				"pb": {Allow: config.AllowConfig{Events: []string{"pa.thing"}}},
			},
		},
	}
	host := NewHost(Dependencies{Bus: bus, Cfg: cfg})
	hs := newHostService(host, "pb")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stream := &fakeSubscribeStream{ctx: ctx}
	done := make(chan error, 1)
	go func() {
		done <- hs.Subscribe(&protov1.SubscribeRequest{Topics: []string{"pa.thing"}}, stream)
	}()
	time.Sleep(50 * time.Millisecond)

	bus.Publish("pa.thing", map[string]any{"hello": "world"})

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		stream.mu.Lock()
		n := len(stream.received)
		stream.mu.Unlock()
		if n >= 1 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	stream.mu.Lock()
	defer stream.mu.Unlock()
	if len(stream.received) == 0 {
		t.Fatal("dynamic event not delivered")
	}
	ev := stream.received[0]
	if ev.TopicName != "pa.thing" {
		t.Errorf("TopicName = %q, want pa.thing", ev.TopicName)
	}
	custom := ev.GetCustom()
	if custom == nil {
		t.Fatal("expected custom Struct payload arm")
	}
	if custom.AsMap()["hello"] != "world" {
		t.Errorf("custom payload = %v, want hello=world", custom.AsMap())
	}

	cancel()
	<-done
}
