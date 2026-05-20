package main

import (
	"bytes"
	"strings"
	"testing"
)

// --- watch command tests ---

func TestWatchCmd_Help(t *testing.T) {
	cmd := newRootCmd()
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SetArgs([]string{"watch", "--help"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("watch --help failed: %v", err)
	}

	out := buf.String()
	if !strings.Contains(out, "Polls for new messages") {
		t.Errorf("expected help to mention 'Polls for new messages', got: %s", out)
	}
	for _, flag := range []string{"--agent", "--all", "--config"} {
		if !strings.Contains(out, flag) {
			t.Errorf("expected %s flag in help, got: %s", flag, out)
		}
	}
}

func TestNewWatchCmd(t *testing.T) {
	cmd := newWatchCmd()
	if cmd.Use != "watch" {
		t.Errorf("Use = %q, want %q", cmd.Use, "watch")
	}

	for _, name := range []string{"agent", "all", "config"} {
		if cmd.Flags().Lookup(name) == nil {
			t.Errorf("expected --%s flag", name)
		}
	}

	agentFlag := cmd.Flags().Lookup("agent")
	if agentFlag.DefValue != "human" {
		t.Errorf("--agent default = %q, want %q", agentFlag.DefValue, "human")
	}

	cfgFlag := cmd.Flags().Lookup("config")
	if cfgFlag.DefValue != "railyard.yaml" {
		t.Errorf("--config default = %q, want %q", cfgFlag.DefValue, "railyard.yaml")
	}
	if cfgFlag.Shorthand != "c" {
		t.Errorf("--config shorthand = %q, want %q", cfgFlag.Shorthand, "c")
	}

	allFlag := cmd.Flags().Lookup("all")
	if allFlag.DefValue != "false" {
		t.Errorf("--all default = %q, want %q", allFlag.DefValue, "false")
	}
}

func TestWatchCmd_MissingConfig(t *testing.T) {
	cmd := newRootCmd()
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SetArgs([]string{"watch", "--config", "/nonexistent/railyard.yaml"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error for missing config file")
	}
	if !strings.Contains(err.Error(), "load config") {
		t.Errorf("error = %q, want to contain %q", err.Error(), "load config")
	}
}

func TestRootCmd_HasWatchSubcommand(t *testing.T) {
	cmd := newRootCmd()
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetArgs([]string{"--help"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("root --help failed: %v", err)
	}

	if !strings.Contains(buf.String(), "watch") {
		t.Error("root help should list 'watch' subcommand")
	}
}
