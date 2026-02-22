package telegraph

import (
	"fmt"
	"strings"
	"time"

	"github.com/zulandar/railyard/internal/models"
	"gorm.io/gorm"
)

// Event types for digests.
const (
	EventDailyDigest  EventType = "daily_digest"
	EventWeeklyDigest EventType = "weekly_digest"
)

// DailyReport holds computed metrics for a 24-hour period.
type DailyReport struct {
	PeriodStart    time.Time
	PeriodEnd      time.Time
	CarsCreated    int
	CarsCompleted  int
	CarsMerged     int
	StallCount     int
	TotalTokens    int64
	EngineCount    int
	TrackBreakdown []TrackDigest
}

// WeeklyReport holds computed metrics for a 7-day period.
type WeeklyReport struct {
	PeriodStart      time.Time
	PeriodEnd        time.Time
	CarsClosed       int
	CarsMerged       int
	MergeAttempts    int
	MergeSuccessRate float64
	TotalTokens      int64
	StallCount       int
	TrackBreakdown   []TrackDigest
}

// TrackDigest holds per-track metrics for digest reports.
type TrackDigest struct {
	Track         string
	Completed     int
	Open          int
	AvgCompletion time.Duration
}

// BuildDailyDigest queries the DB for the last 24 hours and returns a
// DetectedEvent with the daily report. Returns nil when no activity.
func (w *Watcher) BuildDailyDigest() (*DetectedEvent, error) {
	now := time.Now()
	since := now.Add(-24 * time.Hour)

	report, err := buildDailyReport(w.db, since, now)
	if err != nil {
		return nil, fmt.Errorf("telegraph: daily digest: %w", err)
	}

	// Suppress when no activity.
	if report.CarsCreated == 0 && report.CarsCompleted == 0 &&
		report.CarsMerged == 0 && report.StallCount == 0 && report.TotalTokens == 0 {
		return nil, nil
	}

	formatted := FormatDaily(report)
	return &DetectedEvent{
		Type:      EventDailyDigest,
		Timestamp: now,
		Title:     formatted.Title,
		Body:      formatted.Body,
	}, nil
}

// BuildWeeklyDigest queries the DB for the last 7 days and returns a
// DetectedEvent with the weekly report. Returns nil when no activity.
func (w *Watcher) BuildWeeklyDigest() (*DetectedEvent, error) {
	now := time.Now()
	since := now.Add(-7 * 24 * time.Hour)

	report, err := buildWeeklyReport(w.db, since, now)
	if err != nil {
		return nil, fmt.Errorf("telegraph: weekly digest: %w", err)
	}

	// Suppress when no activity.
	if report.CarsClosed == 0 && report.CarsMerged == 0 &&
		report.StallCount == 0 && report.TotalTokens == 0 {
		return nil, nil
	}

	formatted := FormatWeekly(report)
	return &DetectedEvent{
		Type:      EventWeeklyDigest,
		Timestamp: now,
		Title:     formatted.Title,
		Body:      formatted.Body,
	}, nil
}

// buildDailyReport queries Dolt for metrics within the given time range.
func buildDailyReport(db *gorm.DB, since, until time.Time) (*DailyReport, error) {
	report := &DailyReport{
		PeriodStart: since,
		PeriodEnd:   until,
	}

	// Cars completed (status=done, completed_at in range).
	if err := db.Model(&models.Car{}).
		Where("status = ? AND completed_at >= ? AND completed_at < ?", "done", since, until).
		Count(new(int64)).Error; err != nil {
		return nil, err
	}
	var completedCount int64
	db.Model(&models.Car{}).
		Where("status = ? AND completed_at >= ? AND completed_at < ?", "done", since, until).
		Count(&completedCount)
	report.CarsCompleted = int(completedCount)

	// Cars merged (status=merged, completed_at in range).
	var mergedCount int64
	db.Model(&models.Car{}).
		Where("status = ? AND completed_at >= ? AND completed_at < ?", "merged", since, until).
		Count(&mergedCount)
	report.CarsMerged = int(mergedCount)

	// Cars created (created_at in range).
	var createdCount int64
	db.Model(&models.Car{}).
		Where("created_at >= ? AND created_at < ?", since, until).
		Count(&createdCount)
	report.CarsCreated = int(createdCount)

	// Stall count — engines with status=stalled that had activity in range.
	var stallCount int64
	db.Model(&models.Engine{}).
		Where("status = ? AND last_activity >= ? AND last_activity < ?", "stalled", since, until).
		Count(&stallCount)
	report.StallCount = int(stallCount)

	// Token totals from agent_logs in range.
	var tokenSum struct{ Total int64 }
	db.Model(&models.AgentLog{}).
		Where("created_at >= ? AND created_at < ?", since, until).
		Select("COALESCE(SUM(token_count), 0) as total").
		Scan(&tokenSum)
	report.TotalTokens = tokenSum.Total

	// Engine count (currently registered).
	var engineCount int64
	db.Model(&models.Engine{}).Count(&engineCount)
	report.EngineCount = int(engineCount)

	// Per-track breakdown.
	report.TrackBreakdown = buildTrackBreakdown(db, since, until)

	return report, nil
}

// buildWeeklyReport queries Dolt for metrics within the given time range.
func buildWeeklyReport(db *gorm.DB, since, until time.Time) (*WeeklyReport, error) {
	report := &WeeklyReport{
		PeriodStart: since,
		PeriodEnd:   until,
	}

	// Cars closed (done+merged+cancelled, completed_at in range).
	var closedCount int64
	db.Model(&models.Car{}).
		Where("status IN ? AND completed_at >= ? AND completed_at < ?",
			[]string{"done", "merged", "cancelled"}, since, until).
		Count(&closedCount)
	report.CarsClosed = int(closedCount)

	// Cars merged.
	var mergedCount int64
	db.Model(&models.Car{}).
		Where("status = ? AND completed_at >= ? AND completed_at < ?", "merged", since, until).
		Count(&mergedCount)
	report.CarsMerged = int(mergedCount)

	// Merge attempts = merged + merge-failed cars.
	var mergeFailedCount int64
	db.Model(&models.Car{}).
		Where("status = ? AND updated_at >= ? AND updated_at < ?", "merge-failed", since, until).
		Count(&mergeFailedCount)
	report.MergeAttempts = int(mergedCount + mergeFailedCount)
	if report.MergeAttempts > 0 {
		report.MergeSuccessRate = float64(report.CarsMerged) / float64(report.MergeAttempts) * 100
	}

	// Stall count.
	var stallCount int64
	db.Model(&models.Engine{}).
		Where("status = ? AND last_activity >= ? AND last_activity < ?", "stalled", since, until).
		Count(&stallCount)
	report.StallCount = int(stallCount)

	// Token totals.
	var tokenSum struct{ Total int64 }
	db.Model(&models.AgentLog{}).
		Where("created_at >= ? AND created_at < ?", since, until).
		Select("COALESCE(SUM(token_count), 0) as total").
		Scan(&tokenSum)
	report.TotalTokens = tokenSum.Total

	// Per-track breakdown.
	report.TrackBreakdown = buildTrackBreakdown(db, since, until)

	return report, nil
}

// buildTrackBreakdown computes per-track metrics.
func buildTrackBreakdown(db *gorm.DB, since, until time.Time) []TrackDigest {
	var tracks []struct {
		Track string
	}
	db.Model(&models.Car{}).
		Distinct("track").
		Where("track != ''").
		Find(&tracks)

	var breakdown []TrackDigest
	for _, t := range tracks {
		td := TrackDigest{Track: t.Track}

		// Completed in period.
		var completed int64
		db.Model(&models.Car{}).
			Where("track = ? AND status IN ? AND completed_at >= ? AND completed_at < ?",
				t.Track, []string{"done", "merged"}, since, until).
			Count(&completed)
		td.Completed = int(completed)

		// Currently open.
		var open int64
		db.Model(&models.Car{}).
			Where("track = ? AND status IN ?", t.Track, []string{"open", "in_progress", "draft"}).
			Count(&open)
		td.Open = int(open)

		// Average completion time for cars completed in period.
		// Computed in Go for portability across SQLite (tests) and MySQL/Dolt (production).
		var completionRows []struct {
			ClaimedAt   time.Time
			CompletedAt time.Time
		}
		if err := db.Model(&models.Car{}).
			Where("track = ? AND status IN ? AND completed_at >= ? AND completed_at < ? AND claimed_at IS NOT NULL",
				t.Track, []string{"done", "merged"}, since, until).
			Select("claimed_at, completed_at").
			Find(&completionRows).Error; err != nil {
			// Log and continue — a failed avg query shouldn't break the entire breakdown.
			_ = err
		}
		if len(completionRows) > 0 {
			var totalSec float64
			for _, row := range completionRows {
				totalSec += row.CompletedAt.Sub(row.ClaimedAt).Seconds()
			}
			avgSec := totalSec / float64(len(completionRows))
			td.AvgCompletion = time.Duration(avgSec) * time.Second
		}

		breakdown = append(breakdown, td)
	}
	return breakdown
}

// FormatDaily formats a daily digest report as a FormattedEvent.
func FormatDaily(report *DailyReport) FormattedEvent {
	var bodyLines []string
	bodyLines = append(bodyLines, fmt.Sprintf("**Period**: %s – %s",
		report.PeriodStart.Format("Jan 2 15:04"),
		report.PeriodEnd.Format("Jan 2 15:04")))
	bodyLines = append(bodyLines, fmt.Sprintf("**Cars**: %d created, %d completed, %d merged",
		report.CarsCreated, report.CarsCompleted, report.CarsMerged))
	if report.TotalTokens > 0 {
		bodyLines = append(bodyLines, fmt.Sprintf("**Tokens**: %s", formatTokenCount(report.TotalTokens)))
	}
	if report.StallCount > 0 {
		bodyLines = append(bodyLines, fmt.Sprintf("**Stalls**: %d", report.StallCount))
	}
	bodyLines = append(bodyLines, fmt.Sprintf("**Engines**: %d registered", report.EngineCount))

	// Track breakdown.
	if len(report.TrackBreakdown) > 0 {
		bodyLines = append(bodyLines, "")
		bodyLines = append(bodyLines, "**Per Track**:")
		for _, td := range report.TrackBreakdown {
			line := fmt.Sprintf("  %s: %d completed, %d open", td.Track, td.Completed, td.Open)
			if td.AvgCompletion > 0 {
				line += fmt.Sprintf(" (avg %s)", formatDuration(td.AvgCompletion))
			}
			bodyLines = append(bodyLines, line)
		}
	}

	fields := []Field{
		{Name: "Created", Value: fmt.Sprintf("%d", report.CarsCreated), Short: true},
		{Name: "Completed", Value: fmt.Sprintf("%d", report.CarsCompleted), Short: true},
		{Name: "Merged", Value: fmt.Sprintf("%d", report.CarsMerged), Short: true},
		{Name: "Engines", Value: fmt.Sprintf("%d", report.EngineCount), Short: true},
	}
	if report.TotalTokens > 0 {
		fields = append(fields, Field{Name: "Tokens", Value: formatTokenCount(report.TotalTokens), Short: true})
	}
	if report.StallCount > 0 {
		fields = append(fields, Field{Name: "Stalls", Value: fmt.Sprintf("%d", report.StallCount), Short: true})
	}

	return FormattedEvent{
		Title:    "Daily Digest",
		Body:     strings.Join(bodyLines, "\n"),
		Severity: "info",
		Color:    ColorInfo,
		Fields:   fields,
	}
}

// FormatWeekly formats a weekly digest report as a FormattedEvent.
func FormatWeekly(report *WeeklyReport) FormattedEvent {
	var bodyLines []string
	bodyLines = append(bodyLines, fmt.Sprintf("**Period**: %s – %s",
		report.PeriodStart.Format("Jan 2"),
		report.PeriodEnd.Format("Jan 2")))
	bodyLines = append(bodyLines, fmt.Sprintf("**Cars Closed**: %d (%d merged)",
		report.CarsClosed, report.CarsMerged))
	if report.MergeAttempts > 0 {
		bodyLines = append(bodyLines, fmt.Sprintf("**Merge Success Rate**: %.0f%% (%d/%d)",
			report.MergeSuccessRate, report.CarsMerged, report.MergeAttempts))
	}
	if report.TotalTokens > 0 {
		bodyLines = append(bodyLines, fmt.Sprintf("**Tokens**: %s", formatTokenCount(report.TotalTokens)))
	}
	if report.StallCount > 0 {
		bodyLines = append(bodyLines, fmt.Sprintf("**Stalls**: %d", report.StallCount))
	}

	// Track breakdown.
	if len(report.TrackBreakdown) > 0 {
		bodyLines = append(bodyLines, "")
		bodyLines = append(bodyLines, "**Per Track**:")
		for _, td := range report.TrackBreakdown {
			line := fmt.Sprintf("  %s: %d completed, %d open", td.Track, td.Completed, td.Open)
			if td.AvgCompletion > 0 {
				line += fmt.Sprintf(" (avg %s)", formatDuration(td.AvgCompletion))
			}
			bodyLines = append(bodyLines, line)
		}
	}

	fields := []Field{
		{Name: "Closed", Value: fmt.Sprintf("%d", report.CarsClosed), Short: true},
		{Name: "Merged", Value: fmt.Sprintf("%d", report.CarsMerged), Short: true},
	}
	if report.MergeAttempts > 0 {
		fields = append(fields, Field{Name: "Merge Rate", Value: fmt.Sprintf("%.0f%%", report.MergeSuccessRate), Short: true})
	}
	if report.TotalTokens > 0 {
		fields = append(fields, Field{Name: "Tokens", Value: formatTokenCount(report.TotalTokens), Short: true})
	}
	if report.StallCount > 0 {
		fields = append(fields, Field{Name: "Stalls", Value: fmt.Sprintf("%d", report.StallCount), Short: true})
	}

	return FormattedEvent{
		Title:    "Weekly Digest",
		Body:     strings.Join(bodyLines, "\n"),
		Severity: "info",
		Color:    ColorInfo,
		Fields:   fields,
	}
}

// formatTokenCount formats a token count with K/M suffixes.
func formatTokenCount(tokens int64) string {
	if tokens >= 1_000_000 {
		return fmt.Sprintf("%.1fM", float64(tokens)/1_000_000)
	}
	if tokens >= 1_000 {
		return fmt.Sprintf("%.1fK", float64(tokens)/1_000)
	}
	return fmt.Sprintf("%d", tokens)
}

// formatDuration formats a duration as a human-readable string.
func formatDuration(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm", int(d.Minutes()))
	}
	h := int(d.Hours())
	m := int(d.Minutes()) % 60
	if h >= 24 {
		days := h / 24
		h = h % 24
		return fmt.Sprintf("%dd %dh", days, h)
	}
	return fmt.Sprintf("%dh %dm", h, m)
}
