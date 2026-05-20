package yardmaster

import (
	"github.com/zulandar/railyard/internal/events"
	"github.com/zulandar/railyard/pkg/plugin"
)

// publish is a nil-safe forwarder to the event bus. Callers that do not have
// a bus (existing call sites and tests) get a no-op; this preserves the
// "publishing to a nil bus is a no-op" contract from the plugin system spec
// (§6.3) and keeps existing tests green without modification.
//
// Topic strings are derived from the [plugin.EventType] constants so callers
// reference a single source of truth rather than free-form strings.
func publish(bus events.Bus, topic plugin.EventType, payload any) {
	if bus == nil {
		return
	}
	bus.Publish(string(topic), payload)
}
