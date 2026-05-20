package pluginhost

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/zulandar/railyard/internal/config"
	"github.com/zulandar/railyard/pkg/plugin"
)

// fakePlugin is a configurable Plugin implementation that records every
// lifecycle call. Fields named *Fn override the default no-op behavior
// for the corresponding lifecycle hook.
type fakePlugin struct {
	name    string
	calls   *[]string
	initFn  func(ctx context.Context, h plugin.Host) error
	startFn func(ctx context.Context) error
	stopFn  func(ctx context.Context) error
}

func (f *fakePlugin) Name() string { return f.name }
func (f *fakePlugin) Init(ctx context.Context, h plugin.Host) error {
	*f.calls = append(*f.calls, f.name+":init")
	if f.initFn != nil {
		return f.initFn(ctx, h)
	}
	return nil
}
func (f *fakePlugin) Start(ctx context.Context) error {
	*f.calls = append(*f.calls, f.name+":start")
	if f.startFn != nil {
		return f.startFn(ctx)
	}
	return nil
}
func (f *fakePlugin) Stop(ctx context.Context) error {
	*f.calls = append(*f.calls, f.name+":stop")
	if f.stopFn != nil {
		return f.stopFn(ctx)
	}
	return nil
}

// TestLifecycleOrdering verifies Init and Start run in registration order
// and Stop runs in reverse.
func TestLifecycleOrdering(t *testing.T) {
	var calls []string
	host := NewHost(Dependencies{})
	host.Register(&fakePlugin{name: "alpha", calls: &calls})
	host.Register(&fakePlugin{name: "beta", calls: &calls})
	host.Register(&fakePlugin{name: "gamma", calls: &calls})

	host.Init(context.Background())
	host.Start(context.Background())
	host.Stop(context.Background())

	want := []string{
		"alpha:init", "beta:init", "gamma:init",
		"alpha:start", "beta:start", "gamma:start",
		"gamma:stop", "beta:stop", "alpha:stop",
	}
	if len(calls) != len(want) {
		t.Fatalf("call count = %d (%v), want %d (%v)", len(calls), calls, len(want), want)
	}
	for i := range want {
		if calls[i] != want[i] {
			t.Errorf("call[%d] = %q, want %q", i, calls[i], want[i])
		}
	}
}

// TestInitFailureIsolation verifies a plugin whose Init returns an error
// is removed from the running set, while sibling plugins continue.
func TestInitFailureIsolation(t *testing.T) {
	var calls []string
	host := NewHost(Dependencies{})
	host.Register(&fakePlugin{name: "ok-before", calls: &calls})
	host.Register(&fakePlugin{
		name:   "broken",
		calls:  &calls,
		initFn: func(ctx context.Context, h plugin.Host) error { return errors.New("nope") },
	})
	host.Register(&fakePlugin{name: "ok-after", calls: &calls})

	host.Init(context.Background())
	host.Start(context.Background())
	host.Stop(context.Background())

	// "broken" should appear in init but never start or stop.
	for _, c := range calls {
		if c == "broken:start" || c == "broken:stop" {
			t.Errorf("broken plugin should be skipped after failed init, got call %q", c)
		}
	}
	// The other two should complete the full lifecycle.
	for _, name := range []string{"ok-before:start", "ok-after:start", "ok-before:stop", "ok-after:stop"} {
		if !contains(calls, name) {
			t.Errorf("missing expected call %q (got %v)", name, calls)
		}
	}
	if names := host.Names(); len(names) != 2 {
		t.Errorf("Names() after init = %v, want 2 entries", names)
	}
}

// TestStopDrainTimeout asserts the host returns within ~5s even when a
// plugin's Stop ignores cancellation. The plugin's blocking goroutine is
// abandoned; the test only verifies the host did not hang.
func TestStopDrainTimeout(t *testing.T) {
	released := make(chan struct{})
	t.Cleanup(func() { close(released) })

	var calls []string
	host := NewHost(Dependencies{})
	host.Register(&fakePlugin{
		name:  "stubborn",
		calls: &calls,
		stopFn: func(ctx context.Context) error {
			// Block until the test cleanup closes the channel — but the
			// host should NOT wait this long.
			<-released
			return nil
		},
	})

	host.Init(context.Background())
	host.Start(context.Background())

	start := time.Now()
	host.Stop(context.Background())
	elapsed := time.Since(start)

	// Allow generous slack above the 5s drain timeout for goroutine
	// scheduling on busy CI hosts.
	if elapsed > 7*time.Second {
		t.Fatalf("Stop took %v, expected ~5s due to drain timeout", elapsed)
	}
	if elapsed < 4*time.Second {
		t.Fatalf("Stop returned in %v — drain timeout did not engage", elapsed)
	}
}

// TestYardInfoFromConfig verifies YardInfo is populated from the supplied
// config and is cached (returned verbatim on every call).
func TestYardInfoFromConfig(t *testing.T) {
	cfg := &config.Config{
		Owner:   "zulandar",
		Project: "railyard",
		Repo:    "https://github.com/zulandar/railyard",
	}
	host := NewHost(Dependencies{
		Cfg:             cfg,
		RailyardVersion: "v9.9.9",
		BuildCommit:     "deadbeef",
		BuildTime:       time.Unix(1700000000, 0).UTC(),
	})

	info := host.YardInfo()
	if info.Owner != "zulandar" {
		t.Errorf("Owner = %q, want %q", info.Owner, "zulandar")
	}
	if info.Project != "railyard" {
		t.Errorf("Project = %q, want %q", info.Project, "railyard")
	}
	if info.RepoURL != "https://github.com/zulandar/railyard" {
		t.Errorf("RepoURL = %q", info.RepoURL)
	}
	if info.RailyardVersion != "v9.9.9" {
		t.Errorf("RailyardVersion = %q, want v9.9.9", info.RailyardVersion)
	}
	if info.BuildCommit != "deadbeef" {
		t.Errorf("BuildCommit = %q", info.BuildCommit)
	}

	// Cached: a second call should return the same value.
	if host.YardInfo() != info {
		t.Errorf("YardInfo should be cached and stable across calls")
	}
}

// TestRunDaemonInvokesFn confirms the placeholder RunDaemon at least
// invokes the supplied function. The fuller behavior (panic recovery,
// drain, restart bound) is the scope of bead .3.4.
func TestRunDaemonInvokesFn(t *testing.T) {
	host := NewHost(Dependencies{})
	var invoked atomic.Bool
	done := make(chan struct{})
	host.RunDaemon("probe", func(ctx context.Context) error {
		invoked.Store(true)
		close(done)
		return nil
	})
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("RunDaemon did not invoke fn within 1s")
	}
	if !invoked.Load() {
		t.Error("daemon fn never set invoked flag")
	}
}

func contains(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}
