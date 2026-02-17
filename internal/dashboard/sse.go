package dashboard

import (
	"encoding/json"
	"fmt"
	"io"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/zulandar/railyard/internal/models"
	"gorm.io/gorm"
)

// sseEvent represents an SSE event to send to the client.
type sseEvent struct {
	Event string `json:"event"`
	Data  any    `json:"data"`
}

// escalationEvent holds data for an escalation SSE event.
type escalationEvent struct {
	ID       uint   `json:"id"`
	From     string `json:"from"`
	Subject  string `json:"subject"`
	CarID    string `json:"car_id,omitempty"`
	Priority string `json:"priority"`
	Count    int64  `json:"count"`
}

// handleSSE creates a real SSE handler that polls for new escalations.
func handleSSE(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Header("Content-Type", "text/event-stream")
		c.Header("Cache-Control", "no-cache")
		c.Header("Connection", "keep-alive")
		c.Header("X-Accel-Buffering", "no")

		// Send connected event.
		writeSSE(c.Writer, "connected", map[string]string{"type": "connected"})
		c.Writer.Flush()

		// If no DB, just send connected and return — tests use nil DB.
		if db == nil {
			return
		}

		// Track what we've already seen.
		var lastSeenID uint

		// Get the current max ID so we only alert on NEW escalations.
		var maxMsg models.Message
		if err := db.Where("to_agent = ? AND acknowledged = ?", "human", false).
			Order("id DESC").Limit(1).First(&maxMsg).Error; err == nil {
			lastSeenID = maxMsg.ID
		}

		ctx := c.Request.Context()
		ticker := time.NewTicker(3 * time.Second)
		heartbeat := time.NewTicker(15 * time.Second)
		defer ticker.Stop()
		defer heartbeat.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-heartbeat.C:
				writeSSE(c.Writer, "heartbeat", map[string]string{
					"timestamp": time.Now().UTC().Format(time.RFC3339),
				})
				c.Writer.Flush()
			case <-ticker.C:
				// Check for new escalations since last check.
				var newMsgs []models.Message
				db.Where("to_agent = ? AND acknowledged = ? AND id > ?", "human", false, lastSeenID).
					Order("id ASC").
					Find(&newMsgs)

				if len(newMsgs) == 0 {
					continue
				}

				// Update lastSeenID.
				lastSeenID = newMsgs[len(newMsgs)-1].ID

				// Get total unacked count.
				var count int64
				db.Model(&models.Message{}).
					Where("to_agent = ? AND acknowledged = ?", "human", false).
					Count(&count)

				// Send an event for the latest escalation.
				latest := newMsgs[len(newMsgs)-1]
				evt := escalationEvent{
					ID:       latest.ID,
					From:     latest.FromAgent,
					Subject:  latest.Subject,
					CarID:    latest.CarID,
					Priority: latest.Priority,
					Count:    count,
				}
				writeSSE(c.Writer, "escalation", evt)
				c.Writer.Flush()
			}
		}
	}
}

// writeSSE writes a single SSE event to the writer.
func writeSSE(w io.Writer, event string, data any) {
	jsonData, err := json.Marshal(data)
	if err != nil {
		return
	}
	fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, string(jsonData))
}
