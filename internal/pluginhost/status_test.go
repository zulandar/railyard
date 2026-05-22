package pluginhost

import (
	"sort"
	"testing"
	"time"

	"github.com/zulandar/railyard/pkg/plugin"
)

func newStatusFixtureHost(t *testing.T) *Host {
	t.Helper()
	return &Host{
		bootedAt: time.Unix(1_700_000_000, 0),
		clock:    func() time.Time { return time.Unix(1_700_000_100, 0) },
		yardInfo: plugin.YardInfo{}, // unused by Status but Host requires it set elsewhere
		launched: map[string]*launchedPlugin{
			"running-plugin": {
				name:         "running-plugin",
				path:         "/etc/railyard/plugins.d/running-plugin",
				pid:          12345,
				restartCount: 0,
				lastActivity: time.Unix(1_700_000_050, 0),
				capabilities: pluginCapabilities{provideCommands: []string{"do_a", "do_b"}},
			},
			"disabled-plugin": {
				name:           "disabled-plugin",
				path:           "/etc/railyard/plugins.d/disabled-plugin",
				pid:            54321,
				disabled:       true,
				restartCount:   4,
				lastActivity:   time.Unix(1_700_000_010, 0),
				lastExitReason: "crash budget exceeded",
			},
		},
		subscriptions: map[string]int{
			"running-plugin":  3,
			"disabled-plugin": 0,
		},
		initFailures: map[string]initFailure{
			"broken-plugin": {
				name:     "broken-plugin",
				path:     "/etc/railyard/plugins.d/broken-plugin",
				err:      "init: yard not ready",
				failedAt: time.Unix(1_700_000_005, 0),
			},
		},
		skipped: []skippedPlugin{
			{name: "missing-plugin", searched: []string{"/etc/railyard/plugins.d", "./plugins"}},
		},
	}
}

func TestStatusReportsAllFourStates(t *testing.T) {
	h := newStatusFixtureHost(t)
	snap := h.Status()
	if !sort.SliceIsSorted(snap.Plugins, func(i, j int) bool { return snap.Plugins[i].Name < snap.Plugins[j].Name }) {
		t.Fatal("plugins slice must be sorted by name")
	}
	wantStates := map[string]string{
		"broken-plugin":   StatusFailed,
		"disabled-plugin": StatusDisabled,
		"missing-plugin":  StatusSkipped,
		"running-plugin":  StatusRunning,
	}
	if len(snap.Plugins) != len(wantStates) {
		t.Fatalf("plugin count = %d, want %d", len(snap.Plugins), len(wantStates))
	}
	for _, p := range snap.Plugins {
		want, ok := wantStates[p.Name]
		if !ok {
			t.Errorf("unexpected plugin in snapshot: %s", p.Name)
			continue
		}
		if p.Status != want {
			t.Errorf("plugin %s: status=%q want %q", p.Name, p.Status, want)
		}
	}
}

func TestStatusRunningFieldsPopulated(t *testing.T) {
	h := newStatusFixtureHost(t)
	snap := h.Status()
	var got *PluginStatus
	for i := range snap.Plugins {
		if snap.Plugins[i].Name == "running-plugin" {
			got = &snap.Plugins[i]
			break
		}
	}
	if got == nil {
		t.Fatal("running-plugin missing from snapshot")
	}
	if got.PID != 12345 {
		t.Errorf("PID = %d, want 12345", got.PID)
	}
	if got.RestartCount != 0 {
		t.Errorf("RestartCount = %d, want 0", got.RestartCount)
	}
	if got.SubscriptionCount != 3 {
		t.Errorf("SubscriptionCount = %d, want 3", got.SubscriptionCount)
	}
	if got.CommandCount != 2 {
		t.Errorf("CommandCount = %d, want 2", got.CommandCount)
	}
	if got.Path != "/etc/railyard/plugins.d/running-plugin" {
		t.Errorf("Path = %q", got.Path)
	}
	if got.LastActivity.Unix() != 1_700_000_050 {
		t.Errorf("LastActivity = %v, want unix 1700000050", got.LastActivity)
	}
	if got.Error != "" {
		t.Errorf("running plugin Error = %q, want empty", got.Error)
	}
}

func TestStatusDisabledIncludesExitReason(t *testing.T) {
	h := newStatusFixtureHost(t)
	snap := h.Status()
	for _, p := range snap.Plugins {
		if p.Name == "disabled-plugin" {
			if p.Error != "crash budget exceeded" {
				t.Errorf("disabled.Error = %q, want %q", p.Error, "crash budget exceeded")
			}
			if p.RestartCount != 4 {
				t.Errorf("disabled.RestartCount = %d, want 4", p.RestartCount)
			}
			return
		}
	}
	t.Fatal("disabled-plugin missing")
}

func TestStatusFailedHasInitError(t *testing.T) {
	h := newStatusFixtureHost(t)
	snap := h.Status()
	for _, p := range snap.Plugins {
		if p.Name == "broken-plugin" {
			if p.Status != StatusFailed {
				t.Errorf("status = %q", p.Status)
			}
			if p.Error != "init: yard not ready" {
				t.Errorf("Error = %q", p.Error)
			}
			if p.PID != 0 || p.SubscriptionCount != 0 || p.CommandCount != 0 {
				t.Errorf("failed plugin should have zero counts; got pid=%d subs=%d cmds=%d",
					p.PID, p.SubscriptionCount, p.CommandCount)
			}
			if p.LastActivity.Unix() != 1_700_000_005 {
				t.Errorf("LastActivity = %v, want failedAt", p.LastActivity)
			}
			return
		}
	}
	t.Fatal("broken-plugin missing")
}

func TestStatusSkippedReportsSearchPaths(t *testing.T) {
	h := newStatusFixtureHost(t)
	snap := h.Status()
	for _, p := range snap.Plugins {
		if p.Name == "missing-plugin" {
			if p.Status != StatusSkipped {
				t.Errorf("status = %q", p.Status)
			}
			if p.Error == "" {
				t.Errorf("skipped Error must list search paths; got empty")
			}
			if p.PID != 0 || p.Path != "" {
				t.Errorf("skipped should have zero pid + empty path; got pid=%d path=%q", p.PID, p.Path)
			}
			return
		}
	}
	t.Fatal("missing-plugin missing")
}

func TestStatusYardmasterInfoPopulated(t *testing.T) {
	h := newStatusFixtureHost(t)
	h.deps.RailyardVersion = "1.2.3"
	h.deps.BuildCommit = "abc1234"
	snap := h.Status()
	if snap.Yardmaster.Version != "1.2.3" {
		t.Errorf("Version = %q", snap.Yardmaster.Version)
	}
	if snap.Yardmaster.BuildCommit != "abc1234" {
		t.Errorf("BuildCommit = %q", snap.Yardmaster.BuildCommit)
	}
	if snap.Yardmaster.BootedAt.Unix() != 1_700_000_000 {
		t.Errorf("BootedAt = %v", snap.Yardmaster.BootedAt)
	}
}
