package main

import (
	"bytes"
	"strings"
	"testing"
)

// --- yardmaster command tests ---

func TestYardmasterCmd_Help(t *testing.T) {
	cmd := newRootCmd()
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SetArgs([]string{"yardmaster", "--help"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("yardmaster --help failed: %v", err)
	}

	out := buf.String()
	if !strings.Contains(out, "supervisor") {
		t.Errorf("expected help to mention 'supervisor', got: %s", out)
	}
}

func TestYardmasterCmd_Flags(t *testing.T) {
	cmd := newYardmasterCmd()
	if cmd.Use != "yardmaster" {
		t.Errorf("Use = %q, want %q", cmd.Use, "yardmaster")
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

func TestYardmasterCmd_MissingConfig(t *testing.T) {
	cmd := newRootCmd()
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SetArgs([]string{"yardmaster", "--config", "/nonexistent/railyard.yaml"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error for missing config")
	}
	if !strings.Contains(err.Error(), "load config") {
		t.Errorf("error = %q, want to contain %q", err.Error(), "load config")
	}
}

func TestYardmasterCmd_LongDescription(t *testing.T) {
	cmd := newYardmasterCmd()
	if !strings.Contains(cmd.Long, "monitors engines") {
		t.Errorf("long description should mention 'monitors engines'")
	}
	if !strings.Contains(cmd.Long, "merges branches") {
		t.Errorf("long description should mention 'merges branches'")
	}
}

func TestRootCmd_HasYardmasterSubcommand(t *testing.T) {
	cmd := newRootCmd()
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetArgs([]string{"--help"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("root --help failed: %v", err)
	}

	if !strings.Contains(buf.String(), "yardmaster") {
		t.Error("root help should list 'yardmaster' subcommand")
	}
}

// --- switch command tests ---

func TestSwitchCmd_Help(t *testing.T) {
	cmd := newRootCmd()
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SetArgs([]string{"switch", "--help"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("switch --help failed: %v", err)
	}

	out := buf.String()
	if !strings.Contains(out, "merge") {
		t.Errorf("expected help to mention 'merge', got: %s", out)
	}
}

func TestSwitchCmd_Flags(t *testing.T) {
	cmd := newSwitchCmd()
	if cmd.Use != "switch <bead-id>" {
		t.Errorf("Use = %q, want %q", cmd.Use, "switch <bead-id>")
	}
	if cmd.Flags().Lookup("config") == nil {
		t.Error("expected --config flag")
	}
	if cmd.Flags().Lookup("dry-run") == nil {
		t.Error("expected --dry-run flag")
	}
	dryRunFlag := cmd.Flags().Lookup("dry-run")
	if dryRunFlag.DefValue != "false" {
		t.Errorf("--dry-run default = %q, want %q", dryRunFlag.DefValue, "false")
	}
}

func TestSwitchCmd_NoArgs(t *testing.T) {
	cmd := newRootCmd()
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SetArgs([]string{"switch"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error for missing args")
	}
}

func TestSwitchCmd_MissingConfig(t *testing.T) {
	cmd := newRootCmd()
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SetArgs([]string{"switch", "be-12345", "--config", "/nonexistent/railyard.yaml"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error for missing config")
	}
	if !strings.Contains(err.Error(), "load config") {
		t.Errorf("error = %q, want to contain %q", err.Error(), "load config")
	}
}

func TestRootCmd_HasSwitchSubcommand(t *testing.T) {
	cmd := newRootCmd()
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetArgs([]string{"--help"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("root --help failed: %v", err)
	}

	if !strings.Contains(buf.String(), "switch") {
		t.Error("root help should list 'switch' subcommand")
	}
}
