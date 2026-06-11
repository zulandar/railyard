package pluginhost

import (
	"fmt"
	"sort"
	"strings"
	"time"

	protov1 "github.com/zulandar/railyard/pkg/plugin/proto/v1"
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
// above the plugin table. BootedAt uses `omitzero` so the field is dropped
// when zero — avoids leaking "0001-01-01T00:00:00Z" to consumers in the
// nil-provider (no plugin host configured) path.
type YardmasterInfo struct {
	Version     string    `json:"version"`
	BuildCommit string    `json:"build_commit"`
	BootedAt    time.Time `json:"booted_at,omitzero"`
}

// PluginStatus is one row in the snapshot's plugins list. Field
// population rules differ by Status — see the design doc's per-state
// semantics table. LastActivity uses `omitzero` so skipped rows (which
// never observe activity) omit the field rather than emit "0001-01-01..".
type PluginStatus struct {
	Name              string    `json:"name"`
	Status            string    `json:"status"` // running | disabled | failed | skipped
	SDKVersion        string    `json:"sdk_version,omitempty"`
	RestartCount      int       `json:"restart_count"`
	SubscriptionCount int       `json:"subscription_count"`
	CommandCount      int       `json:"command_count"`
	LastActivity      time.Time `json:"last_activity,omitzero"`
	PID               int       `json:"pid"`
	Path              string    `json:"path"`
	Error             string    `json:"error,omitempty"`

	// Optional health probe (railyard-77h.12). Health is the last verdict
	// from the plugin's PluginService.Health RPC — "ok" / "degraded" /
	// "failing", or "n/a" for a plugin that does not implement
	// HealthReporter. Empty (omitted) for non-running rows and for a
	// running plugin not yet polled. HealthMessage carries the plugin's
	// own message or the probe error text; HealthCheckedAt is the time of
	// the last completed poll (omitzero so unpolled/non-running rows stay
	// clean).
	Health          string    `json:"health,omitempty"`
	HealthMessage   string    `json:"health_message,omitempty"`
	HealthCheckedAt time.Time `json:"health_checked_at,omitzero"`

	// CommandSignatures renders each command the plugin owns as a compact
	// "name(arg:type, ...)" string (railyard-77h.16). Commands a plugin
	// registered with a typed schema (RegisterCommandSpec) show their
	// declared args; bare commands (RegisterCommand) render as "name()".
	// Sorted by command name for deterministic output. Populated only for
	// running plugins; omitted when empty so non-running rows stay clean.
	// Surfaced by `ry plugins status -v`.
	CommandSignatures []string `json:"command_signatures,omitempty"`

	// Per-plugin lifetime runtime counters (railyard-77h.14). These are
	// process-lifetime cumulative (reset on yard restart) but survive a
	// plugin relaunch. Populated only for running plugins; other states
	// never observe events or commands. CommandLatencyAvgMicros is derived
	// at snapshot time (total / handled, 0 when handled == 0).
	EventsDelivered           uint64 `json:"events_delivered"`
	EventsDropped             uint64 `json:"events_dropped"`
	CommandsHandled           uint64 `json:"commands_handled"`
	CommandsFailed            uint64 `json:"commands_failed"`
	CommandLatencyTotalMicros uint64 `json:"command_latency_total_micros"`
	CommandLatencyAvgMicros   uint64 `json:"command_latency_avg_micros"`
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

// disabledPlugin is the host-internal record of a plugin that ran
// successfully and was later permanently disabled (e.g. crash-budget
// exhausted). Snapshot is taken at disable time; fields are immutable
// thereafter. [Host.Status] reads h.disabled to surface the "disabled"
// row that the documented 4-state model promises. Lives in a separate
// map from h.launched because handlePermanentDisable also removes the
// plugin from h.launched to keep dispatch lookups clean.
//
// Read/written under [Host.mu].
type disabledPlugin struct {
	name           string
	path           string
	pid            int
	restartCount   int
	lastActivity   time.Time
	lastExitReason string
	commandCount   int
}

// skippedPlugin is the host-internal record of a plugin that appears in
// cfg.Plugins.Enabled but was not found in any plugins.d directory at
// Init time. Searched lists the directories the discovery walked, so
// operators can see *where* it was looked for.
type skippedPlugin struct {
	name     string
	searched []string
}

// commandSignaturesLocked renders a plugin's owned command names as
// compact "name(arg:type, ...)" signatures (railyard-77h.16). A command
// with a stored typed schema (RegisterCommandSpec) shows its declared
// args; a bare command renders as "name()". Output is sorted by command
// name for deterministic display.
//
// Caller MUST hold h.mu (R or W) — it reads h.pluginCmdSpecs.
func (h *Host) commandSignaturesLocked(cmds []string) []string {
	if len(cmds) == 0 {
		return nil
	}
	sorted := append([]string(nil), cmds...)
	sort.Strings(sorted)
	out := make([]string, 0, len(sorted))
	for _, cmd := range sorted {
		out = append(out, formatCommandSignature(cmd, h.pluginCmdSpecs[cmd]))
	}
	return out
}

// formatCommandSignature renders one command as "name(arg:type, ...)".
// A nil/empty spec (a bare command, or one with no declared args) renders
// as "name()". Arg types use the lowercase short names string/int/bool/
// float; an unspecified type renders as "any".
func formatCommandSignature(name string, spec *protov1.CommandSchema) string {
	var b strings.Builder
	b.WriteString(name)
	b.WriteByte('(')
	if spec != nil {
		for i, a := range spec.Args {
			if a == nil {
				continue
			}
			if i > 0 {
				b.WriteString(", ")
			}
			b.WriteString(a.Name)
			b.WriteByte(':')
			b.WriteString(argTypeShortName(a.Type))
		}
	}
	b.WriteByte(')')
	return b.String()
}

// argTypeShortName maps a wire ArgType to its compact display name.
func argTypeShortName(t protov1.ArgType) string {
	switch t {
	case protov1.ArgType_ARG_TYPE_STRING:
		return "string"
	case protov1.ArgType_ARG_TYPE_INT:
		return "int"
	case protov1.ArgType_ARG_TYPE_BOOL:
		return "bool"
	case protov1.ArgType_ARG_TYPE_FLOAT:
		return "float"
	default:
		return "any"
	}
}

// Status returns a point-in-time snapshot of every configured plugin's
// runtime state. The returned struct is a deep copy — callers may
// mutate it freely. Safe for concurrent use.
//
// Plugins are returned in the slice sorted ascending by name.
func (h *Host) Status() Snapshot {
	h.mu.RLock()
	defer h.mu.RUnlock()

	plugins := make([]PluginStatus, 0, len(h.launched)+len(h.disabled)+len(h.initFailures)+len(h.skipped))

	for _, lp := range h.launched {
		// Read the lifetime counters with lock-free atomic Loads. We are
		// already under h.mu.RLock here, but the counters themselves are
		// mutated only via sync/atomic (never under the lock) so Load is
		// the correct, consistent read. Avg latency is derived now.
		handled := lp.commandsHandled.Load()
		totalMicros := lp.commandLatencyTotalMicros.Load()
		var avgMicros uint64
		if handled > 0 {
			avgMicros = totalMicros / handled
		}
		plugins = append(plugins, PluginStatus{
			Name:              lp.name,
			Status:            StatusRunning,
			SDKVersion:        lp.sdkVersion,
			RestartCount:      lp.restartCount,
			SubscriptionCount: h.subscriptions[lp.name],
			CommandCount:      len(lp.capabilities.provideCommands),
			LastActivity:      lp.lastActivity,
			PID:               lp.pid,
			Path:              lp.path,

			Health:          lp.healthValue,
			HealthMessage:   lp.healthMessage,
			HealthCheckedAt: lp.healthCheckedAt,

			CommandSignatures: h.commandSignaturesLocked(lp.capabilities.provideCommands),

			EventsDelivered:           lp.eventsDelivered.Load(),
			EventsDropped:             lp.eventsDropped.Load(),
			CommandsHandled:           handled,
			CommandsFailed:            lp.commandsFailed.Load(),
			CommandLatencyTotalMicros: totalMicros,
			CommandLatencyAvgMicros:   avgMicros,
		})
	}

	for _, dp := range h.disabled {
		plugins = append(plugins, PluginStatus{
			Name:         dp.name,
			Status:       StatusDisabled,
			RestartCount: dp.restartCount,
			CommandCount: dp.commandCount,
			LastActivity: dp.lastActivity,
			PID:          dp.pid,
			Path:         dp.path,
			Error:        dp.lastExitReason,
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
