package engine

import (
	"context"
	"fmt"
	"time"

	"github.com/zulandar/railyard/internal/models"
	"gorm.io/gorm"
)

// DefaultHeartbeatInterval is the default interval between heartbeat updates.
const DefaultHeartbeatInterval = 10 * time.Second

// StartHeartbeat launches a goroutine that periodically updates the engine's
// last_activity timestamp. It returns a channel that receives an error if the
// engine disappears (0 rows affected) or the context is cancelled.
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
				result := db.Model(&models.Engine{}).
					Where("id = ?", engineID).
					Update("last_activity", time.Now())

				if result.Error != nil {
					errCh <- fmt.Errorf("engine: heartbeat %s: %w", engineID, result.Error)
					return
				}
				if result.RowsAffected == 0 {
					errCh <- fmt.Errorf("engine: heartbeat %s: engine not found", engineID)
					return
				}
			}
		}
	}()

	return errCh
}
