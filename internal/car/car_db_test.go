package car

import (
	"strings"
	"testing"

	"github.com/zulandar/railyard/internal/models"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// testDB creates an in-memory SQLite database with the tables needed by the car package.
func testDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	if err := db.AutoMigrate(
		&models.Car{},
		&models.CarDep{},
		&models.CarProgress{},
	); err != nil {
		t.Fatalf("migrate test db: %v", err)
	}
	return db
}

// createCar is a test helper that creates a car with sensible defaults.
func createCar(t *testing.T, db *gorm.DB, opts CreateOpts) *models.Car {
	t.Helper()
	if opts.BranchPrefix == "" {
		opts.BranchPrefix = "ry/test"
	}
	car, err := Create(db, opts)
	if err != nil {
		t.Fatalf("createCar(%q): %v", opts.Title, err)
	}
	return car
}

// --- Create tests ---

func TestCreate_ValidTask(t *testing.T) {
	db := testDB(t)

	car, err := Create(db, CreateOpts{
		Title:        "Implement feature X",
		Description:  "Details here",
		Track:        "backend",
		Priority:     1,
		BranchPrefix: "ry/alice",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if !strings.HasPrefix(car.ID, "car-") {
		t.Errorf("ID = %q, want car- prefix", car.ID)
	}
	if car.Title != "Implement feature X" {
		t.Errorf("Title = %q, want %q", car.Title, "Implement feature X")
	}
	if car.Status != "draft" {
		t.Errorf("Status = %q, want %q", car.Status, "draft")
	}
	if car.Type != "task" {
		t.Errorf("Type = %q, want %q (default)", car.Type, "task")
	}
	if car.Priority != 1 {
		t.Errorf("Priority = %d, want 1", car.Priority)
	}
	if car.Track != "backend" {
		t.Errorf("Track = %q, want %q", car.Track, "backend")
	}
	if !strings.HasPrefix(car.Branch, "ry/alice/backend/car-") {
		t.Errorf("Branch = %q, want prefix %q", car.Branch, "ry/alice/backend/car-")
	}
}

func TestCreate_DefaultType(t *testing.T) {
	db := testDB(t)

	car := createCar(t, db, CreateOpts{Title: "No type", Track: "backend"})
	if car.Type != "task" {
		t.Errorf("Type = %q, want %q", car.Type, "task")
	}
}

func TestCreate_ExplicitType(t *testing.T) {
	db := testDB(t)

	for _, typ := range []string{"epic", "bug", "spike"} {
		car := createCar(t, db, CreateOpts{Title: "Type " + typ, Track: "backend", Type: typ})
		if car.Type != typ {
			t.Errorf("Type = %q, want %q", car.Type, typ)
		}
	}
}

func TestCreate_MissingTitle(t *testing.T) {
	db := testDB(t)

	_, err := Create(db, CreateOpts{Track: "backend", BranchPrefix: "ry/alice"})
	if err == nil {
		t.Fatal("expected error for missing title")
	}
	if !strings.Contains(err.Error(), "title is required") {
		t.Errorf("error = %q, want to contain %q", err.Error(), "title is required")
	}
}

func TestCreate_MissingTrack(t *testing.T) {
	db := testDB(t)

	_, err := Create(db, CreateOpts{Title: "No track", BranchPrefix: "ry/alice"})
	if err == nil {
		t.Fatal("expected error for missing track")
	}
	if !strings.Contains(err.Error(), "track is required") {
		t.Errorf("error = %q, want to contain %q", err.Error(), "track is required")
	}
}

func TestCreate_WithParent(t *testing.T) {
	db := testDB(t)

	epic := createCar(t, db, CreateOpts{Title: "Parent epic", Track: "backend", Type: "epic"})
	child := createCar(t, db, CreateOpts{Title: "Child task", Track: "backend", ParentID: epic.ID})

	if child.ParentID == nil || *child.ParentID != epic.ID {
		t.Errorf("ParentID = %v, want %q", child.ParentID, epic.ID)
	}
}

func TestCreate_ParentTrackInheritance(t *testing.T) {
	db := testDB(t)

	epic := createCar(t, db, CreateOpts{Title: "Epic", Track: "backend", Type: "epic"})

	// Create child without track — should inherit from parent.
	child := createCar(t, db, CreateOpts{Title: "Child", ParentID: epic.ID})
	if child.Track != "backend" {
		t.Errorf("Track = %q, want %q (inherited from parent)", child.Track, "backend")
	}
	if !strings.Contains(child.Branch, "backend") {
		t.Errorf("Branch = %q, want to contain %q", child.Branch, "backend")
	}
}

func TestCreate_InvalidParent(t *testing.T) {
	db := testDB(t)

	_, err := Create(db, CreateOpts{
		Title:        "Orphan",
		Track:        "backend",
		ParentID:     "car-zzzzz",
		BranchPrefix: "ry/alice",
	})
	if err == nil {
		t.Fatal("expected error for non-existent parent")
	}
	if !strings.Contains(err.Error(), "parent not found") {
		t.Errorf("error = %q, want to contain %q", err.Error(), "parent not found")
	}
}

func TestCreate_NonEpicParent(t *testing.T) {
	db := testDB(t)

	task := createCar(t, db, CreateOpts{Title: "A task", Track: "backend", Type: "task"})

	_, err := Create(db, CreateOpts{
		Title:        "Child of task",
		Track:        "backend",
		ParentID:     task.ID,
		BranchPrefix: "ry/alice",
	})
	if err == nil {
		t.Fatal("expected error for non-epic parent")
	}
	if !strings.Contains(err.Error(), "only epics can have children") {
		t.Errorf("error = %q, want to contain %q", err.Error(), "only epics can have children")
	}
}

func TestCreate_BaseBranchAndDesignFields(t *testing.T) {
	db := testDB(t)

	car := createCar(t, db, CreateOpts{
		Title:       "Full fields",
		Track:       "backend",
		BaseBranch:  "develop",
		DesignNotes: "design notes here",
		Acceptance:  "acceptance criteria",
		SkipTests:   true,
	})
	if car.BaseBranch != "develop" {
		t.Errorf("BaseBranch = %q, want %q", car.BaseBranch, "develop")
	}
	if car.DesignNotes != "design notes here" {
		t.Errorf("DesignNotes = %q, want %q", car.DesignNotes, "design notes here")
	}
	if car.Acceptance != "acceptance criteria" {
		t.Errorf("Acceptance = %q, want %q", car.Acceptance, "acceptance criteria")
	}
	if !car.SkipTests {
		t.Error("SkipTests = false, want true")
	}
}

// --- Get tests ---

func TestGet_Found(t *testing.T) {
	db := testDB(t)

	created := createCar(t, db, CreateOpts{Title: "Get test", Track: "backend"})

	got, err := Get(db, created.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.ID != created.ID {
		t.Errorf("ID = %q, want %q", got.ID, created.ID)
	}
	if got.Title != "Get test" {
		t.Errorf("Title = %q, want %q", got.Title, "Get test")
	}
}

func TestGet_PreloadsDepsAndProgress(t *testing.T) {
	db := testDB(t)

	car := createCar(t, db, CreateOpts{Title: "Preload test", Track: "backend"})

	got, err := Get(db, car.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	// Deps and Progress should be non-nil (empty slices from preload).
	if got.Deps == nil {
		t.Error("Deps should be preloaded (empty slice, not nil)")
	}
	if got.Progress == nil {
		t.Error("Progress should be preloaded (empty slice, not nil)")
	}
}

func TestGet_NotFound(t *testing.T) {
	db := testDB(t)

	_, err := Get(db, "car-zzzzz")
	if err == nil {
		t.Fatal("expected error for non-existent ID")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("error = %q, want to contain %q", err.Error(), "not found")
	}
}

// --- List tests ---

func TestList_All(t *testing.T) {
	db := testDB(t)

	createCar(t, db, CreateOpts{Title: "Car 1", Track: "backend"})
	createCar(t, db, CreateOpts{Title: "Car 2", Track: "frontend"})
	createCar(t, db, CreateOpts{Title: "Car 3", Track: "backend"})

	all, err := List(db, ListFilters{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(all) != 3 {
		t.Errorf("List all: got %d, want 3", len(all))
	}
}

func TestList_Empty(t *testing.T) {
	db := testDB(t)

	cars, err := List(db, ListFilters{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(cars) != 0 {
		t.Errorf("List empty: got %d, want 0", len(cars))
	}
}

func TestList_FilterByTrack(t *testing.T) {
	db := testDB(t)

	createCar(t, db, CreateOpts{Title: "BE 1", Track: "backend"})
	createCar(t, db, CreateOpts{Title: "BE 2", Track: "backend"})
	createCar(t, db, CreateOpts{Title: "FE 1", Track: "frontend"})

	be, err := List(db, ListFilters{Track: "backend"})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(be) != 2 {
		t.Errorf("List track=backend: got %d, want 2", len(be))
	}
}

func TestList_FilterByStatus(t *testing.T) {
	db := testDB(t)

	c1 := createCar(t, db, CreateOpts{Title: "Draft car", Track: "backend"})
	c2 := createCar(t, db, CreateOpts{Title: "Open car", Track: "backend"})
	_ = c1

	// Publish c2 to open.
	if err := db.Model(&models.Car{}).Where("id = ?", c2.ID).Update("status", "open").Error; err != nil {
		t.Fatalf("update status: %v", err)
	}

	drafts, err := List(db, ListFilters{Status: "draft"})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(drafts) != 1 {
		t.Errorf("List status=draft: got %d, want 1", len(drafts))
	}

	open, err := List(db, ListFilters{Status: "open"})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(open) != 1 {
		t.Errorf("List status=open: got %d, want 1", len(open))
	}
}

func TestList_FilterByType(t *testing.T) {
	db := testDB(t)

	createCar(t, db, CreateOpts{Title: "Task", Track: "backend", Type: "task"})
	createCar(t, db, CreateOpts{Title: "Epic", Track: "backend", Type: "epic"})
	createCar(t, db, CreateOpts{Title: "Bug", Track: "backend", Type: "bug"})

	tasks, err := List(db, ListFilters{Type: "task"})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(tasks) != 1 {
		t.Errorf("List type=task: got %d, want 1", len(tasks))
	}
}

func TestList_FilterByAssignee(t *testing.T) {
	db := testDB(t)

	c := createCar(t, db, CreateOpts{Title: "Assigned", Track: "backend"})
	// Set assignee directly.
	db.Model(&models.Car{}).Where("id = ?", c.ID).Update("assignee", "engine-01")
	createCar(t, db, CreateOpts{Title: "Unassigned", Track: "backend"})

	result, err := List(db, ListFilters{Assignee: "engine-01"})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(result) != 1 {
		t.Errorf("List assignee=engine-01: got %d, want 1", len(result))
	}
}

func TestList_FilterByParentID(t *testing.T) {
	db := testDB(t)

	epic := createCar(t, db, CreateOpts{Title: "Epic", Track: "backend", Type: "epic"})
	createCar(t, db, CreateOpts{Title: "Child 1", Track: "backend", ParentID: epic.ID})
	createCar(t, db, CreateOpts{Title: "Child 2", Track: "backend", ParentID: epic.ID})
	createCar(t, db, CreateOpts{Title: "Orphan", Track: "backend"})

	children, err := List(db, ListFilters{ParentID: epic.ID})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(children) != 2 {
		t.Errorf("List parentID: got %d, want 2", len(children))
	}
}

func TestList_MultipleFilters(t *testing.T) {
	db := testDB(t)

	createCar(t, db, CreateOpts{Title: "BE task", Track: "backend", Type: "task"})
	createCar(t, db, CreateOpts{Title: "BE epic", Track: "backend", Type: "epic"})
	createCar(t, db, CreateOpts{Title: "FE task", Track: "frontend", Type: "task"})

	result, err := List(db, ListFilters{Track: "backend", Type: "task"})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(result) != 1 {
		t.Errorf("List track=backend,type=task: got %d, want 1", len(result))
	}
}

func TestList_OrderByPriorityThenCreated(t *testing.T) {
	db := testDB(t)

	createCar(t, db, CreateOpts{Title: "Low prio", Track: "backend", Priority: 4})
	createCar(t, db, CreateOpts{Title: "High prio", Track: "backend", Priority: 0})
	createCar(t, db, CreateOpts{Title: "Med prio", Track: "backend", Priority: 2})

	cars, err := List(db, ListFilters{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(cars) != 3 {
		t.Fatalf("List: got %d, want 3", len(cars))
	}
	if cars[0].Title != "High prio" {
		t.Errorf("first car = %q, want %q", cars[0].Title, "High prio")
	}
	if cars[1].Title != "Med prio" {
		t.Errorf("second car = %q, want %q", cars[1].Title, "Med prio")
	}
	if cars[2].Title != "Low prio" {
		t.Errorf("third car = %q, want %q", cars[2].Title, "Low prio")
	}
}

// --- Update tests ---

func TestUpdate_StatusTransition(t *testing.T) {
	db := testDB(t)

	car := createCar(t, db, CreateOpts{Title: "Update test", Track: "backend"})
	// draft → open
	if err := db.Model(&models.Car{}).Where("id = ?", car.ID).Update("status", "open").Error; err != nil {
		t.Fatalf("set open: %v", err)
	}

	// open → ready
	if err := Update(db, car.ID, map[string]interface{}{"status": "ready"}); err != nil {
		t.Fatalf("open→ready: %v", err)
	}

	got, err := Get(db, car.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Status != "ready" {
		t.Errorf("Status = %q, want %q", got.Status, "ready")
	}
}

func TestUpdate_ClaimedSetsClaimedAt(t *testing.T) {
	db := testDB(t)

	car := createCar(t, db, CreateOpts{Title: "Claimed test", Track: "backend"})
	// Move to open → ready → claimed.
	db.Model(&models.Car{}).Where("id = ?", car.ID).Update("status", "open")
	if err := Update(db, car.ID, map[string]interface{}{"status": "ready"}); err != nil {
		t.Fatalf("open→ready: %v", err)
	}
	if err := Update(db, car.ID, map[string]interface{}{"status": "claimed", "assignee": "engine-01"}); err != nil {
		t.Fatalf("ready→claimed: %v", err)
	}

	got, err := Get(db, car.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.ClaimedAt == nil {
		t.Error("ClaimedAt should be set when transitioning to claimed")
	}
	if got.Assignee != "engine-01" {
		t.Errorf("Assignee = %q, want %q", got.Assignee, "engine-01")
	}
}

func TestUpdate_DoneSetsCompletedAt(t *testing.T) {
	db := testDB(t)

	car := createCar(t, db, CreateOpts{Title: "Done test", Track: "backend"})
	// Move through: open → ready → claimed → in_progress → done.
	db.Model(&models.Car{}).Where("id = ?", car.ID).Update("status", "open")
	Update(db, car.ID, map[string]interface{}{"status": "ready"})
	Update(db, car.ID, map[string]interface{}{"status": "claimed", "assignee": "e1"})
	Update(db, car.ID, map[string]interface{}{"status": "in_progress"})
	if err := Update(db, car.ID, map[string]interface{}{"status": "done"}); err != nil {
		t.Fatalf("in_progress→done: %v", err)
	}

	got, err := Get(db, car.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.CompletedAt == nil {
		t.Error("CompletedAt should be set when transitioning to done")
	}
}

func TestUpdate_InvalidTransition(t *testing.T) {
	db := testDB(t)

	car := createCar(t, db, CreateOpts{Title: "Invalid", Track: "backend"})
	db.Model(&models.Car{}).Where("id = ?", car.ID).Update("status", "open")

	// open → done is not valid.
	err := Update(db, car.ID, map[string]interface{}{"status": "done"})
	if err == nil {
		t.Fatal("expected error for invalid transition open→done")
	}
	if !strings.Contains(err.Error(), "invalid status transition") {
		t.Errorf("error = %q, want to contain %q", err.Error(), "invalid status transition")
	}
}

func TestUpdate_AnyToBlocked(t *testing.T) {
	db := testDB(t)

	car := createCar(t, db, CreateOpts{Title: "Block test", Track: "backend"})
	db.Model(&models.Car{}).Where("id = ?", car.ID).Update("status", "open")

	if err := Update(db, car.ID, map[string]interface{}{"status": "blocked"}); err != nil {
		t.Fatalf("open→blocked: %v", err)
	}

	// blocked → open (unblock).
	if err := Update(db, car.ID, map[string]interface{}{"status": "open"}); err != nil {
		t.Fatalf("blocked→open: %v", err)
	}
}

func TestUpdate_NotFound(t *testing.T) {
	db := testDB(t)

	err := Update(db, "car-zzzzz", map[string]interface{}{"status": "ready"})
	if err == nil {
		t.Fatal("expected error for non-existent ID")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("error = %q, want to contain %q", err.Error(), "not found")
	}
}

func TestUpdate_NonStatusFields(t *testing.T) {
	db := testDB(t)

	car := createCar(t, db, CreateOpts{Title: "Field update", Track: "backend"})

	if err := Update(db, car.ID, map[string]interface{}{
		"description": "Updated desc",
		"priority":    1,
	}); err != nil {
		t.Fatalf("Update: %v", err)
	}

	got, err := Get(db, car.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Description != "Updated desc" {
		t.Errorf("Description = %q, want %q", got.Description, "Updated desc")
	}
	if got.Priority != 1 {
		t.Errorf("Priority = %d, want 1", got.Priority)
	}
}

// --- GetChildren tests ---

func TestGetChildren_Valid(t *testing.T) {
	db := testDB(t)

	epic := createCar(t, db, CreateOpts{Title: "Epic", Track: "backend", Type: "epic"})
	createCar(t, db, CreateOpts{Title: "Child 1", Track: "backend", ParentID: epic.ID})
	createCar(t, db, CreateOpts{Title: "Child 2", Track: "backend", ParentID: epic.ID})
	createCar(t, db, CreateOpts{Title: "Child 3", Track: "backend", ParentID: epic.ID})

	children, err := GetChildren(db, epic.ID)
	if err != nil {
		t.Fatalf("GetChildren: %v", err)
	}
	if len(children) != 3 {
		t.Errorf("GetChildren: got %d, want 3", len(children))
	}
}

func TestGetChildren_NoChildren(t *testing.T) {
	db := testDB(t)

	epic := createCar(t, db, CreateOpts{Title: "Empty epic", Track: "backend", Type: "epic"})

	children, err := GetChildren(db, epic.ID)
	if err != nil {
		t.Fatalf("GetChildren: %v", err)
	}
	if len(children) != 0 {
		t.Errorf("GetChildren: got %d, want 0", len(children))
	}
}

func TestGetChildren_NonexistentParent(t *testing.T) {
	db := testDB(t)

	_, err := GetChildren(db, "car-zzzzz")
	if err == nil {
		t.Fatal("expected error for non-existent parent")
	}
	if !strings.Contains(err.Error(), "parent not found") {
		t.Errorf("error = %q, want to contain %q", err.Error(), "parent not found")
	}
}

func TestGetChildren_OrderByPriority(t *testing.T) {
	db := testDB(t)

	epic := createCar(t, db, CreateOpts{Title: "Epic", Track: "backend", Type: "epic"})
	createCar(t, db, CreateOpts{Title: "Low", Track: "backend", ParentID: epic.ID, Priority: 4})
	createCar(t, db, CreateOpts{Title: "High", Track: "backend", ParentID: epic.ID, Priority: 0})

	children, err := GetChildren(db, epic.ID)
	if err != nil {
		t.Fatalf("GetChildren: %v", err)
	}
	if len(children) != 2 {
		t.Fatalf("GetChildren: got %d, want 2", len(children))
	}
	if children[0].Title != "High" {
		t.Errorf("first child = %q, want %q", children[0].Title, "High")
	}
}

// --- ChildrenSummary tests ---

func TestChildrenSummary_StatusCounts(t *testing.T) {
	db := testDB(t)

	epic := createCar(t, db, CreateOpts{Title: "Summary epic", Track: "backend", Type: "epic"})

	// Create children in various statuses.
	createCar(t, db, CreateOpts{Title: "Draft 1", Track: "backend", ParentID: epic.ID})
	createCar(t, db, CreateOpts{Title: "Draft 2", Track: "backend", ParentID: epic.ID})

	c3 := createCar(t, db, CreateOpts{Title: "Open child", Track: "backend", ParentID: epic.ID})
	db.Model(&models.Car{}).Where("id = ?", c3.ID).Update("status", "open")

	summary, err := ChildrenSummary(db, epic.ID)
	if err != nil {
		t.Fatalf("ChildrenSummary: %v", err)
	}

	counts := make(map[string]int)
	total := 0
	for _, sc := range summary {
		counts[sc.Status] = sc.Count
		total += sc.Count
	}
	if total != 3 {
		t.Errorf("total = %d, want 3", total)
	}
	if counts["draft"] != 2 {
		t.Errorf("draft count = %d, want 2", counts["draft"])
	}
	if counts["open"] != 1 {
		t.Errorf("open count = %d, want 1", counts["open"])
	}
}

func TestChildrenSummary_NoChildren(t *testing.T) {
	db := testDB(t)

	epic := createCar(t, db, CreateOpts{Title: "Empty", Track: "backend", Type: "epic"})

	summary, err := ChildrenSummary(db, epic.ID)
	if err != nil {
		t.Fatalf("ChildrenSummary: %v", err)
	}
	if len(summary) != 0 {
		t.Errorf("ChildrenSummary: got %d entries, want 0", len(summary))
	}
}

// --- Publish tests ---

func TestPublish_DraftToOpen(t *testing.T) {
	db := testDB(t)

	car := createCar(t, db, CreateOpts{Title: "Publish me", Track: "backend"})

	count, err := Publish(db, car.ID, false)
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if count != 1 {
		t.Errorf("count = %d, want 1", count)
	}

	got, err := Get(db, car.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Status != "open" {
		t.Errorf("Status = %q, want %q", got.Status, "open")
	}
}

func TestPublish_AlreadyOpen(t *testing.T) {
	db := testDB(t)

	car := createCar(t, db, CreateOpts{Title: "Already open", Track: "backend"})
	db.Model(&models.Car{}).Where("id = ?", car.ID).Update("status", "open")

	count, err := Publish(db, car.ID, false)
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if count != 0 {
		t.Errorf("count = %d, want 0 (already open)", count)
	}
}

func TestPublish_NotFound(t *testing.T) {
	db := testDB(t)

	_, err := Publish(db, "car-zzzzz", false)
	if err == nil {
		t.Fatal("expected error for non-existent ID")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("error = %q, want to contain %q", err.Error(), "not found")
	}
}

func TestPublish_RecursiveEpic(t *testing.T) {
	db := testDB(t)

	epic := createCar(t, db, CreateOpts{Title: "Epic", Track: "backend", Type: "epic"})
	createCar(t, db, CreateOpts{Title: "Child 1", Track: "backend", ParentID: epic.ID})
	createCar(t, db, CreateOpts{Title: "Child 2", Track: "backend", ParentID: epic.ID})

	// One child already open — should not count.
	c3 := createCar(t, db, CreateOpts{Title: "Open child", Track: "backend", ParentID: epic.ID})
	db.Model(&models.Car{}).Where("id = ?", c3.ID).Update("status", "open")

	count, err := Publish(db, epic.ID, true)
	if err != nil {
		t.Fatalf("Publish recursive: %v", err)
	}
	// epic + 2 draft children = 3 published.
	if count != 3 {
		t.Errorf("count = %d, want 3", count)
	}

	// Verify all are now open.
	children, err := GetChildren(db, epic.ID)
	if err != nil {
		t.Fatalf("GetChildren: %v", err)
	}
	for _, child := range children {
		if child.Status != "open" {
			t.Errorf("child %s status = %q, want %q", child.ID, child.Status, "open")
		}
	}
}

func TestPublish_NonRecursiveDoesNotPublishChildren(t *testing.T) {
	db := testDB(t)

	epic := createCar(t, db, CreateOpts{Title: "Epic", Track: "backend", Type: "epic"})
	child := createCar(t, db, CreateOpts{Title: "Child", Track: "backend", ParentID: epic.ID})

	count, err := Publish(db, epic.ID, false)
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if count != 1 {
		t.Errorf("count = %d, want 1 (only epic)", count)
	}

	got, err := Get(db, child.ID)
	if err != nil {
		t.Fatalf("Get child: %v", err)
	}
	if got.Status != "draft" {
		t.Errorf("child status = %q, want %q (not published)", got.Status, "draft")
	}
}

// --- DB error tests (closed connection) ---

// closedDB returns a GORM DB with the underlying sql.DB closed, forcing errors.
func closedDB(t *testing.T) *gorm.DB {
	t.Helper()
	db := testDB(t)
	sqlDB, _ := db.DB()
	sqlDB.Close()
	return db
}

func TestCreate_DBError(t *testing.T) {
	db := closedDB(t)
	_, err := Create(db, CreateOpts{
		Title:        "DB error",
		Track:        "backend",
		BranchPrefix: "ry/test",
	})
	if err == nil {
		t.Fatal("expected error from Create with closed DB")
	}
}

func TestCreate_ParentDBError(t *testing.T) {
	db := closedDB(t)
	_, err := Create(db, CreateOpts{
		Title:        "DB error",
		Track:        "backend",
		ParentID:     "car-12345",
		BranchPrefix: "ry/test",
	})
	if err == nil {
		t.Fatal("expected error from Create with closed DB (parent check)")
	}
}

func TestGet_DBError(t *testing.T) {
	db := closedDB(t)
	_, err := Get(db, "car-12345")
	if err == nil {
		t.Fatal("expected error from Get with closed DB")
	}
	if !strings.Contains(err.Error(), "car: get") {
		t.Errorf("error = %q, want to contain %q", err.Error(), "car: get")
	}
}

func TestList_DBError(t *testing.T) {
	db := closedDB(t)
	_, err := List(db, ListFilters{})
	if err == nil {
		t.Fatal("expected error from List with closed DB")
	}
	if !strings.Contains(err.Error(), "car: list") {
		t.Errorf("error = %q, want to contain %q", err.Error(), "car: list")
	}
}

func TestUpdate_DBError(t *testing.T) {
	db := closedDB(t)
	err := Update(db, "car-12345", map[string]interface{}{"status": "ready"})
	if err == nil {
		t.Fatal("expected error from Update with closed DB")
	}
}

func TestGetChildren_DBError(t *testing.T) {
	db := closedDB(t)
	_, err := GetChildren(db, "car-12345")
	if err == nil {
		t.Fatal("expected error from GetChildren with closed DB")
	}
}

func TestChildrenSummary_DBError(t *testing.T) {
	db := closedDB(t)
	_, err := ChildrenSummary(db, "car-12345")
	if err == nil {
		t.Fatal("expected error from ChildrenSummary with closed DB")
	}
}

func TestPublish_DBError(t *testing.T) {
	db := closedDB(t)
	_, err := Publish(db, "car-12345", false)
	if err == nil {
		t.Fatal("expected error from Publish with closed DB")
	}
}

// --- Full lifecycle test ---

func TestFullLifecycle(t *testing.T) {
	db := testDB(t)

	// Create.
	car := createCar(t, db, CreateOpts{Title: "Lifecycle", Track: "backend", Priority: 1})
	if car.Status != "draft" {
		t.Fatalf("initial Status = %q, want draft", car.Status)
	}

	// Publish (draft → open).
	if _, err := Publish(db, car.ID, false); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	// open → ready.
	if err := Update(db, car.ID, map[string]interface{}{"status": "ready"}); err != nil {
		t.Fatalf("open→ready: %v", err)
	}

	// ready → claimed.
	if err := Update(db, car.ID, map[string]interface{}{"status": "claimed", "assignee": "engine-01"}); err != nil {
		t.Fatalf("ready→claimed: %v", err)
	}

	// claimed → in_progress.
	if err := Update(db, car.ID, map[string]interface{}{"status": "in_progress"}); err != nil {
		t.Fatalf("claimed→in_progress: %v", err)
	}

	// in_progress → done.
	if err := Update(db, car.ID, map[string]interface{}{"status": "done"}); err != nil {
		t.Fatalf("in_progress→done: %v", err)
	}

	got, err := Get(db, car.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Status != "done" {
		t.Errorf("final Status = %q, want done", got.Status)
	}
	if got.ClaimedAt == nil {
		t.Error("ClaimedAt should be set")
	}
	if got.CompletedAt == nil {
		t.Error("CompletedAt should be set")
	}
}
