package models

import "time"

// DispatchSession tracks an active or completed dispatch session initiated
// from Telegraph (chat) or local CLI. The dispatch lock system uses this
// model to prevent concurrent sessions on the same thread/channel.
type DispatchSession struct {
	ID               uint      `gorm:"primaryKey;autoIncrement"`
	Source           string    `gorm:"size:16;not null;index"` // "telegraph" or "local"
	UserName         string    `gorm:"size:64;not null"`
	PlatformThreadID string    `gorm:"size:128;index:idx_thread_channel"`
	ChannelID        string    `gorm:"size:128;index:idx_thread_channel"`
	Status           string    `gorm:"size:16;default:active;index"` // active, completed, expired
	CarsCreated      string    `gorm:"type:json"`                    // JSON array of car IDs
	LastHeartbeat    time.Time `gorm:"index"`
	CreatedAt        time.Time
	CompletedAt      *time.Time

	Conversations []TelegraphConversation `gorm:"foreignKey:SessionID"`
}

// TelegraphConversation stores a single message in a dispatch session's
// conversation history. Used for session recovery when a subprocess dies.
type TelegraphConversation struct {
	ID             uint   `gorm:"primaryKey;autoIncrement"`
	SessionID      uint   `gorm:"not null;index"`
	Sequence       int    `gorm:"not null"`
	Role           string `gorm:"size:16;not null"` // "user", "assistant", "system"
	UserName       string `gorm:"size:64"`
	Content        string `gorm:"type:mediumtext;not null"`
	PlatformMsgID  string `gorm:"size:128"`
	CarsReferenced string `gorm:"type:json"` // JSON array of car IDs
	CreatedAt      time.Time

	Session DispatchSession `gorm:"foreignKey:SessionID"`
}
