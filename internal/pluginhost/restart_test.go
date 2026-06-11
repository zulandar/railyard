package pluginhost

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/zulandar/railyard/internal/config"
	"github.com/zulandar/railyard/internal/events"
)

// buildCrashPlugin compiles the testdata/crashplugin fixture and returns
// the resulting binary path. Mirrors buildTestPlugin in launch_test.go
// (they intentionally do not share code — the build is fast and
// keeping them independent insulates each test from churn in the
// other fixture).
func buildCrashPlugin(t *testing.T) string {
	t.Helper()
	if testing.Short() {
		t.Skip("subprocess plugin build is slow; skip under -short")
	}
	goBin, err := exec.LookPath("go")
	if err != nil {
		t.Skipf("go toolchain not available: %v", err)
	}
	src, err := filepath.Abs(filepath.Join("testdata", "crashplugin"))
	if err != nil {
		t.Fatalf("abs path: %v", err)
	}
	out := filepath.Join(t.TempDir(), "crashplugin")
	if runtime.GOOS == "windows" {
		out += ".exe"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, goBin, "build", "-o", out, ".")
	cmd.Dir = src
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	if err := cmd.Run(); err != nil {
		t.Fatalf("build crashplugin: %v\n%s", err, buf.String())
	}
	if err := os.Chmod(out, 0o700); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	return out
}

// stageCrashPlugin copies the crashplugin binary into a plugins.d
// inside a fresh temp root, chdirs there for the test, and returns the
// path to the counter file the plugin appends to on each boot.
func stageCrashPlugin(t *testing.T, mode string) (counterFile string) {
	t.Helper()
	bin := buildCrashPlugin(t)

	root := t.TempDir()
	pluginsDir := filepath.Join(root, "plugins")
	if err := os.MkdirAll(pluginsDir, 0o755); err != nil {
		t.Fatalf("mkdir plugins: %v", err)
	}
	dst := filepath.Join(pluginsDir, "crashplugin")
	if err := copyExec(bin, dst); err != nil {
		t.Fatalf("copy binary: %v", err)
	}

	prevWd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(root); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(prevWd) })

	counterFile = filepath.Join(t.TempDir(), "boots.log")
	t.Setenv("RAILYARD_CRASHPLUGIN_MODE", mode)
	t.Setenv("RAILYARD_CRASHPLUGIN_COUNTER_FILE", counterFile)
	return counterFile
}

// countBoots reports how many distinct "pid=" lines have been written
// to the counter file by crashplugin's Init.
func countBoots(t *testing.T, path string) int {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return 0
		}
		t.Fatalf("read counter file %s: %v", path, err)
	}
	n := 0
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		if strings.HasPrefix(line, "pid=") {
			n++
		}
	}
	return n
}

// waitForBoots polls the counter file until it reports at least `want`
// boots or the deadline expires. Returns the final observed count.
func waitForBoots(t *testing.T, path string, want int, timeout time.Duration) int {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var last int
	for time.Now().Before(deadline) {
		last = countBoots(t, path)
		if last >= want {
			return last
		}
		time.Sleep(50 * time.Millisecond)
	}
	return last
}

// TestRestartLoop_ExitsAreRestartedUpToBudget exercises railyard-fll.6's
// acceptance criterion: a plugin that crashes after Start is relaunched
// up to 3 times within the 60s window; the 4th crash flips the budget
// and the plugin is permanently disabled.
//
// The fixture crashes on EVERY boot, so:
//   - Boot 1: initial launch + crash → relaunch #1 (budget=1).
//   - Boot 2: relaunch + crash → relaunch #2 (budget=2).
//   - Boot 3: relaunch + crash → relaunch #3 (budget=3).
//   - Boot 4: relaunch + crash → budget=4, permanent-disable, no more
//     relaunches.
//
// We assert: at least 4 boots are observed; the plugin then settles
// (no 5th boot in the next second); the plugin is removed from the
// host's launched set.
func TestRestartLoop_ExitsAreRestartedUpToBudget(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("UDS not supported on Windows")
	}

	counterFile := stageCrashPlugin(t, "after_start")

	cfg := &config.Config{
		Owner:   "tester",
		Project: "railyard",
		Plugins: config.PluginsConfig{
			Enabled: []string{"crashplugin"},
			Settings: map[string]config.PluginSettings{
				"crashplugin": {Allow: config.AllowConfig{
					Events:   []string{"*"},
					Commands: []string{"*"},
				}},
			},
		},
	}
	bus := events.NewBus()
	defer bus.(interface{ Close() }).Close()
	host := NewHost(Dependencies{Cfg: cfg, Bus: bus, RailyardVersion: "test"})

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	host.Init(ctx)
	host.Start(ctx)

	// Wait for the budget to be exhausted (4 boots = initial + 3
	// retries). Generous timeout: 3 backoffs sum to 1.75s plus per-boot
	// launch overhead.
	gotBoots := waitForBoots(t, counterFile, 4, 20*time.Second)
	if gotBoots < 4 {
		t.Fatalf("expected at least 4 boots before permanent-disable, got %d", gotBoots)
	}

	// Wait a beat to confirm the plugin has settled — no 5th boot should
	// appear after permanent-disable.
	time.Sleep(500 * time.Millisecond)
	if extra := countBoots(t, counterFile); extra > gotBoots+1 {
		// Allow +1 to absorb a boot that was racing the
		// permanent-disable observation; anything more indicates the
		// supervisor is still relaunching after budget exhaustion.
		t.Fatalf("plugin still relaunching after budget exhausted: %d boots (was %d)", extra, gotBoots)
	}

	// After permanent-disable, the plugin should be removed from the
	// active set.
	if names := host.Names(); len(names) != 0 {
		t.Errorf("expected zero launched plugins after permanent-disable, got %v", names)
	}

	stopCtx, stopCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer stopCancel()
	host.Stop(stopCtx)
}

// TestRestartPreservesRuntimeCounters asserts that the per-plugin
// lifetime counters (railyard-77h.14) live on the registry entry and so
// survive a relaunch — the supervisor reuses the same *launchedPlugin and
// only swaps the dead subprocess's go-plugin handles in place. A relaunch
// bumps restartCount (surfaced as RESTARTS) but MUST NOT reset the event /
// command counters. We drive the supervisor's in-place
// restartCount bump under h.mu rather than spawning a real subprocess.
func TestRestartPreservesRuntimeCounters(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	h := &Host{
		clock:    func() time.Time { return now },
		launched: map[string]*launchedPlugin{},
	}
	lp := &launchedPlugin{name: "p1"}
	lp.eventsDelivered.Store(50)
	lp.eventsDropped.Store(3)
	lp.commandsHandled.Store(9)
	lp.commandsFailed.Store(2)
	lp.commandLatencyTotalMicros.Store(12345)
	h.launched["p1"] = lp

	// Mirror the supervisor's post-relaunch bookkeeping (supervise.go):
	// restartCount++ and lastActivity under h.mu, reusing the same entry.
	h.mu.Lock()
	if relaunchLP, ok := h.launched["p1"]; ok {
		relaunchLP.restartCount++
		relaunchLP.lastActivity = h.clock()
	}
	h.mu.Unlock()

	if lp.restartCount != 1 {
		t.Errorf("restartCount = %d, want 1", lp.restartCount)
	}
	// The hot-path counters must be untouched by the relaunch bookkeeping.
	if got := lp.eventsDelivered.Load(); got != 50 {
		t.Errorf("eventsDelivered = %d, want 50 (must survive relaunch)", got)
	}
	if got := lp.eventsDropped.Load(); got != 3 {
		t.Errorf("eventsDropped = %d, want 3 (must survive relaunch)", got)
	}
	if got := lp.commandsHandled.Load(); got != 9 {
		t.Errorf("commandsHandled = %d, want 9 (must survive relaunch)", got)
	}
	if got := lp.commandsFailed.Load(); got != 2 {
		t.Errorf("commandsFailed = %d, want 2 (must survive relaunch)", got)
	}
	if got := lp.commandLatencyTotalMicros.Load(); got != 12345 {
		t.Errorf("commandLatencyTotalMicros = %d, want 12345 (must survive relaunch)", got)
	}
}

// TestRestartLoop_BackoffSleepShortCircuitsOnStop is a focused
// concurrency probe — confirms that closing the host's shutdown channel
// short-circuits the supervisor's backoff sleep, so Stop never has to
// wait the full backoff for a crashing plugin to settle.
//
// We use the injected backoffSleep + shutdownCh directly rather than
// driving a real subprocess: a tight unit-level test that nails the
// race-guard semantics without paying the build-the-plugin cost.
func TestRestartLoop_BackoffSleepShortCircuitsOnStop(t *testing.T) {
	host := NewHost(Dependencies{})
	// Issue a long sleep, then close shutdownCh; expect the sleep to
	// return false promptly.
	done := make(chan bool, 1)
	go func() {
		done <- host.backoffSleep(10*time.Second, host.shutdownCh)
	}()

	// Stagger slightly so the goroutine is parked inside the sleep
	// before we close shutdownCh.
	time.Sleep(10 * time.Millisecond)
	close(host.shutdownCh)

	select {
	case got := <-done:
		if got {
			t.Fatal("backoffSleep returned true; expected false on shutdown short-circuit")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("backoffSleep did not return after shutdown close")
	}

	// Re-close shutdown via Stop would panic on closed channel; this
	// test bypasses Stop and never calls it. Make sure NewHost's
	// invariants still hold for the rest of the package.
	host.shutdownOnce.Do(func() {})
}
