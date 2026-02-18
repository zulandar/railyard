package models

import "time"

// AgentLog captures complete I/O for debugging.
type AgentLog struct {
	ID         uint      `gorm:"primaryKey;autoIncrement"`
	EngineID   string    `gorm:"size:64;index:idx_engine_session"`
	SessionID  string    `gorm:"size:64;index:idx_engine_session"`
	CarID     string    `gorm:"size:32;index"`
	Direction  string    `gorm:"size:4"`
	Content    string    `gorm:"type:mediumtext"`
	TokenCount   int
	InputTokens  int
	OutputTokens int
	Model        string `gorm:"size:64"`
	LatencyMs    int
	CreatedAt  time.Time
}
