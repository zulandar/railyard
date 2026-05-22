package plugin

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"sync"
	"time"

	protov1 "github.com/zulandar/railyard/pkg/plugin/proto/v1"
	"google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/timestamppb"
	"gopkg.in/yaml.v3"
)

// hostClient is the in-plugin adapter that satisfies the [Host]
// interface by translating each method call into a gRPC RPC against the
// host process's HostService.
//
// One hostClient is constructed per plugin process during Init. It is
// safe for concurrent use; all underlying gRPC stubs are concurrency-
// safe.
type hostClient struct {
	pluginName string

	// hsc is the gRPC client stub for the host's HostService. It is the
	// single conduit for every Host method below.
	hsc protov1.HostServiceClient

	// rootCtx is a context cancelled when the plugin process is shutting
	// down. It is the parent of every long-lived background goroutine
	// (Subscribe stream reader, Logger forwarder) so that shutdown
	// reliably stops them.
	rootCtx context.Context

	// yardInfo is cached on first call. The proto contract guarantees it
	// is fixed for the lifetime of the host; caching avoids round-trips.
	yardInfoOnce sync.Once
	yardInfoVal  YardInfo
	yardInfoErr  error

	// commandHandlers holds the in-process registry of commands the
	// plugin has registered. It is consulted by the PluginService
	// adapter's HandleCommand handler. The host learns about commands
	// from the Init handshake's advertised capabilities; this map only
	// routes incoming HandleCommand RPCs back to the user impl.
	cmdMu           sync.RWMutex
	commandHandlers map[string]CommandHandler

	// subscribedTopics records every topic the plugin's user code has
	// subscribed to via [hostClient.Subscribe]. The SDK adapter reads
	// this set during the Init RPC response to advertise the plugin's
	// capability wish-list to the host (railyard-fll.4 allow-list flow).
	// Order is preserved insertion order so deterministic test output
	// is possible; duplicate Subscribe calls do not re-record.
	subMu              sync.Mutex
	subscribedTopics   []string
	subscribedTopicSet map[string]struct{}

	// logger is the slog.Logger handed to the plugin via Logger(). It
	// forwards records to the host through HostService.Log.
	logger *slog.Logger
}

// newHostClient constructs a fresh hostClient.
func newHostClient(pluginName string, hsc protov1.HostServiceClient, rootCtx context.Context) *hostClient {
	hc := &hostClient{
		pluginName:         pluginName,
		hsc:                hsc,
		rootCtx:            rootCtx,
		commandHandlers:    make(map[string]CommandHandler),
		subscribedTopicSet: make(map[string]struct{}),
	}
	hc.logger = slog.New(&hostLogHandler{
		hsc:        hsc,
		pluginName: pluginName,
		rootCtx:    rootCtx,
	}).With(slog.String("plugin", pluginName))
	return hc
}

// Config implements Host.Config.
func (h *hostClient) Config(name string) yaml.Node {
	ctx, cancel := context.WithTimeout(h.rootCtx, 5*time.Second)
	defer cancel()
	resp, err := h.hsc.Config(ctx, &protov1.ConfigRequest{Name: name})
	if err != nil || resp == nil || !resp.Present || len(resp.ConfigYaml) == 0 {
		return yaml.Node{}
	}
	var n yaml.Node
	if err := yaml.Unmarshal(resp.ConfigYaml, &n); err != nil {
		return yaml.Node{}
	}
	// yaml.Unmarshal returns a DocumentNode wrapping the actual content
	// node. Plugins expect the content node directly so node.Kind != 0
	// when a block is present.
	if n.Kind == yaml.DocumentNode && len(n.Content) > 0 {
		return *n.Content[0]
	}
	return n
}

// YardInfo implements Host.YardInfo. It memoises the result on first
// successful call.
func (h *hostClient) YardInfo() YardInfo {
	h.yardInfoOnce.Do(func() {
		ctx, cancel := context.WithTimeout(h.rootCtx, 5*time.Second)
		defer cancel()
		resp, err := h.hsc.YardInfo(ctx, &protov1.YardInfoRequest{})
		if err != nil || resp == nil {
			h.yardInfoErr = err
			return
		}
		h.yardInfoVal = yardInfoFromProto(resp)
	})
	return h.yardInfoVal
}

// Subscribe implements Host.Subscribe.
//
// Each topic the plugin's user code subscribes to is also recorded on
// the hostClient so the PluginService Init adapter can advertise the
// full subscription set to the host as the plugin's capability
// wish-list (railyard-fll.4 allow-list flow).
func (h *hostClient) Subscribe(topic EventType, handler EventHandler) Unsubscribe {
	if handler == nil {
		return func() {}
	}
	h.recordSubscribedTopic(string(topic))
	ctx, cancel := context.WithCancel(h.rootCtx)
	stream, err := h.hsc.Subscribe(ctx, &protov1.SubscribeRequest{Topics: []string{string(topic)}})
	if err != nil {
		cancel()
		h.logger.Warn("plugin: Subscribe failed", slog.String("topic", string(topic)), slog.String("err", err.Error()))
		return func() {}
	}
	var once sync.Once
	unsub := func() {
		once.Do(cancel)
	}
	go h.runSubscribeLoop(ctx, topic, stream, handler)
	return unsub
}

// recordSubscribedTopic appends topic to the in-process advertisement
// list, skipping duplicates. Called from Subscribe.
func (h *hostClient) recordSubscribedTopic(topic string) {
	if topic == "" {
		return
	}
	h.subMu.Lock()
	defer h.subMu.Unlock()
	if _, seen := h.subscribedTopicSet[topic]; seen {
		return
	}
	h.subscribedTopicSet[topic] = struct{}{}
	h.subscribedTopics = append(h.subscribedTopics, topic)
}

// advertisedTopics returns a snapshot of every topic the plugin has
// subscribed to so far. Consumed by the PluginService Init adapter.
func (h *hostClient) advertisedTopics() []string {
	h.subMu.Lock()
	defer h.subMu.Unlock()
	return append([]string(nil), h.subscribedTopics...)
}

// advertisedCommandNames returns a snapshot of every command name the
// plugin has registered so far. Consumed by the PluginService Init
// adapter.
func (h *hostClient) advertisedCommandNames() []string {
	h.cmdMu.RLock()
	defer h.cmdMu.RUnlock()
	out := make([]string, 0, len(h.commandHandlers))
	for name := range h.commandHandlers {
		out = append(out, name)
	}
	return out
}

func (h *hostClient) runSubscribeLoop(ctx context.Context, topic EventType, stream protov1.HostService_SubscribeClient, handler EventHandler) {
	defer func() {
		if r := recover(); r != nil {
			h.logger.Error("plugin: subscribe handler panic recovered",
				slog.String("topic", string(topic)),
				slog.Any("panic", r),
			)
		}
	}()
	for {
		ev, err := stream.Recv()
		if err != nil {
			if errors.Is(err, io.EOF) || ctx.Err() != nil {
				return
			}
			h.logger.Warn("plugin: subscribe stream closed",
				slog.String("topic", string(topic)),
				slog.String("err", err.Error()),
			)
			return
		}
		decoded, decErr := decodeEvent(ev)
		if decErr != nil {
			h.logger.Warn("plugin: dropping undecodable event",
				slog.String("topic", string(topic)),
				slog.String("err", decErr.Error()),
			)
			continue
		}
		// Run inside an inline recover so a panicking handler does not
		// terminate the goroutine — the SDK is the sole owner of this
		// goroutine's lifetime.
		func() {
			defer func() {
				if r := recover(); r != nil {
					h.logger.Error("plugin: subscriber panic recovered",
						slog.String("topic", string(topic)),
						slog.Any("panic", r),
					)
				}
			}()
			handler(decoded.topic, decoded.payload)
		}()
	}
}

// Snapshot implements Host.Snapshot.
func (h *hostClient) Snapshot(ctx context.Context) (*Snapshot, error) {
	resp, err := h.hsc.Snapshot(ctx, &protov1.SnapshotRequest{})
	if err != nil {
		return nil, err
	}
	if resp == nil || resp.Snapshot == nil {
		return nil, errors.New("plugin: empty Snapshot response from host")
	}
	snap := snapshotFromProto(resp.Snapshot)
	return &snap, nil
}

// RegisterCommand implements Host.RegisterCommand. As documented in the
// spec this is an in-process registration: the host learns about the
// command name from the Init capability advertisement, then routes
// incoming HandleCommand RPCs back here.
func (h *hostClient) RegisterCommand(name string, handler CommandHandler) error {
	if name == "" {
		return errors.New("plugin: RegisterCommand: name must not be empty")
	}
	if handler == nil {
		return errors.New("plugin: RegisterCommand: handler must not be nil")
	}
	h.cmdMu.Lock()
	defer h.cmdMu.Unlock()
	if _, exists := h.commandHandlers[name]; exists {
		return fmt.Errorf("plugin: RegisterCommand: %q already registered", name)
	}
	h.commandHandlers[name] = handler
	return nil
}

// lookupCommand returns the handler previously registered under name.
// It is consumed by the PluginService adapter on incoming HandleCommand.
func (h *hostClient) lookupCommand(name string) (CommandHandler, bool) {
	h.cmdMu.RLock()
	defer h.cmdMu.RUnlock()
	handler, ok := h.commandHandlers[name]
	return handler, ok
}

// DispatchCommand implements Host.DispatchCommand.
func (h *hostClient) DispatchCommand(ctx context.Context, name string, args CommandArgs) (CommandResult, error) {
	argStruct, err := commandArgsToStruct(args)
	if err != nil {
		return CommandResult{Success: false, Error: err.Error()}, err
	}
	resp, err := h.hsc.DispatchCommand(ctx, &protov1.DispatchCommandRequest{
		Name: name,
		Args: argStruct,
	})
	if err != nil {
		return CommandResult{Success: false, Error: err.Error()}, err
	}
	if resp == nil {
		return CommandResult{Success: false, Error: "plugin: nil DispatchCommand response"}, errors.New("plugin: nil DispatchCommand response")
	}
	return CommandResult{
		Success: resp.Success,
		Error:   resp.Error,
		Data:    structToMap(resp.Data),
	}, nil
}

// RunDaemon implements Host.RunDaemon.
//
// Deprecated: in the subprocess plugin model, a plugin already owns its
// own process — there is no host-managed daemon set to register into.
// RunDaemon is preserved for source-compat with plugins authored against
// the in-process SDK and simply spawns the daemon as a regular
// goroutine with panic recovery. Callers should migrate to plain
// goroutines directly. See railyard-fll.8 for the cleanup sweep.
func (h *hostClient) RunDaemon(name string, fn DaemonFunc) {
	if fn == nil {
		return
	}
	h.logger.Warn(
		"plugin: Host.RunDaemon is deprecated under the subprocess plugin model; spawn a goroutine directly",
		slog.String("daemon", name),
	)
	go func() {
		defer func() {
			if r := recover(); r != nil {
				h.logger.Error("plugin: daemon panic recovered",
					slog.String("daemon", name),
					slog.Any("panic", r),
				)
			}
		}()
		if err := fn(h.rootCtx); err != nil && !errors.Is(err, context.Canceled) {
			h.logger.Error("plugin: daemon returned error",
				slog.String("daemon", name),
				slog.String("err", err.Error()),
			)
		}
	}()
}

// Logger implements Host.Logger.
func (h *hostClient) Logger() *slog.Logger {
	return h.logger
}

// commandArgsToStruct converts a CommandArgs (map[string]any) into a
// *structpb.Struct on the wire. Nil maps round-trip as nil.
func commandArgsToStruct(args CommandArgs) (*structpb.Struct, error) {
	if args == nil {
		return nil, nil
	}
	s, err := structpb.NewStruct(map[string]any(args))
	if err != nil {
		return nil, fmt.Errorf("plugin: cannot encode command args: %w", err)
	}
	return s, nil
}

// structToMap converts a wire *structpb.Struct to a map[string]any. A
// nil struct returns a nil map (not an empty one), so callers can
// distinguish "no data" from "empty data" if they need to.
func structToMap(s *structpb.Struct) map[string]any {
	if s == nil {
		return nil
	}
	return s.AsMap()
}

// hostLogHandler is the slog.Handler that ships records to the host via
// HostService.Log. It is intentionally cheap: no buffering, no batching.
// The host applies the "plugin=<name>" attribute on its side already,
// and the SDK's slog.Logger.With also includes it for completeness so
// records survive even if a future host strips its own attribute.
type hostLogHandler struct {
	hsc        protov1.HostServiceClient
	pluginName string
	rootCtx    context.Context

	// attrs accumulates With/WithGroup additions in flattened form. New
	// handler chains share the underlying slice prefix.
	attrs []slog.Attr
	group string
}

func (h *hostLogHandler) Enabled(_ context.Context, _ slog.Level) bool { return true }

func (h *hostLogHandler) Handle(ctx context.Context, r slog.Record) error {
	attrs := make(map[string]string, len(h.attrs)+r.NumAttrs())
	for _, a := range h.attrs {
		flattenAttr(attrs, h.group, a)
	}
	r.Attrs(func(a slog.Attr) bool {
		flattenAttr(attrs, h.group, a)
		return true
	})

	req := &protov1.LogRequest{
		Level:     int32(r.Level),
		EmittedAt: timestamppb.New(r.Time),
		Message:   r.Message,
		Attrs:     attrs,
	}
	// Use a short deadline so a slow host can't wedge logging.
	logCtx := h.rootCtx
	if ctx != nil {
		logCtx = ctx
	}
	logCtx, cancel := context.WithTimeout(logCtx, 2*time.Second)
	defer cancel()
	_, _ = h.hsc.Log(logCtx, req)
	return nil
}

func (h *hostLogHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	newAttrs := make([]slog.Attr, 0, len(h.attrs)+len(attrs))
	newAttrs = append(newAttrs, h.attrs...)
	newAttrs = append(newAttrs, attrs...)
	return &hostLogHandler{
		hsc:        h.hsc,
		pluginName: h.pluginName,
		rootCtx:    h.rootCtx,
		attrs:      newAttrs,
		group:      h.group,
	}
}

func (h *hostLogHandler) WithGroup(name string) slog.Handler {
	if name == "" {
		return h
	}
	joined := name
	if h.group != "" {
		joined = h.group + "." + name
	}
	return &hostLogHandler{
		hsc:        h.hsc,
		pluginName: h.pluginName,
		rootCtx:    h.rootCtx,
		attrs:      h.attrs,
		group:      joined,
	}
}

func flattenAttr(out map[string]string, prefix string, a slog.Attr) {
	key := a.Key
	if prefix != "" && key != "" {
		key = prefix + "." + key
	}
	v := a.Value.Resolve()
	if v.Kind() == slog.KindGroup {
		for _, sub := range v.Group() {
			flattenAttr(out, key, sub)
		}
		return
	}
	out[key] = v.String()
}
