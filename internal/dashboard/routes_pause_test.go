package dashboard

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/zulandar/railyard/internal/events"
	"github.com/zulandar/railyard/pkg/plugin"
	"gorm.io/gorm"
)

// fakeBus is a minimal events.Bus stub that records every Publish call.
// Defined privately per-test-file so dashboard tests don't depend on the
// internal/events implementation or the yardmaster fakeBus.
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
	return func() {}
}

func (f *fakeBus) snapshot() []fakeEvent {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]fakeEvent, len(f.events))
	copy(out, f.events)
	return out
}

// setupPauseRouter is a variant of setupDBRouter that wires the dashboard
// with a fakeBus so pause/resume route tests can assert publishes.
func setupPauseRouter(t *testing.T) (*gorm.DB, *fakeBus, string, func()) {
	t.Helper()

	db := testDB(t)
	bus := &fakeBus{}

	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(gin.Recovery())

	tmpl, err := parseTemplates()
	if err != nil {
		t.Fatalf("parse templates: %v", err)
	}
	router.SetHTMLTemplate(tmpl)
	registerRoutesWithBus(router, db, "testproject", bus)

	port := findFreePort()
	srv := &http.Server{Addr: fmt.Sprintf(":%d", port), Handler: router}
	go srv.ListenAndServe()

	baseURL := fmt.Sprintf("http://localhost:%d", port)
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := http.Get(baseURL + "/static/style.css")
		if err == nil {
			resp.Body.Close()
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	return db, bus, baseURL, func() { srv.Close() }
}

func TestRoutePauseYard_PersistsAndPublishes(t *testing.T) {
	db, bus, baseURL, cleanup := setupPauseRouter(t)
	defer cleanup()

	body := bytes.NewBufferString(`{"reason":"deploy in progress"}`)
	resp, err := http.Post(baseURL+"/api/yard/pause", "application/json", body)
	if err != nil {
		t.Fatalf("POST /api/yard/pause: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	var payload map[string]string
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if payload["status"] != "paused" {
		t.Fatalf("status = %q, want paused", payload["status"])
	}
	if payload["reason"] != "deploy in progress" {
		t.Fatalf("reason = %q, want %q", payload["reason"], "deploy in progress")
	}

	// Verify DB state.
	paused, reason := GetYardPaused(db)
	if !paused {
		t.Error("expected paused=true after pause endpoint")
	}
	if reason != "deploy in progress" {
		t.Errorf("persisted reason = %q, want %q", reason, "deploy in progress")
	}

	// Verify publish.
	events := bus.snapshot()
	if len(events) != 1 {
		t.Fatalf("expected 1 event; got %d: %+v", len(events), events)
	}
	if events[0].Topic != string(plugin.YardPaused) {
		t.Fatalf("topic = %q, want %q", events[0].Topic, string(plugin.YardPaused))
	}
	ev, ok := events[0].Payload.(plugin.YardPausedEvent)
	if !ok {
		t.Fatalf("payload type = %T, want YardPausedEvent", events[0].Payload)
	}
	if ev.Reason != "deploy in progress" {
		t.Errorf("publish Reason = %q, want %q", ev.Reason, "deploy in progress")
	}
}

func TestRoutePauseYard_EmptyBody(t *testing.T) {
	db, bus, baseURL, cleanup := setupPauseRouter(t)
	defer cleanup()

	resp, err := http.Post(baseURL+"/api/yard/pause", "application/json", nil)
	if err != nil {
		t.Fatalf("POST /api/yard/pause: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	paused, reason := GetYardPaused(db)
	if !paused {
		t.Error("expected paused=true even without a reason body")
	}
	if reason != "" {
		t.Errorf("expected empty reason; got %q", reason)
	}

	ev := bus.snapshot()
	if len(ev) != 1 || ev[0].Topic != string(plugin.YardPaused) {
		t.Fatalf("expected single YardPaused; got %+v", ev)
	}
	payload, _ := ev[0].Payload.(plugin.YardPausedEvent)
	if payload.Reason != "" {
		t.Errorf("expected empty Reason; got %q", payload.Reason)
	}
}

func TestRouteResumeYard_PersistsAndPublishes(t *testing.T) {
	db, bus, baseURL, cleanup := setupPauseRouter(t)
	defer cleanup()

	// Start paused.
	if err := SetYardPaused(db, true, "manual"); err != nil {
		t.Fatalf("seed pause: %v", err)
	}

	body := bytes.NewBufferString(`{"reason":"deploy finished"}`)
	resp, err := http.Post(baseURL+"/api/yard/resume", "application/json", body)
	if err != nil {
		t.Fatalf("POST /api/yard/resume: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	paused, reason := GetYardPaused(db)
	if paused {
		t.Error("expected paused=false after resume")
	}
	if reason != "" {
		// SetYardPaused clears the reason on resume per the design comment.
		t.Errorf("expected persisted reason cleared on resume; got %q", reason)
	}

	events := bus.snapshot()
	if len(events) != 1 {
		t.Fatalf("expected 1 event; got %d: %+v", len(events), events)
	}
	if events[0].Topic != string(plugin.YardResumed) {
		t.Fatalf("topic = %q, want %q", events[0].Topic, string(plugin.YardResumed))
	}
	ev, ok := events[0].Payload.(plugin.YardResumedEvent)
	if !ok {
		t.Fatalf("payload type = %T, want YardResumedEvent", events[0].Payload)
	}
	if ev.Reason != "deploy finished" {
		t.Errorf("publish Reason = %q, want %q", ev.Reason, "deploy finished")
	}
}

func TestRouteResumeYard_FormEncodedBody(t *testing.T) {
	db, bus, baseURL, cleanup := setupPauseRouter(t)
	defer cleanup()

	if err := SetYardPaused(db, true, "manual"); err != nil {
		t.Fatalf("seed pause: %v", err)
	}

	// gin's ShouldBind picks the form decoder for application/x-www-form-urlencoded.
	resp, err := http.Post(baseURL+"/api/yard/resume",
		"application/x-www-form-urlencoded",
		strings.NewReader("reason=via+form"),
	)
	if err != nil {
		t.Fatalf("POST /api/yard/resume: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	ev := bus.snapshot()
	if len(ev) != 1 {
		t.Fatalf("expected 1 publish; got %d: %+v", len(ev), ev)
	}
	payload, ok := ev[0].Payload.(plugin.YardResumedEvent)
	if !ok {
		t.Fatalf("payload type = %T", ev[0].Payload)
	}
	if payload.Reason != "via form" {
		t.Errorf("Reason = %q, want %q", payload.Reason, "via form")
	}
}

func TestRoutePauseYard_NilBusIsSafe(t *testing.T) {
	// Mirror setupPauseRouter but pass a nil bus to confirm the publish
	// helper is a no-op and the route still succeeds.
	db := testDB(t)

	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(gin.Recovery())
	tmpl, err := parseTemplates()
	if err != nil {
		t.Fatalf("parse templates: %v", err)
	}
	router.SetHTMLTemplate(tmpl)
	registerRoutesWithBus(router, db, "testproject", nil)

	port := findFreePort()
	srv := &http.Server{Addr: fmt.Sprintf(":%d", port), Handler: router}
	go srv.ListenAndServe()
	defer srv.Close()
	baseURL := fmt.Sprintf("http://localhost:%d", port)

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := http.Get(baseURL + "/static/style.css")
		if err == nil {
			resp.Body.Close()
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	resp, err := http.Post(baseURL+"/api/yard/pause", "application/json", nil)
	if err != nil {
		t.Fatalf("POST /api/yard/pause: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 (nil bus must not break route)", resp.StatusCode)
	}

	paused, _ := GetYardPaused(db)
	if !paused {
		t.Error("expected paused=true even with nil bus")
	}
}

// --- queries: SetYardPaused / GetYardPaused unit tests ---

func TestSetYardPaused_Roundtrip(t *testing.T) {
	db := testDB(t)

	if err := SetYardPaused(db, true, "test reason"); err != nil {
		t.Fatalf("SetYardPaused(true): %v", err)
	}
	paused, reason := GetYardPaused(db)
	if !paused || reason != "test reason" {
		t.Errorf("after pause: paused=%v reason=%q", paused, reason)
	}

	if err := SetYardPaused(db, false, "resume reason"); err != nil {
		t.Fatalf("SetYardPaused(false): %v", err)
	}
	paused, reason = GetYardPaused(db)
	if paused {
		t.Errorf("after resume: paused=%v, want false", paused)
	}
	// Resume clears the persisted reason per the design comment in queries.go.
	if reason != "" {
		t.Errorf("after resume: reason=%q, want empty", reason)
	}
}

func TestSetYardPaused_NilDB(t *testing.T) {
	if err := SetYardPaused(nil, true, ""); err == nil {
		t.Error("expected error for nil db")
	}
}

func TestGetYardPaused_NilDB(t *testing.T) {
	paused, reason := GetYardPaused(nil)
	if paused || reason != "" {
		t.Errorf("nil db should return zero value; got paused=%v reason=%q", paused, reason)
	}
}

func TestGetYardPaused_NoRow(t *testing.T) {
	db := testDB(t)
	paused, reason := GetYardPaused(db)
	if paused || reason != "" {
		t.Errorf("no row should yield zero value; got paused=%v reason=%q", paused, reason)
	}
}
