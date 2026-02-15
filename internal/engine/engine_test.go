package engine

import (
	"strings"
	"testing"
	"time"
)

func TestGenerateID_Format(t *testing.T) {
	id, err := GenerateID()
	if err != nil {
		t.Fatalf("GenerateID() error: %v", err)
	}
	if !strings.HasPrefix(id, "eng-") {
		t.Errorf("ID %q missing eng- prefix", id)
	}
	// eng- (4 chars) + 8 hex chars = 12 total
	if len(id) != 12 {
		t.Errorf("ID length = %d, want 12; id = %q", len(id), id)
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
		hex := id[4:] // strip "eng-"
		for _, c := range hex {
			if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
				t.Errorf("ID %q contains non-hex char %c", id, c)
			}
		}
	}
}

func TestRegisterOpts_ZeroValue(t *testing.T) {
	opts := RegisterOpts{}
	if opts.Track != "" || opts.Role != "" || opts.VMID != "" || opts.SessionID != "" {
		t.Error("zero-value RegisterOpts should have all empty fields")
	}
}

func TestStatusConstants(t *testing.T) {
	if StatusIdle != "idle" {
		t.Errorf("StatusIdle = %q, want %q", StatusIdle, "idle")
	}
	if StatusWorking != "working" {
		t.Errorf("StatusWorking = %q, want %q", StatusWorking, "working")
	}
	if StatusStalled != "stalled" {
		t.Errorf("StatusStalled = %q, want %q", StatusStalled, "stalled")
	}
	if StatusDead != "dead" {
		t.Errorf("StatusDead = %q, want %q", StatusDead, "dead")
	}
}

func TestDefaultHeartbeatInterval(t *testing.T) {
	if DefaultHeartbeatInterval != 10*time.Second {
		t.Errorf("DefaultHeartbeatInterval = %v, want %v", DefaultHeartbeatInterval, 10*time.Second)
	}
}
