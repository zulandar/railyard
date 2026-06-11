package pluginhost

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/zulandar/railyard/internal/config"
	protov1 "github.com/zulandar/railyard/pkg/plugin/proto/v1"
)

// fakeHealthRPC is a stub PluginServiceClient that only implements the
// Health RPC. Behaviour is controlled per-test via the resp/err fields.
type fakeHealthRPC struct {
	protov1.PluginServiceClient

	mu    sync.Mutex
	resp  *protov1.HealthResponse
	err   error
	delay time.Duration
	calls int
}

func (f *fakeHealthRPC) Health(ctx context.Context, _ *protov1.HealthRequest, _ ...grpc.CallOption) (*protov1.HealthResponse, error) {
	f.mu.Lock()
	f.calls++
	delay := f.delay
	resp := f.resp
	err := f.err
	f.mu.Unlock()
	if delay > 0 {
		select {
		case <-time.After(delay):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return resp, err
}

func (f *fakeHealthRPC) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

// newHealthFixtureHost builds a minimal host with a single launched
// plugin backed by the supplied RPC stub. clock is deterministic.
func newHealthFixtureHost(rpc protov1.PluginServiceClient) (*Host, *launchedPlugin) {
	h := &Host{
		clock:         func() time.Time { return time.Unix(1_700_000_100, 0) },
		launched:      map[string]*launchedPlugin{},
		subscriptions: map[string]int{},
		initFailures:  map[string]initFailure{},
		disabled:      map[string]*disabledPlugin{},
		pluginCmds:    map[string]string{},
		shutdownCh:    make(chan struct{}),
	}
	lp := &launchedPlugin{name: "p", pluginRPC: rpc}
	h.launched["p"] = lp
	return h, lp
}

// TestPollHealthOnceMapsOK verifies a HEALTH_OK probe lands on the
// launched plugin as "ok" with its message and the stubbed clock's
// timestamp (railyard-77h.12).
func TestPollHealthOnceMapsOK(t *testing.T) {
	rpc := &fakeHealthRPC{resp: &protov1.HealthResponse{
		State:   protov1.HealthState_HEALTH_STATE_OK,
		Message: "all good",
	}}
	h, lp := newHealthFixtureHost(rpc)

	h.pollHealthOnce(context.Background())

	h.mu.RLock()
	defer h.mu.RUnlock()
	if lp.healthValue != healthValueOK {
		t.Errorf("healthValue = %q, want %q", lp.healthValue, healthValueOK)
	}
	if lp.healthMessage != "all good" {
		t.Errorf("healthMessage = %q, want %q", lp.healthMessage, "all good")
	}
	if !lp.healthCheckedAt.Equal(time.Unix(1_700_000_100, 0)) {
		t.Errorf("healthCheckedAt = %v, want stubbed clock", lp.healthCheckedAt)
	}
}

// TestPollHealthOnceUnimplementedIsNotAnError verifies a plugin whose
// Health RPC returns codes.Unimplemented is shown as "n/a" rather than
// an error or degraded (railyard-77h.12).
func TestPollHealthOnceUnimplementedIsNotAnError(t *testing.T) {
	rpc := &fakeHealthRPC{err: status.Error(codes.Unimplemented, "no HealthReporter")}
	h, lp := newHealthFixtureHost(rpc)

	h.pollHealthOnce(context.Background())

	h.mu.RLock()
	defer h.mu.RUnlock()
	if lp.healthValue != healthValueNA {
		t.Errorf("healthValue = %q, want %q", lp.healthValue, healthValueNA)
	}
	if lp.healthMessage != "" {
		t.Errorf("healthMessage = %q, want empty for n/a", lp.healthMessage)
	}
	if lp.healthCheckedAt.IsZero() {
		t.Error("healthCheckedAt should be set even for n/a")
	}
}

// TestPollHealthOnceErrorMapsDegraded verifies a transport error maps to
// "degraded" with the error text as the message (railyard-77h.12).
func TestPollHealthOnceErrorMapsDegraded(t *testing.T) {
	rpc := &fakeHealthRPC{err: errors.New("dial fail")}
	h, lp := newHealthFixtureHost(rpc)

	h.pollHealthOnce(context.Background())

	h.mu.RLock()
	defer h.mu.RUnlock()
	if lp.healthValue != healthValueDegraded {
		t.Errorf("healthValue = %q, want %q", lp.healthValue, healthValueDegraded)
	}
	if lp.healthMessage == "" {
		t.Error("healthMessage should carry the error text")
	}
}

// TestHealthPollLoopHonorsIntervalAndStopsCleanly drives the real poll
// loop with a stubbed clock-independent ticker interval and asserts both
// that it polls on the interval AND that it exits cleanly when shutdownCh
// closes (joined by supervisorWG) — no goroutine leak (railyard-77h.12).
func TestHealthPollLoopHonorsIntervalAndStopsCleanly(t *testing.T) {
	rpc := &fakeHealthRPC{resp: &protov1.HealthResponse{State: protov1.HealthState_HEALTH_STATE_OK}}
	h, _ := newHealthFixtureHost(rpc)

	// Tiny interval so the test observes several ticks fast without a
	// real 30s wait. The loop ticks on this interval deterministically.
	interval := 5 * time.Millisecond
	h.supervisorWG.Add(1)
	go h.healthPollLoop(context.Background(), interval)

	// Wait until we have observed at least 2 polls (proves the loop is
	// firing on its interval).
	deadline := time.Now().Add(2 * time.Second)
	for rpc.callCount() < 2 {
		if time.Now().After(deadline) {
			t.Fatalf("poll loop did not fire at least twice; calls=%d", rpc.callCount())
		}
		time.Sleep(interval)
	}

	// Close shutdownCh and assert the loop joins promptly (no leak).
	close(h.shutdownCh)
	done := make(chan struct{})
	go func() {
		h.supervisorWG.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("healthPollLoop did not exit after shutdownCh closed (goroutine leak)")
	}
}

// TestHealthPollLoopZeroIntervalUsesDefault verifies the loop falls back
// to a positive interval when handed a non-positive one and still stops
// cleanly (railyard-77h.12).
func TestHealthPollLoopZeroIntervalUsesDefault(t *testing.T) {
	rpc := &fakeHealthRPC{resp: &protov1.HealthResponse{State: protov1.HealthState_HEALTH_STATE_OK}}
	h, _ := newHealthFixtureHost(rpc)

	h.supervisorWG.Add(1)
	go h.healthPollLoop(context.Background(), 0)

	// With a default (30s) interval the loop performs an immediate first
	// poll, then blocks on the ticker. We only assert the one immediate
	// poll plus a clean stop.
	deadline := time.Now().Add(2 * time.Second)
	for rpc.callCount() < 1 {
		if time.Now().After(deadline) {
			t.Fatal("poll loop did not perform an immediate first poll")
		}
		time.Sleep(5 * time.Millisecond)
	}

	close(h.shutdownCh)
	done := make(chan struct{})
	go func() {
		h.supervisorWG.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("healthPollLoop did not exit (goroutine leak)")
	}
}

// TestHealthIntervalFromConfig is a thin guard that Start wires the
// configured interval through. We assert the resolved duration matches
// config, leaving the loop behaviour to the loop tests above.
func TestHealthIntervalFromConfig(t *testing.T) {
	cfg := &config.Config{Plugins: config.PluginsConfig{HealthIntervalSec: 7}}
	if got := cfg.Plugins.HealthInterval(); got != 7*time.Second {
		t.Fatalf("HealthInterval() = %v, want 7s", got)
	}
}
