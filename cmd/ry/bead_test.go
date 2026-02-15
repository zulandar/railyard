package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestBeadCmd_Help(t *testing.T) {
	cmd := newRootCmd()
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SetArgs([]string{"bead", "--help"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("bead --help failed: %v", err)
	}

	out := buf.String()
	if !strings.Contains(out, "Bead management") {
		t.Errorf("expected help to mention 'Bead management', got: %s", out)
	}
	for _, sub := range []string{"create", "list", "show", "update"} {
		if !strings.Contains(out, sub) {
			t.Errorf("expected help to list %q subcommand, got: %s", sub, out)
		}
	}
}

func TestNewBeadCmd(t *testing.T) {
	cmd := newBeadCmd()
	if cmd.Use != "bead" {
		t.Errorf("Use = %q, want %q", cmd.Use, "bead")
	}
	if !cmd.HasSubCommands() {
		t.Error("bead command should have subcommands")
	}
}

func TestBeadCreateCmd_Help(t *testing.T) {
	cmd := newRootCmd()
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SetArgs([]string{"bead", "create", "--help"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("bead create --help failed: %v", err)
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

func TestNewBeadCreateCmd(t *testing.T) {
	cmd := newBeadCreateCmd()
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

func TestBeadCreateCmd_MissingRequiredFlags(t *testing.T) {
	cmd := newRootCmd()
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	// Missing --title and --track
	cmd.SetArgs([]string{"bead", "create"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error for missing required flags")
	}
}

func TestBeadCreateCmd_MissingConfig(t *testing.T) {
	cmd := newRootCmd()
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SetArgs([]string{"bead", "create",
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

func TestBeadListCmd_Help(t *testing.T) {
	cmd := newRootCmd()
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SetArgs([]string{"bead", "list", "--help"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("bead list --help failed: %v", err)
	}

	out := buf.String()
	for _, flag := range []string{"--track", "--status", "--type", "--assignee"} {
		if !strings.Contains(out, flag) {
			t.Errorf("expected %s flag, got: %s", flag, out)
		}
	}
}

func TestNewBeadListCmd(t *testing.T) {
	cmd := newBeadListCmd()
	if cmd.Use != "list" {
		t.Errorf("Use = %q, want %q", cmd.Use, "list")
	}
	for _, name := range []string{"track", "status", "type", "assignee", "config"} {
		if cmd.Flags().Lookup(name) == nil {
			t.Errorf("expected --%s flag", name)
		}
	}
}

func TestBeadShowCmd_Help(t *testing.T) {
	cmd := newRootCmd()
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SetArgs([]string{"bead", "show", "--help"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("bead show --help failed: %v", err)
	}

	out := buf.String()
	if !strings.Contains(out, "full details of a bead") {
		t.Errorf("expected help to mention 'full details of a bead', got: %s", out)
	}
}

func TestNewBeadShowCmd(t *testing.T) {
	cmd := newBeadShowCmd()
	if cmd.Use != "show <id>" {
		t.Errorf("Use = %q, want %q", cmd.Use, "show <id>")
	}
}

func TestBeadShowCmd_NoArgs(t *testing.T) {
	cmd := newRootCmd()
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SetArgs([]string{"bead", "show"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error for missing args")
	}
}

func TestBeadUpdateCmd_Help(t *testing.T) {
	cmd := newRootCmd()
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SetArgs([]string{"bead", "update", "--help"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("bead update --help failed: %v", err)
	}

	out := buf.String()
	for _, flag := range []string{"--status", "--assignee", "--priority", "--description"} {
		if !strings.Contains(out, flag) {
			t.Errorf("expected %s flag, got: %s", flag, out)
		}
	}
}

func TestNewBeadUpdateCmd(t *testing.T) {
	cmd := newBeadUpdateCmd()
	if cmd.Use != "update <id>" {
		t.Errorf("Use = %q, want %q", cmd.Use, "update <id>")
	}
	for _, name := range []string{"status", "assignee", "priority", "description", "acceptance", "design", "config"} {
		if cmd.Flags().Lookup(name) == nil {
			t.Errorf("expected --%s flag", name)
		}
	}
}

func TestBeadUpdateCmd_NoArgs(t *testing.T) {
	cmd := newRootCmd()
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SetArgs([]string{"bead", "update"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error for missing args")
	}
}

func TestBeadUpdateCmd_NoFlags(t *testing.T) {
	cmd := newRootCmd()
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SetArgs([]string{"bead", "update", "be-12345"})

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

func TestRootCmd_HasBeadSubcommand(t *testing.T) {
	cmd := newRootCmd()
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetArgs([]string{"--help"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("root --help failed: %v", err)
	}

	if !strings.Contains(buf.String(), "bead") {
		t.Error("root help should list 'bead' subcommand")
	}
}
