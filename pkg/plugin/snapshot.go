package plugin

import "time"

// YardInfo is the static metadata describing this railyard instance.
// All fields are fixed for the lifetime of the binary — they are read
// once by the host at boot and returned verbatim from [Host.YardInfo].
//
// Plugins typically call [Host.YardInfo] once during Init and stash the
// result. The trainmaster connector uses RailyardVersion and RepoURL to
// populate identity fields in its outbound RegisterRequest.
type YardInfo struct {
	// YardID is the operator-configured yard identifier from
	// railyard.yaml.
	YardID string

	// Owner is the configured owner of this yard (typically a GitHub
	// org or user). From railyard.yaml.
	Owner string

	// Project is the configured project name. May be empty. From
	// railyard.yaml.
	Project string

	// RepoURL is the URL of the repository this yard manages. From
	// railyard.yaml's `repo:` field.
	RepoURL string

	// RailyardVersion is the version of the railyard core module the
	// binary was built against, resolved at boot via
	// runtime/debug.ReadBuildInfo.
	RailyardVersion string

	// BuildCommit is the git commit hash the binary was built from on
	// a best-effort basis. Empty if not set at build time.
	BuildCommit string

	// BuildTime is the wall-clock time the binary was built on a
	// best-effort basis. Zero value if not set at build time.
	BuildTime time.Time
}

// Snapshot is the current operational state of the yard, gathered in a
// single read transaction. It is intended for heartbeat-style consumers
// that re-send full state to an external system on a cadence.
//
// See the railyard plugin spec §7.2 for the upsert semantics:
// [CarsSnap.Active] is the complete set of currently active cars, and a
// car missing from a later snapshot has transitioned to a terminal
// state (whose transition was signaled via a [CarMerged], [MergeFailed],
// or [CarStatusChanged] event).
type Snapshot struct {
	// Timestamp is the wall-clock time the snapshot was assembled.
	Timestamp time.Time

	// Tracks is one entry per configured track, with the track's
	// language, slot count, and active engine list.
	Tracks []TrackSnap

	// Engines is one entry per engine currently known to the yard.
	Engines []EngineSnap

	// Cars carries the full set of active cars plus a per-status tally.
	Cars CarsSnap

	// Yardmaster reflects the yardmaster's current status and last
	// action.
	Yardmaster YardmasterSnap

	// Stats holds cheap-to-read counters maintained by core.
	// Time-bucketed metrics are not included; plugins maintain those
	// themselves by subscribing to events.
	Stats SnapStats
}

// TrackSnap describes a single track at snapshot time.
type TrackSnap struct {
	// Name is the configured track name.
	Name string

	// Language is the configured language for the track (e.g. "go",
	// "python").
	Language string

	// Slots is the configured slot count (target engine population).
	Slots int

	// ActiveEngines is the list of engine IDs currently assigned to
	// this track.
	ActiveEngines []string
}

// EngineSnap describes a single engine at snapshot time.
type EngineSnap struct {
	// ID is the engine's unique identifier.
	ID string

	// Track is the track the engine is assigned to.
	Track string

	// Status is the engine's current status string (e.g. "idle",
	// "working", "stalled").
	Status string

	// CurrentCar is the ID of the car the engine is processing, or
	// empty if the engine is idle.
	CurrentCar string

	// LastActivity is the wall-clock time the engine last reported
	// activity. Zero value if never observed.
	LastActivity time.Time
}

// CarsSnap is the cars section of a [Snapshot].
//
// Active is the complete list of non-terminal cars (status is one of
// open, ready, claimed, in_progress, blocked). This is the "full state"
// for upsert-style consumers like trainmaster — every active car the
// yard knows about, every snapshot. Terminal-state cars (done, merged,
// cancelled) are signaled via the corresponding bus events and are NOT
// included here.
//
// Counts is a convenience tally keyed by status string. It includes
// every status value present in the yard, including terminal ones, so
// plugins can render dashboards without re-querying.
type CarsSnap struct {
	// Active is the full set of currently-active (non-terminal) cars.
	// A car present in snapshot N but missing from snapshot N+1 has
	// transitioned to a terminal state. The transition event was
	// already published; consumers should treat missing entries as
	// terminal.
	Active []CarSummary

	// Counts is the tally of cars keyed by status string. All statuses
	// present in the yard are included, terminal and non-terminal.
	Counts map[string]int
}

// CarSummary is a flat projection of the most commonly needed fields
// on a car. It intentionally does not expose the full car aggregate;
// plugins that need additional fields should request a richer surface
// in a follow-up SDK change.
type CarSummary struct {
	// ID is the car's unique identifier.
	ID string

	// Title is the human-readable title of the car.
	Title string

	// Track is the track the car is assigned to.
	Track string

	// Status is the car's current status string.
	Status string

	// Type is the car type (e.g. "feature", "bug").
	Type string

	// Priority is the car's numeric priority. Higher values indicate
	// higher priority.
	Priority int

	// Assignee is the ID of the engine currently working the car, or
	// empty if the car is unclaimed.
	Assignee string

	// Branch is the git branch associated with the car.
	Branch string

	// RequestedBy identifies the originator of the car (typically a
	// user or system that created the car).
	RequestedBy string

	// CreatedAt is the wall-clock time the car was created.
	CreatedAt time.Time

	// ClaimedAt is the wall-clock time the car was first claimed by an
	// engine. Nil if the car has not been claimed.
	ClaimedAt *time.Time
}

// YardmasterSnap describes the yardmaster at snapshot time.
type YardmasterSnap struct {
	// Status is the yardmaster's current status string (e.g. "running",
	// "paused").
	Status string

	// LastAction is a human-readable description of the most recent
	// action taken by the yardmaster.
	LastAction string

	// LastActionAt is the wall-clock time of the most recent action.
	// Zero value if no action has been recorded.
	LastActionAt time.Time
}

// SnapStats holds the cheap-to-read counters captured in a [Snapshot].
// Counters here are maintained inexpensively by core; time-bucketed or
// aggregated metrics (e.g. "cars completed today") are intentionally
// out of scope and are the plugin's responsibility to maintain.
type SnapStats struct {
	// EngineCountsByStatus is the tally of engines keyed by status
	// string.
	EngineCountsByStatus map[string]int
}
