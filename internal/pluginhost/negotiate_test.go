package pluginhost

import (
	"context"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/zulandar/railyard/internal/config"
	"github.com/zulandar/railyard/internal/events"
	"github.com/zulandar/railyard/pkg/plugin"
)

// TestCanonicalEventTopicsMatchesSDK pins the host's Init-time topic
// advertisement to the SDK's CoreEventTypes so the two cannot drift
// (railyard-77h.8).
func TestCanonicalEventTopicsMatchesSDK(t *testing.T) {
	t.Parallel()
	got := canonicalEventTopics()
	core := plugin.CoreEventTypes()
	if len(got) != len(core) {
		t.Fatalf("canonicalEventTopics len = %d, want %d", len(got), len(core))
	}
	for i, et := range core {
		if got[i] != string(et) {
			t.Errorf("topic[%d] = %q, want %q", i, got[i], string(et))
		}
	}
}

// TestInitNegotiationE2E proves a plugin built against the current SDK
// Inits against a real host and the SDK version it reports lands in the
// status snapshot (railyard-77h.8 acceptance: round-trip e2e).
func TestInitNegotiationE2E(t *testing.T) {
	if testing.Short() {
		t.Skip("subprocess plugin build is slow; skip under -short")
	}
	if runtime.GOOS != "linux" {
		t.Skipf("pluginhost subprocess plugins require Linux SO_PEERCRED; goos=%s", runtime.GOOS)
	}

	bin := buildTestPlugin(t)
	pluginsDir := t.TempDir()
	dst := filepath.Join(pluginsDir, "testplugin")
	if err := copyExec(bin, dst); err != nil {
		t.Fatalf("copy binary: %v", err)
	}

	cfg := &config.Config{
		Owner:   "tester",
		Project: "railyard",
		Plugins: config.PluginsConfig{
			Enabled:    []string{"testplugin"},
			PluginsDir: pluginsDir,
			Settings: map[string]config.PluginSettings{
				"testplugin": {Allow: config.AllowConfig{Events: []string{"*"}}},
			},
		},
	}
	bus := events.NewBus()
	t.Cleanup(func() {
		if closer, ok := bus.(interface{ Close() }); ok {
			closer.Close()
		}
	})
	host := NewHost(Dependencies{Cfg: cfg, Bus: bus, RailyardVersion: "test"})

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	host.Init(ctx)
	t.Cleanup(func() {
		stopCtx, stopCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer stopCancel()
		host.Stop(stopCtx)
	})

	if names := host.Names(); len(names) != 1 || names[0] != "testplugin" {
		t.Fatalf("expected one launched plugin, got %v", names)
	}

	snap := host.Status()
	var row *PluginStatus
	for i := range snap.Plugins {
		if snap.Plugins[i].Name == "testplugin" {
			row = &snap.Plugins[i]
			break
		}
	}
	if row == nil {
		t.Fatal("testplugin missing from Status() snapshot")
	}
	if row.SDKVersion != plugin.SDKVersion {
		t.Errorf("Status SDKVersion = %q, want %q", row.SDKVersion, plugin.SDKVersion)
	}
}
