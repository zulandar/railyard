package telegraph

import (
	"strings"
	"testing"

	"github.com/zulandar/railyard/internal/orchestration"
)

// --- FormatCarEvent tests ---

func TestFormatCarEvent_Claimed(t *testing.T) {
	e := FormatCarEvent(DetectedEvent{
		Type:      EventCarStatusChange,
		CarID:     "backend-42",
		OldStatus: "open",
		NewStatus: "in_progress",
		Track:     "backend",
		Title:     "Add user auth",
	})
	if e.Title != "Car backend-42 claimed" {
		t.Errorf("title = %q, want %q", e.Title, "Car backend-42 claimed")
	}
	if !strings.Contains(e.Body, "Add user auth") {
		t.Errorf("body should contain car title, got %q", e.Body)
	}
	if !strings.Contains(e.Body, "open → in_progress") {
		t.Errorf("body should contain status transition, got %q", e.Body)
	}
	if e.Severity != "info" {
		t.Errorf("severity = %q, want %q", e.Severity, "info")
	}
	if e.Color != ColorInfo {
		t.Errorf("color = %q, want %q", e.Color, ColorInfo)
	}
}

func TestFormatCarEvent_Completed(t *testing.T) {
	e := FormatCarEvent(DetectedEvent{
		CarID:     "fe-10",
		OldStatus: "in_progress",
		NewStatus: "done",
		Track:     "frontend",
	})
	if e.Title != "Car fe-10 completed" {
		t.Errorf("title = %q", e.Title)
	}
	if e.Severity != "success" {
		t.Errorf("severity = %q, want success", e.Severity)
	}
	if e.Color != ColorSuccess {
		t.Errorf("color = %q, want %q", e.Color, ColorSuccess)
	}
}

func TestFormatCarEvent_Merged(t *testing.T) {
	e := FormatCarEvent(DetectedEvent{
		CarID:     "car-1",
		OldStatus: "done",
		NewStatus: "merged",
	})
	if e.Title != "Car car-1 merged" {
		t.Errorf("title = %q", e.Title)
	}
	if e.Severity != "success" {
		t.Errorf("severity = %q, want success", e.Severity)
	}
}

func TestFormatCarEvent_Blocked(t *testing.T) {
	e := FormatCarEvent(DetectedEvent{
		CarID:     "car-1",
		OldStatus: "open",
		NewStatus: "blocked",
		Track:     "backend",
	})
	if e.Title != "Car car-1 blocked" {
		t.Errorf("title = %q", e.Title)
	}
	if e.Severity != "warning" {
		t.Errorf("severity = %q, want warning", e.Severity)
	}
	if e.Color != ColorWarning {
		t.Errorf("color = %q, want %q", e.Color, ColorWarning)
	}
}

func TestFormatCarEvent_MergeFailed(t *testing.T) {
	e := FormatCarEvent(DetectedEvent{
		CarID:     "car-1",
		NewStatus: "merge-failed",
	})
	if e.Title != "Car car-1 merge failed" {
		t.Errorf("title = %q", e.Title)
	}
	if e.Severity != "warning" {
		t.Errorf("severity = %q, want warning", e.Severity)
	}
}

func TestFormatCarEvent_NewCarNoOldStatus(t *testing.T) {
	e := FormatCarEvent(DetectedEvent{
		CarID:     "car-new",
		OldStatus: "",
		NewStatus: "draft",
		Title:     "Brand new car",
	})
	if e.Title != "Car car-new created" {
		t.Errorf("title = %q", e.Title)
	}
	// Body should not contain transition arrow when no old status.
	if strings.Contains(e.Body, "→") {
		t.Errorf("body should not contain transition for new car, got %q", e.Body)
	}
}

func TestFormatCarEvent_Fields(t *testing.T) {
	e := FormatCarEvent(DetectedEvent{
		CarID:     "car-1",
		NewStatus: "open",
		Track:     "backend",
	})
	fieldNames := make(map[string]bool)
	for _, f := range e.Fields {
		fieldNames[f.Name] = true
	}
	if !fieldNames["Car"] {
		t.Error("missing Car field")
	}
	if !fieldNames["Status"] {
		t.Error("missing Status field")
	}
	if !fieldNames["Track"] {
		t.Error("missing Track field")
	}
}

func TestFormatCarEvent_NoTrackField(t *testing.T) {
	e := FormatCarEvent(DetectedEvent{
		CarID:     "car-1",
		NewStatus: "open",
		Track:     "",
	})
	for _, f := range e.Fields {
		if f.Name == "Track" {
			t.Error("should not include Track field when track is empty")
		}
	}
}

// --- FormatStallEvent tests ---

func TestFormatStallEvent_WithCarAndTrack(t *testing.T) {
	e := FormatStallEvent(DetectedEvent{
		EngineID:   "eng-a1b2",
		CurrentCar: "car-5",
		Track:      "backend",
	})
	if e.Title != "Engine eng-a1b2 stalled" {
		t.Errorf("title = %q", e.Title)
	}
	if !strings.Contains(e.Body, "car-5") {
		t.Errorf("body should mention current car, got %q", e.Body)
	}
	if !strings.Contains(e.Body, "backend") {
		t.Errorf("body should mention track, got %q", e.Body)
	}
	if e.Severity != "warning" {
		t.Errorf("severity = %q, want warning", e.Severity)
	}
	if e.Color != ColorWarning {
		t.Errorf("color = %q, want %q", e.Color, ColorWarning)
	}
}

func TestFormatStallEvent_NoCar(t *testing.T) {
	e := FormatStallEvent(DetectedEvent{
		EngineID: "eng-1",
		Track:    "frontend",
	})
	if strings.Contains(e.Body, "Working on car") {
		t.Errorf("body should not mention car when empty, got %q", e.Body)
	}
	// Should not have Car field.
	for _, f := range e.Fields {
		if f.Name == "Car" {
			t.Error("should not include Car field when current car is empty")
		}
	}
}

func TestFormatStallEvent_NoTrack(t *testing.T) {
	e := FormatStallEvent(DetectedEvent{
		EngineID:   "eng-1",
		CurrentCar: "car-1",
	})
	for _, f := range e.Fields {
		if f.Name == "Track" {
			t.Error("should not include Track field when track is empty")
		}
	}
}

// --- FormatEscalation tests ---

func TestFormatEscalation_NormalPriority(t *testing.T) {
	e := FormatEscalation(DetectedEvent{
		FromAgent: "yardmaster",
		CarID:     "car-1",
		Subject:   "Engine stalled",
		Body:      "Engine eng-1 stopped responding",
		Priority:  "normal",
	})
	if e.Title != "Engine stalled" {
		t.Errorf("title = %q, want subject", e.Title)
	}
	if e.Body != "Engine eng-1 stopped responding" {
		t.Errorf("body = %q", e.Body)
	}
	if e.Severity != "warning" {
		t.Errorf("severity = %q, want warning", e.Severity)
	}
}

func TestFormatEscalation_HighPriority(t *testing.T) {
	e := FormatEscalation(DetectedEvent{
		FromAgent: "yardmaster",
		Subject:   "Critical failure",
		Priority:  "high",
	})
	if e.Severity != "error" {
		t.Errorf("severity = %q, want error for high priority", e.Severity)
	}
	if e.Color != ColorError {
		t.Errorf("color = %q, want %q", e.Color, ColorError)
	}
}

func TestFormatEscalation_UrgentPriority(t *testing.T) {
	e := FormatEscalation(DetectedEvent{
		FromAgent: "engine-1",
		Subject:   "Urgent issue",
		Priority:  "urgent",
	})
	if e.Severity != "error" {
		t.Errorf("severity = %q, want error for urgent priority", e.Severity)
	}
}

func TestFormatEscalation_NoSubjectFallback(t *testing.T) {
	e := FormatEscalation(DetectedEvent{
		FromAgent: "engine-1",
		Body:      "Some issue happened",
	})
	if e.Title != "Escalation from engine-1" {
		t.Errorf("title = %q, want fallback", e.Title)
	}
}

func TestFormatEscalation_Fields(t *testing.T) {
	e := FormatEscalation(DetectedEvent{
		FromAgent: "yardmaster",
		CarID:     "car-1",
		Priority:  "high",
		Subject:   "Test",
	})
	fieldNames := make(map[string]bool)
	for _, f := range e.Fields {
		fieldNames[f.Name] = true
	}
	if !fieldNames["From"] {
		t.Error("missing From field")
	}
	if !fieldNames["Car"] {
		t.Error("missing Car field")
	}
	if !fieldNames["Priority"] {
		t.Error("missing Priority field")
	}
}

func TestFormatEscalation_NoCarField(t *testing.T) {
	e := FormatEscalation(DetectedEvent{
		FromAgent: "engine-1",
		Subject:   "General issue",
	})
	for _, f := range e.Fields {
		if f.Name == "Car" {
			t.Error("should not include Car field when car id is empty")
		}
	}
}

// --- FormatPulse tests ---

func TestFormatPulse_BasicStatus(t *testing.T) {
	info := &orchestration.StatusInfo{
		Engines: []orchestration.EngineInfo{
			{ID: "eng-1", Status: "working"},
			{ID: "eng-2", Status: "idle"},
			{ID: "eng-3", Status: "working"},
		},
		TrackSummary: []orchestration.TrackSummary{
			{Track: "backend", InProgress: 2, Ready: 3, Done: 5, Blocked: 1},
			{Track: "frontend", InProgress: 1, Ready: 1, Done: 2, Blocked: 0},
		},
		TotalTokens: 150000,
		MessageDepth: 3,
	}

	e := FormatPulse(info)
	if e.Title != "Railyard Pulse" {
		t.Errorf("title = %q, want 'Railyard Pulse'", e.Title)
	}
	if e.Severity != "info" {
		t.Errorf("severity = %q, want info", e.Severity)
	}
	if !strings.Contains(e.Body, "3 total, 2 working") {
		t.Errorf("body should contain engine counts, got %q", e.Body)
	}
	if !strings.Contains(e.Body, "3 active, 4 ready, 7 done, 1 blocked") {
		t.Errorf("body should contain car counts, got %q", e.Body)
	}
	if !strings.Contains(e.Body, "150000 total") {
		t.Errorf("body should contain token count, got %q", e.Body)
	}
	if !strings.Contains(e.Body, "3 pending") {
		t.Errorf("body should contain message depth, got %q", e.Body)
	}
}

func TestFormatPulse_NoTokensOrMessages(t *testing.T) {
	info := &orchestration.StatusInfo{
		Engines: []orchestration.EngineInfo{
			{ID: "eng-1", Status: "idle"},
		},
		TrackSummary: []orchestration.TrackSummary{
			{Track: "backend", Done: 10},
		},
	}

	e := FormatPulse(info)
	if strings.Contains(e.Body, "Tokens") {
		t.Errorf("body should not contain tokens when zero, got %q", e.Body)
	}
	if strings.Contains(e.Body, "Messages") {
		t.Errorf("body should not contain messages when zero, got %q", e.Body)
	}
}

func TestFormatPulse_EmptyStatus(t *testing.T) {
	info := &orchestration.StatusInfo{}

	e := FormatPulse(info)
	if e.Title != "Railyard Pulse" {
		t.Errorf("title = %q", e.Title)
	}
	if !strings.Contains(e.Body, "0 total, 0 working") {
		t.Errorf("body should handle zero engines, got %q", e.Body)
	}
}

func TestFormatPulse_BlockedFieldIncluded(t *testing.T) {
	info := &orchestration.StatusInfo{
		TrackSummary: []orchestration.TrackSummary{
			{Track: "backend", Blocked: 5},
		},
	}

	e := FormatPulse(info)
	hasBlocked := false
	for _, f := range e.Fields {
		if f.Name == "Blocked" {
			hasBlocked = true
			if f.Value != "5" {
				t.Errorf("blocked value = %q, want 5", f.Value)
			}
		}
	}
	if !hasBlocked {
		t.Error("missing Blocked field when there are blocked cars")
	}
}

func TestFormatPulse_NoBlockedFieldWhenZero(t *testing.T) {
	info := &orchestration.StatusInfo{
		TrackSummary: []orchestration.TrackSummary{
			{Track: "backend", InProgress: 1},
		},
	}

	e := FormatPulse(info)
	for _, f := range e.Fields {
		if f.Name == "Blocked" {
			t.Error("should not include Blocked field when zero")
		}
	}
}

// --- Helper function tests ---

func TestSeverityColor_AllValues(t *testing.T) {
	tests := []struct {
		severity string
		want     string
	}{
		{"success", ColorSuccess},
		{"info", ColorInfo},
		{"warning", ColorWarning},
		{"error", ColorError},
		{"unknown", ColorInfo},
	}
	for _, tt := range tests {
		got := severityColor(tt.severity)
		if got != tt.want {
			t.Errorf("severityColor(%q) = %q, want %q", tt.severity, got, tt.want)
		}
	}
}

func TestCarStatusVerb_AllStatuses(t *testing.T) {
	tests := []struct {
		status string
		want   string
	}{
		{"open", "opened"},
		{"in_progress", "claimed"},
		{"done", "completed"},
		{"merged", "merged"},
		{"blocked", "blocked"},
		{"merge-failed", "merge failed"},
		{"cancelled", "cancelled"},
		{"draft", "created"},
		{"unknown", "unknown"},
	}
	for _, tt := range tests {
		got := carStatusVerb(tt.status)
		if got != tt.want {
			t.Errorf("carStatusVerb(%q) = %q, want %q", tt.status, got, tt.want)
		}
	}
}

func TestCarStatusSeverity_AllStatuses(t *testing.T) {
	tests := []struct {
		status string
		want   string
	}{
		{"done", "success"},
		{"merged", "success"},
		{"blocked", "warning"},
		{"merge-failed", "warning"},
		{"cancelled", "info"},
		{"open", "info"},
		{"in_progress", "info"},
	}
	for _, tt := range tests {
		got := carStatusSeverity(tt.status)
		if got != tt.want {
			t.Errorf("carStatusSeverity(%q) = %q, want %q", tt.status, got, tt.want)
		}
	}
}
