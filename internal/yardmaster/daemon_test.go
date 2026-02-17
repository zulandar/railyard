package yardmaster

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/zulandar/railyard/internal/config"
)

func TestRunDaemon_NilDB(t *testing.T) {
	cfg := testConfig(config.TrackConfig{Name: "backend", Language: "go"})
	err := RunDaemon(context.Background(), nil, cfg, "railyard.yaml", "/tmp", time.Second, nil)
	if err == nil {
		t.Fatal("expected error for nil db")
	}
	if !strings.Contains(err.Error(), "db is required") {
		t.Errorf("error = %q, want to contain %q", err.Error(), "db is required")
	}
}

func TestRunDaemon_NilConfig(t *testing.T) {
	err := RunDaemon(context.Background(), nil, nil, "railyard.yaml", "/tmp", time.Second, nil)
	if err == nil {
		t.Fatal("expected error")
	}
	// DB check comes first.
	if !strings.Contains(err.Error(), "db is required") {
		t.Errorf("error = %q", err)
	}
}

func TestRunDaemon_EmptyRepoDir(t *testing.T) {
	err := RunDaemon(context.Background(), nil, nil, "railyard.yaml", "", time.Second, nil)
	if err == nil {
		t.Fatal("expected error")
	}
	// DB check comes first.
	if !strings.Contains(err.Error(), "db is required") {
		t.Errorf("error = %q", err)
	}
}

func TestYardmasterID(t *testing.T) {
	if YardmasterID != "yardmaster" {
		t.Errorf("YardmasterID = %q, want %q", YardmasterID, "yardmaster")
	}
}

func TestDefaultPollInterval(t *testing.T) {
	if defaultPollInterval != 30*time.Second {
		t.Errorf("defaultPollInterval = %v, want 30s", defaultPollInterval)
	}
}

func TestSleepWithContext_Cancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately.

	start := time.Now()
	sleepWithContext(ctx, 10*time.Second)
	elapsed := time.Since(start)

	if elapsed > time.Second {
		t.Errorf("sleepWithContext should return immediately on cancelled ctx, took %v", elapsed)
	}
}

func TestSleepWithContext_ShortDuration(t *testing.T) {
	start := time.Now()
	sleepWithContext(context.Background(), 50*time.Millisecond)
	elapsed := time.Since(start)

	if elapsed < 40*time.Millisecond {
		t.Errorf("sleepWithContext returned too early: %v", elapsed)
	}
	if elapsed > time.Second {
		t.Errorf("sleepWithContext took too long: %v", elapsed)
	}
}

func TestMaxTestFailures(t *testing.T) {
	if maxTestFailures != 2 {
		t.Errorf("maxTestFailures = %d, want 2", maxTestFailures)
	}
}
