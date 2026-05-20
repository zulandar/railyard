package events

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// waitFor polls until cond returns true or the deadline is reached.
// Returns true if cond became true in time. Used to give the per-subscriber
// drain goroutine a moment to invoke the handler without a brittle Sleep.
func waitFor(t *testing.T, cond func() bool, timeout time.Duration) bool {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return true
		}
		time.Sleep(2 * time.Millisecond)
	}
	return cond()
}

// closer is the optional Close() shape exposed by the in-memory bus.
// The Bus interface (Publish/Subscribe only) deliberately omits Close so the
// production API stays minimal; tests type-assert through this helper.
type closer interface{ Close() }

func closeBus(t *testing.T, bus Bus) {
	t.Helper()
	if c, ok := bus.(closer); ok {
		c.Close()
	} else {
		t.Fatal("Bus implementation does not expose Close()")
	}
}

func TestBus_PublishDeliversToSubscriber(t *testing.T) {
	bus := NewBus()
	defer closeBus(t, bus)

	var received atomic.Value
	done := make(chan struct{}, 1)
	unsub := bus.Subscribe("car.created", func(payload any) {
		received.Store(payload)
		select {
		case done <- struct{}{}:
		default:
		}
	})
	defer unsub()

	bus.Publish("car.created", "hello")

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("handler not invoked within 1s")
	}

	if got := received.Load(); got != "hello" {
		t.Fatalf("got payload %v, want %q", got, "hello")
	}
}

func TestBus_FanOutToMultipleSubscribers(t *testing.T) {
	bus := NewBus()
	defer closeBus(t, bus)

	var wg sync.WaitGroup
	wg.Add(2)

	var aGot, bGot atomic.Value
	unsubA := bus.Subscribe("engine.started", func(payload any) {
		aGot.Store(payload)
		wg.Done()
	})
	defer unsubA()
	unsubB := bus.Subscribe("engine.started", func(payload any) {
		bGot.Store(payload)
		wg.Done()
	})
	defer unsubB()

	bus.Publish("engine.started", 42)

	doneCh := make(chan struct{})
	go func() {
		wg.Wait()
		close(doneCh)
	}()

	select {
	case <-doneCh:
	case <-time.After(time.Second):
		t.Fatal("both subscribers did not receive within 1s")
	}

	if aGot.Load() != 42 {
		t.Fatalf("subscriber A got %v, want 42", aGot.Load())
	}
	if bGot.Load() != 42 {
		t.Fatalf("subscriber B got %v, want 42", bGot.Load())
	}
}

func TestBus_UnsubscribeStopsDelivery(t *testing.T) {
	bus := NewBus()
	defer closeBus(t, bus)

	var count atomic.Int64
	unsub := bus.Subscribe("yard.paused", func(payload any) {
		count.Add(1)
	})

	bus.Publish("yard.paused", nil)
	if !waitFor(t, func() bool { return count.Load() == 1 }, time.Second) {
		t.Fatalf("expected 1 delivery before unsubscribe, got %d", count.Load())
	}

	unsub()

	// A second unsub call should be a safe no-op.
	unsub()

	bus.Publish("yard.paused", nil)
	bus.Publish("yard.paused", nil)

	// Give any (incorrect) deliveries a chance to fire.
	time.Sleep(50 * time.Millisecond)

	if got := count.Load(); got != 1 {
		t.Fatalf("handler called after unsubscribe: count=%d, want 1", got)
	}
}
