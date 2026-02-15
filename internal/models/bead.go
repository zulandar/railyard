package models

import "time"

// Bead is the core work item in Railyard.
type Bead struct {
	ID          string     `gorm:"primaryKey;size:32"`
	Title       string     `gorm:"not null"`
	Description string     `gorm:"type:text"`
	Type        string     `gorm:"size:16;default:task"`
	Status      string     `gorm:"size:16;default:open;index"`
	Priority    int        `gorm:"default:2"`
	Track       string     `gorm:"size:64;index"`
	Assignee    string     `gorm:"size:64"`
	ParentID    *string    `gorm:"size:32"`
	Branch      string     `gorm:"size:128"`
	DesignNotes string     `gorm:"type:text"`
	Acceptance  string     `gorm:"type:text"`
	CreatedAt   time.Time
	UpdatedAt   time.Time
	ClaimedAt   *time.Time
	CompletedAt *time.Time

	Parent   *Bead          `gorm:"foreignKey:ParentID"`
	Children []Bead         `gorm:"foreignKey:ParentID"`
	Deps     []BeadDep      `gorm:"foreignKey:BeadID"`
	Progress []BeadProgress `gorm:"foreignKey:BeadID"`
}

// BeadDep represents a blocking relationship between beads.
type BeadDep struct {
	BeadID    string `gorm:"primaryKey;size:32"`
	BlockedBy string `gorm:"primaryKey;size:32"`
	DepType   string `gorm:"size:16;default:blocks"`

	Bead    Bead `gorm:"foreignKey:BeadID"`
	Blocker Bead `gorm:"foreignKey:BlockedBy"`
}

// BeadProgress tracks work done across /clear cycles.
type BeadProgress struct {
	ID           uint      `gorm:"primaryKey;autoIncrement"`
	BeadID       string    `gorm:"size:32;index"`
	Cycle        int
	SessionID    string    `gorm:"size:64"`
	EngineID     string    `gorm:"size:64"`
	Note         string    `gorm:"type:text"`
	FilesChanged string    `gorm:"type:json"`
	CommitHash   string    `gorm:"size:40"`
	CreatedAt    time.Time
}
