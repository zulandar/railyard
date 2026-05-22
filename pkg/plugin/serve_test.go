package plugin

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"

	protov1 "github.com/zulandar/railyard/pkg/plugin/proto/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// fakePlugin is a controllable [Plugin] for adapter unit tests. Each
// hook records its invocation and may be overridden per-test to inject
// panics, errors, or capture inputs.
type fakePlugin struct {
	name string

	initCalls  atomic.Int32
	startCalls atomic.Int32
	stopCalls  atomic.Int32

	initFn  func(ctx context.Context, h Host) error
	startFn func(ctx context.Context) error
	stopFn  func(ctx context.Context) error

	lastHost Host
}

func (f *fakePlugin) Name() string { return f.name }
func (f *fakePlugin) Init(ctx context.Context, h Host) error {
	f.initCalls.Add(1)
	f.lastHost = h
	if f.initFn != nil {
		return f.initFn(ctx, h)
	}
	return nil
}
func (f *fakePlugin) Start(ctx context.Context) error {
	f.startCalls.Add(1)
	if f.startFn != nil {
		return f.startFn(ctx)
	}
	return nil
}
func (f *fakePlugin) Stop(ctx context.Context) error {
	f.stopCalls.Add(1)
	if f.stopFn != nil {
		return f.stopFn(ctx)
	}
	return nil
}

// newTestAdapter wires a pluginServiceAdapter with an in-memory
// hostClient. No gRPC dial happens — the hostClient's HostServiceClient
// is left nil because the lifecycle tests do not exercise any host RPC.
func newTestAdapter(impl Plugin) (*pluginServiceAdapter, *hostClient) {
	hc := &hostClient{
		pluginName:      impl.Name(),
		rootCtx:         context.Background(),
		commandHandlers: make(map[string]CommandHandler),
	}
	adapter := newPluginServiceAdapter(impl, func(_ context.Context) (*hostClient, error) {
		return hc, nil
	})
	return adapter, hc
}

// TestAdapterLifecycle exercises the Init / Start / Stop threading
// through the PluginService adapter against a controllable fake plugin.
// It is the canonical "this wiring works" smoke test for the SDK.
func TestAdapterLifecycle(t *testing.T) {
	t.Parallel()

	impl := &fakePlugin{name: "lifecycle-test"}
	adapter, hc := newTestAdapter(impl)

	// Init must hand the user the hostClient that the adapter
	// constructed, and must be idempotent under the sync.Once guard.
	initReq := &protov1.InitRequest{
		PluginName: "lifecycle-test",
		Capabilities: &protov1.Capabilities{
			SubscribeEvents: []string{string(CarCreated)},
			ProvideCommands: []*protov1.CommandSchema{
				{Name: "echo"},
			},
		},
	}
	resp, err := adapter.Init(context.Background(), initReq)
	if err != nil {
		t.Fatalf("Init returned error: %v", err)
	}
	if resp == nil {
		t.Fatal("Init returned nil response")
	}
	if got, want := resp.AllowedEvents, []string{string(CarCreated)}; !reflect.DeepEqual(got, want) {
		t.Errorf("allowed_events = %v, want %v", got, want)
	}
	if got, want := resp.AllowedCommands, []string{"echo"}; !reflect.DeepEqual(got, want) {
		t.Errorf("allowed_commands = %v, want %v", got, want)
	}
	if impl.initCalls.Load() != 1 {
		t.Errorf("Init not called exactly once; got %d", impl.initCalls.Load())
	}
	if impl.lastHost != hc {
		t.Errorf("user Init received host %p, want hostClient %p", impl.lastHost, hc)
	}

	// Second Init must not re-run the user's Init.
	_, err = adapter.Init(context.Background(), initReq)
	if err != nil {
		t.Fatalf("second Init returned error: %v", err)
	}
	if impl.initCalls.Load() != 1 {
		t.Errorf("Init re-ran user method on duplicate RPC; calls=%d", impl.initCalls.Load())
	}

	// Start delegates and returns the no-op StartResponse on success.
	if _, err := adapter.Start(context.Background(), &protov1.StartRequest{}); err != nil {
		t.Fatalf("Start error: %v", err)
	}
	if impl.startCalls.Load() != 1 {
		t.Errorf("Start not called; got %d", impl.startCalls.Load())
	}

	// Stop completes the lifecycle.
	if _, err := adapter.Stop(context.Background(), &protov1.StopRequest{DrainTimeoutMs: 5000}); err != nil {
		t.Fatalf("Stop error: %v", err)
	}
	if impl.stopCalls.Load() != 1 {
		t.Errorf("Stop not called; got %d", impl.stopCalls.Load())
	}
}

// TestAdapterInitError verifies that a user Init returning an error is
// surfaced as a gRPC FailedPrecondition status, which the host treats
// as "skip this plugin" per the proto contract.
func TestAdapterInitError(t *testing.T) {
	t.Parallel()

	impl := &fakePlugin{
		name: "init-error",
		initFn: func(_ context.Context, _ Host) error {
			return errors.New("bad config")
		},
	}
	adapter, _ := newTestAdapter(impl)
	_, err := adapter.Init(context.Background(), &protov1.InitRequest{PluginName: "init-error"})
	if err == nil {
		t.Fatal("expected error from Init, got nil")
	}
	if s, ok := status.FromError(err); !ok || s.Code() != codes.FailedPrecondition {
		t.Fatalf("Init error code = %v, want FailedPrecondition", err)
	}
}

// TestAdapterPanicRecovery verifies a panicking user Init is recovered,
// the configured onFatal hook is invoked (so production code would
// os.Exit(1)), and the host receives a gRPC Internal error rather than
// a connection drop.
func TestAdapterPanicRecovery(t *testing.T) {
	t.Parallel()

	impl := &fakePlugin{
		name: "panicker",
		initFn: func(_ context.Context, _ Host) error {
			panic("boom")
		},
	}
	adapter, _ := newTestAdapter(impl)

	var fatalCalled atomic.Int32
	var capturedRPC string
	var mu sync.Mutex
	adapter.onFatal = func(rpc string, _ any, _ []byte) {
		mu.Lock()
		capturedRPC = rpc
		mu.Unlock()
		fatalCalled.Add(1)
	}

	_, err := adapter.Init(context.Background(), &protov1.InitRequest{PluginName: "panicker"})
	if err == nil {
		t.Fatal("expected error from panicking Init, got nil")
	}
	if fatalCalled.Load() != 1 {
		t.Fatalf("onFatal invoked %d times, want 1", fatalCalled.Load())
	}
	mu.Lock()
	got := capturedRPC
	mu.Unlock()
	if got != "Init" {
		t.Errorf("onFatal rpc = %q, want %q", got, "Init")
	}
}

// TestCommandArgsStructRoundTrip exercises the bidirectional conversion
// between CommandArgs and *structpb.Struct used on the wire for both
// HandleCommand and DispatchCommand. This is the conversion the spec
// calls out explicitly as needing a unit test.
func TestCommandArgsStructRoundTrip(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		in   CommandArgs
	}{
		{
			name: "nil-args",
			in:   nil,
		},
		{
			name: "empty-args",
			in:   CommandArgs{},
		},
		{
			name: "primitives",
			in: CommandArgs{
				"str":  "hello",
				"flag": true,
				// JSON has no int — structpb stores all numbers as
				// float64. We use a float here so the round-trip is
				// exact.
				"count": float64(7),
			},
		},
		{
			name: "nested",
			in: CommandArgs{
				"outer": map[string]any{
					"inner": "value",
					"list":  []any{"a", "b"},
				},
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			pb, err := commandArgsToStruct(tc.in)
			if err != nil {
				t.Fatalf("commandArgsToStruct: %v", err)
			}
			// Nil args MUST encode as a nil struct to preserve the
			// "no args present" wire representation.
			if tc.in == nil {
				if pb != nil {
					t.Fatalf("nil CommandArgs should encode as nil *Struct, got %v", pb)
				}
				if got := structToMap(pb); got != nil {
					t.Fatalf("nil *Struct should decode to nil map, got %v", got)
				}
				return
			}
			got := structToMap(pb)
			if !reflect.DeepEqual(got, map[string]any(tc.in)) {
				t.Errorf("round-trip mismatch:\n  in:  %v\n  out: %v", tc.in, got)
			}
		})
	}
}

// TestHostClientCommandRegistry exercises RegisterCommand and the
// lookupCommand helper used by HandleCommand. This is the in-process
// half of the spec's "the impl registers handlers via the Host
// adapter's RegisterCommand, which the adapter holds in-memory" path.
func TestHostClientCommandRegistry(t *testing.T) {
	t.Parallel()

	hc := &hostClient{
		commandHandlers: make(map[string]CommandHandler),
	}
	called := atomic.Int32{}
	handler := func(_ context.Context, _ CommandArgs) (CommandResult, error) {
		called.Add(1)
		return CommandResult{Success: true}, nil
	}

	if err := hc.RegisterCommand("ping", handler); err != nil {
		t.Fatalf("RegisterCommand: %v", err)
	}
	if err := hc.RegisterCommand("ping", handler); err == nil {
		t.Fatal("duplicate RegisterCommand should error")
	}
	if err := hc.RegisterCommand("", handler); err == nil {
		t.Fatal("empty name should error")
	}
	if err := hc.RegisterCommand("nilh", nil); err == nil {
		t.Fatal("nil handler should error")
	}

	got, ok := hc.lookupCommand("ping")
	if !ok {
		t.Fatal("lookupCommand: ping not found after register")
	}
	if got == nil {
		t.Fatal("lookupCommand returned nil handler")
	}
	if _, missing := hc.lookupCommand("nope"); missing {
		t.Fatal("lookupCommand returned ok for unregistered name")
	}
	if _, err := got(context.Background(), nil); err != nil {
		t.Fatalf("handler invocation: %v", err)
	}
	if called.Load() != 1 {
		t.Errorf("handler called %d times, want 1", called.Load())
	}
}

// TestHandshakeConstants pins the public handshake values so changes
// are caught by the test suite rather than appearing silently in
// downstream host implementations.
func TestHandshakeConstants(t *testing.T) {
	t.Parallel()

	if ProtocolVersion != 1 {
		t.Errorf("ProtocolVersion = %d, want 1", ProtocolVersion)
	}
	if MagicCookieKey == "" {
		t.Error("MagicCookieKey must not be empty")
	}
	if MagicCookieValue == "" {
		t.Error("MagicCookieValue must not be empty")
	}
	if HandshakeConfig.MagicCookieKey != MagicCookieKey {
		t.Errorf("HandshakeConfig.MagicCookieKey = %q, want %q", HandshakeConfig.MagicCookieKey, MagicCookieKey)
	}
	if HandshakeConfig.MagicCookieValue != MagicCookieValue {
		t.Errorf("HandshakeConfig.MagicCookieValue = %q, want %q", HandshakeConfig.MagicCookieValue, MagicCookieValue)
	}
	if HandshakeConfig.ProtocolVersion != uint(ProtocolVersion) {
		t.Errorf("HandshakeConfig.ProtocolVersion = %d, want %d", HandshakeConfig.ProtocolVersion, ProtocolVersion)
	}
}
