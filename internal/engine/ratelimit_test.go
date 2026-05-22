package engine

import (
	"sync"
	"testing"
	"time"
)

// Captured 429 samples (verbatim from railyard-qf1 fixtures).

const sampleOpenRouter429 = `{"error":{"message":"Provider returned error","code":429,"metadata":{"raw":"meta-llama/llama-3.3-70b-instruct:free is temporarily rate-limited upstream. Please retry shortly, or add your own key to accumulate your rate limits: https://openrouter.ai/settings/integrations","provider_name":"Venice","is_byok":false,"retry_after_seconds":25,"retry_after_seconds_raw":24.704,"headers":{"Retry-After":"25"}}},"user_id":"user_xyz"}`

const sampleAnthropic429 = `{"type":"error","error":{"type":"rate_limit_error","message":"You have exceeded the rate limit. Please try again later."}}`

const sampleHTTP429 = "HTTP 429 Too Many Requests\nRetry-After: 60\n"

// recvSignal returns the next signal on the channel or fails the test if
// none arrives within the given timeout.
func recvSignal(t *testing.T, ch <-chan RateLimitSignal, timeout time.Duration) RateLimitSignal {
	t.Helper()
	select {
	case sig := <-ch:
		return sig
	case <-time.After(timeout):
		t.Fatalf("timed out waiting for rate-limit signal after %s", timeout)
		return RateLimitSignal{}
	}
}

// assertNoSignal fails the test if a signal is received within timeout.
func assertNoSignal(t *testing.T, ch <-chan RateLimitSignal, timeout time.Duration) {
	t.Helper()
	select {
	case sig := <-ch:
		t.Fatalf("unexpected rate-limit signal received: %+v", sig)
	case <-time.After(timeout):
		// expected: no signal
	}
}

func TestRateLimitDetector_AnthropicNative(t *testing.T) {
	d := NewRateLimitDetector()
	d.observeOutput([]byte(sampleAnthropic429))

	sig := recvSignal(t, d.Signaled(), time.Second)
	if sig.Source != "anthropic" {
		t.Errorf("Source = %q, want %q", sig.Source, "anthropic")
	}
	if sig.RetryAfter != 0 {
		t.Errorf("RetryAfter = %v, want 0", sig.RetryAfter)
	}
}

func TestRateLimitDetector_OpenRouterWrapped(t *testing.T) {
	d := NewRateLimitDetector()
	d.observeOutput([]byte(sampleOpenRouter429))

	sig := recvSignal(t, d.Signaled(), time.Second)
	if sig.Source != "openrouter" {
		t.Errorf("Source = %q, want %q", sig.Source, "openrouter")
	}
	if sig.RetryAfter != 25*time.Second {
		t.Errorf("RetryAfter = %v, want 25s", sig.RetryAfter)
	}
}

func TestRateLimitDetector_OpenRouterChunked(t *testing.T) {
	// Split the sample at an internal JSON boundary so that neither chunk on
	// its own contains both `"code":429` and "rate-limited". The first chunk
	// stops mid-metadata, before the "raw" key is fully emitted.
	const splitAt = 60 // arbitrary mid-JSON offset; "rate-limited" appears later
	if splitAt >= len(sampleOpenRouter429) {
		t.Fatalf("test fixture too short to split at %d", splitAt)
	}
	first := sampleOpenRouter429[:splitAt]
	second := sampleOpenRouter429[splitAt:]

	d := NewRateLimitDetector()

	// First chunk alone must not fire.
	d.observeOutput([]byte(first))
	select {
	case sig := <-d.Signaled():
		t.Fatalf("detector fired prematurely on partial chunk: %+v", sig)
	default:
	}

	// Second chunk completes the pattern.
	d.observeOutput([]byte(second))
	sig := recvSignal(t, d.Signaled(), time.Second)
	if sig.Source != "openrouter" {
		t.Errorf("Source = %q, want %q", sig.Source, "openrouter")
	}
	if sig.RetryAfter != 25*time.Second {
		t.Errorf("RetryAfter = %v, want 25s", sig.RetryAfter)
	}
}

func TestRateLimitDetector_HTTPGeneric(t *testing.T) {
	d := NewRateLimitDetector()
	d.observeOutput([]byte(sampleHTTP429))

	sig := recvSignal(t, d.Signaled(), time.Second)
	if sig.Source != "http" {
		t.Errorf("Source = %q, want %q", sig.Source, "http")
	}
	if sig.RetryAfter != 60*time.Second {
		t.Errorf("RetryAfter = %v, want 60s", sig.RetryAfter)
	}
}

func TestRateLimitDetector_FalsePositives(t *testing.T) {
	benign := []string{
		"rate of change is high\n",
		"line 429 of foo.go: undefined symbol\n",
		"connecting to port 8429 for telemetry\n",
		"the 429th element was nil\n",
	}

	for _, s := range benign {
		t.Run(s, func(t *testing.T) {
			d := NewRateLimitDetector()
			d.observeOutput([]byte(s))
			assertNoSignal(t, d.Signaled(), 50*time.Millisecond)
		})
	}
}

func TestRateLimitDetector_ConcurrentSafe(t *testing.T) {
	d := NewRateLimitDetector()

	const numWorkers = 8
	const writesPerWorker = 50

	// Pre-build a benign chunk and a single real-match chunk. One worker
	// injects the real sample; the rest pound the detector with benign data
	// to exercise the lock under contention.
	benign := []byte("some benign output line that should not match anything\n")

	var wg sync.WaitGroup
	wg.Add(numWorkers)
	for i := 0; i < numWorkers; i++ {
		go func(idx int) {
			defer wg.Done()
			for j := 0; j < writesPerWorker; j++ {
				d.observeOutput(benign)
			}
			if idx == 0 {
				// One worker injects the real rate-limit sample.
				d.observeOutput([]byte(sampleAnthropic429))
			}
		}(i)
	}
	wg.Wait()

	sig := recvSignal(t, d.Signaled(), time.Second)
	if sig.Source != "anthropic" {
		t.Errorf("Source = %q, want %q", sig.Source, "anthropic")
	}
}

func TestRateLimitDetector_FiresOnce(t *testing.T) {
	d := NewRateLimitDetector()

	d.observeOutput([]byte(sampleOpenRouter429))
	sig := recvSignal(t, d.Signaled(), time.Second)
	if sig.Source != "openrouter" {
		t.Fatalf("first observation: Source = %q, want %q", sig.Source, "openrouter")
	}

	// Second observation of the same sample must not produce another signal.
	d.observeOutput([]byte(sampleOpenRouter429))
	assertNoSignal(t, d.Signaled(), 50*time.Millisecond)
}
