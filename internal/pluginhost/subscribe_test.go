package pluginhost

import (
	"context"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/zulandar/railyard/internal/config"
	"github.com/zulandar/railyard/internal/events"
	"github.com/zulandar/railyard/pkg/plugin"
	protov1 "github.com/zulandar/railyard/pkg/plugin/proto/v1"
)

// permissivePluginCfg returns a minimal config that grants `pluginName`
// the "*" allow-list — used by tests that pre-date railyard-fll.4 and
// just want the legacy "everything allowed" behavior.
func permissivePluginCfg(pluginName string) *config.Config {
	return &config.Config{
		Plugins: config.PluginsConfig{
			Enabled: []string{pluginName},
			Settings: map[string]config.PluginSettings{
				pluginName: {
					Allow: config.AllowConfig{
						Events:   []string{"*"},
						Commands: []string{"*"},
					},
				},
			},
		},
	}
}

// fakeSubscribeStream is a minimal in-process implementation of the
// HostService_SubscribeServer used by the Subscribe RPC. It records
// every Send into a slice and observes the supplied context for
// cancellation.
type fakeSubscribeStream struct {
	protov1.HostService_SubscribeServer // for any unused methods

	ctx context.Context

	mu       sync.Mutex
	received []*protov1.Event
}

func (s *fakeSubscribeStream) Context() context.Context { return s.ctx }

func (s *fakeSubscribeStream) Send(ev *protov1.Event) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.received = append(s.received, ev)
	return nil
}

func (s *fakeSubscribeStream) Recv() (*protov1.Event, error) { return nil, nil }

// TestSubscribeDeliversEvents wires a Subscribe call to the host's bus
// and asserts a published event lands on the gRPC stream.
func TestSubscribeDeliversEvents(t *testing.T) {
	bus := events.NewBus()
	defer bus.(interface{ Close() }).Close()
	host := NewHost(Dependencies{Bus: bus, Cfg: permissivePluginCfg("p1")})
	hs := newHostService(host, "p1")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stream := &fakeSubscribeStream{ctx: ctx}

	done := make(chan error, 1)
	go func() {
		done <- hs.Subscribe(&protov1.SubscribeRequest{
			Topics: []string{string(plugin.CarCreated)},
		}, stream)
	}()

	// Give the goroutine a moment to wire up bus subscriptions.
	time.Sleep(50 * time.Millisecond)

	bus.Publish(string(plugin.CarCreated), plugin.CarCreatedEvent{
		CarID:    "car-abc",
		Track:    "go",
		Type:     "bug",
		Priority: 1,
	})

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
		t.Fatal("no events delivered")
	}
	got := stream.received[0]
	if got.Type != protov1.EventType_EVENT_TYPE_CAR_CREATED {
		t.Errorf("type = %v, want CAR_CREATED", got.Type)
	}
	if p := got.GetCarCreated(); p == nil || p.CarId != "car-abc" {
		t.Errorf("payload = %+v, want CarId=car-abc", p)
	}

	cancel()
	// Stream goroutine should exit promptly when the context is cancelled.
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Subscribe did not exit after context cancel")
	}
}

// TestSubscribeBumpsSubscriptionCount asserts the gRPC Subscribe path
// updates h.subscriptions per-topic (railyard-vdp regression). The
// in-process Host.Subscribe shim already bumped this counter; only the
// subprocess gRPC path used to bypass it, so Status() always reported
// 0 for real plugins.
func TestSubscribeBumpsSubscriptionCount(t *testing.T) {
	bus := events.NewBus()
	defer bus.(interface{ Close() }).Close()
	host := NewHost(Dependencies{Bus: bus, Cfg: permissivePluginCfg("p1")})
	hs := newHostService(host, "p1")

	ctx, cancel := context.WithCancel(context.Background())
	stream := &fakeSubscribeStream{ctx: ctx}

	done := make(chan error, 1)
	go func() {
		done <- hs.Subscribe(&protov1.SubscribeRequest{
			Topics: []string{string(plugin.CarCreated), string(plugin.CarMerged)},
		}, stream)
	}()

	// Wait for the per-topic bus subscriptions to wire up. Poll the
	// counter rather than sleeping to avoid a fixed race window.
	deadline := time.Now().Add(2 * time.Second)
	var got int
	for time.Now().Before(deadline) {
		host.mu.Lock()
		got = host.subscriptions["p1"]
		host.mu.Unlock()
		if got == 2 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if got != 2 {
		t.Fatalf("after Subscribe, h.subscriptions[p1] = %d, want 2", got)
	}

	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Subscribe goroutine did not exit after cancel")
	}

	// After cleanup the counter must return to zero.
	deadline = time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		host.mu.Lock()
		got = host.subscriptions["p1"]
		host.mu.Unlock()
		if got == 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if got != 0 {
		t.Fatalf("after Subscribe cleanup, h.subscriptions[p1] = %d, want 0", got)
	}
}

// TestSubscribeNoGoroutineLeak ensures the per-stream bus subscriptions
// + drain goroutine are cleaned up when the client disconnects. We bound
// the leak check to a few hundred milliseconds because goroutine teardown
// is asynchronous; flaky scheduling under -race can take longer than
// expected, hence the polling loop.
func TestSubscribeNoGoroutineLeak(t *testing.T) {
	// Warm up — give any one-time runtime initialisation a chance to
	// settle so the baseline reading is stable.
	runtime.GC()
	baseline := runtime.NumGoroutine()

	bus := events.NewBus()
	host := NewHost(Dependencies{Bus: bus, Cfg: permissivePluginCfg("p1")})
	hs := newHostService(host, "p1")

	for i := 0; i < 5; i++ {
		ctx, cancel := context.WithCancel(context.Background())
		stream := &fakeSubscribeStream{ctx: ctx}
		done := make(chan error, 1)
		go func() {
			done <- hs.Subscribe(&protov1.SubscribeRequest{
				Topics: []string{string(plugin.CarCreated)},
			}, stream)
		}()
		time.Sleep(20 * time.Millisecond)
		cancel()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Fatal("subscribe did not exit")
		}
	}

	bus.(interface{ Close() }).Close()

	// Allow up to 500ms for stragglers (the bus close itself drains in a
	// goroutine).
	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		runtime.GC()
		if got := runtime.NumGoroutine(); got <= baseline+1 {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Errorf("goroutine leak: NumGoroutine = %d, baseline = %d", runtime.NumGoroutine(), baseline)
}

// TestSubscribeNilBus returns an error when the host has no bus wired.
func TestSubscribeNilBus(t *testing.T) {
	host := NewHost(Dependencies{})
	hs := newHostService(host, "p1")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stream := &fakeSubscribeStream{ctx: ctx}
	if err := hs.Subscribe(&protov1.SubscribeRequest{Topics: []string{"X"}}, stream); err == nil {
		t.Error("expected error from nil-bus Subscribe")
	}
}

// TestSubscribeAllowList_AllDenied confirms a plugin with no allow-list
// entry for the requested topic receives a gRPC PermissionDenied.
func TestSubscribeAllowList_AllDenied(t *testing.T) {
	bus := events.NewBus()
	defer bus.(interface{ Close() }).Close()
	// Plugin is enabled but has no settings entry → strict default
	// (every cap denied).
	cfg := &config.Config{
		Plugins: config.PluginsConfig{
			Enabled: []string{"p1"},
		},
	}
	host := NewHost(Dependencies{Bus: bus, Cfg: cfg})
	hs := newHostService(host, "p1")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stream := &fakeSubscribeStream{ctx: ctx}
	err := hs.Subscribe(&protov1.SubscribeRequest{Topics: []string{string(plugin.CarCreated)}}, stream)
	if err == nil {
		t.Fatal("expected PermissionDenied")
	}
	if !strings.Contains(err.Error(), "not allowed to subscribe") {
		t.Errorf("error = %q, want permission-denied message", err.Error())
	}
}

// TestSubscribeAllowList_PartialDeny_AllowedTopicsFlow confirms that
// when the request mixes allowed and denied topics, the allowed ones
// flow through and the denied ones are silently dropped.
func TestSubscribeAllowList_PartialDeny_AllowedTopicsFlow(t *testing.T) {
	bus := events.NewBus()
	defer bus.(interface{ Close() }).Close()
	cfg := &config.Config{
		Plugins: config.PluginsConfig{
			Enabled: []string{"p1"},
			Settings: map[string]config.PluginSettings{
				"p1": {Allow: config.AllowConfig{
					Events: []string{string(plugin.CarCreated)},
				}},
			},
		},
	}
	host := NewHost(Dependencies{Bus: bus, Cfg: cfg})
	hs := newHostService(host, "p1")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stream := &fakeSubscribeStream{ctx: ctx}

	done := make(chan error, 1)
	go func() {
		// One allowed, one denied. The allowed one should flow.
		done <- hs.Subscribe(&protov1.SubscribeRequest{
			Topics: []string{string(plugin.CarCreated), string(plugin.CarMerged)},
		}, stream)
	}()

	time.Sleep(50 * time.Millisecond)
	bus.Publish(string(plugin.CarCreated), plugin.CarCreatedEvent{CarID: "car-a"})

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
	got := len(stream.received)
	stream.mu.Unlock()
	if got == 0 {
		t.Fatal("allowed CarCreated event was not delivered")
	}

	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Subscribe did not exit")
	}
}
