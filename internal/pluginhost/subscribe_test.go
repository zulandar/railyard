package pluginhost

import (
	"context"
	"runtime"
	"strconv"
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

// gatedSubscribeStream blocks inside the FIRST Send until its gate is
// released, recording every event sent. This lets a test wedge the
// drain loop, flood the bus to force host-side drop-oldest, then
// release and inspect the seq/dropped stamps on subsequently delivered
// events (railyard-77h.10).
type gatedSubscribeStream struct {
	protov1.HostService_SubscribeServer

	ctx context.Context

	mu        sync.Mutex
	received  []*protov1.Event
	gate      chan struct{}
	firstSent chan struct{}
	once      sync.Once
}

func (s *gatedSubscribeStream) Context() context.Context { return s.ctx }

func (s *gatedSubscribeStream) Send(ev *protov1.Event) error {
	s.mu.Lock()
	s.received = append(s.received, ev)
	first := len(s.received) == 1
	s.mu.Unlock()
	if first {
		s.once.Do(func() { close(s.firstSent) })
		select {
		case <-s.gate:
		case <-s.ctx.Done():
		}
	}
	return nil
}

func (s *gatedSubscribeStream) snapshot() []*protov1.Event {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]*protov1.Event, len(s.received))
	copy(out, s.received)
	return out
}

// registerLaunchedForSubscribe inserts a bare launchedPlugin entry under
// `name` so the Subscribe RPC's lookupPluginByName resolves to a registry
// entry whose per-plugin counters (railyard-77h.14) can be asserted.
func registerLaunchedForSubscribe(t *testing.T, h *Host, name string) *launchedPlugin {
	t.Helper()
	lp := &launchedPlugin{name: name}
	h.mu.Lock()
	h.launched[name] = lp
	h.mu.Unlock()
	return lp
}

// TestSubscribeIncrementsEventsDelivered asserts normal delivery bumps
// the per-plugin eventsDelivered counter that survives across
// subscriptions/relaunches (railyard-77h.14).
func TestSubscribeIncrementsEventsDelivered(t *testing.T) {
	bus := events.NewBus()
	defer bus.(interface{ Close() }).Close()
	host := NewHost(Dependencies{Bus: bus, Cfg: permissivePluginCfg("p1")})
	lp := registerLaunchedForSubscribe(t, host, "p1")
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

	time.Sleep(50 * time.Millisecond)
	bus.Publish(string(plugin.CarCreated), plugin.CarCreatedEvent{CarID: "car-abc"})

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if lp.eventsDelivered.Load() >= 1 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if got := lp.eventsDelivered.Load(); got != 1 {
		t.Errorf("eventsDelivered = %d, want 1", got)
	}
	if got := lp.eventsDropped.Load(); got != 0 {
		t.Errorf("eventsDropped = %d, want 0", got)
	}

	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Subscribe did not exit after context cancel")
	}
}

// TestSubscribeIncrementsEventsDropped forces the host-side drop-oldest
// path (same wedge technique as TestSubscribeStampsSeqAndDropped) and
// asserts the per-plugin eventsDropped counter grows once backpressure
// fires (railyard-77h.14).
func TestSubscribeIncrementsEventsDropped(t *testing.T) {
	bus := events.NewBus()
	defer bus.(interface{ Close() }).Close()
	host := NewHost(Dependencies{Bus: bus, Cfg: permissivePluginCfg("p1")})
	lp := registerLaunchedForSubscribe(t, host, "p1")
	hs := newHostService(host, "p1")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stream := &gatedSubscribeStream{
		ctx:       ctx,
		gate:      make(chan struct{}),
		firstSent: make(chan struct{}),
	}

	done := make(chan error, 1)
	go func() {
		done <- hs.Subscribe(&protov1.SubscribeRequest{
			Topics: []string{string(plugin.CarCreated)},
		}, stream)
	}()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		host.mu.Lock()
		n := host.subscriptions["p1"]
		host.mu.Unlock()
		if n == 1 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	bus.Publish(string(plugin.CarCreated), plugin.CarCreatedEvent{CarID: "0"})
	select {
	case <-stream.firstSent:
	case <-time.After(2 * time.Second):
		t.Fatal("drain loop never entered first Send")
	}

	for i := 1; i <= 4000; i++ {
		bus.Publish(string(plugin.CarCreated), plugin.CarCreatedEvent{CarID: strconv.Itoa(i)})
	}
	time.Sleep(200 * time.Millisecond)
	close(stream.gate)
	time.Sleep(300 * time.Millisecond)

	if got := lp.eventsDropped.Load(); got == 0 {
		t.Errorf("expected eventsDropped > 0 after backpressure, got 0")
	}
	if got := lp.eventsDelivered.Load(); got == 0 {
		t.Errorf("expected eventsDelivered > 0, got 0")
	}

	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Subscribe did not exit after cancel")
	}
}

// TestSubscribeStampsSeqAndDropped forces the host-side drop-oldest path
// by wedging the drain loop on its first Send, floods the bus, then
// releases and asserts the delivered events carry a monotonic per-stream
// seq (continuous over DELIVERED events, starting at 1) and a cumulative
// dropped count that grows once backpressure fired (railyard-77h.10).
func TestSubscribeStampsSeqAndDropped(t *testing.T) {
	bus := events.NewBus()
	defer bus.(interface{ Close() }).Close()
	host := NewHost(Dependencies{Bus: bus, Cfg: permissivePluginCfg("p1")})
	hs := newHostService(host, "p1")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stream := &gatedSubscribeStream{
		ctx:       ctx,
		gate:      make(chan struct{}),
		firstSent: make(chan struct{}),
	}

	done := make(chan error, 1)
	go func() {
		done <- hs.Subscribe(&protov1.SubscribeRequest{
			Topics: []string{string(plugin.CarCreated)},
		}, stream)
	}()

	// Wait for the subscription to wire up.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		host.mu.Lock()
		n := host.subscriptions["p1"]
		host.mu.Unlock()
		if n == 1 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	// Publish one event so the drain loop picks it up and wedges in Send.
	bus.Publish(string(plugin.CarCreated), plugin.CarCreatedEvent{CarID: "0"})
	select {
	case <-stream.firstSent:
	case <-time.After(2 * time.Second):
		t.Fatal("drain loop never entered first Send")
	}

	// Flood while the drain loop is wedged. The host queue (cap 256) fills
	// and subsequent events hit the host-side drop-oldest path.
	for i := 1; i <= 4000; i++ {
		bus.Publish(string(plugin.CarCreated), plugin.CarCreatedEvent{CarID: strconv.Itoa(i)})
	}
	// Give the bus a moment to drain its deliveries into the host queue
	// (and trigger drops) before releasing.
	time.Sleep(200 * time.Millisecond)

	// Release the drain loop.
	close(stream.gate)

	// Let delivery proceed.
	time.Sleep(300 * time.Millisecond)

	recv := stream.snapshot()
	if len(recv) < 2 {
		t.Fatalf("expected at least 2 delivered events, got %d", len(recv))
	}
	// seq is continuous over delivered events starting at 1.
	for i, ev := range recv {
		if ev.Seq != uint64(i+1) {
			t.Errorf("delivered[%d].Seq = %d, want %d", i, ev.Seq, i+1)
		}
	}
	// First delivered event predates any drop.
	if recv[0].Dropped != 0 {
		t.Errorf("first delivered event Dropped = %d, want 0", recv[0].Dropped)
	}
	// Backpressure fired: the last delivered event reports drops.
	last := recv[len(recv)-1]
	if last.Dropped == 0 {
		t.Errorf("expected last delivered event to report Dropped > 0, got 0 (delivered=%d)", len(recv))
	}

	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Subscribe did not exit after cancel")
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
