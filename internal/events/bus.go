// Package events provides an in-process, type-agnostic publish/subscribe bus
// used by the railyard plugin system. Topics are plain strings and payloads
// are any; higher-level code (pkg/plugin) wraps this with a typed EventType
// alias and type-checks payloads before forwarding to plugin handlers.
//
// This file implements beads railyard-3q8.1.1 (interface + happy-path),
// railyard-3q8.1.2 (drop-oldest backpressure + WARN log, panic recovery with
// 3-strike disable + ERROR log) and railyard-3q8.1.3 (tests in
// bus_test.go / bus_backpressure_test.go).
//
// Backpressure semantics: when a subscriber's queue is full, Publish drains
// one element from the channel and then sends the new one. Under concurrent
// publishers this race is intentional — the goal is "events keep flowing",
// not strict FIFO. Trainmaster's next heartbeat carries ground-truth state,
// so dropped events do not desync (spec §6.2).
package events

import (
	"fmt"
	"log/slog"
	"runtime/debug"
	"sync"
	"sync/atomic"
)

// subscriberQueueSize is the per-subscriber buffered channel capacity.
// A slow handler that fills this queue causes Publish to drop the oldest
// queued event (FIFO eviction) and log a WARN — see Publish.
const subscriberQueueSize = 256

// panicDisableThreshold is the number of consecutive panics after which a
// subscription is disabled (no further events delivered, no per-event spam).
// A successful handler call resets the counter.
const panicDisableThreshold = 3

// Handler is invoked once per delivered event on the subscriber's drain
// goroutine. Handlers should be cheap; long work should be queued out.
type Handler func(payload any)

// Unsubscribe removes the subscription it was returned for. It is safe to
// call more than once; subsequent calls are no-ops.
type Unsubscribe func()

// Bus is the minimal publish/subscribe surface plugins (via Host) and core
// publishers share. Implementations must be safe for concurrent use.
type Bus interface {
	Publish(topic string, payload any)
	Subscribe(topic string, h Handler) Unsubscribe
}

// subscription holds the per-subscriber queue and lifecycle plumbing.
//
// panics counts CONSECUTIVE handler panics. A successful handler call resets
// it to 0. disabled is set (1) by the drain goroutine when panics reaches
// panicDisableThreshold; once set, Publish skips this subscriber and the
// drain goroutine stops invoking the handler (but still drains the channel
// so Unsubscribe / Close can close the channel cleanly).
type subscription struct {
	name    string
	topic   string
	handler Handler
	ch      chan any
	done    chan struct{} // closed by the drain goroutine once it exits

	// panics and disabled are touched by the drain goroutine (RMW) and read
	// by Publish via atomic.Load — no other writers.
	panics   atomic.Int32
	disabled atomic.Bool
}

// memBus is the in-memory Bus implementation. One buffered channel and one
// drain goroutine per subscriber; publishes fan out non-blocking.
type memBus struct {
	mu     sync.RWMutex
	subs   map[string]map[uint64]*subscription
	nextID uint64
	closed bool
	wg     sync.WaitGroup
	logger *slog.Logger
}

// NewBus returns an in-memory Bus using slog.Default() for backpressure and
// panic-recovery logs. Call Close (via a Bus type-asserted to interface{ Close() })
// to stop drain goroutines on shutdown.
func NewBus() Bus {
	return NewBusWithLogger(nil)
}

// NewBusWithLogger returns an in-memory Bus that emits backpressure WARN and
// panic ERROR logs to the supplied slog.Logger. A nil logger falls back to
// slog.Default(). The returned value also implements Close().
func NewBusWithLogger(logger *slog.Logger) Bus {
	if logger == nil {
		logger = slog.Default()
	}
	return &memBus{
		subs:   make(map[string]map[uint64]*subscription),
		logger: logger,
	}
}

// Publish fans out payload to every current subscriber of topic. Delivery is
// non-blocking. If a subscriber's queue is full, Publish drains one element
// (the oldest) and sends the new payload, emitting a WARN log per eviction.
// Disabled subscribers (3 consecutive panics) are skipped silently.
//
// Other subscribers are unaffected by a slow / disabled peer.
//
// Publish holds the bus read lock for the duration of the fan-out. This
// serialises against Unsubscribe/Close (which take the write lock to close
// the subscriber channels), guaranteeing that we never send on a closed
// channel. Multiple Publish calls can proceed concurrently.
func (b *memBus) Publish(topic string, payload any) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	if b.closed {
		return
	}
	for _, sub := range b.subs[topic] {
		if sub.disabled.Load() {
			continue
		}
		select {
		case sub.ch <- payload:
		default:
			// Queue full: drop the oldest event to make room for the new one.
			// Concurrent publishers may race here — that's accepted; under
			// contention some events may still be lost but the bus keeps
			// flowing rather than dead-locking. The WARN log accounts for
			// every eviction we observe.
			b.evictOldest(sub, payload)
		}
	}
}

// evictOldest performs a non-blocking drain + send, logging the eviction.
// If the post-drain send still cannot make it (because another publisher
// won the race and re-filled the slot), the new event is dropped silently —
// the WARN already announced an eviction, no need to spam.
func (b *memBus) evictOldest(sub *subscription, payload any) {
	select {
	case <-sub.ch:
		b.logger.Warn(fmt.Sprintf(
			"events: dropped oldest event for subscriber %q on topic %q (queue full, cap=%d)",
			sub.name, sub.topic, subscriberQueueSize,
		))
	default:
		// Another goroutine drained it first; nothing to evict, nothing to log.
	}
	select {
	case sub.ch <- payload:
	default:
		// Lost the race against another publisher post-evict. Drop silently.
	}
}

// Subscribe registers handler for topic with an auto-generated subscriber
// name ("sub#<id>") and returns an Unsubscribe that removes it. The handler
// runs on a dedicated drain goroutine.
func (b *memBus) Subscribe(topic string, h Handler) Unsubscribe {
	return b.subscribe("", topic, h)
}

// SubscribeNamed is identical to Subscribe but lets the caller specify a
// human-readable name for logs. Empty name falls back to "sub#<id>". The
// public Bus interface intentionally omits this — internal/test callers
// can type-assert to *memBus to use it.
func (b *memBus) SubscribeNamed(name, topic string, h Handler) Unsubscribe {
	return b.subscribe(name, topic, h)
}

func (b *memBus) subscribe(name, topic string, h Handler) Unsubscribe {
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		// Returning a no-op keeps callers safe even if they race with Close.
		return func() {}
	}
	id := b.nextID
	b.nextID++
	if name == "" {
		name = fmt.Sprintf("sub#%d", id)
	}
	sub := &subscription{
		name:    name,
		topic:   topic,
		handler: h,
		ch:      make(chan any, subscriberQueueSize),
		done:    make(chan struct{}),
	}
	if b.subs[topic] == nil {
		b.subs[topic] = make(map[uint64]*subscription)
	}
	b.subs[topic][id] = sub
	b.wg.Add(1)
	b.mu.Unlock()

	go b.drain(sub)

	var once sync.Once
	return func() {
		once.Do(func() { b.unsubscribe(topic, id) })
	}
}

// drain pulls events off the subscriber's channel and invokes the handler.
// Each handler call is wrapped in a deferred recover; a panic increments the
// consecutive-panic counter and a success resets it. After
// panicDisableThreshold consecutive panics the subscription is disabled —
// further events are still drained from the channel (so Unsubscribe/Close
// don't block) but the handler is not invoked.
//
// Exits when the channel is closed (by unsubscribe or Close).
func (b *memBus) drain(s *subscription) {
	defer b.wg.Done()
	defer close(s.done)
	for payload := range s.ch {
		if s.disabled.Load() {
			// Silent drop — we already logged the disable when we set the flag.
			continue
		}
		b.invoke(s, payload)
	}
}

// invoke runs the handler with panic recovery and consecutive-panic tracking.
// Split out from drain so the deferred recover scopes per-call (otherwise
// the deferred handler would only fire once when the goroutine exits).
func (b *memBus) invoke(s *subscription, payload any) {
	defer func() {
		if r := recover(); r != nil {
			b.logger.Error(fmt.Sprintf(
				"events: subscriber %q panicked on topic %q: %v\n%s",
				s.name, s.topic, r, debug.Stack(),
			))
			n := s.panics.Add(1)
			if n >= panicDisableThreshold {
				s.disabled.Store(true)
				b.logger.Error(fmt.Sprintf(
					"events: subscriber %q disabled after %d consecutive panics",
					s.name, panicDisableThreshold,
				))
			}
			return
		}
		// Successful call — reset the consecutive-panic counter.
		s.panics.Store(0)
	}()
	s.handler(payload)
}

// unsubscribe removes the subscription and shuts down its drain goroutine.
// Safe to call multiple times (guarded by sync.Once in the returned closure).
// Works on disabled subscriptions too — the drain goroutine drains the
// channel without invoking the handler, then exits on close.
func (b *memBus) unsubscribe(topic string, id uint64) {
	b.mu.Lock()
	topicSubs := b.subs[topic]
	sub, ok := topicSubs[id]
	if !ok {
		b.mu.Unlock()
		return
	}
	delete(topicSubs, id)
	if len(topicSubs) == 0 {
		delete(b.subs, topic)
	}
	b.mu.Unlock()

	close(sub.ch)
	<-sub.done
}

// Close stops accepting new publishes/subscribes, closes every subscriber's
// queue, and waits for all drain goroutines to finish. Safe to call once;
// subsequent calls are no-ops.
func (b *memBus) Close() {
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return
	}
	b.closed = true
	// Collect channels to close, then drop the map so future operations are
	// cheap no-ops.
	chans := make([]chan any, 0)
	for _, topicSubs := range b.subs {
		for _, sub := range topicSubs {
			chans = append(chans, sub.ch)
		}
	}
	b.subs = make(map[string]map[uint64]*subscription)
	b.mu.Unlock()

	for _, ch := range chans {
		close(ch)
	}
	b.wg.Wait()
}
