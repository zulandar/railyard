package engine

import (
	"regexp"
	"strconv"
	"sync"
	"time"
)

// rateLimitBufferSize bounds the rolling buffer used to match patterns that
// may straddle multiple Write() chunks (notably OpenRouter's wrapped 429,
// which is a single JSON object that stream-json output can split across
// chunks). 4096 bytes is enough to capture the full OpenRouter error object
// while keeping memory use predictable.
const rateLimitBufferSize = 4096

// RateLimitSignal indicates the engine subprocess hit an upstream rate limit.
// RetryAfter is the parsed Retry-After duration; zero means no value was
// provided in the upstream response.
type RateLimitSignal struct {
	Source     string        // "anthropic" | "openrouter" | "http"
	RetryAfter time.Duration // 0 if not provided
}

// Detection patterns. All are precompiled at package init.
//
// Anthropic native error shape:
//
//	{"type":"error","error":{"type":"rate_limit_error",...}}
//
// We anchor on the JSON field name to avoid matching arbitrary occurrences
// of the substring "rate_limit_error" outside an error object.
var anthropicRateLimitRe = regexp.MustCompile(`"type"\s*:\s*"rate_limit_error"`)

// OpenRouter wrapped 429: a single JSON object containing both `"code":429`
// and the phrase "rate-limited" in metadata.raw. We match the two structural
// markers independently against the rolling buffer (handles chunked JSON)
// and require both to be present.
var openRouterCode429Re = regexp.MustCompile(`"code"\s*:\s*429\b`)
var openRouterRateLimitedRe = regexp.MustCompile(`rate-limited`)

// OpenRouter retry_after_seconds: capture the integer value (the float form
// `retry_after_seconds_raw` is ignored — we use the rounded integer).
var openRouterRetryAfterRe = regexp.MustCompile(`"retry_after_seconds"\s*:\s*(\d+)`)

// Generic HTTP 429 status. Matches forms like:
//
//	"HTTP 429 Too Many Requests"
//	"HTTP/1.1 429"
//	"status: 429"
//
// Case-insensitive. The structural anchoring (HTTP-context word) avoids
// matching incidental "429" occurrences (line numbers, port numbers, etc.).
var httpStatus429Re = regexp.MustCompile(`(?i)\bHTTP(?:/\d\.\d)?\s+429\b|\bstatus\s*:\s*429\b`)

// Generic Retry-After header line.
var httpRetryAfterRe = regexp.MustCompile(`(?i)retry-after\s*:\s*(\d+)`)

// RateLimitDetector scans subprocess output for upstream rate-limit signals
// and emits a single RateLimitSignal on its Signaled() channel. After firing
// once, the detector suppresses further signals until Reset() is called —
// this mirrors StallDetector's "stopped" semantics and lets the engine's
// retry loop (railyard-qf1.2 / F2) re-arm the detector on each retry attempt.
type RateLimitDetector struct {
	mu       sync.Mutex
	buf      []byte // rolling buffer of recent output (max rateLimitBufferSize)
	stopped  bool
	signalCh chan RateLimitSignal
}

// NewRateLimitDetector constructs a detector with a size-1 buffered signal
// channel. Wire observeOutput into a logWriter.onWrite callback to start
// receiving output. The detector does not run any background goroutine —
// all work happens synchronously inside observeOutput.
func NewRateLimitDetector() *RateLimitDetector {
	return &RateLimitDetector{
		buf:      make([]byte, 0, rateLimitBufferSize),
		signalCh: make(chan RateLimitSignal, 1),
	}
}

// Signaled returns a channel that receives a RateLimitSignal the first time
// a rate-limit pattern is matched. After firing, the channel will not
// receive again until Reset() is called.
func (d *RateLimitDetector) Signaled() <-chan RateLimitSignal {
	return d.signalCh
}

// Stop prevents the detector from emitting further signals. Idempotent.
func (d *RateLimitDetector) Stop() {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.stopped = true
}

// Reset re-arms the detector after a previous fire. The retry loop in F2
// calls this before each retry attempt so a subsequent rate limit can be
// observed and acted on. Reset also clears the rolling buffer so stale
// output from the previous attempt does not retrigger the match.
func (d *RateLimitDetector) Reset() {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.stopped = false
	d.buf = d.buf[:0]
}

// observeOutput appends p to the rolling buffer and scans for rate-limit
// markers. Safe for concurrent invocation from stdout + stderr writers.
// Channel sends are non-blocking; if the consumer has not drained the
// previous signal, additional matches are dropped (the detector is also
// marked stopped, so this is moot in practice).
func (d *RateLimitDetector) observeOutput(p []byte) {
	if len(p) == 0 {
		return
	}

	d.mu.Lock()
	defer d.mu.Unlock()

	if d.stopped {
		return
	}

	// Append, then trim from the front to keep the buffer bounded. We keep
	// the most recent rateLimitBufferSize bytes so that patterns spanning
	// multiple Write() calls remain visible.
	d.buf = append(d.buf, p...)
	if len(d.buf) > rateLimitBufferSize {
		// Reslice rather than reallocate; the underlying array stays bounded
		// because append grows it at most once past the limit per chunk and
		// we copy down to keep the prefix from growing without bound.
		drop := len(d.buf) - rateLimitBufferSize
		d.buf = append(d.buf[:0], d.buf[drop:]...)
	}

	// Try detectors in order of specificity: provider-native patterns first
	// (more reliable), generic HTTP last.
	if signal, ok := d.detectAnthropic(); ok {
		d.emit(signal)
		return
	}
	if signal, ok := d.detectOpenRouter(); ok {
		d.emit(signal)
		return
	}
	if signal, ok := d.detectHTTP(); ok {
		d.emit(signal)
		return
	}
}

// detectAnthropic looks for the Anthropic native rate_limit_error shape.
// Must be called with d.mu held.
func (d *RateLimitDetector) detectAnthropic() (RateLimitSignal, bool) {
	if !anthropicRateLimitRe.Match(d.buf) {
		return RateLimitSignal{}, false
	}
	// Anthropic's rate_limit_error response does not carry a Retry-After in
	// the JSON body; the API returns it as an HTTP header which the CLI
	// generally does not surface to stdout. Leave RetryAfter zero.
	return RateLimitSignal{Source: "anthropic"}, true
}

// detectOpenRouter looks for OpenRouter's wrapped 429 shape. Requires both
// `"code":429` and the substring "rate-limited" to be present in the buffer
// — this combination is highly specific to the OpenRouter error shape and
// avoids matching plain 429 status lines. Must be called with d.mu held.
func (d *RateLimitDetector) detectOpenRouter() (RateLimitSignal, bool) {
	if !openRouterCode429Re.Match(d.buf) {
		return RateLimitSignal{}, false
	}
	if !openRouterRateLimitedRe.Match(d.buf) {
		return RateLimitSignal{}, false
	}
	signal := RateLimitSignal{Source: "openrouter"}
	if m := openRouterRetryAfterRe.FindSubmatch(d.buf); m != nil {
		if secs, err := strconv.Atoi(string(m[1])); err == nil && secs > 0 {
			signal.RetryAfter = time.Duration(secs) * time.Second
		}
	}
	return signal, true
}

// detectHTTP matches a generic HTTP 429 status line. Optionally captures a
// Retry-After value if one appears anywhere in the buffer. Must be called
// with d.mu held.
func (d *RateLimitDetector) detectHTTP() (RateLimitSignal, bool) {
	if !httpStatus429Re.Match(d.buf) {
		return RateLimitSignal{}, false
	}
	signal := RateLimitSignal{Source: "http"}
	if m := httpRetryAfterRe.FindSubmatch(d.buf); m != nil {
		if secs, err := strconv.Atoi(string(m[1])); err == nil && secs > 0 {
			signal.RetryAfter = time.Duration(secs) * time.Second
		}
	}
	return signal, true
}

// emit sends a signal (non-blocking) and marks the detector stopped. Must
// be called with d.mu held.
func (d *RateLimitDetector) emit(signal RateLimitSignal) {
	if d.stopped {
		return
	}
	d.stopped = true
	select {
	case d.signalCh <- signal:
	default:
	}
}
