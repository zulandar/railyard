package models

import "time"

// CarMemory stores track-scoped, keyword-indexed memories associated with a car.
type CarMemory struct {
	ID        uint   `gorm:"primaryKey;autoIncrement"`
	CarID     string `gorm:"size:32;uniqueIndex:idx_car_memories_lookup"`
	Track     string `gorm:"size:64;uniqueIndex:idx_car_memories_lookup"`
	Keyword   string `gorm:"size:128;uniqueIndex:idx_car_memories_lookup"`
	Content   string `gorm:"type:text"`
	CreatedAt time.Time
}
