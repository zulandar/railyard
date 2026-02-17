package engine

import (
	"fmt"
	"log"
	"math/rand/v2"
	"strings"
	"time"

	"github.com/zulandar/railyard/internal/models"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const claimMaxRetries = 3

// ClaimCar atomically finds the highest-priority ready car on the given track
// and assigns it to the engine. It uses SELECT ... FOR UPDATE SKIP LOCKED for
// concurrency safety.
//
// Dolt does not fully support row-level SKIP LOCKED and falls back to
// transaction serialization. When two engines race for the same car, the loser
// gets Error 1213 (serialization failure). We retry with jittered backoff.
func ClaimCar(db *gorm.DB, engineID, track string) (*models.Car, error) {
	if engineID == "" {
		return nil, fmt.Errorf("engine: engineID is required")
	}
	if track == "" {
		return nil, fmt.Errorf("engine: track is required")
	}

	var claimed models.Car
	var lastErr error

	for attempt := range claimMaxRetries {
		lastErr = db.Transaction(func(tx *gorm.DB) error {
			// Subquery: car IDs that have at least one non-done, non-cancelled blocker.
			blockedSub := tx.Table("car_deps").
				Select("car_deps.car_id").
				Joins("JOIN cars blocker ON car_deps.blocked_by = blocker.id").
				Where("blocker.status NOT IN ?", []string{"done", "cancelled"})

			// Find the highest-priority ready car, locking the row.
			// Exclude epics — they are container cars, not implementable work.
			result := tx.Where("status = ? AND (assignee = ? OR assignee IS NULL) AND track = ? AND type != ?", "open", "", track, "epic").
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
				"status":      StatusWorking,
				"current_car": claimed.ID,
			}).Error; err != nil {
				return fmt.Errorf("engine: update engine %s: %w", engineID, err)
			}

			return nil
		})

		if lastErr == nil {
			return &claimed, nil
		}

		if !isSerializationError(lastErr) {
			return nil, lastErr
		}

		// Retryable serialization failure — backoff with jitter and try again.
		log.Printf("[engine] ClaimCar: serialization conflict (attempt %d/%d), retrying: %v", attempt+1, claimMaxRetries, lastErr)
		jitter := time.Duration(50+rand.IntN(150)) * time.Millisecond
		time.Sleep(jitter)
	}

	return nil, fmt.Errorf("engine: claim failed after %d retries: %w", claimMaxRetries, lastErr)
}

// isSerializationError checks if an error is a MySQL/Dolt serialization failure
// (Error 1213) or deadlock that should be retried.
func isSerializationError(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "1213") || strings.Contains(msg, "serialization failure") || strings.Contains(msg, "Deadlock")
}
