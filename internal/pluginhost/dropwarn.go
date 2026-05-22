package pluginhost

import (
	"log/slog"
	"sync"
	"time"
)

// dropWarnInterval is the minimum gap between successive WARN logs for
// the same (plugin, topic) pair. Operators want to hear about drops but
// not have their logs flooded when a slow plugin is shedding hundreds
// of events per second; a 1s rollup is the compromise.
const dropWarnInterval = time.Second

// dropWarner emits a WARN log on Subscribe-stream drops, throttled to
// at most 1 WARN per dropWarnInterval per (plugin, topic) pair. Drops
// that arrive within the throttled window are counted and rolled up
// into the next WARN.
//
// The throttler is concurrency-safe; recordDrop may be called from any
// number of goroutines (in practice: one per allowed topic on each
// Subscribe stream's bus-callback goroutine).
//
// Construct with newDropWarner. The zero value is not usable.
type dropWarner struct {
	logger *slog.Logger

	// clock is injectable for tests. Production code passes time.Now.
	clock func() time.Time

	mu    sync.Mutex
	state map[dropKey]*dropEntry
}

// dropKey identifies a drop bucket. Both fields are required so a
// single host-level warner can serve multiple plugins (currently the
// host constructs one warner per Subscribe stream, but the key keeps
// the API host-agnostic).
type dropKey struct {
	plugin string
	topic  string
}

// dropEntry tracks per-key throttle state.
//
//   - lastWarnAt is the timestamp of the most recent emitted WARN. The
//     zero value means "no WARN emitted yet" — the next recordDrop will
//     emit immediately.
//   - pendingCount is the number of drops accumulated since lastWarnAt
//     that have NOT yet been reflected in a WARN. recordDrop bumps it
//     by 1 every call; the count is reset to 0 when a WARN fires.
type dropEntry struct {
	lastWarnAt   time.Time
	pendingCount int
}

// newDropWarner constructs a dropWarner backed by the given logger and
// using time.Now as the clock. Tests use newDropWarnerWithClock to
// inject a deterministic clock.
func newDropWarner(logger *slog.Logger) *dropWarner {
	return newDropWarnerWithClock(logger, time.Now)
}

// newDropWarnerWithClock is the test seam; production callers should
// use newDropWarner.
func newDropWarnerWithClock(logger *slog.Logger, clock func() time.Time) *dropWarner {
	if logger == nil {
		logger = slog.Default()
	}
	if clock == nil {
		clock = time.Now
	}
	return &dropWarner{
		logger: logger,
		clock:  clock,
		state:  make(map[dropKey]*dropEntry),
	}
}

// recordDrop is called every time the Subscribe stream drops an event
// for (plugin, topic). It emits a WARN immediately if dropWarnInterval
// has elapsed since the last WARN for this key (or no WARN has fired
// yet); otherwise it just accumulates the drop into pendingCount for
// inclusion in the next WARN.
//
// The emitted record always reports dropped_in_interval >= 1: it is
// the count of drops covered by THIS warning (the current drop plus
// any swallowed by the throttle since the last warning).
func (w *dropWarner) recordDrop(plugin, topic string) {
	key := dropKey{plugin: plugin, topic: topic}
	now := w.clock()

	w.mu.Lock()
	entry, ok := w.state[key]
	if !ok {
		entry = &dropEntry{}
		w.state[key] = entry
	}
	// The current drop counts toward the next WARN regardless of
	// whether we emit now.
	entry.pendingCount++

	// First WARN for this key always fires immediately (lastWarnAt is
	// the zero value). Subsequent WARNs require dropWarnInterval to
	// have elapsed.
	shouldEmit := entry.lastWarnAt.IsZero() || now.Sub(entry.lastWarnAt) >= dropWarnInterval

	var (
		droppedInInterval int
		sinceLastWarn     time.Duration
	)
	if shouldEmit {
		droppedInInterval = entry.pendingCount
		if !entry.lastWarnAt.IsZero() {
			sinceLastWarn = now.Sub(entry.lastWarnAt)
		}
		entry.pendingCount = 0
		entry.lastWarnAt = now
	}
	w.mu.Unlock()

	if !shouldEmit {
		return
	}

	// Log outside the lock — slog handlers may do arbitrary I/O and we
	// do not want to serialize unrelated keys behind a single writer.
	attrs := []any{
		slog.String("plugin", plugin),
		slog.String("topic", topic),
		slog.Int("dropped_in_interval", droppedInInterval),
	}
	if sinceLastWarn > 0 {
		attrs = append(attrs, slog.Duration("since_last_warn", sinceLastWarn))
	}
	w.logger.Warn("pluginhost: subscribe stream dropped event(s)", attrs...)
}
