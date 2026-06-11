// Package plugintest provides a maintained [plugin.Host] fake plus
// recording affordances so plugin authors can unit-test their plugins
// without hand-rolling a fake host.
//
// The package is part of railyard's stable public SDK surface: every
// exported identifier here is a contract that ships under the same
// versioning rules as the rest of pkg/plugin.
//
// # Typical usage
//
// Construct a [FakeHost] with the zero value, set whatever fields the
// plugin under test reads, and pass it to Init/Start:
//
//	fh := &plugintest.FakeHost{}
//	fh.YardInfoValue = plugin.YardInfo{YardID: "test-yard"}
//	fh.SnapshotValue = &plugin.Snapshot{}
//
//	p := &MyPlugin{}
//	_ = p.Init(context.Background(), fh)
//	_ = p.Start(context.Background())
//
//	fh.DriveEvent(plugin.CarCreated, plugin.CarCreatedEvent{CarID: "c-1"})
//
//	if got := len(fh.Subscriptions()); got != 1 {
//	    t.Fatalf("expected 1 subscription, got %d", got)
//	}
//
// # Design choices
//
// FakeHost is built around the field-set-after-construction pattern:
// callers create a zero value and overwrite specific fields, rather
// than threading every knob through a constructor. This keeps the
// SDK ergonomic when new affordances are added — a new field never
// breaks existing tests.
//
// All recording slices are exported and stable. Callers should read
// them through the accessor methods ([FakeHost.Subscriptions],
// [FakeHost.Registrations], [FakeHost.Logs]) because the accessors take
// the host's mutex and return safe copies. Direct field access is
// permitted but is not concurrency-safe.
package plugintest

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"

	"gopkg.in/yaml.v3"

	"github.com/zulandar/railyard/pkg/plugin"
)

// CapturedLog is one log record captured by [FakeHost.Logger]. The
// record's level, message, and structured attributes are preserved so
// tests can assert on either the text or the typed attribute values.
type CapturedLog struct {
	// Level is the slog level the record was emitted at.
	Level slog.Level

	// Message is the human-readable log message.
	Message string

	// Attrs is the flattened slice of structured attributes attached
	// to the record, in the order slog handed them to the handler.
	Attrs []slog.Attr
}

// RecordedSubscription captures one call to [FakeHost.Subscribe]. The
// handler is retained so [FakeHost.DriveEvent] can dispatch synthetic
// events back into the plugin under test.
type RecordedSubscription struct {
	// Topic is the [plugin.EventType] the plugin subscribed to.
	Topic plugin.EventType

	// Handler is the handler the plugin registered via Subscribe. Nil for
	// subscriptions registered via [FakeHost.SubscribeWithMeta] (see
	// MetaHandler). Tests rarely call this directly — prefer
	// [FakeHost.DriveEvent].
	Handler plugin.EventHandler

	// MetaHandler is the handler registered via [FakeHost.SubscribeWithMeta].
	// Nil for plain Subscribe registrations. Fired by
	// [FakeHost.DriveEventWithMeta] (and by [FakeHost.DriveEvent] with a
	// zero [plugin.EventMeta]).
	MetaHandler plugin.MetaEventHandler

	// Unsubscribed reports whether the subscription's [plugin.Unsubscribe]
	// has been called. [FakeHost.DriveEvent] skips records where this
	// is true.
	Unsubscribed bool
}

// RecordedRegistration captures one call to [FakeHost.RegisterCommand]
// or [FakeHost.RegisterCommandSpec]. Tests can assert that a plugin
// registered the expected command name — and, for the typed variant, the
// expected argument signature — without invoking the underlying handler.
type RecordedRegistration struct {
	// Name is the command name the plugin registered.
	Name string

	// Handler is the [plugin.CommandHandler] the plugin supplied.
	Handler plugin.CommandHandler

	// Spec is the typed [plugin.CommandSpec] for registrations made via
	// [FakeHost.RegisterCommandSpec] (railyard-77h.16). For a bare
	// [FakeHost.RegisterCommand] it is the zero value with Spec.Name set
	// to Name and Spec.Args nil, so tests can read Spec.Args == nil to
	// tell the two registration paths apart.
	Spec plugin.CommandSpec
}

// RecordedEmit captures one call to [FakeHost.Emit] so tests can assert
// "the plugin published topic X with this payload" (railyard-77h.9).
type RecordedEmit struct {
	// Topic is the namespaced topic the plugin emitted.
	Topic string

	// Payload is the map the plugin supplied. Stored by reference; the
	// FakeHost does not copy the map.
	Payload map[string]any
}

// RecordedDispatch captures one call to [FakeHost.DispatchCommand].
// Useful for asserting "the plugin asked the host to invoke X with
// these args" without wiring up a full command pipeline.
type RecordedDispatch struct {
	// Name is the command name the plugin dispatched.
	Name string

	// Args is the argument map the plugin supplied. Stored by reference;
	// the FakeHost does not copy the map.
	Args plugin.CommandArgs
}

// FakeHost is a [plugin.Host] implementation suitable for unit tests.
// Construct it as a zero value and set the public configuration fields
// (SnapshotValue, YardInfoValue, ConfigValues, DispatchHandlers,
// RegisterCommandErr) before passing it to the plugin under test.
//
// FakeHost is safe for concurrent use by the plugin under test.
type FakeHost struct {
	// SnapshotValue is returned (with a nil error) from [FakeHost.Snapshot].
	// When SnapshotErr is also set the error takes precedence and the
	// value is dropped on the floor — see [FakeHost.Snapshot] for the
	// matching real-host contract.
	SnapshotValue *plugin.Snapshot

	// SnapshotErr is returned from [FakeHost.Snapshot] when non-nil.
	// Setting both SnapshotErr and SnapshotValue results in (nil, err)
	// to match railyard's real *Host, which returns (nil, err) on every
	// failure path. This prevents tests from silently passing on code
	// that would panic in production by skipping the err check.
	SnapshotErr error

	// YardInfoValue is returned verbatim from [FakeHost.YardInfo].
	YardInfoValue plugin.YardInfo

	// ConfigValues maps a plugin name to the [yaml.Node] returned from
	// [FakeHost.Config]. Missing keys return the zero value, matching
	// the real host's "no config provided" semantics.
	ConfigValues map[string]yaml.Node

	// DispatchHandlers maps a command name to the function invoked when
	// [FakeHost.DispatchCommand] is called with that name. If no handler
	// is registered for the dispatched name, DispatchCommand returns a
	// [plugin.CommandResult] with Success=false and a "no handler"
	// error message; the returned error is non-nil.
	DispatchHandlers map[string]plugin.CommandHandler

	// RegisterCommandErr, if non-nil, is returned from every call to
	// [FakeHost.RegisterCommand]. The registration is still recorded so
	// tests can assert on the attempted name.
	RegisterCommandErr error

	// EmitErr, if non-nil, is returned from every call to [FakeHost.Emit].
	// The emit is still recorded so tests can assert on the attempted
	// topic and payload.
	EmitErr error

	// LoggerHandler overrides the default capturing handler. Leave nil
	// to use the built-in [CapturedLog] recorder accessible via
	// [FakeHost.Logs].
	LoggerHandler slog.Handler

	mu             sync.Mutex
	subscriptions  []*RecordedSubscription
	registrations  []RecordedRegistration
	dispatches     []RecordedDispatch
	emits          []RecordedEmit
	logs           []CapturedLog
	defaultHandler slog.Handler

	// store is the in-memory key/value fake returned by [FakeHost.Store].
	// It is lazily created on first access so the zero-value FakeHost
	// keeps working. Like the real host's per-connection store it is
	// namespaced to this FakeHost instance — a separate FakeHost has a
	// separate store, so two plugins under test never see each other's
	// keys (railyard-77h.11).
	store *FakeStore
}

// Compile-time assertion that *FakeHost satisfies plugin.Host. The
// assertion is the contract enforcer: if [plugin.Host] grows or
// changes a method, FakeHost must be updated in lockstep.
var _ plugin.Host = (*FakeHost)(nil)

// Config returns the [yaml.Node] previously set for the named plugin
// via ConfigValues. Missing keys return the zero value (Kind == 0),
// matching the real host's "no config provided" contract.
func (h *FakeHost) Config(name string) yaml.Node {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.ConfigValues == nil {
		return yaml.Node{}
	}
	return h.ConfigValues[name]
}

// YardInfo returns the YardInfoValue field verbatim.
func (h *FakeHost) YardInfo() plugin.YardInfo {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.YardInfoValue
}

// Subscribe records the call and returns an [plugin.Unsubscribe] that
// marks the recorded subscription as unsubscribed.
//
// The recorded handler is preserved so tests can fire synthetic events
// via [FakeHost.DriveEvent]. Subscriptions whose Unsubscribe has been
// invoked are skipped by DriveEvent.
func (h *FakeHost) Subscribe(topic plugin.EventType, handler plugin.EventHandler) plugin.Unsubscribe {
	h.mu.Lock()
	rec := &RecordedSubscription{Topic: topic, Handler: handler}
	h.subscriptions = append(h.subscriptions, rec)
	h.mu.Unlock()

	return func() {
		h.mu.Lock()
		defer h.mu.Unlock()
		rec.Unsubscribed = true
	}
}

// SubscribeWithMeta records a meta-aware subscription and returns an
// [plugin.Unsubscribe] that marks it unsubscribed. The recorded handler
// is fired by [FakeHost.DriveEventWithMeta] (with the supplied
// [plugin.EventMeta]) and by [FakeHost.DriveEvent] (with a zero meta).
func (h *FakeHost) SubscribeWithMeta(topic plugin.EventType, handler plugin.MetaEventHandler) plugin.Unsubscribe {
	h.mu.Lock()
	rec := &RecordedSubscription{Topic: topic, MetaHandler: handler}
	h.subscriptions = append(h.subscriptions, rec)
	h.mu.Unlock()

	return func() {
		h.mu.Lock()
		defer h.mu.Unlock()
		rec.Unsubscribed = true
	}
}

// Snapshot returns SnapshotValue and SnapshotErr. When SnapshotErr is
// non-nil the value is dropped and (nil, err) is returned — this
// mirrors railyard's real *Host, which returns (nil, err) on every
// failure path (see pkg/plugin/hostclient_adapter.go). The context is
// not inspected.
func (h *FakeHost) Snapshot(_ context.Context) (*plugin.Snapshot, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.SnapshotErr != nil {
		return nil, h.SnapshotErr
	}
	return h.SnapshotValue, nil
}

// RegisterCommand records the registration and returns an error in the
// same situations railyard's real *Host does (see
// internal/pluginhost/command.go):
//
//   - empty name → "pluginhost: command name must not be empty"
//   - nil handler → "pluginhost: command handler must not be nil"
//   - duplicate name → "pluginhost: command %q is already registered"
//
// A non-nil [FakeHost.RegisterCommandErr] takes precedence over the
// validation errors so tests can inject arbitrary failure modes.
//
// The registration is recorded even when an error is returned so tests
// can assert on the attempted name.
func (h *FakeHost) RegisterCommand(name string, handler plugin.CommandHandler) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.registrations = append(h.registrations, RecordedRegistration{
		Name:    name,
		Handler: handler,
		Spec:    plugin.CommandSpec{Name: name},
	})
	if h.RegisterCommandErr != nil {
		return h.RegisterCommandErr
	}
	if name == "" {
		return errors.New("pluginhost: command name must not be empty")
	}
	if handler == nil {
		return errors.New("pluginhost: command handler must not be nil")
	}
	for _, prev := range h.registrations[:len(h.registrations)-1] {
		if prev.Name == name {
			return fmt.Errorf("pluginhost: command %q is already registered", name)
		}
	}
	return nil
}

// RegisterCommandSpec records a typed registration and returns errors in
// the same situations as [FakeHost.RegisterCommand], keyed off
// spec.Name. The full [plugin.CommandSpec] (including Args) is recorded
// on [RecordedRegistration.Spec] so tests can assert on the declared
// argument signature. A non-nil [FakeHost.RegisterCommandErr] takes
// precedence over the validation errors (railyard-77h.16).
func (h *FakeHost) RegisterCommandSpec(spec plugin.CommandSpec, handler plugin.CommandHandler) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.registrations = append(h.registrations, RecordedRegistration{
		Name:    spec.Name,
		Handler: handler,
		Spec:    spec,
	})
	if h.RegisterCommandErr != nil {
		return h.RegisterCommandErr
	}
	if spec.Name == "" {
		return errors.New("pluginhost: command name must not be empty")
	}
	if handler == nil {
		return errors.New("pluginhost: command handler must not be nil")
	}
	for _, prev := range h.registrations[:len(h.registrations)-1] {
		if prev.Name == spec.Name {
			return fmt.Errorf("pluginhost: command %q is already registered", spec.Name)
		}
	}
	return nil
}

// DispatchCommand records the call and invokes the matching handler
// from DispatchHandlers. If no handler is registered for the supplied
// name, the returned [plugin.CommandResult] has Success=false with an
// "Error" field describing the miss AND the returned error is nil —
// mirroring railyard's real *Host, which returns
// (CommandResult{Success:false, Error:"command not allowed: X"}, nil)
// for unknown names. Tests that branch on err alone (without checking
// Success) would otherwise silently diverge from production behavior.
func (h *FakeHost) DispatchCommand(ctx context.Context, name string, args plugin.CommandArgs) (plugin.CommandResult, error) {
	h.mu.Lock()
	h.dispatches = append(h.dispatches, RecordedDispatch{Name: name, Args: args})
	handler := h.DispatchHandlers[name]
	h.mu.Unlock()

	if handler == nil {
		return plugin.CommandResult{
			Success: false,
			Error:   "plugintest: no handler registered for command " + name,
		}, nil
	}
	return handler(ctx, args)
}

// Emit records the call and returns EmitErr (nil unless set). The real
// host enforces a "<plugin>." namespace prefix and an allow.publish gate
// on the connection-bound identity; the fake performs no enforcement so
// unit tests can assert what the plugin tried to publish. Inject EmitErr
// to exercise the plugin's error handling (railyard-77h.9).
func (h *FakeHost) Emit(_ context.Context, topic string, payload map[string]any) error {
	h.mu.Lock()
	h.emits = append(h.emits, RecordedEmit{Topic: topic, Payload: payload})
	err := h.EmitErr
	h.mu.Unlock()
	return err
}

// Emits returns a snapshot of the [FakeHost.Emit] calls made so far.
func (h *FakeHost) Emits() []RecordedEmit {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]RecordedEmit, len(h.emits))
	copy(out, h.emits)
	return out
}

// Store returns this FakeHost's in-memory [plugin.Store] fake
// (railyard-77h.11). The store is created lazily and is namespaced to
// this FakeHost instance: a different FakeHost has a different store, so
// tests for two plugins never share keys — mirroring the real host's
// per-connection namespacing. Tests can inspect or seed the store
// directly via [FakeHost.StoredValues] / [FakeStore].
func (h *FakeHost) Store() plugin.Store {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.store == nil {
		h.store = NewFakeStore()
	}
	return h.store
}

// StoredValues returns a copy of every key/value currently held by this
// FakeHost's Store, so a test can assert "the plugin persisted X" without
// reaching back through the Store interface. The returned map and its
// byte slices are copies; mutating them does not affect host state.
func (h *FakeHost) StoredValues() map[string][]byte {
	h.mu.Lock()
	store := h.store
	h.mu.Unlock()
	if store == nil {
		return map[string][]byte{}
	}
	return store.snapshot()
}

// Logger returns a [*slog.Logger] backed by either LoggerHandler (when
// set) or a built-in capturing handler that appends every record to
// the slice accessible via [FakeHost.Logs].
func (h *FakeHost) Logger() *slog.Logger {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.LoggerHandler != nil {
		return slog.New(h.LoggerHandler)
	}
	if h.defaultHandler == nil {
		h.defaultHandler = &captureHandler{host: h}
	}
	return slog.New(h.defaultHandler)
}

// Subscriptions returns a value-copy snapshot of the currently
// recorded subscriptions. Each element is a deep copy taken under the
// host mutex, so callers may freely read fields like Unsubscribed even
// while the plugin under test concurrently calls the returned
// Unsubscribe — there is no shared pointer that go test -race could
// flag. Mutating the returned slice does not affect host state.
func (h *FakeHost) Subscriptions() []RecordedSubscription {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]RecordedSubscription, len(h.subscriptions))
	for i, s := range h.subscriptions {
		out[i] = *s
	}
	return out
}

// Registrations returns a snapshot of the currently recorded command
// registrations.
func (h *FakeHost) Registrations() []RecordedRegistration {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]RecordedRegistration, len(h.registrations))
	copy(out, h.registrations)
	return out
}

// Dispatches returns a snapshot of the [FakeHost.DispatchCommand]
// calls made so far.
func (h *FakeHost) Dispatches() []RecordedDispatch {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]RecordedDispatch, len(h.dispatches))
	copy(out, h.dispatches)
	return out
}

// Logs returns a snapshot of the log records captured by the default
// logger. If [FakeHost.LoggerHandler] is set, this returns the records
// captured before LoggerHandler was assigned (typically empty).
func (h *FakeHost) Logs() []CapturedLog {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]CapturedLog, len(h.logs))
	copy(out, h.logs)
	return out
}

// DriveEvent dispatches a synthetic event into every active (non-
// unsubscribed) subscription registered for the given topic. Each
// matching handler is invoked synchronously on the caller's goroutine,
// which simplifies test assertions compared to the real host's
// per-subscriber goroutine model.
//
// DriveEvent snapshots the subscription slice under the host mutex,
// then re-checks each entry's Unsubscribed flag under the mutex
// immediately before invoking its handler. A concurrent Unsubscribe
// observed between the snapshot and the per-entry check causes the
// handler to be skipped — the godoc contract "DriveEvent skips
// records where Unsubscribed is true" therefore holds even when
// plugin code unsubscribes from a goroutine racing this call.
//
// Returns the number of handlers that were invoked.
func (h *FakeHost) DriveEvent(topic plugin.EventType, payload any) int {
	return h.driveEvent(topic, payload, plugin.EventMeta{})
}

// DriveEventWithMeta is like [FakeHost.DriveEvent] but delivers the
// supplied [plugin.EventMeta] to meta-aware subscriptions registered via
// [FakeHost.SubscribeWithMeta]. Plain Subscribe handlers still fire
// (they receive only topic+payload). Returns the number of handlers
// invoked (railyard-77h.10).
func (h *FakeHost) DriveEventWithMeta(topic plugin.EventType, payload any, meta plugin.EventMeta) int {
	return h.driveEvent(topic, payload, meta)
}

// driveEvent is the shared dispatch loop behind DriveEvent and
// DriveEventWithMeta. It fires each active subscription's handler for
// the topic: plain [plugin.EventHandler]s get topic+payload,
// [plugin.MetaEventHandler]s additionally get meta.
func (h *FakeHost) driveEvent(topic plugin.EventType, payload any, meta plugin.EventMeta) int {
	h.mu.Lock()
	snap := make([]*RecordedSubscription, len(h.subscriptions))
	copy(snap, h.subscriptions)
	h.mu.Unlock()

	invoked := 0
	for _, sub := range snap {
		h.mu.Lock()
		if sub.Unsubscribed || sub.Topic != topic {
			h.mu.Unlock()
			continue
		}
		handler := sub.Handler
		metaHandler := sub.MetaHandler
		h.mu.Unlock()
		switch {
		case metaHandler != nil:
			metaHandler(topic, payload, meta)
			invoked++
		case handler != nil:
			handler(topic, payload)
			invoked++
		}
	}
	return invoked
}

// Reset clears every recorded slice and resets the default log handler.
// The configurable fields (SnapshotValue, YardInfoValue, ConfigValues,
// DispatchHandlers, RegisterCommandErr, LoggerHandler) are not touched.
func (h *FakeHost) Reset() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.subscriptions = nil
	h.registrations = nil
	h.dispatches = nil
	h.emits = nil
	h.logs = nil
	h.defaultHandler = nil
	h.store = nil
}

// captureHandler is the default slog handler installed by
// [FakeHost.Logger]. It implements the minimum slog.Handler surface
// needed to flatten a record into a [CapturedLog] and append it to
// the host's logs slice. Group/WithAttrs are pass-throughs that
// preserve attribute state for the next record.
type captureHandler struct {
	host  *FakeHost
	attrs []slog.Attr
	group string
}

// Enabled accepts every level — tests want to observe everything the
// plugin emits, not silently filter.
func (h *captureHandler) Enabled(_ context.Context, _ slog.Level) bool { return true }

// Handle flattens the record into a [CapturedLog] and appends it to
// the host's logs slice under the host mutex.
func (h *captureHandler) Handle(_ context.Context, rec slog.Record) error {
	attrs := make([]slog.Attr, 0, len(h.attrs)+rec.NumAttrs())
	attrs = append(attrs, h.attrs...)
	rec.Attrs(func(a slog.Attr) bool {
		attrs = append(attrs, a)
		return true
	})
	entry := CapturedLog{
		Level:   rec.Level,
		Message: rec.Message,
		Attrs:   attrs,
	}
	h.host.mu.Lock()
	h.host.logs = append(h.host.logs, entry)
	h.host.mu.Unlock()
	return nil
}

// WithAttrs returns a child handler that carries the supplied attrs on
// every subsequent record.
func (h *captureHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	cp := *h
	cp.attrs = append([]slog.Attr{}, h.attrs...)
	cp.attrs = append(cp.attrs, attrs...)
	return &cp
}

// WithGroup is a stub that retains the group name. The default capture
// representation does not nest attribute groups; tests that need group
// fidelity should supply a custom [FakeHost.LoggerHandler].
func (h *captureHandler) WithGroup(name string) slog.Handler {
	cp := *h
	cp.group = name
	return &cp
}

// MustYAMLNode is a tiny helper that parses a YAML string into a
// [yaml.Node] suitable for the ConfigValues map. It panics on parse
// failure — appropriate for test setup where a malformed literal is
// a programmer error, not a runtime condition.
func MustYAMLNode(src string) yaml.Node {
	var node yaml.Node
	if err := yaml.Unmarshal([]byte(src), &node); err != nil {
		panic("plugintest.MustYAMLNode: " + err.Error())
	}
	// yaml.Unmarshal wraps the document in a DocumentNode whose first
	// child is the value plugins expect. Most plugin Config callers
	// deal with the inner node, so unwrap one level when present.
	if node.Kind == yaml.DocumentNode && len(node.Content) == 1 {
		return *node.Content[0]
	}
	return node
}
