package yardmaster

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/zulandar/railyard/internal/config"
	"github.com/zulandar/railyard/internal/events"
	"github.com/zulandar/railyard/internal/messaging"
	"github.com/zulandar/railyard/internal/models"
	"github.com/zulandar/railyard/internal/orchestration"
	"github.com/zulandar/railyard/pkg/plugin"
	"gorm.io/gorm"
)

// handleRestartEngine is a thin wrapper around [handleRestartEngineWithBus]
// that passes a nil bus. Existing tests call this form; the daemon loop uses
// the WithBus variant so it can publish [plugin.YardmasterAction].
func handleRestartEngine(ctx context.Context, db *gorm.DB, cfg *config.Config, configPath string, msg models.Message, logger *slog.Logger) {
	handleRestartEngineWithBus(ctx, db, cfg, configPath, msg, logger, nil)
}

// handleRestartEngineWithBus restarts the engine assigned to the car in msg.CarID.
// It looks up the engine via the car's assignee, reassigns the car back to the
// pool, and launches a replacement engine on the same track.
//
// When bus is non-nil and the restart succeeds, publishes a
// [plugin.YardmasterAction] event with ActionType="restart-engine".
func handleRestartEngineWithBus(_ context.Context, db *gorm.DB, cfg *config.Config, configPath string, msg models.Message, logger *slog.Logger, bus events.Bus) {
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
	reassigned, err := ReassignCar(db, msg.CarID, eng.ID, "dispatch: "+msg.Body)
	if err != nil {
		logger.Error("Action restart-engine: reassign car failed", "car", msg.CarID, "error", err)
		return
	}
	if !reassigned {
		logger.Info("Action restart-engine: car moved on before reassign", "car", msg.CarID)
	}

	// Launch a replacement engine on the same track.
	if err := orchestration.RestartEngine(db, cfg, configPath, eng.ID, nil); err != nil {
		logger.Error("Action restart-engine: failed to restart", "engine", eng.ID, "error", err)
		return
	}

	// Publish AFTER the DB updates land — subscribers see consistent state.
	publish(bus, plugin.YardmasterAction, plugin.YardmasterActionEvent{
		TargetID:   msg.CarID,
		ActionType: "restart-engine",
	})
}

// handleRetryMerge is a thin wrapper around [handleRetryMergeWithBus] that
// passes a nil bus. Existing tests call this form.
func handleRetryMerge(db *gorm.DB, msg models.Message, logger *slog.Logger) {
	handleRetryMergeWithBus(db, msg, logger, nil)
}

// handleRetryMergeWithBus sets a blocked car's status back to "done" so the
// handleCompletedCars phase will pick it up and re-run the switch (merge) flow.
// For epics, it calls TryCloseEpic since epics don't go through the merge flow.
//
// When bus is non-nil, publishes a [plugin.YardmasterAction] event with
// ActionType="retry-merge".
func handleRetryMergeWithBus(db *gorm.DB, msg models.Message, logger *slog.Logger, bus events.Bus) {
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
		publish(bus, plugin.YardmasterAction, plugin.YardmasterActionEvent{
			TargetID:   msg.CarID,
			ActionType: "retry-merge",
		})
		return
	}

	// Only retry regular cars if currently blocked or merge-failed.
	if car.Status != "blocked" && car.Status != "merge-failed" {
		logger.Info("Action retry-merge: car not blocked/merge-failed, skipping", "car", msg.CarID, "status", car.Status)
		return
	}

	// Guard against futile retry loops. Early retries are always allowed — a
	// config/environment fix is a legitimate retry and does not change the
	// branch HEAD — but once a car has been re-armed maxMergeRetries times and
	// keeps failing the merge gate, the cause is almost certainly deterministic
	// (e.g. a test-environment gap). Alert a human instead of looping silently.
	if retries := countMergeRetries(db, msg.CarID); retries >= maxMergeRetries {
		logger.Warn("Action retry-merge: retry limit reached, escalating to human instead of re-arming",
			"car", msg.CarID, "retries", retries, "reason", msg.Body)
		if err := writeProgressNote(db, msg.CarID, "dispatch",
			fmt.Sprintf("Retry merge refused after %d attempts — escalating to human: %s", retries, msg.Body)); err != nil {
			logger.Error("Action retry-merge: progress note failed", "error", err)
		}
		if _, err := messaging.Send(db, YardmasterID, "human", "escalate",
			fmt.Sprintf("Car %s has been retried %d times and keeps failing the merge gate. The cause is likely deterministic (e.g. a test-environment gap), not transient — human intervention needed. Latest reason: %s", msg.CarID, retries, msg.Body),
			messaging.SendOpts{CarID: msg.CarID, Priority: "urgent"}); err != nil {
			logger.Error("Action retry-merge: human escalation message failed", "car", msg.CarID, "error", err)
		}
		publish(bus, plugin.YardmasterAction, plugin.YardmasterActionEvent{
			TargetID:   msg.CarID,
			ActionType: "escalate",
		})
		return
	}

	logger.Info("Action retry-merge: setting car back to done", "car", msg.CarID, "reason", msg.Body)

	if err := db.Model(&models.Car{}).Where("id = ?", msg.CarID).Updates(map[string]interface{}{
		"status":         "done",
		"blocked_reason": "",
	}).Error; err != nil {
		logger.Error("Action retry-merge: update car failed", "car", msg.CarID, "error", err)
		return
	}

	if err := writeProgressNote(db, msg.CarID, "dispatch", fmt.Sprintf("Retry merge requested: %s", msg.Body)); err != nil {
		logger.Error("Action retry-merge: progress note failed", "error", err)
	}

	publish(bus, plugin.YardmasterAction, plugin.YardmasterActionEvent{
		TargetID:   msg.CarID,
		ActionType: "retry-merge",
	})
}

// maxMergeRetries bounds how many times a car may be re-armed for merge via the
// retry-merge action before the loop is treated as futile and handed to a human.
const maxMergeRetries = 3

// countMergeRetries returns how many times a retry-merge has already been
// requested for a car, counted from its retry progress notes.
func countMergeRetries(db *gorm.DB, carID string) int {
	var count int64
	if err := db.Model(&models.CarProgress{}).
		Where("car_id = ? AND note LIKE ?", carID, "Retry merge requested:%").
		Count(&count).Error; err != nil {
		slog.Error("countMergeRetries", "car", carID, "error", err)
		return 0
	}
	return int(count)
}

// handleRequeueCar is a thin wrapper around [handleRequeueCarWithBus] that
// passes a nil bus. Existing tests call this form.
func handleRequeueCar(db *gorm.DB, msg models.Message, logger *slog.Logger) {
	handleRequeueCarWithBus(db, msg, logger, nil)
}

// handleRequeueCarWithBus sets a car's status to "open" and clears the assignee,
// making it available for any engine to claim from scratch.
//
// When bus is non-nil, publishes a [plugin.YardmasterAction] event with
// ActionType="requeue-car".
func handleRequeueCarWithBus(db *gorm.DB, msg models.Message, logger *slog.Logger, bus events.Bus) {
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

	publish(bus, plugin.YardmasterAction, plugin.YardmasterActionEvent{
		TargetID:   msg.CarID,
		ActionType: "requeue-car",
	})
}

// handleNudgeEngine is a thin wrapper around [handleNudgeEngineWithBus] that
// passes a nil bus. Existing tests call this form.
func handleNudgeEngine(db *gorm.DB, msg models.Message, logger *slog.Logger) {
	handleNudgeEngineWithBus(db, msg, logger, nil)
}

// handleNudgeEngineWithBus forwards guidance from dispatch to the engine currently
// working on the specified car.
//
// When bus is non-nil, publishes a [plugin.YardmasterAction] event with
// ActionType="nudge-engine".
func handleNudgeEngineWithBus(db *gorm.DB, msg models.Message, logger *slog.Logger, bus events.Bus) {
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
		return
	}

	publish(bus, plugin.YardmasterAction, plugin.YardmasterActionEvent{
		TargetID:   msg.CarID,
		ActionType: "nudge-engine",
	})
}

// handleUnblockCar is a thin wrapper around [handleUnblockCarWithBus] that
// passes a nil bus. Existing tests call this form.
func handleUnblockCar(db *gorm.DB, msg models.Message, logger *slog.Logger) {
	handleUnblockCarWithBus(db, msg, logger, nil)
}

// handleUnblockCarWithBus transitions a blocked car back to "open" status.
//
// When bus is non-nil, publishes a [plugin.YardmasterAction] event with
// ActionType="unblock-car".
func handleUnblockCarWithBus(db *gorm.DB, msg models.Message, logger *slog.Logger, bus events.Bus) {
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

	if err := db.Model(&models.Car{}).Where("id = ?", msg.CarID).Updates(map[string]interface{}{
		"status":         "open",
		"blocked_reason": "",
	}).Error; err != nil {
		logger.Error("Action unblock-car: update car failed", "car", msg.CarID, "error", err)
		return
	}

	if err := writeProgressNote(db, msg.CarID, "dispatch", fmt.Sprintf("Manually unblocked: %s", msg.Body)); err != nil {
		logger.Error("Action unblock-car: progress note failed", "error", err)
	}

	publish(bus, plugin.YardmasterAction, plugin.YardmasterActionEvent{
		TargetID:   msg.CarID,
		ActionType: "unblock-car",
	})
}

// handleCloseEpic is a thin wrapper around [handleCloseEpicWithBus] that
// passes a nil bus. Existing tests call this form.
func handleCloseEpic(db *gorm.DB, msg models.Message, logger *slog.Logger) {
	handleCloseEpicWithBus(db, msg, logger, nil)
}

// handleCloseEpicWithBus attempts to auto-close an epic whose children are all
// complete.
//
// When bus is non-nil, publishes a [plugin.YardmasterAction] event with
// ActionType="close-epic".
func handleCloseEpicWithBus(db *gorm.DB, msg models.Message, logger *slog.Logger, bus events.Bus) {
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

	publish(bus, plugin.YardmasterAction, plugin.YardmasterActionEvent{
		TargetID:   msg.CarID,
		ActionType: "close-epic",
	})
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
