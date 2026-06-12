//go:build linux
// +build linux

package pluginhost

import (
	"bufio"
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/zulandar/railyard/internal/config"
	"github.com/zulandar/railyard/internal/events"
	"github.com/zulandar/railyard/pkg/plugin"
)

// TestSubscribeWithMetaObservesGapE2E proves the SDK's SubscribeWithMeta
// path carries per-stream metadata across a real subprocess: the plugin
// records (seq, dropped) for every delivered event, and after a burst
// that outpaces its slow handler the plugin observes a gap
// (dropped > 0) while seq stays monotonic (railyard-77h.10).
func TestSubscribeWithMetaObservesGapE2E(t *testing.T) {
	if testing.Short() {
		t.Skip("subprocess + burst; skipped under -short")
	}

	bin := buildTestPlugin(t)
	pluginsDir := t.TempDir()
	dst := filepath.Join(pluginsDir, "testplugin")
	if err := copyExec(bin, dst); err != nil {
		t.Fatalf("copy binary: %v", err)
	}

	metaLog := filepath.Join(t.TempDir(), "meta.log")
	t.Setenv("RAILYARD_TESTPLUGIN_META", "1")
	t.Setenv("RAILYARD_TESTPLUGIN_META_LOG", metaLog)
	t.Setenv("RAILYARD_TESTPLUGIN_META_SLEEP_MS", "2")

	cfg := &config.Config{
		Owner:   "tester",
		Project: "railyard",
		Plugins: config.PluginsConfig{
			Enabled:    []string{"testplugin"},
			PluginsDir: pluginsDir,
			Settings: map[string]config.PluginSettings{
				"testplugin": {Allow: config.AllowConfig{Events: []string{string(plugin.CarCreated)}}},
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

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	host.Init(ctx)
	if names := host.Names(); len(names) != 1 {
		t.Fatalf("expected one launched plugin, got %v", names)
	}
	host.Start(ctx)

	// Wait for the plugin to open the meta log (it does so in Init,
	// before subscribing) so the burst lands on a wired subscription.
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if fi, err := os.Stat(metaLog); err == nil && fi.Size() >= 0 {
			// File exists; give the Subscribe stream a beat to wire up.
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	// Warm-up publishes until the plugin records its first delivery, so
	// we know the stream is live before the real burst.
	warm := time.Now().Add(10 * time.Second)
	for time.Now().Before(warm) {
		if n, _ := countLines(metaLog); n > 0 {
			break
		}
		bus.Publish(string(plugin.CarCreated), plugin.CarCreatedEvent{CarID: "warm"})
		time.Sleep(50 * time.Millisecond)
	}

	// Burst far faster than the 2ms-per-event handler can drain.
	for i := 0; i < 6000; i++ {
		bus.Publish(string(plugin.CarCreated), plugin.CarCreatedEvent{CarID: strconv.Itoa(i)})
	}

	// Wait for the meta log to stop growing (steady state).
	waitFileSteady(t, metaLog, 1*time.Second, 30*time.Second)

	stopCtx, stopCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer stopCancel()
	host.Stop(stopCtx)

	seqs, drops, err := readMetaLog(metaLog)
	if err != nil {
		t.Fatalf("read meta log: %v", err)
	}
	if len(seqs) == 0 {
		t.Fatal("plugin recorded no deliveries")
	}
	// seq is monotonic non-decreasing over delivered events.
	for i := 1; i < len(seqs); i++ {
		if seqs[i] < seqs[i-1] {
			t.Errorf("seq not monotonic: seqs[%d]=%d < seqs[%d]=%d", i, seqs[i], i-1, seqs[i-1])
			break
		}
	}
	// The plugin observed a gap: cumulative dropped rose above zero.
	var maxDropped uint64
	for _, d := range drops {
		if d > maxDropped {
			maxDropped = d
		}
	}
	if maxDropped == 0 {
		t.Errorf("expected the plugin to observe Dropped > 0 after the burst; got max dropped 0 (delivered=%d)", len(seqs))
	}
}

func countLines(path string) (int, error) {
	f, err := os.Open(path) // #nosec G304 -- test reads its own temp file
	if err != nil {
		return 0, err
	}
	defer func() { _ = f.Close() }()
	n := 0
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if strings.TrimSpace(sc.Text()) != "" {
			n++
		}
	}
	return n, sc.Err()
}

func waitFileSteady(t *testing.T, path string, settle, total time.Duration) {
	t.Helper()
	var prev int64 = -1
	stable := time.Time{}
	deadline := time.Now().Add(total)
	for time.Now().Before(deadline) {
		fi, err := os.Stat(path)
		if err == nil {
			cur := fi.Size()
			if cur != prev {
				prev = cur
				stable = time.Time{}
			} else if stable.IsZero() {
				stable = time.Now()
			} else if time.Since(stable) >= settle {
				return
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func readMetaLog(path string) (seqs, drops []uint64, err error) {
	f, err := os.Open(path) // #nosec G304 -- test reads its own temp file
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = f.Close() }()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) != 2 {
			continue
		}
		s, e1 := strconv.ParseUint(fields[0], 10, 64)
		d, e2 := strconv.ParseUint(fields[1], 10, 64)
		if e1 != nil || e2 != nil {
			continue
		}
		seqs = append(seqs, s)
		drops = append(drops, d)
	}
	return seqs, drops, sc.Err()
}
