package pluginhost

import (
	"context"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/zulandar/railyard/internal/events"
	"github.com/zulandar/railyard/pkg/plugin"
	protov1 "github.com/zulandar/railyard/pkg/plugin/proto/v1"
)

// fakeSubscribeStream is a minimal in-process implementation of the
// HostService_SubscribeServer used by the Subscribe RPC. It records
// every Send into a slice and observes the supplied context for
// cancellation.
type fakeSubscribeStream struct {
	protov1.HostService_SubscribeServer // for any unused methods

	ctx context.Context

	mu       sync.Mutex
	received []*protov1.Event
}

func (s *fakeSubscribeStream) Context() context.Context { return s.ctx }

func (s *fakeSubscribeStream) Send(ev *protov1.Event) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.received = append(s.received, ev)
	return nil
}

func (s *fakeSubscribeStream) Recv() (*protov1.Event, error) { return nil, nil }

// TestSubscribeDeliversEvents wires a Subscribe call to the host's bus
// and asserts a published event lands on the gRPC stream.
func TestSubscribeDeliversEvents(t *testing.T) {
	bus := events.NewBus()
	defer bus.(interface{ Close() }).Close()
	host := NewHost(Dependencies{Bus: bus})
	hs := newHostService(host, "p1")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stream := &fakeSubscribeStream{ctx: ctx}

	done := make(chan error, 1)
	go func() {
		done <- hs.Subscribe(&protov1.SubscribeRequest{
			Topics: []string{string(plugin.CarCreated)},
		}, stream)
	}()

	// Give the goroutine a moment to wire up bus subscriptions.
	time.Sleep(50 * time.Millisecond)

	bus.Publish(string(plugin.CarCreated), plugin.CarCreatedEvent{
		CarID:    "car-abc",
		Track:    "go",
		Type:     "bug",
		Priority: 1,
	})

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		stream.mu.Lock()
		n := len(stream.received)
		stream.mu.Unlock()
		if n >= 1 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	stream.mu.Lock()
	defer stream.mu.Unlock()
	if len(stream.received) == 0 {
		t.Fatal("no events delivered")
	}
	got := stream.received[0]
	if got.Type != protov1.EventType_EVENT_TYPE_CAR_CREATED {
		t.Errorf("type = %v, want CAR_CREATED", got.Type)
	}
	if p := got.GetCarCreated(); p == nil || p.CarId != "car-abc" {
		t.Errorf("payload = %+v, want CarId=car-abc", p)
	}

	cancel()
	// Stream goroutine should exit promptly when the context is cancelled.
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Subscribe did not exit after context cancel")
	}
}

// TestSubscribeNoGoroutineLeak ensures the per-stream bus subscriptions
// + drain goroutine are cleaned up when the client disconnects. We bound
// the leak check to a few hundred milliseconds because goroutine teardown
// is asynchronous; flaky scheduling under -race can take longer than
// expected, hence the polling loop.
func TestSubscribeNoGoroutineLeak(t *testing.T) {
	// Warm up — give any one-time runtime initialisation a chance to
	// settle so the baseline reading is stable.
	runtime.GC()
	baseline := runtime.NumGoroutine()

	bus := events.NewBus()
	host := NewHost(Dependencies{Bus: bus})
	hs := newHostService(host, "p1")

	for i := 0; i < 5; i++ {
		ctx, cancel := context.WithCancel(context.Background())
		stream := &fakeSubscribeStream{ctx: ctx}
		done := make(chan error, 1)
		go func() {
			done <- hs.Subscribe(&protov1.SubscribeRequest{
				Topics: []string{string(plugin.CarCreated)},
			}, stream)
		}()
		time.Sleep(20 * time.Millisecond)
		cancel()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Fatal("subscribe did not exit")
		}
	}

	bus.(interface{ Close() }).Close()

	// Allow up to 500ms for stragglers (the bus close itself drains in a
	// goroutine).
	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		runtime.GC()
		if got := runtime.NumGoroutine(); got <= baseline+1 {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Errorf("goroutine leak: NumGoroutine = %d, baseline = %d", runtime.NumGoroutine(), baseline)
}

// TestSubscribeNilBus returns an error when the host has no bus wired.
func TestSubscribeNilBus(t *testing.T) {
	host := NewHost(Dependencies{})
	hs := newHostService(host, "p1")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stream := &fakeSubscribeStream{ctx: ctx}
	if err := hs.Subscribe(&protov1.SubscribeRequest{Topics: []string{"X"}}, stream); err == nil {
		t.Error("expected error from nil-bus Subscribe")
	}
}
