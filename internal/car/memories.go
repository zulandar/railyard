package car

import (
	"fmt"

	"github.com/zulandar/railyard/internal/models"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Remember creates or updates a CarMemory row for the given car, keyword,
// and content. If a memory with the same car+track+keyword already exists,
// its content is updated (INSERT ON CONFLICT UPDATE).
func Remember(db *gorm.DB, carID, keyword, content string) error {
	if carID == "" {
		return fmt.Errorf("memories: car ID is required")
	}
	if keyword == "" {
		return fmt.Errorf("memories: keyword is required")
	}
	if content == "" {
		return fmt.Errorf("memories: content is required")
	}

	// Verify car exists and grab its track.
	track, err := carTrack(db, carID)
	if err != nil {
		return err
	}

	mem := models.CarMemory{
		CarID:   carID,
		Track:   track,
		Keyword: keyword,
		Content: content,
	}

	if err := db.Clauses(clause.OnConflict{
		Columns: []clause.Column{
			{Name: "car_id"},
			{Name: "track"},
			{Name: "keyword"},
		},
		DoUpdates: clause.AssignmentColumns([]string{"content"}),
	}).Create(&mem).Error; err != nil {
		return fmt.Errorf("memories: remember %s/%s: %w", carID, keyword, err)
	}
	return nil
}

// Memories returns all CarMemory rows for a car, optionally filtered by
// keyword. When keyword is empty, all memories for the car are returned.
func Memories(db *gorm.DB, carID, keyword string) ([]models.CarMemory, error) {
	if carID == "" {
		return nil, fmt.Errorf("memories: car ID is required")
	}

	// Verify car exists.
	if _, err := carTrack(db, carID); err != nil {
		return nil, err
	}

	q := db.Where("car_id = ?", carID)
	if keyword != "" {
		q = q.Where("keyword = ?", keyword)
	}

	var memories []models.CarMemory
	if err := q.Order("keyword ASC").Find(&memories).Error; err != nil {
		return nil, fmt.Errorf("memories: list %s: %w", carID, err)
	}
	return memories, nil
}

// Forget deletes a CarMemory row identified by car ID and keyword.
// Returns an error if no matching row was found.
func Forget(db *gorm.DB, carID, keyword string) error {
	if carID == "" {
		return fmt.Errorf("memories: car ID is required")
	}
	if keyword == "" {
		return fmt.Errorf("memories: keyword is required")
	}

	// Verify car exists.
	if _, err := carTrack(db, carID); err != nil {
		return err
	}

	result := db.Where("car_id = ? AND keyword = ?", carID, keyword).Delete(&models.CarMemory{})
	if result.Error != nil {
		return fmt.Errorf("memories: forget %s/%s: %w", carID, keyword, result.Error)
	}
	if result.RowsAffected == 0 {
		return fmt.Errorf("memories: no memory %q found for car %s", keyword, carID)
	}
	return nil
}

// GetCarMemories returns all CarMemory rows scoped to the given car.
// Returns an empty slice (not nil) when no memories exist.
func GetCarMemories(db *gorm.DB, carID string) ([]models.CarMemory, error) {
	if carID == "" {
		return nil, fmt.Errorf("car: get car memories: car ID is required")
	}

	var memories []models.CarMemory
	if err := db.Where("car_id = ?", carID).Order("keyword ASC").Find(&memories).Error; err != nil {
		return nil, fmt.Errorf("car: get car memories for %s: %w", carID, err)
	}
	return memories, nil
}

// GetTrackMemories returns all CarMemory rows scoped to the given track.
// These are shared across all cars on the same track.
// Returns an empty slice (not nil) when no memories exist.
func GetTrackMemories(db *gorm.DB, track string) ([]models.CarMemory, error) {
	if track == "" {
		return nil, fmt.Errorf("car: get track memories: track is required")
	}

	var memories []models.CarMemory
	if err := db.Where("track = ?", track).Order("keyword ASC, car_id ASC").Find(&memories).Error; err != nil {
		return nil, fmt.Errorf("car: get track memories for %s: %w", track, err)
	}
	return memories, nil
}

// carTrack returns the track of a car, or an error if not found.
func carTrack(db *gorm.DB, carID string) (string, error) {
	var c models.Car
	if err := db.Select("track").Where("id = ?", carID).First(&c).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return "", fmt.Errorf("memories: car not found: %s", carID)
		}
		return "", fmt.Errorf("memories: get car %s: %w", carID, err)
	}
	return c.Track, nil
}
