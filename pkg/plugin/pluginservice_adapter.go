package plugin

import (
	"context"
	"fmt"
	"runtime/debug"
	"sync"

	protov1 "github.com/zulandar/railyard/pkg/plugin/proto/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// pluginServiceAdapter is the in-plugin gRPC server-side implementation
// of protov1.PluginServiceServer. It translates incoming RPCs from the
// host into method calls on the user's Plugin impl, recovering from
// panics so a misbehaving user method cannot crash the gRPC server in
// an unrecoverable state.
//
// One adapter is constructed per plugin process by [Serve]. It holds a
// reference to the hostClient so HandleCommand can look up the
// in-process command registry the user populated during Init/Start via
// Host.RegisterCommand.
type pluginServiceAdapter struct {
	protov1.UnimplementedPluginServiceServer

	impl     Plugin
	hostFn   func(ctx context.Context) (*hostClient, error)
	initOnce sync.Once
	initErr  error

	// hc is the resolved hostClient set after the first successful
	// Init. HandleCommand consults hc.lookupCommand.
	mu sync.Mutex
	hc *hostClient

	// onFatal is invoked when a panic inside a user method is
	// recovered. The default behaviour wired in Serve is os.Exit(1) so
	// the host's crash budget can count the exit. It is a field rather
	// than a hard-coded call so tests can observe the panic path
	// without killing the test process.
	onFatal func(rpc string, recovered any, stack []byte)
}

// newPluginServiceAdapter constructs the adapter. hostFn is invoked on
// Init to construct (or retrieve) the hostClient used to back the
// user's Plugin.Init call. Splitting it from the constructor lets tests
// inject a synthetic hostClient without a real gRPC dial.
func newPluginServiceAdapter(impl Plugin, hostFn func(ctx context.Context) (*hostClient, error)) *pluginServiceAdapter {
	return &pluginServiceAdapter{
		impl:   impl,
		hostFn: hostFn,
	}
}

// Init handles the host's lifecycle Init RPC.
func (a *pluginServiceAdapter) Init(ctx context.Context, req *protov1.InitRequest) (*protov1.InitResponse, error) {
	var initErr error
	a.initOnce.Do(func() {
		hc, err := a.hostFn(ctx)
		if err != nil {
			a.initErr = err
			return
		}
		a.mu.Lock()
		a.hc = hc
		a.mu.Unlock()
		initErr = a.callUser("Init", func() error {
			return a.impl.Init(ctx, hc)
		})
		a.initErr = initErr
	})
	if a.initErr != nil {
		// Per spec: errors during Init should surface as a gRPC error.
		// The host treats a non-OK Init as "skip this plugin" — we use
		// FailedPrecondition rather than Internal to communicate that
		// the plugin is well-formed but its Init signalled "do not run".
		return nil, status.Error(codes.FailedPrecondition, fmt.Sprintf("plugin Init failed: %v", a.initErr))
	}
	// Advertise the plugin's capability wish-list to the host. The host
	// applies its allow-list (railyard-fll.4) and stores its filtered
	// view internally; the response we hand back here describes what the
	// plugin TRIED to register so the host can compute denials.
	resp := &protov1.InitResponse{}
	a.mu.Lock()
	hc := a.hc
	a.mu.Unlock()
	if hc != nil {
		resp.AllowedEvents = hc.advertisedTopics()
		resp.AllowedCommands = hc.advertisedCommandNames()
	}
	// Defensive fallback: if the host sent its own declared wish-list in
	// the request and the plugin didn't subscribe to anything in Init,
	// echo the host-provided set. This keeps older plugin authors that
	// rely on host-side advertisement working until they migrate.
	if len(resp.AllowedEvents) == 0 && req != nil && req.Capabilities != nil {
		resp.AllowedEvents = append(resp.AllowedEvents, req.Capabilities.SubscribeEvents...)
	}
	if len(resp.AllowedCommands) == 0 && req != nil && req.Capabilities != nil {
		for _, cmd := range req.Capabilities.ProvideCommands {
			if cmd == nil {
				continue
			}
			resp.AllowedCommands = append(resp.AllowedCommands, cmd.Name)
		}
	}
	return resp, nil
}

// Start handles the lifecycle Start RPC.
func (a *pluginServiceAdapter) Start(ctx context.Context, _ *protov1.StartRequest) (*protov1.StartResponse, error) {
	if err := a.callUser("Start", func() error {
		return a.impl.Start(ctx)
	}); err != nil {
		return nil, status.Error(codes.Internal, fmt.Sprintf("plugin Start failed: %v", err))
	}
	return &protov1.StartResponse{}, nil
}

// Stop handles the lifecycle Stop RPC.
func (a *pluginServiceAdapter) Stop(ctx context.Context, _ *protov1.StopRequest) (*protov1.StopResponse, error) {
	if err := a.callUser("Stop", func() error {
		return a.impl.Stop(ctx)
	}); err != nil {
		return nil, status.Error(codes.Internal, fmt.Sprintf("plugin Stop failed: %v", err))
	}
	return &protov1.StopResponse{}, nil
}

// HandleCommand routes an incoming command RPC to the in-process
// registry the plugin populated through Host.RegisterCommand.
func (a *pluginServiceAdapter) HandleCommand(ctx context.Context, req *protov1.HandleCommandRequest) (*protov1.HandleCommandResponse, error) {
	if req == nil || req.Name == "" {
		return nil, status.Error(codes.InvalidArgument, "command name is required")
	}
	a.mu.Lock()
	hc := a.hc
	a.mu.Unlock()
	if hc == nil {
		return nil, status.Error(codes.FailedPrecondition, "plugin not initialised")
	}
	handler, ok := hc.lookupCommand(req.Name)
	if !ok {
		return &protov1.HandleCommandResponse{
			Success: false,
			Error:   fmt.Sprintf("plugin: command %q not registered", req.Name),
		}, nil
	}
	args := CommandArgs{}
	if req.Args != nil {
		args = CommandArgs(req.Args.AsMap())
	}

	var (
		result    CommandResult
		callErr   error
		recovered any
		stack     []byte
	)
	func() {
		defer func() {
			if r := recover(); r != nil {
				recovered = r
				stack = debug.Stack()
			}
		}()
		result, callErr = handler(ctx, args)
	}()
	if recovered != nil {
		a.fireFatal("HandleCommand", recovered, stack)
		return nil, status.Errorf(codes.Internal, "plugin command panic: %v", recovered)
	}
	if callErr != nil {
		return &protov1.HandleCommandResponse{
			Success: false,
			Error:   callErr.Error(),
		}, nil
	}
	resp := &protov1.HandleCommandResponse{
		Success: result.Success,
		Error:   result.Error,
	}
	if result.Data != nil {
		dataStruct, err := commandArgsToStruct(CommandArgs(result.Data))
		if err != nil {
			return nil, status.Errorf(codes.Internal, "encoding command result: %v", err)
		}
		resp.Data = dataStruct
	}
	return resp, nil
}

// callUser invokes a user-impl method, converting a panic into an
// error and forwarding the recovery to onFatal so the process can exit
// non-zero.
func (a *pluginServiceAdapter) callUser(rpc string, fn func() error) (err error) {
	defer func() {
		if r := recover(); r != nil {
			stack := debug.Stack()
			a.fireFatal(rpc, r, stack)
			err = fmt.Errorf("panic in %s: %v", rpc, r)
		}
	}()
	return fn()
}

func (a *pluginServiceAdapter) fireFatal(rpc string, recovered any, stack []byte) {
	if a.onFatal != nil {
		a.onFatal(rpc, recovered, stack)
	}
}
