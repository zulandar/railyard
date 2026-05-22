// Command hello is the minimal railyard plugin example: it subscribes to
// CarCreated events and logs each one.
//
// Under the subprocess plugin model the example is a standalone
// executable. The enterprise/OSS host discovers the built binary in one
// of the well-known plugins.d directories, launches it as a child
// process, and brokers all interaction over a Unix-domain socket.
//
// The main package's only job is to call [plugin.Serve] with an instance
// of the plugin type — Serve performs the handshake on stdin/stdout,
// listens on the host-provided socket, exposes the PluginService gRPC
// server, and blocks for the lifetime of the process.
//
// See docs/plugins/authoring.md §2 for the full authoring walkthrough.
package main

import (
	"context"

	"github.com/zulandar/railyard/pkg/plugin"
)

// Hello is the example plugin. It holds the host handle captured during
// Init so Start can call host.Subscribe and onCarCreated can call
// host.Logger. State shared between Init/Start/Stop lives here.
type Hello struct {
	host plugin.Host
}

// Name returns the plugin's stable identifier. It must match the
// executable basename the host discovers in plugins.d and the top-level
// YAML key the plugin reads from railyard.yaml.
func (h *Hello) Name() string { return "hello" }

// Init runs once after the host has handshook with this process and
// before core subsystems start. The plugin should stash the host handle
// here; reading the plugin's config block via host.Config() also belongs
// in Init. Returning a non-nil error here causes the host to log a WARN
// and skip the plugin for the rest of the binary's lifetime.
func (h *Hello) Init(_ context.Context, host plugin.Host) error {
	h.host = host
	return nil
}

// Start subscribes to CarCreated. Subscription handlers run on a
// dedicated per-subscriber goroutine, so Start returns immediately.
// Long-lived work should be launched as a plain goroutine from Start —
// never in the Start body itself.
func (h *Hello) Start(_ context.Context) error {
	h.host.Subscribe(plugin.CarCreated, h.onCarCreated)
	return nil
}

// onCarCreated runs on the plugin's dedicated subscriber goroutine.
// Keep handler work fast — long work belongs in a goroutine the plugin
// owns (see docs/plugins/authoring.md §9).
func (h *Hello) onCarCreated(_ plugin.EventType, payload any) {
	evt, ok := payload.(plugin.CarCreatedEvent)
	if !ok {
		// The SDK guarantees the dynamic type matches the topic; this
		// path is defensive only.
		return
	}
	h.host.Logger().Info("hello: car created",
		"id", evt.CarID,
		"track", evt.Track,
		"type", evt.Type,
		"priority", evt.Priority,
		"requested_by", evt.RequestedBy,
	)
}

// Stop is called once on shutdown. The host applies a 5-second drain
// timeout on this call; Stop must not block core shutdown indefinitely.
// We have nothing to clean up — the host cancels our event stream
// before Stop fires.
func (h *Hello) Stop(_ context.Context) error { return nil }

// Compile-time assertion that *Hello satisfies plugin.Plugin. Cheap and
// catches signature drift the moment a method signature changes.
var _ plugin.Plugin = (*Hello)(nil)

func main() {
	plugin.Serve(&Hello{})
}
