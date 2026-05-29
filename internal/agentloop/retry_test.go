package agentloop

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestRateLimitBackoff(t *testing.T) {
	base := 5 * time.Second
	max := 60 * time.Second
	tests := []struct {
		name       string
		retryAfter time.Duration
		attempt    int
		want       time.Duration
	}{
		{"honors retry-after under cap", 27 * time.Second, 0, 27 * time.Second},
		{"backoff base when no retry-after", 0, 0, base},
		{"backoff doubles per attempt", 0, 1, 2 * base},
		{"backoff doubles again", 0, 2, 4 * base},
		{"retry-after capped", 5 * time.Minute, 0, max},
		{"backoff capped", 0, 10, max},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := RateLimitBackoff(tt.retryAfter, tt.attempt, base, max); got != tt.want {
				t.Errorf("RateLimitBackoff(%v, %d) = %v, want %v", tt.retryAfter, tt.attempt, got, tt.want)
			}
		})
	}
}

func noSleep(context.Context, time.Duration) error { return nil }

func TestRunWithRateLimitRetry_RetriesThenSucceeds(t *testing.T) {
	calls := 0
	var attempts []int
	err := RunWithRateLimitRetry(context.Background(), RateLimitRetryConfig{Sleep: noSleep}, func(attempt int) error {
		attempts = append(attempts, attempt)
		calls++
		if calls <= 1 {
			return &RateLimitError{RetryAfter: time.Second, Message: "slow down"}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("err = %v, want nil after a transient 429 is retried", err)
	}
	if calls != 2 {
		t.Errorf("calls = %d, want 2 (one 429 + one success)", calls)
	}
	if len(attempts) != 2 || attempts[0] != 0 || attempts[1] != 1 {
		t.Errorf("attempts = %v, want [0 1] (0-indexed, incrementing)", attempts)
	}
}

func TestRunWithRateLimitRetry_Exhausted(t *testing.T) {
	calls := 0
	sentinel := &RateLimitError{Message: "always"}
	err := RunWithRateLimitRetry(context.Background(), RateLimitRetryConfig{Sleep: noSleep}, func(int) error {
		calls++
		return sentinel
	})
	if !errors.Is(err, sentinel) {
		t.Errorf("err = %v, want the rate-limit error after exhaustion", err)
	}
	if want := 1 + DefaultRateLimitMaxRetries; calls != want {
		t.Errorf("calls = %d, want %d (initial + %d retries)", calls, want, DefaultRateLimitMaxRetries)
	}
}

func TestRunWithRateLimitRetry_NonRateLimitErrorReturnsImmediately(t *testing.T) {
	calls := 0
	want := &APIError{StatusCode: 500, Message: "boom"}
	err := RunWithRateLimitRetry(context.Background(), RateLimitRetryConfig{Sleep: noSleep}, func(int) error {
		calls++
		return want
	})
	if !errors.Is(err, want) {
		t.Errorf("err = %v, want the API error", err)
	}
	if calls != 1 {
		t.Errorf("calls = %d, want 1 (non-429 must not retry)", calls)
	}
}

func TestRunWithRateLimitRetry_ContextCancelDuringBackoff(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	calls := 0
	cancelSleep := func(context.Context, time.Duration) error {
		cancel()
		return context.Canceled
	}
	err := RunWithRateLimitRetry(ctx, RateLimitRetryConfig{Sleep: cancelSleep}, func(int) error {
		calls++
		return &RateLimitError{Message: "slow down"}
	})
	var rle *RateLimitError
	if !errors.As(err, &rle) {
		t.Errorf("err = %v, want the rate-limit error surfaced on ctx cancel", err)
	}
	if calls != 1 {
		t.Errorf("calls = %d, want 1 (cancel during first backoff stops retries)", calls)
	}
}

func TestRunWithRateLimitRetry_OnRetryInvoked(t *testing.T) {
	var waits []time.Duration
	calls := 0
	_ = RunWithRateLimitRetry(context.Background(), RateLimitRetryConfig{
		Sleep:   noSleep,
		OnRetry: func(_, _ int, wait time.Duration) { waits = append(waits, wait) },
	}, func(int) error {
		calls++
		return &RateLimitError{Message: "no retry-after"} // falls back to backoff
	})
	// DefaultRateLimitMaxRetries pauses, each with the exponential backoff.
	if len(waits) != DefaultRateLimitMaxRetries {
		t.Fatalf("OnRetry calls = %d, want %d", len(waits), DefaultRateLimitMaxRetries)
	}
	if waits[0] != defaultRateLimitBaseWait || waits[1] != 2*defaultRateLimitBaseWait {
		t.Errorf("backoff waits = %v, want [%v %v]", waits, defaultRateLimitBaseWait, 2*defaultRateLimitBaseWait)
	}
}
