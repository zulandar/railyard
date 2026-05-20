package yardmaster

import (
	"bytes"
	"context"
	"sync"
	"testing"

	"github.com/zulandar/railyard/internal/config"
	"github.com/zulandar/railyard/internal/engine"
	"github.com/zulandar/railyard/internal/events"
	"github.com/zulandar/railyard/internal/models"
	"github.com/zulandar/railyard/pkg/plugin"
)

// fakeBus is a minimal events.Bus stub that records every Publish call.
// Used by the action / switch tests to assert publish sites without standing
// up the full in-memory bus and its drain goroutines.
type fakeBus struct {
	mu     sync.Mutex
	events []fakeEvent
}

type fakeEvent struct {
	Topic   string
	Payload any
}

func (f *fakeBus) Publish(topic string, payload any) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.events = append(f.events, fakeEvent{Topic: topic, Payload: payload})
}

func (f *fakeBus) Subscribe(topic string, h events.Handler) events.Unsubscribe {
	// Subscribe is unused by publish-site tests; return a no-op unsubscribe.
	return func() {}
}

// snapshot returns a copy of the recorded events so callers can iterate
// without holding the mutex.
func (f *fakeBus) snapshot() []fakeEvent {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]fakeEvent, len(f.events))
	copy(out, f.events)
	return out
}

// hasYardmasterAction asserts a YardmasterAction event was published with the
// expected ActionType and TargetID.
func (f *fakeBus) hasYardmasterAction(targetID, actionType string) bool {
	for _, e := range f.snapshot() {
		if e.Topic != string(plugin.YardmasterAction) {
			continue
		}
		ev, ok := e.Payload.(plugin.YardmasterActionEvent)
		if !ok {
			continue
		}
		if ev.TargetID == targetID && ev.ActionType == actionType {
			return true
		}
	}
	return false
}

// --- handle*WithBus action publish sites ---

func TestHandleRetryMergeWithBus_PublishesAction(t *testing.T) {
	db := testDB(t)
	db.Create(&models.Car{ID: "car-rm1", Type: "task", Status: "merge-failed", Track: "backend"})

	bus := &fakeBus{}
	var buf bytes.Buffer
	logger := actTestLogger(&buf)

	msg := models.Message{Subject: "retry-merge", CarID: "car-rm1", Body: "fixed"}
	handleRetryMergeWithBus(db, msg, logger, bus)

	if !bus.hasYardmasterAction("car-rm1", "retry-merge") {
		t.Fatalf("expected YardmasterAction with target=car-rm1 type=retry-merge; got %+v", bus.snapshot())
	}
}

func TestHandleRetryMergeWithBus_EpicPublishesAction(t *testing.T) {
	db := testDB(t)
	epicID := "epic-rm1"
	db.Create(&models.Car{ID: epicID, Type: "epic", Status: "open", Track: "backend"})

	bus := &fakeBus{}
	var buf bytes.Buffer
	logger := actTestLogger(&buf)

	msg := models.Message{Subject: "retry-merge", CarID: epicID, Body: "all children merged"}
	handleRetryMergeWithBus(db, msg, logger, bus)

	if !bus.hasYardmasterAction(epicID, "retry-merge") {
		t.Fatalf("expected YardmasterAction for epic; got %+v", bus.snapshot())
	}
}

func TestHandleRetryMergeWithBus_NoCarID_NoPublish(t *testing.T) {
	bus := &fakeBus{}
	var buf bytes.Buffer
	logger := actTestLogger(&buf)

	handleRetryMergeWithBus(nil, models.Message{Subject: "retry-merge"}, logger, bus)

	if len(bus.snapshot()) != 0 {
		t.Fatalf("expected no events for empty car-id; got %+v", bus.snapshot())
	}
}

func TestHandleRequeueCarWithBus_PublishesAction(t *testing.T) {
	db := testDB(t)
	db.Create(&models.Car{ID: "car-rq1", Type: "task", Status: "open", Track: "backend"})

	bus := &fakeBus{}
	var buf bytes.Buffer
	logger := actTestLogger(&buf)

	msg := models.Message{Subject: "requeue-car", CarID: "car-rq1", Body: "from scratch"}
	handleRequeueCarWithBus(db, msg, logger, bus)

	if !bus.hasYardmasterAction("car-rq1", "requeue-car") {
		t.Fatalf("expected requeue-car YardmasterAction; got %+v", bus.snapshot())
	}
}

func TestHandleUnblockCarWithBus_PublishesAction(t *testing.T) {
	db := testDB(t)
	db.Create(&models.Car{ID: "car-ub1", Type: "task", Status: "blocked", Track: "backend"})

	bus := &fakeBus{}
	var buf bytes.Buffer
	logger := actTestLogger(&buf)

	msg := models.Message{Subject: "unblock-car", CarID: "car-ub1", Body: "dep ready"}
	handleUnblockCarWithBus(db, msg, logger, bus)

	if !bus.hasYardmasterAction("car-ub1", "unblock-car") {
		t.Fatalf("expected unblock-car YardmasterAction; got %+v", bus.snapshot())
	}
}

func TestHandleUnblockCarWithBus_NotBlocked_NoPublish(t *testing.T) {
	db := testDB(t)
	// car exists but is not blocked — the handler skips the DB update and
	// must not publish either.
	db.Create(&models.Car{ID: "car-ub2", Type: "task", Status: "open", Track: "backend"})

	bus := &fakeBus{}
	var buf bytes.Buffer
	logger := actTestLogger(&buf)

	handleUnblockCarWithBus(db, models.Message{Subject: "unblock-car", CarID: "car-ub2"}, logger, bus)

	if len(bus.snapshot()) != 0 {
		t.Fatalf("expected no publish when car is not blocked; got %+v", bus.snapshot())
	}
}

func TestHandleCloseEpicWithBus_PublishesAction(t *testing.T) {
	db := testDB(t)
	epicID := "epic-ce1"
	db.Create(&models.Car{ID: epicID, Type: "epic", Status: "open", Track: "backend"})

	bus := &fakeBus{}
	var buf bytes.Buffer
	logger := actTestLogger(&buf)

	handleCloseEpicWithBus(db, models.Message{Subject: "close-epic", CarID: epicID}, logger, bus)

	if !bus.hasYardmasterAction(epicID, "close-epic") {
		t.Fatalf("expected close-epic YardmasterAction; got %+v", bus.snapshot())
	}
}

func TestHandleNudgeEngineWithBus_NoEngine_NoPublish(t *testing.T) {
	db := testDB(t)
	// No engine assigned to the car — handler logs and returns without publishing.
	db.Create(&models.Car{ID: "car-nu1", Type: "task", Status: "open", Track: "backend"})

	bus := &fakeBus{}
	var buf bytes.Buffer
	logger := actTestLogger(&buf)

	handleNudgeEngineWithBus(db, models.Message{Subject: "nudge-engine", CarID: "car-nu1", Body: "try X"}, logger, bus)

	if len(bus.snapshot()) != 0 {
		t.Fatalf("expected no publish when no engine assigned; got %+v", bus.snapshot())
	}
}

func TestHandleNudgeEngineWithBus_PublishesAction(t *testing.T) {
	db := testDB(t)
	db.Create(&models.Engine{ID: "eng-nu1", Track: "backend", Status: engine.StatusWorking, CurrentCar: "car-nu2"})
	db.Create(&models.Car{ID: "car-nu2", Type: "task", Status: "claimed", Track: "backend", Assignee: "eng-nu1"})

	bus := &fakeBus{}
	var buf bytes.Buffer
	logger := actTestLogger(&buf)

	handleNudgeEngineWithBus(db, models.Message{Subject: "nudge-engine", CarID: "car-nu2", Body: "try X"}, logger, bus)

	if !bus.hasYardmasterAction("car-nu2", "nudge-engine") {
		t.Fatalf("expected nudge-engine YardmasterAction; got %+v", bus.snapshot())
	}
}

// --- Switch publish sites ---

func TestSwitch_PublishesCarMerged_FullMerge(t *testing.T) {
	// Full-merge path: build a real git repo + remote so Switch's git plumbing
	// succeeds end-to-end, then assert CarMerged fires.
	repoDir, _, run := initTestRepoWithRemote(t)

	run(repoDir, "git", "checkout", "-b", "ry/alice/backend/car-bm1")
	writeFile(t, repoDir, "feature-bm1.txt", "merged event")
	run(repoDir, "git", "add", "feature-bm1.txt")
	run(repoDir, "git", "commit", "-m", "feature work")
	run(repoDir, "git", "checkout", "main")

	db := testDB(t)
	db.Create(&models.Car{
		ID:     "car-bm1",
		Title:  "Bus merged test",
		Track:  "backend",
		Branch: "ry/alice/backend/car-bm1",
		Status: "done",
	})

	bus := &fakeBus{}
	result, err := Switch(db, "car-bm1", SwitchOpts{
		RepoDir:     repoDir,
		TestCommand: "true",
		Bus:         bus,
	})
	if err != nil {
		t.Fatalf("Switch error: %v", err)
	}
	if !result.Merged {
		t.Fatalf("expected Merged=true; result=%+v", result)
	}

	for _, e := range bus.snapshot() {
		if e.Topic != string(plugin.CarMerged) {
			continue
		}
		ev, ok := e.Payload.(plugin.CarMergedEvent)
		if !ok {
			t.Fatalf("CarMerged payload wrong type: %T", e.Payload)
		}
		if ev.CarID != "car-bm1" || ev.Branch != "ry/alice/backend/car-bm1" {
			t.Fatalf("CarMerged payload wrong: %+v", ev)
		}
		return
	}
	t.Fatalf("CarMerged event missing; recorded: %+v", bus.snapshot())
}

func TestSwitch_PublishesMergeFailed_PushFailure(t *testing.T) {
	// Repo without a remote — gitPush fails after merge, triggering the
	// push-failure publish path. The merge is reverted locally.
	repoDir, run := initTestRepo(t)

	run("git", "checkout", "-b", "ry/alice/backend/car-mf1")
	writeFile(t, repoDir, "feature-mf1.txt", "push will fail")
	run("git", "add", "feature-mf1.txt")
	run("git", "commit", "-m", "feature work")
	run("git", "checkout", "main")

	db := testDB(t)
	db.Create(&models.Car{
		ID:     "car-mf1",
		Title:  "Merge fail test",
		Track:  "backend",
		Branch: "ry/alice/backend/car-mf1",
		Status: "done",
	})

	bus := &fakeBus{}
	result, err := Switch(db, "car-mf1", SwitchOpts{
		RepoDir:     repoDir,
		TestCommand: "true",
		Bus:         bus,
	})
	if err == nil {
		t.Fatal("expected push failure error")
	}
	if result.FailureCategory != SwitchFailPush {
		t.Fatalf("FailureCategory = %q; want %q", result.FailureCategory, SwitchFailPush)
	}

	// At least one MergeFailed event must be present.
	for _, e := range bus.snapshot() {
		if e.Topic == string(plugin.MergeFailed) {
			ev, ok := e.Payload.(plugin.MergeFailedEvent)
			if !ok {
				t.Fatalf("MergeFailed payload wrong type: %T", e.Payload)
			}
			if ev.CarID != "car-mf1" {
				t.Fatalf("MergeFailed CarID = %q; want car-mf1", ev.CarID)
			}
			if ev.Reason == "" {
				t.Fatalf("MergeFailed Reason was empty")
			}
			return
		}
	}
	t.Fatalf("MergeFailed event missing; recorded: %+v", bus.snapshot())
}

// --- handleCompletedCarsWithBus publishes the "merge" intent ---

func TestHandleCompletedCarsWithBus_PublishesMergeAction(t *testing.T) {
	// We don't need a real repo — we just need the YardmasterAction(merge)
	// publish, which fires BEFORE the Switch call. Use a bogus repo dir so
	// Switch fails fast (still after the publish).
	db := testDB(t)
	db.Create(&models.Car{
		ID:     "car-act1",
		Title:  "Merge action",
		Track:  "backend",
		Branch: "ry/alice/backend/car-act1",
		Status: "done",
	})

	bus := &fakeBus{}
	var buf bytes.Buffer
	logger := actTestLogger(&buf)
	cfg := testConfig(config.TrackConfig{Name: "backend", Language: "go"})

	err := handleCompletedCarsWithBus(
		context.Background(), db, cfg, "", "/nonexistent", "/nonexistent",
		&sync.WaitGroup{}, nil, make(chan struct{}, 1),
		logger, bus,
	)
	// Switch will error on the fake repo path; that's expected.
	_ = err

	if !bus.hasYardmasterAction("car-act1", "merge") {
		t.Fatalf("expected merge YardmasterAction for car-act1; got %+v", bus.snapshot())
	}
}
