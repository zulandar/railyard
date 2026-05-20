package pluginhost

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/zulandar/railyard/internal/events"
	"github.com/zulandar/railyard/pkg/plugin"
)

// TestLifecycleLogLinesSucceedingPlugin captures slog output through the
// host's complete Init→Start→Stop walk and asserts the three spec §4
// lines fire with the expected plugin name and daemon/subscription
// counts.
//
// The plugin registers one daemon and two subscriptions inside Start so
// the "started" line can quote concrete non-zero numbers — the same
// shape an enterprise plugin (e.g. trainmaster) would produce.
func TestLifecycleLogLinesSucceedingPlugin(t *testing.T) {
	bh, _ := newBufHandler()
	prev := slog.Default()
	slog.SetDefault(slog.New(bh))
	t.Cleanup(func() { slog.SetDefault(prev) })

	bus := events.NewBus()
	host := NewHost(Dependencies{Bus: bus})

	p := &daemonPlugin{
		name: "loglife",
		onStart: func(ctx context.Context, h plugin.Host) error {
			h.RunDaemon("worker", func(ctx context.Context) error {
				<-ctx.Done()
				return nil
			})
			// Two subscriptions so the "started" log line has a non-zero
			// count to verify against. Subscribing to two distinct
			// topics ensures both Subscribe calls hit the tracking path.
			h.Subscribe(plugin.CarCreated, func(plugin.EventType, any) {})
			h.Subscribe(plugin.YardPaused, func(plugin.EventType, any) {})
			return nil
		},
	}
	host.Register(p)
	host.Init(context.Background())
	host.Start(context.Background())
	host.Stop(context.Background())

	got := bh.snapshot()

	// Init log: spec format "plugin <name>: init".
	if !strings.Contains(got, "plugin loglife: init") {
		t.Errorf("missing init log line\nfull output:\n%s", got)
	}
	// Started log: counts measured AFTER Start returned, so we expect
	// "(1 daemons, 2 subscriptions)" verbatim.
	if !strings.Contains(got, "plugin loglife: started (1 daemons, 2 subscriptions)") {
		t.Errorf("missing started log line with expected counts\nfull output:\n%s", got)
	}
	// Stopped log.
	if !strings.Contains(got, "plugin loglife: stopped") {
		t.Errorf("missing stopped log line\nfull output:\n%s", got)
	}
}

// TestLifecycleLogLinesZeroCounts verifies the "started" log reports
// "(0 daemons, 0 subscriptions)" when a plugin registers neither — the
// shape that the OSS-style plugin (e.g. one that only listens via
// snapshots) would emit.
func TestLifecycleLogLinesZeroCounts(t *testing.T) {
	bh, _ := newBufHandler()
	prev := slog.Default()
	slog.SetDefault(slog.New(bh))
	t.Cleanup(func() { slog.SetDefault(prev) })

	host := NewHost(Dependencies{})
	host.Register(&daemonPlugin{name: "quiet"})
	host.Init(context.Background())
	host.Start(context.Background())
	host.Stop(context.Background())

	got := bh.snapshot()
	if !strings.Contains(got, "plugin quiet: started (0 daemons, 0 subscriptions)") {
		t.Errorf("missing zero-counts started log\nfull output:\n%s", got)
	}
}

// TestLifecycleLogInitFailureFormat verifies the spec §4 init-failure
// WARN line matches the exact format the spec calls out:
//
//	plugin trainmaster: init failed — skipped (endpoint required when enabled)
//
// Captured via slog so we can assert the message text — the WARN
// attributes also carry an error= field but the message is the
// human-readable contract.
func TestLifecycleLogInitFailureFormat(t *testing.T) {
	bh, _ := newBufHandler()
	prev := slog.Default()
	slog.SetDefault(slog.New(bh))
	t.Cleanup(func() { slog.SetDefault(prev) })

	host := NewHost(Dependencies{})
	host.Register(&daemonPlugin{
		name: "broken",
		onInit: func(ctx context.Context, h plugin.Host) error {
			return errors.New("endpoint required when enabled")
		},
	})
	host.Init(context.Background())

	got := bh.snapshot()
	if !strings.Contains(got, "plugin broken: init failed — skipped (endpoint required when enabled)") {
		t.Errorf("missing init-failure log with spec format\nfull output:\n%s", got)
	}
}

// TestSubscriptionCounting drives the tracking layer directly: subscribe
// twice via the per-plugin view, assert the gauge reads 2, unsubscribe
// once, assert it drops to 1, then unsubscribe again twice (the SDK
// documents double-unsubscribe as safe) and assert the gauge stays at 0
// rather than going negative.
func TestSubscriptionCounting(t *testing.T) {
	bus := events.NewBus()
	host := NewHost(Dependencies{Bus: bus})

	var unsubA, unsubB plugin.Unsubscribe

	p := &daemonPlugin{
		name: "counted",
		onStart: func(ctx context.Context, h plugin.Host) error {
			unsubA = h.Subscribe(plugin.CarCreated, func(plugin.EventType, any) {})
			unsubB = h.Subscribe(plugin.YardPaused, func(plugin.EventType, any) {})
			return nil
		},
	}
	host.Register(p)
	host.Init(context.Background())
	host.Start(context.Background())
	t.Cleanup(func() { host.Stop(context.Background()) })

	if d, s := host.countsFor("counted"); s != 2 {
		t.Errorf("subscriptions = %d, want 2 (daemons=%d)", s, d)
	}

	unsubA()
	if _, s := host.countsFor("counted"); s != 1 {
		t.Errorf("after first unsubscribe, subscriptions = %d, want 1", s)
	}

	unsubB()
	if _, s := host.countsFor("counted"); s != 0 {
		t.Errorf("after second unsubscribe, subscriptions = %d, want 0", s)
	}

	// Double-unsubscribe (documented safe). The counter must not go
	// negative — countsFor returns int, and the clamp inside
	// decrSubscription is the load-bearing guard.
	unsubA()
	unsubB()
	if _, s := host.countsFor("counted"); s != 0 {
		t.Errorf("after double unsubscribe, subscriptions = %d, want 0 (no underflow)", s)
	}
}

// TestSubscriptionCountingNilBus exercises the no-bus path: the tracking
// layer must still increment and decrement so test contexts that omit a
// bus produce a balanced gauge.
func TestSubscriptionCountingNilBus(t *testing.T) {
	host := NewHost(Dependencies{})

	var unsub plugin.Unsubscribe
	p := &daemonPlugin{
		name: "nobus",
		onStart: func(ctx context.Context, h plugin.Host) error {
			unsub = h.Subscribe(plugin.CarCreated, func(plugin.EventType, any) {})
			return nil
		},
	}
	host.Register(p)
	host.Init(context.Background())
	host.Start(context.Background())
	t.Cleanup(func() { host.Stop(context.Background()) })

	if _, s := host.countsFor("nobus"); s != 1 {
		t.Errorf("subscriptions = %d, want 1 (nil bus path)", s)
	}
	unsub()
	if _, s := host.countsFor("nobus"); s != 0 {
		t.Errorf("after unsubscribe (nil bus), subscriptions = %d, want 0", s)
	}
}
