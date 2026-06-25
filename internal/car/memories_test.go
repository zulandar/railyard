package car

import (
	"strings"
	"testing"

	"github.com/zulandar/railyard/internal/models"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// testMemDB creates an in-memory SQLite database with tables needed by the memories package.
func testMemDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	if err := db.AutoMigrate(
		&models.Car{},
		&models.CarMemory{},
	); err != nil {
		t.Fatalf("migrate test db: %v", err)
	}
	return db
}

// createMemCar is a test helper that creates a car and returns its ID.
func createMemCar(t *testing.T, db *gorm.DB, id, title, track string) {
	t.Helper()
	car := models.Car{
		ID:    id,
		Title: title,
		Track: track,
	}
	if err := db.Create(&car).Error; err != nil {
		t.Fatalf("createCar(%q): %v", id, err)
	}
}

// --- Remember tests ---

func TestRemember_CreatesMemory(t *testing.T) {
	db := testMemDB(t)
	createMemCar(t, db, "car-m1", "Test car", "backend")

	if err := Remember(db, "car-m1", "author", "Alice"); err != nil {
		t.Fatalf("Remember: %v", err)
	}

	var mem models.CarMemory
	if err := db.Where("car_id = ? AND keyword = ?", "car-m1", "author").First(&mem).Error; err != nil {
		t.Fatalf("find memory: %v", err)
	}
	if mem.Content != "Alice" {
		t.Errorf("Content = %q, want %q", mem.Content, "Alice")
	}
	if mem.Track != "backend" {
		t.Errorf("Track = %q, want %q", mem.Track, "backend")
	}
}

func TestRemember_UpsertsExisting(t *testing.T) {
	db := testMemDB(t)
	createMemCar(t, db, "car-m2", "Test car", "backend")

	// First insert.
	if err := Remember(db, "car-m2", "color", "blue"); err != nil {
		t.Fatalf("Remember (first): %v", err)
	}

	// Second insert with same key — should update.
	if err := Remember(db, "car-m2", "color", "red"); err != nil {
		t.Fatalf("Remember (second): %v", err)
	}

	var mem models.CarMemory
	if err := db.Where("car_id = ? AND keyword = ?", "car-m2", "color").First(&mem).Error; err != nil {
		t.Fatalf("find memory: %v", err)
	}
	if mem.Content != "red" {
		t.Errorf("Content = %q, want %q (upserted)", mem.Content, "red")
	}

	// Should still have exactly one row.
	var count int64
	db.Model(&models.CarMemory{}).Where("car_id = ? AND keyword = ?", "car-m2", "color").Count(&count)
	if count != 1 {
		t.Errorf("row count = %d, want 1", count)
	}
}

func TestRemember_MultipleKeywords(t *testing.T) {
	db := testMemDB(t)
	createMemCar(t, db, "car-m3", "Test car", "backend")

	if err := Remember(db, "car-m3", "author", "Alice"); err != nil {
		t.Fatalf("Remember author: %v", err)
	}
	if err := Remember(db, "car-m3", "color", "blue"); err != nil {
		t.Fatalf("Remember color: %v", err)
	}
	if err := Remember(db, "car-m3", "size", "large"); err != nil {
		t.Fatalf("Remember size: %v", err)
	}

	var count int64
	db.Model(&models.CarMemory{}).Where("car_id = ?", "car-m3").Count(&count)
	if count != 3 {
		t.Errorf("row count = %d, want 3", count)
	}
}

func TestRemember_EmptyCarID(t *testing.T) {
	db := testMemDB(t)

	err := Remember(db, "", "keyword", "content")
	if err == nil {
		t.Fatal("expected error for empty car ID")
	}
	if !strings.Contains(err.Error(), "car ID is required") {
		t.Errorf("error = %q, want to contain 'car ID is required'", err.Error())
	}
}

func TestRemember_EmptyKeyword(t *testing.T) {
	db := testMemDB(t)
	createMemCar(t, db, "car-m4", "Test car", "backend")

	err := Remember(db, "car-m4", "", "content")
	if err == nil {
		t.Fatal("expected error for empty keyword")
	}
	if !strings.Contains(err.Error(), "keyword is required") {
		t.Errorf("error = %q, want to contain 'keyword is required'", err.Error())
	}
}

func TestRemember_EmptyContent(t *testing.T) {
	db := testMemDB(t)
	createMemCar(t, db, "car-m5", "Test car", "backend")

	err := Remember(db, "car-m5", "note", "")
	if err == nil {
		t.Fatal("expected error for empty content")
	}
	if !strings.Contains(err.Error(), "content is required") {
		t.Errorf("error = %q, want to contain 'content is required'", err.Error())
	}
}

func TestRemember_CarNotFound(t *testing.T) {
	db := testMemDB(t)

	err := Remember(db, "car-nonexistent", "keyword", "content")
	if err == nil {
		t.Fatal("expected error for nonexistent car")
	}
	if !strings.Contains(err.Error(), "car not found") {
		t.Errorf("error = %q, want to contain 'car not found'", err.Error())
	}
}

func TestRemember_DBError(t *testing.T) {
	db := testMemDB(t)
	createMemCar(t, db, "car-me", "Test car", "backend")

	sqlDB, _ := db.DB()
	sqlDB.Close()

	err := Remember(db, "car-me", "keyword", "content")
	if err == nil {
		t.Fatal("expected error with closed DB")
	}
}

// --- Memories tests ---

func TestMemories_ListsAll(t *testing.T) {
	db := testMemDB(t)
	createMemCar(t, db, "car-l1", "Test car", "backend")

	Remember(db, "car-l1", "author", "Alice")
	Remember(db, "car-l1", "color", "blue")
	Remember(db, "car-l1", "size", "large")

	mems, err := Memories(db, "car-l1", "")
	if err != nil {
		t.Fatalf("Memories: %v", err)
	}
	if len(mems) != 3 {
		t.Errorf("got %d memories, want 3", len(mems))
	}
}

func TestMemories_FilterByKeyword(t *testing.T) {
	db := testMemDB(t)
	createMemCar(t, db, "car-l2", "Test car", "backend")

	Remember(db, "car-l2", "author", "Alice")
	Remember(db, "car-l2", "color", "blue")

	mems, err := Memories(db, "car-l2", "author")
	if err != nil {
		t.Fatalf("Memories: %v", err)
	}
	if len(mems) != 1 {
		t.Fatalf("got %d memories, want 1", len(mems))
	}
	if mems[0].Keyword != "author" {
		t.Errorf("Keyword = %q, want %q", mems[0].Keyword, "author")
	}
	if mems[0].Content != "Alice" {
		t.Errorf("Content = %q, want %q", mems[0].Content, "Alice")
	}
}

func TestMemories_NoMemories(t *testing.T) {
	db := testMemDB(t)
	createMemCar(t, db, "car-l3", "Test car", "backend")

	mems, err := Memories(db, "car-l3", "")
	if err != nil {
		t.Fatalf("Memories: %v", err)
	}
	if len(mems) != 0 {
		t.Errorf("got %d memories, want 0", len(mems))
	}
}

func TestMemories_FilterNoMatch(t *testing.T) {
	db := testMemDB(t)
	createMemCar(t, db, "car-l4", "Test car", "backend")

	Remember(db, "car-l4", "author", "Alice")

	mems, err := Memories(db, "car-l4", "nonexistent")
	if err != nil {
		t.Fatalf("Memories: %v", err)
	}
	if len(mems) != 0 {
		t.Errorf("got %d memories, want 0", len(mems))
	}
}

func TestMemories_EmptyCarID(t *testing.T) {
	db := testMemDB(t)

	_, err := Memories(db, "", "")
	if err == nil {
		t.Fatal("expected error for empty car ID")
	}
	if !strings.Contains(err.Error(), "car ID is required") {
		t.Errorf("error = %q, want to contain 'car ID is required'", err.Error())
	}
}

func TestMemories_CarNotFound(t *testing.T) {
	db := testMemDB(t)

	_, err := Memories(db, "car-nonexistent", "")
	if err == nil {
		t.Fatal("expected error for nonexistent car")
	}
	if !strings.Contains(err.Error(), "car not found") {
		t.Errorf("error = %q, want to contain 'car not found'", err.Error())
	}
}

func TestMemories_OrderedByKeyword(t *testing.T) {
	db := testMemDB(t)
	createMemCar(t, db, "car-lo", "Test car", "backend")

	Remember(db, "car-lo", "zed", "last")
	Remember(db, "car-lo", "alpha", "first")
	Remember(db, "car-lo", "middle", "middle")

	mems, err := Memories(db, "car-lo", "")
	if err != nil {
		t.Fatalf("Memories: %v", err)
	}
	if len(mems) != 3 {
		t.Fatalf("got %d memories, want 3", len(mems))
	}
	if mems[0].Keyword != "alpha" {
		t.Errorf("first = %q, want alpha", mems[0].Keyword)
	}
	if mems[1].Keyword != "middle" {
		t.Errorf("second = %q, want middle", mems[1].Keyword)
	}
	if mems[2].Keyword != "zed" {
		t.Errorf("third = %q, want zed", mems[2].Keyword)
	}
}

func TestMemories_DBError(t *testing.T) {
	db := testMemDB(t)
	createMemCar(t, db, "car-le", "Test car", "backend")

	sqlDB, _ := db.DB()
	sqlDB.Close()

	_, err := Memories(db, "car-le", "")
	if err == nil {
		t.Fatal("expected error with closed DB")
	}
}

// --- Forget tests ---

func TestForget_DeletesMemory(t *testing.T) {
	db := testMemDB(t)
	createMemCar(t, db, "car-f1", "Test car", "backend")

	Remember(db, "car-f1", "temp", "delete me")

	if err := Forget(db, "car-f1", "temp"); err != nil {
		t.Fatalf("Forget: %v", err)
	}

	var count int64
	db.Model(&models.CarMemory{}).Where("car_id = ? AND keyword = ?", "car-f1", "temp").Count(&count)
	if count != 0 {
		t.Errorf("row count = %d, want 0 (deleted)", count)
	}
}

func TestForget_OnlyDeletesMatchingKeyword(t *testing.T) {
	db := testMemDB(t)
	createMemCar(t, db, "car-f2", "Test car", "backend")

	Remember(db, "car-f2", "keep", "keep me")
	Remember(db, "car-f2", "drop", "drop me")

	if err := Forget(db, "car-f2", "drop"); err != nil {
		t.Fatalf("Forget: %v", err)
	}

	// "keep" should still exist.
	var keep models.CarMemory
	if err := db.Where("car_id = ? AND keyword = ?", "car-f2", "keep").First(&keep).Error; err != nil {
		t.Fatalf("'keep' memory should still exist: %v", err)
	}

	// "drop" should be gone.
	var count int64
	db.Model(&models.CarMemory{}).Where("car_id = ? AND keyword = ?", "car-f2", "drop").Count(&count)
	if count != 0 {
		t.Errorf("'drop' count = %d, want 0", count)
	}
}

func TestForget_NotFound(t *testing.T) {
	db := testMemDB(t)
	createMemCar(t, db, "car-f3", "Test car", "backend")

	err := Forget(db, "car-f3", "nonexistent")
	if err == nil {
		t.Fatal("expected error for nonexistent memory")
	}
	if !strings.Contains(err.Error(), "no memory") {
		t.Errorf("error = %q, want to contain 'no memory'", err.Error())
	}
}

func TestForget_EmptyCarID(t *testing.T) {
	db := testMemDB(t)

	err := Forget(db, "", "keyword")
	if err == nil {
		t.Fatal("expected error for empty car ID")
	}
	if !strings.Contains(err.Error(), "car ID is required") {
		t.Errorf("error = %q, want to contain 'car ID is required'", err.Error())
	}
}

func TestForget_EmptyKeyword(t *testing.T) {
	db := testMemDB(t)
	createMemCar(t, db, "car-f4", "Test car", "backend")

	err := Forget(db, "car-f4", "")
	if err == nil {
		t.Fatal("expected error for empty keyword")
	}
	if !strings.Contains(err.Error(), "keyword is required") {
		t.Errorf("error = %q, want to contain 'keyword is required'", err.Error())
	}
}

func TestForget_CarNotFound(t *testing.T) {
	db := testMemDB(t)

	err := Forget(db, "car-nonexistent", "keyword")
	if err == nil {
		t.Fatal("expected error for nonexistent car")
	}
	if !strings.Contains(err.Error(), "car not found") {
		t.Errorf("error = %q, want to contain 'car not found'", err.Error())
	}
}

func TestForget_DBError(t *testing.T) {
	db := testMemDB(t)
	createMemCar(t, db, "car-fe", "Test car", "backend")
	Remember(db, "car-fe", "keyword", "content")

	sqlDB, _ := db.DB()
	sqlDB.Close()

	err := Forget(db, "car-fe", "keyword")
	if err == nil {
		t.Fatal("expected error with closed DB")
	}
}

// --- GetTrackMemories tests ---

func TestGetTrackMemories_ReturnsAllForTrack(t *testing.T) {
	db := testMemDB(t)
	createMemCar(t, db, "car-gt1", "Car A", "backend")
	createMemCar(t, db, "car-gt2", "Car B", "backend")

	Remember(db, "car-gt1", "author", "Alice")
	Remember(db, "car-gt2", "color", "blue")

	mems, err := GetTrackMemories(db, "backend")
	if err != nil {
		t.Fatalf("GetTrackMemories: %v", err)
	}
	if len(mems) != 2 {
		t.Errorf("got %d memories, want 2", len(mems))
	}
	// Ordered by keyword ASC, car_id ASC.
	if mems[0].Keyword != "author" || mems[0].CarID != "car-gt1" {
		t.Errorf("first = %q/%q, want author/car-gt1", mems[0].Keyword, mems[0].CarID)
	}
	if mems[1].Keyword != "color" || mems[1].CarID != "car-gt2" {
		t.Errorf("second = %q/%q, want color/car-gt2", mems[1].Keyword, mems[1].CarID)
	}
}

func TestGetTrackMemories_OnlyReturnsMatchingTrack(t *testing.T) {
	db := testMemDB(t)
	createMemCar(t, db, "car-gt3", "Backend Car", "backend")
	createMemCar(t, db, "car-gt4", "Frontend Car", "frontend")

	Remember(db, "car-gt3", "author", "Alice")
	Remember(db, "car-gt4", "color", "blue")

	mems, err := GetTrackMemories(db, "backend")
	if err != nil {
		t.Fatalf("GetTrackMemories: %v", err)
	}
	if len(mems) != 1 {
		t.Fatalf("got %d memories, want 1", len(mems))
	}
	if mems[0].Keyword != "author" || mems[0].CarID != "car-gt3" {
		t.Errorf("got %q/%q, want author/car-gt3", mems[0].Keyword, mems[0].CarID)
	}
}

func TestGetTrackMemories_EmptySlice(t *testing.T) {
	db := testMemDB(t)
	createMemCar(t, db, "car-gt5", "Test car", "backend")

	mems, err := GetTrackMemories(db, "backend")
	if err != nil {
		t.Fatalf("GetTrackMemories: %v", err)
	}
	if len(mems) != 0 {
		t.Errorf("got %d memories, want 0", len(mems))
	}
}

func TestGetTrackMemories_EmptyTrack(t *testing.T) {
	db := testMemDB(t)

	_, err := GetTrackMemories(db, "")
	if err == nil {
		t.Fatal("expected error for empty track")
	}
	if !strings.Contains(err.Error(), "track is required") {
		t.Errorf("error = %q, want to contain 'track is required'", err.Error())
	}
}

func TestGetTrackMemories_DBError(t *testing.T) {
	db := testMemDB(t)
	createMemCar(t, db, "car-gte", "Test car", "backend")
	Remember(db, "car-gte", "author", "Alice")

	sqlDB, _ := db.DB()
	sqlDB.Close()

	_, err := GetTrackMemories(db, "backend")
	if err == nil {
		t.Fatal("expected error with closed DB")
	}
}

// --- Round-trip test ---

func TestMemoryRoundTrip(t *testing.T) {
	db := testMemDB(t)
	createMemCar(t, db, "car-rt", "Roundtrip", "backend")

	// Start with nothing.
	mems, err := Memories(db, "car-rt", "")
	if err != nil {
		t.Fatalf("Memories (empty): %v", err)
	}
	if len(mems) != 0 {
		t.Errorf("expected 0 memories, got %d", len(mems))
	}

	// Remember two.
	if err := Remember(db, "car-rt", "a", "alpha"); err != nil {
		t.Fatalf("Remember a: %v", err)
	}
	if err := Remember(db, "car-rt", "b", "beta"); err != nil {
		t.Fatalf("Remember b: %v", err)
	}

	// List.
	mems, err = Memories(db, "car-rt", "")
	if err != nil {
		t.Fatalf("Memories: %v", err)
	}
	if len(mems) != 2 {
		t.Fatalf("expected 2 memories, got %d", len(mems))
	}

	// Upsert.
	if err := Remember(db, "car-rt", "a", "updated alpha"); err != nil {
		t.Fatalf("Remember a (upsert): %v", err)
	}
	mems, err = Memories(db, "car-rt", "a")
	if err != nil {
		t.Fatalf("Memories a: %v", err)
	}
	if mems[0].Content != "updated alpha" {
		t.Errorf("Content = %q, want %q", mems[0].Content, "updated alpha")
	}

	// Forget.
	if err := Forget(db, "car-rt", "a"); err != nil {
		t.Fatalf("Forget a: %v", err)
	}
	mems, err = Memories(db, "car-rt", "")
	if err != nil {
		t.Fatalf("Memories after forget: %v", err)
	}
	if len(mems) != 1 {
		t.Fatalf("expected 1 memory after forget, got %d", len(mems))
	}
	if mems[0].Keyword != "b" {
		t.Errorf("remaining keyword = %q, want %q", mems[0].Keyword, "b")
	}

	// Forget last one.
	if err := Forget(db, "car-rt", "b"); err != nil {
		t.Fatalf("Forget b: %v", err)
	}
	mems, err = Memories(db, "car-rt", "")
	if err != nil {
		t.Fatalf("Memories (final): %v", err)
	}
	if len(mems) != 0 {
		t.Errorf("expected 0 memories, got %d", len(mems))
	}
}
