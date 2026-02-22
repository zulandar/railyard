package telegraph

import (
	"fmt"
	"time"

	"github.com/zulandar/railyard/internal/models"
	"gorm.io/gorm"
)

// DefaultHeartbeatTimeout is the duration after which a session's heartbeat
// is considered stale and the lock can be reclaimed.
const DefaultHeartbeatTimeout = 90 * time.Second

// AcquireLock attempts to acquire a dispatch lock for the given source,
// user, thread, and channel. It first expires any stale sessions (heartbeat
// older than timeout), then checks for an existing active session on the
// same thread/channel. If no active session exists, a new one is created.
//
// Returns the new DispatchSession on success, or an error if an active
// session already holds the lock.
func AcquireLock(db *gorm.DB, source, userName, threadID, channelID string, timeout time.Duration) (*models.DispatchSession, error) {
	if timeout <= 0 {
		timeout = DefaultHeartbeatTimeout
	}

	var session *models.DispatchSession

	err := db.Transaction(func(tx *gorm.DB) error {
		cutoff := time.Now().Add(-timeout)

		// Expire stale active sessions on this thread/channel.
		if err := tx.Model(&models.DispatchSession{}).
			Where("status = ? AND last_heartbeat < ? AND platform_thread_id = ? AND channel_id = ?",
				"active", cutoff, threadID, channelID).
			Updates(map[string]interface{}{
				"status":       "expired",
				"completed_at": time.Now(),
			}).Error; err != nil {
			return fmt.Errorf("expire stale sessions: %w", err)
		}

		// Check for an existing active session on this thread/channel.
		var existing models.DispatchSession
		result := tx.Where("status = ? AND platform_thread_id = ? AND channel_id = ?",
			"active", threadID, channelID).First(&existing)
		if result.Error == nil {
			return fmt.Errorf("dispatch lock held by %q (session %d)", existing.UserName, existing.ID)
		}
		if result.Error != gorm.ErrRecordNotFound {
			return fmt.Errorf("check existing session: %w", result.Error)
		}

		// No active session — create a new one.
		now := time.Now()
		session = &models.DispatchSession{
			Source:           source,
			UserName:         userName,
			PlatformThreadID: threadID,
			ChannelID:        channelID,
			Status:           "active",
			CarsCreated:      "[]",
			LastHeartbeat:    now,
		}
		if err := tx.Create(session).Error; err != nil {
			return fmt.Errorf("create session: %w", err)
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("telegraph: acquire lock: %w", err)
	}
	return session, nil
}

// ReleaseLock marks the session as completed and sets CompletedAt.
func ReleaseLock(db *gorm.DB, sessionID uint) error {
	now := time.Now()
	result := db.Model(&models.DispatchSession{}).
		Where("id = ? AND status = ?", sessionID, "active").
		Updates(map[string]interface{}{
			"status":       "completed",
			"completed_at": now,
		})
	if result.Error != nil {
		return fmt.Errorf("telegraph: release lock: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return fmt.Errorf("telegraph: release lock: session %d not found or not active", sessionID)
	}
	return nil
}

// Heartbeat refreshes the LastHeartbeat timestamp for an active session.
func Heartbeat(db *gorm.DB, sessionID uint) error {
	result := db.Model(&models.DispatchSession{}).
		Where("id = ? AND status = ?", sessionID, "active").
		Update("last_heartbeat", time.Now())
	if result.Error != nil {
		return fmt.Errorf("telegraph: heartbeat: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return fmt.Errorf("telegraph: heartbeat: session %d not found or not active", sessionID)
	}
	return nil
}
