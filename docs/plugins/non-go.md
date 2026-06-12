# Writing a railyard plugin WITHOUT the Go SDK

The Go SDK (`pkg/plugin`) hides every wire detail behind `plugin.Serve`
and the `Host` interface. This document is for the other case: writing a
plugin in **any language with a gRPC stack** by speaking the wire
contract directly. It reverse-engineers the hashicorp/go-plugin behavior
the SDK relies on, anchored to the railyard source so it does not drift.

A complete, **runnable** Python implementation lives at
[`examples/plugins/python/`](../../examples/plugins/python/). Read this
document for the *why*; read that example for a concrete *how*.

> **Audience.** You already know gRPC, protobuf, and Unix sockets. You do
> NOT want to link Go. Everything here is the contract a non-Go process
> must satisfy to be launched and driven by a railyard host.

The message schema is in
[`pkg/plugin/proto/v1/plugin.proto`](../../pkg/plugin/proto/v1/plugin.proto)
and summarized in [`proto.md`](proto.md). This document covers the
**envelope**: process launch, handshake, the broker dial-back, lifecycle
ordering, and the Linux peer-cred check.

---

## The big picture

railyard uses [hashicorp/go-plugin](https://github.com/hashicorp/go-plugin)
as its subprocess transport. The roles are **inverted** from what you
might expect:

| Process | go-plugin role | Serves | Calls |
| ------- | -------------- | ------ | ----- |
| railyard host (`ry` / yardmaster) | go-plugin **Client** | `HostService` (via the broker) | `PluginService` (Init/Start/Stop/HandleCommand) |
| your plugin | go-plugin **Server** | `PluginService` + the go-plugin broker | `HostService` (dialed back over the broker) |

So your plugin process is the gRPC **server** for the lifecycle RPCs, and
becomes a gRPC **client** of `HostService` after dialing back through the
broker. Both directions ride over Unix domain sockets.

The exact constants below come from
[`pkg/plugin/handshake.go`](../../pkg/plugin/handshake.go); the host-side
launch wiring is in
[`internal/pluginhost/launch.go`](../../internal/pluginhost/launch.go);
the SDK's own dial-back (which you are re-implementing) is
`resolveHostClient` in
[`pkg/plugin/serve.go`](../../pkg/plugin/serve.go).

---

## 1. Launch and the magic cookie

The host discovers your executable by **basename** matching an entry in
`plugins.enabled` (see [`operating.md`](operating.md)), then runs it as a
direct child via `exec.Command(path)`. Before any gRPC, it sets these
environment variables on the child:

| Env var | Value | Notes |
| ------- | ----- | ----- |
| `RAILYARD_PLUGIN_MAGIC_COOKIE` | `railyard-plugin-v1` | The handshake guard. `MagicCookieKey` / `MagicCookieValue` in `handshake.go`. |
| `PLUGIN_PROTOCOL_VERSIONS` | `1` | Comma-separated list of protocol versions the host accepts. railyard uses `1` (`ProtocolVersion`). |
| `PLUGIN_UNIX_SOCKET_DIR` | a temp dir | The directory your plugin MUST create its listening socket inside. |
| `PLUGIN_MIN_PORT` / `PLUGIN_MAX_PORT` | `0` | Only relevant on Windows (TCP fallback). Ignore on Linux. |

Your first action: **check the cookie.** If
`RAILYARD_PLUGIN_MAGIC_COOKIE != "railyard-plugin-v1"`, you were almost
certainly run directly by a human rather than launched by the host — print
a friendly hint to stderr and exit non-zero. This is a UX guard, **not a
security boundary** (anyone who can launch the host can read the value).

railyard does **not** set `PLUGIN_MULTIPLEX_GRPC` and does **not** use
AutoMTLS, which simplifies two things you would otherwise have to handle
(see §3 and §2).

---

## 2. Listen, then print the handshake line

1. **Create a Unix socket inside `PLUGIN_UNIX_SOCKET_DIR`.** go-plugin's
   own server picks a random `plugin*` filename in that directory; you can
   pick any unique name there. Bind a gRPC server to it.

2. On that gRPC server, register:
   - `railyard.plugin.v1.PluginService` (your Init/Start/Stop/HandleCommand)
   - `plugin.GRPCBroker` (the broker — see §3)
   - `grpc.health.v1.Health` reporting service name **`plugin`** as
     `SERVING`. The host pings this during the handshake; if it is missing,
     `client.Client()` hangs and the launch times out.
   - (Optional) `plugin.GRPCController` (a single `Shutdown` RPC). The host
     calls it on clean teardown; you can omit it and rely on `Stop` +
     SIGTERM, but implementing it is tidy.

3. **Print exactly one line to stdout**, then nothing else ever. stdout is
   reserved for this line; any stray byte corrupts the handshake the host
   parses. Flush it. The format (from go-plugin `server.go`) is
   pipe-delimited:

   ```
   CORE-PROTOCOL-VERSION|APP-PROTOCOL-VERSION|NETWORK|ADDR|PROTOCOL|TLS-CERT
   ```

   - `CORE-PROTOCOL-VERSION` — go-plugin's own core version, currently `1`.
   - `APP-PROTOCOL-VERSION` — railyard's `ProtocolVersion`, currently `1`.
   - `NETWORK` — `unix` (or `tcp` on Windows).
   - `ADDR` — the absolute socket path you bound.
   - `PROTOCOL` — `grpc`.
   - `TLS-CERT` — **empty** for railyard (no AutoMTLS). The field is still
     present (trailing pipe).

   A concrete line your plugin should print:

   ```
   1|1|unix|/tmp/plugin-unix-sockets123/plugin-9f3c.sock|grpc|
   ```

   > **Six fields, not seven.** Newer go-plugin appends a seventh
   > `true`/`false` multiplexing segment *only when* `PLUGIN_MULTIPLEX_GRPC`
   > is set in the environment. railyard does not set it, so emit the
   > six-field form. (If you ever see the host setting that env, see §3 for
   > what changes.)

After this line is read, the host dials your socket, runs the gRPC health
check, and the connection is live.

---

## 3. The broker and the HostService dial-back (the hard part)

Your plugin needs to call back into the host — `Subscribe`, `Snapshot`,
`DispatchCommand`, `Log`, `EmitEvent`. Those live on `HostService`, which
the host serves on a **separate** socket reached through go-plugin's
**broker**.

### What the broker is

The broker is a single bidirectional gRPC stream, `plugin.GRPCBroker/StartStream`,
that multiplexes "please open a side connection with this numeric id"
negotiations between the two processes. Each side can ask the other to open
a fresh listener tagged with a `uint32` stream id; connection info
(network + address) flows over the `StartStream` stream so the dialer knows
where to connect.

railyard reserves **stream id 1** (`HostBrokerID` in `handshake.go`) for
`HostService`. The host serves `HostService` there; your plugin dials it.

### Who serves the stream, who sends what

Because the host is the go-plugin **Client** and you are the **Server**:

- **You** serve `plugin.GRPCBroker/StartStream` (the server side of the
  bidi RPC). The host opens the stream as the client.
- The host calls `broker.AcceptAndServe(1, ...)` (see
  `launch.go`). Internally (non-multiplexed path) that:
  1. opens a **brand-new Unix listener** for `HostService`,
  2. **sends you a `ConnInfo`** down the `StartStream` stream:
     `{ service_id: 1, network: "unix", address: "/tmp/pluginXXXX" }`,
  3. serves `HostService` on that listener.
- **You** receive that `ConnInfo` on your `StartStream` server, see
  `service_id == 1`, and **dial `unix:<address>`** to create a
  `HostService` client.

That is the entire dial-back. The `ConnInfo` message (vendored at
[`examples/plugins/python/proto/grpc_broker.proto`](../../examples/plugins/python/proto/grpc_broker.proto))
is:

```proto
message ConnInfo {
  uint32 service_id = 1;
  string network = 2;
  string address = 3;
  message Knock { bool knock = 1; bool ack = 2; string error = 3; }
  Knock knock = 4;
}
service GRPCBroker {
  rpc StartStream(stream ConnInfo) returns (stream ConnInfo);
}
```

### The exact sequence a non-Go stack performs

1. Register `plugin.GRPCBroker` on your gRPC server (alongside
   `PluginService` + health). Keep the package name `plugin` and the field
   numbers exactly as above — gRPC routes by the fully-qualified service
   name `plugin.GRPCBroker`, and the host will not find your handler if you
   rename it.
2. In your `StartStream` handler, loop reading inbound `ConnInfo` messages
   from the request stream.
3. When a message arrives with `service_id == 1` and a non-empty
   `address`, treat `network`/`address` as the `HostService` endpoint:
   open a gRPC client channel to `unix:<address>` and build a
   `HostService` stub on it. Stash it where `Init`/`Start` can use it.
4. You do **not** need to send anything back on the stream for this
   (non-multiplexed) path — no `Knock`/`Ack` exchange. Just keep the
   response side open (yield nothing) until the host closes the stream.

> **`Knock`/`Ack` only matters with multiplexing.** When
> `PLUGIN_MULTIPLEX_GRPC` is set, go-plugin reuses the main connection
> instead of opening a fresh socket, and the dialer first sends a
> `ConnInfo{ Knock{ knock: true } }` and waits for a
> `ConnInfo{ Knock{ knock: true, ack: true } }` before dialing. railyard
> does not enable multiplexing, so you can ignore the `Knock` arm
> entirely — but it is in the proto so your stubs compile against the real
> contract.

### Timing

The host runs `AcceptAndServe(1)` immediately after it dispenses the
plugin and **before** it calls `PluginService.Init`. In practice the
`ConnInfo` arrives at your `StartStream` handler at roughly the same time
as, or just before, your `Init` is invoked. Make `Init` **wait briefly**
(a few seconds) for the `ConnInfo` to land, then dial. The Python example
uses a `threading.Event` set by the broker handler and waited on at the top
of `Init`.

---

## 4. Lifecycle: the RPC order the host drives

The host is the **client** of `PluginService`. It calls, in this order:

1. **`Init(InitRequest) -> InitResponse`** — once, right after launch.
   - The request carries `plugin_name`, the host's `supported_event_topics`
     (canonical topic names it can deliver — use it to warn on unknown
     topics; an empty list means an old host, so skip the check), and a
     `capabilities` wish-list the host fills from its own config.
   - Your response advertises `allowed_events`, `allowed_commands`,
     `denials`, and your own `sdk_version` string (shown in
     `ry plugins status`). NOTE: the host is the Init *client*, so the only
     way to report YOUR version is on the **response** —
     `Capabilities.sdk_version` on the request is vestigial.
   - Do your dial-back here (or ensure it is ready). Init MUST complete
     before Start.

2. **`Start(StartRequest) -> StartResponse`** — once, after core subsystems
   are up. Open your `HostService.Subscribe` stream, register intentions,
   launch workers. **Return promptly** — long work belongs on a worker
   thread/goroutine, not in the Start body.

3. **`HandleCommand(HandleCommandRequest) -> HandleCommandResponse`** — any
   time after Init, whenever an external caller dispatches a command your
   plugin registered (and the operator allow-listed). `args`/`data` are
   `google.protobuf.Struct`.

4. **`Stop(StopRequest) -> StopResponse`** — once, on shutdown.
   `StopRequest.drain_timeout_ms` is a hint; **the host enforces its own
   ~5-second drain** regardless. Cancel your subscribe stream, close
   clients, and return within that window — past it the host kills the
   process and counts the exit against the crash budget.

Calling `HostService` (Subscribe/Snapshot/DispatchCommand/Log/EmitEvent)
is valid any time after your dial-back is established (i.e. from Init
onward).

---

## 5. The Linux peer-cred check — do NOT fork

On Linux the host performs an `SO_PEERCRED` verification
([`internal/pluginhost/peercred_linux.go`](../../internal/pluginhost/peercred_linux.go),
called from `launch.go`): it opens a probe connection to your socket and
reads the connected peer's **pid** and **uid**. It requires:

- **uid** == the host's own uid, and
- **pid** == the exact pid of the child the host launched.

The consequence for non-Go plugins is concrete and easy to get wrong:

> **The process that binds and accepts on the socket must be the SAME
> process the host `exec`'d** — same PID, same uid.

If you ship a wrapper script that **forks** an interpreter (e.g.
`python plugin.py &`, or any launcher that spawns a child and waits), the
forked child has a different PID, the peer-cred check fails, and the host
kills your plugin with a `SO_PEERCRED: peer pid=... != launched pid=...`
error.

The fix is to **`exec`** (replace the process image in place), which
preserves the PID:

```bash
#!/usr/bin/env bash
exec python3 "$(dirname "$(readlink -f "$0")")/plugin.py"   # exec, NOT &
```

The example's [`py-example`](../../examples/plugins/python/py-example)
launcher does exactly this. On non-Linux platforms the host logs a DEBUG
and trusts the launched pid (peer-cred is Linux-only), but you should
assume Linux semantics.

---

## 6. Sequence diagram

```
 HOST (go-plugin Client)                    PLUGIN (go-plugin Server, your code)
 =======================                    ====================================
        |                                                |
        |  fork+exec child, set env:                     |
        |  RAILYARD_PLUGIN_MAGIC_COOKIE,                 |
        |  PLUGIN_UNIX_SOCKET_DIR, ...                   |
        |----------------------------------------------->|  check magic cookie
        |                                                |  bind unix socket in
        |                                                |  PLUGIN_UNIX_SOCKET_DIR
        |                                                |  serve PluginService +
        |                                                |  plugin.GRPCBroker +
        |                                                |  Health("plugin"=SERVING)
        |                                                |
        |        stdout: "1|1|unix|/.../p.sock|grpc|"    |
        |<-----------------------------------------------|  (one line, then silence)
        |                                                |
        |  dial unix socket, Health/Check("plugin")      |
        |----------------------------------------------->|
        |                                                |
        |  open broker stream: plugin.GRPCBroker/        |
        |    StartStream  (host = stream client)         |
        |<---------------------------------------------->|  serve StartStream
        |                                                |
        |  AcceptAndServe(id=1):                          |
        |   - open NEW unix listener for HostService      |
        |   - Send ConnInfo{service_id:1,                 |
        |       network:"unix", address:"/.../host.sock"} |
        |------------------- (over StartStream) --------->|  recv ConnInfo(id=1)
        |   - serve HostService on that listener           |  dial unix:host.sock
        |                                                 |  -> HostService stub ready
        |                                                 |
        |  PluginService.Init(InitRequest)                |
        |------------------------------------------------>|  (wait for dial-back,
        |                                                 |   then ready)
        |  InitResponse{allowed_events, sdk_version,...}  |
        |<------------------------------------------------|
        |                                                 |
        |  (core boots)                                   |
        |  PluginService.Start(StartRequest)              |
        |------------------------------------------------>|  HostService.Subscribe(
        |  StartResponse                                  |    topics=[CarCreated])
        |<------------------------------------------------|----------------------->|
        |                                                 |   server-stream Event{
        |        bus event ...                            |     seq, dropped, payload}
        |------------------- (over HostService) --------->|  handle event
        |                                                 |
        |  ... HandleCommand any time after Init ...      |
        |                                                 |
        |  PluginService.Stop(StopRequest{drain_ms})      |
        |------------------------------------------------>|  cancel stream, cleanup
        |  StopResponse                                   |  (within ~5s drain)
        |<------------------------------------------------|
        |  (kill + remove socket if drain exceeded)       |
```

---

## 7. The current proto surface to honor

Generate your stubs from
[`pkg/plugin/proto/v1/plugin.proto`](../../pkg/plugin/proto/v1/plugin.proto)
(the example commits a copy under `proto/`). A few fields are easy to miss
and are part of the **current** contract — see [`proto.md`](proto.md) for
the full table:

- **`InitRequest.supported_event_topics`** — the host's canonical topic
  list. Warn (do not fail) if you subscribe to something not in it; empty
  means an old host, so skip the check.
- **`InitResponse.sdk_version`** — report your own version string here, not
  on the request.
- **`Event.seq`** (monotonic, starts at 1 per stream) and
  **`Event.dropped`** (cumulative drops). A jump in `dropped` between two
  received events means the host shed load — re-`Snapshot` to reconcile.
  `seq` resetting to 1 means the stream reopened (treat as "snapshot
  first").
- **`Event.topic_name`** + the **`custom`** oneof arm — for
  plugin-published dynamic events (`HostService.EmitEvent`). For these,
  `type == EVENT_TYPE_UNSPECIFIED`, `topic_name` carries the namespaced
  topic (e.g. `trainmaster.synced`), and the payload is the `custom`
  `google.protobuf.Struct`. Core events instead use the `type` enum and
  the matching typed oneof arm.
- **`HostService.EmitEvent`** — lets your plugin publish a namespaced event
  (`<your-name>.<something>`). The host derives the namespace from the
  connection identity and rejects un-prefixed or un-allow-listed topics
  with `PermissionDenied`.

Topic and field additions are **additive** (see the wire-compat policy in
[`proto.md`](proto.md)): an older plugin built against fewer fields keeps
working, and a newer plugin degrades gracefully against an older host.

---

## 8. The worked example

[`examples/plugins/python/`](../../examples/plugins/python/) implements
everything above in ~250 lines of Python: the cookie check, the socket +
handshake line, the broker `StartStream` server, the dial-back, all four
`PluginService` methods, and a `Subscribe` loop that logs events with their
`seq`/`dropped` counters. Its README has install + run commands and a
captured transcript from a real host launch. Start there.
