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
				sdkVersion:   "9.9.9",
				capabilities: pluginCapabilities{provideCommands: []string{"do_a", "do_b"}},
			},
		},
		subscriptions: map[string]int{
			"running-plugin": 3,
		},
		// disabled-plugin lives in h.disabled (not h.launched) — that
		// mirrors production, where handlePermanentDisable moves the
		// entry across maps in one critical section.
		disabled: map[string]*disabledPlugin{
			"disabled-plugin": {
				name:           "disabled-plugin",
				path:           "/etc/railyard/plugins.d/disabled-plugin",
				pid:            54321,
				restartCount:   4,
				lastActivity:   time.Unix(1_700_000_010, 0),
				lastExitReason: "crash budget exceeded",
			},
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
	if got.SDKVersion != "9.9.9" {
		t.Errorf("SDKVersion = %q, want 9.9.9", got.SDKVersion)
	}
}

// TestStatusSDKVersionOnlyForRunning asserts the reported SDK version is
// surfaced for running plugins and omitted for the other states, which
// never observe an InitResponse (railyard-77h.8).
func TestStatusSDKVersionOnlyForRunning(t *testing.T) {
	h := newStatusFixtureHost(t)
	snap := h.Status()
	for _, p := range snap.Plugins {
		switch p.Name {
		case "running-plugin":
			if p.SDKVersion != "9.9.9" {
				t.Errorf("running SDKVersion = %q, want 9.9.9", p.SDKVersion)
			}
		default:
			if p.SDKVersion != "" {
				t.Errorf("%s SDKVersion = %q, want empty", p.Name, p.SDKVersion)
			}
		}
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

// TestMarkPermanentlyDisabledSurfacesViaStatus is the railyard-kuh
// regression. Before this fix, handlePermanentDisable set lp.disabled=true
// AND delete(h.launched, lp.name) in the same critical section, but
// Status() only iterated h.launched — so a disabled plugin was invisible
// in real execution. The fix moves the entry into h.disabled, which
// Status() iterates as a separate source. This test drives the move
// directly and asserts the disabled row reaches the snapshot.
func TestMarkPermanentlyDisabledSurfacesViaStatus(t *testing.T) {
	h := &Host{
		bootedAt:      time.Unix(1_700_000_000, 0),
		clock:         func() time.Time { return time.Unix(1_700_000_100, 0) },
		launched:      map[string]*launchedPlugin{},
		subscriptions: map[string]int{},
		initFailures:  map[string]initFailure{},
		disabled:      map[string]*disabledPlugin{},
		pluginCmds:    map[string]string{"do_a": "doomed-plugin", "do_b": "doomed-plugin"},
	}
	lp := &launchedPlugin{
		name:           "doomed-plugin",
		path:           "/etc/railyard/plugins.d/doomed-plugin",
		pid:            99999,
		restartCount:   3,
		lastActivity:   time.Unix(1_700_000_080, 0),
		lastExitReason: "crash budget exceeded",
		capabilities:   pluginCapabilities{provideCommands: []string{"do_a", "do_b"}},
	}
	h.launched["doomed-plugin"] = lp

	h.markPermanentlyDisabled(lp)

	// Removed from h.launched, present in h.disabled.
	if _, stillLaunched := h.launched["doomed-plugin"]; stillLaunched {
		t.Fatal("plugin must be removed from h.launched")
	}
	dp, ok := h.disabled["doomed-plugin"]
	if !ok {
		t.Fatal("plugin must be in h.disabled after markPermanentlyDisabled")
	}
	if dp.lastExitReason != "crash budget exceeded" {
		t.Errorf("disabled.lastExitReason = %q", dp.lastExitReason)
	}
	if dp.restartCount != 3 {
		t.Errorf("disabled.restartCount = %d", dp.restartCount)
	}
	if dp.commandCount != 2 {
		t.Errorf("disabled.commandCount = %d", dp.commandCount)
	}
	if len(h.pluginCmds) != 0 {
		t.Errorf("pluginCmds entries owned by disabled plugin must be cleared; got %v", h.pluginCmds)
	}

	// Status() must surface the row as 'disabled'.
	snap := h.Status()
	var row *PluginStatus
	for i := range snap.Plugins {
		if snap.Plugins[i].Name == "doomed-plugin" {
			row = &snap.Plugins[i]
			break
		}
	}
	if row == nil {
		t.Fatal("disabled plugin missing from Status() snapshot")
	}
	if row.Status != StatusDisabled {
		t.Errorf("status = %q, want %q", row.Status, StatusDisabled)
	}
	if row.Error != "crash budget exceeded" {
		t.Errorf("error = %q", row.Error)
	}
	if row.PID != 99999 {
		t.Errorf("PID = %d", row.PID)
	}
	if row.RestartCount != 3 {
		t.Errorf("RestartCount = %d", row.RestartCount)
	}
	if row.CommandCount != 2 {
		t.Errorf("CommandCount = %d", row.CommandCount)
	}
}

// TestStatusSurfacesRuntimeCounters asserts the per-plugin lifetime
// counters living on launchedPlugin (railyard-77h.14) are read out of
// the atomics and surfaced on the running plugin's PluginStatus row.
// CommandLatencyAvgMicros is derived at display time as
// commandLatencyTotalMicros / commandsHandled.
func TestStatusSurfacesRuntimeCounters(t *testing.T) {
	h := newStatusFixtureHost(t)
	lp := h.launched["running-plugin"]
	lp.eventsDelivered.Store(100)
	lp.eventsDropped.Store(7)
	lp.commandsHandled.Store(4)
	lp.commandsFailed.Store(1)
	lp.commandLatencyTotalMicros.Store(8000) // avg = 2000us over 4 handled

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
	if got.EventsDelivered != 100 {
		t.Errorf("EventsDelivered = %d, want 100", got.EventsDelivered)
	}
	if got.EventsDropped != 7 {
		t.Errorf("EventsDropped = %d, want 7", got.EventsDropped)
	}
	if got.CommandsHandled != 4 {
		t.Errorf("CommandsHandled = %d, want 4", got.CommandsHandled)
	}
	if got.CommandsFailed != 1 {
		t.Errorf("CommandsFailed = %d, want 1", got.CommandsFailed)
	}
	if got.CommandLatencyTotalMicros != 8000 {
		t.Errorf("CommandLatencyTotalMicros = %d, want 8000", got.CommandLatencyTotalMicros)
	}
	if got.CommandLatencyAvgMicros != 2000 {
		t.Errorf("CommandLatencyAvgMicros = %d, want 2000", got.CommandLatencyAvgMicros)
	}
}

// TestStatusCounterAvgZeroWhenNoCommands asserts the derived average is
// zero (not a divide-by-zero panic) when no commands have been handled.
func TestStatusCounterAvgZeroWhenNoCommands(t *testing.T) {
	h := newStatusFixtureHost(t)
	snap := h.Status()
	for _, p := range snap.Plugins {
		if p.Name == "running-plugin" {
			if p.CommandLatencyAvgMicros != 0 {
				t.Errorf("CommandLatencyAvgMicros = %d, want 0 with zero handled", p.CommandLatencyAvgMicros)
			}
			return
		}
	}
	t.Fatal("running-plugin missing")
}

// TestStatusSurfacesHealth asserts the optional health probe result
// living on launchedPlugin (railyard-77h.12) is surfaced on the running
// plugin's PluginStatus row, including the checked-at timestamp.
func TestStatusSurfacesHealth(t *testing.T) {
	h := newStatusFixtureHost(t)
	lp := h.launched["running-plugin"]
	lp.healthValue = healthValueDegraded
	lp.healthMessage = "github API 401"
	lp.healthCheckedAt = time.Unix(1_700_000_090, 0)

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
	if got.Health != healthValueDegraded {
		t.Errorf("Health = %q, want %q", got.Health, healthValueDegraded)
	}
	if got.HealthMessage != "github API 401" {
		t.Errorf("HealthMessage = %q, want %q", got.HealthMessage, "github API 401")
	}
	if !got.HealthCheckedAt.Equal(time.Unix(1_700_000_090, 0)) {
		t.Errorf("HealthCheckedAt = %v, want 1_700_000_090", got.HealthCheckedAt)
	}
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
