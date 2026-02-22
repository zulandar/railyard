// Package car provides car lifecycle operations.
package car

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/zulandar/railyard/internal/models"
	"gorm.io/gorm"
)

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
var ValidTransitions = map[string][]string{
	"draft":        {"open"},
	"open":         {"ready", "cancelled", "blocked"},
	"ready":        {"claimed", "blocked"},
	"claimed":      {"in_progress", "blocked"},
	"in_progress":  {"done", "blocked"},
	"done":         {"merged", "merge-failed"},
	"blocked":      {"open", "ready"},
	"merge-failed": {"done"},
}

// GenerateID creates a unique car ID in car-xxxxx format (5-char hex).
func GenerateID() (string, error) {
	b := make([]byte, 3)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("car: generate ID: %w", err)
	}
	return "car-" + hex.EncodeToString(b)[:5], nil
}

// ComputeBranch builds the git branch name for a car.
func ComputeBranch(branchPrefix, track, id string) string {
	return fmt.Sprintf("%s/%s/%s", branchPrefix, track, id)
}

// Create creates a new car with an auto-generated ID.
func Create(db *gorm.DB, opts CreateOpts) (*models.Car, error) {
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

	id, err := generateUniqueID(db)
	if err != nil {
		return nil, err
	}

	car := models.Car{
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
		Branch:      ComputeBranch(opts.BranchPrefix, opts.Track, id),
	}

	if opts.ParentID != "" {
		car.ParentID = &opts.ParentID
	}

	if err := db.Create(&car).Error; err != nil {
		return nil, fmt.Errorf("car: create: %w", err)
	}

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

// Update modifies car fields. Status transitions are validated against ValidTransitions.
func Update(db *gorm.DB, id string, updates map[string]interface{}) error {
	var car models.Car
	if err := db.Where("id = ?", id).First(&car).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return fmt.Errorf("car: not found: %s", id)
		}
		return fmt.Errorf("car: get %s for update: %w", id, err)
	}

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

	if err := db.Model(&models.Car{}).Where("id = ?", id).Updates(updates).Error; err != nil {
		return fmt.Errorf("car: update %s: %w", id, err)
	}
	return nil
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
func Publish(db *gorm.DB, id string, recursive bool) (int, error) {
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
	}

	// Recursively publish draft children.
	if recursive {
		var children []models.Car
		if err := db.Where("parent_id = ? AND status = ?", id, "draft").Find(&children).Error; err != nil {
			return count, fmt.Errorf("car: list draft children of %s: %w", id, err)
		}
		for _, child := range children {
			n, err := Publish(db, child.ID, true)
			if err != nil {
				return count, err
			}
			count += n
		}
	}

	return count, nil
}

// generateUniqueID generates an ID and retries once on collision.
func generateUniqueID(db *gorm.DB) (string, error) {
	for range 2 {
		id, err := GenerateID()
		if err != nil {
			return "", err
		}
		var count int64
		if err := db.Model(&models.Car{}).Where("id = ?", id).Count(&count).Error; err != nil {
			return "", fmt.Errorf("car: check ID uniqueness: %w", err)
		}
		if count == 0 {
			return id, nil
		}
	}
	return "", fmt.Errorf("car: failed to generate unique ID after retries")
}
