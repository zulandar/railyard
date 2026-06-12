// Package plugin defines the public SDK that external railyard plugins
// import. Every type, function, and method exported from this package is
// part of railyard's plugin API contract — signatures are intentionally
// stable across patch releases of the railyard core module.
//
// # Authoring a plugin
//
// A plugin is a Go type that implements [Plugin] and ships as a
// standalone binary whose main package calls [Serve]:
//
//	func main() {
//	    plugin.Serve(&MyPlugin{})
//	}
//
// The host launches each plugin as a subprocess and brokers all
// interaction with internal subsystems via the [Host] interface, which
// is fulfilled by an in-plugin gRPC client adapter. Plugins MUST NOT
// import any package under github.com/zulandar/railyard/internal — the
// [Host] interface is the only sanctioned path into core.
package plugin

import (
	"context"
)

// Plugin is the contract every railyard plugin must implement.
//
// The lifecycle is:
//
//  1. The plugin process is launched by the host. The plugin's main()
//     calls [Serve], which performs the handshake and blocks for the
//     lifetime of the process.
//  2. The host calls Init after config has been loaded but before core
//     subsystems start. Init should read the plugin's config via
//     [Host.Config], validate it, build any clients, AND register every
//     event subscription and command the plugin provides
//     ([Host.Subscribe], [Host.RegisterCommand], [Host.RegisterCommandSpec]).
//     Registrations made during Init are advertised to the host in the Init
//     response; a subscription or command registered later (e.g. in Start)
//     is NOT advertised and is silently never delivered or dispatched. An
//     error returned from Init causes the host to log a warning and skip
//     this plugin; other plugins and the core continue normally.
//  3. The host calls Start after core subsystems are up. Start should
//     launch any long-lived workers as plain goroutines (the plugin owns
//     its own process under the subprocess model) and return quickly. Do
//     NOT register subscriptions or commands here — that must happen in
//     Init (see above).
//  4. On shutdown the host calls Stop with a context that is cancelled
//     after a 5-second drain timeout. Stop must release resources but
//     must not block core shutdown — work that can be abandoned should
//     be abandoned when the context is done.
type Plugin interface {
	// Name returns the plugin's stable identifier. It must match the
	// plugin binary's basename, the top-level key the plugin reads from
	// railyard.yaml, and the name advertised in the gRPC Init handshake.
	// The name is used in log scopes, metrics labels, and command
	// routing.
	Name() string

	// Init is called once after config load and before core subsystems
	// boot. The plugin should read its config, validate it, build any
	// clients, AND register every event subscription and command here —
	// registrations are advertised to the host in the Init response, so a
	// Subscribe / RegisterCommand / RegisterCommandSpec call made after
	// Init returns is silently never wired up. Returning an error causes
	// the host to skip this plugin for the rest of the binary's lifetime.
	Init(ctx context.Context, h Host) error

	// Start is called once after core subsystems are running. Plugins
	// should launch any long-lived workers here; subscriptions and
	// commands must already have been registered in Init (registering them
	// in Start is too late — they are not advertised). Start should not
	// block — long-running work belongs in a goroutine launched from Start.
	Start(ctx context.Context) error

	// Stop is called once on shutdown. The provided context is cancelled
	// after the host's per-plugin drain timeout (5 seconds in Phase 1).
	// Stop must not block core shutdown indefinitely.
	Stop(ctx context.Context) error
}

// HealthStatus is a plugin's self-reported functional state, surfaced by
// the host in `ry plugins status`. It maps one-for-one onto the proto
// HealthState enum on the wire. The zero value is [HealthUnknown]; a
// HealthReporter should always return one of the three real states.
type HealthStatus int

const (
	// HealthUnknown is the zero value. A HealthReporter should never
	// return it; the host treats it as [HealthDegraded] defensively.
	HealthUnknown HealthStatus = iota

	// HealthOK means the plugin is fully functional.
	HealthOK

	// HealthDegraded means the plugin is running but impaired (e.g. a
	// remote dependency is slow or partially unavailable).
	HealthDegraded

	// HealthFailing means the plugin is running but non-functional (e.g.
	// dead remote credentials), even though the process is alive.
	HealthFailing
)

// String returns the lowercase wire/display name of the status.
func (s HealthStatus) String() string {
	switch s {
	case HealthOK:
		return "ok"
	case HealthDegraded:
		return "degraded"
	case HealthFailing:
		return "failing"
	default:
		return "unknown"
	}
}

// HealthReporter is an OPTIONAL interface a [Plugin] may also implement
// to expose a functional-health probe. The host polls it on a
// configurable interval (default 30s) so operators can distinguish a
// plugin whose process is alive but is non-functional — e.g. a connector
// with dead remote credentials — from a genuinely healthy one. The
// result is surfaced in `ry plugins status` under the HEALTH column.
//
// Implementing HealthReporter is opt-in: a plugin that does not implement
// it stays fully backward compatible — the host shows "n/a" for the
// HEALTH column rather than treating the absence as an error.
//
// Health is called from the host on its poll interval with a short (2s)
// deadline. Implementations MUST return promptly and MUST NOT block on
// long remote calls; do any expensive checking in the background and
// return a cached verdict here. The returned message is a short
// human-readable explanation surfaced to operators (e.g. "github API
// 401").
//
// HealthReporter is part of the frozen SDK surface: it is additive to the
// required [Plugin] interface and never changes its signature.
type HealthReporter interface {
	// Health reports the plugin's current functional state and a short
	// human-readable message. It must return quickly (the host enforces a
	// 2s deadline).
	Health(ctx context.Context) (HealthStatus, string)
}
