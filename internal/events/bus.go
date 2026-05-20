// Package events provides an in-process, type-agnostic publish/subscribe bus
// used by the railyard plugin system. Topics are plain strings and payloads
// are any; higher-level code (pkg/plugin) wraps this with a typed EventType
// alias and type-checks payloads before forwarding to plugin handlers.
//
// This file implements bead railyard-3q8.1.1 — the Bus interface plus an
// in-memory implementation. Backpressure logging with drop-oldest semantics
// and per-subscriber panic recovery / disable-on-3-strikes are handled by
// follow-up beads (.1.2 and .1.3 respectively).
package events

import (
	"sync"
)

// subscriberQueueSize is the per-subscriber buffered channel capacity.
// A slow handler that fills this queue causes Publish to drop the event
// (see TODO in publish for the planned .1.2 behavior change).
const subscriberQueueSize = 256

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
type subscription struct {
	handler Handler
	ch      chan any
	done    chan struct{} // closed by the drain goroutine once it exits
}

// memBus is the in-memory Bus implementation. One buffered channel and one
// drain goroutine per subscriber; publishes fan out non-blocking.
type memBus struct {
	mu     sync.RWMutex
	subs   map[string]map[uint64]*subscription
	nextID uint64
	closed bool
	wg     sync.WaitGroup
}

// NewBus returns an in-memory Bus. Call Close (via the *memBus value, e.g.
// bus.(interface{ Close() }).Close()) to stop drain goroutines on shutdown.
func NewBus() Bus {
	return &memBus{
		subs: make(map[string]map[uint64]*subscription),
	}
}

// Publish fans out payload to every current subscriber of topic. Delivery is
// non-blocking: if a subscriber's queue is full the event is dropped for that
// subscriber. Other subscribers are unaffected.
//
// TODO(railyard-3q8.1.2): replace the silent drop in the default branch with
// drop-oldest semantics and a WARN log including subscriber name + topic.
func (b *memBus) Publish(topic string, payload any) {
	b.mu.RLock()
	if b.closed {
		b.mu.RUnlock()
		return
	}
	topicSubs := b.subs[topic]
	// Snapshot the channels so we can release the read lock before sending —
	// keeps Publish from blocking concurrent Subscribe/Unsubscribe calls.
	targets := make([]chan any, 0, len(topicSubs))
	for _, s := range topicSubs {
		targets = append(targets, s.ch)
	}
	b.mu.RUnlock()

	for _, ch := range targets {
		select {
		case ch <- payload:
		default:
			// Queue full — drop. See TODO above.
		}
	}
}

// Subscribe registers handler for topic and returns an Unsubscribe that
// removes it. The handler runs on a dedicated drain goroutine.
func (b *memBus) Subscribe(topic string, h Handler) Unsubscribe {
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		// Returning a no-op keeps callers safe even if they race with Close.
		return func() {}
	}
	id := b.nextID
	b.nextID++
	sub := &subscription{
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
// Exits when the channel is closed (by unsubscribe or Close).
//
// TODO(railyard-3q8.1.3): wrap h(payload) in a recover() with 3-strike
// disable + ERROR log.
func (b *memBus) drain(s *subscription) {
	defer b.wg.Done()
	defer close(s.done)
	for payload := range s.ch {
		s.handler(payload)
	}
}

// unsubscribe removes the subscription and shuts down its drain goroutine.
// Safe to call multiple times (guarded by sync.Once in the returned closure).
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
