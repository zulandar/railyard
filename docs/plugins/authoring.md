# Railyard Plugin Authoring Guide

This guide is for engineers building a railyard plugin. It assumes
working knowledge of Go but no prior familiarity with railyard's
internals. By the end you should have everything needed to ship a plugin
that subscribes to yard events, dispatches commands, runs daemons, and
integrates with an external system — without touching railyard's
`internal/*` code.

The `pkg/plugin` package is treated as a stable public API. The
signatures, lifecycle, and behavior documented below are part of
railyard's contract with plugin authors. Behavioral changes require an
update to this guide.

The reference implementation is the trainmaster connector that lives in
a separate `railyard-enterprise` repository — when in doubt about how a
production plugin uses the SDK, look there.

---

## Table of Contents

1. [Overview](#1-overview)
2. [Quickstart: hello-world plugin](#2-quickstart-hello-world-plugin)
3. [Plugin lifecycle](#3-plugin-lifecycle)
4. [Host method reference](#4-host-method-reference)
5. [Configuration conventions](#5-configuration-conventions)
6. [Event subscriptions](#6-event-subscriptions)
7. [Snapshots](#7-snapshots)
8. [Commands](#8-commands)
9. [Daemons](#9-daemons)
10. [Logging](#10-logging)
11. [Testing patterns](#11-testing-patterns)
12. [Module boundary](#12-module-boundary)
13. [Production checklist](#13-production-checklist)

---

## 1. Overview

A railyard plugin is a Go package that registers itself with railyard's
SDK at `init()` time. Plugins do not run as separate processes — they
are linked into a railyard binary at compile time. The OSS `ry` binary
ships with zero plugins. To run plugins you build a custom binary that
side-effect imports the plugin packages you want.

```
github.com/zulandar/railyard               (OSS, this repo)
├── cmd/ry/                                public CLI binary
├── pkg/plugin/                            public SDK — you import this
└── internal/...                           railyard internals — OFF LIMITS

github.com/zulandar/railyard-enterprise    (private, separate repo)
├── cmd/ry-enterprise/main.go              custom main with side-effect imports
└── plugins/trainmaster/                   the reference plugin
```

Two design choices shape everything in this guide:

- **Custom-binary build, not dynamic loading.** Plugins are linked at
  build time. There is no `go-plugin` style subprocess model, no
  hot-reload, no `.so` discovery. Adding a plugin to a deployment means
  rebuilding the binary.

- **Two-repo split with a hard module boundary.** Plugins live outside
  the `github.com/zulandar/railyard` module. Go's package visibility
  rules make every package under `internal/` unreachable from outside
  the module — the boundary is enforced by the compiler, not policy.
  The only sanctioned entry point is the `Host` interface this guide
  describes.

The trainmaster connector is the canonical reference plugin. It
subscribes to all eleven Phase 1 events, sends heartbeats to a remote
service with the result of `Host.Snapshot`, and forwards external
commands back to railyard via `Host.DispatchCommand`. Several sections
below cite trainmaster as a worked example.

---

## 2. Quickstart: hello-world plugin

This section is a complete working plugin. Copy it, change the names,
ship it.

The hello plugin subscribes to `CarCreated` events and logs each one. It
needs no config to run with defaults, but accepts an optional greeting
override to demonstrate `Host.Config`.

### 2.1 Layout

The plugin lives in a sibling repo:

```
github.com/example/railyard-hello/
├── go.mod
├── plugin.go              the plugin itself
└── cmd/ry-hello/
    └── main.go            the enterprise binary entry point
```

### 2.2 `go.mod`

```go
module github.com/example/railyard-hello

go 1.26

require github.com/zulandar/railyard v0.0.0

// Local-dev convenience: point at a checked-out railyard source tree.
// Drop this once you depend on a tagged release.
replace github.com/zulandar/railyard => ../railyard
```

### 2.3 `plugin.go`

```go
// Package hello is a minimal railyard plugin: it subscribes to CarCreated
// events and logs each one, optionally with a configurable greeting.
package hello

import (
    "context"
    "fmt"

    "github.com/zulandar/railyard/pkg/plugin"
)

// Config is the plugin's top-level YAML block.
type Config struct {
    Enabled  bool   `yaml:"enabled"`
    Greeting string `yaml:"greeting"`
}

// Plugin is the hello plugin. It holds the host handle (captured in Init)
// and any state shared between Init/Start/Stop.
type Plugin struct {
    host plugin.Host
    cfg  Config
    unsub plugin.Unsubscribe
}

// Name returns the plugin's stable identifier. Must match the name passed
// to plugin.Register below and the top-level YAML key the plugin reads.
func (p *Plugin) Name() string { return "hello" }

// Init reads the plugin's config block, validates it, and stashes the host
// handle for later use. Returning a non-nil error here causes the host to
// log a WARN and skip the plugin for the rest of the binary's lifetime;
// other plugins and core continue normally.
func (p *Plugin) Init(ctx context.Context, h plugin.Host) error {
    p.host = h
    node := h.Config(p.Name())
    if node.Kind != 0 {
        if err := node.Decode(&p.cfg); err != nil {
            return fmt.Errorf("hello: invalid config: %w", err)
        }
    }
    if p.cfg.Greeting == "" {
        p.cfg.Greeting = "hello"
    }
    return nil
}

// Start subscribes to CarCreated. Subscription handlers run on a dedicated
// per-subscriber goroutine, so this Start returns immediately.
func (p *Plugin) Start(ctx context.Context) error {
    if !p.cfg.Enabled {
        return nil
    }
    p.unsub = p.host.Subscribe(plugin.CarCreated, p.onCarCreated)
    return nil
}

// Stop releases the subscription. Stop must not block core shutdown;
// the host applies a 5-second drain timeout on this call.
func (p *Plugin) Stop(ctx context.Context) error {
    if p.unsub != nil {
        p.unsub()
    }
    return nil
}

// onCarCreated runs on the plugin's dedicated subscriber goroutine. Keep
// handler work fast — long work belongs in a daemon (see §9).
func (p *Plugin) onCarCreated(topic plugin.EventType, payload any) {
    ev, ok := payload.(plugin.CarCreatedEvent)
    if !ok {
        // The SDK guarantees the dynamic type matches the topic, so this
        // path is defensive only.
        return
    }
    p.host.Logger().Info(
        fmt.Sprintf("%s: car %s created on track %s", p.cfg.Greeting, ev.CarID, ev.Track),
    )
}

// init registers the plugin with the SDK at package import time. The
// enterprise binary's main package does a side-effect import of this
// package, which triggers this init().
func init() {
    plugin.Register("hello", func() plugin.Plugin { return &Plugin{} })
}

// Compile-time assertion that *Plugin satisfies plugin.Plugin. Cheap and
// catches signature drift the moment you change a method.
var _ plugin.Plugin = (*Plugin)(nil)
```

Note: `gopkg.in/yaml.v3` is a transitive dependency of `pkg/plugin`
(`Host.Config` returns a `yaml.Node`), so it resolves automatically
through `go mod tidy`. The plugin does not import `yaml` explicitly —
`node.Decode` is a method on the returned value.

### 2.4 `cmd/ry-hello/main.go`

This is where the enterprise binary is assembled. **Read this carefully**
— the limitation it describes is important.

`cmd/ry/main.go` in this repo is `package main`. Go does not let an
external package call into another `package main`. That means today an
enterprise binary cannot do `import railyardmain "github.com/zulandar/railyard/cmd/ry"; railyardmain.Run()`.

Two workarounds exist:

1. **Fork `cmd/ry/main.go` into your enterprise binary.** Copy the file,
   add your plugin side-effect imports above it, and rebuild. This is
   what railyard-enterprise does today. It's a ~20-line file and it
   only changes when railyard adds a top-level subcommand, which is
   rare.
2. **Wait for a `cmd/ry/cli.Run()` entry point.** A future bead will
   extract a public re-entry function. When that lands, an enterprise
   `main.go` will be ~5 lines: side-effect imports plus `ry.Run()`.

Until that future bead lands, the hello-world enterprise binary's
`main.go` looks like a copy of `cmd/ry/main.go` with one new import:

```go
package main

import (
    "fmt"
    "os"

    "github.com/spf13/cobra"

    // Side-effect import: the package's init() calls plugin.Register.
    _ "github.com/example/railyard-hello"
)

// Version info — typically set via ldflags at build time.
var (
    Version = "dev"
    Commit  = "none"
    Date    = "unknown"
)

func main() {
    // ... copy the existing newRootCmd() / execute() from cmd/ry/main.go ...
    // The plugin host wiring (cmd/ry/pluginhost.go) is what actually walks
    // plugin.Registered() at boot, so as long as your binary uses the same
    // boot path the plugin will be picked up.
    os.Exit(execute(newRootCmd()))
}

// Belt-and-suspenders so a future linter complaint about unused imports
// at the package level is impossible.
var _ = cobra.Command{}
var _ = fmt.Sprintf
```

The mechanical answer: copy `cmd/ry/main.go` and `cmd/ry/pluginhost.go`,
add your side-effect imports, and build with `go build -o ry-hello
./cmd/ry-hello/`. That is verbose; a follow-up bead will eliminate the
copying.

### 2.5 `railyard.yaml`

With no config block, the plugin uses defaults (`enabled: false`, so the
handler is wired but the subscribe call is skipped). To actually fire,
add a top-level `hello:` block:

```yaml
# railyard.yaml — same file the OSS binary reads.

owner: alice
repo: git@github.com:org/repo.git

database:
  host: 127.0.0.1
  port: 3306

# ... rest of standard railyard config ...

# Plugin block. Unknown to OSS railyard — silently stashed and
# returned verbatim from Host.Config("hello").
hello:
  enabled: true
  greeting: "yo"
```

If you omit the block entirely, `Host.Config("hello")` returns a
zero-value `yaml.Node` (Kind == 0); the plugin's `Init` handles that
case and applies its own defaults. The OSS binary tolerates the unknown
key — it logs at DEBUG when it sees one.

### 2.6 Working version in this repo

A compiling version of the plugin package above lives at
[`examples/plugins/hello/`](../../examples/plugins/hello/). It is its
own Go module with a `replace` directive pointing at the in-tree
railyard source, so it builds against the current SDK with no external
release. The verification test in
`cmd/ry/example_plugin_build_test.go` runs `go build ./...` from that
directory on every CI run, so this example stays current with the SDK —
if `pkg/plugin` ever changes in a way that breaks the documented
listing above, CI fails until the guide and the example are brought
back in sync.

That example is a plugin package only (no `cmd/ry-hello/main.go`). The
entry-point limitation in §2.4 is real until the public re-entry bead
lands; once it does, the example will grow a tiny `main.go` and the
verification test will build the full binary.

That's the full plugin. The rest of this guide explains what each piece
does and how to extend it.

---

## 3. Plugin lifecycle

A plugin moves through four phases driven by the host:

| Phase | When | What to do |
|---|---|---|
| `init()` | Package import in the enterprise `main` | Call `plugin.Register(name, factory)`. No side effects. |
| `Init` | After config load, before core subsystems boot | Read config via `Host.Config`, validate it, build clients. Returning an error skips the plugin. |
| `Start` | After core subsystems are up | Subscribe to events, register commands, launch daemons via `Host.RunDaemon`. Must return quickly. |
| `Stop` | On shutdown (SIGTERM) | Release resources, cancel inflight work. Host enforces a 5s per-plugin drain timeout. |

### 3.1 Failure isolation

If `Init` returns an error, the host logs a WARN of the form:

```
plugin trainmaster: init failed — skipped (endpoint required when enabled)
```

…and removes the plugin from the running set. **Other plugins and core
continue normally.** This is deliberate: a misconfigured plugin must
never take the whole binary down.

`Start` errors do not unwind already-started plugins. A `Start` failure
logs at WARN but the plugin stays registered (the host does not have a
clean way to undo subscriptions and daemons that may have been created
mid-`Start`). Prefer to validate everything you can in `Init`.

### 3.2 Boot observability

The host emits these structured log lines automatically — plugins don't
need to log "I started":

```
loaded plugins: trainmaster, hello
plugin trainmaster: init
plugin trainmaster: started (3 daemons, 4 subscriptions)
plugin hello: init
plugin hello: started (0 daemons, 1 subscriptions)
```

On shutdown:

```
plugin hello: stopped
plugin trainmaster: stopped
```

Stop runs in reverse registration order. `ry plugins list` shows the
build-time-registered plugin list at any time.

### 3.3 The 5-second drain timeout

`Stop` receives a context that is cancelled after 5 seconds. Past that,
the host abandons the plugin (and any daemons still running) and
proceeds with binary shutdown. Stop must therefore:

- Respect `ctx.Done()` in any wait or join it performs.
- Avoid blocking calls without a timeout.
- Not assume the plugin's daemons are still alive — they were cancelled
  before Stop ran and may already be torn down.

Plugins that need a graceful flush (e.g. a final heartbeat) should
attempt it with a derived `context.WithTimeout` from the supplied
context, not from `context.Background()`.

---

## 4. Host method reference

The `Host` interface is the only path into railyard. All eight methods,
in the order most plugins use them.

### 4.1 `Config(name string) yaml.Node`

Returns the raw `yaml.Node` for the plugin's top-level block, or the
zero value if the block is absent. The plugin decodes into its own
struct.

```go
func (p *Plugin) Init(ctx context.Context, h plugin.Host) error {
    var cfg Config
    if node := h.Config(p.Name()); node.Kind != 0 {
        if err := node.Decode(&cfg); err != nil {
            return fmt.Errorf("invalid config: %w", err)
        }
    }
    // apply defaults, validate, build clients...
    return nil
}
```

`Kind == 0` means "no block present" — apply defaults and continue.
Empty blocks (`hello: {}`) come back with `Kind == yaml.MappingNode`
and Content of length zero; the plugin should treat that the same as
absence.

### 4.2 `YardInfo() YardInfo`

Returns static metadata about this railyard instance — yard ID, owner,
project, repo URL, the railyard core version, and best-effort build
commit/time. The value is stable for the lifetime of the binary;
read it once in `Init` and stash it.

```go
func (p *Plugin) Init(ctx context.Context, h plugin.Host) error {
    info := h.YardInfo()
    p.identity = RegistrationIdentity{
        YardID:          info.YardID,
        RepoURL:         info.RepoURL,
        RailyardVersion: info.RailyardVersion,
    }
    return nil
}
```

This is how the trainmaster connector populates its outbound
`RegisterRequest` — railyard's identity fields all come from here.

### 4.3 `Subscribe(topic EventType, h EventHandler) Unsubscribe`

Registers a handler for a single event topic. The handler runs on a
dedicated per-subscriber goroutine, so handlers do not block publishers
and do not need to be reentrant.

```go
func (p *Plugin) Start(ctx context.Context) error {
    p.unsubMerged = p.host.Subscribe(plugin.CarMerged, p.onCarMerged)
    p.unsubFailed = p.host.Subscribe(plugin.MergeFailed, p.onMergeFailed)
    return nil
}

func (p *Plugin) onCarMerged(topic plugin.EventType, payload any) {
    ev := payload.(plugin.CarMergedEvent)
    p.metrics.merged.Inc()
    select {
    case p.work <- ev:
    default:
        p.host.Logger().Warn("dropped car_merged event — work queue full")
    }
}
```

The returned `Unsubscribe` must be invoked in `Stop`. It is safe to call
multiple times. Backpressure is drop-oldest with a WARN log; see §6 for
details.

### 4.4 `Snapshot(ctx context.Context) (*Snapshot, error)`

Returns the current full operational state in a single read transaction.
Intended for heartbeat-style plugins that re-send full state on a cadence.

```go
func (p *Plugin) sendHeartbeat(ctx context.Context) error {
    snap, err := p.host.Snapshot(ctx)
    if err != nil {
        return fmt.Errorf("snapshot: %w", err)
    }
    return p.client.Heartbeat(ctx, &HeartbeatRequest{
        YardID:     p.identity.YardID,
        Cars:       snap.Cars.Active, // forward verbatim — upsert semantics
        Engines:    snap.Engines,
        Tracks:     snap.Tracks,
        Yardmaster: snap.Yardmaster,
        Stats:      snap.Stats,
    })
}
```

Snapshot cost grows with yard size — a snapshot reads every engine,
every track, every active car, and every car's status column. Call it
on a cadence (5–30 seconds is typical), not on every event. See §7 for
the upsert semantics that make this safe.

### 4.5 `RegisterCommand(name string, h CommandHandler) error`

Exposes a plugin-owned command name that other plugins (or external
systems reaching the plugin's own server) can invoke through the host.
Returns an error if the name conflicts with the core allow-list or a
previously registered plugin command.

```go
func (p *Plugin) Start(ctx context.Context) error {
    if err := p.host.RegisterCommand("trainmaster.ack", p.onAck); err != nil {
        return fmt.Errorf("register trainmaster.ack: %w", err)
    }
    return nil
}

func (p *Plugin) onAck(ctx context.Context, args plugin.CommandArgs) (plugin.CommandResult, error) {
    id, ok := args["RequestID"].(string)
    if !ok {
        return plugin.CommandResult{Success: false, Error: "RequestID required"}, nil
    }
    p.ackBacklog(id)
    return plugin.CommandResult{Success: true}, nil
}
```

Pick names that are obviously yours. Convention: namespace with your
plugin name (`trainmaster.ack`, `hello.greet`). The core allow-list
uses bare names (`pause_yard`, `resume_yard`) and reserves them.

### 4.6 `DispatchCommand(ctx context.Context, name string, args CommandArgs) (CommandResult, error)`

Invokes a command by name. Plugin-registered commands are NOT dispatched
through this method today — DispatchCommand only looks up the core
allow-list. The list (full schemas in §8):

| Command | Required args |
|---|---|
| `pause_yard` | (none) |
| `resume_yard` | (none) |
| `reassign_car` | `CarID` (string), `FromEngine` (string) |
| `scale_track` | `Track` (string), `Count` (int) |
| `force_complete` | `CarID` (string), `Reason` (string) |

```go
res, err := p.host.DispatchCommand(ctx, "scale_track", plugin.CommandArgs{
    "Track": "backend",
    "Count": 5,
})
if err != nil {
    return err
}
if !res.Success {
    return fmt.Errorf("scale_track failed: %s", res.Error)
}
```

Validation errors return `Success: false` with `Error` set to the
specific violation — DispatchCommand itself returns a non-nil Go error
only for transport-level problems, which don't currently exist (every
allow-list call is in-process). Treat the `(error, !result.Success)`
distinction as future-proofing.

### 4.7 `RunDaemon(name string, fn DaemonFunc)`

Registers a long-lived goroutine the host supervises (panic recovery,
context cancellation on shutdown, 5s drain timeout, 3-strike restart
budget per spec §8).

```go
func (p *Plugin) Start(ctx context.Context) error {
    p.host.RunDaemon("heartbeat", p.heartbeatLoop)
    return nil
}

func (p *Plugin) heartbeatLoop(ctx context.Context) error {
    t := time.NewTicker(p.cfg.HeartbeatInterval)
    defer t.Stop()
    for {
        select {
        case <-ctx.Done():
            return nil
        case <-t.C:
            if err := p.sendHeartbeat(ctx); err != nil {
                p.host.Logger().Warn("heartbeat failed", "err", err)
            }
        }
    }
}
```

RunDaemon returns immediately; the function runs in its own goroutine.
Call it from `Start`, not `Init` — `Start` is when the host has all the
plumbing wired and is ready for background work. See §9 for daemon
patterns.

### 4.8 `Logger() *slog.Logger`

Returns a structured logger scoped with `plugin=<name>`. Use this for
every log statement the plugin emits — never `slog.Default()` directly,
or your records will not be tagged with the plugin name.

```go
p.host.Logger().Info("processed event", "car_id", ev.CarID, "track", ev.Track)
// produces: level=INFO plugin=hello msg="processed event" car_id=car-1 track=main
```

The host also tags daemon supervision logs (`plugin=<name> daemon=<name>`)
so panic restarts are correlatable to the daemon and plugin that caused
them.

---

## 5. Configuration conventions

Plugins own a top-level YAML key in `railyard.yaml`. Unknown top-level
keys are stashed in the loaded config (not rejected) and returned
verbatim from `Host.Config(name)`. This means the same `railyard.yaml`
works for OSS and enterprise binaries — OSS silently ignores plugin
blocks, enterprise consumes them.

### 5.1 Naming

The plugin name passed to `plugin.Register`, the name returned from
`Plugin.Name()`, and the top-level YAML key MUST match. The host uses
them interchangeably for log scoping, registry lookup, and config
retrieval.

### 5.2 The `enabled: false` default

Make every plugin opt-in. The standard pattern:

```go
type Config struct {
    Enabled bool   `yaml:"enabled"`
    // ... other fields ...
}

func (p *Plugin) Init(ctx context.Context, h plugin.Host) error {
    var cfg Config // zero value: Enabled=false
    if node := h.Config(p.Name()); node.Kind != 0 {
        if err := node.Decode(&cfg); err != nil {
            return fmt.Errorf("invalid config: %w", err)
        }
    }
    if !cfg.Enabled {
        // No subscriptions, no daemons, no client connections. The plugin
        // is registered but inert.
        p.cfg = cfg
        return nil
    }
    // Validate the rest of the config now that we know we're going to use it.
    if cfg.Endpoint == "" {
        return errors.New("endpoint required when enabled")
    }
    // Build clients...
    p.cfg = cfg
    return nil
}
```

Rationale: a plugin compiled into the binary should not start producing
side effects (outbound network calls, log spam) unless an operator
explicitly turns it on. This is also why init failures only skip the
offending plugin — a typo in one plugin's config shouldn't take down
the binary.

### 5.3 Validate in `Init`

Return errors from `Init` for any invalid config — empty required
fields, bad URLs, missing credentials. The host's failure isolation
turns those errors into a single WARN log and skips the plugin; trying
to validate at first use produces hard-to-diagnose runtime errors deep
in a daemon stack trace instead.

---

## 6. Event subscriptions

`Host.Subscribe(topic, handler)` registers a handler against one of the
eleven Phase 1 event topics. The host wraps the underlying typed event
bus.

### 6.1 Topic to payload mapping

Every topic has a single payload struct. Handlers receive `any`; type-
assert to the specific struct. The SDK guarantees the dynamic type
matches the topic.

| `EventType`        | Payload struct          | Fields                                          |
|--------------------|-------------------------|-------------------------------------------------|
| `CarCreated`       | `CarCreatedEvent`       | CarID, Track, Type, Priority, RequestedBy        |
| `CarClaimed`       | `CarClaimedEvent`       | CarID, EngineID                                  |
| `CarStatusChanged` | `CarStatusChangedEvent` | CarID, OldStatus, NewStatus                      |
| `CarMerged`        | `CarMergedEvent`        | CarID, Branch                                    |
| `MergeFailed`      | `MergeFailedEvent`      | CarID, Reason                                    |
| `EngineStarted`    | `EngineStartedEvent`    | EngineID, Track                                  |
| `EngineStopped`    | `EngineStoppedEvent`    | EngineID                                         |
| `EngineStalled`    | `EngineStalledEvent`    | EngineID, LastActivityUnix                       |
| `YardmasterAction` | `YardmasterActionEvent` | TargetID, ActionType                             |
| `YardPaused`       | `YardPausedEvent`       | Reason                                           |
| `YardResumed`      | `YardResumedEvent`      | Reason                                           |

### 6.2 Delivery semantics

- Each `Subscribe` call allocates a buffered channel (256 slots by
  default) and a dedicated goroutine that drains it.
- Publishers fan out non-blocking: if a subscriber's channel is full,
  the publisher does not wait. Backpressure is **drop-oldest** with a
  WARN log naming the subscriber and topic.
- The subscriber goroutine wraps each handler invocation in
  panic recovery. Three consecutive panics permanently disable the
  subscription with an ERROR log.

The drop-oldest policy matters for plugin design: **do not assume every
event reaches your handler under sustained load.** For consumers like
trainmaster that need ground truth on a missed event, the periodic
snapshot heartbeat (§7) carries it.

### 6.3 Keep handlers fast

The handler runs on the subscriber's dedicated drain goroutine. If it
blocks, the channel fills and the host starts dropping events. Two
patterns work:

**Pattern A: counter / cheap state mutation.** Fine inline.

```go
func (p *Plugin) onCarMerged(topic plugin.EventType, payload any) {
    ev := payload.(plugin.CarMergedEvent)
    p.dailyMergedCount.Add(1)
    p.lastMergeAt.Store(time.Now().UnixNano())
}
```

**Pattern B: queue to a daemon.** Required for any work that touches
the network, runs SQL, or could block.

```go
func (p *Plugin) Start(ctx context.Context) error {
    p.work = make(chan plugin.CarMergedEvent, 1024)
    p.host.RunDaemon("merge-forwarder", p.forwardMerges)
    p.host.Subscribe(plugin.CarMerged, p.onCarMerged)
    return nil
}

func (p *Plugin) onCarMerged(topic plugin.EventType, payload any) {
    ev := payload.(plugin.CarMergedEvent)
    select {
    case p.work <- ev:
    default:
        p.host.Logger().Warn("dropped CarMerged — forwarder queue full")
    }
}

func (p *Plugin) forwardMerges(ctx context.Context) error {
    for {
        select {
        case <-ctx.Done():
            return nil
        case ev := <-p.work:
            if err := p.client.NotifyMerge(ctx, ev); err != nil {
                p.host.Logger().Warn("notify merge failed", "err", err)
            }
        }
    }
}
```

### 6.4 Unsubscribing

The `Unsubscribe` closure returned from `Subscribe` is the only path to
detach a handler. Call it in `Stop`. It is safe to call multiple times;
subsequent calls are no-ops. It is also safe to call from within the
handler itself (e.g. one-shot handlers).

---

## 7. Snapshots

`Host.Snapshot(ctx)` is the heartbeat-style read path. One read
transaction returns the full operational state.

```go
type Snapshot struct {
    Timestamp  time.Time
    Tracks     []TrackSnap
    Engines    []EngineSnap
    Cars       CarsSnap
    Yardmaster YardmasterSnap
    Stats      SnapStats
}
```

### 7.1 `CarsSnap.Active` is the full active set

This is the most important semantic in the snapshot model:

- `Cars.Active` contains **every car whose status is currently
  non-terminal** — `open`, `ready`, `claimed`, `in_progress`, or
  `blocked`. Terminal-status cars (`done`, `merged`, `cancelled`) are
  not included.
- A car that appeared in snapshot *N* but is missing from snapshot *N+1*
  has transitioned to a terminal state. The transition was already
  published as a `CarMerged`, `MergeFailed`, or `CarStatusChanged`
  event.
- **`Active` is intended for upsert-style consumers.** A plugin
  forwarding state to an external system can replay `Active` verbatim
  each heartbeat. Missing entries on the receiving side correspond to
  cars that became terminal — the event stream covers the transition.

This is the contract the trainmaster connector relies on: it forwards
`snap.Cars.Active` as its `HeartbeatRequest.cars` field, the remote
system upserts on each heartbeat, and any car that disappears from a
snapshot is one whose terminal-transition event was already delivered.

### 7.2 `Counts` includes terminal statuses

`CarsSnap.Counts` is a tally keyed by status string. **It covers every
status present in the yard, terminal and non-terminal.** Plugins can
render dashboards or emit per-status metrics without re-querying.

```go
total := 0
for status, n := range snap.Cars.Counts {
    fmt.Printf("  %-12s %d\n", status, n)
    total += n
}
fmt.Printf("  TOTAL        %d\n", total)
```

### 7.3 Time-bucketed aggregations are the plugin's job

The host does not maintain "cars completed today", "merges this hour",
"avg car duration over the last day", or any other windowed metric.
`SnapStats` exposes only counters that core already maintains cheaply
(engine status counts). Adding windowed metrics to `SnapStats` is
explicitly out of scope.

If a plugin needs them, subscribe to the relevant events and bucket
yourself. A canonical pattern using a daemon for the bucket reset:

```go
type DailyMergeCounter struct {
    mu sync.Mutex
    n  int
}

func (c *DailyMergeCounter) Inc() {
    c.mu.Lock()
    c.n++
    c.mu.Unlock()
}

func (c *DailyMergeCounter) ResetAndRead() int {
    c.mu.Lock()
    defer c.mu.Unlock()
    n := c.n
    c.n = 0
    return n
}

// In Start:
counter := &DailyMergeCounter{}
host.Subscribe(plugin.CarMerged, func(_ plugin.EventType, _ any) {
    counter.Inc()
})
host.RunDaemon("daily-reset", func(ctx context.Context) error {
    // crude midnight tick — production should use a proper scheduled
    // ticker library, but this illustrates the shape.
    t := time.NewTicker(24 * time.Hour)
    defer t.Stop()
    for {
        select {
        case <-ctx.Done():
            return nil
        case <-t.C:
            n := counter.ResetAndRead()
            host.Logger().Info("daily merges", "count", n)
        }
    }
})
```

### 7.4 Snapshot cost

A snapshot scans the engine table, the track table, every active car
row, and the status column of every car (active or terminal). Cost
grows linearly with yard size. Heartbeat cadences in the 5–30 second
range are healthy; sub-second snapshot loops are not the intended use.

If you need higher-frequency reads of a subset of state, subscribe to
events and maintain your own materialized view.

---

## 8. Commands

The host offers two command surfaces.

### 8.1 Dispatched: core allow-list

`Host.DispatchCommand(ctx, name, args)` invokes one of five allow-listed
core commands. The allow-list is defined in railyard core; adding to it
requires a railyard code review.

#### `pause_yard`

Pauses the yard. Idempotent.

```go
res, _ := h.DispatchCommand(ctx, "pause_yard", nil)
// res.Success == true, res.Data == nil
```

Args: none required.

#### `resume_yard`

Resumes a paused yard. Idempotent.

```go
res, _ := h.DispatchCommand(ctx, "resume_yard", nil)
```

Args: none required.

#### `reassign_car`

Force-reassigns a car off the given engine. Used when an engine has
stalled and the operator wants the car picked up by a different engine.

```go
res, _ := h.DispatchCommand(ctx, "reassign_car", plugin.CommandArgs{
    "CarID":      "car-abc",
    "FromEngine": "eng-stall-1",
})
```

Required args:
- `CarID` (string) — the car to reassign.
- `FromEngine` (string) — the engine currently holding the car.

#### `scale_track`

Sets engine count for a track. Delegates to the same path as
`ry engine scale`.

```go
res, _ := h.DispatchCommand(ctx, "scale_track", plugin.CommandArgs{
    "Track": "backend",
    "Count": 5,
})
```

Required args:
- `Track` (string) — track name.
- `Count` (int) — desired engine count. Accepts Go `int`, `int64`, or
  JSON-decoded `float64`.

Caveat: in local tmux deployments, `scale_track` manages tmux panes. In
Kubernetes deployments, the underlying scale path does not currently
manage pod replicas — that's a railyard-core limitation, not a plugin
issue.

#### `force_complete`

Force-marks a car as `done` with an operator reason. Useful for
operator-driven cleanup of stuck cars.

```go
res, _ := h.DispatchCommand(ctx, "force_complete", plugin.CommandArgs{
    "CarID":  "car-xyz",
    "Reason": "verified out-of-band; cleanup",
})
```

Required args:
- `CarID` (string).
- `Reason` (string) — surfaced in audit logs.

### 8.2 Validation behavior

Required keys are checked for presence and type before the underlying
function runs. Failures return:

```go
plugin.CommandResult{
    Success: false,
    Error:   `missing required argument "CarID"`,
}
```

…or:

```go
plugin.CommandResult{
    Success: false,
    Error:   `argument "Count" has wrong type`,
}
```

DispatchCommand's Go error return is reserved for transport / internal
failures that don't happen today; for now, branch on `result.Success`.

### 8.3 Registered: plugin-provided commands

`Host.RegisterCommand(name, handler)` exposes a plugin-owned command.
Use this when the plugin wants to receive commands from outside (e.g.
trainmaster's Phase 2 `CommandStream` will fan inbound commands
through this surface).

```go
type CommandArgs map[string]any

type CommandResult struct {
    Success bool
    Error   string
    Data    map[string]any
}

type CommandHandler func(ctx context.Context, args CommandArgs) (CommandResult, error)
```

```go
err := h.RegisterCommand("trainmaster.set_priority", func(ctx context.Context, args plugin.CommandArgs) (plugin.CommandResult, error) {
    carID, ok := args["CarID"].(string)
    if !ok {
        return plugin.CommandResult{Success: false, Error: `CarID (string) required`}, nil
    }
    priority, ok := args["Priority"].(int)
    if !ok {
        // JSON-decoded payloads arrive as float64; tolerate both.
        if f, fok := args["Priority"].(float64); fok {
            priority = int(f)
            ok = true
        }
    }
    if !ok {
        return plugin.CommandResult{Success: false, Error: `Priority (int) required`}, nil
    }
    if err := p.setPriority(ctx, carID, priority); err != nil {
        return plugin.CommandResult{Success: false, Error: err.Error()}, nil
    }
    return plugin.CommandResult{Success: true}, nil
})
if err != nil {
    return fmt.Errorf("register set_priority: %w", err)
}
```

Returning a Go error from a `CommandHandler` is equivalent to returning
`Success: false` with `Error: err.Error()` — the host translates the
two forms. Pick whichever reads better at the call site.

Registered names must not conflict with the core allow-list or with
another plugin's registered commands. `RegisterCommand` returns an
error if they do — fail `Start` (or skip the conflicting registration)
when that happens.

Note: today `Host.DispatchCommand` only looks up the core allow-list,
not plugin-registered commands. Plugin-registered handlers are reached
by the plugin's own server / IPC path (e.g. trainmaster's `CommandStream`).
A unified dispatch path is a possible follow-up.

---

## 9. Daemons

`Host.RunDaemon(name, fn)` registers a managed goroutine. The host wraps
every daemon with:

- **Panic recovery.** Each panic is logged with a stack trace and the
  daemon is restarted. After **three lifetime panics** the daemon is
  permanently disabled with an ERROR log.
- **Context cancellation on shutdown.** The supplied `ctx` is cancelled
  when the host begins `Stop`. Daemons must observe `ctx.Done()` and
  return promptly.
- **5-second drain window.** A daemon that ignores cancellation past
  the budget is abandoned and the binary exits.
- **Per-daemon structured logger.** All host-emitted supervision logs
  carry `plugin=<name> daemon=<name>` tags.

### 9.1 The canonical ticker daemon

The shape every heartbeat-style daemon follows:

```go
func (p *Plugin) heartbeatLoop(ctx context.Context) error {
    t := time.NewTicker(p.cfg.HeartbeatInterval)
    defer t.Stop()
    for {
        select {
        case <-ctx.Done():
            return nil
        case <-t.C:
            if err := p.sendHeartbeat(ctx); err != nil {
                p.host.Logger().Warn("heartbeat failed", "err", err)
                // Keep looping — transient failures should not bring
                // the daemon down. Return only for unrecoverable errors.
            }
        }
    }
}
```

Note: **the ticker, not `time.Sleep`, is the cadence source.** A
`time.Sleep(interval)` in the main loop is not cancellable from `ctx`
and will block Stop until the sleep finishes. Always sleep via a
ticker or `select` on `<-time.After(d)` with `<-ctx.Done()`.

### 9.2 Don't pin shared state inside the daemon's stack

Daemon state that needs to survive a panic restart belongs on the
plugin struct, not in the daemon function's local variables. After a
panic, the host re-invokes `fn(ctx)` — local variables are reset.

```go
// Bad: counter resets on every panic restart.
host.RunDaemon("metrics", func(ctx context.Context) error {
    n := 0
    for {
        select {
        case <-ctx.Done():
            return nil
        case <-time.After(time.Second):
            n++ // lost on restart
        }
    }
})

// Good: counter survives.
host.RunDaemon("metrics", func(ctx context.Context) error {
    for {
        select {
        case <-ctx.Done():
            return nil
        case <-time.After(time.Second):
            p.metrics.n++ // on the plugin struct
        }
    }
})
```

### 9.3 Errors and the restart budget

A daemon that returns a non-nil error is logged at WARN. The supervisor
does NOT restart on a clean error return — only panics consume the
restart budget. Treat a normal `return err` as "I'm done; do not bring
me back," and a panic as "I crashed; please retry me."

If a daemon needs to retry on transient errors, do it inside the
function:

```go
func (p *Plugin) workLoop(ctx context.Context) error {
    backoff := time.Second
    for {
        if err := p.doWork(ctx); err != nil {
            if errors.Is(err, context.Canceled) {
                return nil
            }
            p.host.Logger().Warn("work failed", "err", err, "backoff", backoff)
            select {
            case <-ctx.Done():
                return nil
            case <-time.After(backoff):
            }
            if backoff < 30*time.Second {
                backoff *= 2
            }
            continue
        }
        backoff = time.Second
    }
}
```

### 9.4 Multiple daemons per plugin

Plugins routinely register multiple daemons — a heartbeat sender, an
event-forwarding worker, a reconnect supervisor. Register them all in
`Start`; the host tracks them per-plugin and cancels them all
concurrently inside the 5-second drain window when `Stop` runs.

---

## 10. Logging

Use `Host.Logger()` for every log statement the plugin emits. The
returned `*slog.Logger` is scoped with `plugin=<name>`; the attribute
appears on every record without the plugin caller needing to add it.

```go
log := p.host.Logger()
log.Info("processed batch", "batch_id", id, "rows", n)
log.Warn("retry", "attempt", i, "err", err)
log.Error("unrecoverable", "err", err)
```

The host emits the following lifecycle log lines automatically — do not
duplicate them in your plugin:

```
loaded plugins: hello                                    (boot summary)
plugin hello: init                                       (Init begin)
plugin hello: init failed — skipped (...)                (Init error)
plugin hello: started (0 daemons, 1 subscriptions)       (Start success)
plugin hello: start failed                               (Start error)
plugin hello: stopped                                    (Stop success)
plugin hello: stop returned error                        (Stop error)
plugin hello: stop drain timeout exceeded — abandoned    (Stop timeout)
```

Daemon supervisor logs (panic recovery, restart budget exhausted) are
also automatic and carry `plugin=<name> daemon=<name>` tags.

What plugins SHOULD log: domain events meaningful to operators
(reconnects, batch sizes, validation rejections), warnings about
transient failures the plugin recovered from, and errors that the
plugin returned through some external boundary (a webhook 5xx, a
client-side timeout).

What plugins should NOT log: lifecycle ("starting up"), per-handler-
invocation trace ("got event"), or anything the slog level filtering
would let you regret in production.

---

## 11. Testing patterns

The plugin SDK is small enough that plugins are testable against a
hand-rolled fake `Host`. There is no shipped `plugintest` package
today; one may appear later if a pattern stabilises.

### 11.1 A minimal fake Host

The fake captures Subscribe / RunDaemon / DispatchCommand calls so a
test can drive them directly. It does not implement event-bus
backpressure or daemon supervision — those belong to integration tests
against the real `pluginhost.Host`.

```go
package hello_test

import (
    "context"
    "log/slog"
    "sync"
    "testing"

    "github.com/example/railyard-hello"
    "github.com/zulandar/railyard/pkg/plugin"
    "gopkg.in/yaml.v3"
)

type fakeHost struct {
    cfg          yaml.Node
    info         plugin.YardInfo
    logger       *slog.Logger

    mu       sync.Mutex
    subs     map[plugin.EventType]plugin.EventHandler
    daemons  map[string]plugin.DaemonFunc
    cmds     map[string]plugin.CommandHandler
    dispatch func(ctx context.Context, name string, args plugin.CommandArgs) (plugin.CommandResult, error)
}

func newFakeHost() *fakeHost {
    return &fakeHost{
        logger:  slog.Default(),
        subs:    make(map[plugin.EventType]plugin.EventHandler),
        daemons: make(map[string]plugin.DaemonFunc),
        cmds:    make(map[string]plugin.CommandHandler),
    }
}

func (h *fakeHost) Config(name string) yaml.Node          { return h.cfg }
func (h *fakeHost) YardInfo() plugin.YardInfo             { return h.info }
func (h *fakeHost) Logger() *slog.Logger                   { return h.logger }
func (h *fakeHost) Snapshot(ctx context.Context) (*plugin.Snapshot, error) {
    return &plugin.Snapshot{}, nil
}

func (h *fakeHost) Subscribe(topic plugin.EventType, fn plugin.EventHandler) plugin.Unsubscribe {
    h.mu.Lock()
    h.subs[topic] = fn
    h.mu.Unlock()
    return func() {
        h.mu.Lock()
        delete(h.subs, topic)
        h.mu.Unlock()
    }
}

func (h *fakeHost) RunDaemon(name string, fn plugin.DaemonFunc) {
    h.mu.Lock()
    h.daemons[name] = fn
    h.mu.Unlock()
}

func (h *fakeHost) RegisterCommand(name string, fn plugin.CommandHandler) error {
    h.mu.Lock()
    h.cmds[name] = fn
    h.mu.Unlock()
    return nil
}

func (h *fakeHost) DispatchCommand(ctx context.Context, name string, args plugin.CommandArgs) (plugin.CommandResult, error) {
    if h.dispatch == nil {
        return plugin.CommandResult{Success: true}, nil
    }
    return h.dispatch(ctx, name, args)
}

// publish is a test helper that invokes a registered subscriber inline.
// Production plugins receive events on a dedicated goroutine, but tests
// usually want deterministic ordering.
func (h *fakeHost) publish(topic plugin.EventType, payload any) {
    h.mu.Lock()
    fn := h.subs[topic]
    h.mu.Unlock()
    if fn != nil {
        fn(topic, payload)
    }
}

var _ plugin.Host = (*fakeHost)(nil)
```

### 11.2 A test that drives the hello plugin

```go
func TestHelloLogsCarCreated(t *testing.T) {
    fh := newFakeHost()

    // Provide a config block: enabled=true, custom greeting.
    var node yaml.Node
    if err := yaml.Unmarshal([]byte("enabled: true\ngreeting: yo"), &node); err != nil {
        t.Fatalf("unmarshal: %v", err)
    }
    // yaml.Unmarshal wraps the doc; pick the document's content node.
    fh.cfg = *node.Content[0]

    p := &hello.Plugin{}
    if err := p.Init(context.Background(), fh); err != nil {
        t.Fatalf("Init: %v", err)
    }
    if err := p.Start(context.Background()); err != nil {
        t.Fatalf("Start: %v", err)
    }

    fh.publish(plugin.CarCreated, plugin.CarCreatedEvent{
        CarID: "car-1",
        Track: "main",
    })

    // Assertions on the captured slog output go here — typically by
    // installing a slog handler that writes to a bytes.Buffer (see
    // internal/pluginhost/host_integration_test.go for the pattern).

    if err := p.Stop(context.Background()); err != nil {
        t.Fatalf("Stop: %v", err)
    }
}
```

For full-stack tests — verifying that a plugin actually interacts with
the real pluginhost, event bus, and database — look at
`internal/pluginhost/host_integration_test.go` and
`internal/pluginhost/e2e_event_delivery_test.go` in the railyard repo.
Those are railyard's own tests but the patterns transfer directly:
construct a real `pluginhost.NewHost` with the dependencies it needs,
register your plugin, drive Init / Start / Stop, and publish events on
the real bus.

---

## 12. Module boundary

**Plugins must never import any package under `github.com/zulandar/railyard/internal/...`.**
This is not a guideline. It is a hard rule, enforced at three layers:

1. **Go visibility.** Plugin code lives in a different Go module
   (`railyard-enterprise`, `railyard-hello`, etc.). Go's `internal/`
   directory convention makes every package under `internal/`
   unreachable from outside the parent module. The compiler will
   refuse to build a plugin that tries.

2. **SDK self-test.** The railyard repo has a self-test at
   `pkg/plugin/import_test.go` that asserts the SDK's transitive
   import graph contains no `github.com/zulandar/railyard/internal/`
   packages. This catches drift inside the SDK itself — a railyard
   contributor who accidentally adds an `internal/` import to
   `pkg/plugin` will fail CI immediately.

3. **This guide.** If you find yourself wanting something that's only
   available in `internal/`, file an issue against railyard asking for
   it to be promoted into `pkg/plugin`, not a workaround.

The SDK's external dependency surface is intentionally tiny:

- The Go standard library.
- `gopkg.in/yaml.v3` (for `Host.Config`'s `yaml.Node` return type).

Every other dependency a plugin brings in is the plugin's own. The SDK
self-test catches accidental drift in that surface too — any new
external import in `pkg/plugin` requires a deliberate update to the
allow-list and a corresponding mention in this guide.

---

## 13. Production checklist

Before shipping a plugin, verify:

- [ ] **`enabled: false` is the default.** Both in the struct (zero
  value) and in `railyard.example.yaml` / your plugin's documentation.
  No outbound side effects until an operator opts in.

- [ ] **Init validates everything you can.** Bad config should be a
  WARN-and-skip at boot, not a panic mid-daemon. Required fields,
  reachable endpoints, parseable credentials — verify in `Init` and
  return a descriptive error.

- [ ] **Event handlers are non-blocking.** Inline only does counters
  and other cheap work. Anything touching the network, the database,
  or `time.Sleep` belongs in a daemon that drains a buffered channel.

- [ ] **Daemons honor `ctx.Done()`.** No `time.Sleep` in the main loop.
  Ticker plus `select` with `<-ctx.Done()`. Verified by triggering Stop
  in a test and asserting prompt return.

- [ ] **Stop actually returns.** Tests cover both the happy path and
  the "context cancelled mid-flush" path. No background goroutines left
  running after Stop returns.

- [ ] **Tests against a fake Host cover the lifecycle.** Init / Start /
  Stop. Subscriptions detach. Daemons exit on cancellation.

- [ ] **No `internal/*` imports.** `go build` enforces this, but
  double-check with `go list -deps ./... | grep zulandar/railyard/internal`
  — it should produce zero lines.

- [ ] **Plugin uses `Host.Logger()`, not `slog.Default()`.** Otherwise
  your records lose the `plugin=<name>` tag.

- [ ] **Compile-time interface assertion.** `var _ plugin.Plugin =
  (*Plugin)(nil)` catches signature drift the moment you change a
  method.

- [ ] **YardInfo read once.** Cached on the plugin struct in `Init`.
  Not re-read on every event.

- [ ] **Snapshot cadence is reasonable.** 5–30 seconds for heartbeat-
  style consumers. Not on every event.

- [ ] **External dependencies declared in your `go.mod`.** Your
  plugin's transitive dependencies are not the SDK's problem — keep
  them lean.

- [ ] **Lifecycle log lines verified in a smoke test.** Run the binary
  with your plugin enabled and confirm `plugin <name>: started (N
  daemons, M subscriptions)` matches what you intended to register.

- [ ] **Drop-oldest tolerance documented.** If your consumer cannot
  tolerate dropped events, document the mitigation (typically the
  heartbeat snapshot carries ground truth).
