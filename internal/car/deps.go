package car

import (
	"fmt"

	"github.com/zulandar/railyard/internal/models"
	"gorm.io/gorm"
)

// AddDep creates a blocking dependency: carID is blocked by blockedBy.
// Validates both IDs exist, prevents self-dependency, and detects simple cycles.
func AddDep(db *gorm.DB, carID, blockedBy, depType string) error {
	if carID == blockedBy {
		return fmt.Errorf("dep: cannot add self-dependency on %s", carID)
	}
	if depType == "" {
		depType = "blocks"
	}

	// Verify both cars exist
	for _, id := range []string{carID, blockedBy} {
		var count int64
		if err := db.Model(&models.Car{}).Where("id = ?", id).Count(&count).Error; err != nil {
			return fmt.Errorf("dep: check car %s: %w", id, err)
		}
		if count == 0 {
			return fmt.Errorf("dep: car not found: %s", id)
		}
	}

	// Cycle detection: check if blockedBy (directly or transitively) depends on carID.
	if hasCycle(db, carID, blockedBy) {
		return fmt.Errorf("dep: adding %s → %s would create a cycle", carID, blockedBy)
	}

	dep := models.CarDep{
		CarID:    carID,
		BlockedBy: blockedBy,
		DepType:   depType,
	}

	if err := db.Create(&dep).Error; err != nil {
		return fmt.Errorf("dep: create %s → %s: %w", carID, blockedBy, err)
	}
	return nil
}

// ListDeps returns the blockers of a car (what blocks it) and the dependents (what it blocks).
func ListDeps(db *gorm.DB, carID string) (blockers []models.CarDep, dependents []models.CarDep, err error) {
	if err := db.Where("car_id = ?", carID).Find(&blockers).Error; err != nil {
		return nil, nil, fmt.Errorf("dep: list blockers for %s: %w", carID, err)
	}
	if err := db.Where("blocked_by = ?", carID).Find(&dependents).Error; err != nil {
		return nil, nil, fmt.Errorf("dep: list dependents for %s: %w", carID, err)
	}
	return blockers, dependents, nil
}

// RemoveDep deletes a dependency relationship.
func RemoveDep(db *gorm.DB, carID, blockedBy string) error {
	result := db.Where("car_id = ? AND blocked_by = ?", carID, blockedBy).Delete(&models.CarDep{})
	if result.Error != nil {
		return fmt.Errorf("dep: remove %s → %s: %w", carID, blockedBy, result.Error)
	}
	if result.RowsAffected == 0 {
		return fmt.Errorf("dep: dependency %s → %s not found", carID, blockedBy)
	}
	return nil
}

// ReadyCars returns cars that are ready for work: status=open, no assignee,
// and all blockers are done or cancelled. Epics are excluded since they are
// container cars and not directly implementable. Per ARCHITECTURE.md Section 2.
func ReadyCars(db *gorm.DB, track string) ([]models.Car, error) {
	q := db.Where("status = ? AND (assignee = ? OR assignee IS NULL) AND type != ?", "open", "", "epic").
		Where("id NOT IN (?)",
			db.Table("car_deps").
				Select("car_deps.car_id").
				Joins("JOIN cars blocker ON car_deps.blocked_by = blocker.id").
				Where("blocker.status NOT IN ?", []string{"done", "cancelled"}),
		)

	if track != "" {
		q = q.Where("track = ?", track)
	}

	var cars []models.Car
	if err := q.Order("priority ASC, created_at ASC").Find(&cars).Error; err != nil {
		return nil, fmt.Errorf("car: ready: %w", err)
	}
	return cars, nil
}

// hasCycle checks if adding carID → blockedBy would create a cycle.
// It walks the dependency graph from blockedBy to see if it can reach carID.
func hasCycle(db *gorm.DB, carID, blockedBy string) bool {
	visited := make(map[string]bool)
	return reachable(db, blockedBy, carID, visited)
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

	var deps []models.CarDep
	if err := db.Where("car_id = ?", current).Find(&deps).Error; err != nil {
		return false
	}
	for _, d := range deps {
		if reachable(db, d.BlockedBy, target, visited) {
			return true
		}
	}
	return false
}
