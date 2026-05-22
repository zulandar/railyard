package pluginhost

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"sync"
	"time"

	goplugin "github.com/hashicorp/go-plugin"
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

	// disabled is true once a fatal-for-lifetime condition (e.g.
	// SO_PEERCRED mismatch) has fired. The plugin is left in the
	// registry so DispatchCommand can return a clear error, but no new
	// work is sent to it.
	disabled bool
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
	h.mu.Lock()
	defer h.mu.Unlock()
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
	Disabled   bool
}

// LaunchedPlugins returns a snapshot of every launched plugin, sorted by
// name. Safe for concurrent use.
func (h *Host) LaunchedPlugins() []LaunchedPluginInfo {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]LaunchedPluginInfo, 0, len(h.launched))
	for _, lp := range h.launched {
		out = append(out, LaunchedPluginInfo{
			Name:       lp.name,
			Path:       lp.path,
			SocketPath: lp.socketPath,
			PID:        lp.pid,
			Disabled:   lp.disabled,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// lookupPluginByName returns the launched plugin under name, or nil.
// Holds the lock for the duration of the read.
func (h *Host) lookupPluginByName(name string) *launchedPlugin {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.launched[name]
}

// lookupPluginByCommand resolves a plugin-registered command name to the
// owning launched plugin, or nil. The pluginCmds map is populated during
// Init from each plugin's AllowedCommands.
func (h *Host) lookupPluginByCommand(cmdName string) *launchedPlugin {
	h.mu.Lock()
	defer h.mu.Unlock()
	owner, ok := h.pluginCmds[cmdName]
	if !ok {
		return nil
	}
	return h.launched[owner]
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
	// Also drop any command-registry rows owned by this plugin.
	for cmd, owner := range h.pluginCmds {
		if owner == name {
			delete(h.pluginCmds, cmd)
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
	for _, name := range missing {
		logger.Warn("pluginhost: enabled plugin not found on disk",
			slog.String("plugin", name),
		)
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

	lp, err := h.launchPlugin(ctx, c, pluginLogger)
	if err != nil {
		pluginLogger.Warn(
			"plugin "+c.name+": launch failed — skipped ("+err.Error()+")",
			slog.String("error", err.Error()),
		)
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
		PluginName:   c.name,
		Capabilities: &protov1.Capabilities{},
	})
	if err != nil {
		pluginLogger.Warn(
			"plugin "+c.name+": Init RPC failed — skipped ("+err.Error()+")",
			slog.String("error", err.Error()),
		)
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

	h.mu.Lock()
	h.launched[lp.name] = lp
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
	}
	h.mu.Unlock()
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
func (h *Host) Start(ctx context.Context) {
	h.mu.Lock()
	names := make([]string, 0, len(h.launched))
	for n := range h.launched {
		names = append(names, n)
	}
	h.mu.Unlock()
	sort.Strings(names)

	for _, name := range names {
		lp := h.lookupPluginByName(name)
		if lp == nil || lp.disabled {
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
	}
}

// Stop calls PluginService.Stop on each launched plugin in reverse-name
// order, then kills the subprocess. Each plugin gets a per-call
// [stopDrainTimeout] budget; a plugin whose Stop blocks past that is
// abandoned but the subprocess is still killed afterwards.
//
// Stop cancels every outstanding Subscribe stream BEFORE invoking the
// plugin's Stop RPC, so a plugin waiting on an event recv loop does not
// deadlock on shutdown.
func (h *Host) Stop(parent context.Context) {
	h.mu.Lock()
	names := make([]string, 0, len(h.launched))
	for n := range h.launched {
		names = append(names, n)
	}
	h.mu.Unlock()
	sort.Sort(sort.Reverse(sort.StringSlice(names)))

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

		// Kill the subprocess unconditionally. go-plugin emits a SIGTERM
		// and waits for the process to exit on a short internal
		// deadline; SIGKILL fires if the plugin ignores SIGTERM.
		lp.client.Kill()
		removeSocket(lp.socketPath)
		h.removeLaunched(name)
	}

	// Belt-and-suspenders: cancel the legacy daemonCtx (still alive for
	// in-process daemon.go users).
	h.mu.Lock()
	if h.daemonCancel != nil {
		h.daemonCancel()
	}
	h.mu.Unlock()
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
