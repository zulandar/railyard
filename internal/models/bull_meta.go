package models

import "time"

// BullMeta stores key-value metadata for the Bull daemon.
type BullMeta struct {
	Key       string `gorm:"primaryKey;size:64"`
	Value     string `gorm:"type:text"`
	UpdatedAt time.Time
}
