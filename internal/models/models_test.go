package models

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
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

func TestResolvedBlockerStatuses(t *testing.T) {
	expected := map[string]bool{"done": true, "cancelled": true, "merged": true}
	if len(ResolvedBlockerStatuses) != len(expected) {
		t.Fatalf("ResolvedBlockerStatuses has %d entries, want %d", len(ResolvedBlockerStatuses), len(expected))
	}
	for _, s := range ResolvedBlockerStatuses {
		if !expected[s] {
			t.Errorf("unexpected status in ResolvedBlockerStatuses: %q", s)
		}
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
	assertGormTag(t, typ, "Status", "default:draft")
	assertGormTag(t, typ, "Status", "index")
	assertGormTag(t, typ, "Priority", "default:2")
	assertGormTag(t, typ, "Track", "size:64")
	assertGormTag(t, typ, "Track", "index")
	assertGormTag(t, typ, "Assignee", "size:64")
	assertGormTag(t, typ, "ParentID", "size:32")
	assertGormTag(t, typ, "Branch", "size:128")
	assertGormTag(t, typ, "DesignNotes", "type:text")
	assertGormTag(t, typ, "Acceptance", "type:text")
	assertGormTag(t, typ, "SkipTests", "default:false")

	assertFieldType(t, typ, "ID", "string")
	assertFieldType(t, typ, "SkipTests", "bool")
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
	assertFieldType(t, typ, "InputTokens", "int")
	assertFieldType(t, typ, "OutputTokens", "int")
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
		CarID:     "car-001",
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
		CarID:        "car-001",
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
		CurrentCar:   "car-001",
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
		CarID:        "car-001",
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
		ID:           1,
		EngineID:     "eng-001",
		SessionID:    "sess-1",
		CarID:        "car-001",
		Direction:    "out",
		Content:      "response content",
		TokenCount:   500,
		InputTokens:  300,
		OutputTokens: 200,
		Model:        "claude-opus-4-6",
		LatencyMs:    1200,
	}
	if a.Direction != "out" {
		t.Errorf("Direction = %q, want %q", a.Direction, "out")
	}
	if a.InputTokens != 300 {
		t.Errorf("InputTokens = %d, want 300", a.InputTokens)
	}
	if a.OutputTokens != 200 {
		t.Errorf("OutputTokens = %d, want 200", a.OutputTokens)
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

func TestDispatchSession_Fields(t *testing.T) {
	typ := reflect.TypeOf(DispatchSession{})

	assertGormTag(t, typ, "ID", "primaryKey")
	assertGormTag(t, typ, "ID", "autoIncrement")
	assertGormTag(t, typ, "Source", "size:16")
	assertGormTag(t, typ, "Source", "not null")
	assertGormTag(t, typ, "Source", "index")
	assertGormTag(t, typ, "UserName", "size:64")
	assertGormTag(t, typ, "UserName", "not null")
	assertGormTag(t, typ, "PlatformThreadID", "size:128")
	assertGormTag(t, typ, "PlatformThreadID", "idx_thread_channel")
	assertGormTag(t, typ, "ChannelID", "size:128")
	assertGormTag(t, typ, "ChannelID", "idx_thread_channel")
	assertGormTag(t, typ, "Status", "size:16")
	assertGormTag(t, typ, "Status", "default:active")
	assertGormTag(t, typ, "Status", "index")
	assertGormTag(t, typ, "CarsCreated", "type:json")
	assertGormTag(t, typ, "LastHeartbeat", "index")

	assertFieldType(t, typ, "ID", "uint")
	assertFieldType(t, typ, "Source", "string")
	assertFieldType(t, typ, "UserName", "string")
	assertFieldType(t, typ, "LastHeartbeat", "time.Time")
	assertFieldType(t, typ, "CreatedAt", "time.Time")
	assertFieldType(t, typ, "CompletedAt", "*time.Time")
}

func TestDispatchSession_Relations(t *testing.T) {
	typ := reflect.TypeOf(DispatchSession{})

	assertGormTag(t, typ, "Conversations", "foreignKey:SessionID")
	assertFieldType(t, typ, "Conversations", "[]models.TelegraphConversation")
}

func TestDispatchSession_Instantiation(t *testing.T) {
	now := time.Now()
	ds := DispatchSession{
		ID:               1,
		Source:           "telegraph",
		UserName:         "alice",
		PlatformThreadID: "thread-123",
		ChannelID:        "C01ABC",
		Status:           "active",
		CarsCreated:      `["car-001","car-002"]`,
		LastHeartbeat:    now,
		CreatedAt:        now,
		CompletedAt:      nil,
	}
	if ds.Source != "telegraph" {
		t.Errorf("Source = %q, want %q", ds.Source, "telegraph")
	}
	if ds.CompletedAt != nil {
		t.Error("CompletedAt should be nil for active session")
	}
}

func TestTelegraphConversation_Fields(t *testing.T) {
	typ := reflect.TypeOf(TelegraphConversation{})

	assertGormTag(t, typ, "ID", "primaryKey")
	assertGormTag(t, typ, "ID", "autoIncrement")
	assertGormTag(t, typ, "SessionID", "not null")
	assertGormTag(t, typ, "SessionID", "index")
	assertGormTag(t, typ, "Sequence", "not null")
	assertGormTag(t, typ, "Role", "size:16")
	assertGormTag(t, typ, "Role", "not null")
	assertGormTag(t, typ, "UserName", "size:64")
	assertGormTag(t, typ, "Content", "type:mediumtext")
	assertGormTag(t, typ, "Content", "not null")
	assertGormTag(t, typ, "PlatformMsgID", "size:128")
	assertGormTag(t, typ, "CarsReferenced", "type:json")

	assertFieldType(t, typ, "ID", "uint")
	assertFieldType(t, typ, "SessionID", "uint")
	assertFieldType(t, typ, "Sequence", "int")
	assertFieldType(t, typ, "Role", "string")
	assertFieldType(t, typ, "Content", "string")
	assertFieldType(t, typ, "CreatedAt", "time.Time")
}

func TestTelegraphConversation_Relations(t *testing.T) {
	typ := reflect.TypeOf(TelegraphConversation{})

	assertGormTag(t, typ, "Session", "foreignKey:SessionID")
	assertFieldType(t, typ, "Session", "models.DispatchSession")
}

func TestTelegraphConversation_Instantiation(t *testing.T) {
	now := time.Now()
	tc := TelegraphConversation{
		ID:             1,
		SessionID:      42,
		Sequence:       1,
		Role:           "user",
		UserName:       "alice",
		Content:        "create a task for fixing the login bug",
		PlatformMsgID:  "msg-456",
		CarsReferenced: `["car-001"]`,
		CreatedAt:      now,
	}
	if tc.SessionID != 42 {
		t.Errorf("SessionID = %d, want 42", tc.SessionID)
	}
	if tc.Role != "user" {
		t.Errorf("Role = %q, want %q", tc.Role, "user")
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

// ---------------------------------------------------------------------------
// CRUD tests — verify AutoMigrate creates tables and basic operations work
// ---------------------------------------------------------------------------

func openTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	if err := db.AutoMigrate(&DispatchSession{}, &TelegraphConversation{}); err != nil {
		t.Fatalf("migrate test db: %v", err)
	}
	return db
}

func TestDispatchSession_CRUD(t *testing.T) {
	db := openTestDB(t)
	now := time.Now()

	// Create
	session := DispatchSession{
		Source:           "telegraph",
		UserName:         "alice",
		PlatformThreadID: "thread-100",
		ChannelID:        "C01ABC",
		Status:           "active",
		CarsCreated:      `["car-001"]`,
		LastHeartbeat:    now,
	}
	if err := db.Create(&session).Error; err != nil {
		t.Fatalf("Create: %v", err)
	}
	if session.ID == 0 {
		t.Fatal("expected auto-increment ID to be set")
	}

	// Read
	var found DispatchSession
	if err := db.First(&found, session.ID).Error; err != nil {
		t.Fatalf("First: %v", err)
	}
	if found.Source != "telegraph" {
		t.Errorf("Source = %q, want %q", found.Source, "telegraph")
	}
	if found.UserName != "alice" {
		t.Errorf("UserName = %q, want %q", found.UserName, "alice")
	}

	// Update
	completed := time.Now()
	if err := db.Model(&found).Updates(map[string]interface{}{
		"status":       "completed",
		"completed_at": completed,
	}).Error; err != nil {
		t.Fatalf("Update: %v", err)
	}
	var updated DispatchSession
	db.First(&updated, session.ID)
	if updated.Status != "completed" {
		t.Errorf("Status = %q, want %q", updated.Status, "completed")
	}

	// Delete
	if err := db.Delete(&DispatchSession{}, session.ID).Error; err != nil {
		t.Fatalf("Delete: %v", err)
	}
	result := db.First(&DispatchSession{}, session.ID)
	if result.Error == nil {
		t.Fatal("expected record not found after delete")
	}
}

func TestTelegraphConversation_CRUD(t *testing.T) {
	db := openTestDB(t)
	now := time.Now()

	// Create parent session first.
	session := DispatchSession{
		Source:        "local",
		UserName:      "bob",
		Status:        "active",
		LastHeartbeat: now,
	}
	db.Create(&session)

	// Create conversation message.
	conv := TelegraphConversation{
		SessionID:      session.ID,
		Sequence:       1,
		Role:           "user",
		UserName:       "bob",
		Content:        "please create a bug fix car",
		PlatformMsgID:  "msg-789",
		CarsReferenced: `[]`,
	}
	if err := db.Create(&conv).Error; err != nil {
		t.Fatalf("Create: %v", err)
	}
	if conv.ID == 0 {
		t.Fatal("expected auto-increment ID to be set")
	}

	// Read
	var found TelegraphConversation
	if err := db.First(&found, conv.ID).Error; err != nil {
		t.Fatalf("First: %v", err)
	}
	if found.Role != "user" {
		t.Errorf("Role = %q, want %q", found.Role, "user")
	}
	if found.SessionID != session.ID {
		t.Errorf("SessionID = %d, want %d", found.SessionID, session.ID)
	}

	// Update
	if err := db.Model(&found).Update("content", "updated content").Error; err != nil {
		t.Fatalf("Update: %v", err)
	}
	var updated TelegraphConversation
	db.First(&updated, conv.ID)
	if updated.Content != "updated content" {
		t.Errorf("Content = %q, want %q", updated.Content, "updated content")
	}

	// Delete
	if err := db.Delete(&TelegraphConversation{}, conv.ID).Error; err != nil {
		t.Fatalf("Delete: %v", err)
	}
	result := db.First(&TelegraphConversation{}, conv.ID)
	if result.Error == nil {
		t.Fatal("expected record not found after delete")
	}
}

func TestDispatchSession_PreloadConversations(t *testing.T) {
	db := openTestDB(t)
	now := time.Now()

	session := DispatchSession{
		Source:        "telegraph",
		UserName:      "carol",
		Status:        "active",
		LastHeartbeat: now,
	}
	db.Create(&session)

	// Create two conversation messages.
	for i := 1; i <= 2; i++ {
		db.Create(&TelegraphConversation{
			SessionID: session.ID,
			Sequence:  i,
			Role:      "user",
			Content:   "message " + strings.Repeat("x", i),
		})
	}

	// Preload conversations.
	var loaded DispatchSession
	if err := db.Preload("Conversations").First(&loaded, session.ID).Error; err != nil {
		t.Fatalf("Preload: %v", err)
	}
	if len(loaded.Conversations) != 2 {
		t.Fatalf("Conversations count = %d, want 2", len(loaded.Conversations))
	}
	if loaded.Conversations[0].Sequence != 1 {
		t.Errorf("first conversation Sequence = %d, want 1", loaded.Conversations[0].Sequence)
	}
}
