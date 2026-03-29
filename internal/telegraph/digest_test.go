package telegraph

import (
	"strings"
	"testing"
	"time"

	"github.com/zulandar/railyard/internal/models"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// openDigestTestDB opens an in-memory SQLite DB with the tables needed for
// digest queries (cars, engines, agent_logs).
func openDigestTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	if err := db.AutoMigrate(
		&models.Car{},
		&models.Engine{},
		&models.AgentLog{},
	); err != nil {
		t.Fatalf("auto migrate: %v", err)
	}
	return db
}

func ptr(t time.Time) *time.Time { return &t }

// ---------------------------------------------------------------------------
// BuildDailyDigest
// ---------------------------------------------------------------------------

func TestBuildDailyDigest_NoActivity(t *testing.T) {
	db := openDigestTestDB(t)
	w, _ := NewWatcher(WatcherOpts{DB: db})

	evt, err := w.BuildDailyDigest()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if evt != nil {
		t.Errorf("expected nil when no activity, got %v", evt)
	}
}

func TestBuildDailyDigest_WithActivity(t *testing.T) {
	db := openDigestTestDB(t)
	now := time.Now()
	recent := now.Add(-2 * time.Hour)

	// Create activity: a completed car, a created car, a stalled engine.
	db.Create(&models.Car{ID: "car-1", Title: "Done car", Status: "done", Track: "backend",
		CompletedAt: ptr(recent), CreatedAt: recent.Add(-12 * time.Hour)})
	db.Create(&models.Car{ID: "car-2", Title: "New car", Status: "open", Track: "backend",
		CreatedAt: recent})
	db.Create(&models.Engine{ID: "eng-1", Status: "stalled", Track: "backend",
		LastActivity: recent})
	db.Create(&models.AgentLog{EngineID: "eng-1", TokenCount: 5000, CreatedAt: recent})

	w, _ := NewWatcher(WatcherOpts{DB: db})
	evt, err := w.BuildDailyDigest()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if evt == nil {
		t.Fatal("expected event, got nil")
	}
	if evt.Type != EventDailyDigest {
		t.Errorf("type = %v, want %v", evt.Type, EventDailyDigest)
	}
	if !strings.Contains(evt.Title, "Daily Digest") {
		t.Errorf("title = %q, want to contain 'Daily Digest'", evt.Title)
	}
	if evt.Body == "" {
		t.Error("expected non-empty body")
	}
}

func TestBuildDailyDigest_OldActivitySuppressed(t *testing.T) {
	db := openDigestTestDB(t)
	old := time.Now().Add(-48 * time.Hour)

	// All activity is older than 24 hours.
	db.Create(&models.Car{ID: "car-1", Title: "Old", Status: "done", Track: "backend",
		CompletedAt: ptr(old), CreatedAt: old.Add(-12 * time.Hour)})

	w, _ := NewWatcher(WatcherOpts{DB: db})
	evt, err := w.BuildDailyDigest()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if evt != nil {
		t.Errorf("expected nil for old activity, got %v", evt)
	}
}

// ---------------------------------------------------------------------------
// BuildWeeklyDigest
// ---------------------------------------------------------------------------

func TestBuildWeeklyDigest_NoActivity(t *testing.T) {
	db := openDigestTestDB(t)
	w, _ := NewWatcher(WatcherOpts{DB: db})

	evt, err := w.BuildWeeklyDigest()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if evt != nil {
		t.Errorf("expected nil when no activity, got %v", evt)
	}
}

func TestBuildWeeklyDigest_WithActivity(t *testing.T) {
	db := openDigestTestDB(t)
	now := time.Now()
	recent := now.Add(-3 * 24 * time.Hour)

	db.Create(&models.Car{ID: "car-1", Title: "Done", Status: "done", Track: "backend",
		CompletedAt: ptr(recent), CreatedAt: recent.Add(-24 * time.Hour)})
	db.Create(&models.Car{ID: "car-2", Title: "Merged", Status: "merged", Track: "frontend",
		CompletedAt: ptr(recent.Add(time.Hour)), CreatedAt: recent})
	db.Create(&models.Engine{ID: "eng-1", Status: "stalled", Track: "backend",
		LastActivity: recent})
	db.Create(&models.AgentLog{EngineID: "eng-1", TokenCount: 15000, CreatedAt: recent})

	w, _ := NewWatcher(WatcherOpts{DB: db})
	evt, err := w.BuildWeeklyDigest()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if evt == nil {
		t.Fatal("expected event, got nil")
	}
	if evt.Type != EventWeeklyDigest {
		t.Errorf("type = %v, want %v", evt.Type, EventWeeklyDigest)
	}
	if !strings.Contains(evt.Title, "Weekly Digest") {
		t.Errorf("title = %q, want to contain 'Weekly Digest'", evt.Title)
	}
}

func TestBuildWeeklyDigest_OldActivitySuppressed(t *testing.T) {
	db := openDigestTestDB(t)
	old := time.Now().Add(-14 * 24 * time.Hour)

	db.Create(&models.Car{ID: "car-1", Title: "Old", Status: "done", Track: "backend",
		CompletedAt: ptr(old)})

	w, _ := NewWatcher(WatcherOpts{DB: db})
	evt, err := w.BuildWeeklyDigest()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if evt != nil {
		t.Errorf("expected nil for old activity, got %v", evt)
	}
}

// ---------------------------------------------------------------------------
// buildDailyReport
// ---------------------------------------------------------------------------

func TestBuildDailyReport_Counts(t *testing.T) {
	db := openDigestTestDB(t)
	now := time.Now()
	since := now.Add(-24 * time.Hour)
	mid := now.Add(-6 * time.Hour)

	// 2 completed, 1 merged, 3 created.
	db.Create(&models.Car{ID: "c1", Title: "Done1", Status: "done", Track: "backend",
		CompletedAt: ptr(mid), CreatedAt: mid.Add(-time.Hour)})
	db.Create(&models.Car{ID: "c2", Title: "Done2", Status: "done", Track: "backend",
		CompletedAt: ptr(mid.Add(time.Hour)), CreatedAt: mid})
	db.Create(&models.Car{ID: "c3", Title: "Merged", Status: "merged", Track: "frontend",
		CompletedAt: ptr(mid), CreatedAt: mid.Add(-2 * time.Hour)})
	db.Create(&models.Car{ID: "c4", Title: "New1", Status: "open", Track: "backend",
		CreatedAt: mid})
	db.Create(&models.Car{ID: "c5", Title: "New2", Status: "draft", Track: "frontend",
		CreatedAt: mid.Add(time.Hour)})

	// 1 stalled engine.
	db.Create(&models.Engine{ID: "e1", Status: "stalled", Track: "backend",
		LastActivity: mid})
	db.Create(&models.Engine{ID: "e2", Status: "working", Track: "frontend"})

	// Token sum.
	db.Create(&models.AgentLog{EngineID: "e1", TokenCount: 1000, CreatedAt: mid})
	db.Create(&models.AgentLog{EngineID: "e2", TokenCount: 2500, CreatedAt: mid.Add(time.Hour)})

	report, err := buildDailyReport(db, since, now)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if report.CarsCompleted != 2 {
		t.Errorf("CarsCompleted = %d, want 2", report.CarsCompleted)
	}
	if report.CarsMerged != 1 {
		t.Errorf("CarsMerged = %d, want 1", report.CarsMerged)
	}
	if report.CarsCreated != 5 {
		t.Errorf("CarsCreated = %d, want 5", report.CarsCreated)
	}
	if report.StallCount != 1 {
		t.Errorf("StallCount = %d, want 1", report.StallCount)
	}
	if report.TotalTokens != 3500 {
		t.Errorf("TotalTokens = %d, want 3500", report.TotalTokens)
	}
	if report.EngineCount != 2 {
		t.Errorf("EngineCount = %d, want 2", report.EngineCount)
	}
}

func TestBuildDailyReport_PeriodBoundaries(t *testing.T) {
	db := openDigestTestDB(t)
	now := time.Now()
	since := now.Add(-24 * time.Hour)

	// Car completed before the window.
	db.Create(&models.Car{ID: "old", Title: "Old", Status: "done", Track: "backend",
		CompletedAt: ptr(since.Add(-time.Hour))})

	report, err := buildDailyReport(db, since, now)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if report.CarsCompleted != 0 {
		t.Errorf("CarsCompleted = %d, want 0 (outside window)", report.CarsCompleted)
	}
}

func TestBuildDailyReport_NoTokens(t *testing.T) {
	db := openDigestTestDB(t)
	now := time.Now()
	since := now.Add(-24 * time.Hour)

	report, err := buildDailyReport(db, since, now)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if report.TotalTokens != 0 {
		t.Errorf("TotalTokens = %d, want 0", report.TotalTokens)
	}
}

// ---------------------------------------------------------------------------
// buildWeeklyReport
// ---------------------------------------------------------------------------

func TestBuildWeeklyReport_MergeSuccessRate(t *testing.T) {
	db := openDigestTestDB(t)
	now := time.Now()
	since := now.Add(-7 * 24 * time.Hour)
	mid := now.Add(-3 * 24 * time.Hour)

	// 3 merged, 1 merge-failed => 75% success rate.
	for i := 0; i < 3; i++ {
		db.Create(&models.Car{
			ID: "m" + string(rune('0'+i)), Title: "Merged", Status: "merged",
			Track: "backend", CompletedAt: ptr(mid), CreatedAt: mid.Add(-time.Hour),
		})
	}
	db.Create(&models.Car{ID: "mf1", Title: "Failed", Status: "merge-failed",
		Track: "backend", CreatedAt: mid.Add(-time.Hour), UpdatedAt: mid})

	report, err := buildWeeklyReport(db, since, now)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if report.CarsMerged != 3 {
		t.Errorf("CarsMerged = %d, want 3", report.CarsMerged)
	}
	if report.MergeAttempts != 4 {
		t.Errorf("MergeAttempts = %d, want 4", report.MergeAttempts)
	}
	if report.MergeSuccessRate != 75 {
		t.Errorf("MergeSuccessRate = %.1f, want 75", report.MergeSuccessRate)
	}
}

func TestBuildWeeklyReport_CarsClosed(t *testing.T) {
	db := openDigestTestDB(t)
	now := time.Now()
	since := now.Add(-7 * 24 * time.Hour)
	mid := now.Add(-2 * 24 * time.Hour)

	// done + merged + cancelled = 3 closed.
	db.Create(&models.Car{ID: "d1", Title: "Done", Status: "done", Track: "backend",
		CompletedAt: ptr(mid)})
	db.Create(&models.Car{ID: "m1", Title: "Merged", Status: "merged", Track: "frontend",
		CompletedAt: ptr(mid)})
	db.Create(&models.Car{ID: "x1", Title: "Cancelled", Status: "cancelled", Track: "backend",
		CompletedAt: ptr(mid)})

	report, err := buildWeeklyReport(db, since, now)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if report.CarsClosed != 3 {
		t.Errorf("CarsClosed = %d, want 3", report.CarsClosed)
	}
}

func TestBuildWeeklyReport_NoMergeAttempts(t *testing.T) {
	db := openDigestTestDB(t)
	now := time.Now()
	since := now.Add(-7 * 24 * time.Hour)

	report, err := buildWeeklyReport(db, since, now)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if report.MergeAttempts != 0 {
		t.Errorf("MergeAttempts = %d, want 0", report.MergeAttempts)
	}
	if report.MergeSuccessRate != 0 {
		t.Errorf("MergeSuccessRate = %.1f, want 0", report.MergeSuccessRate)
	}
}

// ---------------------------------------------------------------------------
// buildTrackBreakdown
// ---------------------------------------------------------------------------

func TestBuildTrackBreakdown_MultipleTracks(t *testing.T) {
	db := openDigestTestDB(t)
	now := time.Now()
	since := now.Add(-24 * time.Hour)
	mid := now.Add(-6 * time.Hour)

	db.Create(&models.Car{ID: "b1", Title: "B1", Status: "done", Track: "backend",
		CompletedAt: ptr(mid), ClaimedAt: ptr(mid.Add(-2 * time.Hour))})
	db.Create(&models.Car{ID: "b2", Title: "B2", Status: "open", Track: "backend"})
	db.Create(&models.Car{ID: "f1", Title: "F1", Status: "merged", Track: "frontend",
		CompletedAt: ptr(mid), ClaimedAt: ptr(mid.Add(-time.Hour))})

	breakdown := buildTrackBreakdown(db, since, now)
	if len(breakdown) < 2 {
		t.Fatalf("expected at least 2 tracks, got %d", len(breakdown))
	}

	trackMap := map[string]TrackDigest{}
	for _, td := range breakdown {
		trackMap[td.Track] = td
	}

	be := trackMap["backend"]
	if be.Completed != 1 {
		t.Errorf("backend completed = %d, want 1", be.Completed)
	}
	if be.Open != 1 {
		t.Errorf("backend open = %d, want 1", be.Open)
	}
	if be.AvgCompletion <= 0 {
		t.Errorf("backend avg completion should be > 0, got %v", be.AvgCompletion)
	}

	fe := trackMap["frontend"]
	if fe.Completed != 1 {
		t.Errorf("frontend completed = %d, want 1", fe.Completed)
	}
}

func TestBuildTrackBreakdown_EmptyTrackSkipped(t *testing.T) {
	db := openDigestTestDB(t)
	now := time.Now()
	since := now.Add(-24 * time.Hour)

	// Car with empty track — should be excluded.
	db.Create(&models.Car{ID: "x1", Title: "NoTrack", Status: "open", Track: ""})

	breakdown := buildTrackBreakdown(db, since, now)
	for _, td := range breakdown {
		if td.Track == "" {
			t.Error("empty track should be excluded from breakdown")
		}
	}
}

func TestBuildTrackBreakdown_NoCompletedCars(t *testing.T) {
	db := openDigestTestDB(t)
	now := time.Now()
	since := now.Add(-24 * time.Hour)

	db.Create(&models.Car{ID: "o1", Title: "Open", Status: "open", Track: "backend"})

	breakdown := buildTrackBreakdown(db, since, now)
	if len(breakdown) != 1 {
		t.Fatalf("expected 1 track, got %d", len(breakdown))
	}
	if breakdown[0].Completed != 0 {
		t.Errorf("completed = %d, want 0", breakdown[0].Completed)
	}
	if breakdown[0].Open != 1 {
		t.Errorf("open = %d, want 1", breakdown[0].Open)
	}
	if breakdown[0].AvgCompletion != 0 {
		t.Errorf("avg completion should be 0, got %v", breakdown[0].AvgCompletion)
	}
}

// ---------------------------------------------------------------------------
// FormatDaily
// ---------------------------------------------------------------------------

func TestFormatDaily_ContainsExpectedFields(t *testing.T) {
	report := &DailyReport{
		PeriodStart:   time.Date(2025, 1, 15, 0, 0, 0, 0, time.UTC),
		PeriodEnd:     time.Date(2025, 1, 16, 0, 0, 0, 0, time.UTC),
		CarsCreated:   5,
		CarsCompleted: 3,
		CarsMerged:    2,
		StallCount:    1,
		TotalTokens:   1500000,
		EngineCount:   4,
		TrackBreakdown: []TrackDigest{
			{Track: "backend", Completed: 2, Open: 3, AvgCompletion: 2 * time.Hour},
		},
	}

	f := FormatDaily(report, "")
	if !strings.Contains(f.Title, "Daily Digest") {
		t.Errorf("title = %q, want to contain 'Daily Digest'", f.Title)
	}
	if f.Severity != "info" {
		t.Errorf("severity = %q, want 'info'", f.Severity)
	}
	if f.Color != ColorInfo {
		t.Errorf("color = %q, want %q", f.Color, ColorInfo)
	}

	// Body should mention key metrics (Prev* are zero so deltas show positive).
	for _, want := range []string{"5 (\u25b25)", "3 (\u25b23)", "2 (\u25b22)", "1.5M", "4 registered"} {
		if !strings.Contains(f.Body, want) {
			t.Errorf("body missing %q:\n%s", want, f.Body)
		}
	}

	// Fields.
	if len(f.Fields) < 4 {
		t.Errorf("expected at least 4 fields, got %d", len(f.Fields))
	}
}

func TestFormatDaily_NoStallsOrTokens(t *testing.T) {
	report := &DailyReport{
		PeriodStart:   time.Date(2025, 1, 15, 0, 0, 0, 0, time.UTC),
		PeriodEnd:     time.Date(2025, 1, 16, 0, 0, 0, 0, time.UTC),
		CarsCreated:   1,
		CarsCompleted: 0,
		EngineCount:   1,
	}

	f := FormatDaily(report, "")
	if strings.Contains(f.Body, "Stalls") {
		t.Error("body should not mention stalls when 0")
	}
	if strings.Contains(f.Body, "Tokens") {
		t.Error("body should not mention tokens when 0")
	}
}

func TestFormatDaily_TrackAvgCompletion(t *testing.T) {
	report := &DailyReport{
		PeriodStart: time.Date(2025, 1, 15, 0, 0, 0, 0, time.UTC),
		PeriodEnd:   time.Date(2025, 1, 16, 0, 0, 0, 0, time.UTC),
		TrackBreakdown: []TrackDigest{
			{Track: "backend", Completed: 1, Open: 0, AvgCompletion: 90 * time.Minute},
		},
	}

	f := FormatDaily(report, "")
	hasAvg := false
	for _, fld := range f.Fields {
		if fld.Name == "backend" && strings.Contains(fld.Value, "avg") {
			hasAvg = true
		}
	}
	if !hasAvg {
		t.Errorf("track field should contain 'avg' for track with completion time, fields: %+v", f.Fields)
	}
}

// ---------------------------------------------------------------------------
// FormatWeekly
// ---------------------------------------------------------------------------

func TestFormatWeekly_ContainsExpectedFields(t *testing.T) {
	report := &WeeklyReport{
		PeriodStart:      time.Date(2025, 1, 8, 0, 0, 0, 0, time.UTC),
		PeriodEnd:        time.Date(2025, 1, 15, 0, 0, 0, 0, time.UTC),
		CarsClosed:       10,
		CarsMerged:       8,
		MergeAttempts:    9,
		MergeSuccessRate: 88.9,
		TotalTokens:      500000,
		StallCount:       2,
		TrackBreakdown: []TrackDigest{
			{Track: "backend", Completed: 5, Open: 2},
			{Track: "frontend", Completed: 3, Open: 1},
		},
	}

	f := FormatWeekly(report, "")
	if !strings.Contains(f.Title, "Weekly Digest") {
		t.Errorf("title = %q, want to contain 'Weekly Digest'", f.Title)
	}
	if f.Severity != "info" {
		t.Errorf("severity = %q, want 'info'", f.Severity)
	}

	// Prev* are zero so deltas show positive; MergeSuccessRate=88.9%, PrevMergeSuccessRate=0 → "▲89%".
	for _, want := range []string{"10 (\u25b210)", "8 (\u25b28)", "89%", "500.0K", "2 (\u25b22)"} {
		if !strings.Contains(f.Body, want) {
			t.Errorf("body missing %q:\n%s", want, f.Body)
		}
	}

	if len(f.Fields) < 2 {
		t.Errorf("expected at least 2 fields, got %d", len(f.Fields))
	}
}

func TestFormatWeekly_NoMergeAttempts(t *testing.T) {
	report := &WeeklyReport{
		PeriodStart: time.Date(2025, 1, 8, 0, 0, 0, 0, time.UTC),
		PeriodEnd:   time.Date(2025, 1, 15, 0, 0, 0, 0, time.UTC),
		CarsClosed:  2,
	}

	f := FormatWeekly(report, "")
	if strings.Contains(f.Body, "Merge Success Rate") {
		t.Error("body should not mention merge rate when 0 attempts")
	}
}

func TestFormatWeekly_NoStallsOrTokens(t *testing.T) {
	report := &WeeklyReport{
		PeriodStart: time.Date(2025, 1, 8, 0, 0, 0, 0, time.UTC),
		PeriodEnd:   time.Date(2025, 1, 15, 0, 0, 0, 0, time.UTC),
		CarsClosed:  1,
	}

	f := FormatWeekly(report, "")
	if strings.Contains(f.Body, "Stalls") {
		t.Error("body should not mention stalls when 0")
	}
	if strings.Contains(f.Body, "Tokens") {
		t.Error("body should not mention tokens when 0")
	}
}

// ---------------------------------------------------------------------------
// formatWithDelta
// ---------------------------------------------------------------------------

func TestFormatWithDelta(t *testing.T) {
	tests := []struct {
		current  int
		previous int
		want     string
	}{
		{12, 8, "12 (\u25b24)"},
		{8, 12, "8 (\u25bc4)"},
		{5, 5, "5 (=)"},
		{0, 0, "0 (=)"},
		{1, 0, "1 (\u25b21)"},
		{0, 3, "0 (\u25bc3)"},
	}
	for _, tt := range tests {
		got := formatWithDelta(tt.current, tt.previous)
		if got != tt.want {
			t.Errorf("formatWithDelta(%d, %d) = %q, want %q", tt.current, tt.previous, got, tt.want)
		}
	}
}

// ---------------------------------------------------------------------------
// formatRateWithDelta
// ---------------------------------------------------------------------------

func TestFormatRateWithDelta(t *testing.T) {
	tests := []struct {
		current  float64
		previous float64
		want     string
	}{
		{90.0, 75.0, "90% (\u25b215%)"},
		{75.0, 90.0, "75% (\u25bc15%)"},
		{88.0, 88.3, "88% (=)"},    // within ±0.5
		{88.0, 87.6, "88% (=)"},    // exactly 0.4 delta — within ±0.5
		{88.0, 87.4, "88% (\u25b21%)"}, // 0.6 delta — outside ±0.5
		{100.0, 0.0, "100% (\u25b2100%)"},
	}
	for _, tt := range tests {
		got := formatRateWithDelta(tt.current, tt.previous)
		if got != tt.want {
			t.Errorf("formatRateWithDelta(%.1f, %.1f) = %q, want %q", tt.current, tt.previous, got, tt.want)
		}
	}
}

// ---------------------------------------------------------------------------
// TestBuildDailyReport_PreviousPeriod
// ---------------------------------------------------------------------------

func TestBuildDailyReport_PreviousPeriod(t *testing.T) {
	db := openDigestTestDB(t)
	now := time.Now()
	since := now.Add(-24 * time.Hour)

	// Current period activity (within last 24h).
	curMid := now.Add(-6 * time.Hour)
	db.Create(&models.Car{ID: "cur-c1", Title: "Done1", Status: "done", Track: "backend",
		CompletedAt: ptr(curMid), CreatedAt: curMid.Add(-time.Hour)})
	db.Create(&models.Car{ID: "cur-c2", Title: "Done2", Status: "done", Track: "backend",
		CompletedAt: ptr(curMid.Add(time.Hour)), CreatedAt: curMid})
	db.Create(&models.Car{ID: "cur-m1", Title: "Merged1", Status: "merged", Track: "backend",
		CompletedAt: ptr(curMid), CreatedAt: curMid.Add(-2 * time.Hour)})

	// Prior period activity (25h–49h ago).
	prevMid := now.Add(-37 * time.Hour)
	db.Create(&models.Car{ID: "prev-c1", Title: "PrevDone", Status: "done", Track: "backend",
		CompletedAt: ptr(prevMid), CreatedAt: prevMid.Add(-time.Hour)})
	db.Create(&models.Engine{ID: "prev-e1", Status: "stalled", Track: "backend",
		LastActivity: prevMid})

	report, err := buildDailyReport(db, since, now)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Current period counts.
	if report.CarsCompleted != 2 {
		t.Errorf("CarsCompleted = %d, want 2", report.CarsCompleted)
	}
	if report.CarsMerged != 1 {
		t.Errorf("CarsMerged = %d, want 1", report.CarsMerged)
	}

	// Previous period counts.
	if report.PrevCarsCompleted != 1 {
		t.Errorf("PrevCarsCompleted = %d, want 1", report.PrevCarsCompleted)
	}
	if report.PrevStallCount != 1 {
		t.Errorf("PrevStallCount = %d, want 1", report.PrevStallCount)
	}
}

// ---------------------------------------------------------------------------
// TestFormatDaily_WithDeltas
// ---------------------------------------------------------------------------

func TestFormatDaily_WithDeltas(t *testing.T) {
	report := &DailyReport{
		PeriodStart:       time.Date(2025, 1, 15, 0, 0, 0, 0, time.UTC),
		PeriodEnd:         time.Date(2025, 1, 16, 0, 0, 0, 0, time.UTC),
		CarsCreated:       10,
		CarsCompleted:     6,
		CarsMerged:        4,
		StallCount:        2,
		EngineCount:       3,
		PrevCarsCreated:   6,
		PrevCarsCompleted: 6,
		PrevCarsMerged:    8,
		PrevStallCount:    2,
	}

	f := FormatDaily(report, "")

	// Created: +4 delta.
	if !strings.Contains(f.Body, "\u25b24") {
		t.Errorf("body missing upward arrow for created delta:\n%s", f.Body)
	}
	// Completed: equal.
	if !strings.Contains(f.Body, "6 (=)") {
		t.Errorf("body missing '6 (=)' for completed delta:\n%s", f.Body)
	}
	// Merged: negative delta.
	if !strings.Contains(f.Body, "4 (\u25bc4)") {
		t.Errorf("body missing '4 (\u25bc4)' for merged delta:\n%s", f.Body)
	}
	// Stalls: equal.
	if !strings.Contains(f.Body, "2 (=)") {
		t.Errorf("body missing '2 (=)' for stall delta:\n%s", f.Body)
	}

	// Verify fields use formatWithDelta too.
	fieldVals := map[string]string{}
	for _, fld := range f.Fields {
		fieldVals[fld.Name] = fld.Value
	}
	if fieldVals["Created"] != "10 (\u25b24)" {
		t.Errorf("Created field = %q, want '10 (\u25b24)'", fieldVals["Created"])
	}
	if fieldVals["Completed"] != "6 (=)" {
		t.Errorf("Completed field = %q, want '6 (=)'", fieldVals["Completed"])
	}
	if fieldVals["Merged"] != "4 (\u25bc4)" {
		t.Errorf("Merged field = %q, want '4 (\u25bc4)'", fieldVals["Merged"])
	}
}

// ---------------------------------------------------------------------------
// TestFormatDaily_FirstEver
// ---------------------------------------------------------------------------

func TestFormatDaily_FirstEver(t *testing.T) {
	// All Prev* == 0, current > 0 → positive deltas shown.
	report := &DailyReport{
		PeriodStart:   time.Date(2025, 1, 15, 0, 0, 0, 0, time.UTC),
		PeriodEnd:     time.Date(2025, 1, 16, 0, 0, 0, 0, time.UTC),
		CarsCreated:   12,
		CarsCompleted: 8,
		CarsMerged:    5,
		EngineCount:   2,
		// Prev* default to zero.
	}

	f := FormatDaily(report, "")

	if !strings.Contains(f.Body, "12 (\u25b212)") {
		t.Errorf("body missing '12 (\u25b212)' for first-ever created:\n%s", f.Body)
	}
	if !strings.Contains(f.Body, "8 (\u25b28)") {
		t.Errorf("body missing '8 (\u25b28)' for first-ever completed:\n%s", f.Body)
	}
	if !strings.Contains(f.Body, "5 (\u25b25)") {
		t.Errorf("body missing '5 (\u25b25)' for first-ever merged:\n%s", f.Body)
	}

	fieldVals := map[string]string{}
	for _, fld := range f.Fields {
		fieldVals[fld.Name] = fld.Value
	}
	if fieldVals["Created"] != "12 (\u25b212)" {
		t.Errorf("Created field = %q, want '12 (\u25b212)'", fieldVals["Created"])
	}
}

// ---------------------------------------------------------------------------
// formatTokenCount
// ---------------------------------------------------------------------------

func TestFormatTokenCount(t *testing.T) {
	tests := []struct {
		tokens int64
		want   string
	}{
		{0, "0"},
		{999, "999"},
		{1000, "1.0K"},
		{1500, "1.5K"},
		{999999, "1000.0K"},
		{1000000, "1.0M"},
		{2500000, "2.5M"},
	}
	for _, tt := range tests {
		got := formatTokenCount(tt.tokens)
		if got != tt.want {
			t.Errorf("formatTokenCount(%d) = %q, want %q", tt.tokens, got, tt.want)
		}
	}
}

// ---------------------------------------------------------------------------
// formatDuration
// ---------------------------------------------------------------------------

func TestFormatDuration(t *testing.T) {
	tests := []struct {
		d    time.Duration
		want string
	}{
		{30 * time.Second, "30s"},
		{5 * time.Minute, "5m"},
		{90 * time.Minute, "1h 30m"},
		{2 * time.Hour, "2h 0m"},
		{25 * time.Hour, "1d 1h"},
		{48 * time.Hour, "2d 0h"},
		{50*time.Hour + 30*time.Minute, "2d 2h"},
	}
	for _, tt := range tests {
		got := formatDuration(tt.d)
		if got != tt.want {
			t.Errorf("formatDuration(%v) = %q, want %q", tt.d, got, tt.want)
		}
	}
}

// ---------------------------------------------------------------------------
// Event type constants
// ---------------------------------------------------------------------------

func TestDigestEventTypes(t *testing.T) {
	if EventDailyDigest != "daily_digest" {
		t.Errorf("EventDailyDigest = %q, want 'daily_digest'", EventDailyDigest)
	}
	if EventWeeklyDigest != "weekly_digest" {
		t.Errorf("EventWeeklyDigest = %q, want 'weekly_digest'", EventWeeklyDigest)
	}
}

// ---------------------------------------------------------------------------
// Emoji, fields, and delta arrow tests
// ---------------------------------------------------------------------------

func TestFormatDaily_FieldsAndEmoji(t *testing.T) {
	report := &DailyReport{
		PeriodStart:       time.Date(2026, 3, 29, 0, 0, 0, 0, time.UTC),
		PeriodEnd:         time.Date(2026, 3, 30, 0, 0, 0, 0, time.UTC),
		CarsCreated:       5,
		CarsCompleted:     3,
		CarsMerged:        2,
		EngineCount:       4,
		PrevCarsCreated:   3,
		PrevCarsCompleted: 3,
		PrevCarsMerged:    1,
		TrackBreakdown: []TrackDigest{
			{Track: "backend", Completed: 2, Open: 3, AvgCompletion: 2 * time.Hour},
			{Track: "frontend", Completed: 1, Open: 1},
		},
	}
	got := FormatDaily(report, "")
	if !strings.Contains(got.Title, "\U0001f4ca") {
		t.Errorf("title should contain daily emoji, got: %q", got.Title)
	}
	if !strings.Contains(got.Body, "\u25b2") {
		t.Errorf("body should contain upward arrow for positive delta, got: %q", got.Body)
	}
	hasTrackField := false
	for _, f := range got.Fields {
		if f.Name == "backend" || f.Name == "frontend" {
			hasTrackField = true
		}
	}
	if !hasTrackField {
		t.Error("expected per-track fields in digest")
	}
}

func TestFormatWeekly_FieldsAndEmoji(t *testing.T) {
	report := &WeeklyReport{
		PeriodStart:          time.Date(2026, 3, 22, 0, 0, 0, 0, time.UTC),
		PeriodEnd:            time.Date(2026, 3, 29, 0, 0, 0, 0, time.UTC),
		CarsClosed:           10,
		CarsMerged:           8,
		MergeAttempts:        9,
		MergeSuccessRate:     88.9,
		PrevCarsClosed:       7,
		PrevCarsMerged:       6,
		PrevMergeSuccessRate: 75.0,
		TrackBreakdown:       []TrackDigest{{Track: "backend", Completed: 5, Open: 2}},
	}
	got := FormatWeekly(report, "")
	if !strings.Contains(got.Title, "\U0001f4c8") {
		t.Errorf("title should contain weekly emoji, got: %q", got.Title)
	}
	hasTrackField := false
	for _, f := range got.Fields {
		if f.Name == "backend" {
			hasTrackField = true
		}
	}
	if !hasTrackField {
		t.Error("expected per-track fields in weekly digest")
	}
}
