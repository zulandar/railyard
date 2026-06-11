# Operating Railyard Plugins

This guide is for operators running railyard who need to install,
enable, configure, and troubleshoot plugins. It assumes Linux
familiarity and access to `railyard.yaml`. It does **not** assume any
knowledge of gRPC or HashiCorp go-plugin.

If you are writing a plugin instead of running one, see
[`authoring.md`](authoring.md). For the wire contract, see
[`proto.md`](proto.md).

---

## Overview

A railyard plugin is a standalone executable that the railyard host
launches as a subprocess. The host and the plugin talk over gRPC on a
per-plugin Unix domain socket. Plugins are isolated from the host:
a crashing plugin cannot bring railyard down with it.

```
  railyard host
    |
    +-- plugin: trainmaster     (subprocess, UDS @ trainmaster.sock)
    |
    +-- plugin: slack-notifier  (subprocess, UDS @ slack-notifier.sock)
    |
    +-- plugin: ...
```

The pieces an operator touches:

| Piece            | Where                                                |
|------------------|------------------------------------------------------|
| Plugin binaries  | One of the `plugins.d/` directories below            |
| Enable list      | `plugins.enabled` in `railyard.yaml`                 |
| Allow-list       | `plugins.<name>.allow` in `railyard.yaml`            |
| Per-plugin cfg   | `plugins.<name>.<keys>` (and/or top-level `<name>:`) |
| Sockets          | `$XDG_RUNTIME_DIR/railyard/plugins/` (or fallback)   |
| Logs             | Wherever railyard's slog handler writes              |

---

## Installation

### Where to put binaries

The host scans these directories in priority order (lowest first; later
entries win on name collision):

1. `/etc/railyard/plugins.d/` — system-wide, for packaged installs.
2. `~/.railyard/plugins/` — per-user.
3. `./plugins/` (railyard's working directory) — developer convenience.
4. `plugins.plugins_dir` from `railyard.yaml` — optional override, wins
   over all three above.

Drop the plugin binary (produced by `go build` on the plugin author's
source) into one of these directories.

### Required: executable bit

The host **only** picks up files with at least one executable bit set.
Non-executable files are silently skipped at DEBUG level — they do not
produce a startup error.

```
chmod +x /etc/railyard/plugins.d/slack-notifier
```

A binary basename like `slack-notifier` (or `slack-notifier.exe` on
Windows; the `.exe` is stripped) becomes the plugin **name** used
everywhere else in this guide.

### Name collisions

If the same plugin name appears in two scanned directories, the
later-listed directory wins and the host emits:

```
WARN pluginhost: plugin name collision; later directory wins
     plugin=slack-notifier
     previous_path=/etc/railyard/plugins.d/slack-notifier
     new_path=/home/railyard/.railyard/plugins/slack-notifier
```

Remove one of the copies to silence the warning.

---

## Enabling plugins

Discovery alone is not enough. A plugin must also appear in
`plugins.enabled` to actually launch.

```yaml
plugins:
  enabled:
    - trainmaster
    - slack-notifier
  plugins_dir: /var/lib/railyard/plugins  # optional override directory
```

Rules:

- A plugin discovered in `plugins.d/` but **not** in `enabled` is
  ignored (no warning — this is the common case for binaries you
  installed but don't want active right now).
- A plugin listed in `enabled` but **not** discovered is logged at
  WARN ("enabled but not discovered") so a missing binary surfaces at
  startup.
- The `plugins:` block is optional. If absent, no plugins launch and
  the host is a pass-through.

---

## Allow-list (capabilities)

Every plugin advertises a set of capabilities at Init time:

- **Event subscriptions** it wants to receive (`CarCreated`,
  `CarMerged`, ...).
- **Commands** it wants to provide (`dispatch.run`, `slack.post`, ...).

The operator decides which of those are granted. The default is
**strict**: a plugin with no `allow` block can run, but every
`Subscribe` and `DispatchCommand` call it makes fails with
`PermissionDenied`. This forces an explicit allow-list — silent
"plugin can do anything" is not a configuration the host supports.

> **Status:** the allow-list configuration is being added by
> `railyard-fll.4`. Run-time enforcement (Subscribe/DispatchCommand
> denials) is task `railyard-fll.4.4` and may not yet be in your
> railyard build. Until it lands, the strict-default *config* is parsed
> but the *enforcement* path is permissive. Verify your railyard
> version's behavior before relying on the allow-list for security.

### Shape

```yaml
plugins:
  enabled: [trainmaster, slack-notifier]

  trainmaster:
    allow:
      events:   ["*"]                # any event (core + plugin-published)
      commands: ["dispatch.*"]       # any command in the dispatch.* namespace
      publish:  ["trainmaster.*"]    # may publish its own namespaced events

  slack-notifier:
    allow:
      events:   [CarMerged, MergeFailed, "trainmaster.synced"]
      commands: []                   # no commands
      # no publish entry -> may not emit any events (deny-by-default)
```

### Publishing events (`allow.publish`)

A plugin can publish its own events onto the bus via `Host.Emit`
(railyard-77h.9) so other plugins can react. Published topics MUST be
namespaced with the plugin's own name (`"<plugin>.<name>"`); the host
rejects any other prefix with `PermissionDenied` using the
connection-bound identity, so a plugin cannot spoof another's namespace.

`allow.publish` gates which topics a plugin may emit, **deny-by-default**
(an empty or absent list forbids all publishing). It uses the same
wildcard grammar as `commands`: `"*"`, a `"ns.*"` prefix wildcard, or a
literal. To let a *subscriber* receive a plugin-published event, list the
namespaced topic (or `"*"`) in that subscriber's `allow.events`, exactly
as for core topics. Subscribers receive the payload as `map[string]any`.

### Wildcards

| Pattern        | Where used        | Matches                              |
|----------------|-------------------|--------------------------------------|
| `"*"`          | events, commands, publish | All entries                  |
| `"ns.*"`       | commands, publish | Anything starting with `ns.`         |
| `"CarMerged"`  | events            | That exact topic                     |
| `"slack.post"` | commands, publish | That exact command/topic             |

`"**"`, `"*x"`, and other patterns are **rejected at config load** with
an error. Keep it simple: full wildcard, prefix wildcard, or literal.

Events use a closed and small topic set (see below), so only the full
`"*"` wildcard is supported for events; there is no `"Car.*"` form.

### Event topics

The Phase-1 closed set of event names a plugin may subscribe to:

| Topic                | Fires when                                          |
|----------------------|-----------------------------------------------------|
| `CarCreated`         | A car is written to the yard                        |
| `CarClaimed`         | An engine takes ownership of an unclaimed car       |
| `CarStatusChanged`   | A car transitions status                            |
| `CarMerged`          | A car is merged to its target branch                |
| `MergeFailed`        | A merge attempt fails                               |
| `EngineStarted`      | An engine registers and begins accepting work       |
| `EngineStopped`      | An engine shuts down cleanly                        |
| `EngineStalled`      | The yard detects an engine has stopped reporting    |
| `YardmasterAction`   | The yardmaster acts on a car or engine              |
| `YardPaused`         | An operator pauses the yard                         |
| `YardResumed`        | An operator resumes a paused yard                   |

Source of truth: `pkg/plugin/event.go`. New topics are a breaking SDK
change.

### Denial behavior

When the plugin advertises a capability the allow-list does not grant:

- The plugin is **not** killed. It still completes Init and continues
  running — this lets a plugin with mixed caps come up partially
  functional.
- Each denied capability is logged at WARN once at startup:

  ```
  WARN pluginhost: capability denied by allow-list
       plugin=slack-notifier cap=CarCreated reason=not-in-allow-list
  ```

- Run-time calls (`Subscribe`, `DispatchCommand`) that depend on the
  denied capability return `PermissionDenied`.

---

## Per-plugin configuration

Plugins read their own configuration from a top-level YAML block keyed
by the plugin's name. Two equivalent shapes are supported; pick the
one you find clearer:

**Under `plugins.<name>`:**

```yaml
plugins:
  enabled: [slack-notifier]
  slack-notifier:
    allow:
      events: [CarMerged, MergeFailed]
    webhook_url: https://hooks.slack.example/T0000/B0000/xxx
    template:    "Car {{.CarID}} merged"
```

**At the top level (sibling to `plugins`):**

```yaml
plugins:
  enabled: [slack-notifier]
  slack-notifier:
    allow:
      events: [CarMerged, MergeFailed]

slack-notifier:
  webhook_url: https://hooks.slack.example/T0000/B0000/xxx
  template:    "Car {{.CarID}} merged"
```

Top-level YAML keys that aren't part of railyard's typed config schema
are stashed and made available to the plugin host's `Config` RPC. The
plugin author decides which keys it accepts — read the plugin's docs,
not railyard's, for the schema.

---

## Runtime

### Sockets

The host creates a Unix domain socket per plugin. Path resolution, in
order:

1. `$XDG_RUNTIME_DIR/railyard/plugins/<name>.sock` — preferred.
2. `/run/railyard/plugins/<name>.sock` — used when the directory is
   already writable (typical of systemd-managed installs).
3. `/tmp/railyard-<uid>/plugins/<name>.sock` — fallback for bare shells
   where neither of the above is available.

The parent directory is created with mode `0700`. The socket file
itself is bound by go-plugin and ends up owned by the railyard uid;
cross-uid attackers cannot attach. Sockets are removed on graceful
Stop and on permanent-disable.

> **Note:** the on-disk socket filename is chosen by the go-plugin
> library inside the per-plugin directory and may not exactly match
> `<name>.sock` — the directory layout is the operator-visible
> contract.

### Handshake and protocol version

When the host launches a plugin it sets the environment variable
`RAILYARD_PLUGIN_MAGIC_COOKIE=railyard-plugin-v1` to guard against the
binary being executed by accident, and negotiates a protocol version.
The current `ProtocolVersion` is **1**. A plugin built against a
different protocol version refuses to handshake; you will see
something like:

```
ERROR pluginhost: go-plugin handshake failed
      plugin=foo err="incompatible protocol version"
```

Rebuild the plugin against the same railyard release.

### Crash policy

> **Status:** the auto-restart loop and 3-in-60s budget are being
> implemented in `railyard-fll.6` and may not yet be in your railyard
> build. Until that lands, a plugin subprocess that exits non-zero is
> **not** auto-relaunched — it is treated as permanently disabled for
> the rest of the railyard process lifetime on its first crash. The
> design below is what `railyard-fll.6` will deliver.

Intended behavior once `railyard-fll.6` lands:

- If a plugin subprocess exits non-zero, the host relaunches it after a
  capped exponential backoff (250 ms × 2ⁿ, max 5 s).
- Crashes within a sliding 60-second window are counted per plugin.
  The **4th** crash within 60 s flips the plugin to permanently
  disabled for the rest of the railyard process lifetime. The host
  logs an ERROR identifying the plugin and the exit reason.
- The crash counter resets across planned restarts of railyard.
- The escape hatch for a permanently-disabled plugin is to restart
  railyard.

### Graceful shutdown

On `SIGTERM` or `SIGINT` to railyard:

1. The host calls `PluginService.Stop` on each plugin in
   reverse-startup order.
2. Each plugin gets a per-call drain budget (`stopDrainTimeout`,
   currently a few seconds). A plugin that blocks past the budget is
   `SIGKILL`ed.
3. Sockets are removed.

### Inspecting runtime state

`ry plugins status` queries a running yardmaster over HTTP and prints a
table of per-plugin runtime state. (Use `ry plugins list` for the
build-time view of what is on disk.)

The default table is kept narrow for readability:

```
NAME  STATUS  HEALTH  SDK  RESTARTS  SUBS  CMDS  LAST-ACTIVITY  PID  PATH  ERROR
```

| Column          | Meaning                                                            |
|-----------------|--------------------------------------------------------------------|
| `STATUS`        | `running` / `disabled` / `failed` / `skipped`                      |
| `HEALTH`        | Plugin's self-reported functional health (see below)               |
| `RESTARTS`      | Successful supervisor relaunches since the yard booted             |
| `SUBS`          | Active event-stream subscriptions the plugin currently holds       |
| `CMDS`          | Commands the plugin registered (capability count, not invocations) |
| `LAST-ACTIVITY` | Relative time since the plugin last did something host-observed    |

#### Plugin health (`HEALTH` column)

`STATUS` tells you whether the **process** is alive; `HEALTH` tells you
whether the plugin is actually **functional**. A connector with dead
remote credentials shows `running` under `STATUS` but `failing` under
`HEALTH`.

The host polls each running plugin's optional health probe on an
interval and renders the latest verdict as `<value> <age>`, e.g.
`ok 12s` — the verdict plus how long ago it was checked:

| `HEALTH` value | Meaning                                                                  |
|----------------|--------------------------------------------------------------------------|
| `ok`           | The plugin reports it is fully functional                                |
| `degraded`     | Running but impaired — OR the host's health probe errored / timed out    |
| `failing`      | Running but non-functional (e.g. dead credentials), though the process is alive |
| `n/a`          | The plugin does not implement the health probe — not an error            |
| `-`            | Not applicable (non-running row) or not yet polled                       |

`n/a` is expected and harmless: implementing the probe is opt-in for
plugin authors (see [`authoring.md`](authoring.md) → *Optional: reporting
health*). The full verdict — `health`, `health_message` (the plugin's
own message or the probe error text), and `health_checked_at` — is always
present in `--json` output.

**Tuning the poll interval.** The host polls every 30 seconds by default.
Override it under the `plugins:` block in `railyard.yaml`:

```yaml
plugins:
  enabled: [trainmaster]
  health_interval_sec: 60   # poll every 60s instead of the 30s default
```

A value of `0` or omitting the key uses the 30s default. The host always
enforces a 2-second deadline on each individual probe regardless of the
interval, so a wedged plugin can never stall the poller.

#### Verbose runtime counters (`-v` / `--verbose`)

`ry plugins status -v` prints an additional block below the table with
per-plugin **lifetime runtime counters**:

```
RUNTIME COUNTERS (process-lifetime; reset on yardmaster restart):
NAME  EVENTS-DELIVERED  EVENTS-DROPPED  CMDS-HANDLED  CMDS-FAILED  AVG-LATENCY
```

| Counter            | Meaning                                                                        |
|--------------------|--------------------------------------------------------------------------------|
| `EVENTS-DELIVERED` | Events successfully sent on the plugin's subscription stream(s)                |
| `EVENTS-DROPPED`   | Events dropped on the drop-oldest backpressure path before reaching the plugin |
| `CMDS-HANDLED`     | Commands dispatched into the plugin's `HandleCommand` (counts every invocation) |
| `CMDS-FAILED`      | Of those, the ones that returned a transport error or a logical failure        |
| `AVG-LATENCY`      | Mean `HandleCommand` wall-clock latency, derived from the cumulative total      |

The raw fields (including `command_latency_total_micros` and
`command_latency_avg_micros`) are always present in `--json` output.

**Reset semantics.** These counters are **process-lifetime**: they
accumulate from the moment the yardmaster process boots and are reset
only when that process restarts. They **survive a plugin relaunch** — a
crashing plugin that the supervisor restarts keeps its accumulated
counters (and `RESTARTS` increments), because the counters live on the
host's per-plugin registry entry, not on the subprocess.

### Logging

Every log line emitted by or about a plugin carries the structured
attribute `plugin=<name>`. Filter by it to isolate one plugin's
output:

```
jq 'select(.plugin == "slack-notifier")' /var/log/railyard.json
```

Init-time capability denials are logged at WARN with `cap=<name>` and
`reason=not-in-allow-list` (see [Allow-list](#allow-list-capabilities)
above).

---

## Troubleshooting

| Symptom                                              | Likely cause                                        | Fix                                                                            |
|------------------------------------------------------|-----------------------------------------------------|--------------------------------------------------------------------------------|
| "plugin not found" / enabled but not discovered      | Binary not in a scanned directory                   | Drop binary in `plugins.d/` or set `plugins.plugins_dir`                       |
| Plugin in `plugins.d/` but never launches            | Missing executable bit                              | `chmod +x <binary>`                                                            |
| `PermissionDenied` on `Subscribe`                    | Event not in `allow.events`                         | Add the topic or `"*"` to `allow.events`                                       |
| `PermissionDenied` on `DispatchCommand`              | Command not in `allow.commands`                     | Add the command or a `"ns.*"` prefix to `allow.commands`                       |
| WARN "plugin name collision"                         | Same plugin in two scanned dirs                     | Remove one; later directory wins                                               |
| ERROR "plugin permanently disabled"                  | Crash budget exceeded (4 crashes in 60 s)           | Check plugin logs, fix the crash, restart railyard                             |
| ERROR `SO_PEERCRED` mismatch                         | Connecting peer's pid/uid did not match launched    | Security check tripped; the plugin will not be retried until railyard restarts |
| ERROR "incompatible protocol version" at handshake   | Plugin built against a different railyard version   | Rebuild the plugin against this railyard release                               |
| Plugin runs but does nothing                         | No `allow` block → all caps denied (strict default) | Add an `allow:` block under `plugins.<name>` listing the caps you want         |
| WARN "capability denied by allow-list"               | Plugin advertised a cap your allow-list doesn't list | Add the cap to `allow.events` / `allow.commands`, or accept the denial         |
| Config rejected: invalid wildcard                    | Used `"**"`, `"*x"`, or `"Car.*"` for events        | Use `"*"`, a literal name, or `"ns.*"` (commands only)                         |

If the symptom isn't in the table, grep the log for `plugin=<name>` —
every plugin-related log line is tagged.

---

## Security notes

- **Plugins run as the railyard uid.** They are not sandboxed beyond
  their own process. Treat each enabled plugin as having full access
  to whatever railyard itself can do (filesystem, network, env vars).
- **Socket permissions.** The per-plugin socket directory is `0700`.
  The socket file is owned by the railyard uid; another uid cannot
  attach.
- **`SO_PEERCRED` check (Linux only).** Immediately after handshake
  the host opens a second connection to the plugin's socket and reads
  the kernel-reported peer credentials. The pid must match the
  subprocess railyard launched and the uid must match railyard's own.
  Mismatch kills the plugin and marks it unlaunchable for the rest of
  the railyard process lifetime. On non-Linux platforms this check is
  skipped (`DEBUG: SO_PEERCRED check skipped`) and the launched pid is
  trusted.
- **Magic cookie.** The `RAILYARD_PLUGIN_MAGIC_COOKIE` env var is a
  usability guard against accidentally running a plugin binary on its
  own. It is **not** a security boundary — anyone with read access to
  the host process environment can see it.
- **Allow-list is the principal trust knob.** If you treat strict
  default + an explicit per-plugin `allow` block as the rule, an
  attacker who replaces a plugin binary can still only exercise the
  caps the operator granted. Be intentional with `"*"`.

---

## Where things live

| Thing               | Path                                                            |
|---------------------|-----------------------------------------------------------------|
| Plugin binaries     | `/etc/railyard/plugins.d/`, `~/.railyard/plugins/`, `./plugins/`, or `plugins.plugins_dir` |
| Sockets             | `$XDG_RUNTIME_DIR/railyard/plugins/<name>.sock` (or fallback)   |
| Logs                | Wherever railyard's slog handler writes (operator's choice)     |
| Config              | The `plugins:` block in `railyard.yaml` plus per-plugin top-level YAML |

---

## See also

- [`docs/plugins/authoring.md`](authoring.md) — writing a plugin.
- [`docs/plugins/proto.md`](proto.md) — wire contract and compatibility
  policy.
- `bd show railyard-fll` — the live design tracker for the plugin
  system overhaul (allow-list in `railyard-fll.4`, crash policy in
  `railyard-fll.6`).
