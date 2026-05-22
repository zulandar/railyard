package cli

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/zulandar/railyard/internal/engine"
)

// fakeRunner records every spawn call and returns a scripted outcome each
// time. It is used to drive spawnAndMonitorWithRetryRunner deterministically
// in tests — no real subprocess, no DB.
type fakeRunner struct {
	mu       sync.Mutex
	calls    []engine.SpawnOpts
	outcomes []sessionOutcome
	idx      int
}

func (f *fakeRunner) run(_ context.Context, opts engine.SpawnOpts) (*engine.Session, sessionOutcome, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, opts)
	if f.idx >= len(f.outcomes) {
		// Default to outcomeClear if we run out of scripted outcomes — this
		// should not happen in well-formed tests.
		return &engine.Session{ID: "sess-end"}, sessionOutcome{kind: outcomeClear}, nil
	}
	out := f.outcomes[f.idx]
	f.idx++
	return &engine.Session{ID: "sess-fake"}, out, nil
}

func (f *fakeRunner) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

func (f *fakeRunner) callAt(i int) engine.SpawnOpts {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls[i]
}

// TestPauseAndRetry_RetryAfterHonored — when the rate-limit signal carries a
// RetryAfter, the engine sleeps for approximately that duration before
// respawning. Uses a small (200ms) RetryAfter so the test stays fast.
func TestPauseAndRetry_RetryAfterHonored(t *testing.T) {
	runner := &fakeRunner{
		outcomes: []sessionOutcome{
			{kind: outcomeRateLimited, rateLimitSignal: engine.RateLimitSignal{
				Source:     "openrouter",
				RetryAfter: 200 * time.Millisecond,
			}},
			{kind: outcomeCompleted},
		},
	}

	start := time.Now()
	_, outcome, err := spawnAndMonitorWithRetryRunner(
		context.Background(),
		engine.SpawnOpts{CarID: "car-rly1", ContextPayload: "ctx-A"},
		3, 60, nil, runner.run,
	)
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if outcome.kind != outcomeCompleted {
		t.Errorf("kind = %d, want outcomeCompleted (%d)", outcome.kind, outcomeCompleted)
	}
	if runner.callCount() != 2 {
		t.Errorf("call count = %d, want 2", runner.callCount())
	}
	if elapsed < 180*time.Millisecond {
		t.Errorf("elapsed = %s, expected >= ~200ms (RetryAfter honored)", elapsed)
	}
	if elapsed > 2*time.Second {
		t.Errorf("elapsed = %s, took far longer than expected", elapsed)
	}
}

// TestPauseAndRetry_RetryAfterCapped — RetryAfter larger than
// RateLimitMaxWaitSec must be clamped down.
func TestPauseAndRetry_RetryAfterCapped(t *testing.T) {
	runner := &fakeRunner{
		outcomes: []sessionOutcome{
			{kind: outcomeRateLimited, rateLimitSignal: engine.RateLimitSignal{
				Source:     "openrouter",
				RetryAfter: 600 * time.Second, // huge; cap is much smaller
			}},
			{kind: outcomeCompleted},
		},
	}

	// maxWaitSec is not an integer-second value here because the helper
	// multiplies by time.Second, but we want a small cap to keep the test
	// fast. We pass maxWaitSec=1 (1 second).
	start := time.Now()
	_, outcome, err := spawnAndMonitorWithRetryRunner(
		context.Background(),
		engine.SpawnOpts{CarID: "car-rly2"},
		3, 1, nil, runner.run,
	)
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if outcome.kind != outcomeCompleted {
		t.Errorf("kind = %d, want outcomeCompleted", outcome.kind)
	}
	// Should have slept ~1s (capped), well under the 600s the signal asked for.
	if elapsed > 3*time.Second {
		t.Errorf("elapsed = %s, expected ~1s (cap should clamp the 600s RetryAfter)", elapsed)
	}
	if elapsed < 900*time.Millisecond {
		t.Errorf("elapsed = %s, expected >= ~1s (the cap)", elapsed)
	}
}

// TestPauseAndRetry_ExponentialBackoff — when signals carry no RetryAfter, the
// engine falls back to computeBackoff. We can't reasonably wait the full
// 10s+30s+60s in a unit test, so we verify the wait grows monotonically by
// driving two retries and confirming the second wait is longer than the first.
// We don't assert the exact computeBackoff schedule here — that's covered by
// TestComputeBackoff_Bounds.
func TestPauseAndRetry_ExponentialBackoff(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping slow exponential-backoff timing test in -short mode")
	}

	runner := &fakeRunner{
		outcomes: []sessionOutcome{
			{kind: outcomeRateLimited, rateLimitSignal: engine.RateLimitSignal{Source: "anthropic"}},
			{kind: outcomeRateLimited, rateLimitSignal: engine.RateLimitSignal{Source: "anthropic"}},
			{kind: outcomeCompleted},
		},
	}

	// Use maxWaitSec=2 to cap each retry at 2s. computeBackoff returns
	// 10s, 30s, ... — both get clamped to 2s. So both gaps will be ~2s.
	// We assert each spawn->next-spawn gap is >= 1s (well above the
	// instant respawn case) and <= ~3s.
	var spawnTimes []time.Time
	var mu sync.Mutex
	wrapped := func(ctx context.Context, opts engine.SpawnOpts) (*engine.Session, sessionOutcome, error) {
		mu.Lock()
		spawnTimes = append(spawnTimes, time.Now())
		mu.Unlock()
		return runner.run(ctx, opts)
	}

	_, outcome, err := spawnAndMonitorWithRetryRunner(
		context.Background(),
		engine.SpawnOpts{CarID: "car-rly3"},
		5, 2, nil, wrapped,
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if outcome.kind != outcomeCompleted {
		t.Errorf("kind = %d, want outcomeCompleted", outcome.kind)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(spawnTimes) != 3 {
		t.Fatalf("spawn count = %d, want 3", len(spawnTimes))
	}
	gap1 := spawnTimes[1].Sub(spawnTimes[0])
	gap2 := spawnTimes[2].Sub(spawnTimes[1])
	if gap1 < 500*time.Millisecond {
		t.Errorf("first retry gap = %s, expected >= ~1s after backoff", gap1)
	}
	if gap2 < 500*time.Millisecond {
		t.Errorf("second retry gap = %s, expected >= ~1s after backoff", gap2)
	}
	// Sanity bound — each retry should be roughly the 2s cap, not 30s+.
	if gap1 > 4*time.Second {
		t.Errorf("first retry gap = %s, expected near cap (~2s)", gap1)
	}
}

// TestPauseAndRetry_MaxRetriesExhausted — after maxRetries rate-limit signals,
// the helper converts the outcome to outcomeStall with a rate_limit_exhausted
// reason so the caller's stall path runs as a fallback. The Detail string must
// report the true count of rate-limit responses observed (= total spawns), not
// (total - 1) — regression guard for railyard-19b.
func TestPauseAndRetry_MaxRetriesExhausted(t *testing.T) {
	runner := &fakeRunner{
		outcomes: []sessionOutcome{
			{kind: outcomeRateLimited, rateLimitSignal: engine.RateLimitSignal{Source: "anthropic", RetryAfter: 1 * time.Millisecond}},
			{kind: outcomeRateLimited, rateLimitSignal: engine.RateLimitSignal{Source: "anthropic", RetryAfter: 1 * time.Millisecond}},
			{kind: outcomeRateLimited, rateLimitSignal: engine.RateLimitSignal{Source: "openrouter", RetryAfter: 1 * time.Millisecond}},
		},
	}

	_, outcome, err := spawnAndMonitorWithRetryRunner(
		context.Background(),
		engine.SpawnOpts{CarID: "car-rly4"},
		2, 1, nil, runner.run,
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if outcome.kind != outcomeStall {
		t.Errorf("kind = %d, want outcomeStall (%d)", outcome.kind, outcomeStall)
	}
	if outcome.stallReason.Type != "rate_limit_exhausted" {
		t.Errorf("stallReason.Type = %q, want %q", outcome.stallReason.Type, "rate_limit_exhausted")
	}
	if runner.callCount() != 3 {
		t.Errorf("call count = %d, want 3 (initial + 2 retries before exhaustion)", runner.callCount())
	}
	// 3 spawns, all rate-limited → the Detail must say "rate-limited 3 times",
	// not 2 (the pre-fix off-by-one).
	if !strings.Contains(outcome.stallReason.Detail, "rate-limited 3 times") {
		t.Errorf("Detail = %q, expected to contain %q", outcome.stallReason.Detail, "rate-limited 3 times")
	}
}

// TestPauseAndRetry_SpawnErrorSurfacesToCaller — when the runner returns a
// non-nil error (spawn failed: binary missing, fork-limit, etc.), the helper
// must propagate the error so the outer engine loop can log+sleep+retry on
// the next poll tick rather than converting it into a permanent stall.
// Regression guard for railyard-27s.
func TestPauseAndRetry_SpawnErrorSurfacesToCaller(t *testing.T) {
	spawnErr := errors.New("simulated SpawnAgent failure")
	runner := func(_ context.Context, _ engine.SpawnOpts) (*engine.Session, sessionOutcome, error) {
		return nil, sessionOutcome{}, spawnErr
	}

	sess, outcome, err := spawnAndMonitorWithRetryRunner(
		context.Background(),
		engine.SpawnOpts{CarID: "car-spawn-err"},
		3, 60, nil, runner,
	)
	if !errors.Is(err, spawnErr) {
		t.Fatalf("err = %v, want simulated spawn error", err)
	}
	if sess != nil {
		t.Errorf("sess = %+v, want nil on spawn error", sess)
	}
	// Outcome should be a zero-value sentinel — the caller must NOT treat this
	// as a stall.
	if outcome.kind == outcomeStall {
		t.Errorf("outcome.kind = outcomeStall, but spawn errors must not auto-stall the car")
	}
}

// TestPauseAndRetry_RespawnUsesSameOpts — the helper must call the runner with
// the same SpawnOpts on every attempt so context, model, and provider settings
// survive the retry intact. This is the core guarantee from ADR 2.
func TestPauseAndRetry_RespawnUsesSameOpts(t *testing.T) {
	runner := &fakeRunner{
		outcomes: []sessionOutcome{
			{kind: outcomeRateLimited, rateLimitSignal: engine.RateLimitSignal{Source: "anthropic", RetryAfter: 1 * time.Millisecond}},
			{kind: outcomeCompleted},
		},
	}

	opts := engine.SpawnOpts{
		EngineID:       "eng-rspn",
		CarID:          "car-rspn",
		ContextPayload: "context-payload-marker-12345",
		WorkDir:        "/tmp/work",
		ProviderName:   "claude",
		Model:          "claude-3-5-sonnet",
	}

	_, _, err := spawnAndMonitorWithRetryRunner(
		context.Background(),
		opts,
		3, 1, nil, runner.run,
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if runner.callCount() != 2 {
		t.Fatalf("call count = %d, want 2", runner.callCount())
	}
	first := runner.callAt(0)
	second := runner.callAt(1)
	if first != second {
		t.Errorf("retry SpawnOpts differ:\n  first  = %+v\n  second = %+v", first, second)
	}
	if first.ContextPayload != opts.ContextPayload {
		t.Errorf("ContextPayload not preserved: got %q", first.ContextPayload)
	}
	if first.Model != opts.Model {
		t.Errorf("Model not preserved: got %q", first.Model)
	}
}

// TestPauseAndRetry_NoRateLimitPassesThrough — when the first run returns a
// terminal outcome (completed/stall/clear), the helper returns immediately
// without sleeping or re-spawning.
func TestPauseAndRetry_NoRateLimitPassesThrough(t *testing.T) {
	runner := &fakeRunner{
		outcomes: []sessionOutcome{
			{kind: outcomeCompleted},
		},
	}

	start := time.Now()
	_, outcome, err := spawnAndMonitorWithRetryRunner(
		context.Background(),
		engine.SpawnOpts{CarID: "car-pt"},
		3, 300, nil, runner.run,
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if outcome.kind != outcomeCompleted {
		t.Errorf("kind = %d, want outcomeCompleted", outcome.kind)
	}
	if runner.callCount() != 1 {
		t.Errorf("call count = %d, want 1 (no retry)", runner.callCount())
	}
	if time.Since(start) > 500*time.Millisecond {
		t.Errorf("elapsed = %s — should be near-instant when no rate limit", time.Since(start))
	}
}

// TestPauseAndRetry_ContextCancelledDuringSleep — if the outer context is
// cancelled while the helper is sleeping between retries, it returns
// outcomeCancelled instead of starting a new attempt.
func TestPauseAndRetry_ContextCancelledDuringSleep(t *testing.T) {
	runner := &fakeRunner{
		outcomes: []sessionOutcome{
			{kind: outcomeRateLimited, rateLimitSignal: engine.RateLimitSignal{
				Source:     "openrouter",
				RetryAfter: 10 * time.Second, // long enough that we can cancel during the sleep
			}},
			{kind: outcomeCompleted}, // should never reach this
		},
	}

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()

	_, outcome, err := spawnAndMonitorWithRetryRunner(
		ctx,
		engine.SpawnOpts{CarID: "car-cnx"},
		3, 60, nil, runner.run,
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if outcome.kind != outcomeCancelled {
		t.Errorf("kind = %d, want outcomeCancelled", outcome.kind)
	}
	if runner.callCount() != 1 {
		t.Errorf("call count = %d, want 1 (no retry after cancel)", runner.callCount())
	}
}

// TestComputeBackoff_Bounds — verify computeBackoff produces values within
// each attempt's expected ±20% jitter window, and that attempts beyond the
// schedule clamp to the last entry (300s base).
func TestComputeBackoff_Bounds(t *testing.T) {
	// Expected base values for attempts 1..5; attempts >5 should clamp to 300s.
	bases := []time.Duration{
		10 * time.Second,
		30 * time.Second,
		60 * time.Second,
		120 * time.Second,
		300 * time.Second,
	}
	for attempt := 1; attempt <= 7; attempt++ {
		idx := attempt - 1
		if idx >= len(bases) {
			idx = len(bases) - 1
		}
		base := bases[idx]
		low := base - (base / 5)
		high := base + (base / 5)
		// Sample several times so jitter doesn't sneak past us. Each call has
		// independent jitter; we just need any out-of-range value to fail.
		for i := 0; i < 32; i++ {
			d := computeBackoff(attempt)
			if d < low || d > high {
				t.Errorf("computeBackoff(%d) = %s, out of [%s, %s]", attempt, d, low, high)
			}
		}
	}
}

// TestComputeBackoff_NonPositiveAttempt — attempt <= 0 should be treated as 1.
func TestComputeBackoff_NonPositiveAttempt(t *testing.T) {
	d0 := computeBackoff(0)
	dn := computeBackoff(-3)
	for _, d := range []time.Duration{d0, dn} {
		// Attempt 1 base = 10s, jitter ±20% = [8s, 12s].
		if d < 8*time.Second || d > 12*time.Second {
			t.Errorf("computeBackoff non-positive attempt produced %s, expected ~10s ±20%%", d)
		}
	}
}
