// Package audit provides structured audit event logging for administrative actions.
package audit

import "time"

// AuditEvent captures an auditable action (config change, credential rotation, etc.).
type AuditEvent struct {
	ID        uint      `gorm:"primaryKey;autoIncrement"`
	EventType string    `gorm:"size:64;not null;index"`
	Actor     string    `gorm:"size:128;not null"`
	Resource  string    `gorm:"size:256;not null"`
	Detail    string    `gorm:"type:text"`
	CreatedAt time.Time `gorm:"autoCreateTime;index"`
}
