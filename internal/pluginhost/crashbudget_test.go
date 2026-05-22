package pluginhost

import (
	"testing"
	"time"
)

// TestCrashBudget_ThreeWithinTenSecondsNoFlip is the lower bound of the
// budget: 3 crashes inside the window leave the plugin recoverable. Only
// the 4th flips.
func TestCrashBudget_ThreeWithinTenSecondsNoFlip(t *testing.T) {
	clk := newFakeClock(time.Unix(1_700_000_000, 0))
	b := newCrashBudget(clk.Now)

	// t+0s
	if cnt, ex := b.recordCrash(); ex || cnt != 1 {
		t.Fatalf("crash 1: cnt=%d ex=%v want 1,false", cnt, ex)
	}
	clk.Advance(5 * time.Second)
	if cnt, ex := b.recordCrash(); ex || cnt != 2 {
		t.Fatalf("crash 2: cnt=%d ex=%v want 2,false", cnt, ex)
	}
	clk.Advance(5 * time.Second)
	if cnt, ex := b.recordCrash(); ex || cnt != 3 {
		t.Fatalf("crash 3: cnt=%d ex=%v want 3,false", cnt, ex)
	}
}

// TestCrashBudget_FourthWithinWindowFlips covers the spec acceptance line:
// 3 crashes at t=0..10s plus a 4th at t=30s (still inside the 60s window)
// flips the budget.
func TestCrashBudget_FourthWithinWindowFlips(t *testing.T) {
	clk := newFakeClock(time.Unix(1_700_000_000, 0))
	b := newCrashBudget(clk.Now)

	b.recordCrash()
	clk.Advance(5 * time.Second)
	b.recordCrash()
	clk.Advance(5 * time.Second)
	b.recordCrash()

	// Advance well inside the window (20s later, still <60s from the
	// first crash) and record the 4th.
	clk.Advance(20 * time.Second)
	cnt, ex := b.recordCrash()
	if !ex {
		t.Fatalf("4th crash inside window should flip; got cnt=%d ex=%v", cnt, ex)
	}
	if cnt != 4 {
		t.Fatalf("4th crash count = %d, want 4", cnt)
	}
}

// TestCrashBudget_WindowSlides covers the spec's "3 crashes at t=0..10s,
// 1 crash at t=70s — window has slid, t=0..10s crashes are out of scope,
// so the 4th is allowed."
func TestCrashBudget_WindowSlides(t *testing.T) {
	clk := newFakeClock(time.Unix(1_700_000_000, 0))
	b := newCrashBudget(clk.Now)

	b.recordCrash()               // t+0
	clk.Advance(5 * time.Second)  // t+5
	b.recordCrash()               //
	clk.Advance(5 * time.Second)  // t+10
	b.recordCrash()               //
	clk.Advance(60 * time.Second) // t+70
	cnt, ex := b.recordCrash()
	if ex {
		t.Fatalf("4th crash at t+70s should not flip (window slid); got cnt=%d ex=%v", cnt, ex)
	}
	if cnt != 1 {
		t.Fatalf("after window slide, in-window count should be 1, got %d", cnt)
	}
}

// TestCrashBudget_Reset clears the window entirely.
func TestCrashBudget_Reset(t *testing.T) {
	clk := newFakeClock(time.Unix(1_700_000_000, 0))
	b := newCrashBudget(clk.Now)

	b.recordCrash()
	b.recordCrash()
	b.recordCrash()
	if b.count() != 3 {
		t.Fatalf("count before reset = %d, want 3", b.count())
	}
	b.reset()
	if b.count() != 0 {
		t.Fatalf("count after reset = %d, want 0", b.count())
	}
	// Subsequent record starts fresh: 3 more crashes do not flip.
	if _, ex := b.recordCrash(); ex {
		t.Fatal("first crash after reset should not flip")
	}
	if _, ex := b.recordCrash(); ex {
		t.Fatal("second crash after reset should not flip")
	}
	if _, ex := b.recordCrash(); ex {
		t.Fatal("third crash after reset should not flip")
	}
}

// TestCrashBudget_FirstCrash returns the oldest in-window timestamp,
// which is what the permanent-disable log line surfaces as
// first_crash_at.
func TestCrashBudget_FirstCrash(t *testing.T) {
	base := time.Unix(1_700_000_000, 0)
	clk := newFakeClock(base)
	b := newCrashBudget(clk.Now)

	if !b.firstCrash().IsZero() {
		t.Fatalf("firstCrash on empty budget = %v, want zero", b.firstCrash())
	}

	b.recordCrash() // base
	clk.Advance(20 * time.Second)
	b.recordCrash() // base+20s
	got := b.firstCrash()
	if !got.Equal(base) {
		t.Fatalf("firstCrash = %v, want %v", got, base)
	}

	// Slide the window past the first crash — first should now be the
	// second one.
	clk.Advance(50 * time.Second) // total t+70 from base; window cutoff = t+10
	got = b.firstCrash()
	wantSecond := base.Add(20 * time.Second)
	if !got.Equal(wantSecond) {
		t.Fatalf("firstCrash after slide = %v, want %v", got, wantSecond)
	}
}

// TestBackoffSchedule verifies the spec'd schedule:
// 250ms, 500ms, 1s, 2s, 4s, 5s, 5s, ...
func TestBackoffSchedule(t *testing.T) {
	cases := []struct {
		n    int
		want time.Duration
	}{
		{0, 250 * time.Millisecond},
		{1, 500 * time.Millisecond},
		{2, 1 * time.Second},
		{3, 2 * time.Second},
		{4, 4 * time.Second},
		{5, 5 * time.Second},
		{6, 5 * time.Second},
		{20, 5 * time.Second},
		{-1, 250 * time.Millisecond},
	}
	for _, c := range cases {
		got := backoffSchedule(c.n)
		if got != c.want {
			t.Errorf("backoffSchedule(%d) = %v, want %v", c.n, got, c.want)
		}
	}
}
