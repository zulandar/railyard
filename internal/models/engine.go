package models

import "time"

// Engine represents a worker agent instance.
type Engine struct {
	ID           string    `gorm:"primaryKey;size:64"`
	VMID         string    `gorm:"size:64"`
	Track        string    `gorm:"size:64;index"`
	Role         string    `gorm:"size:16"`
	Status       string    `gorm:"size:16;index"`
	CurrentCar  string    `gorm:"size:32"`
	SessionID    string    `gorm:"size:64"`
	StartedAt    time.Time
	LastActivity time.Time `gorm:"index"`
}
