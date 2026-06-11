package plugin

import (
	"context"
	"log/slog"

	"gopkg.in/yaml.v3"
)

// EventHandler is the signature for typed event subscribers registered
// via [Host.Subscribe]. The first argument is the [EventType] the
// subscription was registered for, and the second is the payload whose
// concrete type matches the topic-to-payload mapping documented on
// [EventType]. Handlers run on a dedicated per-subscriber goroutine
// drained from a buffered channel; long-running work should be queued
// to avoid filling the channel and triggering drop-oldest backpressure.
type EventHandler func(topic EventType, payload any)

// Unsubscribe cancels a subscription previously registered via
// [Host.Subscribe]. It is safe to call more than once; subsequent calls
// are no-ops. It is safe to call from within the handler itself.
type Unsubscribe func()

// EventMeta carries per-event stream metadata delivered alongside the
// payload to handlers registered via [Host.SubscribeWithMeta]. It lets
// a consumer detect gaps in an at-most-once event stream and reconcile
// via [Host.Snapshot] (railyard-77h.10).
type EventMeta struct {
	// Seq is the per-subscription delivery counter: 1 for the first
	// event delivered on the stream, incrementing by one per delivered
	// event. It RESETS to 1 when the stream is reopened (e.g. after a
	// plugin relaunch), which the consumer should treat as "take a fresh
	// Snapshot before trusting subsequent events".
	Seq uint64

	// Dropped is the cumulative number of events the host dropped on this
	// subscription since the stream opened (drop-oldest backpressure). An
	// increase in Dropped between two received events signals a gap;
	// re-Snapshot to reconcile. Exactly-once delivery is out of scope.
	Dropped uint64
}

// MetaEventHandler is the handler signature for [Host.SubscribeWithMeta].
// It receives the same topic and payload as an [EventHandler] plus the
// [EventMeta] describing stream position and drops.
type MetaEventHandler func(topic EventType, payload any, meta EventMeta)

// Host is the single interface plugins use to interact with railyard
// core. Implementations live in the railyard internal/pluginhost
// package; the in-plugin gRPC client adapter satisfies this interface
// over the wire. This package only declares the contract.
//
// Every method on Host is safe for concurrent use unless documented
// otherwise on the method itself.
type Host interface {
	// Config returns the raw yaml.Node for the named plugin's
	// top-level config block from railyard.yaml. If no block was set
	// the returned node is the zero value and Kind == 0; plugins
	// should treat that as "no config provided" and apply defaults.
	// The plugin is responsible for unmarshalling the node into its
	// own typed configuration struct and validating the result.
	Config(name string) yaml.Node

	// YardInfo returns the static [YardInfo] for this railyard
	// instance. The returned value does not change for the lifetime
	// of the binary; plugins typically call it once during Init.
	YardInfo() YardInfo

	// Subscribe registers a typed event handler for the given topic.
	// The handler runs on a dedicated per-subscriber goroutine drained
	// from a buffered channel. Returns an [Unsubscribe] that cancels
	// the subscription.
	//
	// Backpressure is drop-oldest with a WARN log naming the
	// subscriber and topic; consumers that need every event should
	// not do heavy work inside the handler.
	Subscribe(topic EventType, h EventHandler) Unsubscribe

	// SubscribeWithMeta is like [Host.Subscribe] but additionally hands
	// the handler an [EventMeta] describing the per-stream sequence
	// number and cumulative drop count, so consumers can detect gaps and
	// reconcile via [Host.Snapshot]. Plain Subscribe remains available
	// unchanged for consumers that do not need gap detection.
	SubscribeWithMeta(topic EventType, h MetaEventHandler) Unsubscribe

	// Snapshot returns the current full yard state in a single read
	// transaction. It is intended for heartbeat-style consumers that
	// re-send full state on a cadence. The context controls the
	// timeout of the underlying database read.
	Snapshot(ctx context.Context) (*Snapshot, error)

	// RegisterCommand exposes a plugin-owned command name that other
	// plugins or external systems can invoke through the plugin host.
	// Returns an error if the name conflicts with a previously
	// registered command (whether plugin-provided or a core
	// allow-list entry).
	//
	// A command registered this way carries no argument schema: the host
	// forwards dispatched args verbatim without validation. Use
	// [Host.RegisterCommandSpec] to declare a typed signature the host
	// validates before invoking the handler.
	RegisterCommand(name string, h CommandHandler) error

	// RegisterCommandSpec is the typed-schema variant of
	// [Host.RegisterCommand] (railyard-77h.16). The plugin declares the
	// command's argument signature via [CommandSpec]; the host stores it
	// and validates dispatched args against it — required keys must be
	// present and each present value must match its declared [ArgType] —
	// BEFORE the handler is invoked. A spec with no Args validates only
	// the command name, identical to RegisterCommand.
	//
	// Returns an error on an empty name, a nil handler, or a name that
	// conflicts with a previously registered command (plugin-provided or
	// a core allow-list entry).
	RegisterCommandSpec(spec CommandSpec, h CommandHandler) error

	// DispatchCommand invokes a command by name. The host first looks
	// up the name in the Phase 1 core allow-list (see spec §7.3); if
	// no match, it looks up plugin-registered commands. The arguments
	// are validated against the command's required key/type schema
	// before the underlying implementation runs. Validation failures
	// return [CommandResult] with Success=false and a non-nil error
	// describing the violation.
	DispatchCommand(ctx context.Context, name string, args CommandArgs) (CommandResult, error)

	// Logger returns a structured logger scoped to the plugin's name.
	// All records emitted through the returned logger include a
	// "plugin=<name>" attribute set by the host.
	Logger() *slog.Logger

	// Emit publishes a namespaced event onto the bus so other plugins
	// (and core) can observe it. The topic MUST be prefixed with this
	// plugin's own name, i.e. "<Name()>.<something>"; the host rejects
	// any other prefix and gates the topic against the operator's
	// allow.publish list. Subscribers receive the payload as a
	// map[string]any. Returns an error on a namespace violation, an
	// allow-list denial, or a transport failure (railyard-77h.9).
	Emit(ctx context.Context, topic string, payload map[string]any) error

	// Store returns this plugin's private, persistent key/value store
	// (railyard-77h.11). The store is namespaced to the calling plugin's
	// connection-bound identity — a plugin can only ever read and write
	// its own keys; cross-plugin access is impossible by construction.
	// Values are opaque bytes (the plugin chooses its own encoding).
	//
	// The host enforces per-plugin limits to protect the shared backing
	// DB: a value may be at most 64 KiB, a key at most 256 bytes, and a
	// single plugin may hold at most 1024 keys at once. Overruns surface
	// as an error from Put. A host without a configured DB surfaces an
	// error from every Store method rather than panicking.
	Store() Store
}

// Store is a plugin's private, persistent key/value namespace
// (railyard-77h.11). It is obtained via [Host.Store]. Every method scopes
// to the calling plugin's connection-bound identity on the host side, so
// keys written by one plugin are invisible to every other plugin.
//
// Values are opaque []byte: the plugin owns the encoding. The host caps
// value size (64 KiB), key length (256 bytes), and key count (1024 per
// plugin); a violation is returned as an error from [Store.Put].
type Store interface {
	// Get returns the value stored under key and whether it was present.
	// A missing key returns (nil, false, nil) — absence is not an error.
	Get(ctx context.Context, key string) ([]byte, bool, error)

	// Put inserts or overwrites the value under key. It returns an error
	// when a host limit is exceeded (value too large, key too long, or
	// the per-plugin key cap is reached for a new key) or on transport
	// failure.
	Put(ctx context.Context, key string, value []byte) error

	// Delete removes key from the store. Deleting an absent key is a
	// no-op and returns a nil error.
	Delete(ctx context.Context, key string) error

	// List returns the keys whose name begins with prefix, sorted
	// ascending. An empty prefix returns every key in the namespace.
	List(ctx context.Context, prefix string) ([]string, error)
}
