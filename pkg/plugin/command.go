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

// ArgType is the primitive value type a command argument carries. A
// plugin declares one per argument in a [CommandSpec] via
// [Host.RegisterCommandSpec]; the host type-checks dispatched args
// against it before forwarding to the plugin's handler
// (railyard-77h.16).
//
// The JSON-ish coercion rules the host applies mirror the wire encoding
// (see pkg/plugin/convert.go): values round-trip through
// google.protobuf.Struct, so every JSON number arrives as a float64. An
// [ArgInt] therefore accepts a float64 only when it is integral; an
// [ArgFloat] accepts any float64; [ArgString] accepts a string;
// [ArgBool] accepts a bool.
//
// The zero value is [ArgUnspecified] (matching the proto's reserved 0), so
// an [ArgSpec] that omits Type means "presence-only, any type" rather than
// silently demanding a string. Set an explicit type to enforce one.
type ArgType int

const (
	// ArgUnspecified is the zero value: no declared type. The host
	// presence-checks the argument (when Required) but applies no type
	// check — any value is accepted. This is what an ArgSpec with Type
	// omitted means.
	ArgUnspecified ArgType = iota
	// ArgString validates the value is a string.
	ArgString
	// ArgInt validates the value is an integer (an integral float64 on
	// the wire is accepted; a non-integral float64 is rejected).
	ArgInt
	// ArgBool validates the value is a bool.
	ArgBool
	// ArgFloat validates the value is a float64.
	ArgFloat
)

// ArgSpec declares one typed argument in a command's signature. Required
// args must be present at dispatch; optional args are type-checked only
// when present.
type ArgSpec struct {
	// Name is the argument key in the [CommandArgs] map.
	Name string

	// Type is the expected primitive value type.
	Type ArgType

	// Required reports whether the arg must be present. A missing
	// required arg fails host validation before the handler is invoked.
	Required bool

	// Description is an optional human-readable summary surfaced in
	// `ry plugins status -v` and documentation tooling.
	Description string
}

// CommandSpec is the typed registration shape passed to
// [Host.RegisterCommandSpec]. It pairs a command name with the typed
// argument signature the host validates dispatched args against before
// forwarding to the handler (railyard-77h.16). A spec with no Args
// validates nothing beyond the command name, matching bare
// [Host.RegisterCommand] behaviour.
type CommandSpec struct {
	// Name is the command name (unique across the host).
	Name string

	// Args is the typed argument signature. Empty means no declared
	// args — the command behaves like a bare RegisterCommand.
	Args []ArgSpec
}
