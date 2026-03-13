package engine

import (
	"strings"
	"testing"

	"github.com/zulandar/railyard/internal/db"
	"github.com/zulandar/railyard/internal/models"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func outcomeTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	gormDB, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	if err := db.AutoMigrate(gormDB); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return gormDB
}

// --- HandleCompletion validation tests ---

func TestHandleCompletion_NilCar(t *testing.T) {
	err := HandleCompletion(nil, nil, &models.Engine{ID: "eng-1"}, CompletionOpts{RepoDir: "/tmp"})
	if err == nil {
		t.Fatal("expected error for nil car")
	}
}

func TestHandleCompletion_NilEngine(t *testing.T) {
	err := HandleCompletion(nil, &models.Car{ID: "car-1"}, nil, CompletionOpts{RepoDir: "/tmp"})
	if err == nil {
		t.Fatal("expected error for nil engine")
	}
}

func TestHandleCompletion_EmptyRepoDir(t *testing.T) {
	err := HandleCompletion(nil, &models.Car{ID: "car-1"}, &models.Engine{ID: "eng-1"}, CompletionOpts{})
	if err == nil {
		t.Fatal("expected error for empty repoDir")
	}
}

// --- HandleClearCycle validation tests ---

func TestHandleClearCycle_NilCar(t *testing.T) {
	err := HandleClearCycle(nil, nil, &models.Engine{ID: "eng-1"}, ClearCycleOpts{Cycle: 1})
	if err == nil {
		t.Fatal("expected error for nil car")
	}
}

func TestHandleClearCycle_NilEngine(t *testing.T) {
	err := HandleClearCycle(nil, &models.Car{ID: "car-1"}, nil, ClearCycleOpts{Cycle: 1})
	if err == nil {
		t.Fatal("expected error for nil engine")
	}
}

func TestHandleClearCycle_ZeroCycle(t *testing.T) {
	err := HandleClearCycle(nil, &models.Car{ID: "car-1"}, &models.Engine{ID: "eng-1"}, ClearCycleOpts{Cycle: 0})
	if err == nil {
		t.Fatal("expected error for zero cycle")
	}
}

func TestHandleClearCycle_NegativeCycle(t *testing.T) {
	err := HandleClearCycle(nil, &models.Car{ID: "car-1"}, &models.Engine{ID: "eng-1"}, ClearCycleOpts{Cycle: -1})
	if err == nil {
		t.Fatal("expected error for negative cycle")
	}
}

// --- HandleClearCycle push behavior ---

func TestHandleClearCycle_EmptyBranch_SkipsPush(t *testing.T) {
	gormDB := outcomeTestDB(t)

	// With empty branch, PushBranch should be skipped entirely.
	// No error expected since push is non-fatal and branch guard skips it.
	err := HandleClearCycle(gormDB, &models.Car{ID: "car-1", Branch: ""}, &models.Engine{ID: "eng-1"}, ClearCycleOpts{
		RepoDir: "/nonexistent",
		Cycle:   1,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestHandleClearCycle_EmptyRepoDir_SkipsPush(t *testing.T) {
	gormDB := outcomeTestDB(t)

	// With empty repoDir, PushBranch should be skipped even if branch is set.
	err := HandleClearCycle(gormDB, &models.Car{ID: "car-1", Branch: "feat/x"}, &models.Engine{ID: "eng-1"}, ClearCycleOpts{
		RepoDir: "",
		Cycle:   1,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestHandleClearCycle_NonEmptyBranch_PushFailureNonFatal(t *testing.T) {
	gormDB := outcomeTestDB(t)

	// With non-empty branch and invalid repoDir, PushBranch will fail
	// but the error should be non-fatal (logged, not returned).
	err := HandleClearCycle(gormDB, &models.Car{ID: "car-1", Branch: "feat/x"}, &models.Engine{ID: "eng-1"}, ClearCycleOpts{
		RepoDir: t.TempDir(), // valid dir but not a git repo — push will fail
		Cycle:   1,
	})
	if err != nil {
		if strings.Contains(err.Error(), "push") {
			t.Fatalf("push failure should be non-fatal, got: %v", err)
		}
		t.Fatalf("unexpected error: %v", err)
	}
}
