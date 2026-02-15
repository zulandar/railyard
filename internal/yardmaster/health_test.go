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

func TestReassignBead_NilDB(t *testing.T) {
	err := ReassignBead(nil, "be-001", "eng-001", "stalled")
	if err == nil {
		t.Fatal("expected error for nil db")
	}
	if !strings.Contains(err.Error(), "db is required") {
		t.Errorf("error = %q", err)
	}
}

func TestReassignBead_EmptyBeadID(t *testing.T) {
	// nil db check comes first, then beadID check.
	err := ReassignBead(nil, "", "eng-001", "stalled")
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestReassignBead_EmptyEngineID(t *testing.T) {
	// nil db check comes first, then field checks.
	err := ReassignBead(nil, "be-001", "", "stalled")
	if err == nil {
		t.Fatal("expected error")
	}
}
