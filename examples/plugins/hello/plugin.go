// Package hello is a minimal railyard plugin: it subscribes to CarCreated
// events and logs each one, optionally with a configurable greeting.
//
// This is the working version of the hello-world plugin shown in
// docs/plugins/authoring.md §2. It is built by CI to keep the guide and
// the SDK in sync; if the SDK ever drifts in a way that breaks the
// documented example, the verification test catches it.
package hello

import (
	"context"
	"fmt"

	"github.com/zulandar/railyard/pkg/plugin"
)

// Config is the plugin's top-level YAML block.
type Config struct {
	Enabled  bool   `yaml:"enabled"`
	Greeting string `yaml:"greeting"`
}

// Plugin is the hello plugin. It holds the host handle (captured in Init)
// and any state shared between Init/Start/Stop.
type Plugin struct {
	host  plugin.Host
	cfg   Config
	unsub plugin.Unsubscribe
}

// Name returns the plugin's stable identifier. Must match the name passed
// to plugin.Register below and the top-level YAML key the plugin reads.
func (p *Plugin) Name() string { return "hello" }

// Init reads the plugin's config block, validates it, and stashes the host
// handle for later use. Returning a non-nil error here causes the host to
// log a WARN and skip the plugin for the rest of the binary's lifetime;
// other plugins and core continue normally.
func (p *Plugin) Init(ctx context.Context, h plugin.Host) error {
	p.host = h
	node := h.Config(p.Name())
	if node.Kind != 0 {
		if err := node.Decode(&p.cfg); err != nil {
			return fmt.Errorf("hello: invalid config: %w", err)
		}
	}
	if p.cfg.Greeting == "" {
		p.cfg.Greeting = "hello"
	}
	return nil
}

// Start subscribes to CarCreated. Subscription handlers run on a dedicated
// per-subscriber goroutine, so this Start returns immediately.
func (p *Plugin) Start(ctx context.Context) error {
	if !p.cfg.Enabled {
		return nil
	}
	p.unsub = p.host.Subscribe(plugin.CarCreated, p.onCarCreated)
	return nil
}

// Stop releases the subscription. Stop must not block core shutdown;
// the host applies a 5-second drain timeout on this call.
func (p *Plugin) Stop(ctx context.Context) error {
	if p.unsub != nil {
		p.unsub()
	}
	return nil
}

// onCarCreated runs on the plugin's dedicated subscriber goroutine. Keep
// handler work fast — long work belongs in a daemon (see authoring.md §9).
func (p *Plugin) onCarCreated(topic plugin.EventType, payload any) {
	ev, ok := payload.(plugin.CarCreatedEvent)
	if !ok {
		// The SDK guarantees the dynamic type matches the topic, so this
		// path is defensive only.
		return
	}
	p.host.Logger().Info(
		fmt.Sprintf("%s: car %s created on track %s", p.cfg.Greeting, ev.CarID, ev.Track),
	)
}

// init registers the plugin with the SDK at package import time. The
// enterprise binary's main package does a side-effect import of this
// package, which triggers this init().
func init() {
	plugin.Register("hello", func() plugin.Plugin { return &Plugin{} })
}

// Compile-time assertion that *Plugin satisfies plugin.Plugin. Cheap and
// catches signature drift the moment you change a method.
var _ plugin.Plugin = (*Plugin)(nil)
