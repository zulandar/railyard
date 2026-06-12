// Package car provides car lifecycle operations.
package car

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/zulandar/railyard/internal/events"
	"github.com/zulandar/railyard/internal/models"
	"github.com/zulandar/railyard/pkg/plugin"
	"gorm.io/gorm"
)

// publish is a nil-safe forwarder to the event bus. Callers that do not have
// a bus (existing call sites and tests) get a no-op; this preserves the
// "publishing to a nil bus is a no-op" contract from the plugin system spec
// (§6.3) and keeps existing tests green without modification.
func publish(bus events.Bus, topic plugin.EventType, payload any) {
	if bus == nil {
		return
	}
	bus.Publish(string(topic), payload)
}

// CreateOpts holds parameters for creating a new car.
type CreateOpts struct {
	Title        string
	Description  string
	Type         string // task, epic, bug, spike
	Priority     int    // 0=critical → 4=backlog
	Track        string
	ParentID     string
	DesignNotes  string
	Acceptance   string
	SkipTests    bool
	BranchPrefix string // e.g., "ry/alice"
	BaseBranch   string // base branch for merging (empty = "main")
	RequestedBy  string // who requested this car (username or owner)
}

// ListFilters holds optional filters for listing cars.
type ListFilters struct {
	Track    string
	Status   string
	Type     string
	Assignee string
	ParentID string
}

// StatusCount holds a status and its count for children summaries.
type StatusCount struct {
	Status string
	Count  int
}

// ValidTransitions maps each status to its valid next statuses.
// The special case "any → blocked" is handled in isValidTransition.
//
// This map is the source of truth for the car state machine: it must contain
// every transition any production call site performs (raw db writes included),
// so operators can replay each of them through `ry car update --status`.
// TestValidTransitions_ProductionEdges pins each edge to its call site
// (railyard-knm).
//
// Non-obvious edges:
//   - claimed → done: ry complete when the engine died before marking the
//     car in_progress.
//   - claimed/in_progress → open: yardmaster ReassignCar off a stale engine.
//   - open/ready/claimed/in_progress → merged: reconcileStaleCars detecting a
//     branch merged externally; open → merged/done also covers epic auto-close.
//   - blocked → done: UnblockDeps test-failed retry and the retry-merge action.
//   - done → pr_open → pr_review: PR mode + inspect review claims.
var ValidTransitions = map[string][]string{
	"draft":        {"open"},
	"open":         {"ready", "cancelled", "blocked", "done", "merged"},
	"ready":        {"claimed", "blocked", "merged"},
	"claimed":      {"in_progress", "done", "open", "blocked", "merged"},
	"in_progress":  {"done", "open", "blocked", "merged"},
	"done":         {"merged", "merge-failed", "pr_open"},
	"blocked":      {"open", "ready", "done"},
	"merge-failed": {"done", "cancelled"},
	"pr_open":      {"open", "merged", "cancelled", "pr_review"},
	"pr_review":    {"pr_open", "merged", "cancelled"},
}

// GenerateID creates a random car ID in car-xxxxxxxx format (8-char hex).
// 32 bits of randomness keeps birthday collisions negligible at realistic
// car counts; the previous 5-char/20-bit space hit ~50% collision odds by
// ~1,200 cars (railyard-sos). IDs are opaque strings — existing shorter IDs
// remain valid.
func GenerateID() (string, error) {
	b := make([]byte, 4)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("car: generate ID: %w", err)
	}
	return "car-" + hex.EncodeToString(b), nil
}

// generateID is swapped out by tests to force ID collisions.
var generateID = GenerateID

// ComputeBranch builds the git branch name for a car. An empty or
// whitespace-only prefix yields "track/id" — never a leading slash, which
// would be an invalid git ref that only fails much later in the engine
// (railyard-d5f).
func ComputeBranch(branchPrefix, track, id string) string {
	branchPrefix = strings.TrimSpace(branchPrefix)
	if branchPrefix == "" {
		return fmt.Sprintf("%s/%s", track, id)
	}
	return fmt.Sprintf("%s/%s/%s", branchPrefix, track, id)
}

// validCarTypes is the set of car types CreateOpts documents; type drives
// epic-only behaviors (parent validation, switch skip), so a typo'd type
// silently misbehaves if accepted (railyard-d5f).
var validCarTypes = map[string]bool{
	"task":  true,
	"epic":  true,
	"bug":   true,
	"spike": true,
}

// Create creates a new car with an auto-generated ID.
// Equivalent to CreateWithBus(db, nil, opts) — no events are published.
func Create(db *gorm.DB, opts CreateOpts) (*models.Car, error) {
	return CreateWithBus(db, nil, opts)
}

// CreateWithBus creates a new car and, on success, publishes a [plugin.CarCreated]
// event to bus. Passing a nil bus is equivalent to [Create].
//
// The publish happens AFTER the DB write commits so subscribers see consistent
// state. Per spec §6.3 ("publishing to a nil bus is a no-op"), existing call
// sites that use [Create] continue to work unchanged.
func CreateWithBus(db *gorm.DB, bus events.Bus, opts CreateOpts) (*models.Car, error) {
	if opts.Title == "" {
		return nil, fmt.Errorf("car: title is required")
	}

	// Validate parent and inherit track if needed (before track check).
	if opts.ParentID != "" {
		var parent models.Car
		if err := db.Where("id = ?", opts.ParentID).First(&parent).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return nil, fmt.Errorf("car: parent not found: %s", opts.ParentID)
			}
			return nil, fmt.Errorf("car: check parent %s: %w", opts.ParentID, err)
		}
		if parent.Type != "epic" {
			return nil, fmt.Errorf("car: parent %s is type %q, only epics can have children", opts.ParentID, parent.Type)
		}
		if opts.Track == "" {
			opts.Track = parent.Track
		}
	}

	if opts.Track == "" {
		return nil, fmt.Errorf("car: track is required")
	}

	if opts.Type == "" {
		opts.Type = "task"
	}
	if !validCarTypes[opts.Type] {
		return nil, fmt.Errorf("car: invalid type %q (valid: task, epic, bug, spike)", opts.Type)
	}

	// Insert with retry on duplicate-key: the old COUNT-then-INSERT check was
	// racy — two concurrent creators drawing the same ID both passed count==0
	// and the loser got a raw duplicate-key error (railyard-sos).
	var car models.Car
	const maxIDAttempts = 5
	for attempt := 0; ; attempt++ {
		id, err := generateID()
		if err != nil {
			return nil, err
		}

		car = models.Car{
			ID:          id,
			Title:       opts.Title,
			Description: opts.Description,
			Type:        opts.Type,
			Status:      "draft",
			Priority:    opts.Priority,
			Track:       opts.Track,
			BaseBranch:  opts.BaseBranch,
			DesignNotes: opts.DesignNotes,
			Acceptance:  opts.Acceptance,
			SkipTests:   opts.SkipTests,
			RequestedBy: opts.RequestedBy,
			Branch:      ComputeBranch(opts.BranchPrefix, opts.Track, id),
		}
		if opts.ParentID != "" {
			car.ParentID = &opts.ParentID
		}

		err = db.Create(&car).Error
		if err == nil {
			break
		}
		if isDuplicateKeyError(err) && attempt < maxIDAttempts-1 {
			continue // ID collision — draw a fresh one
		}
		return nil, fmt.Errorf("car: create: %w", err)
	}

	publish(bus, plugin.CarCreated, plugin.CarCreatedEvent{
		CarID:       car.ID,
		Track:       car.Track,
		Type:        car.Type,
		Priority:    car.Priority,
		RequestedBy: car.RequestedBy,
	})

	return &car, nil
}

// Get retrieves a car by ID, preloading Deps and Progress.
func Get(db *gorm.DB, id string) (*models.Car, error) {
	var car models.Car
	if err := db.Preload("Deps").Preload("Progress").Where("id = ?", id).First(&car).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, fmt.Errorf("car: not found: %s", id)
		}
		return nil, fmt.Errorf("car: get %s: %w", id, err)
	}
	return &car, nil
}

// List returns cars matching the given filters, ordered by priority then creation time.
func List(db *gorm.DB, filters ListFilters) ([]models.Car, error) {
	q := db.Model(&models.Car{})

	if filters.Track != "" {
		q = q.Where("track = ?", filters.Track)
	}
	if filters.Status != "" {
		q = q.Where("status = ?", filters.Status)
	}
	if filters.Type != "" {
		q = q.Where("type = ?", filters.Type)
	}
	if filters.Assignee != "" {
		q = q.Where("assignee = ?", filters.Assignee)
	}
	if filters.ParentID != "" {
		q = q.Where("parent_id = ?", filters.ParentID)
	}

	var cars []models.Car
	if err := q.Order("priority ASC, created_at ASC").Find(&cars).Error; err != nil {
		return nil, fmt.Errorf("car: list: %w", err)
	}
	return cars, nil
}

// ErrConcurrentModification is returned by Update/UpdateWithBus when a status
// change validated against a snapshot could not be applied because another
// writer changed the car's status between the read and the write. Callers may
// re-read and retry (railyard-5df).
var ErrConcurrentModification = errors.New("car: concurrent modification")

// Update modifies car fields. Status transitions are validated against ValidTransitions.
// Equivalent to UpdateWithBus(db, nil, id, updates) — no events are published.
func Update(db *gorm.DB, id string, updates map[string]interface{}) error {
	return UpdateWithBus(db, nil, id, updates)
}

// UpdateWithBus modifies car fields and, after a successful status transition,
// publishes the corresponding lifecycle events to bus. Passing a nil bus is
// equivalent to [Update].
//
// Events emitted (only when status actually changes and the DB write commits):
//   - [plugin.CarStatusChanged] on every status transition (always)
//   - [plugin.CarClaimed] when the new status is "claimed"
//   - [plugin.CarMerged] when the new status is "merged"
//   - [plugin.MergeFailed] when the new status is "merge-failed"
//
// Publishes happen AFTER the DB commit so subscribers see consistent state,
// and at most once per transition.
func UpdateWithBus(db *gorm.DB, bus events.Bus, id string, updates map[string]interface{}) error {
	var car models.Car
	if err := db.Where("id = ?", id).First(&car).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return fmt.Errorf("car: not found: %s", id)
		}
		return fmt.Errorf("car: get %s for update: %w", id, err)
	}

	oldStatus := car.Status

	if newStatus, ok := updates["status"].(string); ok {
		if !isValidTransition(car.Status, newStatus) {
			valid := ValidTransitions[car.Status]
			return fmt.Errorf("car: invalid status transition from %q to %q; valid transitions: %v", car.Status, newStatus, valid)
		}

		now := time.Now()
		if newStatus == "claimed" {
			updates["claimed_at"] = now
		}
		if newStatus == "done" {
			updates["completed_at"] = now
		}
	}

	// For status changes, the UPDATE is conditional on the status the
	// transition was validated against — a concurrent writer (yardmaster
	// reassign, reconcile, unblock, another CLI invocation) landing between
	// the read and the write would otherwise be clobbered by a stale-validated
	// transition (railyard-5df). RowsAffected==0 means the car moved on.
	q := db.Model(&models.Car{}).Where("id = ?", id)
	statusChanging := false
	if _, ok := updates["status"].(string); ok {
		statusChanging = true
		q = q.Where("status = ?", oldStatus)
	}
	result := q.Updates(updates)
	if result.Error != nil {
		return fmt.Errorf("car: update %s: %w", id, result.Error)
	}
	if statusChanging && result.RowsAffected == 0 {
		return fmt.Errorf("car: update %s: status changed from %q since read: %w", id, oldStatus, ErrConcurrentModification)
	}

	if newStatus, ok := updates["status"].(string); ok {
		slog.Info("car: status transition", "car", id, "from", oldStatus, "to", newStatus)

		// Publish only if the status actually changed. Defensive — Update's
		// transition validator already rejects no-op transitions for known
		// statuses, but pinning this here keeps the contract explicit.
		if newStatus != oldStatus {
			publish(bus, plugin.CarStatusChanged, plugin.CarStatusChangedEvent{
				CarID:     id,
				OldStatus: oldStatus,
				NewStatus: newStatus,
			})

			switch newStatus {
			case "claimed":
				// Engine ID is the new assignee, supplied alongside the status
				// in the same updates map. If absent (caller bug), publish with
				// an empty EngineID rather than silently dropping the event.
				engineID, _ := updates["assignee"].(string)
				publish(bus, plugin.CarClaimed, plugin.CarClaimedEvent{
					CarID:    id,
					EngineID: engineID,
				})
			case "merged":
				// Branch is captured from the pre-update Car snapshot — the
				// branch itself isn't part of the merge transition so the
				// pre-update value is correct.
				publish(bus, plugin.CarMerged, plugin.CarMergedEvent{
					CarID:  id,
					Branch: car.Branch,
				})
			case "merge-failed":
				// Reason may be carried in updates["blocked_reason"] (engine
				// reports the failure via this field) or absent. Either way,
				// publish — subscribers can correlate with logs.
				reason, _ := updates["blocked_reason"].(string)
				publish(bus, plugin.MergeFailed, plugin.MergeFailedEvent{
					CarID:  id,
					Reason: reason,
				})
			}
		}
	}

	return nil
}

// IsValidTransition reports whether moving a car from status from to
// status to is allowed under [ValidTransitions]. The special case
// "any → blocked" is always permitted. Exposed so callers that need
// to drive the status update through their own transaction (e.g.
// pkg/cli/pluginhost.forceCompleteAdapter, which atomically pairs the
// transition with an audit row) can reproduce the same validation
// gate Update applies.
func IsValidTransition(from, to string) bool {
	return isValidTransition(from, to)
}

// isValidTransition checks whether a status transition is allowed.
func isValidTransition(from, to string) bool {
	if to == "blocked" {
		return true
	}
	valid, ok := ValidTransitions[from]
	if !ok {
		return false
	}
	for _, v := range valid {
		if v == to {
			return true
		}
	}
	return false
}

// GetChildren returns all children of a parent car, ordered by priority then creation time.
func GetChildren(db *gorm.DB, parentID string) ([]models.Car, error) {
	// Verify parent exists.
	var count int64
	if err := db.Model(&models.Car{}).Where("id = ?", parentID).Count(&count).Error; err != nil {
		return nil, fmt.Errorf("car: check parent %s: %w", parentID, err)
	}
	if count == 0 {
		return nil, fmt.Errorf("car: parent not found: %s", parentID)
	}

	var children []models.Car
	if err := db.Where("parent_id = ?", parentID).Order("priority ASC, created_at ASC").Find(&children).Error; err != nil {
		return nil, fmt.Errorf("car: get children of %s: %w", parentID, err)
	}
	return children, nil
}

// ChildrenSummary returns status counts for all children of a parent car.
func ChildrenSummary(db *gorm.DB, parentID string) ([]StatusCount, error) {
	var results []StatusCount
	if err := db.Model(&models.Car{}).
		Select("status, COUNT(*) as count").
		Where("parent_id = ?", parentID).
		Group("status").
		Order("status ASC").
		Find(&results).Error; err != nil {
		return nil, fmt.Errorf("car: children summary of %s: %w", parentID, err)
	}
	return results, nil
}

// Publish transitions a car from draft to open. If recursive is true and the
// car is an epic, all draft children are also published. Returns the count of
// cars published.
// Equivalent to PublishWithBus(db, nil, id, recursive) — no events are published.
func Publish(db *gorm.DB, id string, recursive bool) (int, error) {
	return PublishWithBus(db, nil, id, recursive)
}

// PublishWithBus transitions a car (and, optionally, its draft children) from
// draft to open and emits one [plugin.CarStatusChanged] event per actual
// transition. Passing a nil bus is equivalent to [Publish].
func PublishWithBus(db *gorm.DB, bus events.Bus, id string, recursive bool) (int, error) {
	var c models.Car
	if err := db.Where("id = ?", id).First(&c).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return 0, fmt.Errorf("car: not found: %s", id)
		}
		return 0, fmt.Errorf("car: get %s for publish: %w", id, err)
	}

	count := 0

	// Publish the car itself if it's in draft.
	if c.Status == "draft" {
		if err := db.Model(&models.Car{}).Where("id = ?", id).Update("status", "open").Error; err != nil {
			return 0, fmt.Errorf("car: publish %s: %w", id, err)
		}
		count++
		publish(bus, plugin.CarStatusChanged, plugin.CarStatusChangedEvent{
			CarID:     id,
			OldStatus: "draft",
			NewStatus: "open",
		})
	}

	// Recursively publish draft children.
	if recursive {
		var children []models.Car
		if err := db.Where("parent_id = ? AND status = ?", id, "draft").Find(&children).Error; err != nil {
			return count, fmt.Errorf("car: list draft children of %s: %w", id, err)
		}
		for _, child := range children {
			n, err := PublishWithBus(db, bus, child.ID, true)
			if err != nil {
				return count, err
			}
			count += n
		}
	}

	return count, nil
}

// isDuplicateKeyError reports whether err is a primary-key collision from
// the cars insert: gorm's translated error, MySQL error 1062, or sqlite's
// UNIQUE constraint message.
func isDuplicateKeyError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, gorm.ErrDuplicatedKey) {
		return true
	}
	msg := err.Error()
	return strings.Contains(msg, "1062") ||
		strings.Contains(msg, "Duplicate entry") ||
		strings.Contains(msg, "UNIQUE constraint failed")
}
