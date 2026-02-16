package yardmaster

import (
	"fmt"
	"time"

	"github.com/zulandar/railyard/internal/messaging"
	"github.com/zulandar/railyard/internal/models"
	"gorm.io/gorm"
)

// DefaultStaleThreshold is the default time after which an engine is considered stale.
const DefaultStaleThreshold = 60 * time.Second

// CheckEngineHealth returns engines where last_activity is older than threshold
// and status is not "dead".
func CheckEngineHealth(db *gorm.DB, threshold time.Duration) ([]models.Engine, error) {
	if db == nil {
		return nil, fmt.Errorf("yardmaster: db is required")
	}
	if threshold <= 0 {
		return nil, fmt.Errorf("yardmaster: threshold must be positive")
	}

	cutoff := time.Now().Add(-threshold)
	var engines []models.Engine
	if err := db.Where("last_activity < ? AND status != ?", cutoff, "dead").
		Find(&engines).Error; err != nil {
		return nil, fmt.Errorf("yardmaster: check engine health: %w", err)
	}
	return engines, nil
}

// StaleEngines is a convenience wrapper using the default 60s threshold.
func StaleEngines(db *gorm.DB) ([]models.Engine, error) {
	return CheckEngineHealth(db, DefaultStaleThreshold)
}

// ReassignCar releases a car from a stalled/dead engine so it can be reclaimed.
// It sets the car status to "open", clears the assignee, marks the old engine
// as dead, writes a progress note, and sends a broadcast notification.
func ReassignCar(db *gorm.DB, carID, fromEngineID, reason string) error {
	if db == nil {
		return fmt.Errorf("yardmaster: db is required")
	}
	if carID == "" {
		return fmt.Errorf("yardmaster: carID is required")
	}
	if fromEngineID == "" {
		return fmt.Errorf("yardmaster: fromEngineID is required")
	}

	// Release the car: set status=open, clear assignee.
	result := db.Model(&models.Car{}).Where("id = ?", carID).Updates(map[string]interface{}{
		"status":   "open",
		"assignee": "",
	})
	if result.Error != nil {
		return fmt.Errorf("yardmaster: release car %s: %w", carID, result.Error)
	}
	if result.RowsAffected == 0 {
		return fmt.Errorf("yardmaster: car %s not found", carID)
	}

	// Mark the engine as dead and clear its current car.
	db.Model(&models.Engine{}).Where("id = ?", fromEngineID).Updates(map[string]interface{}{
		"status":       "dead",
		"current_car": "",
	})

	// Write progress note.
	note := fmt.Sprintf("Reassigned from engine %s: %s", fromEngineID, reason)
	db.Create(&models.CarProgress{
		CarID:       carID,
		EngineID:     fromEngineID,
		Note:         note,
		FilesChanged: "[]",
		CreatedAt:    time.Now(),
	})

	// Send broadcast notification.
	messaging.Send(db, "yardmaster", "broadcast", "reassignment",
		fmt.Sprintf("Car %s reassigned from stalled engine %s", carID, fromEngineID),
		messaging.SendOpts{CarID: carID, Priority: "urgent"},
	)

	return nil
}
