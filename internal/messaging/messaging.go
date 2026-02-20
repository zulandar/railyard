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
	CarID        string
	ThreadID     *uint
	Priority     string        // "normal" (default), "urgent"
	NotifyConfig *NotifyConfig // if set, fires notification for human/urgent messages
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
		CarID:     opts.CarID,
		ThreadID:  opts.ThreadID,
		Subject:   subject,
		Body:      body,
		Priority:  priority,
		CreatedAt: time.Now(),
	}

	if err := db.Create(&msg).Error; err != nil {
		return nil, fmt.Errorf("messaging: send: %w", err)
	}

	if opts.NotifyConfig != nil && shouldNotify(&msg) {
		go Notify(&msg, *opts.NotifyConfig)
	}

	return &msg, nil
}

// Inbox returns unacknowledged messages for an agent, ordered by priority then
// creation time. It includes both direct messages and broadcast messages that
// the agent has not yet acknowledged.
func Inbox(db *gorm.DB, agentID string) ([]models.Message, error) {
	if agentID == "" {
		return nil, fmt.Errorf("messaging: agentID is required")
	}

	var msgs []models.Message
	if err := db.Where(
		"(to_agent = ? AND acknowledged = ?) OR (to_agent = 'broadcast' AND id NOT IN (SELECT message_id FROM broadcast_acks WHERE agent_id = ?))",
		agentID, false, agentID,
	).Order("priority ASC, created_at ASC").Find(&msgs).Error; err != nil {
		return nil, fmt.Errorf("messaging: inbox %s: %w", agentID, err)
	}
	return msgs, nil
}

// Acknowledge marks a non-broadcast message as acknowledged.
func Acknowledge(db *gorm.DB, messageID uint) error {
	result := db.Model(&models.Message{}).
		Where("id = ? AND to_agent != ?", messageID, "broadcast").
		Update("acknowledged", true)
	if result.Error != nil {
		return fmt.Errorf("messaging: acknowledge %d: %w", messageID, result.Error)
	}
	if result.RowsAffected == 0 {
		return fmt.Errorf("messaging: message not found or is broadcast: %d", messageID)
	}
	return nil
}

// AcknowledgeBroadcast records that an agent has acknowledged a broadcast message.
func AcknowledgeBroadcast(db *gorm.DB, messageID uint, agentID string) error {
	if agentID == "" {
		return fmt.Errorf("messaging: agentID is required")
	}

	ack := models.BroadcastAck{
		MessageID: messageID,
		AgentID:   agentID,
	}
	if err := db.Create(&ack).Error; err != nil {
		return fmt.Errorf("messaging: broadcast ack %d/%s: %w", messageID, agentID, err)
	}
	return nil
}

// GetThread returns all messages in a thread, ordered by creation time.
func GetThread(db *gorm.DB, threadID uint) ([]models.Message, error) {
	if threadID == 0 {
		return nil, fmt.Errorf("messaging: threadID is required")
	}

	var msgs []models.Message
	if err := db.Where("thread_id = ?", threadID).
		Order("created_at ASC").Find(&msgs).Error; err != nil {
		return nil, fmt.Errorf("messaging: get thread %d: %w", threadID, err)
	}
	return msgs, nil
}

// Reply creates a reply to an existing message. The reply is sent to the
// original sender, inherits the thread ID (or starts one using the parent's
// ID), and prefixes the subject with "Re: ".
func Reply(db *gorm.DB, parentMsgID uint, from, body string) (*models.Message, error) {
	if parentMsgID == 0 {
		return nil, fmt.Errorf("messaging: parentMsgID is required")
	}
	if from == "" {
		return nil, fmt.Errorf("messaging: from is required")
	}

	var parent models.Message
	if err := db.First(&parent, parentMsgID).Error; err != nil {
		return nil, fmt.Errorf("messaging: parent message %d: %w", parentMsgID, err)
	}

	threadID := parent.ThreadID
	if threadID == nil {
		threadID = &parent.ID
	}

	return Send(db, from, parent.FromAgent, "Re: "+parent.Subject, body, SendOpts{
		ThreadID: threadID,
		Priority: parent.Priority,
	})
}
