package plugin

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"sync"
	"time"

	protov1 "github.com/zulandar/railyard/pkg/plugin/proto/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
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
	//
	// commandSpecs holds the typed argument signature for commands
	// registered via [Host.RegisterCommandSpec] (railyard-77h.16), keyed
	// by command name. Commands registered via the bare RegisterCommand
	// carry no entry here. The Init adapter reads it via
	// advertisedCommandSpecs to fill InitResponse.command_specs so the
	// host can validate dispatched args before HandleCommand.
	cmdMu           sync.RWMutex
	commandHandlers map[string]CommandHandler
	commandSpecs    map[string]CommandSpec

	// subscribedTopics records every topic the plugin's user code has
	// subscribed to via [hostClient.Subscribe]. The SDK adapter reads
	// this set during the Init RPC response to advertise the plugin's
	// capability wish-list to the host (railyard-fll.4 allow-list flow).
	// Order is preserved insertion order so deterministic test output
	// is possible; duplicate Subscribe calls do not re-record.
	subMu              sync.Mutex
	subscribedTopics   []string
	subscribedTopicSet map[string]struct{}

	// supportedTopics is the set of event topics the host advertised it
	// can deliver, captured from the Init handshake
	// (InitRequest.supported_event_topics, railyard-77h.8).
	// topicNegotiated is true only when the host advertised a non-empty
	// set; an old host that advertises nothing leaves it false and
	// disables the unknown-topic check so new plugins keep working
	// against old hosts. Both are guarded by subMu.
	supportedTopics map[string]struct{}
	topicNegotiated bool

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
		commandSpecs:       make(map[string]CommandSpec),
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
	// Adapt to the meta-aware path, discarding the metadata. Plain
	// Subscribe behaviour is unchanged (railyard-77h.10).
	return h.subscribeInternal(topic, func(t EventType, payload any, _ EventMeta) {
		handler(t, payload)
	})
}

// SubscribeWithMeta implements Host.SubscribeWithMeta. It shares the
// same stream wiring as Subscribe but surfaces the per-stream sequence
// number and cumulative drop count to the handler (railyard-77h.10).
func (h *hostClient) SubscribeWithMeta(topic EventType, handler MetaEventHandler) Unsubscribe {
	if handler == nil {
		return func() {}
	}
	return h.subscribeInternal(topic, handler)
}

// subscribeInternal is the shared implementation behind Subscribe and
// SubscribeWithMeta. The handler always receives [EventMeta]; the plain
// Subscribe wrapper discards it.
func (h *hostClient) subscribeInternal(topic EventType, handler MetaEventHandler) Unsubscribe {
	h.recordSubscribedTopic(string(topic))
	// Negotiation guard (railyard-77h.8): when the host advertised the
	// topics it supports, a subscription to a topic outside that set will
	// never deliver. Surface a clear, distinct WARN ("unknown-topic")
	// rather than letting it fail silently — distinct from the
	// allow-list denial surfaced in runSubscribeLoop ("allowlist-denied").
	if h.unknownTopic(string(topic)) && h.logger != nil {
		h.logger.Warn(
			"plugin: subscribing to a topic the host does not advertise; it will never deliver — check host/SDK version compatibility",
			slog.String("topic", string(topic)),
			slog.String("reason", "unknown-topic"),
		)
	}
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

// setSupportedTopics records the host's Init-time topic advertisement
// (InitRequest.supported_event_topics). A non-empty list enables the
// unknown-topic check in [hostClient.Subscribe]; an empty list (an old
// host that predates negotiation) leaves the check disabled
// (railyard-77h.8).
func (h *hostClient) setSupportedTopics(topics []string) {
	h.subMu.Lock()
	defer h.subMu.Unlock()
	if len(topics) == 0 {
		h.topicNegotiated = false
		h.supportedTopics = nil
		return
	}
	h.topicNegotiated = true
	h.supportedTopics = make(map[string]struct{}, len(topics))
	for _, t := range topics {
		if t == "" {
			continue
		}
		h.supportedTopics[t] = struct{}{}
	}
}

// unknownTopic reports whether topic is one the host did NOT advertise
// at Init time. It returns false when topic negotiation is inactive
// (the host advertised nothing), so a new plugin keeps working against
// an old host (railyard-77h.8).
func (h *hostClient) unknownTopic(topic string) bool {
	// Plugin-published topics are namespaced "<plugin>.<name>"
	// (railyard-77h.9) and are legitimately absent from the host's core
	// topic advertisement, so never flag a dotted/namespaced topic.
	if strings.Contains(topic, ".") {
		return false
	}
	h.subMu.Lock()
	defer h.subMu.Unlock()
	if !h.topicNegotiated {
		return false
	}
	_, ok := h.supportedTopics[topic]
	return !ok
}

// Emit implements Host.Emit. It encodes the payload as a Struct and
// forwards it to the host's EmitEvent RPC. The host enforces the
// "<plugin>." namespace prefix and the allow.publish gate; a violation
// surfaces here as a gRPC error (railyard-77h.9).
func (h *hostClient) Emit(ctx context.Context, topic string, payload map[string]any) error {
	st, err := structpb.NewStruct(payload)
	if err != nil {
		return fmt.Errorf("plugin: Emit %q: encoding payload: %w", topic, err)
	}
	if _, err := h.hsc.EmitEvent(ctx, &protov1.EmitEventRequest{Topic: topic, Payload: st}); err != nil {
		return fmt.Errorf("plugin: Emit %q: %w", topic, err)
	}
	return nil
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

// advertisedCommandSpecs returns the wire CommandSchema for every command
// registered with a typed spec via [Host.RegisterCommandSpec]
// (railyard-77h.16). Bare commands (RegisterCommand) are absent. Consumed
// by the PluginService Init adapter to fill InitResponse.command_specs.
func (h *hostClient) advertisedCommandSpecs() []*protov1.CommandSchema {
	h.cmdMu.RLock()
	defer h.cmdMu.RUnlock()
	out := make([]*protov1.CommandSchema, 0, len(h.commandSpecs))
	for _, spec := range h.commandSpecs {
		out = append(out, commandSpecToProto(spec))
	}
	return out
}

func (h *hostClient) runSubscribeLoop(ctx context.Context, topic EventType, stream protov1.HostService_SubscribeClient, handler MetaEventHandler) {
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
			// Distinguish an allow-list denial (railyard-77h.8) from a
			// generic stream close so operators can tell "your config
			// blocks this topic" apart from "the host went away".
			if st, ok := status.FromError(err); ok && st.Code() == codes.PermissionDenied {
				h.logger.Warn("plugin: subscription denied by host allow-list",
					slog.String("topic", string(topic)),
					slog.String("reason", "allowlist-denied"),
					slog.String("err", err.Error()),
				)
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
		meta := EventMeta{Seq: ev.Seq, Dropped: ev.Dropped}
		func() {
			defer func() {
				if r := recover(); r != nil {
					h.logger.Error("plugin: subscriber panic recovered",
						slog.String("topic", string(topic)),
						slog.Any("panic", r),
					)
				}
			}()
			handler(decoded.topic, decoded.payload, meta)
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

// RegisterCommandSpec implements Host.RegisterCommandSpec. Like
// RegisterCommand it is an in-process registration; additionally it
// records the typed argument signature so the PluginService Init adapter
// can advertise it to the host in InitResponse.command_specs, where the
// host stores it and validates dispatched args before forwarding to
// HandleCommand (railyard-77h.16).
func (h *hostClient) RegisterCommandSpec(spec CommandSpec, handler CommandHandler) error {
	if spec.Name == "" {
		return errors.New("plugin: RegisterCommandSpec: name must not be empty")
	}
	if handler == nil {
		return errors.New("plugin: RegisterCommandSpec: handler must not be nil")
	}
	h.cmdMu.Lock()
	defer h.cmdMu.Unlock()
	if _, exists := h.commandHandlers[spec.Name]; exists {
		return fmt.Errorf("plugin: RegisterCommandSpec: %q already registered", spec.Name)
	}
	h.commandHandlers[spec.Name] = handler
	h.commandSpecs[spec.Name] = spec
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
