package yardmaster

import (
	"context"
	"fmt"
	"io"
	"log"
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
func handleRestartEngine(_ context.Context, db *gorm.DB, _ *config.Config, configPath string, msg models.Message, out io.Writer) {
	if msg.CarID == "" {
		fmt.Fprintf(out, "Action restart-engine: no car-id provided, skipping\n")
		return
	}

	// Find the engine assigned to this car.
	var eng models.Engine
	if err := db.Where("current_car = ? AND status != ?", msg.CarID, "dead").First(&eng).Error; err != nil {
		fmt.Fprintf(out, "Action restart-engine: no active engine found for car %s\n", msg.CarID)
		return
	}

	fmt.Fprintf(out, "Action restart-engine: restarting engine %s (car %s) — %s\n", eng.ID, msg.CarID, msg.Body)

	// Reassign the car back to the pool so another engine can claim it.
	if err := ReassignCar(db, msg.CarID, eng.ID, "dispatch: "+msg.Body); err != nil {
		log.Printf("restart-engine: reassign car %s: %v", msg.CarID, err)
	}

	// Launch a replacement engine on the same track.
	if err := orchestration.RestartEngine(db, configPath, eng.ID, nil); err != nil {
		log.Printf("restart-engine: restart engine %s: %v", eng.ID, err)
		fmt.Fprintf(out, "Action restart-engine: failed to restart %s: %v\n", eng.ID, err)
	}
}

// handleRetryMerge sets a blocked car's status back to "done" so the
// handleCompletedCars phase will pick it up and re-run the switch (merge) flow.
// For epics, it calls TryCloseEpic since epics don't go through the merge flow.
func handleRetryMerge(db *gorm.DB, msg models.Message, out io.Writer) {
	if msg.CarID == "" {
		fmt.Fprintf(out, "Action retry-merge: no car-id provided, skipping\n")
		return
	}

	var car models.Car
	if err := db.Where("id = ?", msg.CarID).First(&car).Error; err != nil {
		fmt.Fprintf(out, "Action retry-merge: car %s not found\n", msg.CarID)
		return
	}

	// Epics don't have branches — they close when all children are done.
	if car.Type == "epic" {
		fmt.Fprintf(out, "Action retry-merge: car %s is an epic, attempting auto-close — %s\n", msg.CarID, msg.Body)
		TryCloseEpic(db, msg.CarID)
		writeProgressNote(db, msg.CarID, "dispatch", fmt.Sprintf("Retry merge (epic auto-close attempted): %s", msg.Body))
		return
	}

	// Only retry regular cars if currently blocked or merge-failed.
	if car.Status != "blocked" && car.Status != "merge-failed" {
		fmt.Fprintf(out, "Action retry-merge: car %s is %q (not blocked/merge-failed), skipping\n", msg.CarID, car.Status)
		return
	}

	fmt.Fprintf(out, "Action retry-merge: setting car %s back to done — %s\n", msg.CarID, msg.Body)

	db.Model(&models.Car{}).Where("id = ?", msg.CarID).Update("status", "done")

	writeProgressNote(db, msg.CarID, "dispatch", fmt.Sprintf("Retry merge requested: %s", msg.Body))
}

// handleRequeueCar sets a car's status to "open" and clears the assignee,
// making it available for any engine to claim from scratch.
func handleRequeueCar(db *gorm.DB, msg models.Message, out io.Writer) {
	if msg.CarID == "" {
		fmt.Fprintf(out, "Action requeue-car: no car-id provided, skipping\n")
		return
	}

	fmt.Fprintf(out, "Action requeue-car: requeuing car %s — %s\n", msg.CarID, msg.Body)

	db.Model(&models.Car{}).Where("id = ?", msg.CarID).Updates(map[string]interface{}{
		"status":   "open",
		"assignee": "",
	})

	writeProgressNote(db, msg.CarID, "dispatch", fmt.Sprintf("Requeued: %s", msg.Body))
}

// handleNudgeEngine forwards guidance from dispatch to the engine currently
// working on the specified car.
func handleNudgeEngine(db *gorm.DB, msg models.Message, out io.Writer) {
	if msg.CarID == "" {
		fmt.Fprintf(out, "Action nudge-engine: no car-id provided, skipping\n")
		return
	}

	// Find the engine assigned to this car.
	var eng models.Engine
	if err := db.Where("current_car = ? AND status != ?", msg.CarID, "dead").First(&eng).Error; err != nil {
		fmt.Fprintf(out, "Action nudge-engine: no active engine found for car %s\n", msg.CarID)
		return
	}

	fmt.Fprintf(out, "Action nudge-engine: sending guidance to %s (car %s)\n", eng.ID, msg.CarID)

	messaging.Send(db, YardmasterID, eng.ID, "guidance", msg.Body,
		messaging.SendOpts{CarID: msg.CarID})
}

// handleUnblockCar transitions a blocked car back to "open" status.
func handleUnblockCar(db *gorm.DB, msg models.Message, out io.Writer) {
	if msg.CarID == "" {
		fmt.Fprintf(out, "Action unblock-car: no car-id provided, skipping\n")
		return
	}

	var car models.Car
	if err := db.Where("id = ?", msg.CarID).First(&car).Error; err != nil {
		fmt.Fprintf(out, "Action unblock-car: car %s not found\n", msg.CarID)
		return
	}
	if car.Status != "blocked" {
		fmt.Fprintf(out, "Action unblock-car: car %s is %q (not blocked), skipping\n", msg.CarID, car.Status)
		return
	}

	fmt.Fprintf(out, "Action unblock-car: unblocking car %s — %s\n", msg.CarID, msg.Body)

	db.Model(&models.Car{}).Where("id = ?", msg.CarID).Update("status", "open")

	writeProgressNote(db, msg.CarID, "dispatch", fmt.Sprintf("Manually unblocked: %s", msg.Body))
}

// handleCloseEpic attempts to auto-close an epic whose children are all complete.
func handleCloseEpic(db *gorm.DB, msg models.Message, out io.Writer) {
	if msg.CarID == "" {
		fmt.Fprintf(out, "Action close-epic: no car-id provided, skipping\n")
		return
	}

	var car models.Car
	if err := db.Where("id = ?", msg.CarID).First(&car).Error; err != nil {
		fmt.Fprintf(out, "Action close-epic: car %s not found\n", msg.CarID)
		return
	}
	if car.Type != "epic" {
		fmt.Fprintf(out, "Action close-epic: car %s is %q (not an epic), skipping\n", msg.CarID, car.Type)
		return
	}

	fmt.Fprintf(out, "Action close-epic: attempting auto-close for epic %s — %s\n", msg.CarID, msg.Body)
	TryCloseEpic(db, msg.CarID)
	writeProgressNote(db, msg.CarID, "dispatch", fmt.Sprintf("Close epic requested: %s", msg.Body))
}

// writeProgressNote creates a CarProgress record documenting an action.
func writeProgressNote(db *gorm.DB, carID, engineID, note string) {
	db.Create(&models.CarProgress{
		CarID:        carID,
		EngineID:     engineID,
		Note:         note,
		FilesChanged: "[]",
		CreatedAt:    time.Now(),
	})
}
