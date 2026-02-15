// Package bead provides bead lifecycle operations.
package bead

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/zulandar/railyard/internal/models"
	"gorm.io/gorm"
)

// CreateOpts holds parameters for creating a new bead.
type CreateOpts struct {
	Title        string
	Description  string
	Type         string // task, epic, bug, spike
	Priority     int    // 0=critical → 4=backlog
	Track        string
	ParentID     string
	DesignNotes  string
	Acceptance   string
	BranchPrefix string // e.g., "ry/alice"
}

// ListFilters holds optional filters for listing beads.
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
	"open":        {"ready", "cancelled", "blocked"},
	"ready":       {"claimed", "blocked"},
	"claimed":     {"in_progress", "blocked"},
	"in_progress": {"done", "blocked"},
	"blocked":     {"open", "ready"},
}

// GenerateID creates a unique bead ID in be-xxxxx format (5-char hex).
func GenerateID() (string, error) {
	b := make([]byte, 3)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("bead: generate ID: %w", err)
	}
	return "be-" + hex.EncodeToString(b)[:5], nil
}

// ComputeBranch builds the git branch name for a bead.
func ComputeBranch(branchPrefix, track, id string) string {
	return fmt.Sprintf("%s/%s/%s", branchPrefix, track, id)
}

// Create creates a new bead with an auto-generated ID.
func Create(db *gorm.DB, opts CreateOpts) (*models.Bead, error) {
	if opts.Title == "" {
		return nil, fmt.Errorf("bead: title is required")
	}

	// Validate parent and inherit track if needed (before track check).
	if opts.ParentID != "" {
		var parent models.Bead
		if err := db.Where("id = ?", opts.ParentID).First(&parent).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return nil, fmt.Errorf("bead: parent not found: %s", opts.ParentID)
			}
			return nil, fmt.Errorf("bead: check parent %s: %w", opts.ParentID, err)
		}
		if parent.Type != "epic" {
			return nil, fmt.Errorf("bead: parent %s is type %q, only epics can have children", opts.ParentID, parent.Type)
		}
		if opts.Track == "" {
			opts.Track = parent.Track
		}
	}

	if opts.Track == "" {
		return nil, fmt.Errorf("bead: track is required")
	}

	if opts.Type == "" {
		opts.Type = "task"
	}

	id, err := generateUniqueID(db)
	if err != nil {
		return nil, err
	}

	bead := models.Bead{
		ID:          id,
		Title:       opts.Title,
		Description: opts.Description,
		Type:        opts.Type,
		Status:      "open",
		Priority:    opts.Priority,
		Track:       opts.Track,
		DesignNotes: opts.DesignNotes,
		Acceptance:  opts.Acceptance,
		Branch:      ComputeBranch(opts.BranchPrefix, opts.Track, id),
	}

	if opts.ParentID != "" {
		bead.ParentID = &opts.ParentID
	}

	if err := db.Create(&bead).Error; err != nil {
		return nil, fmt.Errorf("bead: create: %w", err)
	}

	return &bead, nil
}

// Get retrieves a bead by ID, preloading Deps and Progress.
func Get(db *gorm.DB, id string) (*models.Bead, error) {
	var bead models.Bead
	if err := db.Preload("Deps").Preload("Progress").Where("id = ?", id).First(&bead).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, fmt.Errorf("bead: not found: %s", id)
		}
		return nil, fmt.Errorf("bead: get %s: %w", id, err)
	}
	return &bead, nil
}

// List returns beads matching the given filters, ordered by priority then creation time.
func List(db *gorm.DB, filters ListFilters) ([]models.Bead, error) {
	q := db.Model(&models.Bead{})

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

	var beads []models.Bead
	if err := q.Order("priority ASC, created_at ASC").Find(&beads).Error; err != nil {
		return nil, fmt.Errorf("bead: list: %w", err)
	}
	return beads, nil
}

// Update modifies bead fields. Status transitions are validated against ValidTransitions.
func Update(db *gorm.DB, id string, updates map[string]interface{}) error {
	var bead models.Bead
	if err := db.Where("id = ?", id).First(&bead).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return fmt.Errorf("bead: not found: %s", id)
		}
		return fmt.Errorf("bead: get %s for update: %w", id, err)
	}

	if newStatus, ok := updates["status"].(string); ok {
		if !isValidTransition(bead.Status, newStatus) {
			valid := ValidTransitions[bead.Status]
			return fmt.Errorf("bead: invalid status transition from %q to %q; valid transitions: %v", bead.Status, newStatus, valid)
		}

		now := time.Now()
		if newStatus == "claimed" {
			updates["claimed_at"] = now
		}
		if newStatus == "done" {
			updates["completed_at"] = now
		}
	}

	if err := db.Model(&models.Bead{}).Where("id = ?", id).Updates(updates).Error; err != nil {
		return fmt.Errorf("bead: update %s: %w", id, err)
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

// GetChildren returns all children of a parent bead, ordered by priority then creation time.
func GetChildren(db *gorm.DB, parentID string) ([]models.Bead, error) {
	// Verify parent exists.
	var count int64
	if err := db.Model(&models.Bead{}).Where("id = ?", parentID).Count(&count).Error; err != nil {
		return nil, fmt.Errorf("bead: check parent %s: %w", parentID, err)
	}
	if count == 0 {
		return nil, fmt.Errorf("bead: parent not found: %s", parentID)
	}

	var children []models.Bead
	if err := db.Where("parent_id = ?", parentID).Order("priority ASC, created_at ASC").Find(&children).Error; err != nil {
		return nil, fmt.Errorf("bead: get children of %s: %w", parentID, err)
	}
	return children, nil
}

// ChildrenSummary returns status counts for all children of a parent bead.
func ChildrenSummary(db *gorm.DB, parentID string) ([]StatusCount, error) {
	var results []StatusCount
	if err := db.Model(&models.Bead{}).
		Select("status, COUNT(*) as count").
		Where("parent_id = ?", parentID).
		Group("status").
		Order("status ASC").
		Find(&results).Error; err != nil {
		return nil, fmt.Errorf("bead: children summary of %s: %w", parentID, err)
	}
	return results, nil
}

// generateUniqueID generates an ID and retries once on collision.
func generateUniqueID(db *gorm.DB) (string, error) {
	for range 2 {
		id, err := GenerateID()
		if err != nil {
			return "", err
		}
		var count int64
		if err := db.Model(&models.Bead{}).Where("id = ?", id).Count(&count).Error; err != nil {
			return "", fmt.Errorf("bead: check ID uniqueness: %w", err)
		}
		if count == 0 {
			return id, nil
		}
	}
	return "", fmt.Errorf("bead: failed to generate unique ID after retries")
}
