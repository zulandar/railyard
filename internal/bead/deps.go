package bead

import (
	"fmt"

	"github.com/zulandar/railyard/internal/models"
	"gorm.io/gorm"
)

// AddDep creates a blocking dependency: beadID is blocked by blockedBy.
// Validates both IDs exist, prevents self-dependency, and detects simple cycles.
func AddDep(db *gorm.DB, beadID, blockedBy, depType string) error {
	if beadID == blockedBy {
		return fmt.Errorf("dep: cannot add self-dependency on %s", beadID)
	}
	if depType == "" {
		depType = "blocks"
	}

	// Verify both beads exist
	for _, id := range []string{beadID, blockedBy} {
		var count int64
		if err := db.Model(&models.Bead{}).Where("id = ?", id).Count(&count).Error; err != nil {
			return fmt.Errorf("dep: check bead %s: %w", id, err)
		}
		if count == 0 {
			return fmt.Errorf("dep: bead not found: %s", id)
		}
	}

	// Cycle detection: check if blockedBy (directly or transitively) depends on beadID.
	if hasCycle(db, beadID, blockedBy) {
		return fmt.Errorf("dep: adding %s → %s would create a cycle", beadID, blockedBy)
	}

	dep := models.BeadDep{
		BeadID:    beadID,
		BlockedBy: blockedBy,
		DepType:   depType,
	}

	if err := db.Create(&dep).Error; err != nil {
		return fmt.Errorf("dep: create %s → %s: %w", beadID, blockedBy, err)
	}
	return nil
}

// ListDeps returns the blockers of a bead (what blocks it) and the dependents (what it blocks).
func ListDeps(db *gorm.DB, beadID string) (blockers []models.BeadDep, dependents []models.BeadDep, err error) {
	if err := db.Where("bead_id = ?", beadID).Find(&blockers).Error; err != nil {
		return nil, nil, fmt.Errorf("dep: list blockers for %s: %w", beadID, err)
	}
	if err := db.Where("blocked_by = ?", beadID).Find(&dependents).Error; err != nil {
		return nil, nil, fmt.Errorf("dep: list dependents for %s: %w", beadID, err)
	}
	return blockers, dependents, nil
}

// RemoveDep deletes a dependency relationship.
func RemoveDep(db *gorm.DB, beadID, blockedBy string) error {
	result := db.Where("bead_id = ? AND blocked_by = ?", beadID, blockedBy).Delete(&models.BeadDep{})
	if result.Error != nil {
		return fmt.Errorf("dep: remove %s → %s: %w", beadID, blockedBy, result.Error)
	}
	if result.RowsAffected == 0 {
		return fmt.Errorf("dep: dependency %s → %s not found", beadID, blockedBy)
	}
	return nil
}

// ReadyBeads returns beads that are ready for work: status=open, no assignee,
// and all blockers are done or cancelled. Per ARCHITECTURE.md Section 2.
func ReadyBeads(db *gorm.DB, track string) ([]models.Bead, error) {
	q := db.Where("status = ? AND assignee = ?", "open", "").
		Where("id NOT IN (?)",
			db.Table("bead_deps").
				Select("bead_deps.bead_id").
				Joins("JOIN beads blocker ON bead_deps.blocked_by = blocker.id").
				Where("blocker.status NOT IN ?", []string{"done", "cancelled"}),
		)

	if track != "" {
		q = q.Where("track = ?", track)
	}

	var beads []models.Bead
	if err := q.Order("priority ASC, created_at ASC").Find(&beads).Error; err != nil {
		return nil, fmt.Errorf("bead: ready: %w", err)
	}
	return beads, nil
}

// hasCycle checks if adding beadID → blockedBy would create a cycle.
// It walks the dependency graph from blockedBy to see if it can reach beadID.
func hasCycle(db *gorm.DB, beadID, blockedBy string) bool {
	visited := make(map[string]bool)
	return reachable(db, blockedBy, beadID, visited)
}

// reachable performs a DFS from 'current' following blocked_by edges
// to determine if 'target' is reachable.
func reachable(db *gorm.DB, current, target string, visited map[string]bool) bool {
	if current == target {
		return true
	}
	if visited[current] {
		return false
	}
	visited[current] = true

	var deps []models.BeadDep
	if err := db.Where("bead_id = ?", current).Find(&deps).Error; err != nil {
		return false
	}
	for _, d := range deps {
		if reachable(db, d.BlockedBy, target, visited) {
			return true
		}
	}
	return false
}
