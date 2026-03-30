package yardmaster

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/zulandar/railyard/internal/config"
	"github.com/zulandar/railyard/internal/messaging"
	"github.com/zulandar/railyard/internal/models"
	"github.com/zulandar/railyard/internal/orchestration"
	"gorm.io/gorm"
)

// handleRestartEngine restarts the engine assigned to the car in msg.CarID.
// It looks up the engine via the car's assignee, reassigns the car back to the
// pool, and launches a replacement engine on the same track.
func handleRestartEngine(_ context.Context, db *gorm.DB, cfg *config.Config, configPath string, msg models.Message, logger *slog.Logger) {
	if msg.CarID == "" {
		logger.Info("Action restart-engine: no car-id provided, skipping")
		return
	}

	// Find the engine assigned to this car.
	var eng models.Engine
	if err := db.Where("current_car = ? AND status != ?", msg.CarID, "dead").First(&eng).Error; err != nil {
		logger.Info("Action restart-engine: no active engine found", "car", msg.CarID)
		return
	}

	logger.Info("Action restart-engine: restarting engine", "engine", eng.ID, "car", msg.CarID, "reason", msg.Body)

	// Reassign the car back to the pool so another engine can claim it.
	if err := ReassignCar(db, msg.CarID, eng.ID, "dispatch: "+msg.Body); err != nil {
		logger.Error("Action restart-engine: reassign car failed", "car", msg.CarID, "error", err)
		return
	}

	// Launch a replacement engine on the same track.
	if err := orchestration.RestartEngine(db, cfg, configPath, eng.ID, nil); err != nil {
		logger.Error("Action restart-engine: failed to restart", "engine", eng.ID, "error", err)
	}
}

// handleRetryMerge sets a blocked car's status back to "done" so the
// handleCompletedCars phase will pick it up and re-run the switch (merge) flow.
// For epics, it calls TryCloseEpic since epics don't go through the merge flow.
func handleRetryMerge(db *gorm.DB, msg models.Message, logger *slog.Logger) {
	if msg.CarID == "" {
		logger.Info("Action retry-merge: no car-id provided, skipping")
		return
	}

	var car models.Car
	if err := db.Where("id = ?", msg.CarID).First(&car).Error; err != nil {
		logger.Info("Action retry-merge: car not found", "car", msg.CarID)
		return
	}

	// Epics don't have branches — they close when all children are done.
	if car.Type == "epic" {
		logger.Info("Action retry-merge: car is an epic, attempting auto-close", "car", msg.CarID, "reason", msg.Body)
		TryCloseEpic(db, msg.CarID)
		if err := writeProgressNote(db, msg.CarID, "dispatch", fmt.Sprintf("Retry merge (epic auto-close attempted): %s", msg.Body)); err != nil {
			logger.Error("Action retry-merge: progress note failed", "error", err)
		}
		return
	}

	// Only retry regular cars if currently blocked or merge-failed.
	if car.Status != "blocked" && car.Status != "merge-failed" {
		logger.Info("Action retry-merge: car not blocked/merge-failed, skipping", "car", msg.CarID, "status", car.Status)
		return
	}

	logger.Info("Action retry-merge: setting car back to done", "car", msg.CarID, "reason", msg.Body)

	if err := db.Model(&models.Car{}).Where("id = ?", msg.CarID).Update("status", "done").Error; err != nil {
		logger.Error("Action retry-merge: update car failed", "car", msg.CarID, "error", err)
		return
	}

	if err := writeProgressNote(db, msg.CarID, "dispatch", fmt.Sprintf("Retry merge requested: %s", msg.Body)); err != nil {
		logger.Error("Action retry-merge: progress note failed", "error", err)
	}
}

// handleRequeueCar sets a car's status to "open" and clears the assignee,
// making it available for any engine to claim from scratch.
func handleRequeueCar(db *gorm.DB, msg models.Message, logger *slog.Logger) {
	if msg.CarID == "" {
		logger.Info("Action requeue-car: no car-id provided, skipping")
		return
	}

	logger.Info("Action requeue-car: requeuing car", "car", msg.CarID, "reason", msg.Body)

	if err := db.Model(&models.Car{}).Where("id = ?", msg.CarID).Updates(map[string]interface{}{
		"status":   "open",
		"assignee": "",
	}).Error; err != nil {
		logger.Error("Action requeue-car: update car failed", "car", msg.CarID, "error", err)
		return
	}

	if err := writeProgressNote(db, msg.CarID, "dispatch", fmt.Sprintf("Requeued: %s", msg.Body)); err != nil {
		logger.Error("Action requeue-car: progress note failed", "error", err)
	}
}

// handleNudgeEngine forwards guidance from dispatch to the engine currently
// working on the specified car.
func handleNudgeEngine(db *gorm.DB, msg models.Message, logger *slog.Logger) {
	if msg.CarID == "" {
		logger.Info("Action nudge-engine: no car-id provided, skipping")
		return
	}

	// Find the engine assigned to this car.
	var eng models.Engine
	if err := db.Where("current_car = ? AND status != ?", msg.CarID, "dead").First(&eng).Error; err != nil {
		logger.Info("Action nudge-engine: no active engine found", "car", msg.CarID)
		return
	}

	logger.Info("Action nudge-engine: sending guidance", "engine", eng.ID, "car", msg.CarID)

	if _, err := messaging.Send(db, YardmasterID, eng.ID, "guidance", msg.Body,
		messaging.SendOpts{CarID: msg.CarID}); err != nil {
		logger.Error("Action nudge-engine: send guidance failed", "engine", eng.ID, "error", err)
	}
}

// handleUnblockCar transitions a blocked car back to "open" status.
func handleUnblockCar(db *gorm.DB, msg models.Message, logger *slog.Logger) {
	if msg.CarID == "" {
		logger.Info("Action unblock-car: no car-id provided, skipping")
		return
	}

	var car models.Car
	if err := db.Where("id = ?", msg.CarID).First(&car).Error; err != nil {
		logger.Info("Action unblock-car: car not found", "car", msg.CarID)
		return
	}
	if car.Status != "blocked" {
		logger.Info("Action unblock-car: car not blocked, skipping", "car", msg.CarID, "status", car.Status)
		return
	}

	logger.Info("Action unblock-car: unblocking car", "car", msg.CarID, "reason", msg.Body)

	if err := db.Model(&models.Car{}).Where("id = ?", msg.CarID).Update("status", "open").Error; err != nil {
		logger.Error("Action unblock-car: update car failed", "car", msg.CarID, "error", err)
		return
	}

	if err := writeProgressNote(db, msg.CarID, "dispatch", fmt.Sprintf("Manually unblocked: %s", msg.Body)); err != nil {
		logger.Error("Action unblock-car: progress note failed", "error", err)
	}
}

// handleCloseEpic attempts to auto-close an epic whose children are all complete.
func handleCloseEpic(db *gorm.DB, msg models.Message, logger *slog.Logger) {
	if msg.CarID == "" {
		logger.Info("Action close-epic: no car-id provided, skipping")
		return
	}

	var car models.Car
	if err := db.Where("id = ?", msg.CarID).First(&car).Error; err != nil {
		logger.Info("Action close-epic: car not found", "car", msg.CarID)
		return
	}
	if car.Type != "epic" {
		logger.Info("Action close-epic: car is not an epic, skipping", "car", msg.CarID, "type", car.Type)
		return
	}

	logger.Info("Action close-epic: attempting auto-close", "epic", msg.CarID, "reason", msg.Body)
	TryCloseEpic(db, msg.CarID)
	if err := writeProgressNote(db, msg.CarID, "dispatch", fmt.Sprintf("Close epic requested: %s", msg.Body)); err != nil {
		logger.Error("Action close-epic: progress note failed", "error", err)
	}
}

// writeProgressNote creates a CarProgress record documenting an action.
func writeProgressNote(db *gorm.DB, carID, engineID, note string) error {
	if err := db.Create(&models.CarProgress{
		CarID:        carID,
		EngineID:     engineID,
		Note:         note,
		FilesChanged: "[]",
		CreatedAt:    time.Now(),
	}).Error; err != nil {
		return fmt.Errorf("yardmaster: progress note for car %s: %w", carID, err)
	}
	return nil
}
