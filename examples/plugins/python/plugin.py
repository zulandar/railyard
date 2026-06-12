#!/usr/bin/env python3
"""A minimal railyard plugin written in Python (grpcio), no Go SDK.

This is a worked example of the wire walkthrough in
docs/plugins/non-go.md. It speaks the hashicorp/go-plugin handshake by
hand, serves PluginService + the go-plugin broker, dials the host's
HostService back on broker stream id 1, subscribes to one event topic,
and logs every event it receives.

Run it via the railyard host (it is NOT a standalone program — the host
exec's it as a child and drives the handshake on stdout). See README.md.
"""

import os
import sys
import threading
import time
from concurrent import futures

import grpc
from grpc_health.v1 import health, health_pb2, health_pb2_grpc

# Generated stubs (committed; regenerate with ./gen.sh).
sys.path.insert(0, os.path.join(os.path.dirname(__file__), "railyard_plugin"))
import grpc_broker_pb2  # noqa: E402
import grpc_broker_pb2_grpc  # noqa: E402
import plugin_pb2  # noqa: E402
import plugin_pb2_grpc  # noqa: E402

# ---------------------------------------------------------------------------
# Handshake constants — these MUST match pkg/plugin/handshake.go exactly.
# ---------------------------------------------------------------------------
MAGIC_COOKIE_KEY = "RAILYARD_PLUGIN_MAGIC_COOKIE"
MAGIC_COOKIE_VALUE = "railyard-plugin-v1"
APP_PROTOCOL_VERSION = 1  # pkg/plugin.ProtocolVersion
CORE_PROTOCOL_VERSION = 1  # go-plugin's CoreProtocolVersion
HOST_BROKER_ID = 1  # pkg/plugin.HostBrokerID — HostService lives here
PLUGIN_NAME = "py-example"
SUBSCRIBE_TOPIC = "CarCreated"  # the one topic this example listens to


# Optional: mirror log lines to a file too. The live-run test harness
# points RYPY_LOG_FILE at a temp file so it can observe the plugin's
# output without depending on go-plugin's stdio capture (which the
# railyard host discards). Day-to-day operators rely on the host
# forwarding HostService.Log instead; this is purely a test affordance.
_LOG_FILE = os.environ.get("RYPY_LOG_FILE", "")


def log(msg):
    """Logs to stderr. stdout is RESERVED for the one handshake line —
    anything else printed there corrupts the handshake the host parses."""
    line = f"[{PLUGIN_NAME}] {msg}"
    print(line, file=sys.stderr, flush=True)
    if _LOG_FILE:
        try:
            with open(_LOG_FILE, "a") as f:
                f.write(line + "\n")
        except OSError:
            pass


class BrokerService(grpc_broker_pb2_grpc.GRPCBrokerServicer):
    """Serves the go-plugin broker stream.

    The railyard host is a go-plugin *Client*: its broker calls
    StartStream as the gRPC client, and WE are the StartStream server.
    When the host calls broker.AcceptAndServe(HOST_BROKER_ID, ...) it
    opens a fresh listener and sends us a ConnInfo carrying that
    listener's network+address tagged with service_id == HOST_BROKER_ID.
    We hand each inbound ConnInfo to a callback so the plugin can dial
    back into HostService.

    railyard does not enable gRPC broker multiplexing, so this is the
    simple (non-knock) path: a single ConnInfo per brokered stream id,
    no Knock/Ack exchange.
    """

    def __init__(self, on_conn_info):
        self._on_conn_info = on_conn_info

    def StartStream(self, request_iterator, context):
        # request_iterator yields ConnInfo messages the host sends us.
        # The host AcceptAndServe for HOST_BROKER_ID lands here. We never
        # need to send anything back for the non-mux dial-back, so we
        # block reading until the host closes the stream.
        for conn_info in request_iterator:
            log(
                f"broker StartStream: service_id={conn_info.service_id} "
                f"network={conn_info.network!r} address={conn_info.address!r}"
            )
            self._on_conn_info(conn_info)
        # Returning ends the response stream. (We yield nothing.)
        return iter(())


class PluginService(plugin_pb2_grpc.PluginServiceServicer):
    """The lifecycle surface the host drives: Init -> Start -> ... -> Stop,
    plus HandleCommand at any time after Init."""

    def __init__(self, host_conn_event):
        self._host_conn_event = host_conn_event  # set when broker hands us a ConnInfo
        self._host_addr = None
        self._host_stub = None
        self._sub_thread = None
        self._stop = threading.Event()

    # -- wired from main() after the broker callback fires --
    def set_host_addr(self, network, address):
        self._host_addr = (network, address)
        self._host_conn_event.set()

    def _dial_host(self):
        """Open the HostService client over the brokered listener address."""
        network, address = self._host_addr
        if network == "unix":
            target = f"unix:{address}"
        else:  # tcp
            target = address
        channel = grpc.insecure_channel(target)
        return plugin_pb2_grpc.HostServiceStub(channel)

    def Init(self, request, context):
        # The host is the CLIENT of Init; it fills InitRequest. We answer
        # with the subset we accept plus our SDK version. The example
        # simply echoes back the requested events as allowed.
        log(
            f"Init: plugin_name={request.plugin_name!r} "
            f"supported_topics={list(request.supported_event_topics)}"
        )
        # By the time Init is called, the host has already run its
        # GRPCClient callback and kicked off AcceptAndServe(HOST_BROKER_ID),
        # so our broker should have (or shortly will have) the ConnInfo.
        if not self._host_conn_event.wait(timeout=5):
            context.abort(grpc.StatusCode.INTERNAL, "host broker ConnInfo not received")
        self._host_stub = self._dial_host()

        # Demonstrate the dial-back works: read static yard identity.
        try:
            info = self._host_stub.YardInfo(plugin_pb2.YardInfoRequest(), timeout=5)
            log(f"dial-back OK: yard_id={info.yard_id!r} version={info.railyard_version!r}")
        except grpc.RpcError as e:
            log(f"dial-back YardInfo failed: {e}")

        return plugin_pb2.InitResponse(
            allowed_events=[SUBSCRIBE_TOPIC],
            allowed_commands=["py-example.ping"],
            sdk_version="python-example/0.1.0",
        )

    def Start(self, request, context):
        # Core is up. Launch the Subscribe stream on a worker thread; it
        # must not block Start.
        log("Start: opening Subscribe stream")
        self._sub_thread = threading.Thread(target=self._subscribe_loop, daemon=True)
        self._sub_thread.start()
        return plugin_pb2.StartResponse()

    def _subscribe_loop(self):
        try:
            req = plugin_pb2.SubscribeRequest(topics=[SUBSCRIBE_TOPIC])
            stream = self._host_stub.Subscribe(req)
            for ev in stream:
                if self._stop.is_set():
                    break
                topic = ev.topic_name or plugin_pb2.EventType.Name(ev.type)
                log(
                    f"event: topic={topic} seq={ev.seq} dropped={ev.dropped} "
                    f"payload={ev.WhichOneof('payload')}"
                )
        except grpc.RpcError as e:
            if not self._stop.is_set():
                log(f"Subscribe stream ended: {e.code()}")

    def HandleCommand(self, request, context):
        # Invoked when an external caller dispatches one of our registered
        # commands. The example handles "py-example.ping".
        log(f"HandleCommand: name={request.name!r}")
        if request.name == "py-example.ping":
            resp = plugin_pb2.HandleCommandResponse(success=True)
            resp.data.update({"pong": True})
            return resp
        return plugin_pb2.HandleCommandResponse(
            success=False, error=f"unknown command: {request.name}"
        )

    def Stop(self, request, context):
        # The host gives ~5s to drain (StopRequest.drain_timeout_ms is a
        # hint; the host enforces its own deadline). Tear down cleanly.
        log(f"Stop: drain_timeout_ms={request.drain_timeout_ms}")
        self._stop.set()
        return plugin_pb2.StopResponse()


def main():
    # 1. Magic-cookie guard. If we were run directly (not by the host) the
    #    cookie is absent; print the same human-friendly hint go-plugin does.
    if os.environ.get(MAGIC_COOKIE_KEY) != MAGIC_COOKIE_VALUE:
        sys.stderr.write(
            "This binary is a railyard plugin. These are not meant to be\n"
            "executed directly. Please execute the program that consumes\n"
            "these plugins, which will load any plugins automatically\n"
        )
        sys.exit(1)

    # 2. Pick the unix socket path inside the dir the host handed us. The
    #    host sets PLUGIN_UNIX_SOCKET_DIR (go-plugin EnvUnixSocketDir) and
    #    expects the plugin to bind a socket inside it.
    socket_dir = os.environ.get("PLUGIN_UNIX_SOCKET_DIR", "")
    if not socket_dir:
        # Fall back to a tmp dir if launched without one (manual testing).
        socket_dir = os.environ.get("TMPDIR", "/tmp")
    socket_path = os.path.join(socket_dir, f"plugin-py-{os.getpid()}.sock")
    if os.path.exists(socket_path):
        os.remove(socket_path)

    host_conn_event = threading.Event()
    plugin_svc = PluginService(host_conn_event)

    def on_conn_info(conn_info):
        # Only the HostService stream id matters to this example.
        if conn_info.service_id == HOST_BROKER_ID:
            plugin_svc.set_host_addr(conn_info.network, conn_info.address)

    server = grpc.server(futures.ThreadPoolExecutor(max_workers=10))
    plugin_pb2_grpc.add_PluginServiceServicer_to_server(plugin_svc, server)
    grpc_broker_pb2_grpc.add_GRPCBrokerServicer_to_server(BrokerService(on_conn_info), server)

    # go-plugin's host pings grpc.health.v1.Health/Check for service
    # "plugin" during the handshake; report SERVING or the host hangs.
    health_svc = health.HealthServicer()
    health_pb2_grpc.add_HealthServicer_to_server(health_svc, server)
    health_svc.set("plugin", health_pb2.HealthCheckResponse.SERVING)

    server.add_insecure_port(f"unix:{socket_path}")
    server.start()
    log(f"listening on unix:{socket_path}")

    # 3. Print the go-plugin handshake line on stdout, then nothing else.
    #    Format (6 fields, no AutoMTLS, no multiplex):
    #      CORE-PROTOCOL-VERSION|APP-PROTOCOL-VERSION|NETWORK|ADDR|PROTOCOL|TLS-CERT
    #    The trailing TLS-CERT field is EMPTY because railyard does not use
    #    AutoMTLS. PROTOCOL is "grpc".
    handshake = "|".join(
        [
            str(CORE_PROTOCOL_VERSION),
            str(APP_PROTOCOL_VERSION),
            "unix",
            socket_path,
            "grpc",
            "",  # empty TLS cert
        ]
    )
    sys.stdout.write(handshake + "\n")
    sys.stdout.flush()

    # 4. Block for the lifetime of the process. The host drives Stop and
    #    then closes the connection / sends SIGTERM.
    try:
        while True:
            time.sleep(0.5)
    except KeyboardInterrupt:
        pass
    finally:
        server.stop(grace=1)
        if os.path.exists(socket_path):
            os.remove(socket_path)


if __name__ == "__main__":
    main()
