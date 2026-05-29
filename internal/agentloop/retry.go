package agentloop

import (
	"context"
	"errors"
	"time"
)

// Default rate-limit retry tuning, shared by the native-loop consumers that run
// a single conversational turn at a time (telegraph chat dispatch and the
// interactive `ry dispatch` REPL) so they all recover from upstream 429s the
// same way. The engine car-building path does NOT use this — it has its own
// outer pause-and-retry wrapper (pkg/cli/engine.go) that also converts
// exhaustion into a blocked car. (railyard-08t)
const (
	// DefaultRateLimitMaxRetries is the number of retries after the first
	// attempt before a turn gives up.
	DefaultRateLimitMaxRetries = 2
	// defaultRateLimitBaseWait is the backoff base used when a 429 carries no
	// Retry-After hint: 5s, 10s, ...
	defaultRateLimitBaseWait = 5 * time.Second
	// defaultRateLimitMaxWait caps a single pause so a bogus or hostile
	// Retry-After can't stall a turn for an unbounded time.
	defaultRateLimitMaxWait = 60 * time.Second
)

// RateLimitRetryConfig tunes RunWithRateLimitRetry. Zero-valued fields take the
// package defaults, so the common case is RunWithRateLimitRetry(ctx, RateLimitRetryConfig{}, run).
type RateLimitRetryConfig struct {
	// MaxRetries bounds retries after the first attempt; <=0 uses the default.
	MaxRetries int
	// BaseWait is the exponential-backoff base; <=0 uses the default.
	BaseWait time.Duration
	// MaxWait caps a single pause; <=0 uses the default.
	MaxWait time.Duration
	// Sleep waits d honoring ctx cancellation; nil uses a context-aware timer.
	// Tests inject a no-op to avoid waiting on real backoff.
	Sleep func(ctx context.Context, d time.Duration) error
	// OnRetry, if set, is invoked just before each backoff pause (attempt is
	// 0-indexed) — wire it to logging or a user-facing "retrying in Ns" notice.
	OnRetry func(attempt, maxRetries int, wait time.Duration)
}

func (c RateLimitRetryConfig) maxRetries() int {
	if c.MaxRetries > 0 {
		return c.MaxRetries
	}
	return DefaultRateLimitMaxRetries
}

func (c RateLimitRetryConfig) baseWait() time.Duration {
	if c.BaseWait > 0 {
		return c.BaseWait
	}
	return defaultRateLimitBaseWait
}

func (c RateLimitRetryConfig) maxWait() time.Duration {
	if c.MaxWait > 0 {
		return c.MaxWait
	}
	return defaultRateLimitMaxWait
}

// RunWithRateLimitRetry invokes run repeatedly, pausing and retrying only on
// *RateLimitError. attempt is 0 on the first call so callers can vary their
// input across attempts — e.g. pass empty input on retries so a re-run doesn't
// re-append the user message to loop history. Non-429 errors and success return
// immediately. After MaxRetries is exhausted the last error is returned so the
// caller can surface it. A context error during a backoff pause also returns the
// last (rate-limit) error, which is more informative than the bare ctx error.
func RunWithRateLimitRetry(ctx context.Context, cfg RateLimitRetryConfig, run func(attempt int) error) error {
	var lastErr error
	for attempt := 0; ; attempt++ {
		err := run(attempt)
		if err == nil {
			return nil
		}
		lastErr = err

		var rle *RateLimitError
		if !errors.As(err, &rle) || attempt >= cfg.maxRetries() {
			return lastErr
		}

		wait := RateLimitBackoff(rle.RetryAfter, attempt, cfg.baseWait(), cfg.maxWait())
		if cfg.OnRetry != nil {
			cfg.OnRetry(attempt, cfg.maxRetries(), wait)
		}
		if err := sleepWith(ctx, cfg.Sleep, wait); err != nil {
			return lastErr
		}
	}
}

// RateLimitBackoff returns the pause before the next retry (0-indexed attempt):
// the upstream Retry-After when present, otherwise exponential backoff from
// base (base, 2*base, 4*base, ...), capped at max.
func RateLimitBackoff(retryAfter time.Duration, attempt int, base, max time.Duration) time.Duration {
	wait := retryAfter
	if wait <= 0 {
		wait = base << attempt
	}
	if wait > max {
		wait = max
	}
	return wait
}

// sleepWith waits d using the injected sleep func, or a context-aware timer.
func sleepWith(ctx context.Context, sleep func(context.Context, time.Duration) error, d time.Duration) error {
	if sleep != nil {
		return sleep(ctx, d)
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}
