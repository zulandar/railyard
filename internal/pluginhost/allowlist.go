package pluginhost

import (
	"strings"

	"github.com/zulandar/railyard/internal/config"
)

// AllowList is the host-side resolved capability allow-list for a single
// plugin. It is constructed from [config.AllowConfig] at Init time and
// consulted on every Subscribe and DispatchCommand to gate runtime
// behaviour.
//
// The zero value is the strict default: every advertised capability is
// denied. Wildcard semantics match [config.AllowConfig]:
//
//   - Events: "*" matches all topics; otherwise literal match.
//   - Commands: "*" matches all; "ns.*" matches any name with the
//     "ns." prefix; otherwise literal match.
//
// The same Commands list controls BOTH what the plugin may expose
// (provide_commands at Init) AND what it may invoke via
// HostService.DispatchCommand. See AllowConfig godoc.
type AllowList struct {
	events   []string
	commands []string
}

// newAllowList constructs an AllowList from a config.AllowConfig. The
// caller's slices are copied so the AllowList is decoupled from any
// later config mutation.
func newAllowList(a config.AllowConfig) AllowList {
	return AllowList{
		events:   append([]string(nil), a.Events...),
		commands: append([]string(nil), a.Commands...),
	}
}

// Events returns a copy of the raw event allow-list entries. Used by
// log/diagnostic surfaces; enforcement uses AllowEvent.
func (a AllowList) Events() []string {
	return append([]string(nil), a.events...)
}

// Commands returns a copy of the raw command allow-list entries.
func (a AllowList) Commands() []string {
	return append([]string(nil), a.commands...)
}

// AllowEvent reports whether the given event topic is allowed by the
// list. An empty list denies everything.
func (a AllowList) AllowEvent(topic string) bool {
	for _, e := range a.events {
		if e == "*" {
			return true
		}
		if e == topic {
			return true
		}
	}
	return false
}

// AllowCommand reports whether the given command name is allowed. An
// empty list denies everything. "*" matches all; "ns.*" matches any
// name with that prefix; otherwise literal match.
func (a AllowList) AllowCommand(name string) bool {
	for _, c := range a.commands {
		if c == "*" {
			return true
		}
		if strings.HasSuffix(c, ".*") {
			prefix := c[:len(c)-1] // keep the trailing dot so "ns." matches "ns.foo" but not "nsfoo"
			if strings.HasPrefix(name, prefix) {
				return true
			}
			continue
		}
		if c == name {
			return true
		}
	}
	return false
}

// IsEmpty reports whether the allow-list has no entries (the strict
// default that denies every capability).
func (a AllowList) IsEmpty() bool {
	return len(a.events) == 0 && len(a.commands) == 0
}
