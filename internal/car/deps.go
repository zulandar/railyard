package car

import (
	"fmt"
	"sort"

	"github.com/zulandar/railyard/internal/models"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// AddDep creates a blocking dependency: carID is blocked by blockedBy.
// Validates both IDs exist, prevents self-dependency, and detects cycles.
//
// The check and insert run in one transaction with the involved car rows
// locked (FOR UPDATE, in sorted ID order to avoid lock-order deadlocks), so
// two concurrent adds A→B / B→A cannot both pass the cycle check before
// either row exists. A committed cycle strands both cars forever — ClaimCar
// and ReadyCars exclude them and nothing sweeps cycles at runtime
// (railyard-9ki). SQLite ignores the locking clause; MySQL/Dolt honor it.
func AddDep(db *gorm.DB, carID, blockedBy, depType string) error {
	if carID == blockedBy {
		return fmt.Errorf("dep: cannot add self-dependency on %s", carID)
	}
	if depType == "" {
		depType = "blocks"
	}

	return db.Transaction(func(tx *gorm.DB) error {
		// Verify both cars exist, locking the rows in sorted order so the
		// cycle check + insert serialize against a concurrent reverse add.
		ids := []string{carID, blockedBy}
		sort.Strings(ids)
		for _, id := range ids {
			var count int64
			if err := tx.Model(&models.Car{}).
				Clauses(clause.Locking{Strength: "UPDATE"}).
				Where("id = ?", id).Count(&count).Error; err != nil {
				return fmt.Errorf("dep: check car %s: %w", id, err)
			}
			if count == 0 {
				return fmt.Errorf("dep: car not found: %s", id)
			}
		}

		// Cycle detection: check if blockedBy (directly or transitively)
		// depends on carID. A DB error fails closed — refusing a legitimate
		// edge is recoverable, a silently committed cycle is not.
		cyclic, err := hasCycle(tx, carID, blockedBy)
		if err != nil {
			return fmt.Errorf("dep: cycle check %s → %s: %w", carID, blockedBy, err)
		}
		if cyclic {
			return fmt.Errorf("dep: adding %s → %s would create a cycle", carID, blockedBy)
		}

		dep := models.CarDep{
			CarID:     carID,
			BlockedBy: blockedBy,
			DepType:   depType,
		}

		if err := tx.Create(&dep).Error; err != nil {
			return fmt.Errorf("dep: create %s → %s: %w", carID, blockedBy, err)
		}
		return nil
	})
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
// and all blockers are resolved (cancelled or merged). Epics are
// excluded since they are container cars and not directly implementable.
// Per ARCHITECTURE.md Section 2.
func ReadyCars(db *gorm.DB, track string) ([]models.Car, error) {
	q := db.Where("status = ? AND (assignee = ? OR assignee IS NULL) AND type != ?", "open", "", "epic").
		Where("id NOT IN (?)",
			db.Table("car_deps").
				Select("car_deps.car_id").
				Joins("JOIN cars blocker ON car_deps.blocked_by = blocker.id").
				Where("blocker.status NOT IN ?", models.ResolvedBlockerStatuses),
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
// DB errors propagate so the caller fails closed instead of reading a
// transient query failure as "no cycle" (railyard-9ki).
func hasCycle(db *gorm.DB, carID, blockedBy string) (bool, error) {
	visited := make(map[string]bool)
	return reachable(db, blockedBy, carID, visited)
}

// reachable performs a DFS from 'current' following blocked_by edges
// to determine if 'target' is reachable.
func reachable(db *gorm.DB, current, target string, visited map[string]bool) (bool, error) {
	if current == target {
		return true, nil
	}
	if visited[current] {
		return false, nil
	}
	visited[current] = true

	var deps []models.CarDep
	if err := db.Where("car_id = ?", current).Find(&deps).Error; err != nil {
		return false, err
	}
	for _, d := range deps {
		found, err := reachable(db, d.BlockedBy, target, visited)
		if err != nil {
			return false, err
		}
		if found {
			return true, nil
		}
	}
	return false, nil
}
