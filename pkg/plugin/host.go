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
	RegisterCommand(name string, h CommandHandler) error

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
}
