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
//     [Host.Config], validate it, and build any clients. An error
//     returned from Init causes the host to log a warning and skip this
//     plugin; other plugins and the core continue normally.
//  3. The host calls Start after core subsystems are up. Start should
//     subscribe to events, register commands, and launch any long-lived
//     workers as plain goroutines (the plugin owns its own process under
//     the subprocess model). Start should return quickly.
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
	// boot. The plugin should read its config, validate it, and build
	// any clients here. Returning an error causes the host to skip this
	// plugin for the rest of the binary's lifetime.
	Init(ctx context.Context, h Host) error

	// Start is called once after core subsystems are running. Plugins
	// should register subscriptions and commands and launch any
	// long-lived workers here. Start should not block — long-running
	// work belongs in a goroutine launched from Start.
	Start(ctx context.Context) error

	// Stop is called once on shutdown. The provided context is cancelled
	// after the host's per-plugin drain timeout (5 seconds in Phase 1).
	// Stop must not block core shutdown indefinitely.
	Stop(ctx context.Context) error
}
