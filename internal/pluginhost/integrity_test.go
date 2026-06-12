package pluginhost

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/zulandar/railyard/internal/config"
	"github.com/zulandar/railyard/internal/events"
)

// hashFileForTest computes the lowercase hex SHA-256 of the file at path.
// Independent reimplementation (not the production helper) so the test
// genuinely cross-checks the host's computation.
func hashFileForTest(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// TestComputeFileSHA256 is a focused unit test for the host's fd-based
// hashing helper: it must match an independent computation and error on a
// missing file (railyard-77h.15).
func TestComputeFileSHA256(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "blob")
	if err := os.WriteFile(p, []byte("hello plugin world"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	got, err := computeFileSHA256(p)
	if err != nil {
		t.Fatalf("computeFileSHA256: %v", err)
	}
	if want := hashFileForTest(t, p); got != want {
		t.Errorf("computeFileSHA256 = %q, want %q", got, want)
	}
	if _, err := computeFileSHA256(filepath.Join(dir, "does-not-exist")); err == nil {
		t.Error("expected error hashing a missing file")
	}
}

// TestLaunch_MatchingPin_LaunchesNormally builds the testplugin, computes
// its hash IN THE TEST, pins it, and asserts the plugin launches and runs
// normally (railyard-77h.15).
func TestLaunch_MatchingPin_LaunchesNormally(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skipf("pluginhost subprocess plugins require Linux SO_PEERCRED; goos=%s", runtime.GOOS)
	}
	bin := buildTestPlugin(t)

	root := t.TempDir()
	pluginsDir := filepath.Join(root, "plugins")
	if err := os.MkdirAll(pluginsDir, 0o755); err != nil {
		t.Fatalf("mkdir plugins: %v", err)
	}
	dst := filepath.Join(pluginsDir, "testplugin")
	if err := copyExec(bin, dst); err != nil {
		t.Fatalf("copy binary: %v", err)
	}
	pin := hashFileForTest(t, dst)

	prevWd, _ := os.Getwd()
	if err := os.Chdir(root); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(prevWd) })

	logPath := filepath.Join(t.TempDir(), "events.log")
	t.Setenv("RAILYARD_TESTPLUGIN_LOG", logPath)

	cfg := &config.Config{
		Owner:   "tester",
		Project: "railyard",
		Plugins: config.PluginsConfig{
			Enabled: []string{"testplugin"},
			Settings: map[string]config.PluginSettings{
				"testplugin": {
					Sha256: pin,
					Allow:  config.AllowConfig{Events: []string{"*"}, Commands: []string{"*"}},
				},
			},
		},
	}
	bus := events.NewBus()
	defer bus.(interface{ Close() }).Close()
	host := NewHost(Dependencies{Cfg: cfg, Bus: bus, RailyardVersion: "test"})

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	host.Init(ctx)
	if names := host.Names(); len(names) != 1 || names[0] != "testplugin" {
		t.Fatalf("matching-pin plugin should launch; Names = %v", names)
	}
	host.Start(ctx)

	stopCtx, stopCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer stopCancel()
	host.Stop(stopCtx)
}

// TestLaunch_MismatchingPin_RefusedAndDisabled pins a WRONG hash and
// asserts: the plugin is never launched, it lands in the disabled state
// with reason "integrity-mismatch" (visible via Status), and a WARN is
// logged with both the expected and actual hashes (railyard-77h.15).
func TestLaunch_MismatchingPin_RefusedAndDisabled(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skipf("pluginhost subprocess plugins require Linux SO_PEERCRED; goos=%s", runtime.GOOS)
	}

	cap, capBuf := newLifecycleCaptureHandler()
	prevDefault := slog.Default()
	slog.SetDefault(slog.New(cap))
	t.Cleanup(func() { slog.SetDefault(prevDefault) })

	bin := buildTestPlugin(t)

	root := t.TempDir()
	pluginsDir := filepath.Join(root, "plugins")
	if err := os.MkdirAll(pluginsDir, 0o755); err != nil {
		t.Fatalf("mkdir plugins: %v", err)
	}
	dst := filepath.Join(pluginsDir, "testplugin")
	if err := copyExec(bin, dst); err != nil {
		t.Fatalf("copy binary: %v", err)
	}
	actual := hashFileForTest(t, dst)
	// A deliberately wrong but well-formed pin (64 hex chars).
	const wrongPin = "0000000000000000000000000000000000000000000000000000000000000000"

	prevWd, _ := os.Getwd()
	if err := os.Chdir(root); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(prevWd) })

	cfg := &config.Config{
		Owner:   "tester",
		Project: "railyard",
		Plugins: config.PluginsConfig{
			Enabled: []string{"testplugin"},
			Settings: map[string]config.PluginSettings{
				"testplugin": {
					Sha256: wrongPin,
					Allow:  config.AllowConfig{Events: []string{"*"}, Commands: []string{"*"}},
				},
			},
		},
	}
	bus := events.NewBus()
	defer bus.(interface{ Close() }).Close()
	host := NewHost(Dependencies{Cfg: cfg, Bus: bus, RailyardVersion: "test"})

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	host.Init(ctx)

	// Plugin must NOT be running.
	if names := host.Names(); len(names) != 0 {
		t.Fatalf("mismatching-pin plugin must NOT launch; Names = %v", names)
	}

	// It must surface in Status() as disabled with reason integrity-mismatch.
	snap := host.Status()
	var row *PluginStatus
	for i := range snap.Plugins {
		if snap.Plugins[i].Name == "testplugin" {
			row = &snap.Plugins[i]
			break
		}
	}
	if row == nil {
		t.Fatalf("testplugin missing from Status(); plugins = %+v", snap.Plugins)
	}
	if row.Status != StatusDisabled {
		t.Errorf("status = %q, want %q", row.Status, StatusDisabled)
	}
	if !strings.Contains(row.Error, integrityMismatchReason) {
		t.Errorf("Error = %q, want it to contain %q", row.Error, integrityMismatchReason)
	}

	// WARN must carry both hashes.
	got := capBuf.snapshot()
	if !strings.Contains(got, wrongPin) {
		t.Errorf("WARN missing expected hash %q\nlog:\n%s", wrongPin, got)
	}
	if !strings.Contains(got, actual) {
		t.Errorf("WARN missing actual hash %q\nlog:\n%s", actual, got)
	}

	stopCtx, stopCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer stopCancel()
	host.Stop(stopCtx)
}

// TestLaunch_NoPin_Unchanged confirms that a plugin with no sha256 pin
// launches exactly as before — the default behavior is unchanged
// (railyard-77h.15).
func TestLaunch_NoPin_Unchanged(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skipf("pluginhost subprocess plugins require Linux SO_PEERCRED; goos=%s", runtime.GOOS)
	}
	bin := buildTestPlugin(t)

	root := t.TempDir()
	pluginsDir := filepath.Join(root, "plugins")
	if err := os.MkdirAll(pluginsDir, 0o755); err != nil {
		t.Fatalf("mkdir plugins: %v", err)
	}
	dst := filepath.Join(pluginsDir, "testplugin")
	if err := copyExec(bin, dst); err != nil {
		t.Fatalf("copy binary: %v", err)
	}

	prevWd, _ := os.Getwd()
	if err := os.Chdir(root); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(prevWd) })

	logPath := filepath.Join(t.TempDir(), "events.log")
	t.Setenv("RAILYARD_TESTPLUGIN_LOG", logPath)

	cfg := &config.Config{
		Owner:   "tester",
		Project: "railyard",
		Plugins: config.PluginsConfig{
			Enabled: []string{"testplugin"},
			Settings: map[string]config.PluginSettings{
				"testplugin": {Allow: config.AllowConfig{Events: []string{"*"}, Commands: []string{"*"}}},
			},
		},
	}
	bus := events.NewBus()
	defer bus.(interface{ Close() }).Close()
	host := NewHost(Dependencies{Cfg: cfg, Bus: bus, RailyardVersion: "test"})

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	host.Init(ctx)
	if names := host.Names(); len(names) != 1 || names[0] != "testplugin" {
		t.Fatalf("no-pin plugin should launch unchanged; Names = %v", names)
	}

	stopCtx, stopCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer stopCancel()
	host.Stop(stopCtx)
}

// TestRelaunch_ReverifiesPin is the load-bearing test for the spec's
// CRITICAL requirement: the integrity check must re-run on EVERY launch,
// including supervisor relaunches. We launch crashplugin (which crashes
// shortly after Start) with a pin matching its ON-DISK content, then —
// while the supervisor is relaunching it — swap the on-disk binary for a
// DIFFERENT executable. The relaunch must observe the new hash, refuse to
// exec, and permanently disable the plugin with reason integrity-mismatch
// (railyard-77h.15).
func TestRelaunch_ReverifiesPin(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skipf("pluginhost subprocess plugins require Linux SO_PEERCRED; goos=%s", runtime.GOOS)
	}

	// Build both fixtures up front.
	crashBin := buildCrashPlugin(t)
	otherBin := buildTestPlugin(t) // a binary with a DIFFERENT hash

	root := t.TempDir()
	pluginsDir := filepath.Join(root, "plugins")
	if err := os.MkdirAll(pluginsDir, 0o755); err != nil {
		t.Fatalf("mkdir plugins: %v", err)
	}
	dst := filepath.Join(pluginsDir, "crashplugin")
	if err := copyExec(crashBin, dst); err != nil {
		t.Fatalf("copy crash binary: %v", err)
	}
	// Pin to the crashplugin's content so the FIRST launch passes.
	pin := hashFileForTest(t, dst)
	swapHash := hashFileForTest(t, otherBin)
	if pin == swapHash {
		t.Fatal("test setup bug: crashplugin and testplugin hash identically")
	}

	prevWd, _ := os.Getwd()
	if err := os.Chdir(root); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(prevWd) })

	counterFile := filepath.Join(t.TempDir(), "boots.log")
	t.Setenv("RAILYARD_CRASHPLUGIN_MODE", "after_start")
	t.Setenv("RAILYARD_CRASHPLUGIN_COUNTER_FILE", counterFile)

	cfg := &config.Config{
		Owner:   "tester",
		Project: "railyard",
		Plugins: config.PluginsConfig{
			Enabled: []string{"crashplugin"},
			Settings: map[string]config.PluginSettings{
				"crashplugin": {
					Sha256: pin,
					Allow:  config.AllowConfig{Events: []string{"*"}, Commands: []string{"*"}},
				},
			},
		},
	}
	bus := events.NewBus()
	defer bus.(interface{ Close() }).Close()
	host := NewHost(Dependencies{Cfg: cfg, Bus: bus, RailyardVersion: "test"})

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	host.Init(ctx)
	// First launch must have succeeded (pin matched on-disk content).
	if got := waitForBoots(t, counterFile, 1, 10*time.Second); got < 1 {
		t.Fatalf("expected at least 1 boot before swap, got %d", got)
	}
	host.Start(ctx)

	// Swap the on-disk binary for one with a different hash. The plugin
	// crashes shortly after Start; the supervisor will relaunch, hit the
	// integrity check against the NEW on-disk content, and refuse.
	//
	// We swap via write-to-temp + atomic rename rather than overwriting in
	// place: a copy that truncates the existing file fails with ETXTBSY
	// while a subprocess still has the binary mmap'd for execution. The
	// rename atomically replaces the directory entry, so the next launch
	// resolves the NEW content by the SAME path.
	swapData, err := os.ReadFile(otherBin)
	if err != nil {
		t.Fatalf("read swap binary: %v", err)
	}
	tmp := dst + ".swap"
	if err := os.WriteFile(tmp, swapData, 0o700); err != nil {
		t.Fatalf("write swap temp: %v", err)
	}
	if err := os.Rename(tmp, dst); err != nil {
		t.Fatalf("rename swap binary: %v", err)
	}

	// Within a few seconds the supervisor should permanently disable the
	// plugin with the integrity-mismatch reason rather than keep looping.
	deadline := time.Now().Add(20 * time.Second)
	var row *PluginStatus
	for time.Now().Before(deadline) {
		snap := host.Status()
		for i := range snap.Plugins {
			if snap.Plugins[i].Name == "crashplugin" && snap.Plugins[i].Status == StatusDisabled {
				row = &snap.Plugins[i]
				break
			}
		}
		if row != nil && strings.Contains(row.Error, integrityMismatchReason) {
			break
		}
		row = nil
		time.Sleep(100 * time.Millisecond)
	}
	if row == nil {
		t.Fatalf("crashplugin never disabled with integrity-mismatch after binary swap; status = %+v", host.Status().Plugins)
	}

	stopCtx, stopCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer stopCancel()
	host.Stop(stopCtx)
}
