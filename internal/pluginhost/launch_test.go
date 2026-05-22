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
	"github.com/zulandar/railyard/pkg/plugin"
)

// buildTestPlugin compiles the helper plugin binary under
// testdata/testplugin and returns its absolute path. The binary is
// rebuilt on every test invocation; for repeated runs in a single
// `go test` session this is a one-time cost.
func buildTestPlugin(t *testing.T) string {
	t.Helper()
	if testing.Short() {
		t.Skip("subprocess plugin build is slow; skip under -short")
	}
	goBin, err := exec.LookPath("go")
	if err != nil {
		t.Skipf("go toolchain not available: %v", err)
	}
	src, err := filepath.Abs(filepath.Join("testdata", "testplugin"))
	if err != nil {
		t.Fatalf("abs path: %v", err)
	}
	out := filepath.Join(t.TempDir(), "testplugin")
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
		t.Fatalf("build testplugin: %v\n%s", err, buf.String())
	}
	if err := os.Chmod(out, 0o700); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	return out
}

// TestLaunchPluginHappyPath builds the helper plugin, drops it in a
// plugins.d-style directory, drives Init → Start → Stop on a real host,
// and asserts the plugin's per-event log file shows the expected
// lifecycle + delivery lines. End-to-end coverage of the new gRPC plumbing.
func TestLaunchPluginHappyPath(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("UDS not supported on Windows")
	}

	bin := buildTestPlugin(t)

	// Set up a plugins.d in the working dir so the discovery scanner
	// picks up our binary. We have to chdir because discoverCandidates
	// reads os.Getwd().
	root := t.TempDir()
	pluginsDir := filepath.Join(root, "plugins")
	if err := os.MkdirAll(pluginsDir, 0o755); err != nil {
		t.Fatalf("mkdir plugins: %v", err)
	}
	dst := filepath.Join(pluginsDir, "testplugin")
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

	// Direct the plugin's log file.
	logPath := filepath.Join(t.TempDir(), "events.log")
	t.Setenv("RAILYARD_TESTPLUGIN_LOG", logPath)

	cfg := &config.Config{
		Owner:   "tester",
		Project: "railyard",
		Plugins: config.PluginsConfig{
			Enabled: []string{"testplugin"},
			Settings: map[string]config.PluginSettings{
				"testplugin": {Allow: config.AllowConfig{
					Events:   []string{"*"},
					Commands: []string{"*"},
				}},
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
		t.Fatalf("expected one launched plugin, got %v", names)
	}
	host.Start(ctx)

	// Publish a CarCreated event and wait for the plugin to record it.
	bus.Publish(string(plugin.CarCreated), plugin.CarCreatedEvent{
		CarID:    "car-123",
		Track:    "go",
		Type:     "feature",
		Priority: 2,
	})

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if got, err := os.ReadFile(logPath); err == nil && strings.Contains(string(got), "event car_id=car-123") {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	stopCtx, stopCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer stopCancel()
	host.Stop(stopCtx)

	got, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read plugin log %s: %v", logPath, err)
	}
	out := string(got)
	for _, want := range []string{
		"init ok",
		"start ok",
		"yard project=railyard owner=tester",
		"event car_id=car-123",
		"stop ok",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("plugin log missing %q\nfull log:\n%s", want, out)
		}
	}

	// Socket file should be cleaned up after Stop.
	if names := host.Names(); len(names) != 0 {
		t.Errorf("expected zero launched plugins after Stop, got %v", names)
	}
}

// TestLaunchPluginRejectsNonExecutable confirms a binary missing the
// exec bit is skipped during discovery — the host does not even try to
// launch it.
func TestLaunchPluginRejectsNonExecutable(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX executable bit semantics")
	}
	bin := buildTestPlugin(t)

	root := t.TempDir()
	pluginsDir := filepath.Join(root, "plugins")
	if err := os.MkdirAll(pluginsDir, 0o755); err != nil {
		t.Fatalf("mkdir plugins: %v", err)
	}
	dst := filepath.Join(pluginsDir, "testplugin")
	if err := copyFileMode(bin, dst, 0o644); err != nil { // NOT executable
		t.Fatalf("copy: %v", err)
	}

	prevWd, _ := os.Getwd()
	if err := os.Chdir(root); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(prevWd) })

	cfg := &config.Config{Plugins: config.PluginsConfig{Enabled: []string{"testplugin"}}}
	host := NewHost(Dependencies{Cfg: cfg})
	host.Init(context.Background())
	if names := host.Names(); len(names) != 0 {
		t.Errorf("non-executable plugin should not launch; Names = %v", names)
	}
}

// copyExec copies src to dst with mode 0700.
func copyExec(src, dst string) error {
	return copyFileMode(src, dst, 0o700)
}

// copyFileMode copies src to dst with the specified mode.
func copyFileMode(src, dst string, mode os.FileMode) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, data, mode)
}
