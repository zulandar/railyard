package db

import (
	"encoding/json"
	"fmt"

	"github.com/zulandar/railyard/internal/config"
	"github.com/zulandar/railyard/internal/models"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// AllModels returns the list of all Phase 1 GORM models for migration.
func AllModels() []interface{} {
	return []interface{}{
		&models.Car{},
		&models.CarDep{},
		&models.CarProgress{},
		&models.Track{},
		&models.Engine{},
		&models.Message{},
		&models.BroadcastAck{},
		&models.AgentLog{},
		&models.RailyardConfig{},
		&models.ReindexJob{},
		&models.DispatchSession{},
		&models.TelegraphConversation{},
	}
}

// AutoMigrate creates or updates all Phase 1 tables.
func AutoMigrate(db *gorm.DB) error {
	if err := db.AutoMigrate(AllModels()...); err != nil {
		return fmt.Errorf("db: auto-migrate: %w", err)
	}
	return nil
}

// SeedTracks upserts Track rows from configuration.
func SeedTracks(db *gorm.DB, tracks []config.TrackConfig) error {
	for _, tc := range tracks {
		conventions, err := marshalJSON(tc.Conventions)
		if err != nil {
			return fmt.Errorf("db: marshal conventions for track %q: %w", tc.Name, err)
		}
		filePatterns, err := marshalJSON(tc.FilePatterns)
		if err != nil {
			return fmt.Errorf("db: marshal file_patterns for track %q: %w", tc.Name, err)
		}

		track := models.Track{
			Name:         tc.Name,
			Language:     tc.Language,
			Conventions:  conventions,
			FilePatterns: filePatterns,
			EngineSlots:  tc.EngineSlots,
			Active:       true,
		}

		result := db.Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "name"}},
			DoUpdates: clause.AssignmentColumns([]string{"language", "conventions", "file_patterns", "engine_slots", "active"}),
		}).Create(&track)
		if result.Error != nil {
			return fmt.Errorf("db: seed track %q: %w", tc.Name, result.Error)
		}
	}
	return nil
}

// SeedConfig writes or updates the RailyardConfig row for this owner.
func SeedConfig(db *gorm.DB, cfg *config.Config) error {
	rc := models.RailyardConfig{
		Owner:    cfg.Owner,
		RepoURL:  cfg.Repo,
		Mode:     "local",
		Settings: "{}",
	}

	result := db.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "owner"}},
		DoUpdates: clause.AssignmentColumns([]string{"repo_url", "mode"}),
	}).Create(&rc)
	if result.Error != nil {
		return fmt.Errorf("db: seed config for %q: %w", cfg.Owner, result.Error)
	}
	return nil
}

// marshalJSON marshals a value to a JSON string, returning empty string for nil.
func marshalJSON(v interface{}) (string, error) {
	if v == nil {
		return "", nil
	}
	data, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	return string(data), nil
}
