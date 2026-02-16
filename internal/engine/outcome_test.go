package engine

import (
	"testing"

	"github.com/zulandar/railyard/internal/models"
)

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
