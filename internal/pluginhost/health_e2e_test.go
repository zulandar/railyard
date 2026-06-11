package pluginhost

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/zulandar/railyard/internal/config"
	"github.com/zulandar/railyard/internal/events"
)

// TestHealthPollEndToEnd launches two real subprocess plugins over gRPC:
//   - healthplugin, which implements pkg/plugin.HealthReporter and reports
//     HEALTH_OK; and
//   - testplugin, which does NOT implement HealthReporter.
//
// It drives Init/Start, runs a single deterministic poll via pollHealthOnce
// (rather than waiting on the 30s loop), and asserts the OK verdict flows
// through to Host.Status() for the reporter while the non-reporter is shown
// as "n/a" — the backward-compatible path (railyard-77h.12).
func TestHealthPollEndToEnd(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("UDS not supported on Windows")
	}

	healthBin := buildPluginBinary(t, "healthplugin")
	testBin := buildPluginBinary(t, "testplugin")

	root := t.TempDir()
	pluginsDir := filepath.Join(root, "plugins")
	if err := os.MkdirAll(pluginsDir, 0o755); err != nil {
		t.Fatalf("mkdir plugins: %v", err)
	}
	if err := copyExec(healthBin, filepath.Join(pluginsDir, "healthplugin")); err != nil {
		t.Fatalf("copy healthplugin: %v", err)
	}
	if err := copyExec(testBin, filepath.Join(pluginsDir, "testplugin")); err != nil {
		t.Fatalf("copy testplugin: %v", err)
	}

	prevWd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(root); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(prevWd) })

	// healthplugin reports a known message so we can assert it propagates.
	t.Setenv("RAILYARD_HEALTHPLUGIN_STATE", "ok")
	t.Setenv("RAILYARD_HEALTHPLUGIN_MSG", "synthetic ok")

	cfg := &config.Config{
		Owner:   "tester",
		Project: "railyard",
		Plugins: config.PluginsConfig{
			Enabled: []string{"healthplugin", "testplugin"},
			Settings: map[string]config.PluginSettings{
				"healthplugin": {Allow: config.AllowConfig{Events: []string{"*"}, Commands: []string{"*"}}},
				"testplugin":   {Allow: config.AllowConfig{Events: []string{"*"}, Commands: []string{"*"}}},
			},
		},
	}
	bus := events.NewBus()
	defer bus.(interface{ Close() }).Close()
	host := NewHost(Dependencies{Cfg: cfg, Bus: bus, RailyardVersion: "test"})

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	host.Init(ctx)
	host.Start(ctx)
	t.Cleanup(func() {
		stopCtx, stopCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer stopCancel()
		host.Stop(stopCtx)
	})

	if names := host.Names(); len(names) != 2 {
		t.Fatalf("expected two launched plugins, got %v", names)
	}

	// One deterministic synchronous poll instead of waiting for the loop.
	host.pollHealthOnce(ctx)

	snap := host.Status()
	byName := map[string]PluginStatus{}
	for _, p := range snap.Plugins {
		byName[p.Name] = p
	}

	hp, ok := byName["healthplugin"]
	if !ok {
		t.Fatal("healthplugin missing from snapshot")
	}
	if hp.Health != healthValueOK {
		t.Errorf("healthplugin Health = %q, want %q", hp.Health, healthValueOK)
	}
	if hp.HealthMessage != "synthetic ok" {
		t.Errorf("healthplugin HealthMessage = %q, want %q", hp.HealthMessage, "synthetic ok")
	}
	if hp.HealthCheckedAt.IsZero() {
		t.Error("healthplugin HealthCheckedAt should be set after a poll")
	}

	tp, ok := byName["testplugin"]
	if !ok {
		t.Fatal("testplugin missing from snapshot")
	}
	if tp.Health != healthValueNA {
		t.Errorf("testplugin Health = %q, want %q (does not implement HealthReporter)", tp.Health, healthValueNA)
	}
}
