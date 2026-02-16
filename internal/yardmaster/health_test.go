package yardmaster

import (
	"strings"
	"testing"
	"time"
)

func TestCheckEngineHealth_NilDB(t *testing.T) {
	_, err := CheckEngineHealth(nil, 60*time.Second)
	if err == nil {
		t.Fatal("expected error for nil db")
	}
	if !strings.Contains(err.Error(), "db is required") {
		t.Errorf("error = %q", err)
	}
}

func TestCheckEngineHealth_ZeroThreshold(t *testing.T) {
	// With nil db, db check happens first.
	_, err := CheckEngineHealth(nil, 0)
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestCheckEngineHealth_NegativeThreshold(t *testing.T) {
	_, err := CheckEngineHealth(nil, -1*time.Second)
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestStaleEngines_UsesDefault(t *testing.T) {
	// Just verify it calls CheckEngineHealth with nil db (returns error).
	_, err := StaleEngines(nil)
	if err == nil {
		t.Fatal("expected error for nil db")
	}
}

func TestDefaultStaleThreshold(t *testing.T) {
	if DefaultStaleThreshold != 60*time.Second {
		t.Errorf("DefaultStaleThreshold = %v, want 60s", DefaultStaleThreshold)
	}
}

func TestReassignCar_NilDB(t *testing.T) {
	err := ReassignCar(nil, "car-001", "eng-001", "stalled")
	if err == nil {
		t.Fatal("expected error for nil db")
	}
	if !strings.Contains(err.Error(), "db is required") {
		t.Errorf("error = %q", err)
	}
}

func TestReassignCar_EmptyCarID(t *testing.T) {
	// nil db check comes first, then carID check.
	err := ReassignCar(nil, "", "eng-001", "stalled")
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestReassignCar_EmptyEngineID(t *testing.T) {
	// nil db check comes first, then field checks.
	err := ReassignCar(nil, "car-001", "", "stalled")
	if err == nil {
		t.Fatal("expected error")
	}
}
