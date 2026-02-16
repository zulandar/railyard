package car

import (
	"strings"
	"testing"
)

func TestGenerateID_Format(t *testing.T) {
	id, err := GenerateID()
	if err != nil {
		t.Fatalf("GenerateID() error: %v", err)
	}
	if !strings.HasPrefix(id, "car-") {
		t.Errorf("ID %q missing car- prefix", id)
	}
	// car- (4 chars) + 5 hex chars = 9 total
	if len(id) != 9 {
		t.Errorf("ID length = %d, want 9; id = %q", len(id), id)
	}
}

func TestGenerateID_Unique(t *testing.T) {
	seen := make(map[string]bool)
	for i := 0; i < 100; i++ {
		id, err := GenerateID()
		if err != nil {
			t.Fatalf("GenerateID() iteration %d: %v", i, err)
		}
		if seen[id] {
			t.Fatalf("duplicate ID %q on iteration %d", id, i)
		}
		seen[id] = true
	}
}

func TestGenerateID_HexChars(t *testing.T) {
	for i := 0; i < 20; i++ {
		id, err := GenerateID()
		if err != nil {
			t.Fatalf("GenerateID(): %v", err)
		}
		hex := id[4:] // strip "car-"
		for _, c := range hex {
			if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
				t.Errorf("ID %q contains non-hex char %c", id, c)
			}
		}
	}
}

func TestComputeBranch(t *testing.T) {
	tests := []struct {
		prefix string
		track  string
		id     string
		want   string
	}{
		{"ry/alice", "backend", "car-abc12", "ry/alice/backend/car-abc12"},
		{"ry/bob", "frontend", "car-00000", "ry/bob/frontend/car-00000"},
		{"ry/carol", "infra", "car-fffff", "ry/carol/infra/car-fffff"},
	}
	for _, tt := range tests {
		got := ComputeBranch(tt.prefix, tt.track, tt.id)
		if got != tt.want {
			t.Errorf("ComputeBranch(%q, %q, %q) = %q, want %q",
				tt.prefix, tt.track, tt.id, got, tt.want)
		}
	}
}

func TestIsValidTransition(t *testing.T) {
	tests := []struct {
		from string
		to   string
		want bool
	}{
		// Valid forward transitions
		{"open", "ready", true},
		{"ready", "claimed", true},
		{"claimed", "in_progress", true},
		{"in_progress", "done", true},
		{"open", "cancelled", true},

		// Any → blocked
		{"open", "blocked", true},
		{"ready", "blocked", true},
		{"claimed", "blocked", true},
		{"in_progress", "blocked", true},

		// Unblock transitions
		{"blocked", "open", true},
		{"blocked", "ready", true},

		// Invalid transitions
		{"open", "done", false},
		{"open", "in_progress", false},
		{"open", "claimed", false},
		{"ready", "done", false},
		{"ready", "open", false},
		{"claimed", "done", false},
		{"in_progress", "ready", false},
		{"done", "open", false},
		{"cancelled", "open", false},

		// Unknown status
		{"unknown", "open", false},
	}
	for _, tt := range tests {
		got := isValidTransition(tt.from, tt.to)
		if got != tt.want {
			t.Errorf("isValidTransition(%q, %q) = %v, want %v",
				tt.from, tt.to, got, tt.want)
		}
	}
}

func TestValidTransitions_AllStatusesPresent(t *testing.T) {
	expected := []string{"open", "ready", "claimed", "in_progress", "blocked"}
	for _, status := range expected {
		if _, ok := ValidTransitions[status]; !ok {
			t.Errorf("ValidTransitions missing key %q", status)
		}
	}
}

func TestCreateOpts_Defaults(t *testing.T) {
	opts := CreateOpts{
		Title: "test",
		Track: "backend",
	}
	if opts.Type != "" {
		t.Errorf("default Type should be empty (applied in Create), got %q", opts.Type)
	}
	if opts.Priority != 0 {
		t.Errorf("default Priority should be 0 (zero value), got %d", opts.Priority)
	}
}

func TestListFilters_ZeroValue(t *testing.T) {
	f := ListFilters{}
	if f.Track != "" || f.Status != "" || f.Type != "" || f.Assignee != "" || f.ParentID != "" {
		t.Error("zero-value ListFilters should have all empty fields")
	}
}

func TestStatusCount_ZeroValue(t *testing.T) {
	sc := StatusCount{}
	if sc.Status != "" || sc.Count != 0 {
		t.Error("zero-value StatusCount should have empty Status and zero Count")
	}
}
