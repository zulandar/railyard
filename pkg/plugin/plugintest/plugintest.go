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

	// Handler is the handler the plugin registered. Tests rarely call
	// this directly — prefer [FakeHost.DriveEvent], which fires every
	// matching subscription and respects unsubscribes.
	Handler plugin.EventHandler

	// Unsubscribed reports whether the subscription's [plugin.Unsubscribe]
	// has been called. [FakeHost.DriveEvent] skips records where this
	// is true.
	Unsubscribed bool
}

// RecordedRegistration captures one call to [FakeHost.RegisterCommand].
// Tests can assert that a plugin registered the expected command name
// without invoking the underlying handler.
type RecordedRegistration struct {
	// Name is the command name the plugin registered.
	Name string

	// Handler is the [plugin.CommandHandler] the plugin supplied.
	Handler plugin.CommandHandler
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
	// If a non-nil SnapshotErr is also set, the error takes precedence and
	// the value is returned alongside it unchanged.
	SnapshotValue *plugin.Snapshot

	// SnapshotErr is returned from [FakeHost.Snapshot] when non-nil.
	// SnapshotValue is still returned alongside it (mirroring how real
	// hosts may return partial state with a wrapped error).
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

	// LoggerHandler overrides the default capturing handler. Leave nil
	// to use the built-in [CapturedLog] recorder accessible via
	// [FakeHost.Logs].
	LoggerHandler slog.Handler

	mu             sync.Mutex
	subscriptions  []*RecordedSubscription
	registrations  []RecordedRegistration
	dispatches     []RecordedDispatch
	logs           []CapturedLog
	defaultHandler slog.Handler
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

// Snapshot returns SnapshotValue and SnapshotErr. The context is not
// inspected — tests that want to assert on context cancellation should
// build that assertion into a custom test, not rely on FakeHost.
func (h *FakeHost) Snapshot(_ context.Context) (*plugin.Snapshot, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.SnapshotValue, h.SnapshotErr
}

// RegisterCommand records the registration and returns
// RegisterCommandErr. The registration is recorded even when an error
// is returned so tests can assert on the attempted name.
func (h *FakeHost) RegisterCommand(name string, handler plugin.CommandHandler) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.registrations = append(h.registrations, RecordedRegistration{Name: name, Handler: handler})
	return h.RegisterCommandErr
}

// DispatchCommand records the call and invokes the matching handler
// from DispatchHandlers. If no handler is registered for the supplied
// name, the returned [plugin.CommandResult] has Success=false and the
// returned error is non-nil with a descriptive message.
func (h *FakeHost) DispatchCommand(ctx context.Context, name string, args plugin.CommandArgs) (plugin.CommandResult, error) {
	h.mu.Lock()
	h.dispatches = append(h.dispatches, RecordedDispatch{Name: name, Args: args})
	handler := h.DispatchHandlers[name]
	h.mu.Unlock()

	if handler == nil {
		return plugin.CommandResult{
			Success: false,
			Error:   "plugintest: no handler registered for command " + name,
		}, errors.New("plugintest: no handler registered for command " + name)
	}
	return handler(ctx, args)
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

// Subscriptions returns a snapshot of the currently recorded
// subscriptions. The returned slice is a fresh copy; mutating it does
// not affect the host's internal state. The pointed-to
// [RecordedSubscription] values are shared, however — reading them is
// safe, but tests should not mutate them.
func (h *FakeHost) Subscriptions() []*RecordedSubscription {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]*RecordedSubscription, len(h.subscriptions))
	copy(out, h.subscriptions)
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
// Returns the number of handlers that were invoked.
func (h *FakeHost) DriveEvent(topic plugin.EventType, payload any) int {
	h.mu.Lock()
	// Copy the slice header so we can release the lock before calling
	// user handlers — they may re-enter the host (e.g. Subscribe inside
	// a handler) and we must not deadlock.
	targets := make([]*RecordedSubscription, 0, len(h.subscriptions))
	for _, sub := range h.subscriptions {
		if sub.Unsubscribed || sub.Topic != topic {
			continue
		}
		targets = append(targets, sub)
	}
	h.mu.Unlock()

	for _, sub := range targets {
		sub.Handler(topic, payload)
	}
	return len(targets)
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
	h.logs = nil
	h.defaultHandler = nil
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
