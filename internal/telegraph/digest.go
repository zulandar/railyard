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

	// Previous-period metrics (prior 24h window).
	PrevCarsCreated   int
	PrevCarsCompleted int
	PrevCarsMerged    int
	PrevStallCount    int
	PrevTotalTokens   int64
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

	// Previous-period metrics (prior 7-day window).
	PrevCarsClosed       int
	PrevCarsMerged       int
	PrevStallCount       int
	PrevMergeSuccessRate float64
	PrevTotalTokens      int64
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

	formatted := FormatDaily(report, w.dashboardURL)
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

	formatted := FormatWeekly(report, w.dashboardURL)
	return &DetectedEvent{
		Type:      EventWeeklyDigest,
		Timestamp: now,
		Title:     formatted.Title,
		Body:      formatted.Body,
	}, nil
}

// buildDailyReport queries the database for metrics within the given time range.
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

	// Previous-period metrics: prior 24h window [since-24h, since].
	prevSince := since.Add(-24 * time.Hour)
	prevUntil := since

	var prevCompletedCount int64
	db.Model(&models.Car{}).
		Where("status = ? AND completed_at >= ? AND completed_at < ?", "done", prevSince, prevUntil).
		Count(&prevCompletedCount)
	report.PrevCarsCompleted = int(prevCompletedCount)

	var prevMergedCount int64
	db.Model(&models.Car{}).
		Where("status = ? AND completed_at >= ? AND completed_at < ?", "merged", prevSince, prevUntil).
		Count(&prevMergedCount)
	report.PrevCarsMerged = int(prevMergedCount)

	var prevCreatedCount int64
	db.Model(&models.Car{}).
		Where("created_at >= ? AND created_at < ?", prevSince, prevUntil).
		Count(&prevCreatedCount)
	report.PrevCarsCreated = int(prevCreatedCount)

	var prevStallCount int64
	db.Model(&models.Engine{}).
		Where("status = ? AND last_activity >= ? AND last_activity < ?", "stalled", prevSince, prevUntil).
		Count(&prevStallCount)
	report.PrevStallCount = int(prevStallCount)

	var prevTokenSum struct{ Total int64 }
	db.Model(&models.AgentLog{}).
		Where("created_at >= ? AND created_at < ?", prevSince, prevUntil).
		Select("COALESCE(SUM(token_count), 0) as total").
		Scan(&prevTokenSum)
	report.PrevTotalTokens = prevTokenSum.Total

	return report, nil
}

// buildWeeklyReport queries the database for metrics within the given time range.
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

	// Previous-period metrics: prior 7-day window [since-7d, since].
	prevSince := since.Add(-7 * 24 * time.Hour)
	prevUntil := since

	var prevClosedCount int64
	db.Model(&models.Car{}).
		Where("status IN ? AND completed_at >= ? AND completed_at < ?",
			[]string{"done", "merged", "cancelled"}, prevSince, prevUntil).
		Count(&prevClosedCount)
	report.PrevCarsClosed = int(prevClosedCount)

	var prevMergedCount int64
	db.Model(&models.Car{}).
		Where("status = ? AND completed_at >= ? AND completed_at < ?", "merged", prevSince, prevUntil).
		Count(&prevMergedCount)
	report.PrevCarsMerged = int(prevMergedCount)

	var prevMergeFailedCount int64
	db.Model(&models.Car{}).
		Where("status = ? AND updated_at >= ? AND updated_at < ?", "merge-failed", prevSince, prevUntil).
		Count(&prevMergeFailedCount)
	prevMergeAttempts := int(prevMergedCount + prevMergeFailedCount)
	if prevMergeAttempts > 0 {
		report.PrevMergeSuccessRate = float64(report.PrevCarsMerged) / float64(prevMergeAttempts) * 100
	}

	var prevStallCount int64
	db.Model(&models.Engine{}).
		Where("status = ? AND last_activity >= ? AND last_activity < ?", "stalled", prevSince, prevUntil).
		Count(&prevStallCount)
	report.PrevStallCount = int(prevStallCount)

	var prevTokenSum struct{ Total int64 }
	db.Model(&models.AgentLog{}).
		Where("created_at >= ? AND created_at < ?", prevSince, prevUntil).
		Select("COALESCE(SUM(token_count), 0) as total").
		Scan(&prevTokenSum)
	report.PrevTotalTokens = prevTokenSum.Total

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
		// Computed in Go for portability across SQLite (tests) and MySQL (production).
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

// formatWithDelta formats an integer count with a delta indicator showing
// change from a previous period (e.g. "12 (▲4)", "8 (▼4)", "5 (=)").
func formatWithDelta(current, previous int) string {
	delta := current - previous
	switch {
	case delta > 0:
		return fmt.Sprintf("%d (▲%d)", current, delta)
	case delta < 0:
		return fmt.Sprintf("%d (▼%d)", current, -delta)
	default:
		return fmt.Sprintf("%d (=)", current)
	}
}

// formatRateWithDelta formats a percentage rate with a delta indicator showing
// change from a previous period (e.g. "90% (▲15%)", "75% (▼15%)", "88% (=)").
// Deltas within ±0.5 are treated as equal.
func formatRateWithDelta(current, previous float64) string {
	delta := current - previous
	switch {
	case delta > 0.5:
		return fmt.Sprintf("%.0f%% (▲%.0f%%)", current, delta)
	case delta < -0.5:
		return fmt.Sprintf("%.0f%% (▼%.0f%%)", current, -delta)
	default:
		return fmt.Sprintf("%.0f%% (=)", current)
	}
}

// FormatDaily formats a daily digest report as a FormattedEvent.
func FormatDaily(report *DailyReport, dashboardURL string) FormattedEvent {
	var bodyLines []string
	bodyLines = append(bodyLines, fmt.Sprintf("**Period**: %s – %s",
		report.PeriodStart.Format("Jan 2 15:04"),
		report.PeriodEnd.Format("Jan 2 15:04")))
	bodyLines = append(bodyLines, fmt.Sprintf("**Cars**: %s created, %s completed, %s merged",
		formatWithDelta(report.CarsCreated, report.PrevCarsCreated),
		formatWithDelta(report.CarsCompleted, report.PrevCarsCompleted),
		formatWithDelta(report.CarsMerged, report.PrevCarsMerged)))
	if report.TotalTokens > 0 {
		bodyLines = append(bodyLines, fmt.Sprintf("**Tokens**: %s", formatTokenCount(report.TotalTokens)))
	}
	if report.StallCount > 0 {
		bodyLines = append(bodyLines, fmt.Sprintf("**Stalls**: %s", formatWithDelta(report.StallCount, report.PrevStallCount)))
	}
	bodyLines = append(bodyLines, fmt.Sprintf("**Engines**: %d registered", report.EngineCount))

	fields := []Field{
		{Name: "Created", Value: formatWithDelta(report.CarsCreated, report.PrevCarsCreated), Short: true},
		{Name: "Completed", Value: formatWithDelta(report.CarsCompleted, report.PrevCarsCompleted), Short: true},
		{Name: "Merged", Value: formatWithDelta(report.CarsMerged, report.PrevCarsMerged), Short: true},
		{Name: "Engines", Value: fmt.Sprintf("%d", report.EngineCount), Short: true},
	}
	if report.TotalTokens > 0 {
		fields = append(fields, Field{Name: "Tokens", Value: formatTokenCount(report.TotalTokens), Short: true})
	}
	if report.StallCount > 0 {
		fields = append(fields, Field{Name: "Stalls", Value: formatWithDelta(report.StallCount, report.PrevStallCount), Short: true})
	}

	// Track breakdown as fields.
	for _, td := range report.TrackBreakdown {
		val := fmt.Sprintf("%d completed, %d open", td.Completed, td.Open)
		if td.AvgCompletion > 0 {
			val += fmt.Sprintf(" (avg %s)", formatDuration(td.AvgCompletion))
		}
		fields = append(fields, Field{Name: td.Track, Value: val, Short: true})
	}

	return FormattedEvent{
		Title:    "📊 Daily Digest",
		Body:     strings.Join(bodyLines, "\n"),
		Severity: "info",
		Color:    ColorInfo,
		Fields:   fields,
	}
}

// FormatWeekly formats a weekly digest report as a FormattedEvent.
func FormatWeekly(report *WeeklyReport, dashboardURL string) FormattedEvent {
	var bodyLines []string
	bodyLines = append(bodyLines, fmt.Sprintf("**Period**: %s – %s",
		report.PeriodStart.Format("Jan 2"),
		report.PeriodEnd.Format("Jan 2")))
	bodyLines = append(bodyLines, fmt.Sprintf("**Cars Closed**: %s (%s merged)",
		formatWithDelta(report.CarsClosed, report.PrevCarsClosed),
		formatWithDelta(report.CarsMerged, report.PrevCarsMerged)))
	if report.MergeAttempts > 0 {
		bodyLines = append(bodyLines, fmt.Sprintf("**Merge Success Rate**: %s (%d/%d)",
			formatRateWithDelta(report.MergeSuccessRate, report.PrevMergeSuccessRate),
			report.CarsMerged, report.MergeAttempts))
	}
	if report.TotalTokens > 0 {
		bodyLines = append(bodyLines, fmt.Sprintf("**Tokens**: %s", formatTokenCount(report.TotalTokens)))
	}
	if report.StallCount > 0 {
		bodyLines = append(bodyLines, fmt.Sprintf("**Stalls**: %s", formatWithDelta(report.StallCount, report.PrevStallCount)))
	}

	fields := []Field{
		{Name: "Closed", Value: formatWithDelta(report.CarsClosed, report.PrevCarsClosed), Short: true},
		{Name: "Merged", Value: formatWithDelta(report.CarsMerged, report.PrevCarsMerged), Short: true},
	}
	if report.MergeAttempts > 0 {
		fields = append(fields, Field{Name: "Merge Rate", Value: formatRateWithDelta(report.MergeSuccessRate, report.PrevMergeSuccessRate), Short: true})
	}
	if report.TotalTokens > 0 {
		fields = append(fields, Field{Name: "Tokens", Value: formatTokenCount(report.TotalTokens), Short: true})
	}
	if report.StallCount > 0 {
		fields = append(fields, Field{Name: "Stalls", Value: formatWithDelta(report.StallCount, report.PrevStallCount), Short: true})
	}

	// Track breakdown as fields.
	for _, td := range report.TrackBreakdown {
		val := fmt.Sprintf("%d completed, %d open", td.Completed, td.Open)
		if td.AvgCompletion > 0 {
			val += fmt.Sprintf(" (avg %s)", formatDuration(td.AvgCompletion))
		}
		fields = append(fields, Field{Name: td.Track, Value: val, Short: true})
	}

	return FormattedEvent{
		Title:    "📈 Weekly Digest",
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
