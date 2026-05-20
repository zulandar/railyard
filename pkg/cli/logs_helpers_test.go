package main

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/zulandar/railyard/internal/models"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func testGormDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	if err := db.AutoMigrate(&models.AgentLog{}, &models.Message{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return db
}

// ---------------------------------------------------------------------------
// 1. TestShortID
// ---------------------------------------------------------------------------

func TestShortID(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"short", "abc", "abc"},
		{"exact_14", "12345678901234", "12345678901234"},
		{"longer_than_14", "123456789012345", "12345678901234"},
		{"empty", "", ""},
		{"16_chars", "abcdefghijklmnop", "abcdefghijklmn"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := shortID(tt.in)
			if got != tt.want {
				t.Errorf("shortID(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// 2. TestFormatStreamLine
// ---------------------------------------------------------------------------

func TestFormatStreamLine(t *testing.T) {
	tests := []struct {
		name string
		line string
		want string // substring that must appear in the result
	}{
		{"empty", "", ""},
		{"non_json", "this is not json", ""},
		{"system_type", `{"type":"system","subtype":"init"}`, "system"},
		{"result_type", `{"type":"result","subtype":"success"}`, "result"},
		{"unknown_type", `{"type":"foobar"}`, "foobar"},
		{"assistant_text", `{"type":"assistant","message":{"content":[{"type":"text","text":"hello world"}]}}`, "assistant"},
		{"user_type", `{"type":"user","message":{"content":[{"type":"text","text":"hi"}]}}`, "user"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := formatStreamLine(tt.line)
			if tt.want != "" && !strings.Contains(got, tt.want) {
				t.Errorf("formatStreamLine(%q) = %q, want substring %q", tt.line, got, tt.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// 3. TestFormatAssistantMessage
// ---------------------------------------------------------------------------

func TestFormatAssistantMessage(t *testing.T) {
	tests := []struct {
		name string
		obj  map[string]interface{}
		want string
	}{
		{
			name: "no_message_key",
			obj:  map[string]interface{}{},
			want: "assistant:",
		},
		{
			name: "empty_content",
			obj: map[string]interface{}{
				"message": map[string]interface{}{
					"content": []interface{}{},
				},
			},
			want: "assistant:",
		},
		{
			name: "text_block",
			obj: map[string]interface{}{
				"message": map[string]interface{}{
					"content": []interface{}{
						map[string]interface{}{"type": "text", "text": "hello world"},
					},
				},
			},
			want: "assistant:",
		},
		{
			name: "tool_use_block",
			obj: map[string]interface{}{
				"message": map[string]interface{}{
					"content": []interface{}{
						map[string]interface{}{"type": "tool_use", "name": "read_file"},
					},
				},
			},
			want: "tool_use: read_file",
		},
		{
			name: "multiple_blocks",
			obj: map[string]interface{}{
				"message": map[string]interface{}{
					"content": []interface{}{
						map[string]interface{}{"type": "text", "text": "thinking about it"},
						map[string]interface{}{"type": "tool_use", "name": "bash"},
					},
				},
			},
			want: "|",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := formatAssistantMessage(tt.obj)
			if !strings.Contains(got, tt.want) {
				t.Errorf("formatAssistantMessage() = %q, want substring %q", got, tt.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// 4. TestFormatUserMessage
// ---------------------------------------------------------------------------

func TestFormatUserMessage(t *testing.T) {
	tests := []struct {
		name string
		obj  map[string]interface{}
		want string
	}{
		{
			name: "no_message_key",
			obj:  map[string]interface{}{},
			want: "user:",
		},
		{
			name: "empty_content",
			obj: map[string]interface{}{
				"message": map[string]interface{}{
					"content": []interface{}{},
				},
			},
			want: "user:",
		},
		{
			name: "tool_result_type",
			obj: map[string]interface{}{
				"message": map[string]interface{}{
					"content": []interface{}{
						map[string]interface{}{"type": "tool_result"},
					},
				},
			},
			want: "tool_result",
		},
		{
			name: "other_type",
			obj: map[string]interface{}{
				"message": map[string]interface{}{
					"content": []interface{}{
						map[string]interface{}{"type": "text", "text": "some input"},
					},
				},
			},
			want: "user:",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := formatUserMessage(tt.obj)
			if !strings.Contains(got, tt.want) {
				t.Errorf("formatUserMessage() = %q, want substring %q", got, tt.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// 5. TestFormatResult
// ---------------------------------------------------------------------------

func TestFormatResult(t *testing.T) {
	tests := []struct {
		name string
		obj  map[string]interface{}
		want string
	}{
		{
			name: "with_subtype",
			obj:  map[string]interface{}{"subtype": "success"},
			want: "result (success)",
		},
		{
			name: "empty_subtype",
			obj:  map[string]interface{}{"subtype": ""},
			want: "result ()",
		},
		{
			name: "nil_subtype",
			obj:  map[string]interface{}{},
			want: "result",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := formatResult(tt.obj)
			if !strings.Contains(got, tt.want) {
				t.Errorf("formatResult() = %q, want substring %q", got, tt.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// 6. TestFormatUptime
// ---------------------------------------------------------------------------

func TestFormatUptime(t *testing.T) {
	tests := []struct {
		name string
		d    time.Duration
		want string
	}{
		{"hours", 2*time.Hour + 15*time.Minute, "2h 15m"},
		{"minutes_only", 5*time.Minute + 30*time.Second, "5m 30s"},
		{"zero", 0, "0m 0s"},
		{"one_hour_zero_min", 1 * time.Hour, "1h 0m"},
		{"seconds_only", 45 * time.Second, "0m 45s"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := formatUptime(tt.d)
			if got != tt.want {
				t.Errorf("formatUptime(%v) = %q, want %q", tt.d, got, tt.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// 7. TestPrintEntry_Raw
// ---------------------------------------------------------------------------

func TestPrintEntry_Raw(t *testing.T) {
	ts := time.Date(2025, 1, 15, 10, 30, 45, 0, time.UTC)
	entry := models.AgentLog{
		EngineID:  "engine-abc123",
		CarID:     "car-xyz",
		Direction: "recv",
		Content:   "raw log content here",
		CreatedAt: ts,
	}

	var buf bytes.Buffer
	printEntry(&buf, entry, true)

	out := buf.String()
	if !strings.Contains(out, "---") {
		t.Error("raw output should contain separator dashes")
	}
	if !strings.Contains(out, "engine-abc123") {
		t.Error("raw output should contain engine ID")
	}
	if !strings.Contains(out, "car-xyz") {
		t.Error("raw output should contain car ID")
	}
	if !strings.Contains(out, "recv") {
		t.Error("raw output should contain direction")
	}
	if !strings.Contains(out, "raw log content here") {
		t.Error("raw output should contain content")
	}
}

// ---------------------------------------------------------------------------
// 8. TestPrintEntry_NonRaw
// ---------------------------------------------------------------------------

func TestPrintEntry_NonRaw(t *testing.T) {
	jsonContent := `{"type":"assistant","message":{"content":[{"type":"text","text":"hello"}]}}` + "\n" +
		`{"type":"result","subtype":"done"}`
	entry := models.AgentLog{
		EngineID:  "engine-001",
		CarID:     "car-001",
		Direction: "send",
		Content:   jsonContent,
		CreatedAt: time.Now(),
	}

	var buf bytes.Buffer
	printEntry(&buf, entry, false)

	out := buf.String()
	if !strings.Contains(out, "assistant") {
		t.Errorf("non-raw output should contain formatted assistant line, got: %s", out)
	}
	if !strings.Contains(out, "result") {
		t.Errorf("non-raw output should contain formatted result line, got: %s", out)
	}
}

// ---------------------------------------------------------------------------
// 9. TestPrintWatchMessage
// ---------------------------------------------------------------------------

func TestPrintWatchMessage(t *testing.T) {
	ts := time.Date(2025, 6, 1, 14, 5, 9, 0, time.UTC)

	t.Run("normal_priority", func(t *testing.T) {
		msg := models.Message{
			FromAgent: "dispatch",
			ToAgent:   "engine-1",
			Subject:   "task assigned",
			Body:      "please handle car 42",
			Priority:  "normal",
			CreatedAt: ts,
		}
		var buf bytes.Buffer
		printWatchMessage(&buf, msg)
		out := buf.String()

		if !strings.Contains(out, "dispatch") {
			t.Errorf("should contain from agent, got: %s", out)
		}
		if !strings.Contains(out, "engine-1") {
			t.Errorf("should contain to agent, got: %s", out)
		}
		if !strings.Contains(out, "task assigned") {
			t.Errorf("should contain subject, got: %s", out)
		}
		if strings.Contains(out, "URGENT") {
			t.Errorf("normal priority should not show URGENT, got: %s", out)
		}
	})

	t.Run("urgent_priority", func(t *testing.T) {
		msg := models.Message{
			FromAgent: "yardmaster",
			ToAgent:   "engine-2",
			Subject:   "emergency stop",
			Body:      "halt immediately",
			Priority:  "urgent",
			CreatedAt: ts,
		}
		var buf bytes.Buffer
		printWatchMessage(&buf, msg)
		out := buf.String()

		if !strings.Contains(out, "URGENT") {
			t.Errorf("urgent priority should show URGENT, got: %s", out)
		}
		if !strings.Contains(out, "yardmaster") {
			t.Errorf("should contain from agent, got: %s", out)
		}
	})
}

// ---------------------------------------------------------------------------
// 10. TestBuildLogsQuery_NoFilters
// ---------------------------------------------------------------------------

func TestBuildLogsQuery_NoFilters(t *testing.T) {
	db := testGormDB(t)

	db.Create(&models.AgentLog{EngineID: "e1", CarID: "c1", SessionID: "s1", Direction: "send", Content: "a"})
	db.Create(&models.AgentLog{EngineID: "e2", CarID: "c2", SessionID: "s2", Direction: "recv", Content: "b"})

	q := buildLogsQuery(db, logsOpts{})
	var results []models.AgentLog
	if err := q.Find(&results).Error; err != nil {
		t.Fatalf("query error: %v", err)
	}
	if len(results) != 2 {
		t.Errorf("expected 2 results, got %d", len(results))
	}
}

// ---------------------------------------------------------------------------
// 11. TestBuildLogsQuery_EngineFilter
// ---------------------------------------------------------------------------

func TestBuildLogsQuery_EngineFilter(t *testing.T) {
	db := testGormDB(t)

	db.Create(&models.AgentLog{EngineID: "engine-a", CarID: "c1", Direction: "send", Content: "x"})
	db.Create(&models.AgentLog{EngineID: "engine-b", CarID: "c2", Direction: "recv", Content: "y"})
	db.Create(&models.AgentLog{EngineID: "engine-a", CarID: "c3", Direction: "send", Content: "z"})

	q := buildLogsQuery(db, logsOpts{engineID: "engine-a"})
	var results []models.AgentLog
	if err := q.Find(&results).Error; err != nil {
		t.Fatalf("query error: %v", err)
	}
	if len(results) != 2 {
		t.Errorf("expected 2 results for engine-a, got %d", len(results))
	}
	for _, r := range results {
		if r.EngineID != "engine-a" {
			t.Errorf("expected engine_id=engine-a, got %s", r.EngineID)
		}
	}
}

// ---------------------------------------------------------------------------
// 12. TestBuildLogsQuery_CarFilter
// ---------------------------------------------------------------------------

func TestBuildLogsQuery_CarFilter(t *testing.T) {
	db := testGormDB(t)

	db.Create(&models.AgentLog{EngineID: "e1", CarID: "target-car", Direction: "send", Content: "a"})
	db.Create(&models.AgentLog{EngineID: "e1", CarID: "other-car", Direction: "recv", Content: "b"})

	q := buildLogsQuery(db, logsOpts{carID: "target-car"})
	var results []models.AgentLog
	if err := q.Find(&results).Error; err != nil {
		t.Fatalf("query error: %v", err)
	}
	if len(results) != 1 {
		t.Errorf("expected 1 result for target-car, got %d", len(results))
	}
	if len(results) > 0 && results[0].CarID != "target-car" {
		t.Errorf("expected car_id=target-car, got %s", results[0].CarID)
	}
}

// ---------------------------------------------------------------------------
// 13. TestBuildLogsQuery_MultipleFilters
// ---------------------------------------------------------------------------

func TestBuildLogsQuery_MultipleFilters(t *testing.T) {
	db := testGormDB(t)

	db.Create(&models.AgentLog{EngineID: "e1", CarID: "c1", SessionID: "s1", Direction: "send", Content: "match"})
	db.Create(&models.AgentLog{EngineID: "e1", CarID: "c2", SessionID: "s1", Direction: "recv", Content: "no"})
	db.Create(&models.AgentLog{EngineID: "e2", CarID: "c1", SessionID: "s1", Direction: "send", Content: "no"})
	db.Create(&models.AgentLog{EngineID: "e1", CarID: "c1", SessionID: "s2", Direction: "send", Content: "no"})

	q := buildLogsQuery(db, logsOpts{engineID: "e1", carID: "c1"})
	var results []models.AgentLog
	if err := q.Find(&results).Error; err != nil {
		t.Fatalf("query error: %v", err)
	}
	if len(results) != 2 {
		t.Errorf("expected 2 results for e1+c1, got %d", len(results))
	}
	for _, r := range results {
		if r.EngineID != "e1" || r.CarID != "c1" {
			t.Errorf("unexpected result: engine=%s car=%s", r.EngineID, r.CarID)
		}
	}
}

// ---------------------------------------------------------------------------
// 14. TestBuildWatchQuery_SpecificAgent
// ---------------------------------------------------------------------------

func TestBuildWatchQuery_SpecificAgent(t *testing.T) {
	db := testGormDB(t)

	db.Create(&models.Message{FromAgent: "a", ToAgent: "target", Subject: "s1", Body: "b1", Priority: "normal"})
	db.Create(&models.Message{FromAgent: "a", ToAgent: "other", Subject: "s2", Body: "b2", Priority: "normal"})
	db.Create(&models.Message{FromAgent: "b", ToAgent: "target", Subject: "s3", Body: "b3", Priority: "urgent"})

	q := buildWatchQuery(db, "target", false)
	var results []models.Message
	if err := q.Find(&results).Error; err != nil {
		t.Fatalf("query error: %v", err)
	}
	if len(results) != 2 {
		t.Errorf("expected 2 messages for target, got %d", len(results))
	}
	for _, r := range results {
		if r.ToAgent != "target" {
			t.Errorf("expected to_agent=target, got %s", r.ToAgent)
		}
	}
}

// ---------------------------------------------------------------------------
// 15. TestBuildWatchQuery_All
// ---------------------------------------------------------------------------

func TestBuildWatchQuery_All(t *testing.T) {
	db := testGormDB(t)

	db.Create(&models.Message{FromAgent: "a", ToAgent: "agent1", Subject: "s1", Body: "b1", Priority: "normal"})
	db.Create(&models.Message{FromAgent: "b", ToAgent: "agent2", Subject: "s2", Body: "b2", Priority: "normal"})
	db.Create(&models.Message{FromAgent: "c", ToAgent: "agent3", Subject: "s3", Body: "b3", Priority: "urgent"})

	q := buildWatchQuery(db, "agent1", true)
	var results []models.Message
	if err := q.Find(&results).Error; err != nil {
		t.Fatalf("query error: %v", err)
	}
	if len(results) != 3 {
		t.Errorf("expected 3 messages with all=true, got %d", len(results))
	}
}
