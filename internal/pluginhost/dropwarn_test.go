package pluginhost

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// newJSONLogger returns a slog.Logger that writes JSON records into the
// supplied buffer. Tests use JSON output because each record is one
// line of structured data and the fields are trivial to assert on.
func newJSONLogger(buf *bytes.Buffer) *slog.Logger {
	return slog.New(slog.NewJSONHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
}

// decodeRecords splits the buffer on newlines and JSON-decodes each
// non-empty line. Useful for asserting per-record fields.
func decodeRecords(t *testing.T, buf *bytes.Buffer) []map[string]any {
	t.Helper()
	out := make([]map[string]any, 0)
	for _, line := range strings.Split(strings.TrimRight(buf.String(), "\n"), "\n") {
		if line == "" {
			continue
		}
		var rec map[string]any
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			t.Fatalf("decoding log line %q: %v", line, err)
		}
		out = append(out, rec)
	}
	return out
}

// fakeClock is a deterministic clock for tests. Advance() moves time
// forward; Now() returns the current time without advancing.
type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func newFakeClock(start time.Time) *fakeClock {
	return &fakeClock{now: start}
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

// TestDropWarner_SingleDropEmitsImmediately verifies the first drop on
// a fresh key produces a WARN with dropped_in_interval=1.
func TestDropWarner_SingleDropEmitsImmediately(t *testing.T) {
	var buf bytes.Buffer
	clock := newFakeClock(time.Unix(1_700_000_000, 0))
	w := newDropWarnerWithClock(newJSONLogger(&buf), clock.Now)

	w.recordDrop("trainmaster", "CarStatusChanged")

	records := decodeRecords(t, &buf)
	if len(records) != 1 {
		t.Fatalf("got %d records, want 1: %s", len(records), buf.String())
	}
	r := records[0]
	if r["level"] != "WARN" {
		t.Errorf("level = %v, want WARN", r["level"])
	}
	if r["plugin"] != "trainmaster" {
		t.Errorf("plugin = %v, want trainmaster", r["plugin"])
	}
	if r["topic"] != "CarStatusChanged" {
		t.Errorf("topic = %v, want CarStatusChanged", r["topic"])
	}
	if got, want := r["dropped_in_interval"], float64(1); got != want {
		t.Errorf("dropped_in_interval = %v, want %v", got, want)
	}
	// First WARN has no since_last_warn attribute (zero duration is
	// suppressed in the formatter).
	if _, ok := r["since_last_warn"]; ok {
		t.Errorf("first WARN should not carry since_last_warn; got %v", r["since_last_warn"])
	}
}

// TestDropWarner_ThrottlesWithinInterval verifies a second drop within
// the throttle window is rolled into the first WARN's count (the WARN
// for drop #2 is suppressed; #2 is accounted for in the next WARN
// after the throttle window elapses).
func TestDropWarner_ThrottlesWithinInterval(t *testing.T) {
	var buf bytes.Buffer
	clock := newFakeClock(time.Unix(1_700_000_000, 0))
	w := newDropWarnerWithClock(newJSONLogger(&buf), clock.Now)

	// t=0: WARN with count=1.
	w.recordDrop("p1", "T1")
	// t=100ms: throttled — no WARN, pending becomes 1.
	clock.Advance(100 * time.Millisecond)
	w.recordDrop("p1", "T1")

	records := decodeRecords(t, &buf)
	if len(records) != 1 {
		t.Fatalf("got %d records, want exactly 1 (second drop must be throttled): %s",
			len(records), buf.String())
	}

	// Now cross the throttle boundary — the next drop should emit a
	// WARN that accounts for BOTH the swallowed drop AND itself
	// (dropped_in_interval = 2).
	clock.Advance(900 * time.Millisecond) // total elapsed since first: 1s
	w.recordDrop("p1", "T1")

	records = decodeRecords(t, &buf)
	if len(records) != 2 {
		t.Fatalf("got %d records, want 2 after the throttle window: %s",
			len(records), buf.String())
	}
	if got, want := records[1]["dropped_in_interval"], float64(2); got != want {
		t.Errorf("second WARN dropped_in_interval = %v, want %v (1 swallowed + 1 current)",
			got, want)
	}
}

// TestDropWarner_MultipleIntervals exercises the "drops at t=0, 500ms,
// 2000ms" scenario from the spec: 2 WARNs total, the second covering
// the intervening drop.
func TestDropWarner_MultipleIntervals(t *testing.T) {
	var buf bytes.Buffer
	clock := newFakeClock(time.Unix(1_700_000_000, 0))
	w := newDropWarnerWithClock(newJSONLogger(&buf), clock.Now)

	// t=0: WARN #1 with count=1.
	w.recordDrop("p1", "T1")
	// t=500ms: throttled.
	clock.Advance(500 * time.Millisecond)
	w.recordDrop("p1", "T1")
	// t=2000ms: WARN #2 with count=2 (the swallowed 500ms drop + this one).
	clock.Advance(1500 * time.Millisecond)
	w.recordDrop("p1", "T1")

	records := decodeRecords(t, &buf)
	if len(records) != 2 {
		t.Fatalf("got %d records, want 2: %s", len(records), buf.String())
	}
	if got, want := records[0]["dropped_in_interval"], float64(1); got != want {
		t.Errorf("WARN[0] dropped_in_interval = %v, want %v", got, want)
	}
	if got, want := records[1]["dropped_in_interval"], float64(2); got != want {
		t.Errorf("WARN[1] dropped_in_interval = %v, want %v", got, want)
	}
	// WARN #2 should include since_last_warn ≈ 2s.
	if rawSince, ok := records[1]["since_last_warn"]; !ok {
		t.Errorf("WARN[1] missing since_last_warn: %v", records[1])
	} else {
		// slog formats Duration as a nanosecond integer in JSON output.
		switch v := rawSince.(type) {
		case float64:
			if time.Duration(v) != 2*time.Second {
				t.Errorf("since_last_warn = %v, want 2s", time.Duration(v))
			}
		default:
			t.Errorf("since_last_warn type = %T, want float64", rawSince)
		}
	}
}

// TestDropWarner_IndependentKeys verifies two different (plugin, topic)
// pairs throttle independently.
func TestDropWarner_IndependentKeys(t *testing.T) {
	var buf bytes.Buffer
	clock := newFakeClock(time.Unix(1_700_000_000, 0))
	w := newDropWarnerWithClock(newJSONLogger(&buf), clock.Now)

	w.recordDrop("p1", "T1")
	w.recordDrop("p2", "T1") // different plugin
	w.recordDrop("p1", "T2") // different topic
	w.recordDrop("p2", "T2") // different both

	records := decodeRecords(t, &buf)
	if len(records) != 4 {
		t.Fatalf("got %d records, want 4 (each unique key emits independently): %s",
			len(records), buf.String())
	}
	// Each record should be dropped_in_interval=1 — none share a key.
	for i, r := range records {
		if got, want := r["dropped_in_interval"], float64(1); got != want {
			t.Errorf("records[%d] dropped_in_interval = %v, want %v", i, got, want)
		}
	}
}

// TestDropWarner_ConcurrentRecordDrop drives many goroutines through
// recordDrop on the same key and verifies that:
//
//   - There are no data races (run with -race).
//   - The number of emitted WARN records is bounded (each WARN
//     advances the throttle window; with a fixed clock all but the
//     first are throttled).
//   - The sum of dropped_in_interval across all records equals the
//     total number of recordDrop calls.
func TestDropWarner_ConcurrentRecordDrop(t *testing.T) {
	var buf bytes.Buffer
	clock := newFakeClock(time.Unix(1_700_000_000, 0))
	w := newDropWarnerWithClock(newJSONLogger(&buf), clock.Now)

	const goroutines = 32
	const dropsPerGoroutine = 100
	const totalDrops = goroutines * dropsPerGoroutine

	var emitted atomic.Int64
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for g := 0; g < goroutines; g++ {
		go func() {
			defer wg.Done()
			for i := 0; i < dropsPerGoroutine; i++ {
				w.recordDrop("p1", "T1")
				emitted.Add(1)
			}
		}()
	}
	wg.Wait()

	if got := emitted.Load(); got != totalDrops {
		t.Fatalf("emitted=%d, want %d (recordDrop is non-blocking)", got, totalDrops)
	}

	// All drops happen at the same fake time → only the first WARN
	// fires; the remaining pendingCount has not yet been flushed.
	records := decodeRecords(t, &buf)
	if len(records) < 1 {
		t.Fatalf("got %d WARN records, want at least 1", len(records))
	}
	if len(records) > 1 {
		t.Fatalf("got %d WARN records, want exactly 1 at a fixed clock (throttling broken)", len(records))
	}

	// Advance past the throttle window and emit one more drop. It
	// should account for everything that was swallowed plus itself.
	clock.Advance(dropWarnInterval + 10*time.Millisecond)
	w.recordDrop("p1", "T1")

	records = decodeRecords(t, &buf)
	if len(records) != 2 {
		t.Fatalf("after window+1: got %d records, want 2", len(records))
	}
	var sum int
	for _, r := range records {
		v, ok := r["dropped_in_interval"].(float64)
		if !ok {
			t.Fatalf("dropped_in_interval not numeric: %v", r)
		}
		sum += int(v)
	}
	if want := totalDrops + 1; sum != want {
		t.Errorf("sum of dropped_in_interval = %d, want %d (total recordDrop calls)", sum, want)
	}
}

// TestDropWarner_NilLoggerSafe protects against an accidentally-nil
// logger — newDropWarner should fall back to slog.Default().
func TestDropWarner_NilLoggerSafe(t *testing.T) {
	w := newDropWarner(nil)
	// Must not panic.
	w.recordDrop("p1", "T1")
}
