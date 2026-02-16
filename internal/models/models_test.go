package models

import (
	"reflect"
	"strings"
	"testing"
	"time"
)

// gormTag extracts the gorm tag from a struct field.
func gormTag(t *testing.T, typ reflect.Type, fieldName string) string {
	t.Helper()
	f, ok := typ.FieldByName(fieldName)
	if !ok {
		t.Fatalf("%s.%s: field not found", typ.Name(), fieldName)
	}
	return f.Tag.Get("gorm")
}

// assertGormTag checks that a struct field's gorm tag contains the expected value.
func assertGormTag(t *testing.T, typ reflect.Type, fieldName, expected string) {
	t.Helper()
	tag := gormTag(t, typ, fieldName)
	if !strings.Contains(tag, expected) {
		t.Errorf("%s.%s gorm tag = %q, want to contain %q", typ.Name(), fieldName, tag, expected)
	}
}

// assertFieldType checks that a struct field has the expected Go type.
func assertFieldType(t *testing.T, typ reflect.Type, fieldName, expectedType string) {
	t.Helper()
	f, ok := typ.FieldByName(fieldName)
	if !ok {
		t.Fatalf("%s.%s: field not found", typ.Name(), fieldName)
	}
	got := f.Type.String()
	if got != expectedType {
		t.Errorf("%s.%s type = %q, want %q", typ.Name(), fieldName, got, expectedType)
	}
}

func TestCar_Fields(t *testing.T) {
	typ := reflect.TypeOf(Car{})

	assertGormTag(t, typ, "ID", "primaryKey")
	assertGormTag(t, typ, "ID", "size:32")
	assertGormTag(t, typ, "Title", "not null")
	assertGormTag(t, typ, "Description", "type:text")
	assertGormTag(t, typ, "Type", "size:16")
	assertGormTag(t, typ, "Type", "default:task")
	assertGormTag(t, typ, "Status", "size:16")
	assertGormTag(t, typ, "Status", "default:open")
	assertGormTag(t, typ, "Status", "index")
	assertGormTag(t, typ, "Priority", "default:2")
	assertGormTag(t, typ, "Track", "size:64")
	assertGormTag(t, typ, "Track", "index")
	assertGormTag(t, typ, "Assignee", "size:64")
	assertGormTag(t, typ, "ParentID", "size:32")
	assertGormTag(t, typ, "Branch", "size:128")
	assertGormTag(t, typ, "DesignNotes", "type:text")
	assertGormTag(t, typ, "Acceptance", "type:text")

	assertFieldType(t, typ, "ID", "string")
	assertFieldType(t, typ, "ParentID", "*string")
	assertFieldType(t, typ, "CreatedAt", "time.Time")
	assertFieldType(t, typ, "UpdatedAt", "time.Time")
	assertFieldType(t, typ, "ClaimedAt", "*time.Time")
	assertFieldType(t, typ, "CompletedAt", "*time.Time")
}

func TestCar_Relations(t *testing.T) {
	typ := reflect.TypeOf(Car{})

	assertGormTag(t, typ, "Parent", "foreignKey:ParentID")
	assertGormTag(t, typ, "Children", "foreignKey:ParentID")
	assertGormTag(t, typ, "Deps", "foreignKey:CarID")
	assertGormTag(t, typ, "Progress", "foreignKey:CarID")

	assertFieldType(t, typ, "Parent", "*models.Car")
	assertFieldType(t, typ, "Children", "[]models.Car")
	assertFieldType(t, typ, "Deps", "[]models.CarDep")
	assertFieldType(t, typ, "Progress", "[]models.CarProgress")
}

func TestCarDep_Fields(t *testing.T) {
	typ := reflect.TypeOf(CarDep{})

	// Composite primary key
	assertGormTag(t, typ, "CarID", "primaryKey")
	assertGormTag(t, typ, "CarID", "size:32")
	assertGormTag(t, typ, "BlockedBy", "primaryKey")
	assertGormTag(t, typ, "BlockedBy", "size:32")
	assertGormTag(t, typ, "DepType", "size:16")
	assertGormTag(t, typ, "DepType", "default:blocks")

	// Foreign key relations
	assertGormTag(t, typ, "Car", "foreignKey:CarID")
	assertGormTag(t, typ, "Blocker", "foreignKey:BlockedBy")
}

func TestCarProgress_Fields(t *testing.T) {
	typ := reflect.TypeOf(CarProgress{})

	assertGormTag(t, typ, "ID", "primaryKey")
	assertGormTag(t, typ, "ID", "autoIncrement")
	assertGormTag(t, typ, "CarID", "size:32")
	assertGormTag(t, typ, "CarID", "index")
	assertGormTag(t, typ, "SessionID", "size:64")
	assertGormTag(t, typ, "EngineID", "size:64")
	assertGormTag(t, typ, "Note", "type:text")
	assertGormTag(t, typ, "FilesChanged", "type:json")
	assertGormTag(t, typ, "CommitHash", "size:40")

	assertFieldType(t, typ, "ID", "uint")
	assertFieldType(t, typ, "Cycle", "int")
	assertFieldType(t, typ, "CreatedAt", "time.Time")
}

func TestTrack_Fields(t *testing.T) {
	typ := reflect.TypeOf(Track{})

	assertGormTag(t, typ, "Name", "primaryKey")
	assertGormTag(t, typ, "Name", "size:64")
	assertGormTag(t, typ, "Language", "size:32")
	assertGormTag(t, typ, "Conventions", "type:json")
	assertGormTag(t, typ, "SystemPrompt", "type:text")
	assertGormTag(t, typ, "FilePatterns", "type:json")
	assertGormTag(t, typ, "EngineSlots", "default:3")
	assertGormTag(t, typ, "Active", "default:true")

	assertFieldType(t, typ, "Name", "string")
	assertFieldType(t, typ, "EngineSlots", "int")
	assertFieldType(t, typ, "Active", "bool")
}

func TestEngine_Fields(t *testing.T) {
	typ := reflect.TypeOf(Engine{})

	assertGormTag(t, typ, "ID", "primaryKey")
	assertGormTag(t, typ, "ID", "size:64")
	assertGormTag(t, typ, "VMID", "size:64")
	assertGormTag(t, typ, "Track", "size:64")
	assertGormTag(t, typ, "Track", "index")
	assertGormTag(t, typ, "Role", "size:16")
	assertGormTag(t, typ, "Status", "size:16")
	assertGormTag(t, typ, "Status", "index")
	assertGormTag(t, typ, "CurrentCar", "size:32")
	assertGormTag(t, typ, "SessionID", "size:64")
	assertGormTag(t, typ, "LastActivity", "index")

	assertFieldType(t, typ, "StartedAt", "time.Time")
	assertFieldType(t, typ, "LastActivity", "time.Time")
}

func TestMessage_Fields(t *testing.T) {
	typ := reflect.TypeOf(Message{})

	assertGormTag(t, typ, "ID", "primaryKey")
	assertGormTag(t, typ, "ID", "autoIncrement")
	assertGormTag(t, typ, "FromAgent", "size:64")
	assertGormTag(t, typ, "FromAgent", "not null")
	assertGormTag(t, typ, "ToAgent", "size:64")
	assertGormTag(t, typ, "ToAgent", "not null")
	assertGormTag(t, typ, "ToAgent", "index")
	assertGormTag(t, typ, "CarID", "size:32")
	assertGormTag(t, typ, "Subject", "size:256")
	assertGormTag(t, typ, "Body", "type:text")
	assertGormTag(t, typ, "Priority", "size:8")
	assertGormTag(t, typ, "Priority", "default:normal")
	assertGormTag(t, typ, "Acknowledged", "default:false")
	assertGormTag(t, typ, "Acknowledged", "index")

	assertFieldType(t, typ, "ID", "uint")
	assertFieldType(t, typ, "ThreadID", "*uint")
	assertFieldType(t, typ, "CreatedAt", "time.Time")
}

func TestAgentLog_Fields(t *testing.T) {
	typ := reflect.TypeOf(AgentLog{})

	assertGormTag(t, typ, "ID", "primaryKey")
	assertGormTag(t, typ, "ID", "autoIncrement")
	assertGormTag(t, typ, "EngineID", "size:64")
	assertGormTag(t, typ, "EngineID", "idx_engine_session")
	assertGormTag(t, typ, "SessionID", "size:64")
	assertGormTag(t, typ, "SessionID", "idx_engine_session")
	assertGormTag(t, typ, "CarID", "size:32")
	assertGormTag(t, typ, "CarID", "index")
	assertGormTag(t, typ, "Direction", "size:4")
	assertGormTag(t, typ, "Content", "type:mediumtext")
	assertGormTag(t, typ, "Model", "size:64")

	assertFieldType(t, typ, "ID", "uint")
	assertFieldType(t, typ, "TokenCount", "int")
	assertFieldType(t, typ, "LatencyMs", "int")
	assertFieldType(t, typ, "CreatedAt", "time.Time")
}

func TestRailyardConfig_Fields(t *testing.T) {
	typ := reflect.TypeOf(RailyardConfig{})

	assertGormTag(t, typ, "ID", "primaryKey")
	assertGormTag(t, typ, "ID", "autoIncrement")
	assertGormTag(t, typ, "Owner", "size:64")
	assertGormTag(t, typ, "Owner", "uniqueIndex")
	assertGormTag(t, typ, "RepoURL", "type:text")
	assertGormTag(t, typ, "RepoURL", "not null")
	assertGormTag(t, typ, "Mode", "size:16")
	assertGormTag(t, typ, "Mode", "default:local")
	assertGormTag(t, typ, "Settings", "type:json")

	assertFieldType(t, typ, "ID", "uint")
	assertFieldType(t, typ, "Owner", "string")
}

func TestReindexJob_Fields(t *testing.T) {
	typ := reflect.TypeOf(ReindexJob{})

	assertGormTag(t, typ, "ID", "primaryKey")
	assertGormTag(t, typ, "ID", "autoIncrement")
	assertGormTag(t, typ, "Track", "size:64")
	assertGormTag(t, typ, "Track", "not null")
	assertGormTag(t, typ, "TriggerCommit", "size:40")
	assertGormTag(t, typ, "Status", "size:16")
	assertGormTag(t, typ, "Status", "default:pending")
	assertGormTag(t, typ, "GPUBoxID", "size:64")
	assertGormTag(t, typ, "ErrorMessage", "type:text")

	assertFieldType(t, typ, "ID", "uint")
	assertFieldType(t, typ, "FilesChanged", "int")
	assertFieldType(t, typ, "ChunksUpdated", "int")
	assertFieldType(t, typ, "StartedAt", "*time.Time")
	assertFieldType(t, typ, "CompletedAt", "*time.Time")
	assertFieldType(t, typ, "CreatedAt", "time.Time")
}

func TestCar_Instantiation(t *testing.T) {
	parentID := "parent-001"
	now := time.Now()
	b := Car{
		ID:          "car-abc12",
		Title:       "Test car",
		Description: "A test car",
		Type:        "task",
		Status:      "open",
		Priority:    2,
		Track:       "backend",
		Assignee:    "engine-1",
		ParentID:    &parentID,
		Branch:      "ry/alice/backend/car-abc12",
		DesignNotes: "design notes",
		Acceptance:  "it works",
		CreatedAt:   now,
		UpdatedAt:   now,
		ClaimedAt:   &now,
		CompletedAt: &now,
	}
	if b.ID != "car-abc12" {
		t.Errorf("ID = %q, want %q", b.ID, "car-abc12")
	}
	if *b.ParentID != "parent-001" {
		t.Errorf("ParentID = %q, want %q", *b.ParentID, "parent-001")
	}
}

func TestCarDep_Instantiation(t *testing.T) {
	d := CarDep{
		CarID:    "car-001",
		BlockedBy: "car-002",
		DepType:   "blocks",
	}
	if d.CarID != "car-001" {
		t.Errorf("CarID = %q, want %q", d.CarID, "car-001")
	}
	if d.BlockedBy != "car-002" {
		t.Errorf("BlockedBy = %q, want %q", d.BlockedBy, "car-002")
	}
}

func TestCarProgress_Instantiation(t *testing.T) {
	p := CarProgress{
		ID:           1,
		CarID:       "car-001",
		Cycle:        3,
		SessionID:    "sess-1",
		EngineID:     "eng-1",
		Note:         "implemented the handler",
		FilesChanged: `["cmd/main.go"]`,
		CommitHash:   "abc1234567890abcdef1234567890abcdef123456",
	}
	if p.Cycle != 3 {
		t.Errorf("Cycle = %d, want 3", p.Cycle)
	}
}

func TestTrack_Instantiation(t *testing.T) {
	tr := Track{
		Name:         "backend",
		Language:     "go",
		Conventions:  `{"go_version": "1.22"}`,
		SystemPrompt: "You are a Go backend engineer.",
		FilePatterns: `["cmd/**", "internal/**"]`,
		EngineSlots:  5,
		Active:       true,
	}
	if tr.Name != "backend" {
		t.Errorf("Name = %q, want %q", tr.Name, "backend")
	}
	if !tr.Active {
		t.Error("Active = false, want true")
	}
}

func TestEngine_Instantiation(t *testing.T) {
	now := time.Now()
	e := Engine{
		ID:           "eng-001",
		VMID:         "vm-abc",
		Track:        "backend",
		Role:         "engine",
		Status:       "idle",
		CurrentCar:  "car-001",
		SessionID:    "sess-1",
		StartedAt:    now,
		LastActivity: now,
	}
	if e.Role != "engine" {
		t.Errorf("Role = %q, want %q", e.Role, "engine")
	}
}

func TestMessage_Instantiation(t *testing.T) {
	threadID := uint(1)
	m := Message{
		ID:           1,
		FromAgent:    "engine-1",
		ToAgent:      "yardmaster",
		CarID:       "car-001",
		ThreadID:     &threadID,
		Subject:      "Need help",
		Body:         "Stuck on merge conflict",
		Priority:     "high",
		Acknowledged: false,
	}
	if m.ToAgent != "yardmaster" {
		t.Errorf("ToAgent = %q, want %q", m.ToAgent, "yardmaster")
	}
	if *m.ThreadID != 1 {
		t.Errorf("ThreadID = %d, want 1", *m.ThreadID)
	}
}

func TestAgentLog_Instantiation(t *testing.T) {
	a := AgentLog{
		ID:         1,
		EngineID:   "eng-001",
		SessionID:  "sess-1",
		CarID:     "car-001",
		Direction:  "out",
		Content:    "response content",
		TokenCount: 500,
		Model:      "claude-opus-4-6",
		LatencyMs:  1200,
	}
	if a.Direction != "out" {
		t.Errorf("Direction = %q, want %q", a.Direction, "out")
	}
}

func TestRailyardConfig_Instantiation(t *testing.T) {
	rc := RailyardConfig{
		ID:       1,
		Owner:    "alice",
		RepoURL:  "git@github.com:org/app.git",
		Mode:     "local",
		Settings: `{"max_engines": 10}`,
	}
	if rc.Owner != "alice" {
		t.Errorf("Owner = %q, want %q", rc.Owner, "alice")
	}
}

func TestReindexJob_Instantiation(t *testing.T) {
	now := time.Now()
	rj := ReindexJob{
		ID:            1,
		Track:         "backend",
		TriggerCommit: "abc123",
		Status:        "pending",
		FilesChanged:  10,
		ChunksUpdated: 5,
		GPUBoxID:      "gpu-1",
		StartedAt:     &now,
		CompletedAt:   nil,
		ErrorMessage:  "",
	}
	if rj.FilesChanged != 10 {
		t.Errorf("FilesChanged = %d, want 10", rj.FilesChanged)
	}
	if rj.CompletedAt != nil {
		t.Error("CompletedAt should be nil for pending job")
	}
}
