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

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/zulandar/railyard/internal/config"
	"github.com/zulandar/railyard/internal/db"
	"github.com/zulandar/railyard/internal/events"
	"github.com/zulandar/railyard/internal/models"
)

// TestKVStoreRoundTripE2E proves the SDK Store accessor round-trips
// Get/Put/Delete/List through a real subprocess plugin against a real host
// DB (railyard-77h.11). The plugin (KV mode) drives the full sequence from
// Start and logs each observed result; the test asserts both the plugin's
// log and the actual rows the host persisted in the shared DB.
func TestKVStoreRoundTripE2E(t *testing.T) {
	if testing.Short() {
		t.Skip("subprocess; skipped under -short")
	}

	bin := buildTestPlugin(t)
	pluginsDir := t.TempDir()
	dst := filepath.Join(pluginsDir, "testplugin")
	if err := copyExec(bin, dst); err != nil {
		t.Fatalf("copy binary: %v", err)
	}

	kvLog := filepath.Join(t.TempDir(), "kv.log")
	t.Setenv("RAILYARD_TESTPLUGIN_KV", "1")
	t.Setenv("RAILYARD_TESTPLUGIN_KV_LOG", kvLog)

	// Real host DB: a shared in-memory SQLite handle. The host server runs
	// in-process, so the plugin's KV RPCs reach this exact DB over gRPC.
	gdb, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := db.AutoMigrate(gdb); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	cfg := &config.Config{
		Owner:   "tester",
		Project: "railyard",
		Plugins: config.PluginsConfig{
			Enabled:    []string{"testplugin"},
			PluginsDir: pluginsDir,
		},
	}
	bus := events.NewBus()
	t.Cleanup(func() {
		if c, ok := bus.(interface{ Close() }); ok {
			c.Close()
		}
	})
	host := NewHost(Dependencies{Cfg: cfg, Bus: bus, DB: gdb, RailyardVersion: "test"})

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

	// Wait for the plugin to finish its round-trip ("done" sentinel).
	var logData string
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if data, rerr := os.ReadFile(kvLog); rerr == nil && strings.Contains(string(data), "done") {
			logData = string(data)
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if logData == "" {
		data, _ := os.ReadFile(kvLog)
		t.Fatalf("plugin never completed KV round-trip; kv log:\n%s", string(data))
	}

	// Assert the observed sequence (each step round-tripped through gRPC).
	wantLines := []string{
		"get-missing found=false",
		"put ok",
		"get found=true value=rev-42",
		"list keys=[seen:a seen:b]",
		"delete ok",
		"get-after-delete found=false",
	}
	for _, want := range wantLines {
		if !strings.Contains(logData, want) {
			t.Errorf("kv log missing %q; full log:\n%s", want, logData)
		}
	}

	// Assert the host actually persisted the plugin's rows under its
	// connection-bound namespace ("testplugin") in the shared DB. After the
	// round-trip "cursor" is deleted but the two "seen:" keys remain.
	var rows []models.PluginKV
	if err := gdb.Where("plugin = ?", "testplugin").Order("key ASC").Find(&rows).Error; err != nil {
		t.Fatalf("query persisted rows: %v", err)
	}
	if len(rows) != 2 || rows[0].Key != "seen:a" || rows[1].Key != "seen:b" {
		t.Fatalf("persisted rows = %+v, want [seen:a seen:b] under namespace testplugin", rows)
	}
}
