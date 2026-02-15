package main

import (
	"bytes"
	"strings"
	"testing"
)

// --- engine command tests ---

func TestEngineCmd_Help(t *testing.T) {
	cmd := newRootCmd()
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SetArgs([]string{"engine", "--help"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("engine --help failed: %v", err)
	}

	out := buf.String()
	if !strings.Contains(out, "Engine daemon") {
		t.Errorf("expected help to mention 'Engine daemon', got: %s", out)
	}
	if !strings.Contains(out, "start") {
		t.Errorf("expected help to list 'start' subcommand, got: %s", out)
	}
}

func TestNewEngineCmd(t *testing.T) {
	cmd := newEngineCmd()
	if cmd.Use != "engine" {
		t.Errorf("Use = %q, want %q", cmd.Use, "engine")
	}
	if !cmd.HasSubCommands() {
		t.Error("engine command should have subcommands")
	}
}

// --- engine start command tests ---

func TestEngineStartCmd_Help(t *testing.T) {
	cmd := newRootCmd()
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SetArgs([]string{"engine", "start", "--help"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("engine start --help failed: %v", err)
	}

	out := buf.String()
	if !strings.Contains(out, "daemon") {
		t.Errorf("expected help to mention 'daemon', got: %s", out)
	}
}

func TestEngineStartCmd_Flags(t *testing.T) {
	cmd := newEngineStartCmd()
	if cmd.Use != "start" {
		t.Errorf("Use = %q, want %q", cmd.Use, "start")
	}

	for _, flagName := range []string{"config", "track", "poll-interval"} {
		if cmd.Flags().Lookup(flagName) == nil {
			t.Errorf("expected --%s flag", flagName)
		}
	}
}

func TestEngineStartCmd_TrackRequired(t *testing.T) {
	cmd := newRootCmd()
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	// Missing --track flag
	cmd.SetArgs([]string{"engine", "start"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error for missing --track flag")
	}
}

func TestRootCmd_HasEngineSubcommand(t *testing.T) {
	cmd := newRootCmd()
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetArgs([]string{"--help"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("root --help failed: %v", err)
	}

	if !strings.Contains(buf.String(), "engine") {
		t.Error("root help should list 'engine' subcommand")
	}
}
