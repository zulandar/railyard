package pluginhost

import (
	"testing"
	"time"

	goplugin "github.com/hashicorp/go-plugin"
)

// TestPrepareRestartRejectsConcurrent proves Restart is serialized per name
// (railyard-uv8.4): once a restart is in progress for a plugin, a second
// prepareRestart for the same name is rejected rather than proceeding to a
// parallel launch that would overwrite h.launched and orphan a live
// subprocess.
func TestPrepareRestartRejectsConcurrent(t *testing.T) {
	h := NewHost(Dependencies{})
	lp := &launchedPlugin{name: "p", path: "/does/not/matter"}
	h.mu.Lock()
	h.launched["p"] = lp
	h.mu.Unlock()

	if _, _, err := h.prepareRestart("p"); err != nil {
		t.Fatalf("first prepareRestart: unexpected error %v", err)
	}
	if _, _, err := h.prepareRestart("p"); err == nil {
		t.Fatal("second prepareRestart while one is in progress must error")
	}

	// Once the in-progress restart clears, a fresh restart is allowed again.
	h.clearRestarting("p")
	if _, _, err := h.prepareRestart("p"); err != nil {
		t.Fatalf("prepareRestart after clear: unexpected error %v", err)
	}
}

// TestWaitForExitWakesOnPluginStop proves the supervisor's exit wait wakes
// on a per-plugin stop signal even when the subprocess is perfectly healthy
// (railyard-uv8.3). Without this, an operator Restart racing an in-flight
// crash-relaunch leaves the supervisor parked on the freshly-relaunched
// healthy subprocess and awaitSupervisor blocks forever — the
// POST /plugins/restart request hangs unboundedly. A zero-value go-plugin
// client never reports Exited(), modelling a healthy subprocess.
func TestWaitForExitWakesOnPluginStop(t *testing.T) {
	h := NewHost(Dependencies{})
	lp := &launchedPlugin{
		client: &goplugin.Client{}, // Exited() stays false: healthy subprocess
		stopCh: make(chan struct{}),
	}

	done := make(chan bool, 1)
	go func() { done <- h.waitForExitOrShutdown(lp) }()

	// It must block: no shutdown, no stop signal, subprocess healthy.
	select {
	case <-done:
		t.Fatal("waitForExitOrShutdown returned before any stop/shutdown signal")
	case <-time.After(100 * time.Millisecond):
	}

	// Closing the per-plugin stop channel must wake it and report "no
	// relaunch" (false), exactly like a host shutdown.
	close(lp.stopCh)
	select {
	case relaunch := <-done:
		if relaunch {
			t.Error("waitForExitOrShutdown returned true (relaunch) on plugin stop; want false")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("waitForExitOrShutdown did not wake on stopCh — restart would hang")
	}
}
