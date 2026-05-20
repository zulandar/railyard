package engine

import (
	"sync"
	"testing"
	"time"

	"github.com/zulandar/railyard/internal/db"
	"github.com/zulandar/railyard/internal/events"
	"github.com/zulandar/railyard/internal/models"
	"github.com/zulandar/railyard/pkg/plugin"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// fakeBusEvent is one captured publish.
type fakeBusEvent struct {
	Topic   string
	Payload any
}

// fakeBus is a minimal events.Bus that records every publish for assertion.
// It implements only what we need — Subscribe returns a no-op Unsubscribe
// since none of the engine code under test subscribes.
type fakeBus struct {
	mu     sync.Mutex
	events []fakeBusEvent
}

func (f *fakeBus) Publish(topic string, payload any) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.events = append(f.events, fakeBusEvent{Topic: topic, Payload: payload})
}

func (f *fakeBus) Subscribe(_ string, _ events.Handler) events.Unsubscribe {
	return func() {}
}

func (f *fakeBus) snapshot() []fakeBusEvent {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]fakeBusEvent, len(f.events))
	copy(out, f.events)
	return out
}

func eventsTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	gormDB, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	sqlDB, _ := gormDB.DB()
	sqlDB.SetMaxOpenConns(1)
	if err := db.AutoMigrate(gormDB); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return gormDB
}

// --- RegisterWithBus ---

func TestRegisterWithBus_PublishesEngineStarted(t *testing.T) {
	gormDB := eventsTestDB(t)
	bus := &fakeBus{}

	eng, err := RegisterWithBus(gormDB, RegisterOpts{Track: "backend"}, bus)
	if err != nil {
		t.Fatalf("RegisterWithBus: %v", err)
	}

	got := bus.snapshot()
	if len(got) != 1 {
		t.Fatalf("got %d events, want 1: %+v", len(got), got)
	}
	if got[0].Topic != string(plugin.EngineStarted) {
		t.Errorf("Topic = %q, want %q", got[0].Topic, string(plugin.EngineStarted))
	}
	payload, ok := got[0].Payload.(plugin.EngineStartedEvent)
	if !ok {
		t.Fatalf("payload = %T, want plugin.EngineStartedEvent", got[0].Payload)
	}
	if payload.EngineID != eng.ID {
		t.Errorf("EngineID = %q, want %q", payload.EngineID, eng.ID)
	}
	if payload.Track != "backend" {
		t.Errorf("Track = %q, want %q", payload.Track, "backend")
	}
}

func TestRegisterWithBus_NilBus_NoPanic(t *testing.T) {
	gormDB := eventsTestDB(t)

	eng, err := RegisterWithBus(gormDB, RegisterOpts{Track: "backend"}, nil)
	if err != nil {
		t.Fatalf("RegisterWithBus: %v", err)
	}
	if eng.Track != "backend" {
		t.Errorf("Track = %q, want %q", eng.Track, "backend")
	}
}

func TestRegisterWithBus_RegisterError_NoPublish(t *testing.T) {
	gormDB := eventsTestDB(t)
	bus := &fakeBus{}

	// Empty track triggers validation error before any DB write.
	_, err := RegisterWithBus(gormDB, RegisterOpts{}, bus)
	if err == nil {
		t.Fatal("expected validation error")
	}

	if got := bus.snapshot(); len(got) != 0 {
		t.Errorf("got %d events after failed register, want 0: %+v", len(got), got)
	}
}

// --- DeregisterWithBus ---

func TestDeregisterWithBus_PublishesEngineStopped(t *testing.T) {
	gormDB := eventsTestDB(t)
	bus := &fakeBus{}

	eng, err := RegisterWithBus(gormDB, RegisterOpts{Track: "backend"}, bus)
	if err != nil {
		t.Fatalf("Register: %v", err)
	}

	if err := DeregisterWithBus(gormDB, eng.ID, bus); err != nil {
		t.Fatalf("DeregisterWithBus: %v", err)
	}

	got := bus.snapshot()
	if len(got) != 2 {
		t.Fatalf("got %d events, want 2: %+v", len(got), got)
	}
	// Order: EngineStarted (from Register), then EngineStopped.
	stop := got[1]
	if stop.Topic != string(plugin.EngineStopped) {
		t.Errorf("Topic = %q, want %q", stop.Topic, string(plugin.EngineStopped))
	}
	payload, ok := stop.Payload.(plugin.EngineStoppedEvent)
	if !ok {
		t.Fatalf("payload = %T, want plugin.EngineStoppedEvent", stop.Payload)
	}
	if payload.EngineID != eng.ID {
		t.Errorf("EngineID = %q, want %q", payload.EngineID, eng.ID)
	}
}

func TestDeregisterWithBus_NotFound_NoPublish(t *testing.T) {
	gormDB := eventsTestDB(t)
	bus := &fakeBus{}

	err := DeregisterWithBus(gormDB, "eng-doesnotexist", bus)
	if err == nil {
		t.Fatal("expected not-found error")
	}
	if got := bus.snapshot(); len(got) != 0 {
		t.Errorf("got %d events after failed deregister, want 0", len(got))
	}
}

func TestDeregisterWithBus_NilBus_NoPanic(t *testing.T) {
	gormDB := eventsTestDB(t)

	eng, err := Register(gormDB, RegisterOpts{Track: "backend"})
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	if err := DeregisterWithBus(gormDB, eng.ID, nil); err != nil {
		t.Fatalf("DeregisterWithBus nil bus: %v", err)
	}
}

// --- HandleStallWithBus ---

func TestHandleStallWithBus_PublishesEngineStalled(t *testing.T) {
	gormDB := eventsTestDB(t)
	bus := &fakeBus{}

	// Set up: an engine and a car the engine is "working" on.
	eng, err := Register(gormDB, RegisterOpts{Track: "backend"})
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	lastAct := time.Now().Add(-3 * time.Minute).Truncate(time.Second)
	if err := gormDB.Model(&models.Engine{}).Where("id = ?", eng.ID).
		Update("last_activity", lastAct).Error; err != nil {
		t.Fatalf("update last_activity: %v", err)
	}

	car := &models.Car{
		ID:     "car-stall1",
		Title:  "stall test",
		Status: "working",
		Track:  "backend",
	}
	if err := gormDB.Create(car).Error; err != nil {
		t.Fatalf("create car: %v", err)
	}

	reason := StallReason{Type: "stdout_timeout", Detail: "no output for 2m"}
	if err := HandleStallWithBus(gormDB, eng.ID, car.ID, reason, "", "", bus); err != nil {
		t.Fatalf("HandleStallWithBus: %v", err)
	}

	got := bus.snapshot()
	if len(got) != 1 {
		t.Fatalf("got %d events, want 1: %+v", len(got), got)
	}
	if got[0].Topic != string(plugin.EngineStalled) {
		t.Errorf("Topic = %q, want %q", got[0].Topic, string(plugin.EngineStalled))
	}
	payload, ok := got[0].Payload.(plugin.EngineStalledEvent)
	if !ok {
		t.Fatalf("payload = %T, want plugin.EngineStalledEvent", got[0].Payload)
	}
	if payload.EngineID != eng.ID {
		t.Errorf("EngineID = %q, want %q", payload.EngineID, eng.ID)
	}
	if payload.LastActivityUnix != lastAct.Unix() {
		t.Errorf("LastActivityUnix = %d, want %d", payload.LastActivityUnix, lastAct.Unix())
	}

	// Verify side effects of HandleStall still happen (engine stalled, car blocked).
	var engAfter models.Engine
	gormDB.Where("id = ?", eng.ID).First(&engAfter)
	if engAfter.Status != StatusStalled {
		t.Errorf("engine status = %q, want %q", engAfter.Status, StatusStalled)
	}
	var carAfter models.Car
	gormDB.Where("id = ?", car.ID).First(&carAfter)
	if carAfter.Status != "blocked" {
		t.Errorf("car status = %q, want %q", carAfter.Status, "blocked")
	}
}

func TestHandleStallWithBus_HandleStallError_NoPublish(t *testing.T) {
	gormDB := eventsTestDB(t)
	bus := &fakeBus{}

	// No engine, no car — HandleStall must fail and we must not publish.
	reason := StallReason{Type: "stdout_timeout", Detail: "no output"}
	err := HandleStallWithBus(gormDB, "eng-missing", "car-missing", reason, "", "", bus)
	if err == nil {
		t.Fatal("expected error from HandleStallWithBus with missing engine")
	}
	if got := bus.snapshot(); len(got) != 0 {
		t.Errorf("got %d events after failed stall handler, want 0: %+v", len(got), got)
	}
}

func TestHandleStallWithBus_NilBus_NoPanic(t *testing.T) {
	gormDB := eventsTestDB(t)

	eng, err := Register(gormDB, RegisterOpts{Track: "backend"})
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	car := &models.Car{
		ID:     "car-nilbus",
		Title:  "stall nil bus",
		Status: "working",
		Track:  "backend",
	}
	if err := gormDB.Create(car).Error; err != nil {
		t.Fatalf("create car: %v", err)
	}

	reason := StallReason{Type: "stdout_timeout", Detail: "x"}
	if err := HandleStallWithBus(gormDB, eng.ID, car.ID, reason, "", "", nil); err != nil {
		t.Fatalf("HandleStallWithBus nil bus: %v", err)
	}
}

// --- publish helper ---

func TestPublish_NilBusNoOp(t *testing.T) {
	// Direct unit test of the nil-safe publish helper.
	publish(nil, "anything", "payload") // must not panic
}

func TestPublish_DelegatesToBus(t *testing.T) {
	bus := &fakeBus{}
	publish(bus, "topic-x", "payload-x")
	got := bus.snapshot()
	if len(got) != 1 || got[0].Topic != "topic-x" || got[0].Payload != "payload-x" {
		t.Errorf("publish did not forward correctly: %+v", got)
	}
}
