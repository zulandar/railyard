package telegraph

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/zulandar/railyard/internal/config"
	"github.com/zulandar/railyard/internal/models"
	"github.com/zulandar/railyard/internal/orchestration"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func testCfg() *config.Config {
	return &config.Config{
		Owner:        "alice",
		Repo:         "git@github.com:org/myapp.git",
		BranchPrefix: "ry/alice",
		Tracks:       []config.TrackConfig{{Name: "backend", Language: "go"}},
		Telegraph: config.TelegraphConfig{
			Channel: "C123",
			Events: config.EventsConfig{
				CarLifecycle:    true,
				EngineStalls:    true,
				Escalations:     true,
				PollIntervalSec: 1,
			},
			DispatchLock: config.DispatchLockConfig{
				HeartbeatIntervalSec: 30,
				HeartbeatTimeoutSec:  90,
			},
		},
	}
}

func openTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	// Auto-migrate models needed by Router/SessionManager.
	if err := db.AutoMigrate(
		&models.DispatchSession{},
		&models.TelegraphConversation{},
		&models.Car{},
		&models.Engine{},
		&models.Message{},
		&models.AgentLog{},
	); err != nil {
		t.Fatalf("auto-migrate: %v", err)
	}
	return db
}

// ---------------------------------------------------------------------------
// NewDaemon validation tests
// ---------------------------------------------------------------------------

func TestNewDaemon_NilDB(t *testing.T) {
	_, err := NewDaemon(DaemonOpts{
		Config:  testCfg(),
		Adapter: NewMockAdapter(),
	})
	if err == nil {
		t.Fatal("expected error for nil DB")
	}
	if !strings.Contains(err.Error(), "db is required") {
		t.Errorf("error = %q", err)
	}
}

func TestNewDaemon_NilConfig(t *testing.T) {
	_, err := NewDaemon(DaemonOpts{
		DB:      openTestDB(t),
		Adapter: NewMockAdapter(),
	})
	if err == nil {
		t.Fatal("expected error for nil config")
	}
	if !strings.Contains(err.Error(), "config is required") {
		t.Errorf("error = %q", err)
	}
}

func TestNewDaemon_NilAdapter(t *testing.T) {
	_, err := NewDaemon(DaemonOpts{
		DB:     openTestDB(t),
		Config: testCfg(),
	})
	if err == nil {
		t.Fatal("expected error for nil adapter")
	}
	if !strings.Contains(err.Error(), "adapter is required") {
		t.Errorf("error = %q", err)
	}
}

func TestNewDaemon_Success(t *testing.T) {
	d, err := NewDaemon(DaemonOpts{
		DB:      openTestDB(t),
		Config:  testCfg(),
		Adapter: NewMockAdapter(),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if d == nil {
		t.Fatal("expected non-nil daemon")
	}
}

// ---------------------------------------------------------------------------
// Run lifecycle tests
// ---------------------------------------------------------------------------

func TestRun_ConnectsAndShutdown(t *testing.T) {
	mock := NewMockAdapter()
	var buf bytes.Buffer

	d, err := NewDaemon(DaemonOpts{
		DB:             openTestDB(t),
		Config:         testCfg(),
		Adapter:        mock,
		StatusProvider: &nullStatusProvider{},
		Out:            &buf,
	})
	if err != nil {
		t.Fatalf("NewDaemon: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan error, 1)
	go func() {
		done <- d.Run(ctx)
	}()

	// Wait for the daemon to be online.
	waitFor(t, func() bool {
		return strings.Contains(buf.String(), "Telegraph online")
	}, 2*time.Second)

	// Verify online message was sent.
	if mock.SentCount() < 1 {
		t.Fatal("expected online message to be sent")
	}
	first, _ := mock.LastSent()
	if first.Text != "Telegraph online" {
		t.Errorf("first message = %q, want %q", first.Text, "Telegraph online")
	}

	// Cancel context to trigger shutdown.
	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run returned error: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for Run to return")
	}

	output := buf.String()
	if !strings.Contains(output, "Telegraph shutting down") {
		t.Errorf("missing shutdown message in output: %s", output)
	}
	if !strings.Contains(output, "Telegraph stopped") {
		t.Errorf("missing stopped message in output: %s", output)
	}

	// Verify shutdown message was sent.
	last, ok := mock.LastSent()
	if !ok {
		t.Fatal("expected shutdown message")
	}
	if last.Text != "Telegraph shutting down" {
		t.Errorf("last message = %q, want %q", last.Text, "Telegraph shutting down")
	}
}

func TestRun_HandlesClosed(t *testing.T) {
	mock := NewMockAdapter()
	var buf bytes.Buffer

	d, err := NewDaemon(DaemonOpts{
		DB:             openTestDB(t),
		Config:         testCfg(),
		Adapter:        mock,
		StatusProvider: &nullStatusProvider{},
		Out:            &buf,
	})
	if err != nil {
		t.Fatalf("NewDaemon: %v", err)
	}

	ctx := context.Background()

	done := make(chan error, 1)
	go func() {
		done <- d.Run(ctx)
	}()

	// Wait for online.
	waitFor(t, func() bool {
		return strings.Contains(buf.String(), "Telegraph online")
	}, 2*time.Second)

	// Close the adapter externally (simulates adapter disconnect).
	mock.Close()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run returned error: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for Run to return")
	}

	if !strings.Contains(buf.String(), "inbound channel closed") {
		t.Errorf("missing channel closed message in output: %s", buf.String())
	}
}

// ---------------------------------------------------------------------------
// Inbound routing tests
// ---------------------------------------------------------------------------

func TestRun_InboundRoutedToRouter(t *testing.T) {
	mock := NewMockAdapter()
	var buf bytes.Buffer

	d, err := NewDaemon(DaemonOpts{
		DB:             openTestDB(t),
		Config:         testCfg(),
		Adapter:        mock,
		StatusProvider: &nullStatusProvider{},
		Out:            &buf,
	})
	if err != nil {
		t.Fatalf("NewDaemon: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	go func() {
		done <- d.Run(ctx)
	}()

	// Wait for online.
	waitFor(t, func() bool {
		return strings.Contains(buf.String(), "Telegraph online")
	}, 2*time.Second)

	initialCount := mock.SentCount()

	// Send "!ry help" command — should be routed to CommandHandler.
	mock.SimulateInbound(InboundMessage{
		Platform:  "test",
		ChannelID: "C123",
		UserName:  "bob",
		Text:      "!ry help",
	})

	// Wait for command response.
	waitFor(t, func() bool {
		return mock.SentCount() > initialCount
	}, 2*time.Second)

	// Verify command response contains help text.
	all := mock.AllSent()
	found := false
	for _, msg := range all[initialCount:] {
		if strings.Contains(msg.Text, "Railyard Commands") {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected help response to contain 'Railyard Commands'")
	}

	cancel()
	<-done
}

func TestRun_BotUserIDFiltering(t *testing.T) {
	mock := NewMockAdapter()
	mock.SetBotUserID("BOT123")
	var buf bytes.Buffer

	d, err := NewDaemon(DaemonOpts{
		DB:             openTestDB(t),
		Config:         testCfg(),
		Adapter:        mock,
		StatusProvider: &nullStatusProvider{},
		Out:            &buf,
	})
	if err != nil {
		t.Fatalf("NewDaemon: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	go func() {
		done <- d.Run(ctx)
	}()

	waitFor(t, func() bool {
		return strings.Contains(buf.String(), "Telegraph online")
	}, 2*time.Second)

	countBefore := mock.SentCount()

	// Send message from bot itself — should be filtered.
	mock.SimulateInbound(InboundMessage{
		Platform:  "test",
		ChannelID: "C123",
		UserID:    "BOT123",
		UserName:  "railyard-bot",
		Text:      "!ry help",
	})

	// Give it a moment to process.
	time.Sleep(100 * time.Millisecond)

	// No new messages should have been sent (the bot message was filtered).
	if mock.SentCount() != countBefore {
		t.Errorf("expected no new messages (self-message should be filtered), got %d new",
			mock.SentCount()-countBefore)
	}

	cancel()
	<-done
}

func TestRun_NoSpawner_CommandsWork(t *testing.T) {
	mock := NewMockAdapter()
	var buf bytes.Buffer

	d, err := NewDaemon(DaemonOpts{
		DB:             openTestDB(t),
		Config:         testCfg(),
		Adapter:        mock,
		StatusProvider: &nullStatusProvider{},
		// No Spawner — dispatch disabled, but commands should work.
		Out: &buf,
	})
	if err != nil {
		t.Fatalf("NewDaemon: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	go func() {
		done <- d.Run(ctx)
	}()

	waitFor(t, func() bool {
		return strings.Contains(buf.String(), "Telegraph online")
	}, 2*time.Second)

	initialCount := mock.SentCount()

	mock.SimulateInbound(InboundMessage{
		Platform:  "test",
		ChannelID: "C123",
		UserName:  "bob",
		Text:      "!ry help",
	})

	waitFor(t, func() bool {
		return mock.SentCount() > initialCount
	}, 2*time.Second)

	all := mock.AllSent()
	found := false
	for _, msg := range all[initialCount:] {
		if strings.Contains(msg.Text, "Railyard Commands") {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected help response even without spawner")
	}

	cancel()
	<-done
}

// ---------------------------------------------------------------------------
// Event dispatch tests
// ---------------------------------------------------------------------------

func TestHandleDetectedEvent_CarLifecycle(t *testing.T) {
	mock := NewMockAdapter()
	ctx := context.Background()
	mock.Connect(ctx)

	var buf bytes.Buffer
	d := &Daemon{
		cfg:     testCfg(),
		adapter: mock,
		out:     &buf,
	}

	event := DetectedEvent{
		Type:      EventCarStatusChange,
		CarID:     "backend-42",
		OldStatus: "in_progress",
		NewStatus: "done",
		Track:     "backend",
		Title:     "Add login flow",
	}

	d.handleDetectedEvent(ctx, event, d.cfg.Telegraph.Events)

	if mock.SentCount() != 1 {
		t.Fatalf("expected 1 sent message, got %d", mock.SentCount())
	}
	sent, _ := mock.LastSent()
	if len(sent.Events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(sent.Events))
	}
	if !strings.Contains(sent.Events[0].Title, "backend-42") {
		t.Errorf("event title = %q, want to contain 'backend-42'", sent.Events[0].Title)
	}
}

func TestHandleDetectedEvent_Filtered(t *testing.T) {
	mock := NewMockAdapter()
	ctx := context.Background()
	mock.Connect(ctx)

	var buf bytes.Buffer
	cfg := testCfg()
	cfg.Telegraph.Events.CarLifecycle = false

	d := &Daemon{
		cfg:     cfg,
		adapter: mock,
		out:     &buf,
	}

	event := DetectedEvent{
		Type:      EventCarStatusChange,
		CarID:     "backend-42",
		NewStatus: "done",
	}

	d.handleDetectedEvent(ctx, event, cfg.Telegraph.Events)

	if mock.SentCount() != 0 {
		t.Fatalf("expected no messages when CarLifecycle=false, got %d", mock.SentCount())
	}
}

func TestDispatchEvents_Channel(t *testing.T) {
	mock := NewMockAdapter()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	mock.Connect(ctx)

	d := &Daemon{
		cfg:     testCfg(),
		adapter: mock,
		out:     &bytes.Buffer{},
	}

	ch := make(chan DetectedEvent, 4)
	ch <- DetectedEvent{
		Type:      EventCarStatusChange,
		CarID:     "car-1",
		NewStatus: "done",
	}
	ch <- DetectedEvent{
		Type:     EventEngineStalled,
		EngineID: "eng-1",
		Track:    "backend",
	}
	close(ch)

	// Run in goroutine — will return when channel closes.
	done := make(chan struct{})
	go func() {
		d.dispatchEvents(ctx, ch)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("dispatchEvents did not return after channel close")
	}

	if mock.SentCount() != 2 {
		t.Fatalf("expected 2 sent messages, got %d", mock.SentCount())
	}
}

func TestFireDigest_NoActivity(t *testing.T) {
	mock := NewMockAdapter()
	ctx := context.Background()
	mock.Connect(ctx)

	db := openTestDB(t)
	sp := &nullStatusProvider{}
	watcher, err := NewWatcher(WatcherOpts{DB: db, StatusProvider: sp})
	if err != nil {
		t.Fatal(err)
	}

	d := &Daemon{
		cfg:     testCfg(),
		adapter: mock,
		out:     &bytes.Buffer{},
	}

	// No activity in DB — digest should be suppressed.
	d.fireDigest(ctx, watcher, "daily")

	if mock.SentCount() != 0 {
		t.Fatalf("expected no digest when no activity, got %d messages", mock.SentCount())
	}
}

func TestRunDigestScheduler_NeitherEnabled(t *testing.T) {
	cfg := testCfg()
	cfg.Telegraph.Digest.Daily.Enabled = false
	cfg.Telegraph.Digest.Weekly.Enabled = false

	d := &Daemon{
		cfg:     cfg,
		adapter: NewMockAdapter(),
		out:     &bytes.Buffer{},
	}

	db := openTestDB(t)
	sp := &nullStatusProvider{}
	watcher, err := NewWatcher(WatcherOpts{DB: db, StatusProvider: sp})
	if err != nil {
		t.Fatal(err)
	}

	// Should return immediately.
	done := make(chan struct{})
	go func() {
		d.runDigestScheduler(context.Background(), watcher)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("runDigestScheduler should return immediately when neither digest enabled")
	}
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

// nullStatusProvider returns empty StatusInfo — suitable for tests that don't
// exercise orchestration status.
type nullStatusProvider struct{}

func (nullStatusProvider) Status() (*orchestration.StatusInfo, error) {
	return &orchestration.StatusInfo{}, nil
}

// waitFor polls condition fn until it returns true or timeout expires.
func waitFor(t *testing.T, fn func() bool, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if fn() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("waitFor timed out after %v", timeout)
}
