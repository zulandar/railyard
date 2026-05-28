package events

import (
	"bytes"
	"log/slog"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// newTestBus returns a memBus and a lockedWriter holding the slog output.
// Tests read the captured log via lw.String() — the underlying bytes.Buffer
// is guarded by the lockedWriter mutex, so concurrent slog writes and test
// reads are race-free.
func newTestBus(t *testing.T) (Bus, *lockedWriter) {
	t.Helper()
	lw := &lockedWriter{w: &bytes.Buffer{}}
	h := slog.NewTextHandler(lw, &slog.HandlerOptions{Level: slog.LevelDebug})
	logger := slog.New(h)
	return NewBusWithLogger(logger), lw
}

// lockedWriter wraps bytes.Buffer with a mutex so concurrent slog writes from
// the drain/publish paths don't race the test goroutine reading the buffer.
// Both Write and String take the same mutex.
type lockedWriter struct {
	mu sync.Mutex
	w  *bytes.Buffer
}

func (l *lockedWriter) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.w.Write(p)
}

func (l *lockedWriter) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.w.String()
}

func TestBus_DropOldestBackpressure(t *testing.T) {
	bus, lw := newTestBus(t)
	defer closeBus(t, bus)

	// Block the subscriber until we say go, so we can stuff its queue.
	gate := make(chan struct{})
	var received []int
	var mu sync.Mutex

	unsub := bus.(*memBus).SubscribeNamed("flood-sub", "flood", func(payload any) {
		<-gate
		mu.Lock()
		received = append(received, payload.(int))
		mu.Unlock()
	})
	defer unsub()

	// Publish more events than the queue can hold. With the drain goroutine
	// blocked in <-gate, evictOldest keeps the newest subscriberQueueSize
	// events in the channel.
	total := subscriberQueueSize * 3
	for i := 0; i < total; i++ {
		bus.Publish("flood", i)
	}

	// Release the handler; it now drains whatever survived eviction.
	close(gate)

	// Use unsubscribe as a deterministic barrier: it closes the channel and
	// blocks on <-sub.done, which the drain goroutine closes only after the
	// for-range over the channel has fully drained and the goroutine exits.
	// This guarantees every event still in the channel has flowed through
	// the handler before we read `received`.
	//
	// The earlier "waitFor(processed == len(received))" predicate was a
	// racy snapshot — that condition holds transiently between every
	// handler invocation, so the 2ms-poll waitFor could latch onto a
	// mid-drain moment and return while events were still in flight (see
	// railyard-a6v: CI flake at 2026-05-28 18:21Z, post-merge of #49).
	unsub()

	// expectedMax bounds delivered events: 1 in-flight first event plus the
	// queue (cap=subscriberQueueSize). Whether the in-flight slot is occupied
	// depends on drain scheduling — under tight GOMAXPROCS the drain may not
	// receive its first event until after close(gate), in which case there
	// is no in-flight slot and the queue alone (size subscriberQueueSize) is
	// delivered. Either count is correct.
	expectedMax := subscriberQueueSize + 1

	mu.Lock()
	got := append([]int(nil), received...)
	mu.Unlock()

	if len(got) > expectedMax {
		t.Fatalf("got %d events delivered, want <= %d", len(got), expectedMax)
	}
	if len(got) < 2 {
		t.Fatalf("expected at least a couple deliveries, got %d", len(got))
	}

	// (b) The newest event must have survived eviction — it was the last
	// thing sent into the queue.
	last := got[len(got)-1]
	if last != total-1 {
		t.Fatalf("newest event missing: last delivered=%d, want %d", last, total-1)
	}

	// (a) An early event (not the first, which was already in-flight) must
	// have been evicted. Pick one well into the early portion.
	dropTarget := total / 3
	for _, v := range got {
		if v == dropTarget {
			t.Fatalf("expected event %d to be evicted, but it was delivered", dropTarget)
		}
	}

	// (c) WARN log must have fired.
	logs := lw.String()
	want := `events: dropped oldest event for subscriber \"flood-sub\" on topic \"flood\"`
	if !strings.Contains(logs, want) {
		t.Fatalf("expected WARN log containing %q, got:\n%s", want, logs)
	}
	if !strings.Contains(logs, "level=WARN") {
		t.Fatalf("expected WARN level in log output, got:\n%s", logs)
	}
}

func TestBus_PanicRecovery_SinglePanic(t *testing.T) {
	bus, lw := newTestBus(t)
	defer closeBus(t, bus)

	var calls atomic.Int64
	unsub := bus.(*memBus).SubscribeNamed("panicky", "topic", func(payload any) {
		n := calls.Add(1)
		if n == 1 {
			panic("boom")
		}
	})
	defer unsub()

	bus.Publish("topic", 1)
	bus.Publish("topic", 2)

	if !waitFor(t, func() bool { return calls.Load() >= 2 }, 2*time.Second) {
		t.Fatalf("expected at least 2 handler invocations after panic recovery, got %d", calls.Load())
	}

	logs := lw.String()
	want := `events: subscriber \"panicky\" panicked on topic \"topic\"`
	if !strings.Contains(logs, want) {
		t.Fatalf("expected panic ERROR log containing %q, got:\n%s", want, logs)
	}
	if !strings.Contains(logs, "level=ERROR") {
		t.Fatalf("expected ERROR level, got:\n%s", logs)
	}
	if strings.Contains(logs, "disabled after 3 consecutive panics") {
		t.Fatalf("subscription should NOT have been disabled after a single panic, got:\n%s", logs)
	}
}

func TestBus_PanicRecovery_ThreeStrikeDisable(t *testing.T) {
	bus, lw := newTestBus(t)
	defer closeBus(t, bus)

	var calls atomic.Int64
	unsub := bus.(*memBus).SubscribeNamed("striker", "topic", func(payload any) {
		calls.Add(1)
		panic("always")
	})
	defer unsub()

	for i := 0; i < 3; i++ {
		bus.Publish("topic", i)
	}

	if !waitFor(t, func() bool { return calls.Load() == 3 }, 2*time.Second) {
		t.Fatalf("expected 3 calls before disable, got %d", calls.Load())
	}

	// Wait for the disable log to be emitted (it happens inside the deferred
	// recover, which runs after the handler returns).
	if !waitFor(t, func() bool {
		return strings.Contains(lw.String(), "disabled after 3 consecutive panics")
	}, 2*time.Second) {
		t.Fatalf("disable ERROR log never fired, got:\n%s", lw.String())
	}

	// The 4th publish must NOT invoke the handler — the subscription is disabled.
	bus.Publish("topic", 99)

	time.Sleep(50 * time.Millisecond)
	if got := calls.Load(); got != 3 {
		t.Fatalf("handler called after disable: got %d calls, want 3", got)
	}

	logs := lw.String()
	want := `events: subscriber \"striker\" disabled after 3 consecutive panics`
	if !strings.Contains(logs, want) {
		t.Fatalf("expected disable ERROR log containing %q, got:\n%s", want, logs)
	}
}

func TestBus_PanicRecovery_SuccessResetsCounter(t *testing.T) {
	bus, lw := newTestBus(t)
	defer closeBus(t, bus)

	// Sequence: panic, panic, success, panic, panic — should NOT disable
	// because the success in the middle resets the counter to 0.
	var seq atomic.Int64
	unsub := bus.(*memBus).SubscribeNamed("resetter", "topic", func(payload any) {
		n := seq.Add(1)
		switch n {
		case 1, 2, 4, 5:
			panic("nope")
		}
	})
	defer unsub()

	for i := 0; i < 5; i++ {
		bus.Publish("topic", i)
	}

	if !waitFor(t, func() bool { return seq.Load() == 5 }, 2*time.Second) {
		t.Fatalf("expected 5 handler invocations, got %d", seq.Load())
	}

	// Give any (incorrect) disable log a chance to land before asserting.
	time.Sleep(50 * time.Millisecond)

	logs := lw.String()
	if strings.Contains(logs, "disabled after 3 consecutive panics") {
		t.Fatalf("subscription wrongly disabled despite mid-sequence success, log:\n%s", logs)
	}

	// A 6th event should still reach the handler.
	bus.Publish("topic", 99)
	if !waitFor(t, func() bool { return seq.Load() >= 6 }, time.Second) {
		t.Fatalf("expected 6th call after reset, got %d", seq.Load())
	}
}

func TestBus_UnsubscribeAfterDisable(t *testing.T) {
	bus, _ := newTestBus(t)
	defer closeBus(t, bus)

	var calls atomic.Int64
	unsub := bus.(*memBus).SubscribeNamed("doomed", "topic", func(payload any) {
		calls.Add(1)
		panic("nope")
	})

	for i := 0; i < 3; i++ {
		bus.Publish("topic", i)
	}
	if !waitFor(t, func() bool { return calls.Load() == 3 }, 2*time.Second) {
		t.Fatalf("expected disable after 3 panics, got %d", calls.Load())
	}

	// Unsubscribe must still complete cleanly — no hang, no panic.
	done := make(chan struct{})
	go func() {
		unsub()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Unsubscribe after disable did not complete in 2s")
	}

	// Calling unsub a second time must also be a safe no-op.
	unsub()
}

func TestBus_ConcurrentPublish(t *testing.T) {
	bus := NewBus()
	defer closeBus(t, bus)

	var got atomic.Int64
	unsub := bus.Subscribe("blast", func(payload any) {
		got.Add(1)
	})
	defer unsub()

	const goroutines = 16
	const perGoroutine = 200
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for g := 0; g < goroutines; g++ {
		go func() {
			defer wg.Done()
			for i := 0; i < perGoroutine; i++ {
				bus.Publish("blast", i)
			}
		}()
	}
	wg.Wait()

	// Under load some events may be dropped (drop-oldest); the bus must
	// (a) not race and (b) deliver at least *some* events.
	total := int64(goroutines * perGoroutine)
	if !waitFor(t, func() bool { return got.Load() > 0 }, 3*time.Second) {
		t.Fatalf("no events delivered concurrently")
	}
	// Give the drain a moment to catch up.
	time.Sleep(50 * time.Millisecond)
	if got.Load() > total {
		t.Fatalf("got %d > total published %d (impossible — bug)", got.Load(), total)
	}
}

func TestBus_ConcurrentSubscribeUnsubscribe_NoGoroutineLeak(t *testing.T) {
	bus := NewBus()
	defer closeBus(t, bus)

	// Settle base goroutine count.
	runtime.GC()
	time.Sleep(20 * time.Millisecond)
	baseline := runtime.NumGoroutine()

	const workers = 8
	const iters = 50

	stop := make(chan struct{})
	var pubWG sync.WaitGroup
	pubWG.Add(1)
	go func() {
		defer pubWG.Done()
		for {
			select {
			case <-stop:
				return
			default:
				bus.Publish("topic", "x")
			}
		}
	}()

	var subWG sync.WaitGroup
	subWG.Add(workers)
	for w := 0; w < workers; w++ {
		go func() {
			defer subWG.Done()
			for i := 0; i < iters; i++ {
				unsub := bus.Subscribe("topic", func(payload any) {})
				// Hold the subscription briefly so events flow through it.
				time.Sleep(time.Millisecond)
				unsub()
			}
		}()
	}

	subWG.Wait()
	close(stop)
	pubWG.Wait()

	// Allow drain goroutines to exit.
	if !waitFor(t, func() bool {
		runtime.GC()
		return runtime.NumGoroutine() <= baseline+2
	}, 3*time.Second) {
		t.Fatalf("goroutine leak suspected: baseline=%d, now=%d", baseline, runtime.NumGoroutine())
	}
}
