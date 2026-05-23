# Authoring a Railyard Plugin

This guide walks a Go developer through writing a railyard plugin for
the current SDK. It assumes working Go knowledge but **does not assume**
prior familiarity with HashiCorp go-plugin or protobuf — the SDK hides
both. The companion document `docs/plugins/proto.md` covers the gRPC
wire contract for anyone who needs it.

---

## Overview

A railyard plugin is a standalone Go binary that the railyard host
launches as a subprocess. The host and the plugin talk over gRPC on a
Unix domain socket; the SDK wires both sides.

```
   +---------------------+                      +----------------------+
   |   railyard host     |  gRPC over UDS       |   plugin binary      |
   |  (ry / ry-...)      | <------------------> |   (./my-plugin)      |
   |  PluginService      |                      |  HostService client  |
   |  client             |                      |  (Subscribe,         |
   |                     |                      |   Snapshot, ...)     |
   |  HostService        |                      |  PluginService       |
   |  server             |                      |  server (your impl)  |
   +---------------------+                      +----------------------+
```

The subprocess model exists for three reasons:

- **Isolation.** A plugin panic exits the plugin process, not the
  railyard binary. The host has a crash budget (3 panics in 60 seconds)
  before it permanently disables the plugin — see `railyard-fll.6`.
- **No host rebuild.** Adding or upgrading a plugin is a file drop into
  a `plugins.d/` directory plus a restart. No enterprise binary, no
  side-effect import, no relink.
- **Polyglot future.** The wire contract is plain gRPC. Nothing in the
  host assumes the plugin is written in Go; that affordance is reserved
  for a future SDK in another language.

The author-facing surface is small: implement four methods on a Go
type, hand it to `plugin.Serve`, build, drop the binary into a plugins
directory, and enable it in `railyard.yaml`. The rest is detail.

---

## Prerequisites

- **Go 1.26 or newer.** The SDK's `go.mod` declares `go 1.26`.
- **A plugins directory** the host can scan. The host looks at:
  - `/etc/railyard/plugins.d/` (system)
  - `~/.railyard/plugins/` (user)
  - `./plugins/` (next to `railyard.yaml`, for dev)
- The `ry` binary. The plugin process never runs on its own — the host
  launches it.

---

## Hello plugin walkthrough

A complete working plugin. The pre-built reference lives in
[`examples/plugins/hello/`](../../examples/plugins/hello/); CI keeps it
in sync with the SDK.

### 1. Initialize the module

```
mkdir my-plugin && cd my-plugin
go mod init github.com/me/my-plugin
```

### 2. Add the SDK dependency

For a released railyard:

```
go get github.com/zulandar/railyard/pkg/plugin@latest
```

For in-tree development, mirror `examples/plugins/hello/go.mod`:

```go
module github.com/me/my-plugin

go 1.26

require github.com/zulandar/railyard v0.0.0

// Drop this once you depend on a tagged release.
replace github.com/zulandar/railyard => ../railyard
```

Then `go mod tidy`.

### 3. Write `main.go`

```go
package main

import (
    "context"
    "fmt"

    "github.com/zulandar/railyard/pkg/plugin"
)

type Config struct {
    Greeting string `yaml:"greeting"`
}

type MyPlugin struct {
    host  plugin.Host
    cfg   Config
    unsub plugin.Unsubscribe
}

func (p *MyPlugin) Name() string { return "my-plugin" }

func (p *MyPlugin) Init(ctx context.Context, h plugin.Host) error {
    p.host = h
    node := h.Config(p.Name())
    if node.Kind != 0 {
        if err := node.Decode(&p.cfg); err != nil {
            return fmt.Errorf("my-plugin: invalid config: %w", err)
        }
    }
    if p.cfg.Greeting == "" {
        p.cfg.Greeting = "hello"
    }
    return nil
}

func (p *MyPlugin) Start(ctx context.Context) error {
    p.unsub = p.host.Subscribe(plugin.CarCreated, p.onCarCreated)
    return nil
}

func (p *MyPlugin) Stop(ctx context.Context) error {
    if p.unsub != nil {
        p.unsub()
    }
    return nil
}

func (p *MyPlugin) onCarCreated(topic plugin.EventType, payload any) {
    ev, ok := payload.(plugin.CarCreatedEvent)
    if !ok {
        return // defensive — SDK guarantees the dynamic type
    }
    p.host.Logger().Info(
        fmt.Sprintf("%s: car %s on track %s", p.cfg.Greeting, ev.CarID, ev.Track),
    )
}

func main() {
    plugin.Serve(&MyPlugin{})
}

var _ plugin.Plugin = (*MyPlugin)(nil)
```

### 4. Build

```
go build -o my-plugin .
chmod +x my-plugin
```

The binary must be executable; the host launches it as a subprocess.

### 5. Install

Drop `my-plugin` into one of:

- `/etc/railyard/plugins.d/my-plugin`
- `~/.railyard/plugins/my-plugin`
- `./plugins/my-plugin`

### 6. Enable in `railyard.yaml`

```yaml
plugins:
  enabled: [my-plugin]
  my-plugin:
    greeting: "yo"
    allow:
      events: [CarCreated]
```

The `enabled` list is the opt-in. The per-plugin block carries your
config plus an `allow` block listing the events and commands the
operator authorizes — see [Capabilities and
allow-listing](#capabilities-and-allow-listing).

### 7. Restart railyard

`ry plugins list` should now show `my-plugin`. Log lines from the
plugin carry `plugin=my-plugin`:

```
level=INFO plugin=my-plugin msg="yo: car car-1 on track main"
```

---

## The Plugin interface

```go
type Plugin interface {
    Name() string
    Init(ctx context.Context, h Host) error
    Start(ctx context.Context) error
    Stop(ctx context.Context) error
}
```

**`Name`** — stable identifier. Must match the binary filename, the
key under `plugins:` in `railyard.yaml`, and the log scope.

**`Init`** — called once after config load, before core subsystems
boot. Decode config via `Host.Config(p.Name())`, validate every
required field, build any external clients, stash the `Host` argument
on your struct. Returning an error causes the host to log a WARN and
skip the plugin for the rest of the binary's lifetime; other plugins
and core continue normally. Do **not** subscribe to events, register
commands, or launch goroutines here.

**`Start`** — called once after core subsystems are running.
Subscribe, register commands, and launch goroutines here. Return
quickly; long work belongs in a goroutine.

**`Stop`** — called once on shutdown. The supplied context is
cancelled after a 5-second drain timeout — past that, the host
abandons the plugin and proceeds with binary shutdown. Cancel inflight
work, drop unsubscribe closures, close clients. Honor `ctx.Done()`.
Graceful flushes should use `context.WithTimeout(ctx, ...)` derived
from this context, not `context.Background()`.

---

## The Host interface

`plugin.Host` is the only path from your plugin into railyard. Every
method is an RPC against the host process; the SDK's in-plugin adapter
forwards each call over gRPC.

```go
type Host interface {
    Config(name string) yaml.Node
    YardInfo() YardInfo
    Subscribe(topic EventType, h EventHandler) Unsubscribe
    Snapshot(ctx context.Context) (*Snapshot, error)
    RegisterCommand(name string, h CommandHandler) error
    DispatchCommand(ctx context.Context, name string, args CommandArgs) (CommandResult, error)
    RunDaemon(name string, fn DaemonFunc) // deprecated
    Logger() *slog.Logger
}
```

### Config

Returns the raw `yaml.Node` for the plugin's top-level block, or the
zero value (`Kind == 0`) if no block is present. Decode into your own
struct:

```go
var cfg struct {
    Endpoint string `yaml:"endpoint"`
    Retries  int    `yaml:"retries"`
}
node := h.Config(p.Name())
if node.Kind != 0 {
    if err := node.Decode(&cfg); err != nil {
        return fmt.Errorf("invalid config: %w", err)
    }
}
```

`gopkg.in/yaml.v3` is a transitive dependency of `pkg/plugin`, so
`node.Decode` works without an explicit import.

### YardInfo

Static metadata about the railyard instance — yard ID, owner, project,
repo URL, the railyard core version, best-effort build metadata.
Stable for the lifetime of the host; read once in `Init` and cache.

### Subscribe

Registers a handler for one event topic. The handler runs on a
dedicated per-subscriber goroutine inside the plugin process; the host
streams events over the gRPC back-channel.

```go
p.unsub = h.Subscribe(plugin.CarCreated, func(topic plugin.EventType, payload any) {
    ev := payload.(plugin.CarCreatedEvent)
    h.Logger().Info("car created", "id", ev.CarID, "track", ev.Track)
})
```

The topic → payload mapping lives in `pkg/plugin/event.go`. Each
`EventType` constant pairs with exactly one payload struct
(`CarCreated` ↔ `CarCreatedEvent`, etc.). The SDK guarantees the
dynamic type matches the topic.

The returned `Unsubscribe` closure should be called from `Stop`. It is
safe to call multiple times.

**Backpressure** is drop-oldest with a WARN log naming the topic and
subscriber. Plugins that need ground truth under sustained load should
also call `Snapshot` periodically.

### Snapshot

Returns the current full operational state in a single read
transaction. Intended for heartbeat-style plugins.

```go
snap, err := h.Snapshot(ctx)
if err != nil {
    return fmt.Errorf("snapshot: %w", err)
}
for _, car := range snap.Cars.Active {
    // forward verbatim — terminal cars drop out of subsequent snapshots
}
```

Tree shape is in `pkg/plugin/snapshot.go`. `Cars.Active` is the full
set of non-terminal cars; a car missing from a subsequent snapshot has
transitioned to a terminal state (its transition was already
published as `CarMerged`, `MergeFailed`, or `CarStatusChanged`).
`Cars.Counts` is a status tally including terminal states.

Snapshot cost scales with yard size. 5–30 second cadence is healthy;
sub-second loops are not the intended use.

### RegisterCommand

Exposes a plugin-owned command name. The handler signature is:

```go
type CommandHandler func(ctx context.Context, args CommandArgs) (CommandResult, error)

func (p *MyPlugin) Start(ctx context.Context) error {
    return p.host.RegisterCommand("my-plugin.greet", p.onGreet)
}

func (p *MyPlugin) onGreet(ctx context.Context, args plugin.CommandArgs) (plugin.CommandResult, error) {
    name, ok := args["name"].(string)
    if !ok {
        return plugin.CommandResult{Success: false, Error: "name (string) required"}, nil
    }
    return plugin.CommandResult{Success: true, Data: map[string]any{
        "greeting": fmt.Sprintf("hello, %s", name),
    }}, nil
}
```

Returning a Go error from a `CommandHandler` is equivalent to setting
`Success: false` with `Error: err.Error()` — the host normalizes the
two forms.

Convention: namespace command names with your plugin name
(`my-plugin.greet`, `trainmaster.ack`). Bare names like `pause_yard`
are reserved for the core allow-list. Even though `RegisterCommand`
succeeds locally, the host only dispatches commands the operator has
explicitly allowed — see [Capabilities and
allow-listing](#capabilities-and-allow-listing).

### DispatchCommand

Invokes a command by name. The host first checks the core allow-list; if
the name is not allow-listed, the call falls through to any matching
handler registered via `RegisterCommand`. The allow-list remains
authoritative for the built-in names — `RegisterCommand` rejects
collisions, so the fall-through can never shadow a core binding. Names
absent from both maps return `Success: false` with
`Error: "command not allowed: <name>"`.

```go
res, err := h.DispatchCommand(ctx, "scale_track", plugin.CommandArgs{
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

`CommandResult` is `{Success bool, Error string, Data map[string]any}`.
Validation failures land as `Success: false` with `Error` set; the Go
`error` return is reserved for transport failures. The full set of
dispatchable core commands is in the operator guide
(`railyard-fll.9.3`).

### Logger

Returns a `*slog.Logger` scoped with `plugin=<name>`. Use this for
every log statement — never `slog.Default()`, or your records lose the
plugin tag.

```go
p.host.Logger().Info("processed batch", "rows", n)
// level=INFO plugin=my-plugin msg="processed batch" rows=42
```

### RunDaemon (deprecated)

The legacy in-process host wrapped goroutines with panic recovery and
supervised restart via `RunDaemon`. Under the subprocess model the
plugin already owns its process, so this is deprecated. The SDK still
satisfies the interface for source compatibility; new plugins should
just spawn goroutines:

```go
func (p *MyPlugin) Start(ctx context.Context) error {
    go p.heartbeatLoop(ctx)
    return nil
}
```

`plugin.Serve` translates SIGINT/SIGTERM into context cancellation, so
goroutines that observe `ctx.Done()` return on their own when the host
requests shutdown. See `railyard-fll.8` for the deprecation sweep.

---

## Configuration

The per-plugin block lives under `plugins.<name>`:

```yaml
plugins:
  enabled: [my-plugin]
  my-plugin:
    endpoint: https://api.example.com
    retries: 3
    allow:
      events: [CarCreated, CarMerged]
      commands: [scale_track]
```

In the plugin:

```go
type Config struct {
    Endpoint string `yaml:"endpoint"`
    Retries  int    `yaml:"retries"`
}

func (p *MyPlugin) Init(ctx context.Context, h plugin.Host) error {
    node := h.Config(p.Name())
    if node.Kind != 0 {
        if err := node.Decode(&p.cfg); err != nil {
            return fmt.Errorf("my-plugin: invalid config: %w", err)
        }
    }
    if p.cfg.Endpoint == "" {
        return errors.New("my-plugin: endpoint required")
    }
    return nil
}
```

The `allow` block under your plugin's name is reserved for the
operator; the SDK does not decode it. Your config keys live alongside
it and are delivered verbatim by `Host.Config`.

**Validate in Init.** Required fields, parseable URLs, reachable
credentials — verify at boot and return a descriptive error. The host
turns init errors into a single WARN log and skips the plugin; trying
to validate at first use produces hard-to-diagnose runtime errors deep
in a goroutine stack.

---

## Capabilities and allow-listing

The plugin advertises what it wants; the host enforces what is
actually granted.

### What the plugin advertises (automatic)

The plugin's first RPC carries a `Capabilities` message listing:

- `subscribe_events` — every `EventType` the plugin has called
  `Subscribe` for.
- `provide_commands` — every command name the plugin has called
  `RegisterCommand` for.

**The SDK assembles this transparently.** Plugin authors do not touch
the proto types; the SDK observes your Subscribe and RegisterCommand
calls and reports them to the host on the next Init exchange.

### What the operator allows (explicit)

The operator opts the plugin into specific events and commands via
the per-plugin `allow` block:

```yaml
plugins:
  enabled: [my-plugin]
  my-plugin:
    allow:
      events: [CarCreated, CarMerged]
      commands: [scale_track]
```

From the plugin's perspective:

- `Subscribe(topic)` for a non-allowed topic still returns an
  `Unsubscribe`, but the host never delivers events for that topic.
  The host logs a denial at WARN at Init time.
- `DispatchCommand(name)` for a non-allowed name returns
  `PermissionDenied`.
- `RegisterCommand(name)` succeeds locally; the host marks the command
  unreachable from external dispatchers unless the operator opts in.

A plugin with no `allow` block runs but cannot do anything — every
Subscribe and DispatchCommand call is denied. This is deliberate: the
operator must consciously grant each capability.

Full allow-list syntax lives in the operator guide
(`railyard-fll.9.3`). See also `railyard-fll.4` for the operator
config schema design.

---

## Errors and lifecycle

**Init returning non-nil** — the host logs a WARN of the form `plugin
my-plugin: init failed — skipped (<err>)` and removes the plugin from
the running set. Other plugins and core continue.

**Start returning non-nil** — logs a WARN; the plugin stays
registered. The host has no clean way to undo partial state. Validate
everything you can in `Init`.

**Panic** — a panic inside any plugin method (or inside an event
handler, since the SDK invokes those from a subscriber goroutine)
exits the plugin subprocess with status 1. The host counts the exit
toward its **crash budget** (3 panics inside a rolling 60-second
window). Once exceeded, the host stops restarting the plugin and logs
an ERROR. See `railyard-fll.6` for the host-side design.

**Shutdown drain** — the `Stop` context is cancelled after 5 seconds.
Past that, the host abandons the plugin and exits. Honor `ctx.Done()`
on any wait Stop performs.

---

## Testing your plugin

The SDK ships `pkg/plugin/plugintest`, a maintained `plugin.Host` fake
plus recording affordances, so you don't have to hand-roll a host stub
in every plugin project. Construct a `plugintest.FakeHost` (zero value
is usable), drive `Init`/`Start`, fire a synthetic event via
`DriveEvent`, then assert on the captured subscriptions, command
registrations, dispatches, and log records.

```go
import (
    "context"
    "testing"

    "github.com/zulandar/railyard/pkg/plugin"
    "github.com/zulandar/railyard/pkg/plugin/plugintest"
)

func TestPluginLogsCarCreated(t *testing.T) {
    fh := &plugintest.FakeHost{}
    p := &MyPlugin{}

    _ = p.Init(context.Background(), fh)
    _ = p.Start(context.Background())

    fh.DriveEvent(plugin.CarCreated, plugin.CarCreatedEvent{CarID: "c-1"})

    if len(fh.Logs()) != 1 {
        t.Fatalf("expected 1 log record, got %d", len(fh.Logs()))
    }
}
```

`FakeHost` covers every `plugin.Host` method (`Config`, `YardInfo`,
`Snapshot`, `Subscribe`, `RegisterCommand`, `DispatchCommand`,
`Logger`) and adds a test-only `DriveEvent` affordance. See
`examples/plugins/hello/plugin_test.go` for a working example and the
godoc on `pkg/plugin/plugintest` for the full set of recording
accessors.

---

## Deploying

The binary needs to be executable (`chmod +x`) and live in one of the
host's discovery directories (`/etc/railyard/plugins.d/`,
`~/.railyard/plugins/`, or `./plugins/`).

The host launches each discovered binary as a subprocess. Before any
gRPC messages are exchanged, the host and plugin perform a magic-
cookie handshake:

- `RAILYARD_PLUGIN_MAGIC_COOKIE` — env var set by the host, checked by
  the plugin.
- `ProtocolVersion` — currently `1`; bumped on wire-incompatible gRPC
  envelope changes.

**Plugin authors do not touch either value.** `plugin.Serve` wires the
handshake automatically; the constants live in
`pkg/plugin/handshake.go` for the host-side implementation.

`ry plugins list` shows every plugin the host has discovered. The
operator guide (`docs/plugins/operating.md`, `railyard-fll.9.3`)
documents discovery, runtime controls, and observability in detail.

---

## Runtime introspection

`ry plugins list` shows the build-time view: every plugin binary the host
*would* attempt to launch on startup, intersected with `railyard.yaml`'s
`plugins.enabled`. It does not reflect what is currently running.

`ry plugins status` queries a running yardmaster over HTTP and reports
the live state of each configured plugin.

### What it reports

For every plugin in `plugins.enabled` or known to the running host, one
of four states:

- **running** — launched, alive, accepting commands.
- **disabled** — supervisor permanently disabled the plugin (crash budget
  exhausted, peer-cred mismatch). The `error` column shows the last exit
  reason.
- **failed** — the launch handshake succeeded but `PluginService.Init`
  returned an error. The `error` column shows the init error.
- **skipped** — listed in `plugins.enabled` but no matching binary was
  found in any of the plugins.d directories the host searches.

Per-plugin fields:

| Field | Meaning |
|-------|---------|
| `restart_count` | Cumulative supervisor relaunches since the host booted. |
| `subscription_count` | Live event subscriptions the plugin owns. |
| `command_count` | Plugin-registered commands the host routes to it. |
| `last_activity` | Last lifecycle or dispatch event timestamp. NOT bumped by event delivery — see below. |
| `pid` | Subprocess PID (0 when not running). |
| `path` | Discovered binary path. |
| `error` | Last exit reason (disabled), init error (failed), or "not found in: …" (skipped). |

`last_activity` advances on: successful Init, successful Start, supervisor
relaunch, every `DispatchCommand` hit, every `Subscribe`. It does **not**
advance per delivered event — that would impose a host mutex write on
every bus message.

### Running it

**Local-dev:**

```bash
ry plugins status
```
Reads `cfg.yardmaster.health_port` from `railyard.yaml` and queries
`http://127.0.0.1:<port>/plugins/status`.

**k8s — port-forward:**

```bash
kubectl -n railyard port-forward svc/yardmaster 8081:8081
ry plugins status --url=http://localhost:8081/plugins/status
```

**k8s — exec:**

The chart image ships the `ry` binary, so you can run the command inside
the yardmaster pod:

```bash
kubectl -n railyard exec -it deploy/yardmaster -- ry plugins status
```

### Scripting

Pass `--json` for the raw response in the wire format documented in the
plugin status spec:

```bash
ry plugins status --json | jq '.plugins[] | select(.status != "running")'
```

---

## Migrating from the legacy in-process model

If you have an existing plugin written against the old `init()` +
`plugin.Register` model, the migration is mechanical.

**1. Move to a `main` package.** The plugin used to live in a
non-main package an enterprise binary side-effect-imported. It now
lives in `package main` (or has a `cmd/<name>/main.go` that
side-effect-imports the plugin package and calls `Serve`).

**2. Replace `init` registration with `Serve`.**

Before:

```go
package myplugin

func init() {
    plugin.Register("my-plugin", func() plugin.Plugin { return &Impl{} })
}
```

After:

```go
package main

func main() {
    plugin.Serve(&Impl{})
}
```

`plugin.Register` and `plugin.Registered` remain in the SDK for source
compatibility but are deprecated — `railyard-fll.8` tracks the
deprecation sweep.

**3. Drop the enterprise binary.** The OSS `ry` binary discovers and
launches plugins from `plugins.d/` directly. No `cmd/ry-enterprise`,
no side-effect imports, no version ldflags to maintain.

**4. Drop `Host.RunDaemon` calls.** Replace `host.RunDaemon("name",
fn)` with `go fn(ctx)`. The SDK still satisfies `RunDaemon` for source
compat, but new code should not use it.

**5. Everything else stays the same.** The `Plugin` interface, the
`Host` methods you actually call (`Config`, `Subscribe`, `Snapshot`,
`RegisterCommand`, `DispatchCommand`, `Logger`), and the event topics
and payload structs in `pkg/plugin/event.go` are unchanged. If the
only thing you touch is the package declaration and `init` → `main`,
the rest of the plugin compiles unchanged.

---

## Where to next

- **Operator guide** — `docs/plugins/operating.md`
  (`railyard-fll.9.3`). Allow-list syntax, runtime controls,
  observability.
- **Proto contract** — `docs/plugins/proto.md`. The wire-level gRPC
  schema the SDK wraps. Authors should not need it day-to-day; it's
  the source of truth if the SDK ever surprises you.
- **Active design** — the `railyard-fll` bd epic. Crash-budget
  (`railyard-fll.6`), operator config (`railyard-fll.4`), deprecation
  sweep (`railyard-fll.8`).
- **Working example** — `examples/plugins/hello/` in this repo. A
  minimal plugin CI builds on every commit so the documented
  walkthrough stays in sync with the SDK.
