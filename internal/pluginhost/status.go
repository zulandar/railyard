package pluginhost

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// Snapshot is the runtime-state projection returned by [Host.Status].
// It is the wire format for GET /plugins/status served by the yardmaster
// HTTP server and consumed by `ry plugins status`.
//
// The struct is JSON-marshalable; field tags match the documented wire
// format in docs/superpowers/specs/2026-05-22-ry-plugins-status-design.md.
type Snapshot struct {
	Yardmaster YardmasterInfo `json:"yardmaster"`
	Plugins    []PluginStatus `json:"plugins"`
}

// YardmasterInfo carries the host-process identity fields the CLI surfaces
// above the plugin table.
type YardmasterInfo struct {
	Version     string    `json:"version"`
	BuildCommit string    `json:"build_commit"`
	BootedAt    time.Time `json:"booted_at"`
}

// PluginStatus is one row in the snapshot's plugins list. Field
// population rules differ by Status — see the design doc's per-state
// semantics table.
type PluginStatus struct {
	Name              string    `json:"name"`
	Status            string    `json:"status"` // running | disabled | failed | skipped
	RestartCount      int       `json:"restart_count"`
	SubscriptionCount int       `json:"subscription_count"`
	CommandCount      int       `json:"command_count"`
	LastActivity      time.Time `json:"last_activity"`
	PID               int       `json:"pid"`
	Path              string    `json:"path"`
	Error             string    `json:"error,omitempty"`
}

// Snapshot status string constants. Strings — not iota ints — because
// they cross the JSON boundary verbatim and are stable wire values.
const (
	StatusRunning  = "running"
	StatusDisabled = "disabled"
	StatusFailed   = "failed"
	StatusSkipped  = "skipped"
)

// initFailure is the host-internal record of a plugin whose launch
// succeeded but whose PluginService.Init RPC returned an error. The host
// retains the entry so [Host.Status] can report the failure to operators.
// Cleared on a subsequent successful relaunch of the same plugin name.
type initFailure struct {
	name     string
	path     string
	err      string
	failedAt time.Time
}

// skippedPlugin is the host-internal record of a plugin that appears in
// cfg.Plugins.Enabled but was not found in any plugins.d directory at
// Init time. Searched lists the directories the discovery walked, so
// operators can see *where* it was looked for.
type skippedPlugin struct {
	name     string
	searched []string
}

// Status returns a point-in-time snapshot of every configured plugin's
// runtime state. The returned struct is a deep copy — callers may
// mutate it freely. Safe for concurrent use.
//
// Plugins are returned in the slice sorted ascending by name.
func (h *Host) Status() Snapshot {
	h.mu.Lock()
	defer h.mu.Unlock()

	plugins := make([]PluginStatus, 0, len(h.launched)+len(h.initFailures)+len(h.skipped))

	for _, lp := range h.launched {
		status := StatusRunning
		errStr := ""
		if lp.disabled {
			status = StatusDisabled
			errStr = lp.lastExitReason
		}
		plugins = append(plugins, PluginStatus{
			Name:              lp.name,
			Status:            status,
			RestartCount:      lp.restartCount,
			SubscriptionCount: h.subscriptions[lp.name],
			CommandCount:      len(lp.capabilities.provideCommands),
			LastActivity:      lp.lastActivity,
			PID:               lp.pid,
			Path:              lp.path,
			Error:             errStr,
		})
	}

	for _, f := range h.initFailures {
		plugins = append(plugins, PluginStatus{
			Name:         f.name,
			Status:       StatusFailed,
			Path:         f.path,
			LastActivity: f.failedAt,
			Error:        f.err,
		})
	}

	for _, s := range h.skipped {
		plugins = append(plugins, PluginStatus{
			Name:   s.name,
			Status: StatusSkipped,
			Error:  fmt.Sprintf("not found in: %s", strings.Join(s.searched, ", ")),
		})
	}

	sort.Slice(plugins, func(i, j int) bool { return plugins[i].Name < plugins[j].Name })

	return Snapshot{
		Yardmaster: YardmasterInfo{
			Version:     h.deps.RailyardVersion,
			BuildCommit: h.deps.BuildCommit,
			BootedAt:    h.bootedAt,
		},
		Plugins: plugins,
	}
}
