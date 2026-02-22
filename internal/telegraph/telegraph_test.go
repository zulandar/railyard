package telegraph

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/zulandar/railyard/internal/config"
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
		DB:      openTestDB(t),
		Config:  testCfg(),
		Adapter: mock,
		Out:     &buf,
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

func TestRun_ReceivesInboundMessages(t *testing.T) {
	mock := NewMockAdapter()
	var buf bytes.Buffer

	d, err := NewDaemon(DaemonOpts{
		DB:      openTestDB(t),
		Config:  testCfg(),
		Adapter: mock,
		Out:     &buf,
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

	// Simulate an inbound message.
	mock.SimulateInbound(InboundMessage{
		Platform:  "test",
		ChannelID: "C123",
		UserName:  "bob",
		Text:      "status?",
	})

	// Wait for it to be logged.
	waitFor(t, func() bool {
		return strings.Contains(buf.String(), "bob: status?")
	}, 2*time.Second)

	cancel()
	<-done
}

func TestRun_HandlesClosed(t *testing.T) {
	mock := NewMockAdapter()
	var buf bytes.Buffer

	d, err := NewDaemon(DaemonOpts{
		DB:      openTestDB(t),
		Config:  testCfg(),
		Adapter: mock,
		Out:     &buf,
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
// helpers
// ---------------------------------------------------------------------------

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
