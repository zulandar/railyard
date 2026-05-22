// Package pluginhost provides the concrete implementation of the
// [plugin.Host] contract declared in github.com/zulandar/railyard/pkg/plugin.
//
// Under the subprocess plugin model the host discovers plugin binaries
// on disk, launches each enabled binary as a subprocess over a
// Unix-domain socket via [github.com/hashicorp/go-plugin], and brokers
// state through the [HostService] gRPC server defined in hostservice.go.
// Plugins are processes, not goroutines.
//
// The bare *Host type still implements [plugin.Host] so internal
// subsystems can construct an in-process Host view for testing. Those
// methods are not exercised by subprocess plugins — they reach the host
// exclusively through gRPC.
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
// an error explaining the binding is not wired.
type Dependencies struct {
	// Cfg is the loaded railyard configuration. It is read for YardInfo
	// fields, plugin config blocks, and the subprocess discovery list.
	Cfg *config.Config

	// DB is the GORM handle used by snapshot assembly. Required for
	// [Host.Snapshot]; pass an in-memory SQLite handle in tests.
	DB *gorm.DB

	// Bus is the typed event bus the host delegates Subscribe calls to.
	// Required for [Host.Subscribe] and the HostService.Subscribe stream.
	Bus events.Bus

	// Build metadata. RailyardVersion is best-effort: when empty, the
	// host resolves it from runtime/debug.ReadBuildInfo.
	RailyardVersion string
	BuildCommit     string
	BuildTime       time.Time

	// PauseYardFn pauses the yard. Bound to the "pause_yard" command.
	PauseYardFn func(ctx context.Context, reason string) error

	// ResumeYardFn resumes a paused yard. Bound to "resume_yard".
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

// Host is the concrete implementation of [plugin.Host] AND the supervisor
// for the launched subprocess plugins.
//
// Construct it with [NewHost], then call [Host.Init] to launch the
// configured plugins, [Host.Start] when core subsystems are ready, and
// [Host.Stop] on shutdown. Methods that mutate launched-plugin state take
// h.mu.
type Host struct {
	deps Dependencies

	// yardInfo is computed once at construction and returned verbatim
	// from YardInfo. The SDK guarantees the value is stable for the
	// lifetime of the binary.
	yardInfo plugin.YardInfo

	// allowed is the static command allow-list (spec §7.3). Populated
	// once in NewHost.
	allowed map[string]commandBinding

	// Mutex guarding the maps below.
	mu sync.Mutex

	// pluginCmds maps a plugin-registered command name to its serving
	// subprocess plugin's name. Populated from Init's advertised
	// capabilities. HostService.DispatchCommand consults this map after
	// the core allow-list misses.
	pluginCmds map[string]string

	// inProcCmds holds in-process command handlers registered via
	// [Host.RegisterCommand]. Subprocess plugins do NOT use this path —
	// they advertise commands through their PluginService.Init response
	// and the host routes through pluginCmds + PluginService.HandleCommand.
	// Retained for in-process Host-interface satisfaction so existing
	// tests and the in-plugin SDK view keep compiling.
	inProcCmds map[string]plugin.CommandHandler

	// subscriptions tracks per-plugin live Subscribe count for the
	// bare-*Host Subscribe shim. *Host must satisfy [plugin.Host] even
	// though subprocess plugins never reach this path (they go through
	// the gRPC HostService.Subscribe stream in subscribe.go).
	subscriptions map[string]int

	// launched is the registry of subprocess plugins the host owns.
	// Keyed by plugin name.
	launched map[string]*launchedPlugin

	// shutdownCh is closed by [Host.Stop] (idempotently, via
	// shutdownOnce) to signal every per-plugin supervisor goroutine
	// that it must NOT relaunch on the next observed subprocess exit.
	// Supervisors poll this channel between iterations and exit
	// cleanly when it's closed.
	shutdownCh   chan struct{}
	shutdownOnce sync.Once

	// supervisorWG joins every supervisor goroutine. [Host.Stop] blocks
	// on it after closing shutdownCh so a relaunch attempt cannot race
	// the socket cleanup.
	supervisorWG sync.WaitGroup

	// started is set true after [Host.Start] runs. The supervisor uses
	// it to decide whether a relaunched plugin should additionally be
	// driven through PluginService.Start after Init — i.e. whether the
	// host was already past the Start barrier when the crash happened.
	//
	// Read/written under h.mu.
	started bool

	// clock returns the current wall-clock time. The default is
	// time.Now; tests override it so the restart-loop and crash-budget
	// machinery is fully deterministic. Read without the lock — the
	// field is set once at NewHost time.
	clock func() time.Time

	// backoffSleep blocks for d or until the host shutdownCh is
	// closed (whichever comes first). Returns true if the sleep
	// completed; false if it was short-circuited by shutdown. Default
	// implementation set in NewHost; tests override with a deterministic
	// version that signals via channels.
	backoffSleep func(d time.Duration, shutdown <-chan struct{}) bool
}

// Compile-time assertion that *Host implements plugin.Host. Catches
// signature drift between this file and the frozen SDK contract.
var _ plugin.Host = (*Host)(nil)

// NewHost constructs a Host from the supplied dependencies. It caches
// [YardInfo] and the static command allow-list; both are stable for the
// returned Host's lifetime. The host does NOT launch any subprocess
// plugins until [Host.Init] is called.
func NewHost(deps Dependencies) *Host {
	h := &Host{
		deps:       deps,
		pluginCmds: make(map[string]string),
		inProcCmds: make(map[string]plugin.CommandHandler),
		launched:   make(map[string]*launchedPlugin),
		shutdownCh: make(chan struct{}),
		clock:      time.Now,
	}
	h.backoffSleep = defaultBackoffSleep
	h.yardInfo = buildYardInfo(deps)
	h.allowed = buildAllowList(&deps)
	// One-shot INFO log when YardID was filled from the legacy Project
	// alias instead of the dedicated cfg.YardID field. Mirrors the
	// fallback decision in buildYardInfo so operators can spot the
	// implicit aliasing in logs and migrate their config. Emitted at
	// INFO (not DEBUG) because daemon entrypoints default to
	// slog.LevelInfo via logutil.ParseLevel — a DEBUG record would be
	// dropped by the handler's Enabled gate and the operator nudge
	// would never reach the log. This fires at most once per host
	// boot so the noise budget is tiny.
	if deps.Cfg != nil && deps.Cfg.YardID == "" && deps.Cfg.Project != "" {
		slog.Default().Info(
			"pluginhost: yard_id not set in config; falling back to project for plugin.YardInfo.YardID",
			"project", deps.Cfg.Project,
		)
	}
	return h
}

// defaultBackoffSleep is the production [Host.backoffSleep] — a real
// timer that is short-circuited by shutdown. Returns true if d elapsed,
// false if shutdown closed first.
func defaultBackoffSleep(d time.Duration, shutdown <-chan struct{}) bool {
	if d <= 0 {
		// Still honor shutdown even for zero waits so the supervisor
		// can react to a Stop racing with a relaunch.
		select {
		case <-shutdown:
			return false
		default:
			return true
		}
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return true
	case <-shutdown:
		return false
	}
}

// buildYardInfo gathers the static metadata once.
//
// YardID resolution prefers the dedicated cfg.YardID field and falls
// back to cfg.Project when YardID is empty. The fallback preserves
// pre-existing behavior for configs that have not yet adopted the
// `yard_id:` field — see NewHost for the one-time DEBUG log emitted on
// the fallback path. The fallback decision is encapsulated here so the
// log site in NewHost can mirror it without diverging.
func buildYardInfo(deps Dependencies) plugin.YardInfo {
	info := plugin.YardInfo{
		RailyardVersion: deps.RailyardVersion,
		BuildCommit:     deps.BuildCommit,
		BuildTime:       deps.BuildTime,
	}
	if deps.Cfg != nil {
		if deps.Cfg.YardID != "" {
			info.YardID = deps.Cfg.YardID
		} else {
			info.YardID = deps.Cfg.Project
		}
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
// provided either by the in-plugin SDK (which attaches `plugin=<name>`
// to every record forwarded over HostService.Log) or by the pluginView
// wrapper for in-process callers.
func (h *Host) Logger() *slog.Logger {
	return slog.Default()
}

// Subscribe delegates to the underlying events bus. Retained for
// [plugin.Host] interface satisfaction; subprocess plugins reach the bus
// through HostService.Subscribe instead.
func (h *Host) Subscribe(topic plugin.EventType, handler plugin.EventHandler) plugin.Unsubscribe {
	return h.subscribeFor("", topic, handler)
}

// subscribeFor is the per-plugin-tracked Subscribe implementation used
// by in-process callers via *Host.Subscribe. Subprocess plugins use the
// gRPC Subscribe path implemented in subscribe.go.
func (h *Host) subscribeFor(pluginName string, topic plugin.EventType, handler plugin.EventHandler) plugin.Unsubscribe {
	h.incrSubscription(pluginName)
	var once sync.Once
	decrement := func() {
		once.Do(func() {
			h.decrSubscription(pluginName)
		})
	}
	if h.deps.Bus == nil {
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
