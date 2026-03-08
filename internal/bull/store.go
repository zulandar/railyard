package bull

import (
	"context"
	"fmt"
	"time"

	"github.com/zulandar/railyard/internal/car"
	"github.com/zulandar/railyard/internal/models"
	"gorm.io/gorm"
)

// Store implements DaemonStore backed by a gorm.DB.
type Store struct {
	db           *gorm.DB
	branchPrefix string
}

// NewStore creates a new Store.
func NewStore(db *gorm.DB, branchPrefix string) *Store {
	return &Store{db: db, branchPrefix: branchPrefix}
}

func (s *Store) GetTrackedIssues(_ context.Context) ([]models.BullIssue, error) {
	var issues []models.BullIssue
	if err := s.db.Where("last_known_status NOT IN ?", []string{"released", "cancelled"}).Find(&issues).Error; err != nil {
		return nil, fmt.Errorf("bull store: get tracked issues: %w", err)
	}
	return issues, nil
}

func (s *Store) GetCarStatus(_ context.Context, carID string) (string, error) {
	var c models.Car
	if err := s.db.Select("status").Where("id = ?", carID).First(&c).Error; err != nil {
		return "", fmt.Errorf("bull store: get car status %q: %w", carID, err)
	}
	return c.Status, nil
}

func (s *Store) UpdateIssueStatus(_ context.Context, issueID uint, newStatus string) error {
	now := time.Now()
	if err := s.db.Model(&models.BullIssue{}).Where("id = ?", issueID).Updates(map[string]interface{}{
		"last_known_status": newStatus,
		"last_synced_at":    &now,
	}).Error; err != nil {
		return fmt.Errorf("bull store: update issue status %d: %w", issueID, err)
	}
	return nil
}

func (s *Store) GetMergedIssues(_ context.Context) ([]models.BullIssue, error) {
	var issues []models.BullIssue
	if err := s.db.Where("last_known_status = ?", "merged").Find(&issues).Error; err != nil {
		return nil, fmt.Errorf("bull store: get merged issues: %w", err)
	}
	return issues, nil
}

func (s *Store) GetLastReleaseCheck(_ context.Context) (time.Time, error) {
	var meta models.BullMeta
	if err := s.db.Where("`key` = ?", "last_release_check").First(&meta).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return time.Time{}, nil
		}
		return time.Time{}, fmt.Errorf("bull store: get last release check: %w", err)
	}
	t, err := time.Parse(time.RFC3339, meta.Value)
	if err != nil {
		return time.Time{}, fmt.Errorf("bull store: parse last release check: %w", err)
	}
	return t, nil
}

func (s *Store) SetLastReleaseCheck(_ context.Context, t time.Time) error {
	meta := models.BullMeta{
		Key:   "last_release_check",
		Value: t.UTC().Format(time.RFC3339),
	}
	if err := s.db.Where("`key` = ?", "last_release_check").Assign(meta).FirstOrCreate(&meta).Error; err != nil {
		return fmt.Errorf("bull store: set last release check: %w", err)
	}
	return nil
}

func (s *Store) CreateCar(_ context.Context, opts CarCreateOpts) (string, error) {
	c, err := car.Create(s.db, car.CreateOpts{
		Title:        opts.Title,
		Description:  opts.Description,
		Type:         opts.Type,
		Priority:     opts.Priority,
		Track:        opts.Track,
		DesignNotes:  opts.DesignNotes,
		Acceptance:   opts.Acceptance,
		BranchPrefix: opts.BranchPrefix,
		RequestedBy:  opts.RequestedBy,
	})
	if err != nil {
		return "", fmt.Errorf("bull store: create car: %w", err)
	}

	// Set source_issue on the car.
	if opts.SourceIssue > 0 {
		s.db.Model(&models.Car{}).Where("id = ?", c.ID).Update("source_issue", opts.SourceIssue)
	}

	return c.ID, nil
}

func (s *Store) RecordTriagedIssue(_ context.Context, issue models.BullIssue) error {
	if err := s.db.Create(&issue).Error; err != nil {
		return fmt.Errorf("bull store: record triaged issue: %w", err)
	}
	return nil
}
