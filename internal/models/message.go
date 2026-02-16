package models

import "time"

// Message represents agent-to-agent communication.
type Message struct {
	ID           uint      `gorm:"primaryKey;autoIncrement"`
	FromAgent    string    `gorm:"size:64;not null"`
	ToAgent      string    `gorm:"size:64;not null;index"`
	CarID       string    `gorm:"size:32"`
	ThreadID     *uint
	Subject      string    `gorm:"size:256"`
	Body         string    `gorm:"type:text"`
	Priority     string    `gorm:"size:8;default:normal"`
	Acknowledged bool      `gorm:"default:false;index"`
	CreatedAt    time.Time
}

// BroadcastAck tracks which agents have acknowledged a broadcast message.
type BroadcastAck struct {
	MessageID uint   `gorm:"primaryKey"`
	AgentID   string `gorm:"primaryKey;size:64"`
}
