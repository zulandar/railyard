package models

import "time"

// BullIssue tracks a GitHub issue that Bull has triaged or is monitoring.
type BullIssue struct {
	ID              uint   `gorm:"primaryKey;autoIncrement"`
	IssueNumber     int    `gorm:"uniqueIndex"`
	CarID           string `gorm:"size:32;index"`
	LastKnownStatus string `gorm:"size:32"`
	TriageSummary   string `gorm:"type:text"`
	TriageResponse  string `gorm:"type:text"`
	TriageMode      string `gorm:"size:16"`
	LastSyncedAt    *time.Time
	CreatedAt       time.Time
	UpdatedAt       time.Time
}
