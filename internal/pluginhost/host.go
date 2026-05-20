// Package pluginhost provides the concrete implementation of the
// [plugin.Host] contract declared in github.com/zulandar/railyard/pkg/plugin.
//
// The host bridges the public plugin SDK to railyard's internal subsystems
// while keeping the import graph small: only stdlib, the SDK, the typed
// event bus (internal/events), the loaded config (internal/config), the
// GORM models (internal/models), and gorm itself are imported here. All
// other internal subsystems (e.g. car update functions, scale paths) are
// reached via small typed function fields supplied through [Dependencies].
//
// This package implements beads railyard-3q8.3.1, .3.2, .3.3, and .3.4 —
// the Host struct, lifecycle/registry, snapshot, command, and daemon
// management surfaces. The daemon manager (panic recovery, per-plugin
// context cancellation, 5s drain bound, 3-strike lifetime restart budget,
// per-daemon structured logging) lives in daemon.go.
package pluginhost

import (
	"context"
	"log/slog"
	"runtime/debug"
	"sync"
	"time"

	"gopkg.in/yaml.v3"
	"gorm.io/gorm"

	"github.com/zulandar/railyard/internal/config"
	"github.com/zulandar/railyard/internal/events"
	"github.com/zulandar/railyard/pkg/plugin"
)

// Dependencies bundles the inputs required to construct a [*Host]. Wiring
// happens in cmd/ry boot; this struct keeps the host package free of
// imports into other internal subsystems. Each Fn field is a function
// pointer the host invokes when the corresponding command is dispatched.
//
// All Fn fields are optional. If a binding's Fn is nil, the corresponding
// command is allow-listed but returns Success=false at dispatch time with
// an error explaining the binding is not wired. This keeps the OSS binary
// runnable without plugins forcing every internal subsystem to be hooked
// up.
type Dependencies struct {
	// Cfg is the loaded railyard configuration. It is read for YardInfo
	// fields, plugin config blocks, and any other static metadata.
	Cfg *config.Config

	// DB is the GORM handle used by snapshot assembly. Required for
	// [Host.Snapshot]; pass an in-memory SQLite handle in tests.
	DB *gorm.DB

	// Bus is the typed event bus the host delegates Subscribe calls to.
	// Required for [Host.Subscribe].
	Bus events.Bus

	// Build metadata. RailyardVersion is best-effort: when empty, the
	// host resolves it from runtime/debug.ReadBuildInfo.
	RailyardVersion string
	BuildCommit     string
	BuildTime       time.Time

	// PauseYardFn pauses the yard. Bound to the "pause_yard" command.
	PauseYardFn func(ctx context.Context, reason string) error

	// ResumeYardFn resumes a paused yard. Bound to the "resume_yard"
	// command.
	ResumeYardFn func(ctx context.Context, reason string) error

	// ReassignCarFn force-reassigns a car off the given engine. Bound to
	// the "reassign_car" command.
	ReassignCarFn func(ctx context.Context, carID, fromEngine string) error

	// ScaleTrackFn sets the engine count for a track. Bound to the
	// "scale_track" command.
	ScaleTrackFn func(ctx context.Context, track string, count int) error

	// ForceCompleteFn force-marks a car done with an operator reason.
	// Bound to the "force_complete" command.
	ForceCompleteFn func(ctx context.Context, carID, reason string) error
}

// Host is the concrete implementation of [plugin.Host]. Construct it with
// [NewHost], then call [Host.Register] for each plugin and walk the
// lifecycle ([Host.Init], [Host.Start], [Host.Stop]) from cmd/ry boot.
//
// A *Host is safe for concurrent use after construction. Methods that
// mutate internal state take the lifecycle mutex.
type Host struct {
	deps Dependencies

	// yardInfo is computed once at construction and returned verbatim
	// from YardInfo. The SDK guarantees the value is stable for the
	// lifetime of the binary.
	yardInfo plugin.YardInfo

	// allowed is the static command allow-list (spec §7.3). Populated
	// once in NewHost.
	allowed map[string]commandBinding

	// Plugin lifecycle state. mu guards plugins, pluginCmds, daemons,
	// daemonCtx, and daemonCancel.
	mu         sync.Mutex
	plugins    []plugin.Plugin
	pluginCmds map[string]plugin.CommandHandler // plugin-registered commands

	// daemons maps plugin name to its currently-supervised daemons. An
	// entry is appended on RunDaemon and removed on cancelDaemons (called
	// during plugin Stop). See daemon.go.
	daemons map[string][]*daemonState

	// subscriptions is the per-plugin live subscription count. Incremented
	// when a plugin (via [pluginView.Subscribe]) registers a handler;
	// decremented when the returned Unsubscribe is invoked. Used by the
	// per-plugin "started" boot log line to report `(N daemons, M
	// subscriptions)` per spec §4. Negative counts are clamped to zero —
	// double-unsubscribe is documented as safe and must not skew the gauge.
	subscriptions map[string]int

	// daemonCtx is the root context every daemon's per-plugin context
	// derives from. Lazily initialised on the first RunDaemon call so
	// hosts that never register daemons stay cheap. daemonCancel is its
	// CancelFunc; calling it fans out cancellation to every daemon.
	daemonCtx    context.Context
	daemonCancel context.CancelFunc
}

// pluginView is a per-plugin wrapper that satisfies [plugin.Host]. It
// delegates every method to the underlying *Host except [Logger], which
// scopes its returned logger to the wrapped plugin's name. Wrapping
// (rather than copying *Host) avoids the sync.Mutex copy that the
// straightforward "clone Host" approach trips over.
type pluginView struct {
	*Host
	name string
}

// Logger overrides Host.Logger so each plugin sees its own scoped
// logger. All other Host methods are inherited via embedding.
func (v *pluginView) Logger() *slog.Logger {
	return slog.Default().With(slog.String("plugin", v.name))
}

// RunDaemon overrides Host.RunDaemon so the host knows which plugin
// registered the daemon. The bare *Host.RunDaemon still exists (to keep
// the plugin.Host compile-time assertion satisfied) but is unscoped and
// records the daemon under an empty plugin name; in practice plugins
// only ever see a *pluginView, so this override is what gets called.
func (v *pluginView) RunDaemon(name string, fn plugin.DaemonFunc) {
	v.Host.runDaemonFor(v.name, name, fn)
}

// Subscribe overrides Host.Subscribe so the host can track which plugin
// owns each subscription. Mirrors the [pluginView.RunDaemon] override:
// the bare *Host.Subscribe stays in place (so the compile-time
// plugin.Host assertion holds) but only tracks at the empty plugin name;
// real plugins always reach Subscribe through this view.
//
// The returned Unsubscribe wraps the underlying bus unsubscribe so the
// per-plugin counter is decremented exactly once even if the caller
// invokes Unsubscribe multiple times (the SDK documents that as safe).
func (v *pluginView) Subscribe(topic plugin.EventType, handler plugin.EventHandler) plugin.Unsubscribe {
	return v.Host.subscribeFor(v.name, topic, handler)
}

// Compile-time assertion that *Host implements plugin.Host. Catches
// signature drift between this file and the frozen SDK contract.
var _ plugin.Host = (*Host)(nil)

// NewHost constructs a Host from the supplied dependencies. It caches
// [YardInfo] and the static command allow-list; both are stable for the
// returned Host's lifetime.
func NewHost(deps Dependencies) *Host {
	h := &Host{
		deps:       deps,
		pluginCmds: make(map[string]plugin.CommandHandler),
	}
	h.yardInfo = buildYardInfo(deps)
	h.allowed = buildAllowList(&deps)
	return h
}

// buildYardInfo gathers the static metadata once. Best-effort: missing
// build info fields stay empty / zero.
func buildYardInfo(deps Dependencies) plugin.YardInfo {
	info := plugin.YardInfo{
		RailyardVersion: deps.RailyardVersion,
		BuildCommit:     deps.BuildCommit,
		BuildTime:       deps.BuildTime,
	}
	if deps.Cfg != nil {
		info.YardID = deps.Cfg.Project // YardID currently aliases project; cfg has no dedicated yard_id field yet
		info.Owner = deps.Cfg.Owner
		info.Project = deps.Cfg.Project
		info.RepoURL = deps.Cfg.Repo
	}
	if info.RailyardVersion == "" {
		if bi, ok := debug.ReadBuildInfo(); ok && bi.Main.Version != "" {
			info.RailyardVersion = bi.Main.Version
		}
	}
	return info
}

// YardInfo returns the cached static metadata for this railyard instance.
func (h *Host) YardInfo() plugin.YardInfo {
	return h.yardInfo
}

// Config returns the raw yaml.Node for the named plugin's top-level
// config block, or a zero-value node if no block was set.
func (h *Host) Config(name string) yaml.Node {
	if h.deps.Cfg == nil || h.deps.Cfg.PluginConfigs == nil {
		return yaml.Node{}
	}
	return h.deps.Cfg.PluginConfigs[name]
}

// Logger returns the default structured logger. Per-plugin scoping is
// provided by the pluginView wrapper that hostFor returns; calling
// Logger directly on a *Host (rather than through a view) returns the
// unscoped logger.
func (h *Host) Logger() *slog.Logger {
	return slog.Default()
}

// hostFor returns a per-plugin [plugin.Host] view. The returned value
// shares the underlying *Host's state — registry, command maps, deps —
// but reports its own name through Logger so each plugin sees a scoped
// log stream.
func (h *Host) hostFor(name string) plugin.Host {
	return &pluginView{Host: h, name: name}
}

// RunDaemon registers a managed daemon under an empty plugin name. In
// production plugins always call through a [pluginView] (see hostFor),
// whose RunDaemon override forwards to runDaemonFor with the correct
// plugin name. The bare-*Host signature exists so *Host continues to
// satisfy plugin.Host (compile-time assertion below).
func (h *Host) RunDaemon(name string, fn plugin.DaemonFunc) {
	h.runDaemonFor("", name, fn)
}

// Subscribe delegates to the underlying events bus. The wrapper converts
// the bus's untyped payload into the typed (EventType, any) signature the
// SDK exposes. In practice plugins always call Subscribe through a
// [pluginView], whose override forwards to subscribeFor with the
// registered plugin's name; this bare-*Host signature exists to satisfy
// the plugin.Host compile-time assertion and tracks under the empty
// plugin name (matching the bare-*Host RunDaemon behaviour).
func (h *Host) Subscribe(topic plugin.EventType, handler plugin.EventHandler) plugin.Unsubscribe {
	return h.subscribeFor("", topic, handler)
}

// subscribeFor is the per-plugin-tracked Subscribe implementation. It
// wraps the underlying events.Bus subscription with a counter increment
// on attach and a once-only decrement on unsubscribe. The counter is the
// source of truth for the "M subscriptions" field in the per-plugin
// "started" log line.
//
// A nil bus is tolerated (matches the pre-existing Host.Subscribe
// contract) — counter bookkeeping still runs so tests without a wired
// bus exercise the tracking path.
func (h *Host) subscribeFor(pluginName string, topic plugin.EventType, handler plugin.EventHandler) plugin.Unsubscribe {
	h.incrSubscription(pluginName)

	// once guards the decrement so double-unsubscribe (documented safe by
	// the SDK) does not double-count.
	var once sync.Once
	decrement := func() {
		once.Do(func() {
			h.decrSubscription(pluginName)
		})
	}

	if h.deps.Bus == nil {
		// No bus wired — return a no-op unsubscribe that still releases
		// the per-plugin counter so the tracking layer stays balanced
		// across plugin lifecycles in test contexts that omit a bus.
		return func() {
			decrement()
		}
	}
	wrapper := func(payload any) {
		handler(topic, payload)
	}
	busUnsub := h.deps.Bus.Subscribe(string(topic), wrapper)
	return func() {
		busUnsub()
		decrement()
	}
}

// incrSubscription bumps the per-plugin live subscription gauge.
func (h *Host) incrSubscription(pluginName string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.subscriptions == nil {
		h.subscriptions = make(map[string]int)
	}
	h.subscriptions[pluginName]++
}

// decrSubscription decrements the per-plugin live subscription gauge.
// Clamps at zero so a misbehaving caller can't drive the counter negative.
func (h *Host) decrSubscription(pluginName string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.subscriptions == nil {
		return
	}
	if h.subscriptions[pluginName] > 0 {
		h.subscriptions[pluginName]--
	}
}

// countsFor returns the live (daemons, subscriptions) gauge pair for the
// named plugin. Used by [Host.Start] to populate the "started" log line.
// Safe for concurrent use.
func (h *Host) countsFor(pluginName string) (daemons, subscriptions int) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.daemons[pluginName]), h.subscriptions[pluginName]
}
