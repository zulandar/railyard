// Package messaging provides agent communication primitives.
package messaging

import (
	"fmt"
	"time"

	"github.com/zulandar/railyard/internal/models"
	"gorm.io/gorm"
)

// SendOpts holds optional parameters for sending a message.
type SendOpts struct {
	BeadID   string
	ThreadID *uint
	Priority string // "normal" (default), "urgent"
}

// Send creates a new message from one agent to another.
func Send(db *gorm.DB, from, to, subject, body string, opts SendOpts) (*models.Message, error) {
	if from == "" {
		return nil, fmt.Errorf("messaging: from is required")
	}
	if to == "" {
		return nil, fmt.Errorf("messaging: to is required")
	}
	if subject == "" {
		return nil, fmt.Errorf("messaging: subject is required")
	}

	priority := opts.Priority
	if priority == "" {
		priority = "normal"
	}

	msg := models.Message{
		FromAgent: from,
		ToAgent:   to,
		BeadID:    opts.BeadID,
		ThreadID:  opts.ThreadID,
		Subject:   subject,
		Body:      body,
		Priority:  priority,
		CreatedAt: time.Now(),
	}

	if err := db.Create(&msg).Error; err != nil {
		return nil, fmt.Errorf("messaging: send: %w", err)
	}

	return &msg, nil
}

// Inbox returns unacknowledged messages for an agent, ordered by creation time.
func Inbox(db *gorm.DB, agentID string) ([]models.Message, error) {
	if agentID == "" {
		return nil, fmt.Errorf("messaging: agentID is required")
	}

	var msgs []models.Message
	if err := db.Where("to_agent = ? AND acknowledged = ?", agentID, false).
		Order("created_at ASC").Find(&msgs).Error; err != nil {
		return nil, fmt.Errorf("messaging: inbox %s: %w", agentID, err)
	}
	return msgs, nil
}

// Acknowledge marks a message as acknowledged.
func Acknowledge(db *gorm.DB, messageID uint) error {
	result := db.Model(&models.Message{}).Where("id = ?", messageID).
		Update("acknowledged", true)
	if result.Error != nil {
		return fmt.Errorf("messaging: acknowledge %d: %w", messageID, result.Error)
	}
	if result.RowsAffected == 0 {
		return fmt.Errorf("messaging: message not found: %d", messageID)
	}
	return nil
}
