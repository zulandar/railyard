package pluginhost

import (
	"sync"
	"time"
)

// crashBudgetWindow is the sliding-window length used by the crash budget.
// Four crashes inside any 60-second window flips the plugin into a
// permanent-disabled state (see [crashBudgetThreshold] for the bound).
const crashBudgetWindow = 60 * time.Second

// crashBudgetThreshold is the number of crashes within [crashBudgetWindow]
// that triggers a permanent disable. The brief is "3-in-60s budget; the
// 4th flips": up to 3 crashes inside any 60s window is recoverable, the
// 4th is fatal-for-lifetime. We model that as `count >= threshold` where
// threshold = 4.
const crashBudgetThreshold = 4

// crashBudget tracks plugin crashes over a sliding 60-second window. A
// plugin permanently disables on the 4th crash within any 60-second
// window. The counter resets on a clean (planned) shutdown.
//
// crashBudget is goroutine-safe — recordCrash, reset, and inspect each
// take an internal mutex. The clock is injectable so restart-loop tests
// don't have to rely on real time.
type crashBudget struct {
	window    time.Duration
	threshold int
	// clock returns the current time. Always non-nil; defaults to
	// time.Now in [newCrashBudget].
	clock func() time.Time

	mu sync.Mutex
	// crashes holds the wall-clock times of every crash inside the
	// current window, sorted ascending. Times older than `window` are
	// pruned at the head on every recordCrash and on every inspect.
	crashes []time.Time
}

// newCrashBudget constructs a fresh crashBudget with the default
// 60s/4-flip configuration. clock may be nil — time.Now is used in that
// case.
func newCrashBudget(clock func() time.Time) *crashBudget {
	if clock == nil {
		clock = time.Now
	}
	return &crashBudget{
		window:    crashBudgetWindow,
		threshold: crashBudgetThreshold,
		clock:     clock,
	}
}

// recordCrash registers one crash at the current clock time. It returns
// the live count in the sliding window AFTER pruning expired entries and
// AFTER appending the new entry, and whether that count has reached the
// permanent-disable threshold.
//
// `exceeded` is true on the call that flips the budget AND on every
// subsequent call — the caller is expected to act on the first true and
// stop driving the supervisor.
func (b *crashBudget) recordCrash() (count int, exceeded bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	now := b.clock()
	b.pruneLocked(now)
	b.crashes = append(b.crashes, now)
	count = len(b.crashes)
	exceeded = count >= b.threshold
	return count, exceeded
}

// reset clears every recorded crash. Called on planned shutdown and on
// successful Stop so a future railyard restart starts with a fresh
// budget.
func (b *crashBudget) reset() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.crashes = nil
}

// firstCrash returns the timestamp of the oldest crash currently inside
// the window, or the zero time if the window is empty. Used for the
// `first_crash_at` log attribute on permanent-disable.
func (b *crashBudget) firstCrash() time.Time {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.pruneLocked(b.clock())
	if len(b.crashes) == 0 {
		return time.Time{}
	}
	return b.crashes[0]
}

// count returns the number of crashes currently inside the window. Used
// by tests and by the permanent-disable log line.
func (b *crashBudget) count() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.pruneLocked(b.clock())
	return len(b.crashes)
}

// pruneLocked drops every crash strictly older than `now - window`. The
// caller must hold b.mu.
func (b *crashBudget) pruneLocked(now time.Time) {
	if len(b.crashes) == 0 {
		return
	}
	cutoff := now.Add(-b.window)
	// crashes is sorted ascending; find the first index strictly inside
	// the (cutoff, now] half-open window and slice the head off. A crash
	// at exactly `cutoff` is OUT — i.e. the window is "the last
	// `window` seconds, exclusive at the trailing edge". The spec line
	// "3 crashes at t=0..10s, 1 crash at t=70s → window has slid; the
	// t=0..10s crashes are out" hinges on this: at t+70, the t+10
	// crash (60s old exactly) is out.
	idx := 0
	for idx < len(b.crashes) && !b.crashes[idx].After(cutoff) {
		idx++
	}
	if idx > 0 {
		b.crashes = append(b.crashes[:0], b.crashes[idx:]...)
	}
}

// backoffSchedule returns the restart backoff for the n-th consecutive
// crash since the last successful start. n starts at 0 for the first
// crash: 250ms, 500ms, 1s, 2s, 4s, 5s, 5s, ... — exponential with a 5s
// ceiling.
//
// We compute the value by left-shifting 250ms — the ceiling guards both
// the obvious "uncapped" runaway AND the int64 overflow if n is large.
func backoffSchedule(n int) time.Duration {
	const base = 250 * time.Millisecond
	const ceiling = 5 * time.Second
	if n < 0 {
		n = 0
	}
	// 5s / 250ms = 20; 2^5 = 32 > 20, so anything n>=5 saturates.
	if n >= 5 {
		return ceiling
	}
	d := base << uint(n)
	if d > ceiling {
		return ceiling
	}
	return d
}
