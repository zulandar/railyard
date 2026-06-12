package engine

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/zulandar/railyard/internal/models"
	"gorm.io/gorm"
)

// DefaultHeartbeatInterval is the default interval between heartbeat updates.
const DefaultHeartbeatInterval = 10 * time.Second

// ErrMarkedDead is delivered on the heartbeat channel when the engine's own
// row was set to status=dead by an external actor (scale-down, RestartEngine,
// yardmaster stale-marking). Engine IDs are unique per process, so a dead
// mark on our row is always a request for this process to stop. The daemon
// treats it as a graceful drain, not a failure (railyard-7em / railyard-8m6).
var ErrMarkedDead = errors.New("engine: marked dead externally")

// StartHeartbeat launches a goroutine that periodically updates the engine's
// last_activity timestamp. It returns a channel that receives an error if the
// engine disappears (0 rows affected), the row is marked dead ([ErrMarkedDead]),
// or the heartbeat write fails. The channel stays silent on context cancel.
//
// Note: the previous behavior self-healed dead -> idle on every tick. That
// silently resurrected engines the yardmaster had deliberately marked dead
// (after reassigning their car), producing two engines on one car, and made
// scale-down's dead mark a no-op. An engine marked dead must drain instead.
func StartHeartbeat(ctx context.Context, db *gorm.DB, engineID string, interval time.Duration) <-chan error {
	if interval <= 0 {
		interval = DefaultHeartbeatInterval
	}

	errCh := make(chan error, 1)

	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				now := time.Now()
				var status string
				result := db.Model(&models.Engine{}).
					Where("id = ?", engineID).
					Update("last_activity", now)
				if result.Error != nil {
					errCh <- fmt.Errorf("engine: heartbeat %s: %w", engineID, result.Error)
					return
				}
				if result.RowsAffected == 0 {
					errCh <- fmt.Errorf("engine: heartbeat %s: engine not found", engineID)
					return
				}

				if err := db.Model(&models.Engine{}).
					Select("status").
					Where("id = ?", engineID).
					Scan(&status).Error; err != nil {
					errCh <- fmt.Errorf("engine: heartbeat %s: read status: %w", engineID, err)
					return
				}
				if status == StatusDead {
					errCh <- fmt.Errorf("engine: heartbeat %s: %w", engineID, ErrMarkedDead)
					return
				}
			}
		}
	}()

	return errCh
}
