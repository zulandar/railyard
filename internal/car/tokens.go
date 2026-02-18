package car

import (
	"fmt"

	"github.com/zulandar/railyard/internal/models"
	"gorm.io/gorm"
)

// TokenSummary holds aggregated token usage for a car.
type TokenSummary struct {
	InputTokens  int64
	OutputTokens int64
	TotalTokens  int64
	Model        string
}

// GetTokenUsage returns aggregated token usage for a single car.
func GetTokenUsage(db *gorm.DB, carID string) (TokenSummary, error) {
	var summary TokenSummary

	err := db.Model(&models.AgentLog{}).
		Select("COALESCE(SUM(input_tokens),0) as input_tokens, COALESCE(SUM(output_tokens),0) as output_tokens, COALESCE(SUM(token_count),0) as total_tokens").
		Where("car_id = ? AND direction = ?", carID, "out").
		Scan(&summary).Error
	if err != nil {
		return summary, fmt.Errorf("car: get token usage for %s: %w", carID, err)
	}

	// Get most recent model.
	var log models.AgentLog
	err = db.Where("car_id = ? AND direction = ? AND model != ?", carID, "out", "").
		Order("created_at DESC").
		First(&log).Error
	if err == nil {
		summary.Model = log.Model
	}

	return summary, nil
}

// CarTokenMap returns token summaries for multiple cars in a single batch query.
func CarTokenMap(db *gorm.DB, carIDs []string) (map[string]TokenSummary, error) {
	result := make(map[string]TokenSummary)
	if len(carIDs) == 0 {
		return result, nil
	}

	type row struct {
		CarID        string `gorm:"column:car_id"`
		InputTokens  int64  `gorm:"column:input_tokens"`
		OutputTokens int64  `gorm:"column:output_tokens"`
		TotalTokens  int64  `gorm:"column:total_tokens"`
	}

	var rows []row
	err := db.Model(&models.AgentLog{}).
		Select("car_id, COALESCE(SUM(input_tokens),0) as input_tokens, COALESCE(SUM(output_tokens),0) as output_tokens, COALESCE(SUM(token_count),0) as total_tokens").
		Where("car_id IN ? AND direction = ?", carIDs, "out").
		Group("car_id").
		Scan(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("car: batch token usage: %w", err)
	}

	for _, r := range rows {
		result[r.CarID] = TokenSummary{
			InputTokens:  r.InputTokens,
			OutputTokens: r.OutputTokens,
			TotalTokens:  r.TotalTokens,
		}
	}

	return result, nil
}
