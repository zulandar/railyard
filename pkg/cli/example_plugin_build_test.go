// Package cli — end-to-end integration test for the documented
// hello-world plugin at examples/plugins/hello/.
//
// This test is the regression contract for docs/plugins/authoring.md §2:
// if the plugin SDK or the pluginhost ever drift in a way that breaks
// the documented quickstart, this test catches it before the guide goes
// stale.
//
// The test:
//
//  1. Shells out to `go build` inside examples/plugins/hello/ to compile
//     the standalone plugin binary. The example is its own Go module with
//     `replace github.com/zulandar/railyard => ../../..`, so the build
//     exercises the in-tree SDK against the in-tree plugin.
//  2. Drops the binary into a temp `plugins.d` directory.
//  3. Constructs a real events.Bus and pluginhost.Host pointed at that
//     directory via the typed config.
//  4. Drives Init → Start, publishes a CarCreated event, and asserts
//     the plugin's `hello: car created` log line is captured by a
//     swapped-in slog handler. The plugin emits logs over its
//     HostService.Log RPC, which the host forwards through
//     slog.Default().Handler() — so installing a capture handler as the
//     default for the test window is sufficient to observe the line.
//  5. Cleans up: host.Stop kills the subprocess and removes the socket
//     file; the test verifies host.Names() returns empty afterwards.
//
// Allow-list filtering (Lane F, railyard-fll.4) is wired. The Config
// built below grants the hello plugin the CarCreated event so the
// runtime Subscribe enforcement permits the subscription. Without the
// allow block the host would PermissionDenied the stream.
package cli

import (
	"bytes"
	"context"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/zulandar/railyard/internal/config"
	"github.com/zulandar/railyard/internal/events"
	"github.com/zulandar/railyard/internal/pluginhost"
	"github.com/zulandar/railyard/pkg/plugin"
)

// TestOSSExamplePluginEndToEnd builds examples/plugins/hello, launches
// it via pluginhost, publishes a CarCreated event, and asserts the
// plugin's log line was forwarded to the host's slog handler.
//
// Skip conditions:
//   - `-short` (the build of the hello binary takes a few seconds; CI
//     runs the full suite without -short).
//   - `go` toolchain not on PATH.
//   - non-Linux: the host's SO_PEERCRED verification path is
//     Linux-specific (internal/pluginhost/peercred_*.go); skipping
//     elsewhere matches the bound on TestLaunchPluginHappyPath.
func TestOSSExamplePluginEndToEnd(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping example plugin end-to-end test under -short; runs in full CI")
	}
	if runtime.GOOS != "linux" {
		t.Skipf("pluginhost subprocess plugins require Linux SO_PEERCRED; goos=%s", runtime.GOOS)
	}

	goBin, err := exec.LookPath("go")
	if err != nil {
		t.Skipf("go toolchain not available: %v", err)
	}

	repoRoot, err := findRepoRoot()
	if err != nil {
		t.Fatalf("finding repo root: %v", err)
	}

	exampleDir := filepath.Join(repoRoot, "examples", "plugins", "hello")
	if fi, err := os.Stat(exampleDir); err != nil || !fi.IsDir() {
		t.Fatalf("example dir missing at %s: %v", exampleDir, err)
	}

	// --- 1) Build the hello binary into a temp plugins.d ---------------
	// We build directly into the plugins.d we'll point the host at, so
	// the binary is named exactly `hello` (the discovery scanner uses
	// the basename to match `plugins.enabled`).
	pluginsDir := t.TempDir()
	binPath := filepath.Join(pluginsDir, "hello")

	// 60s mirrors TestOSSSmokeBuild — generous for a cold build with
	// module-cache miss, tight enough that a wedged toolchain doesn't
	// hang CI.
	buildCtx, buildCancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer buildCancel()

	buildCmd := exec.CommandContext(buildCtx, goBin, "build", "-o", binPath, ".")
	buildCmd.Dir = exampleDir
	var buildOut bytes.Buffer
	buildCmd.Stdout = &buildOut
	buildCmd.Stderr = &buildOut
	if err := buildCmd.Run(); err != nil {
		t.Fatalf("`go build -o %s .` in %s failed: %v\nbuild output:\n%s",
			binPath, exampleDir, err, buildOut.String())
	}
	// Plugin binaries should typically be 0700; the go toolchain already
	// gives us an executable file, but tighten perms to match the spec.
	if err := os.Chmod(binPath, 0o700); err != nil {
		t.Fatalf("chmod %s: %v", binPath, err)
	}

	// --- 2) Swap slog.Default to a capture handler ---------------------
	// The plugin emits its log line over HostService.Log; the host's
	// hostservice.Log forwards via slog.Default().Handler().Handle. By
	// installing our capture handler as the default for the duration of
	// the test we can assert on the forwarded line.
	cap, capBuf := newCaptureHandler()
	prevDefault := slog.Default()
	slog.SetDefault(slog.New(cap))
	t.Cleanup(func() { slog.SetDefault(prevDefault) })

	// --- 3) Build host + bus pointed at the plugins.d ------------------
	cfg := &config.Config{
		Owner:   "tester",
		Project: "railyard",
		Plugins: config.PluginsConfig{
			Enabled:    []string{"hello"},
			PluginsDir: pluginsDir,
			Settings: map[string]config.PluginSettings{
				"hello": {
					Allow: config.AllowConfig{
						Events: []string{string(plugin.CarCreated)},
					},
				},
			},
		},
	}
	bus := events.NewBus()
	t.Cleanup(func() {
		if closer, ok := bus.(interface{ Close() }); ok {
			closer.Close()
		}
	})
	host := pluginhost.NewHost(pluginhost.Dependencies{
		Cfg:             cfg,
		Bus:             bus,
		RailyardVersion: "test",
	})

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	host.Init(ctx)
	if names := host.Names(); len(names) != 1 || names[0] != "hello" {
		t.Fatalf("expected one launched plugin %q, got %v", "hello", names)
	}
	host.Start(ctx)

	// --- 4) Publish CarCreated and wait for the plugin's log line ------
	//
	// Publish is fire-and-forget: PluginService.Start returns when the
	// plugin's Start() body returns, but Start() invokes
	// host.Subscribe(...) which spins up a gRPC HostService.Subscribe
	// stream asynchronously. The host-side bus subscription is wired
	// only after the plugin's stream-server goroutine reaches the
	// Bus.Subscribe call. There is no synchronous "subscribed" signal
	// crossing the boundary, so we re-publish on a short cadence until
	// the log line shows up (or the budget expires).
	//
	// 8s budget gives a cold subprocess + gRPC stream plenty of slack
	// on busy CI without letting a wedged host hang the suite.
	const (
		waitBudget = 8 * time.Second
		waitStep   = 100 * time.Millisecond
	)
	publishEvent := func() {
		bus.Publish(string(plugin.CarCreated), plugin.CarCreatedEvent{
			CarID:       "car-e2e-hello",
			Track:       "backend",
			Type:        "feature",
			Priority:    7,
			RequestedBy: "alice",
		})
	}
	publishEvent()
	deadline := time.Now().Add(waitBudget)
	found := false
	for time.Now().Before(deadline) {
		if strings.Contains(capBuf.snapshot(), "hello: car created") {
			found = true
			break
		}
		time.Sleep(waitStep)
		publishEvent()
	}
	if !found {
		t.Fatalf("plugin log line %q not observed within %v\ncaptured log:\n%s",
			"hello: car created", waitBudget, capBuf.snapshot())
	}

	// The forwarded record should carry the plugin attribution (added
	// by hostservice.Log) and the typed-event attrs the plugin's
	// onCarCreated emitted. We assert on the textual form rather than
	// parsing JSON to keep the test resilient to handler-format choice.
	captured := capBuf.snapshot()
	for _, want := range []string{
		"plugin=hello",
		"id=car-e2e-hello",
		"track=backend",
		"type=feature",
		"priority=7",
		"requested_by=alice",
	} {
		if !strings.Contains(captured, want) {
			t.Errorf("captured log missing %q\nfull capture:\n%s", want, captured)
		}
	}

	// --- 5) Stop and verify cleanup ------------------------------------
	stopCtx, stopCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer stopCancel()
	host.Stop(stopCtx)

	if names := host.Names(); len(names) != 0 {
		t.Errorf("expected zero launched plugins after Stop, got %v", names)
	}
}

// captureHandler is a slog.Handler that writes text records into a
// mutex-guarded buffer. We use the stdlib slog.TextHandler under the
// hood so the output shape matches what real operators see and so
// attribute assertions can match e.g. `plugin=hello` directly.
//
// All clones (returned from WithAttrs/WithGroup) share the same
// underlying buffer + mutex via the lockedBuf indirection — slog
// handlers may be cloned by the host's per-plugin
// slog.Default().With(...) call in hostService.newHostService.
type captureHandler struct {
	mu  *sync.Mutex
	buf *lockedBuf
	h   slog.Handler
}

func newCaptureHandler() (*captureHandler, *lockedBuf) {
	lb := &lockedBuf{}
	mu := &sync.Mutex{}
	lb.mu = mu
	ch := &captureHandler{mu: mu, buf: lb}
	ch.h = slog.NewTextHandler(lb, &slog.HandlerOptions{Level: slog.LevelDebug})
	return ch, lb
}

func (c *captureHandler) Enabled(ctx context.Context, l slog.Level) bool {
	return c.h.Enabled(ctx, l)
}
func (c *captureHandler) Handle(ctx context.Context, r slog.Record) error {
	return c.h.Handle(ctx, r)
}
func (c *captureHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &captureHandler{mu: c.mu, buf: c.buf, h: c.h.WithAttrs(attrs)}
}
func (c *captureHandler) WithGroup(name string) slog.Handler {
	return &captureHandler{mu: c.mu, buf: c.buf, h: c.h.WithGroup(name)}
}

// lockedBuf is a mutex-guarded io.Writer + snapshot reader. The
// underlying TextHandler may write concurrently from multiple host
// goroutines (the subscribe-fanout pump and the host's own boot logs);
// the mutex serialises both writes and reads.
type lockedBuf struct {
	mu  *sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuf) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuf) snapshot() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}
