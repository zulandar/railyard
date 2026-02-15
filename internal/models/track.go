package models

// Track defines an area of concern within the repo.
type Track struct {
	Name         string `gorm:"primaryKey;size:64"`
	Language     string `gorm:"size:32"`
	Conventions  string `gorm:"type:json"`
	SystemPrompt string `gorm:"type:text"`
	FilePatterns string `gorm:"type:json"`
	EngineSlots  int    `gorm:"default:3"`
	Active       bool   `gorm:"default:true"`
}
