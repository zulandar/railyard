package car

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/zulandar/railyard/internal/models"
	"gorm.io/gorm"
)

// DefaultMaxClearCycles is the threshold above which a car is considered stalled.
const DefaultMaxClearCycles = 5

// CycleSummary holds aggregated cycle metrics for a car.
type CycleSummary struct {
	TotalCycles       int
	AvgDurationSec    float64
	TotalFilesChanged int
	Engines           []string
	Stalled           bool
}

// CycleDetail holds per-cycle detail for a car.
type CycleDetail struct {
	Cycle        int
	EngineID     string
	DurationSec  float64
	FilesChanged int
	Note         string
	CommitHash   string
	CreatedAt    time.Time
}

// GetCycleMetrics returns cycle summary and per-cycle details for a single car.
func GetCycleMetrics(db *gorm.DB, carID string) (CycleSummary, []CycleDetail, error) {
	var summary CycleSummary
	var details []CycleDetail

	var rows []models.CarProgress
	if err := db.Where("car_id = ? AND cycle > 0", carID).Order("cycle ASC").Find(&rows).Error; err != nil {
		return summary, details, fmt.Errorf("car: get cycle metrics for %s: %w", carID, err)
	}

	if len(rows) == 0 {
		return summary, details, nil
	}

	summary.TotalCycles = len(rows)
	summary.Stalled = summary.TotalCycles > DefaultMaxClearCycles

	engineSet := make(map[string]bool)
	var totalDuration float64

	for i, p := range rows {
		var dur float64
		if i > 0 {
			dur = p.CreatedAt.Sub(rows[i-1].CreatedAt).Seconds()
			totalDuration += dur
		}

		fc := countFiles(p.FilesChanged)
		summary.TotalFilesChanged += fc
		engineSet[p.EngineID] = true

		details = append(details, CycleDetail{
			Cycle:        p.Cycle,
			EngineID:     p.EngineID,
			DurationSec:  dur,
			FilesChanged: fc,
			Note:         p.Note,
			CommitHash:   p.CommitHash,
			CreatedAt:    p.CreatedAt,
		})
	}

	if summary.TotalCycles > 1 {
		summary.AvgDurationSec = totalDuration / float64(summary.TotalCycles-1)
	}

	for eng := range engineSet {
		summary.Engines = append(summary.Engines, eng)
	}

	return summary, details, nil
}

// CarCycleMap returns cycle summaries for multiple cars in a single batch query.
func CarCycleMap(db *gorm.DB, carIDs []string) (map[string]CycleSummary, error) {
	result := make(map[string]CycleSummary)
	if len(carIDs) == 0 {
		return result, nil
	}

	type row struct {
		CarID       string `gorm:"column:car_id"`
		TotalCycles int    `gorm:"column:total_cycles"`
	}

	var rows []row
	err := db.Model(&models.CarProgress{}).
		Select("car_id, COUNT(*) as total_cycles").
		Where("car_id IN ? AND cycle > 0", carIDs).
		Group("car_id").
		Scan(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("car: batch cycle metrics: %w", err)
	}

	for _, r := range rows {
		result[r.CarID] = CycleSummary{
			TotalCycles: r.TotalCycles,
			Stalled:     r.TotalCycles > DefaultMaxClearCycles,
		}
	}

	return result, nil
}

// countFiles parses a JSON array string and returns the element count.
func countFiles(jsonStr string) int {
	if jsonStr == "" || jsonStr == "null" || jsonStr == "[]" {
		return 0
	}
	var arr []json.RawMessage
	if err := json.Unmarshal([]byte(jsonStr), &arr); err != nil {
		return 0
	}
	return len(arr)
}
