package dashboard

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/zulandar/railyard/internal/models"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// testDB creates an in-memory SQLite database with all tables needed by the dashboard package.
func testDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	if err := db.AutoMigrate(
		&models.Engine{},
		&models.Car{},
		&models.CarDep{},
		&models.CarProgress{},
		&models.Message{},
		&models.AgentLog{},
		&models.DispatchSession{},
		&models.TelegraphConversation{},
	); err != nil {
		t.Fatalf("migrate test db: %v", err)
	}
	return db
}

// --- EngineSummary tests ---

func TestEngineSummary_WithEngines(t *testing.T) {
	db := testDB(t)

	now := time.Now()
	db.Create(&models.Engine{ID: "eng-1", Track: "backend", Status: "active", LastActivity: now})
	db.Create(&models.Engine{ID: "eng-2", Track: "frontend", Status: "active", LastActivity: now})
	// Yardmaster should be excluded.
	db.Create(&models.Engine{ID: "ym-1", Track: "", Status: "active", Role: "yardmaster", LastActivity: now})
	// Old dead engine should be excluded (dead > 1 hour ago).
	db.Create(&models.Engine{ID: "eng-dead", Track: "backend", Status: "dead", LastActivity: now.Add(-2 * time.Hour)})

	rows, err := EngineSummary(db)
	if err != nil {
		t.Fatalf("EngineSummary: %v", err)
	}
	if len(rows) != 2 {
		t.Errorf("got %d rows, want 2 (active engines only)", len(rows))
	}
	ids := make(map[string]bool)
	for _, r := range rows {
		ids[r.ID] = true
	}
	if ids["ym-1"] {
		t.Error("yardmaster should be excluded")
	}
	if ids["eng-dead"] {
		t.Error("old dead engine should be excluded")
	}
}

func TestEngineSummary_RecentDeadIncluded(t *testing.T) {
	db := testDB(t)

	now := time.Now()
	// Dead engine with recent activity (< 1 hour) should be included.
	db.Create(&models.Engine{ID: "eng-recent-dead", Track: "backend", Status: "dead", LastActivity: now.Add(-30 * time.Minute)})

	rows, err := EngineSummary(db)
	if err != nil {
		t.Fatalf("EngineSummary: %v", err)
	}
	if len(rows) != 1 {
		t.Errorf("got %d rows, want 1 (recent dead included)", len(rows))
	}
	if len(rows) > 0 && rows[0].ID != "eng-recent-dead" {
		t.Errorf("ID = %q, want %q", rows[0].ID, "eng-recent-dead")
	}
}

func TestEngineSummary_Empty(t *testing.T) {
	db := testDB(t)

	rows, err := EngineSummary(db)
	if err != nil {
		t.Fatalf("EngineSummary: %v", err)
	}
	if len(rows) != 0 {
		t.Errorf("got %d rows, want 0", len(rows))
	}
}

// --- TrackSummary tests ---

func TestTrackSummary_WithCars(t *testing.T) {
	db := testDB(t)

	db.Create(&models.Car{ID: "car-1", Title: "T1", Track: "backend", Status: "open", Type: "task"})
	db.Create(&models.Car{ID: "car-2", Title: "T2", Track: "backend", Status: "done", Type: "task"})
	db.Create(&models.Car{ID: "car-3", Title: "T3", Track: "frontend", Status: "open", Type: "task"})
	// Epic should be excluded from track summary.
	db.Create(&models.Car{ID: "car-4", Title: "T4", Track: "backend", Status: "open", Type: "epic"})

	result, err := TrackSummary(db)
	if err != nil {
		t.Fatalf("TrackSummary: %v", err)
	}

	trackMap := make(map[string]TrackStatusCount)
	for _, tc := range result {
		trackMap[tc.Track] = tc
	}

	be, ok := trackMap["backend"]
	if !ok {
		t.Fatal("missing backend track")
	}
	if be.Open != 1 {
		t.Errorf("backend Open = %d, want 1", be.Open)
	}
	if be.Done != 1 {
		t.Errorf("backend Done = %d, want 1", be.Done)
	}
	if be.Total != 2 {
		t.Errorf("backend Total = %d, want 2 (epic excluded)", be.Total)
	}

	fe, ok := trackMap["frontend"]
	if !ok {
		t.Fatal("missing frontend track")
	}
	if fe.Open != 1 {
		t.Errorf("frontend Open = %d, want 1", fe.Open)
	}
}

func TestTrackSummary_ReadyMapsToOpen(t *testing.T) {
	db := testDB(t)

	db.Create(&models.Car{ID: "car-1", Title: "T1", Track: "backend", Status: "ready", Type: "task"})
	db.Create(&models.Car{ID: "car-2", Title: "T2", Track: "backend", Status: "open", Type: "task"})

	result, err := TrackSummary(db)
	if err != nil {
		t.Fatalf("TrackSummary: %v", err)
	}
	if len(result) != 1 {
		t.Fatalf("got %d tracks, want 1", len(result))
	}
	if result[0].Open != 2 {
		t.Errorf("Open = %d, want 2 (ready + open both count as Open)", result[0].Open)
	}
}

func TestTrackSummary_Empty(t *testing.T) {
	db := testDB(t)

	result, err := TrackSummary(db)
	if err != nil {
		t.Fatalf("TrackSummary: %v", err)
	}
	if len(result) != 0 {
		t.Errorf("got %d tracks, want 0", len(result))
	}
}

// --- MessageQueueDepth tests ---

func TestMessageQueueDepth_WithMessages(t *testing.T) {
	db := testDB(t)

	// Unacked non-broadcast - should be counted.
	db.Create(&models.Message{FromAgent: "eng-1", ToAgent: "yardmaster", Subject: "msg1", Acknowledged: false})
	db.Create(&models.Message{FromAgent: "eng-2", ToAgent: "human", Subject: "msg2", Acknowledged: false})
	// Broadcast - should be excluded.
	db.Create(&models.Message{FromAgent: "ym", ToAgent: "broadcast", Subject: "msg3", Acknowledged: false})
	// Acked - should be excluded.
	db.Create(&models.Message{FromAgent: "eng-1", ToAgent: "yardmaster", Subject: "msg4", Acknowledged: true})

	count, err := MessageQueueDepth(db)
	if err != nil {
		t.Fatalf("MessageQueueDepth: %v", err)
	}
	if count != 2 {
		t.Errorf("count = %d, want 2", count)
	}
}

func TestMessageQueueDepth_Empty(t *testing.T) {
	db := testDB(t)

	count, err := MessageQueueDepth(db)
	if err != nil {
		t.Fatalf("MessageQueueDepth: %v", err)
	}
	if count != 0 {
		t.Errorf("count = %d, want 0", count)
	}
}

// --- CarList tests ---

func TestCarList_ReturnsAllCars(t *testing.T) {
	db := testDB(t)

	db.Create(&models.Car{ID: "car-1", Title: "Low prio", Track: "backend", Status: "open", Type: "task", Priority: 3})
	db.Create(&models.Car{ID: "car-2", Title: "High prio", Track: "backend", Status: "open", Type: "task", Priority: 1})

	result := CarList(db, "", "", "", "")
	if len(result.Cars) != 2 {
		t.Fatalf("got %d cars, want 2", len(result.Cars))
	}
	// Should be ordered by priority ASC.
	if result.Cars[0].Title != "High prio" {
		t.Errorf("first car = %q, want %q", result.Cars[0].Title, "High prio")
	}
	if result.Cars[1].Title != "Low prio" {
		t.Errorf("second car = %q, want %q", result.Cars[1].Title, "Low prio")
	}
}

func TestCarList_FilterByTrack(t *testing.T) {
	db := testDB(t)

	db.Create(&models.Car{ID: "car-1", Title: "BE", Track: "backend", Status: "open", Type: "task"})
	db.Create(&models.Car{ID: "car-2", Title: "FE", Track: "frontend", Status: "open", Type: "task"})

	result := CarList(db, "backend", "", "", "")
	if len(result.Cars) != 1 {
		t.Fatalf("got %d cars, want 1", len(result.Cars))
	}
	if result.Cars[0].Track != "backend" {
		t.Errorf("Track = %q, want %q", result.Cars[0].Track, "backend")
	}
}

func TestCarList_FilterByStatus(t *testing.T) {
	db := testDB(t)

	db.Create(&models.Car{ID: "car-1", Title: "Open", Track: "backend", Status: "open", Type: "task"})
	db.Create(&models.Car{ID: "car-2", Title: "Done", Track: "backend", Status: "done", Type: "task"})

	result := CarList(db, "", "open", "", "")
	if len(result.Cars) != 1 {
		t.Fatalf("got %d cars, want 1", len(result.Cars))
	}
	if result.Cars[0].Status != "open" {
		t.Errorf("Status = %q, want %q", result.Cars[0].Status, "open")
	}
}

func TestCarList_FilterByType(t *testing.T) {
	db := testDB(t)

	db.Create(&models.Car{ID: "car-1", Title: "Task", Track: "backend", Status: "open", Type: "task"})
	db.Create(&models.Car{ID: "car-2", Title: "Bug", Track: "backend", Status: "open", Type: "bug"})

	result := CarList(db, "", "", "bug", "")
	if len(result.Cars) != 1 {
		t.Fatalf("got %d cars, want 1", len(result.Cars))
	}
	if result.Cars[0].Type != "bug" {
		t.Errorf("Type = %q, want %q", result.Cars[0].Type, "bug")
	}
}

func TestCarList_FilterByParent(t *testing.T) {
	db := testDB(t)

	parentID := "car-epic"
	db.Create(&models.Car{ID: "car-epic", Title: "Epic", Track: "backend", Status: "open", Type: "epic"})
	db.Create(&models.Car{ID: "car-child", Title: "Child", Track: "backend", Status: "open", Type: "task", ParentID: &parentID})
	db.Create(&models.Car{ID: "car-orphan", Title: "Orphan", Track: "backend", Status: "open", Type: "task"})

	result := CarList(db, "", "", "", "car-epic")
	if len(result.Cars) != 1 {
		t.Fatalf("got %d cars, want 1", len(result.Cars))
	}
	if result.Cars[0].ID != "car-child" {
		t.Errorf("ID = %q, want %q", result.Cars[0].ID, "car-child")
	}
}

func TestCarList_CombinedFilters(t *testing.T) {
	db := testDB(t)

	db.Create(&models.Car{ID: "car-1", Title: "BE open task", Track: "backend", Status: "open", Type: "task"})
	db.Create(&models.Car{ID: "car-2", Title: "BE done task", Track: "backend", Status: "done", Type: "task"})
	db.Create(&models.Car{ID: "car-3", Title: "FE open task", Track: "frontend", Status: "open", Type: "task"})

	result := CarList(db, "backend", "open", "task", "")
	if len(result.Cars) != 1 {
		t.Fatalf("got %d cars, want 1", len(result.Cars))
	}
	if result.Cars[0].ID != "car-1" {
		t.Errorf("ID = %q, want %q", result.Cars[0].ID, "car-1")
	}
}

func TestCarList_PopulatesDropdowns(t *testing.T) {
	db := testDB(t)

	db.Create(&models.Car{ID: "car-1", Title: "T1", Track: "backend", Status: "open", Type: "task"})
	db.Create(&models.Car{ID: "car-2", Title: "T2", Track: "frontend", Status: "done", Type: "bug"})

	result := CarList(db, "", "", "", "")
	if len(result.Tracks) < 2 {
		t.Errorf("Tracks = %v, want at least 2", result.Tracks)
	}
	if len(result.Statuses) < 2 {
		t.Errorf("Statuses = %v, want at least 2", result.Statuses)
	}
	if len(result.Types) < 2 {
		t.Errorf("Types = %v, want at least 2", result.Types)
	}
}

func TestCarList_Empty(t *testing.T) {
	db := testDB(t)

	result := CarList(db, "", "", "", "")
	if len(result.Cars) != 0 {
		t.Errorf("got %d cars, want 0", len(result.Cars))
	}
}

// --- GetCarDetail tests ---

func TestGetCarDetail_BasicFields(t *testing.T) {
	db := testDB(t)

	db.Create(&models.Car{
		ID:          "car-1",
		Title:       "Test car",
		Description: "A description",
		Type:        "task",
		Status:      "open",
		Priority:    1,
		Track:       "backend",
		Branch:      "ry/test/car-1",
		Assignee:    "eng-1",
		DesignNotes: "design",
		Acceptance:  "acceptance",
		SkipTests:   true,
	})

	detail, err := GetCarDetail(db, "car-1")
	if err != nil {
		t.Fatalf("GetCarDetail: %v", err)
	}
	if detail.ID != "car-1" {
		t.Errorf("ID = %q, want %q", detail.ID, "car-1")
	}
	if detail.Title != "Test car" {
		t.Errorf("Title = %q, want %q", detail.Title, "Test car")
	}
	if detail.Description != "A description" {
		t.Errorf("Description = %q, want %q", detail.Description, "A description")
	}
	if detail.Track != "backend" {
		t.Errorf("Track = %q, want %q", detail.Track, "backend")
	}
	if !detail.SkipTests {
		t.Error("SkipTests = false, want true")
	}
}

func TestGetCarDetail_WithParent(t *testing.T) {
	db := testDB(t)

	db.Create(&models.Car{ID: "car-epic", Title: "Parent Epic", Type: "epic", Track: "backend", Status: "open"})
	parentID := "car-epic"
	db.Create(&models.Car{ID: "car-child", Title: "Child", Type: "task", Track: "backend", Status: "open", ParentID: &parentID})

	detail, err := GetCarDetail(db, "car-child")
	if err != nil {
		t.Fatalf("GetCarDetail: %v", err)
	}
	if detail.ParentID != "car-epic" {
		t.Errorf("ParentID = %q, want %q", detail.ParentID, "car-epic")
	}
	if detail.ParentTitle != "Parent Epic" {
		t.Errorf("ParentTitle = %q, want %q", detail.ParentTitle, "Parent Epic")
	}
}

func TestGetCarDetail_WithChildren(t *testing.T) {
	db := testDB(t)

	db.Create(&models.Car{ID: "car-epic", Title: "Epic", Type: "epic", Track: "backend", Status: "open"})
	epicID := "car-epic"
	db.Create(&models.Car{ID: "car-c1", Title: "Child Low", Type: "task", Track: "backend", Status: "open", Priority: 3, ParentID: &epicID})
	db.Create(&models.Car{ID: "car-c2", Title: "Child High", Type: "task", Track: "backend", Status: "open", Priority: 1, ParentID: &epicID})

	detail, err := GetCarDetail(db, "car-epic")
	if err != nil {
		t.Fatalf("GetCarDetail: %v", err)
	}
	if len(detail.Children) != 2 {
		t.Fatalf("Children count = %d, want 2", len(detail.Children))
	}
	// Ordered by priority ASC.
	if detail.Children[0].Title != "Child High" {
		t.Errorf("first child = %q, want %q", detail.Children[0].Title, "Child High")
	}
	if detail.Children[1].Title != "Child Low" {
		t.Errorf("second child = %q, want %q", detail.Children[1].Title, "Child Low")
	}
}

func TestGetCarDetail_WithDeps(t *testing.T) {
	db := testDB(t)

	db.Create(&models.Car{ID: "car-a", Title: "Car A", Type: "task", Track: "backend", Status: "open"})
	db.Create(&models.Car{ID: "car-b", Title: "Car B", Type: "task", Track: "backend", Status: "open"})
	db.Create(&models.Car{ID: "car-c", Title: "Car C", Type: "task", Track: "backend", Status: "open"})
	// car-b is blocked by car-a.
	db.Create(&models.CarDep{CarID: "car-b", BlockedBy: "car-a"})
	// car-c is blocked by car-b.
	db.Create(&models.CarDep{CarID: "car-c", BlockedBy: "car-b"})

	detail, err := GetCarDetail(db, "car-b")
	if err != nil {
		t.Fatalf("GetCarDetail: %v", err)
	}
	if len(detail.BlockedBy) != 1 {
		t.Fatalf("BlockedBy count = %d, want 1", len(detail.BlockedBy))
	}
	if detail.BlockedBy[0].CarID != "car-a" {
		t.Errorf("BlockedBy[0].CarID = %q, want %q", detail.BlockedBy[0].CarID, "car-a")
	}
	if len(detail.Blocks) != 1 {
		t.Fatalf("Blocks count = %d, want 1", len(detail.Blocks))
	}
	if detail.Blocks[0].CarID != "car-c" {
		t.Errorf("Blocks[0].CarID = %q, want %q", detail.Blocks[0].CarID, "car-c")
	}
}

func TestGetCarDetail_WithProgress(t *testing.T) {
	db := testDB(t)

	db.Create(&models.Car{ID: "car-1", Title: "T1", Type: "task", Track: "backend", Status: "open"})
	db.Create(&models.CarProgress{CarID: "car-1", Cycle: 1, EngineID: "eng-1", Note: "First pass"})
	db.Create(&models.CarProgress{CarID: "car-1", Cycle: 2, EngineID: "eng-1", Note: "Second pass"})

	detail, err := GetCarDetail(db, "car-1")
	if err != nil {
		t.Fatalf("GetCarDetail: %v", err)
	}
	if len(detail.Progress) != 2 {
		t.Fatalf("Progress count = %d, want 2", len(detail.Progress))
	}
	if detail.Progress[0].Note != "First pass" {
		t.Errorf("Progress[0].Note = %q, want %q", detail.Progress[0].Note, "First pass")
	}
}

func TestGetCarDetail_NotFound(t *testing.T) {
	db := testDB(t)

	_, err := GetCarDetail(db, "car-nonexistent")
	if err == nil {
		t.Fatal("expected error for non-existent car")
	}
}

// --- DependencyGraph tests ---

func TestDependencyGraph_BasicChain(t *testing.T) {
	db := testDB(t)

	// A → B → C chain (A blocks B, B blocks C).
	db.Create(&models.Car{ID: "car-a", Title: "Car A", Type: "task", Track: "backend", Status: "open"})
	db.Create(&models.Car{ID: "car-b", Title: "Car B", Type: "task", Track: "backend", Status: "open"})
	db.Create(&models.Car{ID: "car-c", Title: "Car C", Type: "task", Track: "backend", Status: "open"})
	db.Create(&models.CarDep{CarID: "car-b", BlockedBy: "car-a"})
	db.Create(&models.CarDep{CarID: "car-c", BlockedBy: "car-b"})

	result := DependencyGraph(db, "car-b")
	if len(result.Nodes) != 3 {
		t.Errorf("Nodes count = %d, want 3", len(result.Nodes))
	}
	if len(result.Edges) < 2 {
		t.Errorf("Edges count = %d, want >= 2", len(result.Edges))
	}

	nodeIDs := make(map[string]bool)
	for _, n := range result.Nodes {
		nodeIDs[n.CarID] = true
	}
	for _, id := range []string{"car-a", "car-b", "car-c"} {
		if !nodeIDs[id] {
			t.Errorf("missing node %q", id)
		}
	}
}

func TestDependencyGraph_NoDeps(t *testing.T) {
	db := testDB(t)

	db.Create(&models.Car{ID: "car-solo", Title: "Solo", Type: "task", Track: "backend", Status: "open"})

	result := DependencyGraph(db, "car-solo")
	if len(result.Nodes) != 1 {
		t.Errorf("Nodes count = %d, want 1", len(result.Nodes))
	}
	if len(result.Edges) != 0 {
		t.Errorf("Edges count = %d, want 0", len(result.Edges))
	}
}

func TestDependencyGraph_NotFound(t *testing.T) {
	db := testDB(t)

	result := DependencyGraph(db, "car-nonexistent")
	if len(result.Nodes) != 0 {
		t.Errorf("Nodes count = %d, want 0", len(result.Nodes))
	}
}

// --- GetEngineDetail tests ---

func TestGetEngineDetail_WithCurrentCar(t *testing.T) {
	db := testDB(t)

	started := time.Now().Add(-2 * time.Hour)
	db.Create(&models.Engine{ID: "eng-1", Track: "backend", Status: "active", CurrentCar: "car-1", StartedAt: started, LastActivity: time.Now()})
	db.Create(&models.Car{ID: "car-1", Title: "Active car", Type: "task", Track: "backend", Status: "in_progress"})

	detail, err := GetEngineDetail(db, "eng-1")
	if err != nil {
		t.Fatalf("GetEngineDetail: %v", err)
	}
	if detail.CurrentCar != "car-1" {
		t.Errorf("CurrentCar = %q, want %q", detail.CurrentCar, "car-1")
	}
	if detail.CurrentTitle != "Active car" {
		t.Errorf("CurrentTitle = %q, want %q", detail.CurrentTitle, "Active car")
	}
	if detail.CurrentStatus != "in_progress" {
		t.Errorf("CurrentStatus = %q, want %q", detail.CurrentStatus, "in_progress")
	}
	if detail.Uptime == "" {
		t.Error("Uptime should be computed")
	}
}

func TestGetEngineDetail_NoCar(t *testing.T) {
	db := testDB(t)

	db.Create(&models.Engine{ID: "eng-idle", Track: "backend", Status: "active", CurrentCar: "", StartedAt: time.Now()})

	detail, err := GetEngineDetail(db, "eng-idle")
	if err != nil {
		t.Fatalf("GetEngineDetail: %v", err)
	}
	if detail.CurrentCar != "" {
		t.Errorf("CurrentCar = %q, want empty", detail.CurrentCar)
	}
	if detail.CurrentTitle != "" {
		t.Errorf("CurrentTitle = %q, want empty", detail.CurrentTitle)
	}
	if detail.CurrentStatus != "" {
		t.Errorf("CurrentStatus = %q, want empty", detail.CurrentStatus)
	}
}

func TestGetEngineDetail_NotFound(t *testing.T) {
	db := testDB(t)

	_, err := GetEngineDetail(db, "eng-nonexistent")
	if err == nil {
		t.Fatal("expected error for non-existent engine")
	}
}

// --- GetEngineActivity tests ---

func TestGetEngineActivity_WithNotes(t *testing.T) {
	db := testDB(t)

	db.Create(&models.Car{ID: "car-1", Title: "Test Car", Type: "task", Track: "backend", Status: "open"})
	db.Create(&models.CarProgress{CarID: "car-1", Cycle: 1, EngineID: "eng-1", Note: "Did some work", CreatedAt: time.Now()})
	db.Create(&models.CarProgress{CarID: "car-1", Cycle: 2, EngineID: "eng-1", Note: "More work", CreatedAt: time.Now()})
	// Different engine - should not appear.
	db.Create(&models.CarProgress{CarID: "car-1", Cycle: 1, EngineID: "eng-2", Note: "Other engine"})

	rows := GetEngineActivity(db, "eng-1")
	if len(rows) != 2 {
		t.Fatalf("got %d rows, want 2", len(rows))
	}
	if rows[0].CarTitle != "Test Car" {
		t.Errorf("CarTitle = %q, want %q", rows[0].CarTitle, "Test Car")
	}
}

func TestGetEngineActivity_Empty(t *testing.T) {
	db := testDB(t)

	rows := GetEngineActivity(db, "eng-nonexistent")
	if len(rows) != 0 {
		t.Errorf("got %d rows, want 0", len(rows))
	}
}

// --- RecentEscalations tests ---

func TestRecentEscalations_WithData(t *testing.T) {
	db := testDB(t)

	// Unacked to human - should be included.
	db.Create(&models.Message{FromAgent: "eng-1", ToAgent: "human", Subject: "Help", Body: "stuck", Acknowledged: false})
	// Acked to human - should be excluded.
	db.Create(&models.Message{FromAgent: "eng-2", ToAgent: "human", Subject: "Old", Body: "done", Acknowledged: true})
	// Unacked to other agent - should be excluded.
	db.Create(&models.Message{FromAgent: "eng-1", ToAgent: "yardmaster", Subject: "Status", Body: "ok", Acknowledged: false})

	result, err := RecentEscalations(db)
	if err != nil {
		t.Fatalf("RecentEscalations: %v", err)
	}
	if len(result) != 1 {
		t.Fatalf("got %d escalations, want 1", len(result))
	}
	if result[0].Subject != "Help" {
		t.Errorf("Subject = %q, want %q", result[0].Subject, "Help")
	}
}

func TestRecentEscalations_Empty(t *testing.T) {
	db := testDB(t)

	result, err := RecentEscalations(db)
	if err != nil {
		t.Fatalf("RecentEscalations: %v", err)
	}
	if len(result) != 0 {
		t.Errorf("got %d escalations, want 0", len(result))
	}
}

// --- ListMessages tests ---

func TestListMessages_AllMessages(t *testing.T) {
	db := testDB(t)

	db.Create(&models.Message{FromAgent: "eng-1", ToAgent: "ym", Subject: "M1"})
	db.Create(&models.Message{FromAgent: "eng-2", ToAgent: "ym", Subject: "M2"})

	result := ListMessages(db, MessageFilters{})
	if len(result.Messages) != 2 {
		t.Errorf("got %d messages, want 2", len(result.Messages))
	}
}

func TestListMessages_FilterByAgent(t *testing.T) {
	db := testDB(t)

	db.Create(&models.Message{FromAgent: "eng-1", ToAgent: "ym", Subject: "From eng-1"})
	db.Create(&models.Message{FromAgent: "ym", ToAgent: "eng-1", Subject: "To eng-1"})
	db.Create(&models.Message{FromAgent: "eng-2", ToAgent: "ym", Subject: "Other"})

	result := ListMessages(db, MessageFilters{Agent: "eng-1"})
	if len(result.Messages) != 2 {
		t.Errorf("got %d messages, want 2 (from_agent OR to_agent match)", len(result.Messages))
	}
}

func TestListMessages_FilterByPriority(t *testing.T) {
	db := testDB(t)

	db.Create(&models.Message{FromAgent: "eng-1", ToAgent: "ym", Subject: "Urgent", Priority: "urgent"})
	db.Create(&models.Message{FromAgent: "eng-1", ToAgent: "ym", Subject: "Normal", Priority: "normal"})

	result := ListMessages(db, MessageFilters{Priority: "urgent"})
	if len(result.Messages) != 1 {
		t.Fatalf("got %d messages, want 1", len(result.Messages))
	}
	if result.Messages[0].Subject != "Urgent" {
		t.Errorf("Subject = %q, want %q", result.Messages[0].Subject, "Urgent")
	}
}

func TestListMessages_FilterUnacked(t *testing.T) {
	db := testDB(t)

	db.Create(&models.Message{FromAgent: "eng-1", ToAgent: "ym", Subject: "Unacked", Acknowledged: false})
	db.Create(&models.Message{FromAgent: "eng-1", ToAgent: "ym", Subject: "Acked", Acknowledged: true})

	result := ListMessages(db, MessageFilters{Unacked: true})
	if len(result.Messages) != 1 {
		t.Fatalf("got %d messages, want 1", len(result.Messages))
	}
	if result.Messages[0].Subject != "Unacked" {
		t.Errorf("Subject = %q, want %q", result.Messages[0].Subject, "Unacked")
	}
}

func TestListMessages_PopulatesDropdowns(t *testing.T) {
	db := testDB(t)

	db.Create(&models.Message{FromAgent: "eng-1", ToAgent: "ym", Priority: "urgent"})
	db.Create(&models.Message{FromAgent: "eng-2", ToAgent: "human", Priority: "normal"})

	result := ListMessages(db, MessageFilters{})
	if len(result.Agents) < 2 {
		t.Errorf("Agents = %v, want at least 2", result.Agents)
	}
	if len(result.Priorities) < 2 {
		t.Errorf("Priorities = %v, want at least 2", result.Priorities)
	}
}

// --- PendingEscalationCount tests ---

func TestPendingEscalationCount_WithData(t *testing.T) {
	db := testDB(t)

	db.Create(&models.Message{FromAgent: "eng-1", ToAgent: "human", Acknowledged: false})
	db.Create(&models.Message{FromAgent: "eng-2", ToAgent: "human", Acknowledged: false})
	db.Create(&models.Message{FromAgent: "eng-1", ToAgent: "human", Acknowledged: true})
	db.Create(&models.Message{FromAgent: "eng-1", ToAgent: "yardmaster", Acknowledged: false})

	count := PendingEscalationCount(db)
	if count != 2 {
		t.Errorf("count = %d, want 2", count)
	}
}

// --- AgentLogList tests ---

func TestAgentLogList_AllLogs(t *testing.T) {
	db := testDB(t)

	db.Create(&models.AgentLog{EngineID: "eng-1", Direction: "in", Content: "prompt", CarID: "car-1"})
	db.Create(&models.AgentLog{EngineID: "eng-1", Direction: "out", Content: "response", CarID: "car-1"})

	result := AgentLogList(db, AgentLogFilters{})
	if len(result.Logs) != 2 {
		t.Errorf("got %d logs, want 2", len(result.Logs))
	}
}

func TestAgentLogList_FilterByEngine(t *testing.T) {
	db := testDB(t)

	db.Create(&models.AgentLog{EngineID: "eng-1", Direction: "in", Content: "p1"})
	db.Create(&models.AgentLog{EngineID: "eng-2", Direction: "in", Content: "p2"})

	result := AgentLogList(db, AgentLogFilters{EngineID: "eng-1"})
	if len(result.Logs) != 1 {
		t.Fatalf("got %d logs, want 1", len(result.Logs))
	}
	if result.Logs[0].EngineID != "eng-1" {
		t.Errorf("EngineID = %q, want %q", result.Logs[0].EngineID, "eng-1")
	}
}

func TestAgentLogList_FilterByDirection(t *testing.T) {
	db := testDB(t)

	db.Create(&models.AgentLog{EngineID: "eng-1", Direction: "in", Content: "prompt"})
	db.Create(&models.AgentLog{EngineID: "eng-1", Direction: "out", Content: "response"})

	result := AgentLogList(db, AgentLogFilters{Direction: "out"})
	if len(result.Logs) != 1 {
		t.Fatalf("got %d logs, want 1", len(result.Logs))
	}
	if result.Logs[0].Direction != "out" {
		t.Errorf("Direction = %q, want %q", result.Logs[0].Direction, "out")
	}
}

func TestAgentLogList_FilterByCarID(t *testing.T) {
	db := testDB(t)

	db.Create(&models.AgentLog{EngineID: "eng-1", Direction: "in", Content: "p1", CarID: "car-1"})
	db.Create(&models.AgentLog{EngineID: "eng-1", Direction: "in", Content: "p2", CarID: "car-2"})

	result := AgentLogList(db, AgentLogFilters{CarID: "car-1"})
	if len(result.Logs) != 1 {
		t.Fatalf("got %d logs, want 1", len(result.Logs))
	}
	if result.Logs[0].CarID != "car-1" {
		t.Errorf("CarID = %q, want %q", result.Logs[0].CarID, "car-1")
	}
}

func TestAgentLogList_TruncatesContent(t *testing.T) {
	db := testDB(t)

	longContent := strings.Repeat("x", 300)
	db.Create(&models.AgentLog{EngineID: "eng-1", Direction: "in", Content: longContent})

	result := AgentLogList(db, AgentLogFilters{})
	if len(result.Logs) != 1 {
		t.Fatalf("got %d logs, want 1", len(result.Logs))
	}
	if len(result.Logs[0].Content) != 203 {
		t.Errorf("Content length = %d, want 203 (200 + '...')", len(result.Logs[0].Content))
	}
	if !strings.HasSuffix(result.Logs[0].Content, "...") {
		t.Error("truncated content should end with '...'")
	}
}

func TestAgentLogList_PopulatesDropdowns(t *testing.T) {
	db := testDB(t)

	db.Create(&models.AgentLog{EngineID: "eng-1", Direction: "in", Content: "p1", CarID: "car-1"})
	db.Create(&models.AgentLog{EngineID: "eng-2", Direction: "out", Content: "p2", CarID: "car-2"})

	result := AgentLogList(db, AgentLogFilters{})
	if len(result.Engines) < 2 {
		t.Errorf("Engines = %v, want at least 2", result.Engines)
	}
	if len(result.Cars) < 2 {
		t.Errorf("Cars = %v, want at least 2", result.Cars)
	}
	if len(result.Directions) < 2 {
		t.Errorf("Directions = %v, want at least 2", result.Directions)
	}
}

// --- TokenUsageSummary tests ---

func TestTokenUsageSummary_WithData(t *testing.T) {
	db := testDB(t)

	db.Create(&models.Engine{ID: "eng-1", Track: "backend", Status: "active"})
	db.Create(&models.Engine{ID: "eng-2", Track: "frontend", Status: "active"})
	// Only direction=out should be aggregated.
	db.Create(&models.AgentLog{EngineID: "eng-1", Direction: "out", InputTokens: 100, OutputTokens: 200, TokenCount: 300, Model: "gpt-4", CreatedAt: time.Now()})
	db.Create(&models.AgentLog{EngineID: "eng-1", Direction: "out", InputTokens: 50, OutputTokens: 100, TokenCount: 150, Model: "gpt-4", CreatedAt: time.Now()})
	db.Create(&models.AgentLog{EngineID: "eng-2", Direction: "out", InputTokens: 80, OutputTokens: 160, TokenCount: 240, Model: "claude", CreatedAt: time.Now()})
	// direction=in should be excluded.
	db.Create(&models.AgentLog{EngineID: "eng-1", Direction: "in", InputTokens: 999, OutputTokens: 999, TokenCount: 999})

	result := TokenUsageSummary(db)
	if len(result.ByEngine) != 2 {
		t.Fatalf("ByEngine count = %d, want 2", len(result.ByEngine))
	}

	engineMap := make(map[string]TokenUsageRow)
	for _, r := range result.ByEngine {
		engineMap[r.EngineID] = r
	}

	eng1 := engineMap["eng-1"]
	if eng1.InputTokens != 150 {
		t.Errorf("eng-1 InputTokens = %d, want 150", eng1.InputTokens)
	}
	if eng1.OutputTokens != 300 {
		t.Errorf("eng-1 OutputTokens = %d, want 300", eng1.OutputTokens)
	}
	if eng1.Track != "backend" {
		t.Errorf("eng-1 Track = %q, want %q", eng1.Track, "backend")
	}
	if eng1.Model != "gpt-4" {
		t.Errorf("eng-1 Model = %q, want %q", eng1.Model, "gpt-4")
	}

	if result.TotalInput != 230 {
		t.Errorf("TotalInput = %d, want 230", result.TotalInput)
	}
	if result.TotalOutput != 460 {
		t.Errorf("TotalOutput = %d, want 460", result.TotalOutput)
	}
}

func TestTokenUsageSummary_Empty(t *testing.T) {
	db := testDB(t)

	result := TokenUsageSummary(db)
	if len(result.ByEngine) != 0 {
		t.Errorf("ByEngine count = %d, want 0", len(result.ByEngine))
	}
	if result.TotalInput != 0 {
		t.Errorf("TotalInput = %d, want 0", result.TotalInput)
	}
}

// --- SessionList tests ---

func TestSessionList_AllSessions(t *testing.T) {
	db := testDB(t)

	db.Create(&models.DispatchSession{Source: "telegraph", UserName: "alice", Status: "active"})
	db.Create(&models.DispatchSession{Source: "local", UserName: "bob", Status: "completed"})

	result := SessionList(db, SessionFilters{})
	if len(result.Sessions) != 2 {
		t.Errorf("got %d sessions, want 2", len(result.Sessions))
	}
}

func TestSessionList_FilterBySource(t *testing.T) {
	db := testDB(t)

	db.Create(&models.DispatchSession{Source: "telegraph", UserName: "alice", Status: "active"})
	db.Create(&models.DispatchSession{Source: "local", UserName: "bob", Status: "active"})

	result := SessionList(db, SessionFilters{Source: "telegraph"})
	if len(result.Sessions) != 1 {
		t.Fatalf("got %d sessions, want 1", len(result.Sessions))
	}
	if result.Sessions[0].Source != "telegraph" {
		t.Errorf("Source = %q, want %q", result.Sessions[0].Source, "telegraph")
	}
}

func TestSessionList_FilterByStatus(t *testing.T) {
	db := testDB(t)

	db.Create(&models.DispatchSession{Source: "local", UserName: "alice", Status: "active"})
	db.Create(&models.DispatchSession{Source: "local", UserName: "bob", Status: "completed"})

	result := SessionList(db, SessionFilters{Status: "completed"})
	if len(result.Sessions) != 1 {
		t.Fatalf("got %d sessions, want 1", len(result.Sessions))
	}
	if result.Sessions[0].Status != "completed" {
		t.Errorf("Status = %q, want %q", result.Sessions[0].Status, "completed")
	}
}

func TestSessionList_FilterByUser(t *testing.T) {
	db := testDB(t)

	db.Create(&models.DispatchSession{Source: "local", UserName: "alice", Status: "active"})
	db.Create(&models.DispatchSession{Source: "local", UserName: "bob", Status: "active"})

	result := SessionList(db, SessionFilters{UserName: "alice"})
	if len(result.Sessions) != 1 {
		t.Fatalf("got %d sessions, want 1", len(result.Sessions))
	}
	if result.Sessions[0].UserName != "alice" {
		t.Errorf("UserName = %q, want %q", result.Sessions[0].UserName, "alice")
	}
}

func TestSessionList_CarsCreatedCount(t *testing.T) {
	db := testDB(t)

	db.Create(&models.DispatchSession{Source: "local", UserName: "alice", Status: "active", CarsCreated: `["car-1","car-2","car-3"]`})
	db.Create(&models.DispatchSession{Source: "local", UserName: "bob", Status: "active", CarsCreated: `[]`})

	result := SessionList(db, SessionFilters{})
	countMap := make(map[string]int)
	for _, s := range result.Sessions {
		countMap[s.UserName] = s.CarsCreatedCount
	}
	if countMap["alice"] != 3 {
		t.Errorf("alice CarsCreatedCount = %d, want 3", countMap["alice"])
	}
	if countMap["bob"] != 0 {
		t.Errorf("bob CarsCreatedCount = %d, want 0", countMap["bob"])
	}
}

func TestSessionList_CompletedDuration(t *testing.T) {
	db := testDB(t)

	created := time.Now().Add(-2 * time.Hour)
	completed := time.Now()
	db.Create(&models.DispatchSession{Source: "local", UserName: "alice", Status: "completed", CreatedAt: created, CompletedAt: &completed})

	result := SessionList(db, SessionFilters{})
	if len(result.Sessions) != 1 {
		t.Fatalf("got %d sessions, want 1", len(result.Sessions))
	}
	if result.Sessions[0].Duration == "" || result.Sessions[0].Duration == "\u2014" {
		t.Errorf("Duration = %q, want a computed duration", result.Sessions[0].Duration)
	}
}

func TestSessionList_PopulatesDropdowns(t *testing.T) {
	db := testDB(t)

	db.Create(&models.DispatchSession{Source: "telegraph", UserName: "alice", Status: "active"})
	db.Create(&models.DispatchSession{Source: "local", UserName: "bob", Status: "completed"})

	result := SessionList(db, SessionFilters{})
	if len(result.Sources) < 2 {
		t.Errorf("Sources = %v, want at least 2", result.Sources)
	}
	if len(result.Statuses) < 2 {
		t.Errorf("Statuses = %v, want at least 2", result.Statuses)
	}
	if len(result.Users) < 2 {
		t.Errorf("Users = %v, want at least 2", result.Users)
	}
}

// --- GetSessionDetail tests ---

func TestGetSessionDetail_WithConversations(t *testing.T) {
	db := testDB(t)

	session := models.DispatchSession{Source: "telegraph", UserName: "alice", Status: "active", CarsCreated: `["car-1"]`}
	db.Create(&session)

	db.Create(&models.TelegraphConversation{SessionID: session.ID, Sequence: 2, Role: "assistant", Content: "Response", UserName: "bot"})
	db.Create(&models.TelegraphConversation{SessionID: session.ID, Sequence: 1, Role: "user", Content: "Hello", UserName: "alice"})

	detail, err := GetSessionDetail(db, fmt.Sprintf("%d", session.ID))
	if err != nil {
		t.Fatalf("GetSessionDetail: %v", err)
	}
	if len(detail.Conversations) != 2 {
		t.Fatalf("Conversations count = %d, want 2", len(detail.Conversations))
	}
	// Should be ordered by sequence ASC.
	if detail.Conversations[0].Sequence != 1 {
		t.Errorf("first conversation Sequence = %d, want 1", detail.Conversations[0].Sequence)
	}
	if detail.Conversations[1].Sequence != 2 {
		t.Errorf("second conversation Sequence = %d, want 2", detail.Conversations[1].Sequence)
	}
	if len(detail.CarsCreated) != 1 {
		t.Errorf("CarsCreated count = %d, want 1", len(detail.CarsCreated))
	}
}

func TestGetSessionDetail_NotFound(t *testing.T) {
	db := testDB(t)

	_, err := GetSessionDetail(db, "99999")
	if err == nil {
		t.Fatal("expected error for non-existent session")
	}
}

// --- ActiveSessionCount tests ---

func TestActiveSessionCount_WithData(t *testing.T) {
	db := testDB(t)

	db.Create(&models.DispatchSession{Source: "local", UserName: "alice", Status: "active"})
	db.Create(&models.DispatchSession{Source: "local", UserName: "bob", Status: "active"})
	db.Create(&models.DispatchSession{Source: "local", UserName: "carol", Status: "completed"})

	count := ActiveSessionCount(db)
	if count != 2 {
		t.Errorf("count = %d, want 2", count)
	}
}

// --- YardmasterStatus tests ---

func TestYardmasterStatus_Present(t *testing.T) {
	db := testDB(t)

	started := time.Now().Add(-1 * time.Hour)
	db.Create(&models.Engine{ID: "ym-1", Role: "yardmaster", Status: "active", StartedAt: started, LastActivity: time.Now()})

	info := YardmasterStatus(db)
	if info == nil {
		t.Fatal("expected non-nil result")
	}
	if info.ID != "ym-1" {
		t.Errorf("ID = %q, want %q", info.ID, "ym-1")
	}
	if info.Uptime == "" {
		t.Error("Uptime should be computed")
	}
}

func TestYardmasterStatus_Absent(t *testing.T) {
	db := testDB(t)

	// No yardmaster engine.
	db.Create(&models.Engine{ID: "eng-1", Role: "", Status: "active"})

	info := YardmasterStatus(db)
	if info != nil {
		t.Errorf("expected nil, got %+v", info)
	}
}

// --- CompletedToday tests ---

func TestCompletedToday_WithData(t *testing.T) {
	db := testDB(t)

	now := time.Now()
	midnight := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	today := midnight.Add(1 * time.Hour)      // 1am today — always after midnight
	yesterday := midnight.Add(-1 * time.Hour) // 11pm yesterday — always before midnight

	db.Create(&models.Car{ID: "car-1", Title: "T1", Track: "backend", Status: "done", Type: "task", CompletedAt: &today})
	db.Create(&models.Car{ID: "car-2", Title: "T2", Track: "backend", Status: "done", Type: "task", CompletedAt: &today})
	// Completed yesterday - should be excluded.
	db.Create(&models.Car{ID: "car-3", Title: "T3", Track: "backend", Status: "done", Type: "task", CompletedAt: &yesterday})
	// Not done - should be excluded.
	db.Create(&models.Car{ID: "car-4", Title: "T4", Track: "backend", Status: "open", Type: "task"})

	count := CompletedToday(db)
	if count != 2 {
		t.Errorf("count = %d, want 2", count)
	}
}

func TestCompletedToday_Empty(t *testing.T) {
	db := testDB(t)

	count := CompletedToday(db)
	if count != 0 {
		t.Errorf("count = %d, want 0", count)
	}
}

// --- TotalTokenUsage tests ---

func TestTotalTokenUsage_WithData(t *testing.T) {
	db := testDB(t)

	db.Create(&models.AgentLog{EngineID: "eng-1", Direction: "out", OutputTokens: 100})
	db.Create(&models.AgentLog{EngineID: "eng-1", Direction: "out", OutputTokens: 200})
	db.Create(&models.AgentLog{EngineID: "eng-2", Direction: "in", OutputTokens: 50})

	total := TotalTokenUsage(db)
	if total != 350 {
		t.Errorf("total = %d, want 350 (sum of all output_tokens)", total)
	}
}

// --- ComputeStats tests ---

func TestComputeStats_Integration(t *testing.T) {
	db := testDB(t)

	now := time.Now()
	midnight := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	today := midnight.Add(1 * time.Hour) // 1am today — always after midnight

	// Set up engines.
	engines := []EngineRow{
		{ID: "eng-1", Status: "active"},
		{ID: "eng-2", Status: "dead"},
		{ID: "eng-3", Status: "active"},
	}

	// Set up tracks.
	tracks := []TrackStatusCount{
		{Track: "backend", Open: 3, InProgress: 2, Blocked: 1},
		{Track: "frontend", Open: 1, InProgress: 0, Blocked: 0},
	}

	// Completed today.
	db.Create(&models.Car{ID: "car-done", Title: "Done", Track: "backend", Status: "done", Type: "task", CompletedAt: &today})

	// Token usage.
	db.Create(&models.AgentLog{EngineID: "eng-1", Direction: "out", OutputTokens: 500})

	stats := ComputeStats(engines, tracks, db)
	if stats.ActiveEngines != 2 {
		t.Errorf("ActiveEngines = %d, want 2", stats.ActiveEngines)
	}
	if stats.OpenCars != 4 {
		t.Errorf("OpenCars = %d, want 4", stats.OpenCars)
	}
	if stats.InProgressCars != 2 {
		t.Errorf("InProgressCars = %d, want 2", stats.InProgressCars)
	}
	if stats.BlockedCars != 1 {
		t.Errorf("BlockedCars = %d, want 1", stats.BlockedCars)
	}
	if stats.CompletedToday != 1 {
		t.Errorf("CompletedToday = %d, want 1", stats.CompletedToday)
	}
	if stats.TotalTokens != 500 {
		t.Errorf("TotalTokens = %d, want 500", stats.TotalTokens)
	}
}

// --- Combined filter tests ---

func TestListMessages_CombinedFilters(t *testing.T) {
	db := testDB(t)
	db.Create(&models.Message{FromAgent: "eng-01", ToAgent: "yardmaster", Subject: "Match", Priority: "urgent", Acknowledged: false})
	db.Create(&models.Message{FromAgent: "eng-01", ToAgent: "yardmaster", Subject: "Wrong priority", Priority: "normal", Acknowledged: false})
	db.Create(&models.Message{FromAgent: "eng-02", ToAgent: "human", Subject: "Wrong agent", Priority: "urgent", Acknowledged: false})

	result := ListMessages(db, MessageFilters{Agent: "eng-01", Priority: "urgent", Unacked: true})
	if len(result.Messages) != 1 {
		t.Fatalf("got %d, want 1", len(result.Messages))
	}
	if result.Messages[0].Subject != "Match" {
		t.Errorf("Subject = %q, want Match", result.Messages[0].Subject)
	}
}

func TestAgentLogList_CombinedFilters(t *testing.T) {
	db := testDB(t)
	db.Create(&models.AgentLog{EngineID: "eng-01", CarID: "car-a", Direction: "out", Content: "match"})
	db.Create(&models.AgentLog{EngineID: "eng-01", CarID: "car-a", Direction: "in", Content: "wrong dir"})
	db.Create(&models.AgentLog{EngineID: "eng-02", CarID: "car-a", Direction: "out", Content: "wrong engine"})

	result := AgentLogList(db, AgentLogFilters{EngineID: "eng-01", CarID: "car-a", Direction: "out"})
	if len(result.Logs) != 1 {
		t.Fatalf("got %d, want 1", len(result.Logs))
	}
}

func TestSessionList_CombinedFilters(t *testing.T) {
	db := testDB(t)
	db.Create(&models.DispatchSession{Source: "telegraph", UserName: "alice", Status: "active"})
	db.Create(&models.DispatchSession{Source: "telegraph", UserName: "bob", Status: "completed"})
	db.Create(&models.DispatchSession{Source: "local", UserName: "alice", Status: "active"})

	result := SessionList(db, SessionFilters{Source: "telegraph", Status: "active", UserName: "alice"})
	if len(result.Sessions) != 1 {
		t.Fatalf("got %d, want 1", len(result.Sessions))
	}
}

// --- Coverage gap tests ---

func TestDependencyGraph_DeepChain(t *testing.T) {
	db := testDB(t)
	// Create a chain: A -> B -> C -> D (depth 3, hits addSubTree recursion)
	db.Create(&models.Car{ID: "car-d1", Title: "D1", Status: "open", Type: "task", Track: "backend"})
	db.Create(&models.Car{ID: "car-d2", Title: "D2", Status: "open", Type: "task", Track: "backend"})
	db.Create(&models.Car{ID: "car-d3", Title: "D3", Status: "open", Type: "task", Track: "backend"})
	db.Create(&models.Car{ID: "car-d4", Title: "D4", Status: "open", Type: "task", Track: "backend"})
	db.Create(&models.CarDep{CarID: "car-d2", BlockedBy: "car-d1"})
	db.Create(&models.CarDep{CarID: "car-d3", BlockedBy: "car-d2"})
	db.Create(&models.CarDep{CarID: "car-d4", BlockedBy: "car-d3"})

	result := DependencyGraph(db, "car-d2")
	// Should have 4 nodes (d1 upstream, d2 root, d3+d4 downstream)
	if len(result.Nodes) < 3 {
		t.Errorf("Nodes = %d, want at least 3", len(result.Nodes))
	}
	// Tree should have entries from addSubTree
	if len(result.Tree) < 3 {
		t.Errorf("Tree = %d, want at least 3 entries", len(result.Tree))
	}
}

func TestDependencyGraph_BidirectionalDeps(t *testing.T) {
	db := testDB(t)
	// Car B is blocked by A and blocks C (tests both up and down in addSubTree)
	db.Create(&models.Car{ID: "car-up1", Title: "Upstream1", Status: "done", Type: "task", Track: "backend"})
	db.Create(&models.Car{ID: "car-up2", Title: "Upstream2", Status: "done", Type: "task", Track: "backend"})
	db.Create(&models.Car{ID: "car-mid", Title: "Middle", Status: "open", Type: "task", Track: "backend"})
	db.Create(&models.Car{ID: "car-dn1", Title: "Down1", Status: "open", Type: "task", Track: "backend"})
	db.Create(&models.Car{ID: "car-dn2", Title: "Down2", Status: "open", Type: "task", Track: "backend"})
	// up1 and up2 block mid; mid blocks dn1 and dn2
	db.Create(&models.CarDep{CarID: "car-mid", BlockedBy: "car-up1"})
	db.Create(&models.CarDep{CarID: "car-mid", BlockedBy: "car-up2"})
	db.Create(&models.CarDep{CarID: "car-dn1", BlockedBy: "car-mid"})
	db.Create(&models.CarDep{CarID: "car-dn2", BlockedBy: "car-mid"})

	result := DependencyGraph(db, "car-mid")
	if len(result.Nodes) != 5 {
		t.Errorf("Nodes = %d, want 5", len(result.Nodes))
	}
	if len(result.Tree) < 5 {
		t.Errorf("Tree = %d, want at least 5", len(result.Tree))
	}
}

func TestTrackSummary_AllStatuses(t *testing.T) {
	db := testDB(t)
	// Test every status branch in the switch statement
	db.Create(&models.Car{ID: "c-draft", Title: "Draft", Track: "t", Status: "draft", Type: "task"})
	db.Create(&models.Car{ID: "c-open", Title: "Open", Track: "t", Status: "open", Type: "task"})
	db.Create(&models.Car{ID: "c-ready", Title: "Ready", Track: "t", Status: "ready", Type: "task"})
	db.Create(&models.Car{ID: "c-claimed", Title: "Claimed", Track: "t", Status: "claimed", Type: "task"})
	db.Create(&models.Car{ID: "c-ip", Title: "InProgress", Track: "t", Status: "in_progress", Type: "task"})
	db.Create(&models.Car{ID: "c-done", Title: "Done", Track: "t", Status: "done", Type: "task"})
	db.Create(&models.Car{ID: "c-merged", Title: "Merged", Track: "t", Status: "merged", Type: "task"})
	db.Create(&models.Car{ID: "c-blocked", Title: "Blocked", Track: "t", Status: "blocked", Type: "task"})

	tracks, err := TrackSummary(db)
	if err != nil {
		t.Fatalf("TrackSummary: %v", err)
	}
	if len(tracks) != 1 {
		t.Fatalf("got %d tracks, want 1", len(tracks))
	}
	tc := tracks[0]
	if tc.Draft != 1 {
		t.Errorf("Draft = %d, want 1", tc.Draft)
	}
	if tc.Open != 2 {
		t.Errorf("Open = %d, want 2 (open + ready)", tc.Open)
	}
	if tc.Claimed != 1 {
		t.Errorf("Claimed = %d, want 1", tc.Claimed)
	}
	if tc.InProgress != 1 {
		t.Errorf("InProgress = %d, want 1", tc.InProgress)
	}
	if tc.Done != 1 {
		t.Errorf("Done = %d, want 1", tc.Done)
	}
	if tc.Merged != 1 {
		t.Errorf("Merged = %d, want 1", tc.Merged)
	}
	if tc.Blocked != 1 {
		t.Errorf("Blocked = %d, want 1", tc.Blocked)
	}
	if tc.Total != 8 {
		t.Errorf("Total = %d, want 8", tc.Total)
	}
}

// --- CycleUsageSummary tests ---

func TestCycleUsageSummary_ExcludesZeroCycle(t *testing.T) {
	db := testDB(t)

	now := time.Now()
	// 2 real cycles + 2 zero-cycle rows for car-1.
	db.Create(&models.CarProgress{CarID: "car-1", Cycle: 0, EngineID: "eng-1", Note: "progress", CreatedAt: now})
	db.Create(&models.CarProgress{CarID: "car-1", Cycle: 1, EngineID: "eng-1", Note: "cycle 1", CreatedAt: now.Add(time.Minute)})
	db.Create(&models.CarProgress{CarID: "car-1", Cycle: 2, EngineID: "eng-1", Note: "cycle 2", CreatedAt: now.Add(2 * time.Minute)})
	db.Create(&models.CarProgress{CarID: "car-1", Cycle: 0, EngineID: "eng-1", Note: "completion", CreatedAt: now.Add(3 * time.Minute)})

	result := CycleUsageSummary(db)
	if result.TotalCycles != 2 {
		t.Errorf("TotalCycles = %d, want 2 (Cycle=0 excluded)", result.TotalCycles)
	}
	if result.AvgPerCar != 2.0 {
		t.Errorf("AvgPerCar = %f, want 2.0", result.AvgPerCar)
	}
	if result.StalledCars != 0 {
		t.Errorf("StalledCars = %d, want 0", result.StalledCars)
	}
}

func TestCycleUsageSummary_StalledExcludesZeroCycle(t *testing.T) {
	db := testDB(t)

	now := time.Now()
	// 5 real cycles + 3 zero-cycle rows = 8 total, but only 5 real => not stalled.
	for i := 0; i < 3; i++ {
		db.Create(&models.CarProgress{CarID: "car-s", Cycle: 0, EngineID: "eng-1", CreatedAt: now.Add(time.Duration(i) * time.Minute)})
	}
	for i := 1; i <= 5; i++ {
		db.Create(&models.CarProgress{CarID: "car-s", Cycle: i, EngineID: "eng-1", CreatedAt: now.Add(time.Duration(i+3) * time.Minute)})
	}

	result := CycleUsageSummary(db)
	if result.TotalCycles != 5 {
		t.Errorf("TotalCycles = %d, want 5", result.TotalCycles)
	}
	if result.StalledCars != 0 {
		t.Errorf("StalledCars = %d, want 0 (5 real cycles <= threshold)", result.StalledCars)
	}
}

func TestCarList_CycleCountExcludesZeroCycle(t *testing.T) {
	db := testDB(t)

	db.Create(&models.Car{ID: "car-c", Title: "Test", Status: "open", Type: "task", Track: "backend", Priority: 2})
	now := time.Now()
	db.Create(&models.CarProgress{CarID: "car-c", Cycle: 0, EngineID: "eng-1", CreatedAt: now})
	db.Create(&models.CarProgress{CarID: "car-c", Cycle: 1, EngineID: "eng-1", CreatedAt: now.Add(time.Minute)})
	db.Create(&models.CarProgress{CarID: "car-c", Cycle: 2, EngineID: "eng-1", CreatedAt: now.Add(2 * time.Minute)})

	result := CarList(db, "", "", "", "")
	if len(result.Cars) != 1 {
		t.Fatalf("Cars count = %d, want 1", len(result.Cars))
	}
	if result.Cars[0].TotalCycles != 2 {
		t.Errorf("TotalCycles = %d, want 2 (Cycle=0 excluded)", result.Cars[0].TotalCycles)
	}
}

func TestGetCarDetail_CycleMetricsExcludeZeroCycle(t *testing.T) {
	db := testDB(t)

	now := time.Now()
	db.Create(&models.Car{ID: "car-d", Title: "Detail Test", Status: "open", Type: "task", Track: "backend", Priority: 2, CreatedAt: now, UpdatedAt: now})
	db.Create(&models.CarProgress{CarID: "car-d", Cycle: 0, EngineID: "eng-1", Note: "generic progress", FilesChanged: `["junk.go"]`, CreatedAt: now})
	db.Create(&models.CarProgress{CarID: "car-d", Cycle: 1, EngineID: "eng-1", Note: "first cycle", FilesChanged: `["a.go"]`, CreatedAt: now.Add(time.Minute)})
	db.Create(&models.CarProgress{CarID: "car-d", Cycle: 2, EngineID: "eng-1", Note: "second cycle", FilesChanged: `["b.go","c.go"]`, CreatedAt: now.Add(3 * time.Minute)})
	db.Create(&models.CarProgress{CarID: "car-d", Cycle: 0, EngineID: "eng-1", Note: "completion", FilesChanged: `[]`, CreatedAt: now.Add(4 * time.Minute)})

	detail, err := GetCarDetail(db, "car-d")
	if err != nil {
		t.Fatalf("GetCarDetail: %v", err)
	}
	if detail.TotalCycles != 2 {
		t.Errorf("TotalCycles = %d, want 2 (Cycle=0 excluded)", detail.TotalCycles)
	}
	if detail.TotalFilesChanged != 3 {
		t.Errorf("TotalFilesChanged = %d, want 3 (Cycle=0 files excluded)", detail.TotalFilesChanged)
	}
	if detail.CycleStalled {
		t.Error("expected not stalled with 2 real cycles")
	}
	if len(detail.CycleDetails) != 2 {
		t.Errorf("CycleDetails count = %d, want 2", len(detail.CycleDetails))
	}
	// Duration should be ~120s between cycle 1 and 2.
	if len(detail.CycleDetails) == 2 {
		dur := detail.CycleDetails[1].DurationSec
		if dur < 119 || dur > 121 {
			t.Errorf("CycleDetails[1].DurationSec = %f, want ~120", dur)
		}
	}
}
