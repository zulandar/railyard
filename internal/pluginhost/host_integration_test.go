//go:build legacy_inproc
// +build legacy_inproc

// Legacy in-process lifecycle integration tests. They drive the retired
// host.Register(plugin.Plugin) + Init/Start/Stop walk and the daemon
// manager. The subprocess plugin model (railyard-fll.3) replaced that
// surface; re-writing these as subprocess-driven tests is tracked by bd
// issue railyard-bjp.
package pluginhost

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/zulandar/railyard/internal/events"
	"github.com/zulandar/railyard/pkg/plugin"
)

// daemonPlugin is a fakePlugin variant whose Start callback can register
// daemons via Host.RunDaemon. Sharing fakePlugin would couple unrelated
// tests, so we use a small dedicated type here.
//
// onStart receives the per-plugin Host (a *pluginView) so the test can
// drive RunDaemon/Subscribe/etc. with the same per-plugin scoping
// production code observes.
type daemonPlugin struct {
	name    string
	onInit  func(ctx context.Context, h plugin.Host) error
	onStart func(ctx context.Context, h plugin.Host) error
	onStop  func(ctx context.Context) error

	mu   sync.Mutex
	host plugin.Host
}

func (p *daemonPlugin) Name() string { return p.name }

func (p *daemonPlugin) Init(ctx context.Context, h plugin.Host) error {
	p.mu.Lock()
	p.host = h
	p.mu.Unlock()
	if p.onInit != nil {
		return p.onInit(ctx, h)
	}
	return nil
}

func (p *daemonPlugin) Start(ctx context.Context) error {
	p.mu.Lock()
	h := p.host
	p.mu.Unlock()
	if p.onStart != nil {
		return p.onStart(ctx, h)
	}
	return nil
}

func (p *daemonPlugin) Stop(ctx context.Context) error {
	if p.onStop != nil {
		return p.onStop(ctx)
	}
	return nil
}

// waitFor polls the predicate until it returns true or the timeout
// expires. Returns true on success. Used instead of long Sleeps to keep
// concurrency tests responsive on busy CI.
func waitFor(t *testing.T, timeout time.Duration, pred func() bool) bool {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if pred() {
			return true
		}
		time.Sleep(2 * time.Millisecond)
	}
	return pred()
}

// TestDaemonHappyPathShutdown exercises the most common daemon flow:
// register a daemon, host.Stop cancels its context, daemon exits cleanly,
// and Stop returns promptly (well under the 5s budget).
func TestDaemonHappyPathShutdown(t *testing.T) {
	host := NewHost(Dependencies{})
	started := make(chan struct{})
	var exited atomic.Bool

	p := &daemonPlugin{
		name: "happy",
		onStart: func(ctx context.Context, h plugin.Host) error {
			h.RunDaemon("ticker", func(ctx context.Context) error {
				close(started)
				<-ctx.Done()
				exited.Store(true)
				return nil
			})
			return nil
		},
	}
	host.Register(p)
	host.Init(context.Background())
	host.Start(context.Background())

	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("daemon did not start within 1s")
	}

	stopStart := time.Now()
	host.Stop(context.Background())
	elapsed := time.Since(stopStart)

	if !exited.Load() {
		t.Error("daemon did not observe ctx cancellation before host returned")
	}
	if elapsed > 500*time.Millisecond {
		t.Errorf("Stop took %v, expected to return promptly after daemon honored cancellation", elapsed)
	}
}

// TestDaemonPanicRestart asserts a single panic results in a restart and
// the daemon continues running. Counts invocations to confirm the
// supervisor reinvoked fn.
func TestDaemonPanicRestart(t *testing.T) {
	host := NewHost(Dependencies{})

	var invocations atomic.Int32
	secondRun := make(chan struct{})

	p := &daemonPlugin{
		name: "flappy",
		onStart: func(ctx context.Context, h plugin.Host) error {
			h.RunDaemon("flaky", func(ctx context.Context) error {
				n := invocations.Add(1)
				if n == 1 {
					panic("first call panics")
				}
				close(secondRun)
				<-ctx.Done()
				return nil
			})
			return nil
		},
	}
	host.Register(p)
	host.Init(context.Background())
	host.Start(context.Background())

	select {
	case <-secondRun:
	case <-time.After(2 * time.Second):
		t.Fatal("daemon did not restart after first panic within 2s")
	}

	if invocations.Load() < 2 {
		t.Errorf("invocations = %d, expected at least 2 (one panic + one restart)", invocations.Load())
	}

	host.Stop(context.Background())
}

// TestDaemonPanicBudgetExhausted asserts the lifetime panic budget caps
// at exactly daemonRestartBudget invocations. After the budget is
// exhausted the daemon must not be invoked again.
func TestDaemonPanicBudgetExhausted(t *testing.T) {
	host := NewHost(Dependencies{})

	var invocations atomic.Int32
	done := make(chan struct{})

	p := &daemonPlugin{
		name: "doomed",
		onStart: func(ctx context.Context, h plugin.Host) error {
			h.RunDaemon("crash", func(ctx context.Context) error {
				n := invocations.Add(1)
				if n == int32(daemonRestartBudget) {
					// Signal we hit the budget exactly; the supervisor
					// must NOT call us again after this returns (which
					// it does via panic propagation through the recover).
					defer close(done)
				}
				panic("always panics")
			})
			return nil
		},
	}
	host.Register(p)
	host.Init(context.Background())
	host.Start(context.Background())

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatalf("daemon did not reach budget within 2s (invocations=%d)", invocations.Load())
	}

	// Give the supervisor a brief window to (incorrectly) re-invoke.
	// daemonRestartBudget is small and the goroutine is tight; 100ms is
	// plenty without introducing flakiness.
	if waitFor(t, 100*time.Millisecond, func() bool {
		return invocations.Load() > int32(daemonRestartBudget)
	}) {
		t.Errorf("invocations = %d, expected exactly %d (budget exhausted, no further restarts)",
			invocations.Load(), daemonRestartBudget)
	}
	if got, want := invocations.Load(), int32(daemonRestartBudget); got != want {
		t.Errorf("invocations = %d, want %d", got, want)
	}

	host.Stop(context.Background())
}

// TestDaemonAbandonedAfterDrainTimeout verifies a daemon that ignores
// ctx cancellation is abandoned after the 5s drain timeout. Host.Stop
// must return within ~5s.
func TestDaemonAbandonedAfterDrainTimeout(t *testing.T) {
	host := NewHost(Dependencies{})
	released := make(chan struct{})
	t.Cleanup(func() { close(released) })

	started := make(chan struct{})
	p := &daemonPlugin{
		name: "stubborn-daemon",
		onStart: func(ctx context.Context, h plugin.Host) error {
			h.RunDaemon("ignore-ctx", func(ctx context.Context) error {
				close(started)
				// Intentionally ignore ctx.Done — only return when the
				// test's cleanup releases us.
				<-released
				return nil
			})
			return nil
		},
	}
	host.Register(p)
	host.Init(context.Background())
	host.Start(context.Background())

	<-started

	stopStart := time.Now()
	host.Stop(context.Background())
	elapsed := time.Since(stopStart)

	if elapsed < 4500*time.Millisecond {
		t.Errorf("Stop returned in %v — drain timeout did not engage (expected ~5s)", elapsed)
	}
	if elapsed > 6*time.Second {
		t.Errorf("Stop took %v, expected ~5s drain bound", elapsed)
	}
}

// TestPluginEventSubscription wires a real events.Bus, has one plugin
// publish via the bus (simulating internal/* call sites), and asserts a
// subscriber registered via Host.Subscribe receives the typed payload.
func TestPluginEventSubscription(t *testing.T) {
	bus := events.NewBus()
	host := NewHost(Dependencies{Bus: bus})

	received := make(chan plugin.CarCreatedEvent, 1)
	var unsub plugin.Unsubscribe

	subscriber := &daemonPlugin{
		name: "subscriber",
		onStart: func(ctx context.Context, h plugin.Host) error {
			unsub = h.Subscribe(plugin.CarCreated, func(topic plugin.EventType, payload any) {
				if topic != plugin.CarCreated {
					t.Errorf("handler topic = %q, want CarCreated", topic)
				}
				ev, ok := payload.(plugin.CarCreatedEvent)
				if !ok {
					t.Errorf("payload type = %T, want CarCreatedEvent", payload)
					return
				}
				received <- ev
			})
			return nil
		},
	}
	host.Register(subscriber)
	host.Init(context.Background())
	host.Start(context.Background())
	defer host.Stop(context.Background())
	defer func() {
		if unsub != nil {
			unsub()
		}
	}()

	bus.Publish(string(plugin.CarCreated), plugin.CarCreatedEvent{
		CarID:       "car-1",
		Track:       "main",
		Type:        "feature",
		Priority:    1,
		RequestedBy: "alice",
	})

	select {
	case ev := <-received:
		if ev.CarID != "car-1" {
			t.Errorf("CarID = %q, want car-1", ev.CarID)
		}
	case <-time.After(time.Second):
		t.Fatal("subscriber did not receive event within 1s")
	}
}

// TestSubscribeWithNilBus verifies the no-op Unsubscribe path: a host
// constructed without a bus still returns a callable unsubscribe so
// plugin code stays correct in stripped-down test contexts.
func TestSubscribeWithNilBus(t *testing.T) {
	host := NewHost(Dependencies{})

	called := false
	p := &daemonPlugin{
		name: "no-bus",
		onStart: func(ctx context.Context, h plugin.Host) error {
			unsub := h.Subscribe(plugin.CarCreated, func(topic plugin.EventType, payload any) {
				called = true
			})
			if unsub == nil {
				t.Error("Subscribe with nil bus returned nil Unsubscribe")
				return nil
			}
			// Must not panic — exercises the no-op closure path.
			unsub()
			return nil
		},
	}
	host.Register(p)
	host.Init(context.Background())
	host.Start(context.Background())
	host.Stop(context.Background())

	if called {
		t.Error("handler invoked despite nil bus")
	}
}

// bufHandler is a slog.Handler that writes JSON lines to a mutex-guarded
// buffer. Used to assert the per-plugin Logger attaches the right
// attributes without races against concurrent log writes.
type bufHandler struct {
	mu  sync.Mutex
	buf *bytes.Buffer
	h   slog.Handler
}

func newBufHandler() (*bufHandler, *bytes.Buffer) {
	buf := &bytes.Buffer{}
	bh := &bufHandler{buf: buf}
	bh.h = slog.NewTextHandler(&lockedWriter{bh: bh}, nil)
	return bh, buf
}

func (b *bufHandler) Enabled(ctx context.Context, l slog.Level) bool {
	return b.h.Enabled(ctx, l)
}
func (b *bufHandler) Handle(ctx context.Context, r slog.Record) error {
	return b.h.Handle(ctx, r)
}
func (b *bufHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &bufHandler{buf: b.buf, h: b.h.WithAttrs(attrs)}
}
func (b *bufHandler) WithGroup(name string) slog.Handler {
	return &bufHandler{buf: b.buf, h: b.h.WithGroup(name)}
}

// lockedWriter is the io.Writer view of bufHandler's buffer with a mutex.
// slog handlers can be cloned via WithAttrs; the mutex lives on the root
// bufHandler so every clone shares the same lock.
type lockedWriter struct{ bh *bufHandler }

func (w *lockedWriter) Write(p []byte) (int, error) {
	w.bh.mu.Lock()
	defer w.bh.mu.Unlock()
	return w.bh.buf.Write(p)
}

// snapshot returns a copy of the buffered log under the mutex. Test
// assertions must use this rather than reading the buffer directly to
// avoid racing with daemon goroutines that may still be writing.
func (b *bufHandler) snapshot() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// TestPerPluginLoggerScoping captures slog output and asserts the
// per-plugin logger from Host.Logger() attaches plugin=<name>. Also
// asserts daemon supervision logs include plugin= and daemon= tags.
func TestPerPluginLoggerScoping(t *testing.T) {
	bh, _ := newBufHandler()
	prev := slog.Default()
	slog.SetDefault(slog.New(bh))
	t.Cleanup(func() { slog.SetDefault(prev) })

	host := NewHost(Dependencies{})

	loggerEmitted := make(chan struct{})
	p := &daemonPlugin{
		name: "scoped-plugin",
		onStart: func(ctx context.Context, h plugin.Host) error {
			h.Logger().Info("hello from plugin")
			close(loggerEmitted)
			return nil
		},
	}
	host.Register(p)
	host.Init(context.Background())
	host.Start(context.Background())

	<-loggerEmitted

	got := bh.snapshot()
	if !strings.Contains(got, `plugin=scoped-plugin`) {
		t.Errorf("log output missing plugin=scoped-plugin\nfull output:\n%s", got)
	}
	if !strings.Contains(got, "hello from plugin") {
		t.Errorf("log output missing plugin message\nfull output:\n%s", got)
	}

	host.Stop(context.Background())
}

// TestDaemonLoggerHasPluginAndDaemonTags drives a daemon panic and
// inspects the captured log output for the plugin= and daemon= tags spec
// §8 requires.
func TestDaemonLoggerHasPluginAndDaemonTags(t *testing.T) {
	bh, _ := newBufHandler()
	prev := slog.Default()
	slog.SetDefault(slog.New(bh))
	t.Cleanup(func() { slog.SetDefault(prev) })

	host := NewHost(Dependencies{})

	panicked := make(chan struct{}, 1)
	p := &daemonPlugin{
		name: "logger-test",
		onStart: func(ctx context.Context, h plugin.Host) error {
			h.RunDaemon("tagged", func(ctx context.Context) error {
				select {
				case panicked <- struct{}{}:
				default:
				}
				panic("inspect the log")
			})
			return nil
		},
	}
	host.Register(p)
	host.Init(context.Background())
	host.Start(context.Background())

	// Wait for at least one panic to be logged.
	<-panicked
	if !waitFor(t, time.Second, func() bool {
		return strings.Contains(bh.snapshot(), "daemon panicked")
	}) {
		t.Fatalf("did not observe daemon panic log within 1s\noutput:\n%s", bh.snapshot())
	}

	got := bh.snapshot()
	if !strings.Contains(got, "plugin=logger-test") {
		t.Errorf("log output missing plugin=logger-test\noutput:\n%s", got)
	}
	if !strings.Contains(got, "daemon=tagged") {
		t.Errorf("log output missing daemon=tagged\noutput:\n%s", got)
	}

	host.Stop(context.Background())
}

// TestInitFailureSkipsDaemons asserts a plugin whose Init fails never has
// its Start called and therefore never registers daemons. Belt-and-
// suspenders: even if the plugin tried to register a daemon from Init
// (which production plugins must not do), the host's lifecycle would
// drop the plugin before Start anyway.
func TestInitFailureSkipsDaemons(t *testing.T) {
	host := NewHost(Dependencies{})

	var daemonRan atomic.Bool
	p := &daemonPlugin{
		name: "init-fails",
		onInit: func(ctx context.Context, h plugin.Host) error {
			return errors.New("intentional init failure")
		},
		onStart: func(ctx context.Context, h plugin.Host) error {
			h.RunDaemon("never", func(ctx context.Context) error {
				daemonRan.Store(true)
				<-ctx.Done()
				return nil
			})
			return nil
		},
	}
	host.Register(p)
	host.Init(context.Background())
	host.Start(context.Background())
	host.Stop(context.Background())

	if daemonRan.Load() {
		t.Error("daemon ran despite Init failure — Start should not have been called")
	}
	// The host must not retain phantom daemonStates for a skipped plugin.
	if states := host.cancelDaemons("init-fails"); states != nil {
		t.Errorf("host retained %d daemonStates for skipped plugin", len(states))
	}
}

// TestMultiplePluginsDaemonsFanout registers daemons across multiple
// plugins and asserts Stop cancels every one of them concurrently.
func TestMultiplePluginsDaemonsFanout(t *testing.T) {
	host := NewHost(Dependencies{})

	const numPlugins = 3
	const daemonsPerPlugin = 2

	var exited atomic.Int32
	starts := make(chan struct{}, numPlugins*daemonsPerPlugin)

	for i := 0; i < numPlugins; i++ {
		name := "p" + string(rune('A'+i))
		host.Register(&daemonPlugin{
			name: name,
			onStart: func(ctx context.Context, h plugin.Host) error {
				for d := 0; d < daemonsPerPlugin; d++ {
					h.RunDaemon("d"+string(rune('1'+d)), func(ctx context.Context) error {
						starts <- struct{}{}
						<-ctx.Done()
						exited.Add(1)
						return nil
					})
				}
				return nil
			},
		})
	}

	host.Init(context.Background())
	host.Start(context.Background())

	for i := 0; i < numPlugins*daemonsPerPlugin; i++ {
		select {
		case <-starts:
		case <-time.After(time.Second):
			t.Fatalf("only %d/%d daemons started", i, numPlugins*daemonsPerPlugin)
		}
	}

	stopStart := time.Now()
	host.Stop(context.Background())
	elapsed := time.Since(stopStart)

	if got, want := exited.Load(), int32(numPlugins*daemonsPerPlugin); got != want {
		t.Errorf("exited = %d, want %d", got, want)
	}
	// Each plugin's drain budget is independent and runs sequentially in
	// Stop, but each one returns the instant its daemons honor ctx —
	// well under the per-plugin budget. Total should be tiny.
	if elapsed > time.Second {
		t.Errorf("Stop took %v across %d plugins; expected prompt shutdown", elapsed, numPlugins)
	}
}

// TestSubscribeAfterUnsubscribe verifies the unsubscribe closure stops
// further delivery to a plugin handler.
func TestSubscribeAfterUnsubscribe(t *testing.T) {
	bus := events.NewBus()
	host := NewHost(Dependencies{Bus: bus})

	var received atomic.Int32
	var unsub plugin.Unsubscribe

	p := &daemonPlugin{
		name: "unsub",
		onStart: func(ctx context.Context, h plugin.Host) error {
			unsub = h.Subscribe(plugin.YardPaused, func(topic plugin.EventType, payload any) {
				received.Add(1)
			})
			return nil
		},
	}
	host.Register(p)
	host.Init(context.Background())
	host.Start(context.Background())
	defer host.Stop(context.Background())

	bus.Publish(string(plugin.YardPaused), plugin.YardPausedEvent{Reason: "test"})
	if !waitFor(t, time.Second, func() bool { return received.Load() == 1 }) {
		t.Fatalf("first event not delivered, received=%d", received.Load())
	}

	unsub()
	// Multiple unsub() calls must be safe.
	unsub()

	bus.Publish(string(plugin.YardPaused), plugin.YardPausedEvent{Reason: "test"})
	// Give the bus a moment; if anything is going to arrive, it does so
	// well under 100ms.
	if waitFor(t, 100*time.Millisecond, func() bool { return received.Load() > 1 }) {
		t.Errorf("event delivered after Unsubscribe, received=%d", received.Load())
	}
}

// TestDaemonOnStartedPluginExitsCleanly is a regression test for the
// happy path where the plugin's own Stop drains its own resources and
// the host's daemon cancellation runs alongside without conflict.
func TestDaemonOnStartedPluginExitsCleanly(t *testing.T) {
	host := NewHost(Dependencies{})

	var stopCalled atomic.Bool
	var daemonExited atomic.Bool
	pluginCtx, pluginCancel := context.WithCancel(context.Background())

	p := &daemonPlugin{
		name: "polite",
		onStart: func(ctx context.Context, h plugin.Host) error {
			h.RunDaemon("worker", func(ctx context.Context) error {
				<-ctx.Done()
				daemonExited.Store(true)
				return nil
			})
			return nil
		},
		onStop: func(ctx context.Context) error {
			stopCalled.Store(true)
			pluginCancel() // the plugin tracks its own ctx independently
			return nil
		},
	}
	_ = pluginCtx // referenced only to honor the "plugin tracks its own ctx" pattern

	host.Register(p)
	host.Init(context.Background())
	host.Start(context.Background())
	host.Stop(context.Background())

	if !stopCalled.Load() {
		t.Error("plugin Stop was not called")
	}
	if !daemonExited.Load() {
		t.Error("daemon did not exit cleanly during host Stop")
	}
}
