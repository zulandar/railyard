package telegraph

import (
	"fmt"
	"strings"
	"testing"

	"github.com/zulandar/railyard/internal/models"
	"github.com/zulandar/railyard/internal/orchestration"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func openCommandTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	if err := db.AutoMigrate(
		&models.Car{},
		&models.CarDep{},
		&models.CarProgress{},
		&models.Engine{},
		&models.Message{},
		&models.Track{},
		&models.DispatchSession{},
		&models.TelegraphConversation{},
	); err != nil {
		t.Fatalf("auto migrate: %v", err)
	}
	return db
}

// --- NewCommandHandler tests ---

func TestNewCommandHandler_NilDB(t *testing.T) {
	_, err := NewCommandHandler(CommandHandlerOpts{})
	if err == nil {
		t.Fatal("expected error for nil db")
	}
}

func TestNewCommandHandler_Success(t *testing.T) {
	db := openCommandTestDB(t)
	ch, err := NewCommandHandler(CommandHandlerOpts{DB: db})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ch == nil {
		t.Fatal("expected non-nil handler")
	}
}

// --- parseCommand tests ---

func TestParseCommand(t *testing.T) {
	tests := []struct {
		input string
		want  []string
	}{
		{"!ry", nil},
		{"!ry status", []string{"status"}},
		{"!ry car list", []string{"car", "list"}},
		{"!ry car list --track backend", []string{"car", "list", "--track", "backend"}},
		{"!ry car show my-car-1", []string{"car", "show", "my-car-1"}},
		{"!ry engine list", []string{"engine", "list"}},
		{"!ry help", []string{"help"}},
		{"!ry  status", []string{"status"}}, // extra space
	}
	for _, tt := range tests {
		got := parseCommand(tt.input)
		if tt.want == nil && got != nil {
			t.Errorf("parseCommand(%q) = %v, want nil", tt.input, got)
			continue
		}
		if tt.want != nil && got == nil {
			t.Errorf("parseCommand(%q) = nil, want %v", tt.input, tt.want)
			continue
		}
		if len(got) != len(tt.want) {
			t.Errorf("parseCommand(%q) = %v, want %v", tt.input, got, tt.want)
			continue
		}
		for i := range got {
			if got[i] != tt.want[i] {
				t.Errorf("parseCommand(%q)[%d] = %q, want %q", tt.input, i, got[i], tt.want[i])
			}
		}
	}
}

// --- Execute tests ---

func TestExecute_Help(t *testing.T) {
	db := openCommandTestDB(t)
	ch, _ := NewCommandHandler(CommandHandlerOpts{DB: db})

	result := ch.Execute("!ry help")
	if !strings.Contains(result, "Railyard Commands") {
		t.Errorf("help should contain 'Railyard Commands', got %q", result)
	}
}

func TestExecute_BareCommand(t *testing.T) {
	db := openCommandTestDB(t)
	ch, _ := NewCommandHandler(CommandHandlerOpts{DB: db})

	result := ch.Execute("!ry")
	if !strings.Contains(result, "Railyard Commands") {
		t.Errorf("bare command should show help, got %q", result)
	}
}

func TestExecute_UnknownCommand(t *testing.T) {
	db := openCommandTestDB(t)
	ch, _ := NewCommandHandler(CommandHandlerOpts{DB: db})

	result := ch.Execute("!ry deploy")
	if !strings.Contains(result, "Unknown command") {
		t.Errorf("expected 'Unknown command', got %q", result)
	}
	if !strings.Contains(result, "deploy") {
		t.Errorf("error should mention the unknown command, got %q", result)
	}
}

// --- Status command ---

func TestExecute_Status(t *testing.T) {
	db := openCommandTestDB(t)
	sp := &mockStatusProvider{
		info: &orchestration.StatusInfo{
			Engines: []orchestration.EngineInfo{
				{ID: "eng-1", Status: "working", Track: "backend"},
			},
			TrackSummary: []orchestration.TrackSummary{
				{Track: "backend", InProgress: 1, Ready: 2, Done: 3},
			},
		},
	}
	ch, _ := NewCommandHandler(CommandHandlerOpts{DB: db, StatusProvider: sp})

	result := ch.Execute("!ry status")
	if !strings.Contains(result, "ENGINES") {
		t.Errorf("status should contain ENGINES, got %q", result)
	}
	if !strings.Contains(result, "eng-1") {
		t.Errorf("status should contain engine ID, got %q", result)
	}
}

func TestExecute_StatusError(t *testing.T) {
	db := openCommandTestDB(t)
	sp := &mockStatusProvider{err: fmt.Errorf("db down")}
	ch, _ := NewCommandHandler(CommandHandlerOpts{DB: db, StatusProvider: sp})

	result := ch.Execute("!ry status")
	if !strings.Contains(result, "Error") {
		t.Errorf("status error should contain 'Error', got %q", result)
	}
}

// --- Car list command ---

func TestExecute_CarList(t *testing.T) {
	db := openCommandTestDB(t)
	db.Create(&models.Car{ID: "car-1", Title: "First task", Status: "open", Track: "backend", Priority: 1})
	db.Create(&models.Car{ID: "car-2", Title: "Second task", Status: "in_progress", Track: "frontend", Priority: 2})

	ch, _ := NewCommandHandler(CommandHandlerOpts{DB: db})

	result := ch.Execute("!ry car list")
	if !strings.Contains(result, "car-1") {
		t.Errorf("car list should contain car-1, got %q", result)
	}
	if !strings.Contains(result, "car-2") {
		t.Errorf("car list should contain car-2, got %q", result)
	}
	if !strings.Contains(result, "Cars") {
		t.Errorf("car list should contain 'Cars' header, got %q", result)
	}
}

func TestExecute_CarListWithTrackFilter(t *testing.T) {
	db := openCommandTestDB(t)
	db.Create(&models.Car{ID: "car-1", Title: "Backend task", Status: "open", Track: "backend"})
	db.Create(&models.Car{ID: "car-2", Title: "Frontend task", Status: "open", Track: "frontend"})

	ch, _ := NewCommandHandler(CommandHandlerOpts{DB: db})

	result := ch.Execute("!ry car list --track backend")
	if !strings.Contains(result, "car-1") {
		t.Errorf("should contain backend car, got %q", result)
	}
	if strings.Contains(result, "car-2") {
		t.Errorf("should not contain frontend car, got %q", result)
	}
}

func TestExecute_CarListWithStatusFilter(t *testing.T) {
	db := openCommandTestDB(t)
	db.Create(&models.Car{ID: "car-1", Title: "Open", Status: "open", Track: "backend"})
	db.Create(&models.Car{ID: "car-2", Title: "Done", Status: "done", Track: "backend"})

	ch, _ := NewCommandHandler(CommandHandlerOpts{DB: db})

	result := ch.Execute("!ry car list --status open")
	if !strings.Contains(result, "car-1") {
		t.Errorf("should contain open car, got %q", result)
	}
	if strings.Contains(result, "car-2") {
		t.Errorf("should not contain done car, got %q", result)
	}
}

func TestExecute_CarListEmpty(t *testing.T) {
	db := openCommandTestDB(t)
	ch, _ := NewCommandHandler(CommandHandlerOpts{DB: db})

	result := ch.Execute("!ry car list")
	if !strings.Contains(result, "No cars found") {
		t.Errorf("empty list should say 'No cars found', got %q", result)
	}
}

func TestExecute_CarNoSubcommand(t *testing.T) {
	db := openCommandTestDB(t)
	ch, _ := NewCommandHandler(CommandHandlerOpts{DB: db})

	result := ch.Execute("!ry car")
	if !strings.Contains(result, "Usage") {
		t.Errorf("expected usage text, got %q", result)
	}
}

func TestExecute_CarUnknownSubcommand(t *testing.T) {
	db := openCommandTestDB(t)
	ch, _ := NewCommandHandler(CommandHandlerOpts{DB: db})

	result := ch.Execute("!ry car delete")
	if !strings.Contains(result, "Unknown car subcommand") {
		t.Errorf("expected error, got %q", result)
	}
}

// --- Car show command ---

func TestExecute_CarShow(t *testing.T) {
	db := openCommandTestDB(t)
	db.Create(&models.Car{
		ID:          "backend-42",
		Title:       "Implement auth",
		Status:      "in_progress",
		Track:       "backend",
		Priority:    1,
		Type:        "task",
		Assignee:    "alice",
		Branch:      "ry/alice/backend-42",
		Description: "Add JWT authentication",
	})

	ch, _ := NewCommandHandler(CommandHandlerOpts{DB: db})

	result := ch.Execute("!ry car show backend-42")
	if !strings.Contains(result, "backend-42") {
		t.Errorf("should contain car ID, got %q", result)
	}
	if !strings.Contains(result, "Implement auth") {
		t.Errorf("should contain title, got %q", result)
	}
	if !strings.Contains(result, "in_progress") {
		t.Errorf("should contain status, got %q", result)
	}
	if !strings.Contains(result, "alice") {
		t.Errorf("should contain assignee, got %q", result)
	}
	if !strings.Contains(result, "JWT") {
		t.Errorf("should contain description, got %q", result)
	}
}

func TestExecute_CarShowNotFound(t *testing.T) {
	db := openCommandTestDB(t)
	ch, _ := NewCommandHandler(CommandHandlerOpts{DB: db})

	result := ch.Execute("!ry car show nonexistent")
	if !strings.Contains(result, "Error") {
		t.Errorf("should contain error for missing car, got %q", result)
	}
}

func TestExecute_CarShowNoID(t *testing.T) {
	db := openCommandTestDB(t)
	ch, _ := NewCommandHandler(CommandHandlerOpts{DB: db})

	result := ch.Execute("!ry car show")
	if !strings.Contains(result, "Usage") {
		t.Errorf("expected usage text when no ID given, got %q", result)
	}
}

// --- Engine list command ---

func TestExecute_EngineList(t *testing.T) {
	db := openCommandTestDB(t)
	db.Create(&models.Engine{ID: "eng-1", Track: "backend", Status: "working", CurrentCar: "car-1"})
	db.Create(&models.Engine{ID: "eng-2", Track: "frontend", Status: "idle"})

	ch, _ := NewCommandHandler(CommandHandlerOpts{DB: db})

	result := ch.Execute("!ry engine list")
	if !strings.Contains(result, "eng-1") {
		t.Errorf("should contain eng-1, got %q", result)
	}
	if !strings.Contains(result, "eng-2") {
		t.Errorf("should contain eng-2, got %q", result)
	}
	if !strings.Contains(result, "Engines") {
		t.Errorf("should contain 'Engines' header, got %q", result)
	}
}

func TestExecute_EngineListExcludesDead(t *testing.T) {
	db := openCommandTestDB(t)
	db.Create(&models.Engine{ID: "eng-1", Track: "backend", Status: "working"})
	db.Create(&models.Engine{ID: "eng-dead", Track: "backend", Status: "dead"})

	ch, _ := NewCommandHandler(CommandHandlerOpts{DB: db})

	result := ch.Execute("!ry engine list")
	if !strings.Contains(result, "eng-1") {
		t.Errorf("should contain alive engine, got %q", result)
	}
	if strings.Contains(result, "eng-dead") {
		t.Errorf("should not contain dead engine, got %q", result)
	}
}

func TestExecute_EngineListEmpty(t *testing.T) {
	db := openCommandTestDB(t)
	ch, _ := NewCommandHandler(CommandHandlerOpts{DB: db})

	result := ch.Execute("!ry engine list")
	if !strings.Contains(result, "No active engines") {
		t.Errorf("expected 'No active engines', got %q", result)
	}
}

func TestExecute_EngineNoSubcommand(t *testing.T) {
	db := openCommandTestDB(t)
	ch, _ := NewCommandHandler(CommandHandlerOpts{DB: db})

	result := ch.Execute("!ry engine")
	if !strings.Contains(result, "Usage") {
		t.Errorf("expected usage text, got %q", result)
	}
}

func TestExecute_EngineUnknownSubcommand(t *testing.T) {
	db := openCommandTestDB(t)
	ch, _ := NewCommandHandler(CommandHandlerOpts{DB: db})

	result := ch.Execute("!ry engine restart")
	if !strings.Contains(result, "Usage") {
		t.Errorf("expected usage text for unknown subcommand, got %q", result)
	}
}

// --- Format function tests ---

func TestFormatCarTable(t *testing.T) {
	cars := []models.Car{
		{ID: "car-1", Title: "Short title", Status: "open", Track: "backend", Priority: 1},
		{ID: "car-2", Title: strings.Repeat("x", 50), Status: "done", Track: "frontend", Priority: 2},
	}

	result := formatCarTable(cars)
	if !strings.Contains(result, "Cars") {
		t.Error("should contain header")
	}
	if !strings.Contains(result, "car-1") {
		t.Error("should contain car-1")
	}
	// Long title should be truncated.
	if !strings.Contains(result, "...") {
		t.Error("long title should be truncated with ...")
	}
}

func TestFormatCarDetail(t *testing.T) {
	c := &models.Car{
		ID:          "car-1",
		Title:       "Test car",
		Status:      "open",
		Track:       "backend",
		Priority:    1,
		Type:        "task",
		Assignee:    "alice",
		Branch:      "ry/car-1",
		Description: "Some description",
	}

	result := formatCarDetail(c)
	if !strings.Contains(result, "car-1") {
		t.Error("should contain car ID")
	}
	if !strings.Contains(result, "Test car") {
		t.Error("should contain title")
	}
	if !strings.Contains(result, "alice") {
		t.Error("should contain assignee")
	}
	if !strings.Contains(result, "ry/car-1") {
		t.Error("should contain branch")
	}
	if !strings.Contains(result, "Some description") {
		t.Error("should contain description")
	}
}

func TestFormatCarDetail_MinimalFields(t *testing.T) {
	c := &models.Car{
		ID:     "car-1",
		Title:  "Minimal",
		Status: "open",
	}

	result := formatCarDetail(c)
	if !strings.Contains(result, "car-1") {
		t.Error("should contain car ID")
	}
	// Should not contain empty field labels.
	if strings.Contains(result, "Assignee") {
		t.Error("should not show Assignee when empty")
	}
	if strings.Contains(result, "Branch") {
		t.Error("should not show Branch when empty")
	}
}

func TestFormatEngineTable(t *testing.T) {
	engines := []models.Engine{
		{ID: "eng-1", Track: "backend", Status: "working", CurrentCar: "car-1"},
		{ID: "eng-2", Track: "frontend", Status: "idle"},
	}

	result := formatEngineTable(engines)
	if !strings.Contains(result, "Engines") {
		t.Error("should contain header")
	}
	if !strings.Contains(result, "eng-1") {
		t.Error("should contain eng-1")
	}
	if !strings.Contains(result, "car-1") {
		t.Error("should contain current car")
	}
	// Empty car should show "-".
	if !strings.Contains(result, "-") {
		t.Error("empty current car should show '-'")
	}
}
