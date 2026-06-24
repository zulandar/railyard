package models

import "time"

// CarMemory stores track-scoped, keyword-indexed memories associated with a car.
type CarMemory struct {
	ID        uint   `gorm:"primaryKey;autoIncrement"`
	CarID     string `gorm:"size:32;index"`
	Track     string `gorm:"size:64;index"`
	Keyword   string `gorm:"size:128;index"`
	Content   string `gorm:"type:text"`
	CreatedAt time.Time
}
