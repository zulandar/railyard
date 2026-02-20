package engine

import (
	"github.com/zulandar/railyard/internal/models"
	"testing"
)

func TestClassifyMessage_Nil(t *testing.T) {
	if got := ClassifyMessage(nil); got != InstructionUnknown {
		t.Errorf("ClassifyMessage(nil) = %q, want %q", got, InstructionUnknown)
	}
}

func TestClassifyMessage_AllTypes(t *testing.T) {
	tests := []struct {
		subject string
		want    InstructionType
	}{
		{"abort", InstructionAbort},
		{"pause", InstructionPause},
		{"resume", InstructionResume},
		{"switch-track", InstructionSwitchTrack},
		{"guidance", InstructionGuidance},
		{"something-else", InstructionUnknown},
		{"", InstructionUnknown},
	}
	for _, tt := range tests {
		msg := &models.Message{Subject: tt.subject}
		if got := ClassifyMessage(msg); got != tt.want {
			t.Errorf("ClassifyMessage(%q) = %q, want %q", tt.subject, got, tt.want)
		}
	}
}

func TestProcessInbox_EmptyEngineID(t *testing.T) {
	_, err := ProcessInbox(nil, "")
	if err == nil {
		t.Fatal("expected error for empty engineID")
	}
}

func TestShouldAbort_MatchingCar(t *testing.T) {
	instructions := []Instruction{
		{Type: InstructionGuidance, CarID: "car-123"},
		{Type: InstructionAbort, CarID: "car-456"},
	}
	if !ShouldAbort(instructions, "car-456") {
		t.Error("expected abort for matching car")
	}
	if ShouldAbort(instructions, "car-999") {
		t.Error("did not expect abort for non-matching car")
	}
}

func TestShouldAbort_EmptyCarID(t *testing.T) {
	instructions := []Instruction{
		{Type: InstructionAbort, CarID: ""},
	}
	// Empty CarID on abort means "abort any car"
	if !ShouldAbort(instructions, "car-anything") {
		t.Error("expected abort when instruction has empty CarID")
	}
}

func TestShouldAbort_Empty(t *testing.T) {
	if ShouldAbort(nil, "car-123") {
		t.Error("expected no abort for empty instructions")
	}
}

func TestShouldPause(t *testing.T) {
	if ShouldPause(nil) {
		t.Error("expected no pause for nil")
	}
	instructions := []Instruction{{Type: InstructionPause}}
	if !ShouldPause(instructions) {
		t.Error("expected pause")
	}
}

func TestHasResume(t *testing.T) {
	if HasResume(nil) {
		t.Error("expected no resume for nil")
	}
	instructions := []Instruction{{Type: InstructionResume}}
	if !HasResume(instructions) {
		t.Error("expected resume")
	}
}

func TestInstructionTypeConstants(t *testing.T) {
	if InstructionAbort != "abort" {
		t.Errorf("InstructionAbort = %q", InstructionAbort)
	}
	if InstructionPause != "pause" {
		t.Errorf("InstructionPause = %q", InstructionPause)
	}
	if InstructionResume != "resume" {
		t.Errorf("InstructionResume = %q", InstructionResume)
	}
	if InstructionSwitchTrack != "switch-track" {
		t.Errorf("InstructionSwitchTrack = %q", InstructionSwitchTrack)
	}
	if InstructionGuidance != "guidance" {
		t.Errorf("InstructionGuidance = %q", InstructionGuidance)
	}
}
