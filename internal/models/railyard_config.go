package models

// RailyardConfig stores instance-level configuration.
type RailyardConfig struct {
	ID       uint   `gorm:"primaryKey;autoIncrement"`
	Owner    string `gorm:"size:64;uniqueIndex"`
	RepoURL  string `gorm:"type:text;not null"`
	Mode     string `gorm:"size:16;default:local"`
	Settings string `gorm:"type:json"`
}
