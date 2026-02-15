package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestDispatchCmd_Help(t *testing.T) {
	cmd := newRootCmd()
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SetArgs([]string{"dispatch", "--help"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("dispatch --help failed: %v", err)
	}

	out := buf.String()
	if !strings.Contains(out, "planner") {
		t.Errorf("expected help to mention 'planner', got: %s", out)
	}
}

func TestDispatchCmd_Flags(t *testing.T) {
	cmd := newDispatchCmd()
	if cmd.Use != "dispatch" {
		t.Errorf("Use = %q, want %q", cmd.Use, "dispatch")
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

func TestDispatchCmd_MissingConfig(t *testing.T) {
	cmd := newRootCmd()
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SetArgs([]string{"dispatch", "--config", "/nonexistent/railyard.yaml"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error for missing config file")
	}
	if !strings.Contains(err.Error(), "load config") {
		t.Errorf("error = %q, want to contain %q", err.Error(), "load config")
	}
}

func TestRootCmd_HasDispatchSubcommand(t *testing.T) {
	cmd := newRootCmd()
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetArgs([]string{"--help"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("root --help failed: %v", err)
	}

	if !strings.Contains(buf.String(), "dispatch") {
		t.Error("root help should list 'dispatch' subcommand")
	}
}

func TestDispatchCmd_LongDescription(t *testing.T) {
	cmd := newDispatchCmd()
	if !strings.Contains(cmd.Long, "interactive") {
		t.Errorf("expected long description to mention 'interactive', got: %s", cmd.Long)
	}
	if !strings.Contains(cmd.Long, "decompose") {
		t.Errorf("expected long description to mention 'decompose', got: %s", cmd.Long)
	}
}

func TestDispatchCmd_ShortDescription(t *testing.T) {
	cmd := newDispatchCmd()
	if !strings.Contains(cmd.Short, "Dispatch") {
		t.Errorf("expected short description to mention 'Dispatch', got: %s", cmd.Short)
	}
}
