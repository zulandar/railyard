package plugin

import (
	goplugin "github.com/hashicorp/go-plugin"
)

// ProtocolVersion is the wire version of the railyard plugin protocol.
// Both the host and the plugin must agree on this value before
// go-plugin permits a connection. Increment on any wire-incompatible
// change to the proto contract under pkg/plugin/proto.
//
// This is intentionally distinct from the proto package version: it
// guards the *envelope* (handshake + service registration shape), while
// the proto package version (v1, v2, ...) guards the message schema.
const ProtocolVersion = 1

// PluginSetKey is the name go-plugin uses to dispense the railyard
// plugin from the PluginSet on either side of the connection. The host
// and the plugin SDK both register under this key.
const PluginSetKey = "railyard-plugin"

// MagicCookieKey is the environment variable name used by go-plugin to
// guard against a binary being executed as a plugin by accident. The
// host sets it before exec; the plugin process checks for it. Mismatch
// produces a human-readable error instead of mysterious behaviour.
const MagicCookieKey = "RAILYARD_PLUGIN_MAGIC_COOKIE"

// MagicCookieValue is the expected value of MagicCookieKey. It is not a
// security boundary — anyone with access to the host can read it — it is
// purely a UX guard that catches "I ran my plugin binary by mistake".
const MagicCookieValue = "railyard-plugin-v1"

// HostBrokerID is the well-known go-plugin broker stream ID on which the
// host serves its HostService. The plugin SDK dials this ID to obtain a
// HostServiceClient back-channel into the host process.
//
// This is a coordination contract between the SDK and the host
// implementation; bumping it would require both sides to update in
// lockstep. Defined as a constant so the host package (railyard-fll.3)
// imports the same value rather than duplicating a literal.
const HostBrokerID uint32 = 1

// HandshakeConfig is the magic-cookie pair the host and plugin must
// agree on for go-plugin to accept the connection. Plugin author code
// does not need to reference this directly — plugin.Serve wires it in
// automatically — but the railyard host package imports it so the two
// sides stay in sync.
var HandshakeConfig = goplugin.HandshakeConfig{
	ProtocolVersion:  ProtocolVersion,
	MagicCookieKey:   MagicCookieKey,
	MagicCookieValue: MagicCookieValue,
}
