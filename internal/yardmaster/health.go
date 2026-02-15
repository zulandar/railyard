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

// ReassignBead releases a bead from a stalled/dead engine so it can be reclaimed.
// It sets the bead status to "open", clears the assignee, marks the old engine
// as dead, writes a progress note, and sends a broadcast notification.
func ReassignBead(db *gorm.DB, beadID, fromEngineID, reason string) error {
	if db == nil {
		return fmt.Errorf("yardmaster: db is required")
	}
	if beadID == "" {
		return fmt.Errorf("yardmaster: beadID is required")
	}
	if fromEngineID == "" {
		return fmt.Errorf("yardmaster: fromEngineID is required")
	}

	// Release the bead: set status=open, clear assignee.
	result := db.Model(&models.Bead{}).Where("id = ?", beadID).Updates(map[string]interface{}{
		"status":   "open",
		"assignee": "",
	})
	if result.Error != nil {
		return fmt.Errorf("yardmaster: release bead %s: %w", beadID, result.Error)
	}
	if result.RowsAffected == 0 {
		return fmt.Errorf("yardmaster: bead %s not found", beadID)
	}

	// Mark the engine as dead and clear its current bead.
	db.Model(&models.Engine{}).Where("id = ?", fromEngineID).Updates(map[string]interface{}{
		"status":       "dead",
		"current_bead": "",
	})

	// Write progress note.
	note := fmt.Sprintf("Reassigned from engine %s: %s", fromEngineID, reason)
	db.Create(&models.BeadProgress{
		BeadID:       beadID,
		EngineID:     fromEngineID,
		Note:         note,
		FilesChanged: "[]",
		CreatedAt:    time.Now(),
	})

	// Send broadcast notification.
	messaging.Send(db, "yardmaster", "broadcast", "reassignment",
		fmt.Sprintf("Bead %s reassigned from stalled engine %s", beadID, fromEngineID),
		messaging.SendOpts{BeadID: beadID, Priority: "urgent"},
	)

	return nil
}
