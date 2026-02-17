package dashboard

import (
	"time"

	"github.com/zulandar/railyard/internal/models"
	"gorm.io/gorm"
)

// EngineRow holds engine data for display.
type EngineRow struct {
	ID           string
	Track        string
	Status       string
	CurrentCar   string
	LastActivity time.Time
	StartedAt    time.Time
}

// EngineSummary returns all non-yardmaster engines.
func EngineSummary(db *gorm.DB) ([]EngineRow, error) {
	var engines []models.Engine
	if err := db.Where("role != ? OR role IS NULL", "yardmaster").
		Order("track ASC, id ASC").Find(&engines).Error; err != nil {
		return nil, err
	}

	rows := make([]EngineRow, len(engines))
	for i, e := range engines {
		rows[i] = EngineRow{
			ID:           e.ID,
			Track:        e.Track,
			Status:       e.Status,
			CurrentCar:   e.CurrentCar,
			LastActivity: e.LastActivity,
			StartedAt:    e.StartedAt,
		}
	}
	return rows, nil
}

// TrackStatusCount holds car counts by status for a single track.
type TrackStatusCount struct {
	Track       string
	Draft       int
	Open        int
	Claimed     int
	InProgress  int
	Done        int
	Merged      int
	Blocked     int
	Total       int
}

// TrackSummary returns per-track car counts grouped by status.
func TrackSummary(db *gorm.DB) ([]TrackStatusCount, error) {
	type row struct {
		Track  string
		Status string
		Count  int
	}
	var rows []row
	if err := db.Model(&models.Car{}).
		Select("track, status, count(*) as count").
		Where("type != ?", "epic").
		Group("track, status").
		Find(&rows).Error; err != nil {
		return nil, err
	}

	// Aggregate by track.
	trackMap := make(map[string]*TrackStatusCount)
	for _, r := range rows {
		tc, ok := trackMap[r.Track]
		if !ok {
			tc = &TrackStatusCount{Track: r.Track}
			trackMap[r.Track] = tc
		}
		tc.Total += r.Count
		switch r.Status {
		case "draft":
			tc.Draft += r.Count
		case "open", "ready":
			tc.Open += r.Count
		case "claimed":
			tc.Claimed += r.Count
		case "in_progress":
			tc.InProgress += r.Count
		case "done":
			tc.Done += r.Count
		case "merged":
			tc.Merged += r.Count
		case "blocked":
			tc.Blocked += r.Count
		}
	}

	result := make([]TrackStatusCount, 0, len(trackMap))
	for _, tc := range trackMap {
		result = append(result, *tc)
	}
	return result, nil
}

// MessageQueueDepth returns the count of unacknowledged non-broadcast messages.
func MessageQueueDepth(db *gorm.DB) (int64, error) {
	var count int64
	if err := db.Model(&models.Message{}).
		Where("acknowledged = ? AND to_agent != ?", false, "broadcast").
		Count(&count).Error; err != nil {
		return 0, err
	}
	return count, nil
}

// Escalation holds a recent escalation message for display.
type Escalation struct {
	ID        uint
	FromAgent string
	CarID     string
	Subject   string
	Body      string
	Priority  string
	CreatedAt time.Time
}

// RecentEscalations returns unacknowledged messages sent to "human".
func RecentEscalations(db *gorm.DB) ([]Escalation, error) {
	var msgs []models.Message
	if err := db.Where("to_agent = ? AND acknowledged = ?", "human", false).
		Order("created_at DESC").
		Limit(20).
		Find(&msgs).Error; err != nil {
		return nil, err
	}

	result := make([]Escalation, len(msgs))
	for i, m := range msgs {
		result[i] = Escalation{
			ID:        m.ID,
			FromAgent: m.FromAgent,
			CarID:     m.CarID,
			Subject:   m.Subject,
			Body:      m.Body,
			Priority:  m.Priority,
			CreatedAt: m.CreatedAt,
		}
	}
	return result, nil
}
