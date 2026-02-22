package main

import (
	"bytes"
	"strings"
	"testing"

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
	for _, sub := range []string{"create", "list", "show", "update", "children"} {
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
		{BaseBranch: ""},  // empty defaults to "main"
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
