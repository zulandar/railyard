package plugin

import "context"

// CommandArgs is the weakly-typed argument map for commands dispatched
// through [Host.DispatchCommand] or handled by a plugin-registered
// command via [Host.RegisterCommand]. Keys are command-specific; the
// host validates required keys and value types before binding to the
// underlying implementation.
//
// Using map[string]any keeps the SDK stable while command surfaces
// evolve: adding optional arguments to an existing command does not
// require a new SDK release. See the railyard plugin spec §7.3 for the
// Phase 1 allow-list of dispatchable core commands.
type CommandArgs map[string]any

// CommandResult is the canonical return value of a command. A
// well-behaved command sets Success=true on completion and populates
// Data with any command-specific payload. On failure, Success is false
// and Error carries a human-readable description.
//
// Data may be nil. Implementations should not rely on map identity:
// callers may receive a freshly-allocated map on every call.
type CommandResult struct {
	// Success reports whether the command completed without error.
	Success bool

	// Error is a human-readable description of the failure when
	// Success is false. It is empty on success.
	Error string

	// Data carries command-specific output. It may be nil even on
	// success. Keys and value types are defined by the command.
	Data map[string]any
}

// CommandHandler is the signature for command implementations
// registered by a plugin via [Host.RegisterCommand]. The handler
// receives the request context (which is cancelled when the host
// shuts down) and the validated argument map. Returning a non-nil
// error is equivalent to returning a CommandResult with Success=false
// and Error set to err.Error(); the host translates the two forms.
type CommandHandler func(ctx context.Context, args CommandArgs) (CommandResult, error)
