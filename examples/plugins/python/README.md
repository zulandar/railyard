# py-example — a railyard plugin in Python (no Go SDK)

This is a minimal, working railyard plugin written in **Python with
`grpcio`**, implementing the wire contract by hand instead of using the Go
SDK. It is the worked example for
[`docs/plugins/non-go.md`](../../../docs/plugins/non-go.md) — read that
document first for the full handshake + dial-back walkthrough.

What it demonstrates:

- The hashicorp/go-plugin **magic-cookie handshake** and the single
  **stdout handshake line** the host parses.
- Listening on a **Unix domain socket** and serving `PluginService`
  (Init / Start / Stop / HandleCommand).
- Serving the go-plugin **broker** (`plugin.GRPCBroker/StartStream`) and
  the **dial-back** into the host's `HostService` on broker stream id 1.
- `Subscribe`-ing to one event topic (`CarCreated`) and logging each event
  with its `seq` / `dropped` counters.

> **CI note.** This example is intentionally NOT referenced by any Go
> build or test. `go build ./...` and `go test ./...` pass on a machine
> with no Python toolchain. Validation is manual — the transcript below
> was captured against a locally built host.

## Layout

```
.
├── plugin.py                 the plugin (package-free script; speaks gRPC by hand)
├── py-example                launcher the host execs (chmod +x; basename matters)
├── gen.sh                    regenerate the Python stubs from the protos
├── requirements.txt          grpcio + grpcio-tools + grpcio-health-checking
├── proto/
│   ├── plugin.proto          copy of pkg/plugin/proto/v1/plugin.proto
│   └── grpc_broker.proto     vendored go-plugin broker contract (see header)
└── railyard_plugin/          generated stubs (committed; runnable as-is)
    ├── plugin_pb2.py / plugin_pb2_grpc.py
    └── grpc_broker_pb2.py / grpc_broker_pb2_grpc.py
```

## Install deps

```bash
cd examples/plugins/python
python -m venv .venv
. .venv/bin/activate
pip install -r requirements.txt
```

## Regenerate stubs (optional — they are committed)

Run this only after refreshing `proto/plugin.proto` from
`pkg/plugin/proto/v1/plugin.proto`:

```bash
./gen.sh                      # uses `python`; override with PYTHON=...
PYTHON=.venv/bin/python ./gen.sh
```

## Run it against a dev host

The plugin is **not** a standalone program. The railyard host execs the
`py-example` launcher as a child, drives the handshake on its stdout, and
brokers everything over a Unix socket. You install the launcher into a
`plugins.d` directory the host scans and enable it in `railyard.yaml`.

The launcher `exec`s `python plugin.py` (exec, not fork, so the connecting
PID stays the host's direct child — required by the Linux peer-cred check;
see `docs/plugins/non-go.md`). Point it at your venv interpreter with
`RYPY_PYTHON`:

```yaml
# railyard.yaml
plugins:
  enabled: [py-example]
  plugins_dir: ./plugins        # or wherever your host scans
  py-example:
    allow:
      events: [CarCreated]       # deny-by-default: grant the topic it subscribes to
      commands: [py-example.ping]
```

```bash
# Drop a symlink (or copy) into the plugins dir under the basename the
# host matches against plugins.enabled.
mkdir -p ./plugins
ln -sf "$PWD/examples/plugins/python/py-example" ./plugins/py-example

# Tell the launcher which interpreter to use.
export RYPY_PYTHON="$PWD/examples/plugins/python/.venv/bin/python"

# Start the host (e.g. the yardmaster). The plugin's stderr lines are
# forwarded by the host's stdio plumbing; its event logs also go through
# HostService.Log when you wire them that way.
ry up    # or however you start your dev host
```

`ry plugins status` should then show `py-example` as **running**.

## Verified run (captured transcript)

This example was run against an in-tree host built from this repo on
Linux. The host was stood up with the same wiring
`pkg/cli/example_plugin_build_test.go` uses for the Go hello example: a
real `events.Bus` + `pluginhost.Host` pointed at a temp `plugins.d`
containing a symlink to `py-example`, with `RYPY_PYTHON` set to the venv
interpreter and `RYPY_LOG_FILE` set so the harness could observe the
plugin's own log lines. After `Init`/`Start`, the harness published a
`CarCreated` event on the bus.

Plugin log (stderr, mirrored to the harness log file):

```
[py-example] listening on unix:/tmp/plugin-py-301831.sock
[py-example] broker StartStream: service_id=1 network='unix' address='/tmp/plugin1178711144'
[py-example] Init: plugin_name='py-example' supported_topics=['CarCreated', 'CarClaimed', 'CarStatusChanged', 'CarMerged', 'MergeFailed', 'EngineStarted', 'EngineStopped', 'EngineStalled', 'YardmasterAction', 'YardPaused', 'YardResumed']
[py-example] dial-back OK: yard_id='railyard' version='test-live'
[py-example] Start: opening Subscribe stream
[py-example] event: topic=EVENT_TYPE_CAR_CREATED seq=1 dropped=0 payload=car_created
```

Host log (slog), showing the lifecycle and the deny-by-default allow-list:

```
INFO  pluginhost: yard_id not set in config; falling back to project ... project=railyard
INFO  plugin py-example: init plugin=py-example
WARN  pluginhost: capability denied plugin=py-example cap=command:py-example.ping reason=not-in-allow-list
INFO  plugin py-example: started (events=1 commands=0) events=1 commands=0
INFO  plugin py-example: stopped plugin=py-example
```

(The `py-example.ping` denial above is from a run whose `allow` block
granted only the event, not the command — grant `commands: [py-example.ping]`
to clear it. The `topic=EVENT_TYPE_CAR_CREATED` line shows the raw enum
name because this example logs `EventType.Name(ev.type)`; the canonical
allow-list spelling is `CarCreated`.)

The key lines proving the contract works end-to-end:

- `broker StartStream: service_id=1 ...` — the host's `AcceptAndServe(1)`
  reached the plugin's broker.
- `dial-back OK: ...` — the plugin dialed `HostService` on that address and
  successfully called `YardInfo`.
- `event: ... seq=1 dropped=0 ...` — a real bus event was delivered on the
  `Subscribe` stream with the monotonic counters intact.
