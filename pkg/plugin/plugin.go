// Package plugin defines the public SDK that external railyard plugins
// import. Every type, function, and method exported from this package is
// part of railyard's plugin API contract — signatures are intentionally
// stable across patch releases of the railyard core module.
//
// A plugin is a Go type that implements [Plugin]. Plugins are registered
// at package init time by calling [Register] from a side-effect import in
// an enterprise binary's main package. The plugin host (provided by the
// railyard core, not by this package) walks the registry on boot, calls
// each plugin's lifecycle methods, and brokers all interaction with
// internal subsystems via the [Host] interface.
//
// Plugins must not import any package under github.com/zulandar/railyard/internal.
// The [Host] interface is the only sanctioned path into core.
package plugin

import (
	"context"
	"sync"
)

// Plugin is the contract every railyard plugin must implement.
//
// The lifecycle is:
//
//  1. The plugin's package init() calls [Register], handing the host a
//     factory. No work happens here beyond registration.
//  2. The host invokes the factory once during boot, then calls Init
//     after config has been loaded but before core subsystems start.
//     Init should read the plugin's config via [Host.Config], validate
//     it, and build any clients. An error returned from Init causes the
//     host to log a warning and skip this plugin; other plugins and the
//     core continue normally.
//  3. The host calls Start after core subsystems are up. Start should
//     subscribe to events, register commands, and launch any long-lived
//     workers via [Host.RunDaemon]. Start should return quickly.
//  4. On shutdown the host calls Stop with a context that is cancelled
//     after a 5-second drain timeout. Stop must release resources but
//     must not block core shutdown — work that can be abandoned should
//     be abandoned when the context is done.
type Plugin interface {
	// Name returns the plugin's stable identifier. It must match the
	// name passed to [Register] and the top-level key the plugin reads
	// from railyard.yaml. The name is used in log scopes, metrics
	// labels, and the registry lookup.
	Name() string

	// Init is called once after config load and before core subsystems
	// boot. The plugin should read its config, validate it, and build
	// any clients here. Returning an error causes the host to skip this
	// plugin for the rest of the binary's lifetime.
	Init(ctx context.Context, h Host) error

	// Start is called once after core subsystems are running. Plugins
	// should register subscriptions and commands and launch daemons
	// here. Start should not block — long-running work belongs in a
	// daemon registered via [Host.RunDaemon].
	Start(ctx context.Context) error

	// Stop is called once on shutdown. The provided context is cancelled
	// after the host's per-plugin drain timeout (5 seconds in Phase 1).
	// Stop must not block core shutdown indefinitely.
	Stop(ctx context.Context) error
}

// PluginEntry is a single entry in the plugin registry. It pairs the
// plugin's declared name with the factory function that constructs a
// new instance.
type PluginEntry struct {
	// Name is the plugin's stable identifier as passed to [Register].
	Name string

	// Factory constructs a new instance of the plugin. It is called
	// exactly once per binary lifetime by the host during boot.
	Factory func() Plugin
}

var (
	registryMu sync.RWMutex
	registry   []PluginEntry
)

// Register adds a plugin to the package-level registry. It is intended
// to be called from a package init() function so that side-effect
// imports in an enterprise main package wire plugins in automatically:
//
//	import _ "github.com/zulandar/railyard-enterprise/plugins/trainmaster"
//
// Register panics if name is empty or if factory is nil; both indicate
// a programming error in the plugin itself. Registering the same name
// twice is allowed — later registrations replace earlier ones, which is
// useful for testing — but the host will log a WARN on duplicates at
// boot.
func Register(name string, factory func() Plugin) {
	if name == "" {
		panic("plugin.Register: name must not be empty")
	}
	if factory == nil {
		panic("plugin.Register: factory must not be nil")
	}
	registryMu.Lock()
	defer registryMu.Unlock()
	for i := range registry {
		if registry[i].Name == name {
			registry[i].Factory = factory
			return
		}
	}
	registry = append(registry, PluginEntry{Name: name, Factory: factory})
}

// Registered returns a snapshot of every plugin currently in the
// registry, in registration order. The returned slice is a copy; the
// caller is free to mutate it. This is the lookup the host walks during
// boot.
func Registered() []PluginEntry {
	registryMu.RLock()
	defer registryMu.RUnlock()
	out := make([]PluginEntry, len(registry))
	copy(out, registry)
	return out
}
