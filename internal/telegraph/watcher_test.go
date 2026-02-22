package telegraph

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/zulandar/railyard/internal/models"
	"github.com/zulandar/railyard/internal/orchestration"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// mockStatusProvider implements StatusProvider for testing pulse digest.
type mockStatusProvider struct {
	info *orchestration.StatusInfo
	err  error
}

func (m *mockStatusProvider) Status() (*orchestration.StatusInfo, error) {
	if m.err != nil {
		return nil, m.err
	}
	return m.info, nil
}

func openWatcherTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	if err := db.AutoMigrate(
		&models.Car{},
		&models.Engine{},
		&models.Message{},
		&models.Track{},
		&models.DispatchSession{},
		&models.TelegraphConversation{},
	); err != nil {
		t.Fatalf("auto migrate: %v", err)
	}
	return db
}

// --- NewWatcher tests ---

func TestNewWatcher_NilDB(t *testing.T) {
	_, err := NewWatcher(WatcherOpts{})
	if err == nil {
		t.Fatal("expected error for nil db")
	}
}

func TestNewWatcher_Defaults(t *testing.T) {
	db := openWatcherTestDB(t)
	w, err := NewWatcher(WatcherOpts{DB: db})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if w.pollInterval != DefaultPollInterval {
		t.Errorf("poll interval = %v, want %v", w.pollInterval, DefaultPollInterval)
	}
	if w.pulseInterval != DefaultPulseInterval {
		t.Errorf("pulse interval = %v, want %v", w.pulseInterval, DefaultPulseInterval)
	}
}

func TestNewWatcher_CustomIntervals(t *testing.T) {
	db := openWatcherTestDB(t)
	w, err := NewWatcher(WatcherOpts{
		DB:            db,
		PollInterval:  5 * time.Second,
		PulseInterval: 10 * time.Minute,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if w.pollInterval != 5*time.Second {
		t.Errorf("poll interval = %v, want 5s", w.pollInterval)
	}
	if w.pulseInterval != 10*time.Minute {
		t.Errorf("pulse interval = %v, want 10m", w.pulseInterval)
	}
}

// --- detectCarEvents tests ---

func TestDetectCarEvents_FirstPollSeedsSnapshot(t *testing.T) {
	db := openWatcherTestDB(t)
	db.Create(&models.Car{ID: "car-1", Title: "First car", Status: "open", Track: "backend"})
	db.Create(&models.Car{ID: "car-2", Title: "Second car", Status: "in_progress", Track: "frontend"})

	w, _ := NewWatcher(WatcherOpts{DB: db})

	events, err := w.detectCarEvents()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// First poll should seed snapshot without emitting events.
	if len(events) != 0 {
		t.Errorf("expected 0 events on first poll, got %d", len(events))
	}
	if !w.Seeded() {
		t.Error("expected watcher to be seeded after first poll")
	}
	snap := w.Snapshot()
	if len(snap) != 2 {
		t.Errorf("snapshot size = %d, want 2", len(snap))
	}
}

func TestDetectCarEvents_StatusChange(t *testing.T) {
	db := openWatcherTestDB(t)
	db.Create(&models.Car{ID: "car-1", Title: "First car", Status: "open", Track: "backend"})

	w, _ := NewWatcher(WatcherOpts{DB: db})

	// Seed.
	w.detectCarEvents()

	// Change status.
	db.Model(&models.Car{}).Where("id = ?", "car-1").Update("status", "in_progress")

	events, err := w.detectCarEvents()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	e := events[0]
	if e.Type != EventCarStatusChange {
		t.Errorf("type = %v, want %v", e.Type, EventCarStatusChange)
	}
	if e.CarID != "car-1" {
		t.Errorf("car id = %q, want %q", e.CarID, "car-1")
	}
	if e.OldStatus != "open" {
		t.Errorf("old status = %q, want %q", e.OldStatus, "open")
	}
	if e.NewStatus != "in_progress" {
		t.Errorf("new status = %q, want %q", e.NewStatus, "in_progress")
	}
}

func TestDetectCarEvents_NoChangeNoDuplicate(t *testing.T) {
	db := openWatcherTestDB(t)
	db.Create(&models.Car{ID: "car-1", Title: "First car", Status: "open", Track: "backend"})

	w, _ := NewWatcher(WatcherOpts{DB: db})

	// Seed.
	w.detectCarEvents()

	// Poll again without changing anything.
	events, err := w.detectCarEvents()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(events) != 0 {
		t.Errorf("expected 0 events for unchanged car, got %d", len(events))
	}
}

func TestDetectCarEvents_NewCarAfterSeed(t *testing.T) {
	db := openWatcherTestDB(t)
	db.Create(&models.Car{ID: "car-1", Title: "First car", Status: "open", Track: "backend"})

	w, _ := NewWatcher(WatcherOpts{DB: db})

	// Seed.
	w.detectCarEvents()

	// Add a new car.
	db.Create(&models.Car{ID: "car-2", Title: "New car", Status: "draft", Track: "backend"})

	events, err := w.detectCarEvents()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("expected 1 event for new car, got %d", len(events))
	}
	if events[0].CarID != "car-2" {
		t.Errorf("car id = %q, want %q", events[0].CarID, "car-2")
	}
	if events[0].OldStatus != "" {
		t.Errorf("old status = %q, want empty", events[0].OldStatus)
	}
	if events[0].NewStatus != "draft" {
		t.Errorf("new status = %q, want %q", events[0].NewStatus, "draft")
	}
}

func TestDetectCarEvents_MultipleChanges(t *testing.T) {
	db := openWatcherTestDB(t)
	db.Create(&models.Car{ID: "car-1", Title: "First", Status: "open", Track: "backend"})
	db.Create(&models.Car{ID: "car-2", Title: "Second", Status: "open", Track: "frontend"})
	db.Create(&models.Car{ID: "car-3", Title: "Third", Status: "in_progress", Track: "backend"})

	w, _ := NewWatcher(WatcherOpts{DB: db})
	w.detectCarEvents() // seed

	// Change two cars.
	db.Model(&models.Car{}).Where("id = ?", "car-1").Update("status", "in_progress")
	db.Model(&models.Car{}).Where("id = ?", "car-3").Update("status", "done")

	events, err := w.detectCarEvents()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("expected 2 events, got %d", len(events))
	}
}

func TestDetectCarEvents_DeletedCarRemovedFromSnapshot(t *testing.T) {
	db := openWatcherTestDB(t)
	db.Create(&models.Car{ID: "car-1", Title: "First", Status: "open", Track: "backend"})

	w, _ := NewWatcher(WatcherOpts{DB: db})
	w.detectCarEvents() // seed

	if len(w.Snapshot()) != 1 {
		t.Fatalf("snapshot should have 1 car")
	}

	// Delete the car.
	db.Delete(&models.Car{}, "id = ?", "car-1")

	w.detectCarEvents()

	snap := w.Snapshot()
	if len(snap) != 0 {
		t.Errorf("snapshot should be empty after car deleted, got %d", len(snap))
	}
}

// --- detectStalls tests ---

func TestDetectStalls_NoStalledEngines(t *testing.T) {
	db := openWatcherTestDB(t)
	db.Create(&models.Engine{ID: "eng-1", Status: "working", Track: "backend"})

	w, _ := NewWatcher(WatcherOpts{DB: db})
	events, err := w.detectStalls()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(events) != 0 {
		t.Errorf("expected 0 stall events, got %d", len(events))
	}
}

func TestDetectStalls_StalledEngine(t *testing.T) {
	db := openWatcherTestDB(t)
	db.Create(&models.Engine{ID: "eng-1", Status: "stalled", Track: "backend", CurrentCar: "car-1"})

	w, _ := NewWatcher(WatcherOpts{DB: db})
	events, err := w.detectStalls()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("expected 1 stall event, got %d", len(events))
	}
	e := events[0]
	if e.Type != EventEngineStalled {
		t.Errorf("type = %v, want %v", e.Type, EventEngineStalled)
	}
	if e.EngineID != "eng-1" {
		t.Errorf("engine id = %q, want %q", e.EngineID, "eng-1")
	}
	if e.Track != "backend" {
		t.Errorf("track = %q, want %q", e.Track, "backend")
	}
	if e.CurrentCar != "car-1" {
		t.Errorf("current car = %q, want %q", e.CurrentCar, "car-1")
	}
}

func TestDetectStalls_MultipleStalledEngines(t *testing.T) {
	db := openWatcherTestDB(t)
	db.Create(&models.Engine{ID: "eng-1", Status: "stalled", Track: "backend"})
	db.Create(&models.Engine{ID: "eng-2", Status: "stalled", Track: "frontend"})
	db.Create(&models.Engine{ID: "eng-3", Status: "working", Track: "backend"})

	w, _ := NewWatcher(WatcherOpts{DB: db})
	events, err := w.detectStalls()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(events) != 2 {
		t.Errorf("expected 2 stall events, got %d", len(events))
	}
}

// --- detectEscalations tests ---

func TestDetectEscalations_NoMessages(t *testing.T) {
	db := openWatcherTestDB(t)

	w, _ := NewWatcher(WatcherOpts{DB: db})
	events, err := w.detectEscalations()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(events) != 0 {
		t.Errorf("expected 0 escalation events, got %d", len(events))
	}
}

func TestDetectEscalations_HumanMessage(t *testing.T) {
	db := openWatcherTestDB(t)
	db.Create(&models.Message{
		FromAgent:    "yardmaster",
		ToAgent:      "human",
		CarID:        "car-1",
		Subject:      "Engine stalled",
		Body:         "Engine eng-1 has stalled on car-1",
		Priority:     "high",
		Acknowledged: false,
	})

	w, _ := NewWatcher(WatcherOpts{DB: db})
	events, err := w.detectEscalations()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("expected 1 escalation event, got %d", len(events))
	}
	e := events[0]
	if e.Type != EventEscalation {
		t.Errorf("type = %v, want %v", e.Type, EventEscalation)
	}
	if e.FromAgent != "yardmaster" {
		t.Errorf("from agent = %q, want %q", e.FromAgent, "yardmaster")
	}
	if e.Subject != "Engine stalled" {
		t.Errorf("subject = %q, want %q", e.Subject, "Engine stalled")
	}
	if e.Priority != "high" {
		t.Errorf("priority = %q, want %q", e.Priority, "high")
	}
}

func TestDetectEscalations_TelegraphMessage(t *testing.T) {
	db := openWatcherTestDB(t)
	db.Create(&models.Message{
		FromAgent:    "engine-1",
		ToAgent:      "telegraph",
		Subject:      "Need help",
		Body:         "Stuck on merge conflict",
		Acknowledged: false,
	})

	w, _ := NewWatcher(WatcherOpts{DB: db})
	events, err := w.detectEscalations()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("expected 1 escalation, got %d", len(events))
	}
	if events[0].FromAgent != "engine-1" {
		t.Errorf("from = %q, want %q", events[0].FromAgent, "engine-1")
	}
}

func TestDetectEscalations_MarksAcknowledged(t *testing.T) {
	db := openWatcherTestDB(t)
	db.Create(&models.Message{
		FromAgent:    "yardmaster",
		ToAgent:      "human",
		Subject:      "Test",
		Body:         "Test body",
		Acknowledged: false,
	})

	w, _ := NewWatcher(WatcherOpts{DB: db})

	// First call picks up the message.
	events, err := w.detectEscalations()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}

	// Second call should find nothing — message is acknowledged.
	events2, err := w.detectEscalations()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(events2) != 0 {
		t.Errorf("expected 0 events after acknowledgement, got %d", len(events2))
	}
}

func TestDetectEscalations_IgnoresOtherAgents(t *testing.T) {
	db := openWatcherTestDB(t)
	// Message to a regular agent — should be ignored.
	db.Create(&models.Message{
		FromAgent:    "yardmaster",
		ToAgent:      "engine-1",
		Subject:      "Task",
		Body:         "Work on this",
		Acknowledged: false,
	})

	w, _ := NewWatcher(WatcherOpts{DB: db})
	events, err := w.detectEscalations()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(events) != 0 {
		t.Errorf("expected 0 events for non-human/telegraph message, got %d", len(events))
	}
}

func TestDetectEscalations_IgnoresAcknowledged(t *testing.T) {
	db := openWatcherTestDB(t)
	db.Create(&models.Message{
		FromAgent:    "yardmaster",
		ToAgent:      "human",
		Subject:      "Already acked",
		Acknowledged: true,
	})

	w, _ := NewWatcher(WatcherOpts{DB: db})
	events, err := w.detectEscalations()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(events) != 0 {
		t.Errorf("expected 0 events for already-acknowledged message, got %d", len(events))
	}
}

// --- Poll integration test ---

func TestPoll_CombinesAllEventTypes(t *testing.T) {
	db := openWatcherTestDB(t)

	// Seed some cars.
	db.Create(&models.Car{ID: "car-1", Title: "First", Status: "open", Track: "backend"})

	w, _ := NewWatcher(WatcherOpts{DB: db})

	// First poll: seeds snapshot.
	events, err := w.Poll(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(events) != 0 {
		t.Errorf("first poll should return 0 events, got %d", len(events))
	}

	// Set up changes for second poll.
	db.Model(&models.Car{}).Where("id = ?", "car-1").Update("status", "done")
	db.Create(&models.Engine{ID: "eng-1", Status: "stalled", Track: "backend"})
	db.Create(&models.Message{
		FromAgent:    "yardmaster",
		ToAgent:      "human",
		Subject:      "Help",
		Acknowledged: false,
	})

	events, err = w.Poll(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Expect: 1 car change + 1 stall + 1 escalation = 3.
	if len(events) != 3 {
		t.Errorf("expected 3 events, got %d", len(events))
	}

	// Verify event types.
	typeCounts := map[EventType]int{}
	for _, e := range events {
		typeCounts[e.Type]++
	}
	if typeCounts[EventCarStatusChange] != 1 {
		t.Errorf("car events = %d, want 1", typeCounts[EventCarStatusChange])
	}
	if typeCounts[EventEngineStalled] != 1 {
		t.Errorf("stall events = %d, want 1", typeCounts[EventEngineStalled])
	}
	if typeCounts[EventEscalation] != 1 {
		t.Errorf("escalation events = %d, want 1", typeCounts[EventEscalation])
	}
}

// --- Run loop test ---

func TestRun_EmitsEventsAndStopsOnCancel(t *testing.T) {
	db := openWatcherTestDB(t)
	db.Create(&models.Car{ID: "car-1", Title: "First", Status: "open", Track: "backend"})

	w, _ := NewWatcher(WatcherOpts{DB: db, PollInterval: 50 * time.Millisecond})

	ctx, cancel := context.WithCancel(context.Background())
	ch := w.Run(ctx)

	// Wait for seed poll (no events).
	time.Sleep(80 * time.Millisecond)

	// Change a car status.
	db.Model(&models.Car{}).Where("id = ?", "car-1").Update("status", "done")

	// Wait for next poll to detect the change.
	var received []DetectedEvent
	timeout := time.After(500 * time.Millisecond)
	for {
		select {
		case e, ok := <-ch:
			if !ok {
				goto done
			}
			received = append(received, e)
			if len(received) >= 1 {
				goto done
			}
		case <-timeout:
			goto done
		}
	}
done:
	cancel()

	// Drain remaining events after cancel.
	for range ch {
	}

	if len(received) < 1 {
		t.Errorf("expected at least 1 event from Run, got %d", len(received))
	}
}

// --- BuildPulse tests ---

func activeStatusInfo() *orchestration.StatusInfo {
	return &orchestration.StatusInfo{
		Engines: []orchestration.EngineInfo{
			{ID: "eng-1", Status: "working"},
			{ID: "eng-2", Status: "idle"},
		},
		TrackSummary: []orchestration.TrackSummary{
			{Track: "backend", InProgress: 2, Ready: 3, Done: 5, Blocked: 1},
		},
	}
}

func TestBuildPulse_EmitsWhenActive(t *testing.T) {
	db := openWatcherTestDB(t)
	sp := &mockStatusProvider{info: activeStatusInfo()}
	w, _ := NewWatcher(WatcherOpts{DB: db, StatusProvider: sp})

	pulse, err := w.BuildPulse()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if pulse == nil {
		t.Fatal("expected pulse event, got nil")
	}
	if pulse.Type != EventPulse {
		t.Errorf("type = %v, want %v", pulse.Type, EventPulse)
	}
	if pulse.Title != "Railyard Pulse" {
		t.Errorf("title = %q, want 'Railyard Pulse'", pulse.Title)
	}
}

func TestBuildPulse_SuppressedWhenIdle(t *testing.T) {
	db := openWatcherTestDB(t)
	sp := &mockStatusProvider{
		info: &orchestration.StatusInfo{
			Engines: []orchestration.EngineInfo{
				{ID: "eng-1", Status: "idle"},
			},
			TrackSummary: []orchestration.TrackSummary{
				{Track: "backend", Done: 10}, // nothing active or ready
			},
		},
	}
	w, _ := NewWatcher(WatcherOpts{DB: db, StatusProvider: sp})

	pulse, err := w.BuildPulse()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if pulse != nil {
		t.Errorf("expected nil (suppressed) pulse when idle, got %v", pulse)
	}
}

func TestBuildPulse_SuppressedWhenNoChange(t *testing.T) {
	db := openWatcherTestDB(t)
	sp := &mockStatusProvider{info: activeStatusInfo()}
	w, _ := NewWatcher(WatcherOpts{DB: db, StatusProvider: sp})

	// First pulse emits.
	pulse1, _ := w.BuildPulse()
	if pulse1 == nil {
		t.Fatal("first pulse should not be nil")
	}

	// Same data — should be suppressed.
	pulse2, err := w.BuildPulse()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if pulse2 != nil {
		t.Errorf("expected nil (suppressed) when nothing changed, got %v", pulse2)
	}
}

func TestBuildPulse_EmitsWhenStatusChanges(t *testing.T) {
	db := openWatcherTestDB(t)
	info := activeStatusInfo()
	sp := &mockStatusProvider{info: info}
	w, _ := NewWatcher(WatcherOpts{DB: db, StatusProvider: sp})

	// First pulse.
	w.BuildPulse()

	// Change status data.
	sp.info = &orchestration.StatusInfo{
		Engines: []orchestration.EngineInfo{
			{ID: "eng-1", Status: "working"},
			{ID: "eng-2", Status: "working"}, // was idle, now working
		},
		TrackSummary: []orchestration.TrackSummary{
			{Track: "backend", InProgress: 3, Ready: 2, Done: 5, Blocked: 1}, // active changed
		},
	}

	pulse, err := w.BuildPulse()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if pulse == nil {
		t.Fatal("expected pulse after status change, got nil")
	}
}

func TestBuildPulse_ResumesAfterIdle(t *testing.T) {
	db := openWatcherTestDB(t)
	// Start idle.
	sp := &mockStatusProvider{
		info: &orchestration.StatusInfo{
			TrackSummary: []orchestration.TrackSummary{
				{Track: "backend", Done: 5},
			},
		},
	}
	w, _ := NewWatcher(WatcherOpts{DB: db, StatusProvider: sp})

	// Suppressed because idle.
	pulse1, _ := w.BuildPulse()
	if pulse1 != nil {
		t.Fatal("expected nil when idle")
	}

	// New work appears.
	sp.info = activeStatusInfo()

	pulse2, err := w.BuildPulse()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if pulse2 == nil {
		t.Fatal("expected pulse when work resumes, got nil")
	}
}

func TestBuildPulse_ErrorFromProvider(t *testing.T) {
	db := openWatcherTestDB(t)
	sp := &mockStatusProvider{err: fmt.Errorf("connection lost")}
	w, _ := NewWatcher(WatcherOpts{DB: db, StatusProvider: sp})

	_, err := w.BuildPulse()
	if err == nil {
		t.Fatal("expected error when status provider fails")
	}
}

func TestBuildPulse_UpdatesLastPulseAt(t *testing.T) {
	db := openWatcherTestDB(t)
	sp := &mockStatusProvider{info: activeStatusInfo()}
	w, _ := NewWatcher(WatcherOpts{DB: db, StatusProvider: sp})

	before := time.Now()
	w.BuildPulse()
	after := time.Now()

	lastPulse := w.LastPulseAt()
	if lastPulse.Before(before) || lastPulse.After(after) {
		t.Errorf("lastPulseAt = %v, expected between %v and %v", lastPulse, before, after)
	}
}

func TestBuildDigest_ComputesCorrectly(t *testing.T) {
	info := &orchestration.StatusInfo{
		Engines: []orchestration.EngineInfo{
			{ID: "eng-1", Status: "working"},
			{ID: "eng-2", Status: "idle"},
			{ID: "eng-3", Status: "working"},
		},
		TrackSummary: []orchestration.TrackSummary{
			{Track: "backend", InProgress: 2, Ready: 3, Done: 5, Blocked: 1},
			{Track: "frontend", InProgress: 1, Ready: 1, Done: 2, Blocked: 0},
		},
	}

	d := buildDigest(info)
	if d.EngineCount != 3 {
		t.Errorf("engine count = %d, want 3", d.EngineCount)
	}
	if d.Working != 2 {
		t.Errorf("working = %d, want 2", d.Working)
	}
	if d.TotalActive != 3 {
		t.Errorf("total active = %d, want 3", d.TotalActive)
	}
	if d.TotalReady != 4 {
		t.Errorf("total ready = %d, want 4", d.TotalReady)
	}
	if d.TotalDone != 7 {
		t.Errorf("total done = %d, want 7", d.TotalDone)
	}
	if d.TotalBlocked != 1 {
		t.Errorf("total blocked = %d, want 1", d.TotalBlocked)
	}
}
