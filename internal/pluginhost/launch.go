package pluginhost

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"

	"github.com/hashicorp/go-hclog"
	goplugin "github.com/hashicorp/go-plugin"
	"google.golang.org/grpc"

	"github.com/zulandar/railyard/pkg/plugin"
	protov1 "github.com/zulandar/railyard/pkg/plugin/proto/v1"
)

// hostGRPCPlugin is the host-side adapter that captures the go-plugin
// broker on first call so the host can serve HostService back to the
// plugin on the well-known [plugin.HostBrokerID] stream id.
//
// We embed NetRPCUnsupportedPlugin so we only need to satisfy
// GRPCPlugin's two methods.
type hostGRPCPlugin struct {
	goplugin.NetRPCUnsupportedPlugin

	// server is the host's HostService implementation. It is registered
	// onto every broker-served gRPC server the plugin dials.
	server protov1.HostServiceServer

	// brokerCh receives the *GRPCBroker on the first GRPCClient
	// callback. Buffered length 1 — the channel is read once during
	// launchPlugin and discarded.
	brokerCh chan *goplugin.GRPCBroker
}

// GRPCServer is unused on the host side. Returning nil satisfies the
// interface; the plugin's own SDK serves PluginService on its side.
func (p *hostGRPCPlugin) GRPCServer(_ *goplugin.GRPCBroker, _ *grpc.Server) error {
	return nil
}

// GRPCClient is invoked by go-plugin on Dispense. We receive the broker
// here and immediately hand it back via brokerCh so launchPlugin can
// spin up the HostService server on HostBrokerID. The returned interface
// is the PluginService client stub the host will use for the plugin's
// lifecycle RPCs.
func (p *hostGRPCPlugin) GRPCClient(_ context.Context, broker *goplugin.GRPCBroker, conn *grpc.ClientConn) (any, error) {
	// Non-blocking send: subsequent Dispense calls (which don't happen
	// in our wiring) won't get stuck.
	select {
	case p.brokerCh <- broker:
	default:
	}
	return protov1.NewPluginServiceClient(conn), nil
}

// Compile-time assertion that the type satisfies go-plugin's Plugin and
// GRPCPlugin interfaces.
var _ goplugin.Plugin = (*hostGRPCPlugin)(nil)
var _ goplugin.GRPCPlugin = (*hostGRPCPlugin)(nil)

// launchPluginOnce spawns one plugin binary as a go-plugin subprocess,
// waits for the gRPC handshake, performs the SO_PEERCRED verification,
// and starts serving HostService on the broker's HostBrokerID stream.
// It returns a fully-wired [*launchedPlugin] on success.
//
// Any error along the way causes the subprocess to be killed and the
// socket file removed before the function returns.
//
// "Once" in the name is load-bearing — this function performs a SINGLE
// launch attempt. The restart-on-crash supervision wrapper is in
// [Host.superviseLaunch] (railyard-fll.6). Existing callers that want a
// single-shot launch (initial discovery walk) get one launch + the
// supervisor that owns subsequent relaunches.
func (h *Host) launchPluginOnce(ctx context.Context, c candidate, logger *slog.Logger) (*launchedPlugin, error) {
	if c.path == "" {
		return nil, errors.New("pluginhost: candidate path is empty")
	}

	// Resolve the per-plugin socket dir. go-plugin auto-creates a
	// subdirectory of our choosing and binds a `plugin*` file inside;
	// the resulting path satisfies the spec's 0600/owner-only policy.
	sockPath, err := resolveSocketPath(c.name)
	if err != nil {
		return nil, fmt.Errorf("resolve socket dir: %w", err)
	}
	// We hand go-plugin the parent dir; the actual socket filename is
	// chosen by the library. We record the dir for cleanup.
	socketDir := filepath.Dir(sockPath)
	if err := os.MkdirAll(socketDir, socketDirPerm); err != nil {
		return nil, fmt.Errorf("ensure socket dir %s: %w", socketDir, err)
	}

	brokerCh := make(chan *goplugin.GRPCBroker, 1)
	hostSrv := newHostService(h, c.name)
	gp := &hostGRPCPlugin{
		server:   hostSrv,
		brokerCh: brokerCh,
	}

	clientCfg := &goplugin.ClientConfig{
		HandshakeConfig:  plugin.HandshakeConfig,
		Plugins:          goplugin.PluginSet{plugin.PluginSetKey: gp},
		Cmd:              exec.Command(c.path), //nolint:gosec // intentional: launching configured plugin binary
		AllowedProtocols: []goplugin.Protocol{goplugin.ProtocolGRPC},
		Logger:           quietHCLog(),
		UnixSocketConfig: &goplugin.UnixSocketConfig{TempDir: socketDir},
	}

	client := goplugin.NewClient(clientCfg)

	// Bring the subprocess up. This blocks until the handshake succeeds
	// or the parent context is cancelled.
	rpcClient, err := client.Client()
	if err != nil {
		client.Kill()
		return nil, fmt.Errorf("go-plugin handshake failed: %w", err)
	}

	// Dispense once to receive the broker via the GRPCClient callback.
	raw, err := rpcClient.Dispense(plugin.PluginSetKey)
	if err != nil {
		client.Kill()
		return nil, fmt.Errorf("dispense: %w", err)
	}
	pluginRPC, ok := raw.(protov1.PluginServiceClient)
	if !ok {
		client.Kill()
		return nil, fmt.Errorf("dispense returned unexpected type %T", raw)
	}

	var broker *goplugin.GRPCBroker
	select {
	case broker = <-brokerCh:
	case <-ctx.Done():
		client.Kill()
		return nil, ctx.Err()
	}
	if broker == nil {
		client.Kill()
		return nil, errors.New("pluginhost: nil broker after dispense")
	}

	// Resolve the actual socket the go-plugin library bound to.
	rc := client.ReattachConfig()
	actualSocket := ""
	pid := 0
	if rc != nil {
		if rc.Addr != nil {
			actualSocket = rc.Addr.String()
		}
		pid = rc.Pid
	}

	// SO_PEERCRED verification on Linux. On other platforms we log a
	// DEBUG and trust the launched pid.
	if peerCredSupported() {
		if err := verifyPeerCredFor(actualSocket, pid, logger); err != nil {
			client.Kill()
			removeSocket(actualSocket)
			return nil, fmt.Errorf("SO_PEERCRED: %w", err)
		}
	} else {
		logger.Debug("pluginhost: SO_PEERCRED check skipped (non-Linux)",
			slog.String("goos", runtime.GOOS),
			slog.Int("pid", pid),
		)
	}

	// Serve HostService on the well-known broker stream id. The plugin
	// SDK's resolveHostClient dials this id during user-Init.
	go broker.AcceptAndServe(plugin.HostBrokerID, func(_ []grpc.ServerOption) *grpc.Server {
		s := grpc.NewServer()
		protov1.RegisterHostServiceServer(s, hostSrv)
		return s
	})

	lp := &launchedPlugin{
		name:       c.name,
		path:       c.path,
		socketPath: actualSocket,
		client:     client,
		pluginRPC:  pluginRPC,
		pid:        pid,
		logger:     logger,
	}
	return lp, nil
}

// verifyPeerCredFor opens a short-lived UDS connection to socketPath and
// verifies that the connected peer's pid matches expectedPid AND its uid
// matches the railyard process uid. Mismatch returns an error.
//
// We open a second conn purely for the SO_PEERCRED probe. The plugin
// process accepts the conn, sees it does not speak any of its services,
// and closes it — short and harmless.
func verifyPeerCredFor(socketPath string, expectedPid int, logger *slog.Logger) error {
	if socketPath == "" {
		return errors.New("empty socket path")
	}
	conn, err := net.Dial("unix", socketPath)
	if err != nil {
		return fmt.Errorf("dial %s: %w", socketPath, err)
	}
	defer func() { _ = conn.Close() }()

	pid, uid, err := readPeerCred(conn)
	if err != nil {
		return err
	}
	hostUID := uint32(os.Getuid())
	if uid != hostUID {
		return fmt.Errorf("peer uid=%d != host uid=%d", uid, hostUID)
	}
	if expectedPid > 0 && int32(expectedPid) != pid {
		return fmt.Errorf("peer pid=%d != launched pid=%d", pid, expectedPid)
	}
	logger.Debug("pluginhost: SO_PEERCRED verified",
		slog.Int("pid", int(pid)),
		slog.Int("uid", int(uid)),
	)
	return nil
}

// quietHCLog returns an hclog.Logger that silently discards every
// record. go-plugin demands an hclog instance; railyard does not want
// the library's TRACE-level chatter forwarded into the host's slog.
// Important events the host cares about (handshake errors, plugin exits)
// are surfaced through the client's return values, not the logger.
func quietHCLog() hclog.Logger {
	return hclog.New(&hclog.LoggerOptions{
		Output: io.Discard,
		Level:  hclog.Off,
		Name:   "plugin",
	})
}
