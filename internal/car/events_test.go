package car

import (
	"sync"
	"testing"

	"github.com/zulandar/railyard/internal/events"
	"github.com/zulandar/railyard/internal/models"
	"github.com/zulandar/railyard/pkg/plugin"
	"gorm.io/gorm"
)

// captureBus is a test [events.Bus] that appends every (topic, payload) pair
// to an in-memory slice. Concurrency-safe via a mutex so concurrent callers
// (drain goroutines from a real subscription chain) can't corrupt state, even
// though the tests below drive transitions sequentially.
type captureBus struct {
	mu     sync.Mutex
	events []capturedEvent
}

type capturedEvent struct {
	Topic   string
	Payload any
}

func (b *captureBus) Publish(topic string, payload any) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.events = append(b.events, capturedEvent{Topic: topic, Payload: payload})
}

// Subscribe is a no-op for the capture bus — tests assert against captured
// events directly, not via a subscription. We still need to satisfy the
// [events.Bus] interface.
func (b *captureBus) Subscribe(_ string, _ events.Handler) events.Unsubscribe {
	return func() {}
}

func (b *captureBus) snapshot() []capturedEvent {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]capturedEvent, len(b.events))
	copy(out, b.events)
	return out
}

// topics returns the topic strings of every captured event in order.
func (b *captureBus) topics() []string {
	snap := b.snapshot()
	out := make([]string, len(snap))
	for i, e := range snap {
		out[i] = e.Topic
	}
	return out
}

// firstOf returns the payload of the first event matching topic, or nil if
// none exists. Useful for asserting payload shape.
func (b *captureBus) firstOf(topic plugin.EventType) any {
	for _, e := range b.snapshot() {
		if e.Topic == string(topic) {
			return e.Payload
		}
	}
	return nil
}

// --- CarCreated ---

func TestCreateWithBus_PublishesCarCreated(t *testing.T) {
	db := testDB(t)
	bus := &captureBus{}

	car, err := CreateWithBus(db, bus, CreateOpts{
		Title:        "Test car",
		Track:        "backend",
		Priority:     2,
		RequestedBy:  "alice",
		BranchPrefix: "ry/test",
	})
	if err != nil {
		t.Fatalf("CreateWithBus: %v", err)
	}

	got := bus.firstOf(plugin.CarCreated)
	if got == nil {
		t.Fatalf("no CarCreated event captured; topics = %v", bus.topics())
	}
	payload, ok := got.(plugin.CarCreatedEvent)
	if !ok {
		t.Fatalf("payload type = %T, want plugin.CarCreatedEvent", got)
	}
	if payload.CarID != car.ID {
		t.Errorf("CarID = %q, want %q", payload.CarID, car.ID)
	}
	if payload.Track != "backend" {
		t.Errorf("Track = %q, want backend", payload.Track)
	}
	if payload.Type != "task" {
		t.Errorf("Type = %q, want task (default)", payload.Type)
	}
	if payload.Priority != 2 {
		t.Errorf("Priority = %d, want 2", payload.Priority)
	}
	if payload.RequestedBy != "alice" {
		t.Errorf("RequestedBy = %q, want alice", payload.RequestedBy)
	}
}

func TestCreateWithBus_NoEventOnValidationError(t *testing.T) {
	db := testDB(t)
	bus := &captureBus{}

	_, err := CreateWithBus(db, bus, CreateOpts{Track: "backend"}) // missing title
	if err == nil {
		t.Fatal("expected validation error")
	}
	if len(bus.snapshot()) != 0 {
		t.Errorf("expected no events on validation error, got %v", bus.topics())
	}
}

func TestCreate_NilBusSafe(t *testing.T) {
	// Sanity check: original Create() must continue to work without a bus.
	db := testDB(t)
	if _, err := Create(db, CreateOpts{Title: "no bus", Track: "backend", BranchPrefix: "ry/test"}); err != nil {
		t.Fatalf("Create: %v", err)
	}
}

// --- CarStatusChanged + CarClaimed / CarMerged / MergeFailed ---

// drive moves a car deterministically through valid transitions to the target
// status, using the bus-aware Update. Returns once the car is at status.
func drive(t *testing.T, db *gorm.DB, bus events.Bus, id string, path ...map[string]interface{}) {
	t.Helper()
	for i, step := range path {
		if err := UpdateWithBus(db, bus, id, step); err != nil {
			t.Fatalf("drive step %d (%v): %v", i, step, err)
		}
	}
}

func TestUpdateWithBus_PublishesCarStatusChanged(t *testing.T) {
	db := testDB(t)
	bus := &captureBus{}

	c, err := CreateWithBus(db, bus, CreateOpts{Title: "status test", Track: "backend"})
	if err != nil {
		t.Fatalf("CreateWithBus: %v", err)
	}
	// Reset bus so we only assert on the transition we drive next.
	bus.events = nil

	// draft → open is normally driven by Publish; do it raw to isolate Update.
	if err := db.Model(&models.Car{}).Where("id = ?", c.ID).Update("status", "open").Error; err != nil {
		t.Fatalf("seed open: %v", err)
	}
	drive(t, db, bus, c.ID, map[string]interface{}{"status": "ready"})

	got := bus.firstOf(plugin.CarStatusChanged)
	if got == nil {
		t.Fatalf("no CarStatusChanged event; topics = %v", bus.topics())
	}
	payload, ok := got.(plugin.CarStatusChangedEvent)
	if !ok {
		t.Fatalf("payload type = %T, want plugin.CarStatusChangedEvent", got)
	}
	if payload.OldStatus != "open" || payload.NewStatus != "ready" {
		t.Errorf("transition = %q→%q, want open→ready", payload.OldStatus, payload.NewStatus)
	}
	if payload.CarID != c.ID {
		t.Errorf("CarID = %q, want %q", payload.CarID, c.ID)
	}
}

func TestUpdateWithBus_PublishesCarClaimed(t *testing.T) {
	db := testDB(t)
	bus := &captureBus{}

	c, err := CreateWithBus(db, bus, CreateOpts{Title: "claim test", Track: "backend"})
	if err != nil {
		t.Fatalf("CreateWithBus: %v", err)
	}
	bus.events = nil

	// Drive draft → open → ready → claimed.
	if err := db.Model(&models.Car{}).Where("id = ?", c.ID).Update("status", "open").Error; err != nil {
		t.Fatalf("seed open: %v", err)
	}
	drive(t, db, bus, c.ID,
		map[string]interface{}{"status": "ready"},
		map[string]interface{}{"status": "claimed", "assignee": "engine-42"},
	)

	got := bus.firstOf(plugin.CarClaimed)
	if got == nil {
		t.Fatalf("no CarClaimed event; topics = %v", bus.topics())
	}
	payload, ok := got.(plugin.CarClaimedEvent)
	if !ok {
		t.Fatalf("payload type = %T, want plugin.CarClaimedEvent", got)
	}
	if payload.CarID != c.ID {
		t.Errorf("CarID = %q, want %q", payload.CarID, c.ID)
	}
	if payload.EngineID != "engine-42" {
		t.Errorf("EngineID = %q, want engine-42", payload.EngineID)
	}

	// CarClaimed should accompany CarStatusChanged on the same transition.
	if bus.firstOf(plugin.CarStatusChanged) == nil {
		t.Errorf("CarStatusChanged should also be published on claim; topics = %v", bus.topics())
	}
}

func TestUpdateWithBus_PublishesCarMerged(t *testing.T) {
	db := testDB(t)
	bus := &captureBus{}

	c, err := CreateWithBus(db, bus, CreateOpts{Title: "merge test", Track: "backend", BranchPrefix: "ry/x"})
	if err != nil {
		t.Fatalf("CreateWithBus: %v", err)
	}
	bus.events = nil

	// Seed the car directly into "pr_open" — pr_open is the only state from
	// which "merged" is reachable per ValidTransitions.
	if err := db.Model(&models.Car{}).Where("id = ?", c.ID).Update("status", "pr_open").Error; err != nil {
		t.Fatalf("seed pr_open: %v", err)
	}
	drive(t, db, bus, c.ID, map[string]interface{}{"status": "merged"})

	got := bus.firstOf(plugin.CarMerged)
	if got == nil {
		t.Fatalf("no CarMerged event; topics = %v", bus.topics())
	}
	payload, ok := got.(plugin.CarMergedEvent)
	if !ok {
		t.Fatalf("payload type = %T, want plugin.CarMergedEvent", got)
	}
	if payload.CarID != c.ID {
		t.Errorf("CarID = %q, want %q", payload.CarID, c.ID)
	}
	if payload.Branch != c.Branch {
		t.Errorf("Branch = %q, want %q", payload.Branch, c.Branch)
	}
}

func TestUpdateWithBus_PublishesMergeFailed(t *testing.T) {
	db := testDB(t)
	bus := &captureBus{}

	c, err := CreateWithBus(db, bus, CreateOpts{Title: "fail test", Track: "backend"})
	if err != nil {
		t.Fatalf("CreateWithBus: %v", err)
	}
	bus.events = nil

	// Drive through to done, then transition to merge-failed.
	if err := db.Model(&models.Car{}).Where("id = ?", c.ID).Update("status", "open").Error; err != nil {
		t.Fatalf("seed open: %v", err)
	}
	drive(t, db, bus, c.ID,
		map[string]interface{}{"status": "ready"},
		map[string]interface{}{"status": "claimed", "assignee": "e1"},
		map[string]interface{}{"status": "in_progress"},
		map[string]interface{}{"status": "done"},
	)
	bus.events = nil // isolate the merge-failed transition

	drive(t, db, bus, c.ID, map[string]interface{}{
		"status":         "merge-failed",
		"blocked_reason": "conflict on main",
	})

	got := bus.firstOf(plugin.MergeFailed)
	if got == nil {
		t.Fatalf("no MergeFailed event; topics = %v", bus.topics())
	}
	payload, ok := got.(plugin.MergeFailedEvent)
	if !ok {
		t.Fatalf("payload type = %T, want plugin.MergeFailedEvent", got)
	}
	if payload.CarID != c.ID {
		t.Errorf("CarID = %q, want %q", payload.CarID, c.ID)
	}
	if payload.Reason != "conflict on main" {
		t.Errorf("Reason = %q, want %q", payload.Reason, "conflict on main")
	}
}

func TestUpdateWithBus_InvalidTransitionPublishesNothing(t *testing.T) {
	db := testDB(t)
	bus := &captureBus{}

	c, err := CreateWithBus(db, bus, CreateOpts{Title: "invalid", Track: "backend"})
	if err != nil {
		t.Fatalf("CreateWithBus: %v", err)
	}
	bus.events = nil

	// draft → done is invalid; no event must be published.
	if err := UpdateWithBus(db, bus, c.ID, map[string]interface{}{"status": "done"}); err == nil {
		t.Fatal("expected invalid transition error")
	}
	if len(bus.snapshot()) != 0 {
		t.Errorf("expected no events on invalid transition; got %v", bus.topics())
	}
}

func TestUpdateWithBus_NonStatusUpdatePublishesNothing(t *testing.T) {
	db := testDB(t)
	bus := &captureBus{}

	c, err := CreateWithBus(db, bus, CreateOpts{Title: "field only", Track: "backend"})
	if err != nil {
		t.Fatalf("CreateWithBus: %v", err)
	}
	bus.events = nil

	if err := UpdateWithBus(db, bus, c.ID, map[string]interface{}{"description": "new"}); err != nil {
		t.Fatalf("UpdateWithBus: %v", err)
	}
	if len(bus.snapshot()) != 0 {
		t.Errorf("non-status update must not publish; got %v", bus.topics())
	}
}

// --- Publish (draft → open) ---

func TestPublishWithBus_PublishesCarStatusChanged(t *testing.T) {
	db := testDB(t)
	bus := &captureBus{}

	c, err := CreateWithBus(db, bus, CreateOpts{Title: "publish test", Track: "backend"})
	if err != nil {
		t.Fatalf("CreateWithBus: %v", err)
	}
	bus.events = nil

	n, err := PublishWithBus(db, bus, c.ID, false)
	if err != nil {
		t.Fatalf("PublishWithBus: %v", err)
	}
	if n != 1 {
		t.Errorf("count = %d, want 1", n)
	}

	got := bus.firstOf(plugin.CarStatusChanged)
	if got == nil {
		t.Fatalf("no CarStatusChanged event; topics = %v", bus.topics())
	}
	payload := got.(plugin.CarStatusChangedEvent)
	if payload.OldStatus != "draft" || payload.NewStatus != "open" {
		t.Errorf("transition = %q→%q, want draft→open", payload.OldStatus, payload.NewStatus)
	}
}

func TestPublishWithBus_AlreadyOpenPublishesNothing(t *testing.T) {
	db := testDB(t)
	bus := &captureBus{}

	c, err := CreateWithBus(db, bus, CreateOpts{Title: "already open", Track: "backend"})
	if err != nil {
		t.Fatalf("CreateWithBus: %v", err)
	}
	if err := db.Model(&models.Car{}).Where("id = ?", c.ID).Update("status", "open").Error; err != nil {
		t.Fatalf("seed open: %v", err)
	}
	bus.events = nil

	if _, err := PublishWithBus(db, bus, c.ID, false); err != nil {
		t.Fatalf("PublishWithBus: %v", err)
	}
	if len(bus.snapshot()) != 0 {
		t.Errorf("no transition should publish nothing; got %v", bus.topics())
	}
}

func TestPublishWithBus_RecursiveEmitsPerChild(t *testing.T) {
	db := testDB(t)
	bus := &captureBus{}

	epic, err := CreateWithBus(db, bus, CreateOpts{Title: "epic", Track: "backend", Type: "epic"})
	if err != nil {
		t.Fatalf("CreateWithBus epic: %v", err)
	}
	for i := 0; i < 3; i++ {
		if _, err := CreateWithBus(db, bus, CreateOpts{Title: "child", Track: "backend", ParentID: epic.ID}); err != nil {
			t.Fatalf("CreateWithBus child %d: %v", i, err)
		}
	}
	bus.events = nil

	n, err := PublishWithBus(db, bus, epic.ID, true)
	if err != nil {
		t.Fatalf("PublishWithBus recursive: %v", err)
	}
	if n != 4 {
		t.Errorf("count = %d, want 4 (epic + 3 children)", n)
	}

	// Expect one CarStatusChanged event per published car.
	count := 0
	for _, e := range bus.snapshot() {
		if e.Topic == string(plugin.CarStatusChanged) {
			count++
		}
	}
	if count != 4 {
		t.Errorf("CarStatusChanged count = %d, want 4; all events = %v", count, bus.topics())
	}
}

// --- Real bus integration smoke ---

func TestPublishWithRealBus_Subscriber(t *testing.T) {
	db := testDB(t)
	bus := events.NewBus()

	var mu sync.Mutex
	var received []plugin.CarCreatedEvent
	done := make(chan struct{}, 1)
	unsub := bus.Subscribe(string(plugin.CarCreated), func(p any) {
		mu.Lock()
		received = append(received, p.(plugin.CarCreatedEvent))
		mu.Unlock()
		done <- struct{}{}
	})
	defer unsub()

	if _, err := CreateWithBus(db, bus, CreateOpts{Title: "real bus", Track: "backend"}); err != nil {
		t.Fatalf("CreateWithBus: %v", err)
	}

	<-done

	mu.Lock()
	defer mu.Unlock()
	if len(received) != 1 {
		t.Fatalf("subscriber got %d events, want 1", len(received))
	}
	if received[0].Track != "backend" {
		t.Errorf("Track = %q, want backend", received[0].Track)
	}
}
