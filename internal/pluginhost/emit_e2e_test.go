//go:build linux
// +build linux

package pluginhost

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zulandar/railyard/internal/config"
	"github.com/zulandar/railyard/internal/events"
)

// TestEmitEventRoundTripE2E proves the full plugin-published-event path
// across a real subprocess: the plugin calls Host.Emit("testplugin.ping")
// (SDK -> HostService.EmitEvent), the host enforces the namespace +
// allow.publish gate and publishes onto the bus, and the same plugin's
// Subscribe stream delivers the dynamic event back with its map payload
// intact (railyard-77h.9).
func TestEmitEventRoundTripE2E(t *testing.T) {
	if testing.Short() {
		t.Skip("subprocess; skipped under -short")
	}

	bin := buildTestPlugin(t)
	pluginsDir := t.TempDir()
	dst := filepath.Join(pluginsDir, "testplugin")
	if err := copyExec(bin, dst); err != nil {
		t.Fatalf("copy binary: %v", err)
	}

	emitLog := filepath.Join(t.TempDir(), "emit.log")
	t.Setenv("RAILYARD_TESTPLUGIN_EMIT", "1")
	t.Setenv("RAILYARD_TESTPLUGIN_EMIT_LOG", emitLog)

	cfg := &config.Config{
		Owner:   "tester",
		Project: "railyard",
		Plugins: config.PluginsConfig{
			Enabled:    []string{"testplugin"},
			PluginsDir: pluginsDir,
			Settings: map[string]config.PluginSettings{
				"testplugin": {Allow: config.AllowConfig{
					Events:  []string{"testplugin.ping"},
					Publish: []string{"testplugin.*"},
				}},
			},
		},
	}
	bus := events.NewBus()
	t.Cleanup(func() {
		if c, ok := bus.(interface{ Close() }); ok {
			c.Close()
		}
	})
	host := NewHost(Dependencies{Cfg: cfg, Bus: bus, RailyardVersion: "test"})

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	host.Init(ctx)
	if names := host.Names(); len(names) != 1 {
		t.Fatalf("expected one launched plugin, got %v", names)
	}
	host.Start(ctx)
	t.Cleanup(func() {
		stopCtx, stopCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer stopCancel()
		host.Stop(stopCtx)
	})

	// The plugin emits "testplugin.ping" every 100ms from Start and logs
	// each received dynamic payload. Wait for the round-trip to land.
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if data, err := os.ReadFile(emitLog); err == nil && strings.Contains(string(data), "testplugin.ping pong") {
			return // success: dynamic event round-tripped with payload intact
		}
		time.Sleep(100 * time.Millisecond)
	}
	data, _ := os.ReadFile(emitLog)
	t.Fatalf("plugin never observed its own emitted event with payload; emit log:\n%s", string(data))
}
