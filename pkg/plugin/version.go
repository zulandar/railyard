package plugin

// SDKVersion is the semantic version of this pkg/plugin SDK. A plugin
// reports it to the host during the Init handshake (carried in the
// InitResponse), and the host surfaces it in `ry plugins status` for
// support diagnostics. Bump it at release time when the SDK surface
// changes.
//
// Version negotiation is informational, not gating: a host never
// refuses a plugin over a version mismatch (railyard-77h.8). The host
// separately advertises the set of event topics it can deliver (see
// [CoreEventTypes]) so the SDK can warn a plugin author who subscribes
// to a topic the running host does not know about.
const SDKVersion = "1.0.0"

// CoreEventTypes returns the closed set of event topics this SDK
// version defines, in declaration order. The host uses it to populate
// the Init-time topic advertisement so the advertised list cannot drift
// from the [EventType] constants. Adding a new topic here is an
// additive (minor) change — see the wire-compat note on [EventType].
//
// The returned slice is a fresh copy; callers may mutate it freely.
func CoreEventTypes() []EventType {
	return []EventType{
		CarCreated,
		CarClaimed,
		CarStatusChanged,
		CarMerged,
		MergeFailed,
		EngineStarted,
		EngineStopped,
		EngineStalled,
		YardmasterAction,
		YardPaused,
		YardResumed,
	}
}
