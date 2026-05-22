package pluginhost

import (
	"bytes"
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/zulandar/railyard/internal/config"
	"github.com/zulandar/railyard/internal/events"
)

// TestLifecycleLogLinesAndSocketCleanup is the subprocess-era replacement
// for the host-side lifecycle log assertions that lived in the deleted
// (legacy_inproc-tagged) lifecycle_log_test.go. It launches the testplugin
// fixture and asserts the host emits the spec-shaped lifecycle log lines
// — "plugin <name>: init", "plugin <name>: started (events=N commands=M)",
// "plugin <name>: stopped" — and that the UDS socket file is removed
// after Stop. Gap-fill tracked by bd issue railyard-bjp.
//
// Skip conditions match TestLaunchPluginHappyPath: -short and non-Linux.
func TestLifecycleLogLinesAndSocketCleanup(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skipf("pluginhost subprocess plugins require Linux SO_PEERCRED; goos=%s", runtime.GOOS)
	}

	// Capture slog output for the duration of the test. The host writes
	// every lifecycle line through slog.Default(); per-plugin scoping is
	// achieved via .With(plugin=<name>), which the stdlib TextHandler
	// emits as `plugin=<name>` attrs alongside the message.
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

	prevWd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
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
				"testplugin": {Allow: config.AllowConfig{
					Events:   []string{"*"},
					Commands: []string{"*"},
				}},
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
	host.Start(ctx)

	// Capture the socket path for the post-Stop cleanup assertion.
	infos := host.LaunchedPlugins()
	if len(infos) != 1 {
		t.Fatalf("expected 1 launched plugin, got %d", len(infos))
	}
	socketPath := infos[0].SocketPath
	if socketPath == "" {
		t.Fatal("expected non-empty SocketPath")
	}
	if _, err := os.Stat(socketPath); err != nil {
		t.Errorf("socket file should exist after Start: %v", err)
	}

	stopCtx, stopCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer stopCancel()
	host.Stop(stopCtx)

	// Host-side lifecycle log lines (subprocess equivalents of spec §4).
	got := capBuf.snapshot()
	for _, want := range []string{
		"plugin testplugin: init",
		"plugin testplugin: started (events=",
		"plugin testplugin: stopped",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing host lifecycle line %q\nfull capture:\n%s", want, got)
		}
	}

	// Socket file should be gone after Stop's removeSocket call.
	if _, err := os.Stat(socketPath); !os.IsNotExist(err) {
		t.Errorf("socket file %s should be removed after Stop; stat err = %v", socketPath, err)
	}

	if names := host.Names(); len(names) != 0 {
		t.Errorf("expected zero launched plugins after Stop, got %v", names)
	}
}

// lifecycleCaptureHandler is a minimal slog.Handler that funnels every
// record into a mutex-guarded buffer via the stdlib TextHandler. Cloning
// (WithAttrs / WithGroup) shares the underlying buffer so per-plugin
// scoped loggers still write to the same place.
type lifecycleCaptureHandler struct {
	buf *lifecycleLockedBuf
	h   slog.Handler
}

func newLifecycleCaptureHandler() (*lifecycleCaptureHandler, *lifecycleLockedBuf) {
	lb := &lifecycleLockedBuf{}
	ch := &lifecycleCaptureHandler{buf: lb}
	ch.h = slog.NewTextHandler(lb, &slog.HandlerOptions{Level: slog.LevelDebug})
	return ch, lb
}

func (c *lifecycleCaptureHandler) Enabled(ctx context.Context, l slog.Level) bool {
	return c.h.Enabled(ctx, l)
}
func (c *lifecycleCaptureHandler) Handle(ctx context.Context, r slog.Record) error {
	return c.h.Handle(ctx, r)
}
func (c *lifecycleCaptureHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &lifecycleCaptureHandler{buf: c.buf, h: c.h.WithAttrs(attrs)}
}
func (c *lifecycleCaptureHandler) WithGroup(name string) slog.Handler {
	return &lifecycleCaptureHandler{buf: c.buf, h: c.h.WithGroup(name)}
}

// lifecycleLockedBuf is a mutex-guarded io.Writer with a snapshot reader.
type lifecycleLockedBuf struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lifecycleLockedBuf) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lifecycleLockedBuf) snapshot() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}
