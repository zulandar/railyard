package pluginhost

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"sort"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	goplugin "github.com/hashicorp/go-plugin"
	"github.com/zulandar/railyard/pkg/plugin"
	protov1 "github.com/zulandar/railyard/pkg/plugin/proto/v1"
)

// stopDrainTimeout is the per-plugin Stop deadline (spec §4). A plugin
// whose Stop blocks longer than this is abandoned and core shutdown
// continues.
const stopDrainTimeout = 5 * time.Second

// launchedPlugin captures the runtime handle for one subprocess plugin.
// It is created during [Host.Init] when go-plugin successfully completes
// the handshake; it is removed during [Host.Stop] (or earlier, on a
// permanent disable triggered by SO_PEERCRED mismatch).
type launchedPlugin struct {
	// name is the stable identifier — matches the plugin binary's
	// basename and the railyard.yaml allow-list entry.
	name string

	// path is the absolute path to the plugin binary.
	path string

	// socketPath is the UDS the host bound for this plugin's RPC
	// channel. Removed on Stop.
	socketPath string

	// client is the go-plugin client owning the subprocess. Kill() ends
	// the subprocess; the go-plugin library handles signal forwarding
	// and exit-code observation.
	client *goplugin.Client

	// pluginRPC is the PluginService stub the host invokes to drive the
	// plugin's lifecycle (Init/Start/Stop/HandleCommand).
	pluginRPC protov1.PluginServiceClient

	// pid is the operating-system pid of the subprocess. Recorded for
	// log diagnostics and for the SO_PEERCRED check.
	pid int

	// capabilities is the AllowedEvents / AllowedCommands intersection
	// the host echoed back during Init. Recorded so DispatchCommand can
	// confirm the plugin actually advertised the command before
	// routing.
	capabilities pluginCapabilities

	// sdkVersion is the pkg/plugin SDK version the plugin reported in its
	// InitResponse (railyard-77h.8). Empty for a plugin built before
	// version reporting. Set once at Init; surfaced by Status(). Read
	// under [Host.mu].
	sdkVersion string

	// allow is the per-plugin capability allow-list resolved from
	// railyard.yaml at Init time (railyard-fll.4). It is consulted on
	// every Subscribe and DispatchCommand to enforce the policy at
	// runtime. The zero value denies every capability — that is the
	// strict default when no allow block is configured.
	allow AllowList

	// logger is a slog scope with `plugin=<name>` already attached.
	logger *slog.Logger

	// subOnce / subCancel are populated by the Subscribe RPC server;
	// holding them on the launched plugin lets Stop cancel any
	// outstanding event streams.
	subMu      sync.Mutex
	subCancels []context.CancelFunc

	// budget tracks crash recurrence inside a 60s sliding window. The
	// 4th crash inside the window flips the plugin into a
	// permanently-disabled state. handlePermanentDisable then moves the
	// snapshot into Host.disabled and removes us from h.launched.
	budget *crashBudget

	// stopping is set true the moment the host begins a planned
	// shutdown for this plugin (host Stop, or a successful Plugin.Stop
	// after which we don't want a restart). The supervisor consults
	// this flag on every subprocess-exit observation: stopping=true
	// suppresses the restart loop and stops budget accounting.
	//
	// stopping lives on launchedPlugin (not on a global host channel)
	// so per-plugin Stop semantics stay independent. Read/written under
	// [Host.mu].
	stopping bool

	// superviseDone is closed by the supervisor goroutine when it has
	// fully exited (either after permanent-disable or after a planned
	// shutdown). [Host.Stop] waits on this so we never race the host's
	// teardown with a restart attempt.
	//
	// May be nil for the brief window between launchedPlugin
	// construction and supervisor spawn; consumers must nil-check.
	superviseDone chan struct{}

	// stopCh is closed by an operator restart (signalPluginStop) to wake
	// this plugin's supervisor out of its exit-wait WITHOUT a host-wide
	// shutdown — the per-plugin analogue of h.shutdownCh. The supervisor
	// selects on it in waitForExitOrShutdown and treats a close as a
	// planned stop (no relaunch). It lets Restart unblock the supervisor
	// even when it is parked on a freshly-relaunched healthy subprocess,
	// closing the unbounded-hang window (railyard-uv8.3). Created in
	// startSupervisor alongside superviseDone; nil before the supervisor
	// spawns, so consumers must nil-check (a nil channel never selects).
	// Closed under [Host.mu] via signalPluginStop.
	stopCh chan struct{}

	// consecutiveCrashes is the count of crashes since the last
	// successful (re)launch. The supervisor uses it to index into
	// [backoffSchedule]; it is reset on a clean Init.
	//
	// Touched only by the supervisor goroutine — no synchronization
	// required.
	consecutiveCrashes int

	// lastExitReason is a short, human-readable string describing why
	// the subprocess last terminated unexpectedly. Surfaced in the
	// permanent-disable ERROR log AND by Status() for disabled rows.
	// Read/written under [Host.mu]. Same-goroutine reads in
	// handlePermanentDisable run sequentially after the supervisor's
	// locked write, so they see the latest value without re-locking.
	lastExitReason string

	// restartCount is the cumulative count of successful supervisor
	// relaunches since this host booted. Distinct from
	// consecutiveCrashes, which resets on a clean Init. Read/written
	// under [Host.mu].
	restartCount int

	// lastActivity is the most recent timestamp at which this plugin
	// did something the host noticed: successful Init, Start,
	// supervisor relaunch, DispatchCommand hit, or Subscribe. Event
	// delivery into the plugin's subscription stream does NOT bump
	// this field (hot path). Read/written under [Host.mu].
	lastActivity time.Time

	// --- Optional health probe state (railyard-77h.12) ---
	//
	// The host polls each running plugin's optional PluginService.Health
	// RPC on a configurable interval (default 30s). These fields hold the
	// most recent result. They are NOT on the per-event hot path (a poll
	// happens at most once per interval), so unlike the railyard-77h.14
	// counters they are read/written under [Host.mu] rather than via
	// atomics.

	// healthValue is the last observed health verdict: one of
	// healthValueOK / healthValueDegraded / healthValueFailing /
	// healthValueNA. Empty before the first poll. A plugin that does not
	// implement HealthReporter (Unimplemented) is recorded as
	// healthValueNA, not an error.
	healthValue string

	// healthMessage is the human-readable message from the last Health
	// probe — the plugin's own message on success, or the error text on
	// a degraded (RPC error/timeout) result. Empty for n/a.
	healthMessage string

	// healthCheckedAt is the wall-clock time (from h.clock) of the last
	// completed Health poll. Zero before the first poll; used by Status()
	// to render the health age.
	healthCheckedAt time.Time

	// --- Per-plugin lifetime runtime counters (railyard-77h.14) ---
	//
	// HOT-PATH RULE: these counters are touched on the per-event delivery
	// path (subscribe.go) and the command-dispatch path (hostservice.go).
	// They MUST be mutated with sync/atomic ONLY — NEVER acquire [Host.mu]
	// in the per-event loop to bump them. They live on launchedPlugin (not
	// on the subprocess or a per-subscription value) so they are
	// process-lifetime cumulative AND survive a plugin relaunch (the
	// supervisor reuses the same *launchedPlugin and only swaps the dead
	// subprocess's handles in place — see supervise.go relaunch). They
	// reset only when the yard process itself restarts.
	//
	// Status() reads them with a lock-free Load() (cheap); it may do so
	// while already holding [Host.mu.RLock], which is fine — the rule is
	// only that the EVENT loop adds no locking.

	// eventsDelivered counts events successfully sent on this plugin's
	// subscription stream(s) over the host's lifetime.
	eventsDelivered atomic.Uint64

	// eventsDropped counts events dropped on the drop-oldest backpressure
	// path before reaching this plugin.
	eventsDropped atomic.Uint64

	// commandsHandled counts commands routed into this plugin's
	// PluginService.HandleCommand (counted once the RPC is invoked,
	// regardless of outcome).
	commandsHandled atomic.Uint64

	// commandsFailed counts HandleCommand invocations that returned a
	// transport error OR a logical failure (resp.Success == false).
	commandsFailed atomic.Uint64

	// commandLatencyTotalMicros is the cumulative wall-clock time spent in
	// PluginService.HandleCommand, in microseconds. The display layer
	// derives the average as commandLatencyTotalMicros / commandsHandled.
	commandLatencyTotalMicros atomic.Uint64
}

// pluginCapabilities is the host's view of the negotiated capability
// surface for a single plugin.
type pluginCapabilities struct {
	subscribeEvents []string
	provideCommands []string
}

// Names returns the names of every currently launched plugin, sorted.
// Used by `ry plugins list` (once it is rewired in railyard-hqe) and by
// the boot summary log line.
func (h *Host) Names() []string {
	h.mu.RLock()
	defer h.mu.RUnlock()
	out := make([]string, 0, len(h.launched))
	for name := range h.launched {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// LaunchedPluginInfo is a read-only projection of a single launched
// plugin's identity and status for external introspection (e.g. the
// future `ry plugins list` rewire tracked by railyard-hqe).
type LaunchedPluginInfo struct {
	Name       string
	Path       string
	SocketPath string
	PID        int
}

// LaunchedPlugins returns a snapshot of every launched plugin, sorted by
// name. Safe for concurrent use.
func (h *Host) LaunchedPlugins() []LaunchedPluginInfo {
	h.mu.RLock()
	defer h.mu.RUnlock()
	out := make([]LaunchedPluginInfo, 0, len(h.launched))
	for _, lp := range h.launched {
		out = append(out, LaunchedPluginInfo{
			Name:       lp.name,
			Path:       lp.path,
			SocketPath: lp.socketPath,
			PID:        lp.pid,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// lookupPluginByName returns the launched plugin under name, or nil.
// Holds the lock for the duration of the read.
func (h *Host) lookupPluginByName(name string) *launchedPlugin {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.launched[name]
}

// lookupPluginByCommand resolves a plugin-registered command name to the
// owning launched plugin, or nil. The pluginCmds map is populated during
// Init from each plugin's AllowedCommands.
func (h *Host) lookupPluginByCommand(cmdName string) *launchedPlugin {
	h.mu.RLock()
	defer h.mu.RUnlock()
	owner, ok := h.pluginCmds[cmdName]
	if !ok {
		return nil
	}
	return h.launched[owner]
}

// lookupPluginCmdSpec returns the typed argument schema a subprocess
// plugin declared for cmdName via RegisterCommandSpec (railyard-77h.16),
// or nil if the command has no stored spec (a bare RegisterCommand, or a
// plugin built before spec reporting). A nil return tells
// DispatchCommand to skip arg validation for that command. Holds the lock
// for the duration of the read.
func (h *Host) lookupPluginCmdSpec(cmdName string) *protov1.CommandSchema {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.pluginCmdSpecs[cmdName]
}

// removeLaunched deletes the launched-plugin entry for name and returns
// the removed struct (or nil). Idempotent.
func (h *Host) removeLaunched(name string) *launchedPlugin {
	h.mu.Lock()
	defer h.mu.Unlock()
	lp, ok := h.launched[name]
	if !ok {
		return nil
	}
	delete(h.launched, name)
	// Also drop any command-registry rows owned by this plugin, including
	// the typed arg specs stored alongside them (railyard-77h.16).
	for cmd, owner := range h.pluginCmds {
		if owner == name {
			delete(h.pluginCmds, cmd)
			delete(h.pluginCmdSpecs, cmd)
		}
	}
	return lp
}

// Init discovers plugin binaries, launches every binary in
// `plugins.enabled`, completes the gRPC handshake, and calls
// PluginService.Init on each.
//
// A plugin that fails to launch or whose Init returns an error is
// removed from the running set, the subprocess killed, and the socket
// cleaned up. Other plugins and core continue.
//
// Init walks plugins serially. Within each plugin: launch → peer-cred
// check → PluginService.Init → record capabilities. Parallel launch is
// a future bead.
func (h *Host) Init(ctx context.Context) {
	if h.deps.Cfg == nil {
		return
	}
	enabled := h.deps.Cfg.Plugins.Enabled
	if len(enabled) == 0 {
		return
	}
	extra := h.deps.Cfg.Plugins.PluginsDir
	logger := slog.Default()

	cs := discoverCandidates(extra, logger)
	launch, missing := filterEnabled(cs, enabled)

	if len(missing) > 0 {
		// Same directory resolution as discoverCandidates — shared so the
		// "not found in: …" diagnostic reflects the paths actually walked.
		searched := pluginSearchDirs(extra)

		h.mu.Lock()
		for _, name := range missing {
			// Per-entry copy so a future append on one skippedPlugin's
			// `searched` cannot mutate every other entry via a shared
			// backing array.
			perEntry := append([]string(nil), searched...)
			h.skipped = append(h.skipped, skippedPlugin{name: name, searched: perEntry})
			logger.Warn("pluginhost: enabled plugin not found on disk",
				slog.String("plugin", name),
			)
		}
		h.mu.Unlock()
	}

	for _, c := range launch {
		h.initOne(ctx, c, logger)
	}
}

// initOne handles the launch + handshake + PluginService.Init dance for
// a single plugin candidate. Any error along the way causes the
// subprocess to be killed and the socket cleaned up before the function
// returns. Logged at WARN.
func (h *Host) initOne(ctx context.Context, c candidate, parentLogger *slog.Logger) {
	pluginLogger := parentLogger.With(slog.String("plugin", c.name))
	pluginLogger.Info("plugin " + c.name + ": init")

	lp, err := h.launchPluginOnce(ctx, c, pluginLogger)
	if err != nil {
		// A failed sha256 pin (railyard-77h.15) is a permanent, distinct
		// disable — NOT an ordinary init failure. No launchedPlugin exists
		// yet on this first-boot refusal, so record a disabled entry
		// directly with the integrity-mismatch reason (the relaunch path
		// reuses the existing launchedPlugin via handlePermanentDisable
		// instead). verifyBinaryPin already WARN-logged both hashes.
		var integ *integrityMismatchError
		if errors.As(err, &integ) {
			h.mu.Lock()
			h.disabled[c.name] = &disabledPlugin{
				name:           c.name,
				path:           c.path,
				lastExitReason: integrityMismatchReason,
			}
			h.mu.Unlock()
			return
		}
		pluginLogger.Warn(
			"plugin "+c.name+": launch failed — skipped ("+err.Error()+")",
			slog.String("error", err.Error()),
		)
		h.mu.Lock()
		h.initFailures[c.name] = initFailure{
			name:     c.name,
			path:     c.path,
			err:      err.Error(),
			failedAt: h.clock(),
		}
		h.mu.Unlock()
		return
	}

	// Resolve the per-plugin allow-list from config BEFORE invoking
	// PluginService.Init so we can stash it on launchedPlugin even when
	// Init's response is empty. A plugin listed in `enabled` with no
	// settings entry gets the strict default (zero AllowList → deny all).
	allow := h.resolveAllowList(c.name)
	lp.allow = allow

	// Call PluginService.Init. Capabilities advertisement is the
	// plugin's responsibility — Lane D's SDK fills resp.AllowedEvents
	// and resp.AllowedCommands from the user's Subscribe /
	// RegisterCommand calls during impl.Init.
	initCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	resp, err := lp.pluginRPC.Init(initCtx, &protov1.InitRequest{
		PluginName:           c.name,
		Capabilities:         &protov1.Capabilities{},
		SupportedEventTopics: canonicalEventTopics(),
	})
	if err != nil {
		pluginLogger.Warn(
			"plugin "+c.name+": Init RPC failed — skipped ("+err.Error()+")",
			slog.String("error", err.Error()),
		)
		h.mu.Lock()
		h.initFailures[c.name] = initFailure{
			name:     c.name,
			path:     c.path,
			err:      err.Error(),
			failedAt: h.clock(),
		}
		h.mu.Unlock()
		lp.client.Kill()
		removeSocket(lp.socketPath)
		return
	}

	// Intersect the plugin's advertised wish-list with the configured
	// allow-list. Denied caps are logged at WARN; the plugin still runs
	// (per the .4.3 design decision — denials are surfaced in logs +
	// InitResponse, not fatal). The host stores ONLY the allowed subset
	// in lp.capabilities so subsequent dispatch routing trusts it.
	advertisedEvents := append([]string(nil), resp.AllowedEvents...)
	advertisedCmds := append([]string(nil), resp.AllowedCommands...)
	allowedEvents, deniedEvents := filterAllowedEvents(advertisedEvents, allow)
	allowedCmds, deniedCmds := filterAllowedCommands(advertisedCmds, allow)
	for _, name := range deniedEvents {
		pluginLogger.Warn(
			"pluginhost: capability denied",
			slog.String("plugin", c.name),
			slog.String("cap", "event:"+name),
			slog.String("reason", "not-in-allow-list"),
		)
	}
	for _, name := range deniedCmds {
		pluginLogger.Warn(
			"pluginhost: capability denied",
			slog.String("plugin", c.name),
			slog.String("cap", "command:"+name),
			slog.String("reason", "not-in-allow-list"),
		)
	}

	// Record the filtered capability surface.
	lp.capabilities = pluginCapabilities{
		subscribeEvents: allowedEvents,
		provideCommands: allowedCmds,
	}

	// Record the plugin's reported SDK version (railyard-77h.8). Set
	// before startSupervisor inserts lp into h.launched, so there is no
	// concurrent reader yet. Empty for plugins built before version
	// reporting.
	lp.sdkVersion = resp.SdkVersion

	// Index the typed arg specs the plugin reported (railyard-77h.16) by
	// command name so the ownership loop below can stash the matching
	// spec alongside h.pluginCmds. A command with no entry here (a bare
	// RegisterCommand, or a plugin built before spec reporting) is dispatched
	// without arg validation, exactly as before.
	specByName := make(map[string]*protov1.CommandSchema, len(resp.CommandSpecs))
	for _, cs := range resp.CommandSpecs {
		if cs == nil || cs.Name == "" {
			continue
		}
		specByName[cs.Name] = cs
	}

	// Register command ownership BEFORE spawning the supervisor so a
	// crash-restart race cannot leave a window where the plugin's
	// commands look unowned.
	h.mu.Lock()
	for _, cmd := range allowedCmds {
		if cmd == "" {
			continue
		}
		if _, taken := h.allowed[cmd]; taken {
			pluginLogger.Warn(
				"pluginhost: plugin command conflicts with core allow-list — ignoring",
				slog.String("command", cmd),
			)
			continue
		}
		if existing, taken := h.pluginCmds[cmd]; taken {
			pluginLogger.Warn(
				"pluginhost: plugin command name collision — keeping first registration",
				slog.String("command", cmd),
				slog.String("first_plugin", existing),
				slog.String("second_plugin", lp.name),
			)
			continue
		}
		h.pluginCmds[cmd] = lp.name
		// Stash the typed arg spec (if the plugin declared one via
		// RegisterCommandSpec) so DispatchCommand validates against it.
		if spec, ok := specByName[cmd]; ok && len(spec.Args) > 0 {
			h.pluginCmdSpecs[cmd] = spec
		}
	}
	h.mu.Unlock()

	// Hand the plugin to the supervisor — it owns the registry
	// insertion AND the restart loop for the lifetime of the host.
	h.startSupervisor(ctx, c, lp)

	// Init success: record that the plugin was just active.
	h.bumpActivity(c.name)

	// Note: we do NOT touch h.initFailures here. Init() walks each plugin
	// exactly once and routes failures to the early returns above, so
	// reaching this line guarantees no prior initFailure entry exists
	// for c.name. Supervisor relaunch() bypasses initOne entirely. If a
	// future code path adds in-place re-init (e.g. config reload), it
	// will need to clear initFailures itself in the same lock acquisition
	// that re-inserts into h.launched.
}

// canonicalEventTopics returns the host's advertised event-topic list
// for the Init handshake, derived from the SDK's
// pkg/plugin.CoreEventTypes() so the advertised set cannot drift from
// the SDK constants (railyard-77h.8). The plugin uses it to warn on a
// subscription to a topic the host cannot deliver.
func canonicalEventTopics() []string {
	core := plugin.CoreEventTypes()
	out := make([]string, 0, len(core))
	for _, et := range core {
		out = append(out, string(et))
	}
	return out
}

// hostInitRequest builds the InitRequest the host sends on every launch
// path — first boot, crash-relaunch, and operator restart. Centralising it
// guarantees the host always advertises its supported event topics
// (railyard-77h.8) so a relaunch re-negotiates exactly like the first boot
// rather than degrading to a bare request (railyard-uv8.7).
func hostInitRequest(name string) *protov1.InitRequest {
	return &protov1.InitRequest{
		PluginName:           name,
		Capabilities:         &protov1.Capabilities{},
		SupportedEventTopics: canonicalEventTopics(),
	}
}

// applyInitResponse intersects a freshly-received InitResponse's advertised
// capabilities with lp.allow, records the filtered surface + reported SDK
// version on lp, and refreshes the host command registry for lp.name: it
// first drops every command ownership AND arg spec this plugin currently
// holds, then re-registers from the response. Clearing first means a
// relaunched binary that dropped, renamed, or re-typed a command cannot
// leave stale ownership or a stale arg schema behind (railyard-uv8.7,
// compounding railyard-uv8.2). It is the shared response-application step
// for the crash-relaunch and operator-restart paths; initOne keeps its own
// first-boot variant because it additionally WARN-logs each denied cap.
//
// Holds h.mu for the registry mutation.
func (h *Host) applyInitResponse(lp *launchedPlugin, resp *protov1.InitResponse) {
	allowedEvents, _ := filterAllowedEvents(append([]string(nil), resp.AllowedEvents...), lp.allow)
	allowedCmds, _ := filterAllowedCommands(append([]string(nil), resp.AllowedCommands...), lp.allow)

	specByName := make(map[string]*protov1.CommandSchema, len(resp.CommandSpecs))
	for _, cs := range resp.CommandSpecs {
		if cs == nil || cs.Name == "" {
			continue
		}
		specByName[cs.Name] = cs
	}

	h.mu.Lock()
	defer h.mu.Unlock()

	lp.capabilities = pluginCapabilities{
		subscribeEvents: allowedEvents,
		provideCommands: allowedCmds,
	}
	lp.sdkVersion = resp.SdkVersion

	// Drop this plugin's previous ownership + specs so a changed command
	// set cannot leave stale entries.
	for cmd, owner := range h.pluginCmds {
		if owner == lp.name {
			delete(h.pluginCmds, cmd)
			delete(h.pluginCmdSpecs, cmd)
		}
	}

	for _, cmd := range allowedCmds {
		if cmd == "" {
			continue
		}
		if _, taken := h.allowed[cmd]; taken {
			lp.logger.Warn("pluginhost: plugin command conflicts with core allow-list — ignoring",
				slog.String("command", cmd))
			continue
		}
		if existing, taken := h.pluginCmds[cmd]; taken {
			lp.logger.Warn("pluginhost: plugin command name collision — keeping first registration",
				slog.String("command", cmd),
				slog.String("first_plugin", existing),
				slog.String("second_plugin", lp.name))
			continue
		}
		h.pluginCmds[cmd] = lp.name
		if spec, ok := specByName[cmd]; ok && len(spec.Args) > 0 {
			h.pluginCmdSpecs[cmd] = spec
		}
	}
}

// resolveAllowList builds the per-plugin AllowList from the loaded
// config. A plugin without a settings entry receives the strict default
// (zero-value AllowList; every cap denied).
func (h *Host) resolveAllowList(name string) AllowList {
	if h.deps.Cfg == nil {
		return AllowList{}
	}
	s, ok := h.deps.Cfg.Plugins.Settings[name]
	if !ok {
		return AllowList{}
	}
	return newAllowList(s.Allow)
}

// filterAllowedEvents intersects advertised event topics with the
// allow-list. Returns the allowed subset (in advertised order) and the
// denied complement.
func filterAllowedEvents(advertised []string, allow AllowList) (allowed, denied []string) {
	for _, e := range advertised {
		if e == "" {
			continue
		}
		if allow.AllowEvent(e) {
			allowed = append(allowed, e)
		} else {
			denied = append(denied, e)
		}
	}
	return allowed, denied
}

// filterAllowedCommands intersects advertised command names with the
// allow-list. Returns the allowed subset (in advertised order) and the
// denied complement.
func filterAllowedCommands(advertised []string, allow AllowList) (allowed, denied []string) {
	for _, c := range advertised {
		if c == "" {
			continue
		}
		if allow.AllowCommand(c) {
			allowed = append(allowed, c)
		} else {
			denied = append(denied, c)
		}
	}
	return allowed, denied
}

// Start calls PluginService.Start on every launched plugin. A plugin
// whose Start fails is left in the registry (so subsequent Stop is
// orderly) but logged at WARN.
//
// Start also flips h.started, which the supervisor consults: a plugin
// crash-restart that lands AFTER Start has been called must re-drive
// Start on the relaunched subprocess, otherwise the plugin would
// stay Init'd-but-not-Started after a relaunch.
func (h *Host) Start(ctx context.Context) {
	h.mu.Lock()
	h.started = true
	names := make([]string, 0, len(h.launched))
	for n := range h.launched {
		names = append(names, n)
	}
	h.mu.Unlock()
	sort.Strings(names)

	for _, name := range names {
		lp := h.lookupPluginByName(name)
		if lp == nil {
			continue
		}
		startCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		if _, err := lp.pluginRPC.Start(startCtx, &protov1.StartRequest{}); err != nil {
			lp.logger.Warn("plugin "+name+": Start failed",
				slog.String("error", err.Error()),
			)
			cancel()
			continue
		}
		cancel()
		lp.logger.Info(
			fmt.Sprintf("plugin %s: started (events=%d commands=%d)",
				name, len(lp.capabilities.subscribeEvents), len(lp.capabilities.provideCommands)),
			slog.Int("events", len(lp.capabilities.subscribeEvents)),
			slog.Int("commands", len(lp.capabilities.provideCommands)),
		)
		// Start success: record that the plugin was just active.
		h.bumpActivity(name)
	}

	// Launch the single health-poll goroutine once core is up
	// (railyard-77h.12). It joins through supervisorWG and stops when
	// shutdownCh closes, so [Host.Stop] reaps it with no leak. Guarded by
	// healthPollOnce so a repeated Start cannot spawn a second poller.
	h.healthPollOnce.Do(func() {
		d := time.Duration(defaultHealthIntervalSec()) * time.Second
		if h.deps.Cfg != nil {
			d = h.deps.Cfg.Plugins.HealthInterval()
		}
		h.supervisorWG.Add(1)
		go h.healthPollLoop(ctx, d)
	})
}

// Stop calls PluginService.Stop on each launched plugin in reverse-name
// order, then escalates SIGTERM → 5s wait → SIGKILL. Each plugin gets a
// per-call [stopDrainTimeout] budget for the Stop RPC; a plugin whose
// Stop blocks past that is abandoned but the subprocess is still
// signalled afterwards.
//
// Stop cancels every outstanding Subscribe stream BEFORE invoking the
// plugin's Stop RPC, so a plugin waiting on an event recv loop does not
// deadlock on shutdown.
//
// Stop ordering with respect to the supervisor loop (railyard-fll.6):
//  1. Mark each lp.stopping=true under the host lock so a concurrent
//     subprocess exit observed by the supervisor reads as "planned".
//  2. Close h.shutdownCh so any in-flight backoff sleep returns early.
//  3. For each plugin: cancel subscriptions, drive PluginService.Stop,
//     send SIGTERM, wait up to 5s, fall back to SIGKILL.
//  4. Reset the plugin's crashBudget so a future railyard restart
//     starts fresh.
//  5. After all plugins have been torn down, block on supervisorWG so
//     no relaunch attempt is still in flight when Stop returns.
//
// Idempotent: subsequent Stop calls are no-ops (shutdownCh is closed
// at most once via shutdownOnce).
func (h *Host) Stop(parent context.Context) {
	h.mu.Lock()
	names := make([]string, 0, len(h.launched))
	for n, lp := range h.launched {
		// Mark each plugin stopping under the lock that the supervisor
		// reads via isPluginStopping; this is the race guard ensuring
		// no concurrent crash observed mid-Stop triggers a relaunch.
		lp.stopping = true
		names = append(names, n)
	}
	h.mu.Unlock()
	sort.Sort(sort.Reverse(sort.StringSlice(names)))

	// Wake every per-plugin supervisor and short-circuit any pending
	// backoff sleep.
	h.shutdownOnce.Do(func() { close(h.shutdownCh) })

	for _, name := range names {
		lp := h.lookupPluginByName(name)
		if lp == nil {
			continue
		}
		h.cancelPluginSubscriptions(lp)

		ctx, cancel := context.WithTimeout(parent, stopDrainTimeout)
		done := make(chan error, 1)
		go func() {
			_, err := lp.pluginRPC.Stop(ctx, &protov1.StopRequest{DrainTimeoutMs: stopDrainTimeout.Milliseconds()})
			done <- err
		}()
		select {
		case err := <-done:
			if err != nil {
				lp.logger.Warn("plugin "+name+": Stop returned error",
					slog.String("error", err.Error()))
			} else {
				lp.logger.Info("plugin " + name + ": stopped")
			}
		case <-ctx.Done():
			lp.logger.Warn("plugin "+name+": Stop drain timeout exceeded — abandoned",
				slog.Duration("timeout", stopDrainTimeout))
		}
		cancel()

		// SIGTERM → 5s wait → SIGKILL escalation. go-plugin's
		// client.Kill() jumps straight to SIGKILL via Process.Kill;
		// we wrap that with a graceful SIGTERM first so well-behaved
		// plugins can clean up.
		h.terminateSubprocess(lp)

		// Reset the crash budget so a future railyard restart starts
		// fresh — the brief explicitly requires this on graceful Stop.
		if lp.budget != nil {
			lp.budget.reset()
		}

		removeSocket(lp.socketPath)
		h.removeLaunched(name)
	}

	// Block on every supervisor goroutine before returning. Without
	// this, a relaunch attempt could still be in flight (e.g. inside
	// launchPluginOnce's handshake) when Stop returns, leaving a
	// zombie subprocess. The shutdownCh / lp.stopping signals above
	// guarantee the supervisor exits promptly; this Wait is the join.
	h.supervisorWG.Wait()
}

// terminateSubprocess performs the SIGTERM → 5s wait → SIGKILL
// escalation on lp's subprocess.
//
// We extract the pid from go-plugin's recorded value (registry.go) and
// send signals directly via os.Process. If the process exits cleanly
// within [stopDrainTimeout], we still invoke client.Kill() to release
// go-plugin's internal goroutines (its own clientWaitGroup) — the
// double-call is a no-op once the process is gone.
func (h *Host) terminateSubprocess(lp *launchedPlugin) {
	pid := lp.pid
	if pid > 0 {
		proc, err := os.FindProcess(pid)
		if err == nil {
			if err := proc.Signal(syscall.SIGTERM); err != nil {
				// Process may already be gone — fine, fall through
				// to client.Kill() for go-plugin bookkeeping.
				lp.logger.Debug("plugin "+lp.name+": SIGTERM send failed (process may already be gone)",
					slog.Int("pid", pid),
					slog.String("error", err.Error()),
				)
			}
		}
	}

	// Wait up to 5s for graceful exit, polling go-plugin's Exited().
	deadline := h.clock().Add(stopDrainTimeout)
	for h.clock().Before(deadline) {
		if lp.client.Exited() {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	// Escalate to SIGKILL via go-plugin (also tears down the
	// library's internal goroutines). Idempotent on an already-dead
	// process.
	lp.client.Kill()
}

// cancelPluginSubscriptions cancels every Subscribe stream owned by lp.
// Called from Stop before the plugin's Stop RPC fires.
func (h *Host) cancelPluginSubscriptions(lp *launchedPlugin) {
	lp.subMu.Lock()
	cancels := lp.subCancels
	lp.subCancels = nil
	lp.subMu.Unlock()
	for _, c := range cancels {
		c()
	}
}
