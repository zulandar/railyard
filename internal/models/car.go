package models

import "time"

// Car is the core work item in Railyard.
type Car struct {
	ID          string  `gorm:"primaryKey;size:32"`
	Title       string  `gorm:"not null"`
	Description string  `gorm:"type:text"`
	Type        string  `gorm:"size:16;default:task"`
	Status      string  `gorm:"size:16;default:draft;index"`
	Priority    int     `gorm:"default:2"`
	Track       string  `gorm:"size:64;index"`
	Assignee    string  `gorm:"size:64"`
	ParentID    *string `gorm:"size:32"`
	Branch      string  `gorm:"size:128"`
	DesignNotes string  `gorm:"type:text"`
	Acceptance  string  `gorm:"type:text"`
	SkipTests   bool    `gorm:"default:false"`
	CreatedAt   time.Time
	UpdatedAt   time.Time
	ClaimedAt   *time.Time
	CompletedAt *time.Time

	Parent   *Car          `gorm:"foreignKey:ParentID"`
	Children []Car         `gorm:"foreignKey:ParentID"`
	Deps     []CarDep      `gorm:"foreignKey:CarID"`
	Progress []CarProgress `gorm:"foreignKey:CarID"`
}

// CarDep represents a blocking relationship between cars.
type CarDep struct {
	CarID     string `gorm:"primaryKey;size:32"`
	BlockedBy string `gorm:"primaryKey;size:32"`
	DepType   string `gorm:"size:16;default:blocks"`

	Car     Car `gorm:"foreignKey:CarID"`
	Blocker Car `gorm:"foreignKey:BlockedBy"`
}

// CarProgress tracks work done across /clear cycles.
type CarProgress struct {
	ID           uint   `gorm:"primaryKey;autoIncrement"`
	CarID        string `gorm:"size:32;index"`
	Cycle        int
	SessionID    string `gorm:"size:64"`
	EngineID     string `gorm:"size:64"`
	Note         string `gorm:"type:text"`
	FilesChanged string `gorm:"type:json"`
	CommitHash   string `gorm:"size:40"`
	CreatedAt    time.Time
}
