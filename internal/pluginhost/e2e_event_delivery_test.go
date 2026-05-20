package pluginhost

import (
	"context"
	"runtime"
	"sync"
	"testing"
	"time"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/zulandar/railyard/internal/car"
	"github.com/zulandar/railyard/internal/dashboard"
	"github.com/zulandar/railyard/internal/db"
	"github.com/zulandar/railyard/internal/engine"
	"github.com/zulandar/railyard/internal/events"
	"github.com/zulandar/railyard/internal/models"
	"github.com/zulandar/railyard/pkg/plugin"
)

// TestE2EEventDelivery is the full publish-chain end-to-end test for the
// closed Phase 1 event set (spec §6.1). It wires a real events.Bus, a real
// pluginhost.Host, and a fake plugin that subscribes to all 11 EventTypes
// via Host.Subscribe. Each subtest drives one of the publish call sites
// (internal/car, internal/engine, internal/dashboard) and asserts the fake
// plugin received the event with the expected typed payload.
//
// Compromises explicitly documented inline:
//
//   - YardmasterAction publish sites in internal/yardmaster (handle*WithBus,
//     rebalanceEnginesWithBus, etc.) are all package-private. Driving them
//     end-to-end from outside the yardmaster package would require either
//     placing this test in internal/yardmaster (which loses the natural
//     fit with pluginhost) or standing up real git repos for Switch. Per
//     the task's documented fallback, this subtest publishes through the
//     same Bus.Publish call signature production code uses.
//
//   - YardPaused / YardResumed are driven by calling dashboard.SetYardPaused
//     followed by bus.Publish — this is exactly what the dashboard route
//     handler does (see internal/dashboard/routes.go handlePauseYard /
//     handleResumeYard). Going through httptest.NewServer would test gin
//     wiring rather than the event chain.
//
//   - CarMerged is driven via car.UpdateWithBus only. Both internal/car and
//     internal/yardmaster (Switch) publish CarMerged on a successful merge;
//     this test relies on at-least-once delivery of CarMerged so we choose
//     the simpler path. Documented in the subtest.
//
// The test is structured as a single setup followed by per-event subtests
// so a failure in one subtest leaves the others independently observable.
func TestE2EEventDelivery(t *testing.T) {
	if testing.Short() {
		t.Skip("e2e event delivery test skipped in -short mode")
	}

	// Goroutine baseline before any host wiring. Used in the final
	// cleanup-check subtest to assert no daemon / drain goroutines leak.
	goroutinesBefore := runtime.NumGoroutine()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	gormDB := newTestDB(t)
	bus := events.NewBus()

	fp := newE2EFakePlugin("fp")

	host := NewHost(Dependencies{
		DB:  gormDB,
		Bus: bus,
	})
	host.Register(fp)
	host.Init(ctx)
	host.Start(ctx)

	// Sanity: the fake plugin's onStart subscribes to all 11 topics. If
	// Init had failed it would have been removed from the running set
	// before Start ran; assert it's still alive.
	if names := host.Names(); len(names) != 1 || names[0] != "fp" {
		t.Fatalf("plugin not in running set after Init/Start: names=%v", names)
	}

	// -- CarCreated ------------------------------------------------------
	t.Run("CarCreated", func(t *testing.T) {
		c, err := car.CreateWithBus(gormDB, bus, car.CreateOpts{
			Title:        "E2E CarCreated",
			Track:        "backend",
			Type:         "task",
			Priority:     1,
			RequestedBy:  "alice",
			BranchPrefix: "ry/e2e",
		})
		if err != nil {
			t.Fatalf("car.CreateWithBus: %v", err)
		}

		ev := waitForEvent[plugin.CarCreatedEvent](t, fp, plugin.CarCreated,
			func(p plugin.CarCreatedEvent) bool { return p.CarID == c.ID })
		if ev.Track != "backend" {
			t.Errorf("Track = %q, want backend", ev.Track)
		}
		if ev.Type != "task" {
			t.Errorf("Type = %q, want task", ev.Type)
		}
		if ev.Priority != 1 {
			t.Errorf("Priority = %d, want 1", ev.Priority)
		}
		if ev.RequestedBy != "alice" {
			t.Errorf("RequestedBy = %q, want alice", ev.RequestedBy)
		}
	})

	// -- CarStatusChanged ------------------------------------------------
	// draft → open → ready is the cleanest pair of transitions that fires
	// CarStatusChanged without also firing CarClaimed / CarMerged /
	// MergeFailed. We seed the car directly into "open" via raw SQL to
	// isolate the ready transition.
	t.Run("CarStatusChanged", func(t *testing.T) {
		c, err := car.CreateWithBus(gormDB, bus, car.CreateOpts{
			Title:        "E2E status",
			Track:        "backend",
			BranchPrefix: "ry/e2e",
		})
		if err != nil {
			t.Fatalf("seed car: %v", err)
		}
		if err := gormDB.Model(&models.Car{}).Where("id = ?", c.ID).
			Update("status", "open").Error; err != nil {
			t.Fatalf("seed open: %v", err)
		}

		// open → ready emits exactly CarStatusChanged.
		if err := car.UpdateWithBus(gormDB, bus, c.ID, map[string]interface{}{
			"status": "ready",
		}); err != nil {
			t.Fatalf("UpdateWithBus: %v", err)
		}

		ev := waitForEvent[plugin.CarStatusChangedEvent](t, fp, plugin.CarStatusChanged,
			func(p plugin.CarStatusChangedEvent) bool {
				return p.CarID == c.ID && p.NewStatus == "ready"
			})
		if ev.OldStatus != "open" {
			t.Errorf("OldStatus = %q, want open", ev.OldStatus)
		}
	})

	// -- CarClaimed ------------------------------------------------------
	t.Run("CarClaimed", func(t *testing.T) {
		c, err := car.CreateWithBus(gormDB, bus, car.CreateOpts{
			Title:        "E2E claim",
			Track:        "backend",
			BranchPrefix: "ry/e2e",
		})
		if err != nil {
			t.Fatalf("seed car: %v", err)
		}
		if err := gormDB.Model(&models.Car{}).Where("id = ?", c.ID).
			Update("status", "ready").Error; err != nil {
			t.Fatalf("seed ready: %v", err)
		}

		// ready → claimed fires CarStatusChanged AND CarClaimed.
		if err := car.UpdateWithBus(gormDB, bus, c.ID, map[string]interface{}{
			"status":   "claimed",
			"assignee": "eng-e2e-1",
		}); err != nil {
			t.Fatalf("UpdateWithBus: %v", err)
		}

		ev := waitForEvent[plugin.CarClaimedEvent](t, fp, plugin.CarClaimed,
			func(p plugin.CarClaimedEvent) bool { return p.CarID == c.ID })
		if ev.EngineID != "eng-e2e-1" {
			t.Errorf("EngineID = %q, want eng-e2e-1", ev.EngineID)
		}
	})

	// -- CarMerged -------------------------------------------------------
	// Note: per spec §6.3, CarMerged is published from BOTH internal/car
	// (Update status=merged) and internal/yardmaster (Switch). This test
	// drives the simpler path through car.UpdateWithBus. The bus is the
	// same instance the yardmaster path would publish on, so the assertion
	// is identical regardless of which publisher fired.
	t.Run("CarMerged", func(t *testing.T) {
		c, err := car.CreateWithBus(gormDB, bus, car.CreateOpts{
			Title:        "E2E merged",
			Track:        "backend",
			BranchPrefix: "ry/e2e",
		})
		if err != nil {
			t.Fatalf("seed car: %v", err)
		}
		// pr_open is the only status from which "merged" is reachable per
		// car.ValidTransitions.
		if err := gormDB.Model(&models.Car{}).Where("id = ?", c.ID).
			Update("status", "pr_open").Error; err != nil {
			t.Fatalf("seed pr_open: %v", err)
		}

		if err := car.UpdateWithBus(gormDB, bus, c.ID, map[string]interface{}{
			"status": "merged",
		}); err != nil {
			t.Fatalf("UpdateWithBus: %v", err)
		}

		ev := waitForEvent[plugin.CarMergedEvent](t, fp, plugin.CarMerged,
			func(p plugin.CarMergedEvent) bool { return p.CarID == c.ID })
		if ev.Branch != c.Branch {
			t.Errorf("Branch = %q, want %q", ev.Branch, c.Branch)
		}
	})

	// -- MergeFailed -----------------------------------------------------
	t.Run("MergeFailed", func(t *testing.T) {
		c, err := car.CreateWithBus(gormDB, bus, car.CreateOpts{
			Title:        "E2E merge-failed",
			Track:        "backend",
			BranchPrefix: "ry/e2e",
		})
		if err != nil {
			t.Fatalf("seed car: %v", err)
		}
		// Drive through to "done" then transition to "merge-failed".
		if err := gormDB.Model(&models.Car{}).Where("id = ?", c.ID).
			Update("status", "done").Error; err != nil {
			t.Fatalf("seed done: %v", err)
		}

		if err := car.UpdateWithBus(gormDB, bus, c.ID, map[string]interface{}{
			"status":         "merge-failed",
			"blocked_reason": "conflict on main",
		}); err != nil {
			t.Fatalf("UpdateWithBus: %v", err)
		}

		ev := waitForEvent[plugin.MergeFailedEvent](t, fp, plugin.MergeFailed,
			func(p plugin.MergeFailedEvent) bool { return p.CarID == c.ID })
		if ev.Reason != "conflict on main" {
			t.Errorf("Reason = %q, want %q", ev.Reason, "conflict on main")
		}
	})

	// -- EngineStarted ---------------------------------------------------
	t.Run("EngineStarted", func(t *testing.T) {
		eng, err := engine.RegisterWithBus(gormDB,
			engine.RegisterOpts{Track: "backend"}, bus)
		if err != nil {
			t.Fatalf("RegisterWithBus: %v", err)
		}

		ev := waitForEvent[plugin.EngineStartedEvent](t, fp, plugin.EngineStarted,
			func(p plugin.EngineStartedEvent) bool { return p.EngineID == eng.ID })
		if ev.Track != "backend" {
			t.Errorf("Track = %q, want backend", ev.Track)
		}
	})

	// -- EngineStopped ---------------------------------------------------
	t.Run("EngineStopped", func(t *testing.T) {
		eng, err := engine.RegisterWithBus(gormDB,
			engine.RegisterOpts{Track: "backend"}, bus)
		if err != nil {
			t.Fatalf("RegisterWithBus: %v", err)
		}
		// Drain the EngineStarted from the previous call; we only care
		// that EngineStopped fires for this engine.
		if err := engine.DeregisterWithBus(gormDB, eng.ID, bus); err != nil {
			t.Fatalf("DeregisterWithBus: %v", err)
		}

		ev := waitForEvent[plugin.EngineStoppedEvent](t, fp, plugin.EngineStopped,
			func(p plugin.EngineStoppedEvent) bool { return p.EngineID == eng.ID })
		if ev.EngineID != eng.ID {
			t.Errorf("EngineID = %q, want %q", ev.EngineID, eng.ID)
		}
	})

	// -- EngineStalled ---------------------------------------------------
	t.Run("EngineStalled", func(t *testing.T) {
		eng, err := engine.Register(gormDB, engine.RegisterOpts{Track: "backend"})
		if err != nil {
			t.Fatalf("Register: %v", err)
		}
		lastAct := time.Now().Add(-5 * time.Minute).Truncate(time.Second)
		if err := gormDB.Model(&models.Engine{}).Where("id = ?", eng.ID).
			Update("last_activity", lastAct).Error; err != nil {
			t.Fatalf("update last_activity: %v", err)
		}

		stallCar := &models.Car{
			ID:     "car-stall-e2e",
			Title:  "stall e2e",
			Status: "working",
			Track:  "backend",
		}
		if err := gormDB.Create(stallCar).Error; err != nil {
			t.Fatalf("create stall car: %v", err)
		}

		reason := engine.StallReason{Type: "stdout_timeout", Detail: "no output"}
		if err := engine.HandleStallWithBus(gormDB, eng.ID, stallCar.ID,
			reason, "", "", bus); err != nil {
			t.Fatalf("HandleStallWithBus: %v", err)
		}

		ev := waitForEvent[plugin.EngineStalledEvent](t, fp, plugin.EngineStalled,
			func(p plugin.EngineStalledEvent) bool { return p.EngineID == eng.ID })
		if ev.LastActivityUnix != lastAct.Unix() {
			t.Errorf("LastActivityUnix = %d, want %d",
				ev.LastActivityUnix, lastAct.Unix())
		}
	})

	// -- YardmasterAction ------------------------------------------------
	//
	// All yardmaster publish sites for YardmasterAction live in
	// package-private handle*WithBus / rebalanceEnginesWithBus / etc.
	// functions (see internal/yardmaster/actions.go, daemon.go,
	// rebalance.go). The exported entry points either need a real git
	// repo (Switch) or run inside a long-lived daemon. Per the task's
	// documented fallback, we drive the publish directly using the same
	// Bus.Publish signature production code uses. The full publish path
	// (events.Bus -> Host.Subscribe -> typed handler) is still exercised
	// end-to-end; only the upstream caller differs from a typical
	// yardmaster action.
	t.Run("YardmasterAction", func(t *testing.T) {
		const targetID = "car-yma-e2e"
		const actionType = "nudge-engine"
		bus.Publish(string(plugin.YardmasterAction), plugin.YardmasterActionEvent{
			TargetID:   targetID,
			ActionType: actionType,
		})

		ev := waitForEvent[plugin.YardmasterActionEvent](t, fp, plugin.YardmasterAction,
			func(p plugin.YardmasterActionEvent) bool {
				return p.TargetID == targetID && p.ActionType == actionType
			})
		if ev.ActionType != actionType {
			t.Errorf("ActionType = %q, want %q", ev.ActionType, actionType)
		}
	})

	// -- YardPaused ------------------------------------------------------
	// Mirrors the route handler in internal/dashboard/routes.go:
	// SetYardPaused commits the row, then bus.Publish fires.
	t.Run("YardPaused", func(t *testing.T) {
		const reason = "deploy in progress"
		if err := dashboard.SetYardPaused(gormDB, true, reason); err != nil {
			t.Fatalf("SetYardPaused(true): %v", err)
		}
		bus.Publish(string(plugin.YardPaused), plugin.YardPausedEvent{
			Reason: reason,
		})

		ev := waitForEvent[plugin.YardPausedEvent](t, fp, plugin.YardPaused,
			func(p plugin.YardPausedEvent) bool { return p.Reason == reason })
		if ev.Reason != reason {
			t.Errorf("Reason = %q, want %q", ev.Reason, reason)
		}

		// Verify DB side effect committed before the publish (matching
		// the route handler's order-of-operations contract).
		paused, persisted := dashboard.GetYardPaused(gormDB)
		if !paused || persisted != reason {
			t.Errorf("DB state = paused=%v reason=%q, want true / %q",
				paused, persisted, reason)
		}
	})

	// -- YardResumed -----------------------------------------------------
	t.Run("YardResumed", func(t *testing.T) {
		// Ensure we start paused so resume is meaningful.
		if err := dashboard.SetYardPaused(gormDB, true, "interim"); err != nil {
			t.Fatalf("SetYardPaused(true): %v", err)
		}

		const reason = "deploy finished"
		if err := dashboard.SetYardPaused(gormDB, false, reason); err != nil {
			t.Fatalf("SetYardPaused(false): %v", err)
		}
		bus.Publish(string(plugin.YardResumed), plugin.YardResumedEvent{
			Reason: reason,
		})

		ev := waitForEvent[plugin.YardResumedEvent](t, fp, plugin.YardResumed,
			func(p plugin.YardResumedEvent) bool { return p.Reason == reason })
		if ev.Reason != reason {
			t.Errorf("Reason = %q, want %q", ev.Reason, reason)
		}

		paused, _ := dashboard.GetYardPaused(gormDB)
		if paused {
			t.Error("DB state = paused=true after resume, want false")
		}
	})

	// -- Clean shutdown + goroutine leak guard ---------------------------
	// host.Stop must cancel daemons, close subscriptions through the
	// fakePlugin.Stop unsubscribe path, and return promptly. We then
	// close the bus and verify the goroutine count returned to near
	// baseline.
	t.Run("CleanShutdown", func(t *testing.T) {
		stopStart := time.Now()
		host.Stop(context.Background())
		elapsed := time.Since(stopStart)
		if elapsed > 2*time.Second {
			t.Errorf("host.Stop took %v, expected prompt shutdown", elapsed)
		}

		// Close the bus to drain subscriber goroutines we created via
		// Host.Subscribe. Unsubscribe alone (which fp.Stop performs)
		// would also work, but Close gives us a hard guarantee for the
		// goroutine leak assertion below.
		if closer, ok := bus.(interface{ Close() }); ok {
			closer.Close()
		}

		// Allow the runtime a brief moment to reap finished goroutines.
		// Polling here (rather than time.Sleep) keeps the test fast on
		// idle CI and resilient on busy CI.
		deadline := time.Now().Add(2 * time.Second)
		var got int
		for time.Now().Before(deadline) {
			got = runtime.NumGoroutine()
			if got <= goroutinesBefore+2 {
				break
			}
			time.Sleep(10 * time.Millisecond)
		}
		// Some goroutines (testing framework, GC scavenger) come and go
		// independently of our code. Allow a small slack rather than
		// asserting exact equality.
		if got > goroutinesBefore+5 {
			t.Errorf("goroutine leak: before=%d after=%d (slack=5)",
				goroutinesBefore, got)
		}
	})
}

// --- e2eFakePlugin + capture log -----------------------------------------

// e2eFakePlugin subscribes to every Phase 1 EventType via the per-plugin
// Host.Subscribe and records each delivered event into a mutex-guarded
// map keyed by EventType. Subtests read from that map via the typed
// helper waitForEvent below.
//
// Named with the e2e prefix so it does not collide with the existing
// fakePlugin in host_test.go (which serves a different purpose — basic
// lifecycle tracking).
type e2eFakePlugin struct {
	name string

	mu       sync.Mutex
	host     plugin.Host
	events   map[plugin.EventType][]any
	unsubs   []plugin.Unsubscribe
	subbedAt []plugin.EventType
}

// allEventTypes lists every Phase 1 closed-set EventType. The fake plugin
// subscribes to all of them in Start; the test asserts a delivered
// payload for each.
var allEventTypes = []plugin.EventType{
	plugin.CarCreated,
	plugin.CarClaimed,
	plugin.CarStatusChanged,
	plugin.CarMerged,
	plugin.MergeFailed,
	plugin.EngineStarted,
	plugin.EngineStopped,
	plugin.EngineStalled,
	plugin.YardmasterAction,
	plugin.YardPaused,
	plugin.YardResumed,
}

func newE2EFakePlugin(name string) *e2eFakePlugin {
	return &e2eFakePlugin{
		name:   name,
		events: make(map[plugin.EventType][]any),
	}
}

func (p *e2eFakePlugin) Name() string { return p.name }

func (p *e2eFakePlugin) Init(_ context.Context, h plugin.Host) error {
	p.mu.Lock()
	p.host = h
	p.mu.Unlock()
	return nil
}

// Start registers one subscription per EventType. Each handler does the
// per-topic type assertion the SDK guarantees (see pkg/plugin/event.go
// docs) and appends the typed payload into events[topic]. A test that
// reads an entry by EventType then performs a second type assertion to
// the expected payload struct — that final assertion is the proof that
// the bus delivered the payload with its concrete dynamic type intact.
func (p *e2eFakePlugin) Start(_ context.Context) error {
	p.mu.Lock()
	h := p.host
	p.mu.Unlock()
	if h == nil {
		// Should never happen in this test — Init runs before Start.
		return nil
	}

	for _, et := range allEventTypes {
		topic := et // capture range var
		unsub := h.Subscribe(topic, func(deliveredTopic plugin.EventType, payload any) {
			// Sanity: the SDK guarantees the delivered topic matches the
			// subscribed topic. Verify rather than trust.
			if deliveredTopic != topic {
				return
			}
			// Topic-specific concrete type assertions. Any mismatch
			// silently drops — the subtest's waitForEvent will time out
			// and surface a useful error referencing the missing topic.
			switch topic {
			case plugin.CarCreated:
				if v, ok := payload.(plugin.CarCreatedEvent); ok {
					p.record(topic, v)
				}
			case plugin.CarClaimed:
				if v, ok := payload.(plugin.CarClaimedEvent); ok {
					p.record(topic, v)
				}
			case plugin.CarStatusChanged:
				if v, ok := payload.(plugin.CarStatusChangedEvent); ok {
					p.record(topic, v)
				}
			case plugin.CarMerged:
				if v, ok := payload.(plugin.CarMergedEvent); ok {
					p.record(topic, v)
				}
			case plugin.MergeFailed:
				if v, ok := payload.(plugin.MergeFailedEvent); ok {
					p.record(topic, v)
				}
			case plugin.EngineStarted:
				if v, ok := payload.(plugin.EngineStartedEvent); ok {
					p.record(topic, v)
				}
			case plugin.EngineStopped:
				if v, ok := payload.(plugin.EngineStoppedEvent); ok {
					p.record(topic, v)
				}
			case plugin.EngineStalled:
				if v, ok := payload.(plugin.EngineStalledEvent); ok {
					p.record(topic, v)
				}
			case plugin.YardmasterAction:
				if v, ok := payload.(plugin.YardmasterActionEvent); ok {
					p.record(topic, v)
				}
			case plugin.YardPaused:
				if v, ok := payload.(plugin.YardPausedEvent); ok {
					p.record(topic, v)
				}
			case plugin.YardResumed:
				if v, ok := payload.(plugin.YardResumedEvent); ok {
					p.record(topic, v)
				}
			}
		})

		p.mu.Lock()
		p.unsubs = append(p.unsubs, unsub)
		p.subbedAt = append(p.subbedAt, topic)
		p.mu.Unlock()
	}
	return nil
}

func (p *e2eFakePlugin) Stop(_ context.Context) error {
	p.mu.Lock()
	unsubs := p.unsubs
	p.unsubs = nil
	p.mu.Unlock()
	for _, u := range unsubs {
		if u != nil {
			u()
		}
	}
	return nil
}

// record appends one payload under the given topic. Mutex-guarded so the
// fan-out from concurrent drain goroutines (one per Subscribe) is safe.
func (p *e2eFakePlugin) record(topic plugin.EventType, payload any) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.events[topic] = append(p.events[topic], payload)
}

// snapshot returns a defensive copy of the captured events for one topic.
// Callers that want to scan multiple topics should call snapshot once per
// topic; this matches how the existing yardmaster/car publish-site tests
// inspect their captureBus.
func (p *e2eFakePlugin) snapshot(topic plugin.EventType) []any {
	p.mu.Lock()
	defer p.mu.Unlock()
	src := p.events[topic]
	out := make([]any, len(src))
	copy(out, src)
	return out
}

// --- assertion helpers ---------------------------------------------------

// waitForEvent polls the fakePlugin for an event matching `match` on the
// given topic, type-asserts the payload to T, and returns it. Fails the
// subtest if no matching event arrives within the polling budget.
//
// Generic over T so each subtest can declare its expected payload type
// at the call site. The polling cadence (10ms) matches the budget recipe
// from the existing host_integration_test.go waitFor helper.
func waitForEvent[T any](t *testing.T, fp *e2eFakePlugin, topic plugin.EventType, match func(T) bool) T {
	t.Helper()
	const (
		timeout = 2 * time.Second
		step    = 10 * time.Millisecond
	)
	deadline := time.Now().Add(timeout)
	var zero T
	for time.Now().Before(deadline) {
		for _, raw := range fp.snapshot(topic) {
			ev, ok := raw.(T)
			if !ok {
				continue
			}
			if match == nil || match(ev) {
				return ev
			}
		}
		time.Sleep(step)
	}
	// One more check after the deadline (covers cooperative-scheduling
	// hiccups on busy CI).
	for _, raw := range fp.snapshot(topic) {
		if ev, ok := raw.(T); ok && (match == nil || match(ev)) {
			return ev
		}
	}
	t.Fatalf("timed out waiting for matching %s event after %v (captured=%d)",
		topic, timeout, len(fp.snapshot(topic)))
	return zero
}

// --- DB harness ----------------------------------------------------------

// newTestDB returns an in-memory SQLite database migrated with every
// table the publish-site code paths touch. Mirrors the pattern from
// internal/db/db_test.go and internal/yardmaster/rebalance_test.go —
// SetMaxOpenConns(1) keeps every query on the same in-memory connection
// so concurrent goroutines see consistent state.
func newTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	gormDB, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	sqlDB, err := gormDB.DB()
	if err != nil {
		t.Fatalf("get sql.DB: %v", err)
	}
	sqlDB.SetMaxOpenConns(1)
	if err := db.AutoMigrate(gormDB); err != nil {
		t.Fatalf("auto-migrate: %v", err)
	}
	return gormDB
}
