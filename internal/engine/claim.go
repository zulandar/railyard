package engine

import (
	"fmt"
	"time"

	"github.com/zulandar/railyard/internal/models"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// ClaimCar atomically finds the highest-priority ready car on the given track
// and assigns it to the engine. It uses SELECT ... FOR UPDATE SKIP LOCKED for
// concurrency safety.
//
// Note: Dolt may not fully support row-level SKIP LOCKED. Correctness is
// preserved via transaction serialization; just lower concurrency.
func ClaimCar(db *gorm.DB, engineID, track string) (*models.Car, error) {
	if engineID == "" {
		return nil, fmt.Errorf("engine: engineID is required")
	}
	if track == "" {
		return nil, fmt.Errorf("engine: track is required")
	}

	var claimed models.Car

	err := db.Transaction(func(tx *gorm.DB) error {
		// Subquery: car IDs that have at least one non-done, non-cancelled blocker.
		blockedSub := tx.Table("car_deps").
			Select("car_deps.car_id").
			Joins("JOIN cars blocker ON car_deps.blocked_by = blocker.id").
			Where("blocker.status NOT IN ?", []string{"done", "cancelled"})

		// Find the highest-priority ready car, locking the row.
		result := tx.Where("status = ? AND (assignee = ? OR assignee IS NULL) AND track = ?", "open", "", track).
			Where("id NOT IN (?)", blockedSub).
			Clauses(clause.Locking{Strength: "UPDATE", Options: "SKIP LOCKED"}).
			Order("priority ASC, created_at ASC").
			Limit(1).
			Find(&claimed)

		if result.Error != nil {
			return fmt.Errorf("engine: find ready car: %w", result.Error)
		}
		if result.RowsAffected == 0 {
			return fmt.Errorf("engine: no ready cars: %w", gorm.ErrRecordNotFound)
		}

		// Update the car: status=claimed, assignee=engineID, claimed_at=now.
		now := time.Now()
		if err := tx.Model(&models.Car{}).Where("id = ?", claimed.ID).Updates(map[string]interface{}{
			"status":     "claimed",
			"assignee":   engineID,
			"claimed_at": now,
		}).Error; err != nil {
			return fmt.Errorf("engine: claim car %s: %w", claimed.ID, err)
		}
		claimed.Status = "claimed"
		claimed.Assignee = engineID
		claimed.ClaimedAt = &now

		// Update the engine: status=working, current_car=car.ID.
		if err := tx.Model(&models.Engine{}).Where("id = ?", engineID).Updates(map[string]interface{}{
			"status":       StatusWorking,
			"current_car": claimed.ID,
		}).Error; err != nil {
			return fmt.Errorf("engine: update engine %s: %w", engineID, err)
		}

		return nil
	})

	if err != nil {
		return nil, err
	}
	return &claimed, nil
}
