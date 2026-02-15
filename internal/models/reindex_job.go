package models

import "time"

// ReindexJob tracks Roundhouse re-indexing operations.
type ReindexJob struct {
	ID            uint      `gorm:"primaryKey;autoIncrement"`
	Track         string    `gorm:"size:64;not null"`
	TriggerCommit string    `gorm:"size:40"`
	Status        string    `gorm:"size:16;default:pending"`
	FilesChanged  int
	ChunksUpdated int
	GPUBoxID      string    `gorm:"size:64"`
	StartedAt     *time.Time
	CompletedAt   *time.Time
	CreatedAt     time.Time
	ErrorMessage  string    `gorm:"type:text"`
}
