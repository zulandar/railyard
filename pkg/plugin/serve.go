package plugin

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"sync"
	"syscall"

	goplugin "github.com/hashicorp/go-plugin"
	protov1 "github.com/zulandar/railyard/pkg/plugin/proto/v1"
	"google.golang.org/grpc"
)

// Serve runs the plugin as a HashiCorp go-plugin subprocess.
//
// A plugin's main package should be:
//
//	func main() {
//	    plugin.Serve(&MyPlugin{})
//	}
//
// Serve performs the magic-cookie handshake on stdin/stdout, listens on
// the Unix socket provided by the host, exposes a PluginService gRPC
// server backed by impl, and blocks until the host requests shutdown.
// Signals (SIGINT/SIGTERM) cancel the root context, after which Serve
// drains active gRPC handlers and returns.
//
// Panics inside impl methods are recovered, logged via the host's
// Logger(), and cause Serve to exit non-zero so the host's crash budget
// can count the exit. Any error from go-plugin's own machinery is
// logged to stderr and also exits non-zero.
//
// Serve does not return on success — it blocks for the lifetime of the
// plugin process. Callers that need a non-blocking entrypoint should
// build their own wiring using the lower-level helpers in this package.
func Serve(impl Plugin) {
	if impl == nil {
		fmt.Fprintln(os.Stderr, "plugin.Serve: impl must not be nil")
		os.Exit(2)
	}

	rootCtx, rootCancel := context.WithCancel(context.Background())
	defer rootCancel()

	// Translate process-level signals into context cancellation. This
	// matches the contract documented on the Serve godoc: SIGINT and
	// SIGTERM cancel the root context, after which gRPC drains.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		rootCancel()
	}()
	defer signal.Stop(sigCh)

	// hostClient is built lazily inside Init, because it needs the
	// broker handed to the GRPCServer hook before it can dial the host.
	// servePlugin closes over a shared slot the GRPCServer callback
	// fills in, and the adapter's hostFn reads from the same slot.
	sp := &servePlugin{
		impl:    impl,
		rootCtx: rootCtx,
	}
	sp.adapter = newPluginServiceAdapter(impl, sp.resolveHostClient)
	sp.adapter.onFatal = func(rpc string, recovered any, stack []byte) {
		// Best-effort log before exit. Use the configured logger if it
		// was wired through hostClient already; otherwise fall back to
		// stderr.
		if logger := sp.currentLogger(); logger != nil {
			logger.Error("plugin: user method panicked, exiting 1",
				slog.String("rpc", rpc),
				slog.Any("panic", recovered),
				slog.String("stack", string(stack)),
			)
		} else {
			fmt.Fprintf(os.Stderr, "plugin: %s panicked: %v\n%s\n", rpc, recovered, stack)
		}
		// Exit asynchronously so the panicked RPC can still return a
		// gRPC error to the host before the process dies.
		go func() {
			os.Exit(1)
		}()
	}

	goplugin.Serve(&goplugin.ServeConfig{
		HandshakeConfig: HandshakeConfig,
		Plugins: goplugin.PluginSet{
			PluginSetKey: sp,
		},
		GRPCServer: goplugin.DefaultGRPCServer,
	})
}

// servePlugin is the goplugin.GRPCPlugin implementation. It is also the
// owner of the hostClient slot the adapter resolves through hostFn.
type servePlugin struct {
	goplugin.NetRPCUnsupportedPlugin

	impl    Plugin
	rootCtx context.Context
	adapter *pluginServiceAdapter

	// broker is the GRPCBroker handed to us by goplugin.GRPCServer. It
	// is the only way to dial back into the host process for the
	// HostService side-channel.
	mu     sync.Mutex
	broker *goplugin.GRPCBroker
	hc     *hostClient
}

// GRPCServer implements goplugin.GRPCPlugin.
func (s *servePlugin) GRPCServer(broker *goplugin.GRPCBroker, server *grpc.Server) error {
	s.mu.Lock()
	s.broker = broker
	s.mu.Unlock()

	protov1.RegisterPluginServiceServer(server, s.adapter)
	return nil
}

// GRPCClient implements goplugin.GRPCPlugin. The server side never uses
// this hook; it exists so the host's go-plugin Client can call into us.
// We return the adapter itself so a host that links the SDK can use a
// type-asserted form when running in-process for tests.
func (s *servePlugin) GRPCClient(_ context.Context, _ *goplugin.GRPCBroker, conn *grpc.ClientConn) (any, error) {
	return protov1.NewPluginServiceClient(conn), nil
}

// resolveHostClient is invoked by the adapter on the first Init RPC. It
// dials the host's HostService on the well-known broker stream ID and
// constructs the hostClient that backs the user's Host argument.
func (s *servePlugin) resolveHostClient(_ context.Context) (*hostClient, error) {
	s.mu.Lock()
	if s.hc != nil {
		hc := s.hc
		s.mu.Unlock()
		return hc, nil
	}
	broker := s.broker
	s.mu.Unlock()
	if broker == nil {
		return nil, fmt.Errorf("plugin.Serve: gRPC broker not yet attached; Init invoked before GRPCServer hook ran")
	}
	conn, err := broker.Dial(HostBrokerID)
	if err != nil {
		return nil, fmt.Errorf("plugin.Serve: dialling host broker id %d: %w", HostBrokerID, err)
	}
	pluginName := ""
	if s.impl != nil {
		pluginName = s.impl.Name()
	}
	hc := newHostClient(pluginName, protov1.NewHostServiceClient(conn), s.rootCtx)
	s.mu.Lock()
	s.hc = hc
	s.mu.Unlock()
	return hc, nil
}

func (s *servePlugin) currentLogger() *slog.Logger {
	s.mu.Lock()
	hc := s.hc
	s.mu.Unlock()
	if hc == nil {
		return nil
	}
	return hc.Logger()
}
