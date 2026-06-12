# Railyard plugin gRPC contract (v1)

This document describes the wire contract between a railyard host and an
out-of-process plugin. The contract is the source of truth — the Go SDK
in `pkg/plugin` wraps it, but anything that crosses the process boundary
must serialize to the proto schema here.

> Writing a plugin in a language other than Go? See
> [`non-go.md`](non-go.md) for the full launch/handshake/broker-dial-back
> walkthrough plus a runnable Python example.

## Layout

```
buf.yaml                                    # repo-root workspace + lint config
buf.gen.yaml                                # codegen config (Go + Go gRPC)
scripts/proto.sh                            # regenerate stubs (and `--check` in CI)
pkg/plugin/proto/v1/plugin.proto            # the contract
pkg/plugin/proto/v1/plugin.pb.go            # generated; Go package `protov1`
pkg/plugin/proto/v1/plugin_grpc.pb.go       # generated
pkg/plugin/proto/v1/compat_test.go          # golden compat check (runs `buf breaking`)
pkg/plugin/proto/snapshots/v1/plugin.proto  # last-known-good snapshot for `buf breaking`
```

`pkg/plugin/proto/v1` is the active module. The sibling
`pkg/plugin/proto/snapshots/v1` holds the last-known-good snapshot
against which `buf breaking` compares. The snapshot lives outside the
active module so buf does not try to compile two copies of every type.

> **Caveat — the snapshot gate can be defeated in a single commit.**
> `buf breaking --against pkg/plugin/proto/snapshots/v1` only protects you
> if the snapshot is updated in a *separate, reviewed* step from the proto
> change. If a single commit edits `plugin.proto` *and* refreshes the
> snapshot in lockstep, buf compares the new proto against the new snapshot
> and validates nothing. Additivity was verified by hand for the Phase 3
> changes; the robust gate is `buf breaking --against '.git#branch=main'`,
> which compares the working proto against the last-merged proto and cannot
> be bypassed this way. Switching CI to it requires checking out full
> history (`fetch-depth: 0`) and validating buf's git-ref branch
> resolution; tracked as a follow-up.

## Services

### `PluginService` (implemented by the plugin)

| RPC             | Purpose                                                           |
| --------------- | ----------------------------------------------------------------- |
| `Init`          | Capability handshake. Plugin advertises desired events/commands; host returns the allow-listed subset and structured denials. |
| `Start`         | Host has finished booting; plugin may launch workers via callback. |
| `Stop`          | Shutdown signal. Plugin must return within the host's drain timeout. |
| `HandleCommand` | Invoked when an external dispatcher calls a command the plugin registered. |

### `HostService` (implemented by the host, called by the plugin)

| RPC               | Purpose                                                                  |
| ----------------- | ------------------------------------------------------------------------ |
| `YardInfo`        | Static yard identity. Fixed for the lifetime of the host.                |
| `Snapshot`        | Full current yard state in a single read transaction.                    |
| `Subscribe`       | Server-streamed multiplexed event channel filtered to the plugin's allow-list. |
| `DispatchCommand` | Invoke a core or plugin-registered command by name.                      |
| `Config`          | Raw YAML bytes of the plugin's config block from `railyard.yaml`. SDK rehydrates back to `yaml.Node`. |
| `Log`             | Plugin emits a structured log record; host re-emits with a `plugin=<name>` attribute. |

## Capability handshake

`InitRequest` carries the plugin's wish-list plus the host's topic
advertisement:

- `capabilities.subscribe_events` — `EventType` names the plugin wants
- `capabilities.provide_commands` — `CommandSchema` entries the plugin wishes to register
- `capabilities.sdk_version` — unused (see note below)
- `supported_event_topics` — the host's canonical list of topics it can
  deliver, the string form of `plugin.CoreEventTypes()`. The SDK uses it
  to warn on a subscription to a topic the host does not know about. An
  empty list means the host predates negotiation, and the SDK disables
  the check.

`InitResponse` carries the host's answer plus the plugin's reported version:

- `allowed_events` — the subset of `subscribe_events` permitted by the allow-list
- `allowed_commands` — the subset of command names the plugin may dispatch
- `denials` — structured `{kind, name, reason}` for anything refused, so the plugin can log it at WARN
- `sdk_version` — the value of `plugin.SDKVersion` the plugin was built
  against; the host surfaces it in `ry plugins status`

> **Why `sdk_version` lives on `InitResponse`, not `InitRequest`.**
> go-plugin makes the *host* the client that calls `PluginService.Init`,
> so the host fills `InitRequest`. Only the plugin knows its own SDK
> version, so it can only report it on the response side. The
> `capabilities.sdk_version` field on `InitRequest` is therefore vestigial
> and left unused (additive policy: it cannot be removed without a v2).

**Event topics are additive.** Adding a new `EventType` enum value (and
its oneof arm + payload message) is a minor, wire-compatible change. A
plugin built against an older SDK simply never subscribes to a topic it
does not know about; a plugin built against a newer SDK gets a WARN from
the host's `supported_event_topics` advertisement rather than a silently
dead subscription. New topics MUST be appended to `plugin.CoreEventTypes()`
so the host advertises them.

`Subscribe(topics)` is intersected with `allowed_events` — the plugin
cannot receive a topic it was denied at Init, even if it later asks for
it.

`HandleCommand` runs only for commands present in the plugin's
declared `provide_commands` (otherwise the host returns an error
before invoking the plugin).

## Subscribe stream shape

```proto
message Event {
  EventType type = 1;
  google.protobuf.Timestamp emitted_at = 2;
  oneof payload {
    CarCreatedEvent car_created = 10;
    CarClaimedEvent car_claimed = 11;
    // ... one oneof arm per topic, all in the 10..N range
  }
}
```

`type` is the discriminator the plugin SHOULD switch on; the oneof
field number per arm is stable and SHOULD NOT be relied upon for
external routing (it's an implementation detail of the encoding).

## Config

`HostService.Config(name)` returns the raw bytes of the plugin's
top-level config block from `railyard.yaml`. The SDK rehydrates the
bytes into a `yaml.Node` so the in-process `Host.Config(name) yaml.Node`
signature stays stable for plugin authors. `present = false` means no
block was configured under that name; treat that as "use defaults".

## Wire-compat policy

The v1 contract is forward-compatible for **additive** changes only.

| Change                                  | Compat   | Required action                                           |
| --------------------------------------- | -------- | --------------------------------------------------------- |
| New field on an existing message        | additive | bump snapshot, commit                                     |
| New enum value on `EventType` or `Kind` | additive | bump snapshot, commit                                     |
| New oneof arm on `Event.payload`        | additive | bump snapshot, commit                                     |
| New message type                        | additive | bump snapshot, commit                                     |
| New RPC on either service               | additive | bump snapshot, commit                                     |
| Rename a field, message, or enum value  | breaking | `v2` package required                                     |
| Renumber a field                        | breaking | `v2` package required                                     |
| Change a field's type                   | breaking | `v2` package required                                     |
| Remove a field, message, or enum value  | breaking | reserve the number+name on the removed slot; `v2` package |

`buf breaking` runs against `pkg/plugin/proto/snapshots/v1` and flags
any non-additive change. The Go test
`pkg/plugin/proto/v1/compat_test.go` invokes the same check so CI fails
on accidental breaks.

When a deliberate, additive change is reviewed and merged, refresh the
snapshot in the same commit:

```bash
cp pkg/plugin/proto/v1/plugin.proto pkg/plugin/proto/snapshots/v1/plugin.proto
```

Without that refresh, every future commit will appear to "add" the
same fields when compared against the stale snapshot — harmless but
noisy.

If a wire-breaking change is genuinely required, create a new
`pkg/plugin/proto/v2/` package alongside v1. v1 stays live until every
deployed plugin has migrated.

## Regenerating the Go stubs

```bash
scripts/proto.sh           # regenerate stubs in place; run lint + breaking
scripts/proto.sh --check   # CI mode: fail if stubs would change
```

`scripts/proto.sh` requires:

- `buf` — `go install github.com/bufbuild/buf/cmd/buf@latest`
- `protoc-gen-go` — `go install google.golang.org/protobuf/cmd/protoc-gen-go@latest`
- `protoc-gen-go-grpc` — `go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest`

The script pulls `$GOBIN` (falling back to `$GOPATH/bin`) onto `PATH`
automatically.

## Authoring guidance

- **Always add fields at the next free number.** Do not reuse a number,
  even one that was never deployed — buf cannot tell deployed history
  from local history.
- **Prefer extending messages over redefining them.** A new optional
  field next to an existing one is always safer than a parallel
  message.
- **Bound your message sizes.** Streaming `Event`s should not grow
  unboundedly; keep payloads to the minimum needed for routing and
  display.
- **`google.protobuf.Struct` is a deliberate concession** for
  `HandleCommand.args` / `CommandResult.data` to preserve the Go SDK's
  weakly-typed `map[string]any`. Once a command schema stabilizes, a
  typed message is preferable.
