package inspect

import (
	"context"
	"time"

	"github.com/zulandar/railyard/internal/models"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Store wraps a GORM DB handle for inspect-related car queries.
type Store struct {
	db *gorm.DB
}

// NewStore creates a Store backed by the given GORM database.
func NewStore(db *gorm.DB) *Store {
	return &Store{db: db}
}

// ListPROpenCars returns all cars with status "pr_open".
func (s *Store) ListPROpenCars(ctx context.Context) ([]models.Car, error) {
	var cars []models.Car
	if err := s.db.WithContext(ctx).Where("status = ?", "pr_open").Find(&cars).Error; err != nil {
		return nil, err
	}
	return cars, nil
}

// ClaimForReview atomically transitions a car from pr_open to pr_review and
// sets the assignee to the given replicaID. Returns true if the row was
// updated (i.e., the claim succeeded).
func (s *Store) ClaimForReview(ctx context.Context, carID, replicaID string) (bool, error) {
	now := time.Now()
	result := s.db.WithContext(ctx).
		Clauses(clause.Locking{Strength: "UPDATE"}).
		Model(&models.Car{}).
		Where("id = ? AND status = ?", carID, "pr_open").
		Updates(map[string]interface{}{
			"status":     "pr_review",
			"assignee":   replicaID,
			"claimed_at": now,
			"updated_at": now,
		})
	if result.Error != nil {
		return false, result.Error
	}
	return result.RowsAffected > 0, nil
}

// ReleaseReview clears the claim on a car and applies any additional updates.
// By default, it sets status back to "pr_open", clears the assignee, and
// updates the timestamp.
func (s *Store) ReleaseReview(ctx context.Context, carID string, updates map[string]interface{}) error {
	defaults := map[string]interface{}{
		"status":     "pr_open",
		"assignee":   "",
		"updated_at": time.Now(),
	}
	for k, v := range updates {
		defaults[k] = v
	}
	return s.db.WithContext(ctx).
		Model(&models.Car{}).
		Where("id = ?", carID).
		Updates(defaults).Error
}

// UpdateCarStatus sets the status of a car and clears its assignee.
func (s *Store) UpdateCarStatus(ctx context.Context, carID, status string) error {
	return s.db.WithContext(ctx).
		Model(&models.Car{}).
		Where("id = ?", carID).
		Updates(map[string]interface{}{
			"status":     status,
			"assignee":   "",
			"updated_at": time.Now(),
		}).Error
}
