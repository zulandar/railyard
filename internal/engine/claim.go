package engine

import (
	"fmt"
	"log/slog"
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
// MySQL does not fully support row-level SKIP LOCKED and falls back to
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
			// Subquery: car IDs that have at least one unresolved blocker.
			blockedSub := tx.Table("car_deps").
				Select("car_deps.car_id").
				Joins("JOIN cars blocker ON car_deps.blocked_by = blocker.id").
				Where("blocker.status NOT IN ?", models.ResolvedBlockerStatuses)

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
			slog.Info("engine: claimed car",
				"engine", engineID,
				"car", claimed.ID,
				"track", track,
				"priority", claimed.Priority,
			)
			return &claimed, nil
		}

		if strings.Contains(lastErr.Error(), "no ready cars") {
			// No claimable car — the common idle-poll path, not a failure.
			// Return a clean message (no "retries" noise) that still wraps
			// gorm.ErrRecordNotFound so callers' errors.Is checks hold
			// (railyard-j0j).
			return nil, fmt.Errorf("engine: no ready cars on track %q: %w", track, gorm.ErrRecordNotFound)
		}

		if !isSerializationError(lastErr) {
			return nil, lastErr
		}

		// Retryable serialization failure — backoff with jitter and try again.
		// Skip the sleep on the final attempt: it would just delay the return.
		if attempt == claimMaxRetries-1 {
			break
		}
		slog.Warn("engine: claim serialization conflict, retrying",
			"engine", engineID,
			"track", track,
			"attempt", attempt+1,
			"max_retries", claimMaxRetries,
			"error", lastErr,
		)
		jitter := time.Duration(50+rand.IntN(150)) * time.Millisecond
		time.Sleep(jitter)
	}

	return nil, fmt.Errorf("engine: claim failed after %d retries: %w", claimMaxRetries, lastErr)
}

// MarkInProgress transitions a car from claimed to in_progress as the engine
// spawns the agent subprocess, so reporting surfaces (ry status, dashboard,
// telegraph digest) show the car as actively worked and ry complete's
// claimed/in_progress guard passes (railyard-rsy).
//
// The update is conditional on the car still being claimed by this engine, so
// it is safe against concurrent reassignment and idempotent across re-claim
// cycles (a car already in_progress is a no-op). Returns whether the
// transition happened; an error only on DB failure.
func MarkInProgress(db *gorm.DB, carID, engineID string) (bool, error) {
	if carID == "" {
		return false, fmt.Errorf("engine: carID is required")
	}
	if engineID == "" {
		return false, fmt.Errorf("engine: engineID is required")
	}

	result := db.Model(&models.Car{}).
		Where("id = ? AND status = ? AND assignee = ?", carID, "claimed", engineID).
		Update("status", "in_progress")
	if result.Error != nil {
		return false, fmt.Errorf("engine: mark car %s in_progress: %w", carID, result.Error)
	}
	if result.RowsAffected > 0 {
		slog.Info("engine: car in progress", "engine", engineID, "car", carID)
	}
	return result.RowsAffected > 0, nil
}

// isSerializationError checks if an error is a MySQL serialization failure
// (Error 1213) or deadlock that should be retried.
func isSerializationError(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "1213") || strings.Contains(msg, "serialization failure") || strings.Contains(msg, "Deadlock")
}
