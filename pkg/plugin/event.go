package plugin

// EventType is the stable string identifier for a railyard event topic.
// It is declared as a defined string type so plugin code can use the
// exported constants below without importing the host's internal bus.
// The plugin host converts EventType to its underlying string when
// routing to the internal event bus.
//
// Topic to payload mapping
//
// Every constant below is paired with exactly one typed payload struct.
// Handlers registered via [Host.Subscribe] receive the payload as an
// untyped any; the implementation guarantees the concrete dynamic type
// matches the topic. Plugin handlers should type-assert as follows:
//
//	[CarCreated]        -> [CarCreatedEvent]
//	[CarClaimed]        -> [CarClaimedEvent]
//	[CarStatusChanged]  -> [CarStatusChangedEvent]
//	[CarMerged]         -> [CarMergedEvent]
//	[MergeFailed]       -> [MergeFailedEvent]
//	[EngineStarted]     -> [EngineStartedEvent]
//	[EngineStopped]     -> [EngineStoppedEvent]
//	[EngineStalled]     -> [EngineStalledEvent]
//	[YardmasterAction]  -> [YardmasterActionEvent]
//	[YardPaused]        -> [YardPausedEvent]
//	[YardResumed]       -> [YardResumedEvent]
//
// Adding a new event type is a breaking SDK change and requires bumping
// the major version of the railyard module.
type EventType string

// Phase 1 closed set of event topics. The numeric ordering has no
// meaning; topics are referenced by name only.
const (
	// CarCreated fires when a new car has been written to the yard.
	// Payload: [CarCreatedEvent].
	CarCreated EventType = "CarCreated"

	// CarClaimed fires when an engine takes ownership of a previously
	// unclaimed car. Payload: [CarClaimedEvent].
	CarClaimed EventType = "CarClaimed"

	// CarStatusChanged fires on every status transition on a car
	// (including transitions into terminal states). Payload:
	// [CarStatusChangedEvent].
	CarStatusChanged EventType = "CarStatusChanged"

	// CarMerged fires when a car has been merged into its target branch.
	// Payload: [CarMergedEvent].
	CarMerged EventType = "CarMerged"

	// MergeFailed fires when a merge attempt failed. Payload:
	// [MergeFailedEvent].
	MergeFailed EventType = "MergeFailed"

	// EngineStarted fires when an engine registers with the yard and
	// begins accepting work. Payload: [EngineStartedEvent].
	EngineStarted EventType = "EngineStarted"

	// EngineStopped fires when an engine shuts down cleanly. Payload:
	// [EngineStoppedEvent].
	EngineStopped EventType = "EngineStopped"

	// EngineStalled fires when the yard detects an engine has stopped
	// reporting activity. Payload: [EngineStalledEvent].
	EngineStalled EventType = "EngineStalled"

	// YardmasterAction fires when the yardmaster takes an action against
	// a car or engine. Payload: [YardmasterActionEvent].
	YardmasterAction EventType = "YardmasterAction"

	// YardPaused fires when an operator pauses the yard. Payload:
	// [YardPausedEvent].
	YardPaused EventType = "YardPaused"

	// YardResumed fires when an operator resumes a paused yard. Payload:
	// [YardResumedEvent].
	YardResumed EventType = "YardResumed"
)

// CarCreatedEvent is the payload for the [CarCreated] topic.
type CarCreatedEvent struct {
	CarID       string
	Track       string
	Type        string
	Priority    int
	RequestedBy string
}

// CarClaimedEvent is the payload for the [CarClaimed] topic.
type CarClaimedEvent struct {
	CarID    string
	EngineID string
}

// CarStatusChangedEvent is the payload for the [CarStatusChanged] topic.
type CarStatusChangedEvent struct {
	CarID     string
	OldStatus string
	NewStatus string
}

// CarMergedEvent is the payload for the [CarMerged] topic.
type CarMergedEvent struct {
	CarID  string
	Branch string
}

// MergeFailedEvent is the payload for the [MergeFailed] topic.
type MergeFailedEvent struct {
	CarID  string
	Reason string
}

// EngineStartedEvent is the payload for the [EngineStarted] topic.
type EngineStartedEvent struct {
	EngineID string
	Track    string
}

// EngineStoppedEvent is the payload for the [EngineStopped] topic.
type EngineStoppedEvent struct {
	EngineID string
}

// EngineStalledEvent is the payload for the [EngineStalled] topic.
// LastActivityUnix is seconds since the Unix epoch, matching the wire
// representation used by trainmaster.
type EngineStalledEvent struct {
	EngineID         string
	LastActivityUnix int64
}

// YardmasterActionEvent is the payload for the [YardmasterAction] topic.
type YardmasterActionEvent struct {
	TargetID   string
	ActionType string
}

// YardPausedEvent is the payload for the [YardPaused] topic.
type YardPausedEvent struct {
	Reason string
}

// YardResumedEvent is the payload for the [YardResumed] topic.
type YardResumedEvent struct {
	Reason string
}
