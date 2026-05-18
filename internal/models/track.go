package models

// PlaywrightConfig configures Playwright PR demo generation for a Track.
type PlaywrightConfig struct {
	Enabled  bool   `yaml:"enabled" json:"enabled"`
	SpecPath string `yaml:"spec_path" json:"spec_path"`
	Filename string `yaml:"filename" json:"filename"`
	Template string `yaml:"template" json:"template"`
}

// Track defines an area of concern within the repo.
type Track struct {
	Name         string            `gorm:"primaryKey;size:64"`
	Language     string            `gorm:"size:32"`
	Conventions  string            `gorm:"type:json"`
	SystemPrompt string            `gorm:"type:text"`
	FilePatterns string            `gorm:"type:json"`
	EngineSlots  int               `gorm:"default:3"`
	Active       bool              `gorm:"default:true"`
	Playwright   *PlaywrightConfig `gorm:"-" yaml:"playwright,omitempty" json:"playwright,omitempty"`
}
