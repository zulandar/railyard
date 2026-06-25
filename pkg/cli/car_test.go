package cli

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/zulandar/railyard/internal/models"
)

func TestCarCmd_Help(t *testing.T) {
	cmd := newRootCmd()
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SetArgs([]string{"car", "--help"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("car --help failed: %v", err)
	}

	out := buf.String()
	if !strings.Contains(out, "Car management") {
		t.Errorf("expected help to mention 'Car management', got: %s", out)
	}
	for _, sub := range []string{"create", "list", "show", "update", "children", "search", "remember", "memories", "forget"} {
		if !strings.Contains(out, sub) {
			t.Errorf("expected help to list %q subcommand, got: %s", sub, out)
		}
	}
}

func TestNewCarCmd(t *testing.T) {
	cmd := newCarCmd()
	if cmd.Use != "car" {
		t.Errorf("Use = %q, want %q", cmd.Use, "car")
	}
	if !cmd.HasSubCommands() {
		t.Error("car command should have subcommands")
	}
}

func TestCarCreateCmd_Help(t *testing.T) {
	cmd := newRootCmd()
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SetArgs([]string{"car", "create", "--help"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("car create --help failed: %v", err)
	}

	out := buf.String()
	if !strings.Contains(out, "--title") {
		t.Errorf("expected --title flag, got: %s", out)
	}
	if !strings.Contains(out, "--track") {
		t.Errorf("expected --track flag, got: %s", out)
	}
	if !strings.Contains(out, "--type") {
		t.Errorf("expected --type flag, got: %s", out)
	}
	if !strings.Contains(out, "--priority") {
		t.Errorf("expected --priority flag, got: %s", out)
	}
	if !strings.Contains(out, "--config") {
		t.Errorf("expected --config flag, got: %s", out)
	}
}

func TestNewCarCreateCmd(t *testing.T) {
	cmd := newCarCreateCmd()
	if cmd.Use != "create" {
		t.Errorf("Use = %q, want %q", cmd.Use, "create")
	}

	// Check required flags
	flag := cmd.Flags().Lookup("title")
	if flag == nil {
		t.Fatal("expected --title flag")
	}
	flag = cmd.Flags().Lookup("track")
	if flag == nil {
		t.Fatal("expected --track flag")
	}

	// Check defaults
	typeFlag := cmd.Flags().Lookup("type")
	if typeFlag == nil {
		t.Fatal("expected --type flag")
	}
	if typeFlag.DefValue != "task" {
		t.Errorf("--type default = %q, want %q", typeFlag.DefValue, "task")
	}

	priFlag := cmd.Flags().Lookup("priority")
	if priFlag == nil {
		t.Fatal("expected --priority flag")
	}
	if priFlag.DefValue != "2" {
		t.Errorf("--priority default = %q, want %q", priFlag.DefValue, "2")
	}

	cfgFlag := cmd.Flags().Lookup("config")
	if cfgFlag == nil {
		t.Fatal("expected --config flag")
	}
	if cfgFlag.DefValue != "railyard.yaml" {
		t.Errorf("--config default = %q, want %q", cfgFlag.DefValue, "railyard.yaml")
	}
	if cfgFlag.Shorthand != "c" {
		t.Errorf("--config shorthand = %q, want %q", cfgFlag.Shorthand, "c")
	}
}

func TestCarCreateCmd_ParentFlag(t *testing.T) {
	cmd := newCarCreateCmd()
	flag := cmd.Flags().Lookup("parent")
	if flag == nil {
		t.Fatal("expected --parent flag")
	}
	if flag.DefValue != "" {
		t.Errorf("--parent default = %q, want empty", flag.DefValue)
	}
}

func TestCarCreateCmd_MissingRequiredFlags(t *testing.T) {
	cmd := newRootCmd()
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	// Missing --title and --track
	cmd.SetArgs([]string{"car", "create"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error for missing required flags")
	}
}

func TestCarCreateCmd_MissingConfig(t *testing.T) {
	cmd := newRootCmd()
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SetArgs([]string{"car", "create",
		"--title", "test",
		"--track", "backend",
		"--config", "/nonexistent/railyard.yaml",
	})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error for missing config file")
	}
	if !strings.Contains(err.Error(), "load config") {
		t.Errorf("error = %q, want to contain %q", err.Error(), "load config")
	}
}

func TestCarListCmd_Help(t *testing.T) {
	cmd := newRootCmd()
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SetArgs([]string{"car", "list", "--help"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("car list --help failed: %v", err)
	}

	out := buf.String()
	for _, flag := range []string{"--track", "--status", "--type", "--assignee"} {
		if !strings.Contains(out, flag) {
			t.Errorf("expected %s flag, got: %s", flag, out)
		}
	}
}

func TestNewCarListCmd(t *testing.T) {
	cmd := newCarListCmd()
	if cmd.Use != "list" {
		t.Errorf("Use = %q, want %q", cmd.Use, "list")
	}
	for _, name := range []string{"track", "status", "type", "assignee", "config"} {
		if cmd.Flags().Lookup(name) == nil {
			t.Errorf("expected --%s flag", name)
		}
	}
}

func TestCarShowCmd_Help(t *testing.T) {
	cmd := newRootCmd()
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SetArgs([]string{"car", "show", "--help"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("car show --help failed: %v", err)
	}

	out := buf.String()
	if !strings.Contains(out, "full details of a car") {
		t.Errorf("expected help to mention 'full details of a car', got: %s", out)
	}
}

func TestNewCarShowCmd(t *testing.T) {
	cmd := newCarShowCmd()
	if cmd.Use != "show <id>" {
		t.Errorf("Use = %q, want %q", cmd.Use, "show <id>")
	}
}

func TestCarShowCmd_NoArgs(t *testing.T) {
	cmd := newRootCmd()
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SetArgs([]string{"car", "show"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error for missing args")
	}
}

func TestCarUpdateCmd_Help(t *testing.T) {
	cmd := newRootCmd()
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SetArgs([]string{"car", "update", "--help"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("car update --help failed: %v", err)
	}

	out := buf.String()
	for _, flag := range []string{"--status", "--assignee", "--priority", "--description"} {
		if !strings.Contains(out, flag) {
			t.Errorf("expected %s flag, got: %s", flag, out)
		}
	}
}

func TestNewCarUpdateCmd(t *testing.T) {
	cmd := newCarUpdateCmd()
	if cmd.Use != "update <id>" {
		t.Errorf("Use = %q, want %q", cmd.Use, "update <id>")
	}
	for _, name := range []string{"status", "assignee", "priority", "description", "acceptance", "design", "config"} {
		if cmd.Flags().Lookup(name) == nil {
			t.Errorf("expected --%s flag", name)
		}
	}
}

func TestCarUpdateCmd_NoArgs(t *testing.T) {
	cmd := newRootCmd()
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SetArgs([]string{"car", "update"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error for missing args")
	}
}

func TestCarUpdateCmd_NoFlags(t *testing.T) {
	cmd := newRootCmd()
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SetArgs([]string{"car", "update", "car-12345"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error for no update flags")
	}
	if !strings.Contains(err.Error(), "no fields to update") {
		t.Errorf("error = %q, want to contain %q", err.Error(), "no fields to update")
	}
}

func TestTruncate(t *testing.T) {
	tests := []struct {
		input  string
		maxLen int
		want   string
	}{
		{"short", 10, "short"},
		{"exactly10!", 10, "exactly10!"},
		{"this is way too long for the limit", 15, "this is way ..."},
		{"abc", 3, "abc"},
	}
	for _, tt := range tests {
		got := truncate(tt.input, tt.maxLen)
		if tt.maxLen >= 3 && got != tt.want {
			t.Errorf("truncate(%q, %d) = %q, want %q", tt.input, tt.maxLen, got, tt.want)
		}
	}
}

// --- dep command tests ---

func TestCarDepCmd_Help(t *testing.T) {
	cmd := newRootCmd()
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SetArgs([]string{"car", "dep", "--help"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("car dep --help failed: %v", err)
	}

	out := buf.String()
	for _, sub := range []string{"add", "list", "remove"} {
		if !strings.Contains(out, sub) {
			t.Errorf("expected dep help to list %q subcommand, got: %s", sub, out)
		}
	}
}

func TestNewCarDepCmd(t *testing.T) {
	cmd := newCarDepCmd()
	if cmd.Use != "dep" {
		t.Errorf("Use = %q, want %q", cmd.Use, "dep")
	}
	if !cmd.HasSubCommands() {
		t.Error("dep command should have subcommands")
	}
}

func TestCarDepAddCmd_Flags(t *testing.T) {
	cmd := newCarDepAddCmd()
	if cmd.Use != "add <car-id>" {
		t.Errorf("Use = %q, want %q", cmd.Use, "add <car-id>")
	}
	if cmd.Flags().Lookup("blocked-by") == nil {
		t.Error("expected --blocked-by flag")
	}
	if cmd.Flags().Lookup("type") == nil {
		t.Error("expected --type flag")
	}
	typeFlag := cmd.Flags().Lookup("type")
	if typeFlag.DefValue != "blocks" {
		t.Errorf("--type default = %q, want %q", typeFlag.DefValue, "blocks")
	}
}

func TestCarDepAddCmd_NoArgs(t *testing.T) {
	cmd := newRootCmd()
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SetArgs([]string{"car", "dep", "add"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error for missing args")
	}
}

func TestCarDepAddCmd_MissingBlockedBy(t *testing.T) {
	cmd := newRootCmd()
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SetArgs([]string{"car", "dep", "add", "car-12345"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error for missing --blocked-by")
	}
}

func TestCarDepListCmd_Flags(t *testing.T) {
	cmd := newCarDepListCmd()
	if cmd.Use != "list <car-id>" {
		t.Errorf("Use = %q, want %q", cmd.Use, "list <car-id>")
	}
}

func TestCarDepListCmd_NoArgs(t *testing.T) {
	cmd := newRootCmd()
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SetArgs([]string{"car", "dep", "list"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error for missing args")
	}
}

func TestCarDepRemoveCmd_Flags(t *testing.T) {
	cmd := newCarDepRemoveCmd()
	if cmd.Use != "remove <car-id>" {
		t.Errorf("Use = %q, want %q", cmd.Use, "remove <car-id>")
	}
	if cmd.Flags().Lookup("blocked-by") == nil {
		t.Error("expected --blocked-by flag")
	}
}

func TestCarDepRemoveCmd_NoArgs(t *testing.T) {
	cmd := newRootCmd()
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SetArgs([]string{"car", "dep", "remove"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error for missing args")
	}
}

// --- ready command tests ---

func TestCarReadyCmd_Help(t *testing.T) {
	cmd := newRootCmd()
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SetArgs([]string{"car", "ready", "--help"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("car ready --help failed: %v", err)
	}

	out := buf.String()
	if !strings.Contains(out, "ready") {
		t.Errorf("expected help to mention 'ready', got: %s", out)
	}
	if !strings.Contains(out, "--track") {
		t.Errorf("expected --track flag, got: %s", out)
	}
}

func TestNewCarReadyCmd(t *testing.T) {
	cmd := newCarReadyCmd()
	if cmd.Use != "ready" {
		t.Errorf("Use = %q, want %q", cmd.Use, "ready")
	}
	if cmd.Flags().Lookup("track") == nil {
		t.Error("expected --track flag")
	}
}

// --- children command tests ---

func TestCarChildrenCmd_Help(t *testing.T) {
	cmd := newRootCmd()
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SetArgs([]string{"car", "children", "--help"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("car children --help failed: %v", err)
	}

	out := buf.String()
	if !strings.Contains(out, "children") {
		t.Errorf("expected help to mention 'children', got: %s", out)
	}
}

func TestCarChildrenCmd_Flags(t *testing.T) {
	cmd := newCarChildrenCmd()
	if cmd.Use != "children <parent-id>" {
		t.Errorf("Use = %q, want %q", cmd.Use, "children <parent-id>")
	}
	if cmd.Flags().Lookup("config") == nil {
		t.Error("expected --config flag")
	}
}

func TestCarChildrenCmd_NoArgs(t *testing.T) {
	cmd := newRootCmd()
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SetArgs([]string{"car", "children"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error for missing args")
	}
}

func TestRootCmd_HasCarSubcommand(t *testing.T) {
	cmd := newRootCmd()
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetArgs([]string{"--help"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("root --help failed: %v", err)
	}

	if !strings.Contains(buf.String(), "car") {
		t.Error("root help should list 'car' subcommand")
	}
}

func TestHasMultipleBaseBranches_AllMain(t *testing.T) {
	cars := []models.Car{
		{BaseBranch: "main"},
		{BaseBranch: ""}, // empty defaults to "main"
		{BaseBranch: "main"},
	}
	if hasMultipleBaseBranches(cars) {
		t.Error("expected false when all cars target main")
	}
}

func TestHasMultipleBaseBranches_Mixed(t *testing.T) {
	cars := []models.Car{
		{BaseBranch: "main"},
		{BaseBranch: "develop"},
	}
	if !hasMultipleBaseBranches(cars) {
		t.Error("expected true when cars target different branches")
	}
}

func TestHasMultipleBaseBranches_Empty(t *testing.T) {
	if hasMultipleBaseBranches(nil) {
		t.Error("expected false for empty slice")
	}
}

func TestHasMultipleBaseBranches_Single(t *testing.T) {
	cars := []models.Car{{BaseBranch: "develop"}}
	if hasMultipleBaseBranches(cars) {
		t.Error("expected false for single car")
	}
}

// TestRunCarCreate_UnknownTrack: a typo'd track produces a car no engine can
// ever claim (engines filter strictly by track =) and nothing sweeps it.
// Create must reject tracks not present in the config (railyard-d5f).
func TestRunCarCreate_UnknownTrack(t *testing.T) {
	gormDB := mockTestDB(t)
	cleanup := withMockDB(t, gormDB)
	defer cleanup()

	_, err := execCmd(t, []string{"car", "create", "--title", "typo", "--track", "bakend", "--config", "test.yaml"})
	if err == nil {
		t.Fatal("expected error for unknown track")
	}
	if !strings.Contains(err.Error(), "bakend") || !strings.Contains(err.Error(), "backend") {
		t.Errorf("error should name the bad track and list valid ones, got: %v", err)
	}

	var count int64
	gormDB.Model(&models.Car{}).Count(&count)
	if count != 0 {
		t.Errorf("cars created = %d, want 0", count)
	}
}

// TestRunCarCreate_KnownTrack: configured tracks still work.
func TestRunCarCreate_KnownTrack(t *testing.T) {
	gormDB := mockTestDB(t)
	cleanup := withMockDB(t, gormDB)
	defer cleanup()

	out, err := execCmd(t, []string{"car", "create", "--title", "ok", "--track", "backend", "--config", "test.yaml"})
	if err != nil {
		t.Fatalf("unexpected error: %v\n%s", err, out)
	}
}

// --- remember / memories / forget command tests ---

func TestCarRememberCmd_Help(t *testing.T) {
	cmd := newRootCmd()
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SetArgs([]string{"car", "remember", "--help"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("car remember --help failed: %v", err)
	}

	out := buf.String()
	if !strings.Contains(out, "remember") {
		t.Errorf("expected help to mention 'remember', got: %s", out)
	}
	if !strings.Contains(out, "--config") {
		t.Errorf("expected --config flag, got: %s", out)
	}
}

func TestNewCarRememberCmd(t *testing.T) {
	cmd := newCarRememberCmd()
	if cmd.Use != "remember <car-id> <keyword> <content>" {
		t.Errorf("Use = %q, want %q", cmd.Use, "remember <car-id> <keyword> <content>")
	}
	if cmd.Flags().Lookup("config") == nil {
		t.Error("expected --config flag")
	}
}

func TestCarRememberCmd_NoArgs(t *testing.T) {
	cmd := newRootCmd()
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SetArgs([]string{"car", "remember"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error for missing args")
	}
}

func TestCarRememberCmd_TooFewArgs(t *testing.T) {
	cmd := newRootCmd()
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SetArgs([]string{"car", "remember", "car-001"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error for too few args")
	}
}

func TestCarRememberCmd_MissingConfig(t *testing.T) {
	cmd := newRootCmd()
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SetArgs([]string{"car", "remember", "car-001", "key", "value", "--config", "/nonexistent/railyard.yaml"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error for missing config file")
	}
	if !strings.Contains(err.Error(), "load config") {
		t.Errorf("error = %q, want to contain %q", err.Error(), "load config")
	}
}

func TestCarMemoriesCmd_Help(t *testing.T) {
	cmd := newRootCmd()
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SetArgs([]string{"car", "memories", "--help"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("car memories --help failed: %v", err)
	}

	out := buf.String()
	if !strings.Contains(out, "memories") {
		t.Errorf("expected help to mention 'memories', got: %s", out)
	}
	if !strings.Contains(out, "--keyword") {
		t.Errorf("expected --keyword flag, got: %s", out)
	}
	if !strings.Contains(out, "--config") {
		t.Errorf("expected --config flag, got: %s", out)
	}
}

func TestNewCarMemoriesCmd(t *testing.T) {
	cmd := newCarMemoriesCmd()
	if cmd.Use != "memories <car-id> [keyword]" {
		t.Errorf("Use = %q, want %q", cmd.Use, "memories <car-id> [keyword]")
	}
	if cmd.Flags().Lookup("keyword") == nil {
		t.Error("expected --keyword flag")
	}
	if cmd.Flags().Lookup("config") == nil {
		t.Error("expected --config flag")
	}
	kwFlag := cmd.Flags().Lookup("keyword")
	if kwFlag.Shorthand != "k" {
		t.Errorf("--keyword shorthand = %q, want %q", kwFlag.Shorthand, "k")
	}
}

func TestCarMemoriesCmd_NoArgs(t *testing.T) {
	cmd := newRootCmd()
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SetArgs([]string{"car", "memories"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error for missing args")
	}
}

func TestCarMemoriesCmd_TooManyArgs(t *testing.T) {
	cmd := newRootCmd()
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SetArgs([]string{"car", "memories", "car-001", "color", "extra"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error for too many args")
	}
}

func TestCarForgetCmd_Help(t *testing.T) {
	cmd := newRootCmd()
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SetArgs([]string{"car", "forget", "--help"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("car forget --help failed: %v", err)
	}

	out := buf.String()
	if !strings.Contains(out, "forget") {
		t.Errorf("expected help to mention 'forget', got: %s", out)
	}
	if !strings.Contains(out, "--config") {
		t.Errorf("expected --config flag, got: %s", out)
	}
}

func TestNewCarForgetCmd(t *testing.T) {
	cmd := newCarForgetCmd()
	if cmd.Use != "forget <car-id> <keyword>" {
		t.Errorf("Use = %q, want %q", cmd.Use, "forget <car-id> <keyword>")
	}
	if cmd.Flags().Lookup("config") == nil {
		t.Error("expected --config flag")
	}
}

func TestCarForgetCmd_NoArgs(t *testing.T) {
	cmd := newRootCmd()
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SetArgs([]string{"car", "forget"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error for missing args")
	}
}

func TestCarForgetCmd_TooFewArgs(t *testing.T) {
	cmd := newRootCmd()
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SetArgs([]string{"car", "forget", "car-001"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error for too few args")
	}
}

// --- Integration tests (in-memory DB) ---

func TestRunCarRemember_Success(t *testing.T) {
	gormDB := mockTestDB(t)
	cleanup := withMockDB(t, gormDB)
	defer cleanup()

	// Create a car first.
	now := time.Now()
	gormDB.Create(&models.Car{ID: "car-mem1", Title: "Memory Test", Status: "open", Track: "backend", CreatedAt: now, UpdatedAt: now})

	out, err := execCmd(t, []string{"car", "remember", "car-mem1", "author", "Alice", "--config", "test.yaml"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "Remembered") {
		t.Errorf("expected 'Remembered', got: %s", out)
	}

	// Verify DB.
	var mem models.CarMemory
	if err := gormDB.Where("car_id = ? AND keyword = ?", "car-mem1", "author").First(&mem).Error; err != nil {
		t.Fatalf("find memory: %v", err)
	}
	if mem.Content != "Alice" {
		t.Errorf("Content = %q, want %q", mem.Content, "Alice")
	}
}

func TestRunCarRemember_Upsert(t *testing.T) {
	gormDB := mockTestDB(t)
	cleanup := withMockDB(t, gormDB)
	defer cleanup()

	now := time.Now()
	gormDB.Create(&models.Car{ID: "car-mem2", Title: "Upsert Test", Status: "open", Track: "backend", CreatedAt: now, UpdatedAt: now})

	// First insert.
	_, err := execCmd(t, []string{"car", "remember", "car-mem2", "color", "blue", "--config", "test.yaml"})
	if err != nil {
		t.Fatalf("first remember: %v", err)
	}

	// Second insert — upsert.
	_, err = execCmd(t, []string{"car", "remember", "car-mem2", "color", "red", "--config", "test.yaml"})
	if err != nil {
		t.Fatalf("second remember: %v", err)
	}

	var mem models.CarMemory
	gormDB.Where("car_id = ? AND keyword = ?", "car-mem2", "color").First(&mem)
	if mem.Content != "red" {
		t.Errorf("Content = %q, want %q (upserted)", mem.Content, "red")
	}

	// Only one row.
	var count int64
	gormDB.Model(&models.CarMemory{}).Where("car_id = ? AND keyword = ?", "car-mem2", "color").Count(&count)
	if count != 1 {
		t.Errorf("row count = %d, want 1", count)
	}
}

func TestRunCarRemember_CarNotFound(t *testing.T) {
	gormDB := mockTestDB(t)
	cleanup := withMockDB(t, gormDB)
	defer cleanup()

	_, err := execCmd(t, []string{"car", "remember", "car-nonexistent", "key", "value", "--config", "test.yaml"})
	if err == nil {
		t.Fatal("expected error for nonexistent car")
	}
}

func TestRunCarMemories_ListAll(t *testing.T) {
	gormDB := mockTestDB(t)
	cleanup := withMockDB(t, gormDB)
	defer cleanup()

	now := time.Now()
	gormDB.Create(&models.Car{ID: "car-ml1", Title: "List Test", Status: "open", Track: "backend", CreatedAt: now, UpdatedAt: now})
	gormDB.Create(&models.CarMemory{CarID: "car-ml1", Track: "backend", Keyword: "author", Content: "Bob"})
	gormDB.Create(&models.CarMemory{CarID: "car-ml1", Track: "backend", Keyword: "color", Content: "green"})

	out, err := execCmd(t, []string{"car", "memories", "car-ml1", "--config", "test.yaml"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	for _, want := range []string{"author", "Bob", "color", "green", "KEYWORD", "CONTENT"} {
		if !strings.Contains(out, want) {
			t.Errorf("expected output to contain %q, got:\n%s", want, out)
		}
	}
}

func TestRunCarMemories_FilterByKeywordFlag(t *testing.T) {
	gormDB := mockTestDB(t)
	cleanup := withMockDB(t, gormDB)
	defer cleanup()

	now := time.Now()
	gormDB.Create(&models.Car{ID: "car-ml2", Title: "Filter Test", Status: "open", Track: "backend", CreatedAt: now, UpdatedAt: now})
	gormDB.Create(&models.CarMemory{CarID: "car-ml2", Track: "backend", Keyword: "author", Content: "Bob"})
	gormDB.Create(&models.CarMemory{CarID: "car-ml2", Track: "backend", Keyword: "color", Content: "green"})

	out, err := execCmd(t, []string{"car", "memories", "car-ml2", "--keyword", "author", "--config", "test.yaml"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(out, "author") {
		t.Errorf("expected output to contain 'author', got:\n%s", out)
	}
	if strings.Contains(out, "color") {
		t.Errorf("expected output NOT to contain 'color', got:\n%s", out)
	}
}

func TestRunCarMemories_FilterByKeywordArg(t *testing.T) {
	gormDB := mockTestDB(t)
	cleanup := withMockDB(t, gormDB)
	defer cleanup()

	now := time.Now()
	gormDB.Create(&models.Car{ID: "car-ml3", Title: "Arg Filter", Status: "open", Track: "backend", CreatedAt: now, UpdatedAt: now})
	gormDB.Create(&models.CarMemory{CarID: "car-ml3", Track: "backend", Keyword: "author", Content: "Bob"})
	gormDB.Create(&models.CarMemory{CarID: "car-ml3", Track: "backend", Keyword: "color", Content: "green"})

	out, err := execCmd(t, []string{"car", "memories", "car-ml3", "author", "--config", "test.yaml"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(out, "author") {
		t.Errorf("expected output to contain 'author', got:\n%s", out)
	}
	if strings.Contains(out, "color") {
		t.Errorf("expected output NOT to contain 'color', got:\n%s", out)
	}
}

func TestRunCarMemories_NoMemories(t *testing.T) {
	gormDB := mockTestDB(t)
	cleanup := withMockDB(t, gormDB)
	defer cleanup()

	now := time.Now()
	gormDB.Create(&models.Car{ID: "car-ml4", Title: "No Memories", Status: "open", Track: "backend", CreatedAt: now, UpdatedAt: now})

	out, err := execCmd(t, []string{"car", "memories", "car-ml4", "--config", "test.yaml"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "No memories found") {
		t.Errorf("expected 'No memories found', got:\n%s", out)
	}
}

func TestRunCarMemories_CarNotFound(t *testing.T) {
	gormDB := mockTestDB(t)
	cleanup := withMockDB(t, gormDB)
	defer cleanup()

	_, err := execCmd(t, []string{"car", "memories", "car-nonexistent", "--config", "test.yaml"})
	if err == nil {
		t.Fatal("expected error for nonexistent car")
	}
}

func TestRunCarForget_Success(t *testing.T) {
	gormDB := mockTestDB(t)
	cleanup := withMockDB(t, gormDB)
	defer cleanup()

	now := time.Now()
	gormDB.Create(&models.Car{ID: "car-mf1", Title: "Forget Test", Status: "open", Track: "backend", CreatedAt: now, UpdatedAt: now})
	gormDB.Create(&models.CarMemory{CarID: "car-mf1", Track: "backend", Keyword: "temp", Content: "delete me"})

	out, err := execCmd(t, []string{"car", "forget", "car-mf1", "temp", "--config", "test.yaml"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "Forgot memory") {
		t.Errorf("expected 'Forgot memory', got: %s", out)
	}

	// Verify DB deletion.
	var count int64
	gormDB.Model(&models.CarMemory{}).Where("car_id = ? AND keyword = ?", "car-mf1", "temp").Count(&count)
	if count != 0 {
		t.Errorf("row count = %d, want 0", count)
	}
}

func TestRunCarForget_NotFound(t *testing.T) {
	gormDB := mockTestDB(t)
	cleanup := withMockDB(t, gormDB)
	defer cleanup()

	now := time.Now()
	gormDB.Create(&models.Car{ID: "car-mf2", Title: "Forget NotFound", Status: "open", Track: "backend", CreatedAt: now, UpdatedAt: now})

	_, err := execCmd(t, []string{"car", "forget", "car-mf2", "nonexistent", "--config", "test.yaml"})
	if err == nil {
		t.Fatal("expected error for nonexistent memory")
	}
}

func TestRunCarForget_CarNotFound(t *testing.T) {
	gormDB := mockTestDB(t)
	cleanup := withMockDB(t, gormDB)
	defer cleanup()

	_, err := execCmd(t, []string{"car", "forget", "car-nonexistent", "key", "--config", "test.yaml"})
	if err == nil {
		t.Fatal("expected error for nonexistent car")
	}
}
