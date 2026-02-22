package main

import (
	"bytes"
	"strings"
	"testing"
)

// --- start command tests ---

func TestStartCmd_Help(t *testing.T) {
	cmd := newRootCmd()
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SetArgs([]string{"start", "--help"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("start --help failed: %v", err)
	}

	out := buf.String()
	if !strings.Contains(out, "tmux") {
		t.Errorf("expected help to mention 'tmux', got: %s", out)
	}
}

func TestStartCmd_Flags(t *testing.T) {
	cmd := newStartCmd()
	if cmd.Use != "start" {
		t.Errorf("Use = %q, want %q", cmd.Use, "start")
	}
	if cmd.Flags().Lookup("config") == nil {
		t.Error("expected --config flag")
	}
	if cmd.Flags().Lookup("engines") == nil {
		t.Error("expected --engines flag")
	}
	engFlag := cmd.Flags().Lookup("engines")
	if engFlag.DefValue != "0" {
		t.Errorf("--engines default = %q, want %q", engFlag.DefValue, "0")
	}
	tgFlag := cmd.Flags().Lookup("telegraph")
	if tgFlag == nil {
		t.Fatal("expected --telegraph flag")
	}
	if tgFlag.DefValue != "false" {
		t.Errorf("--telegraph default = %q, want %q", tgFlag.DefValue, "false")
	}
}

func TestStartCmd_MissingConfig(t *testing.T) {
	cmd := newRootCmd()
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SetArgs([]string{"start", "--config", "/nonexistent/railyard.yaml"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error for missing config")
	}
	if !strings.Contains(err.Error(), "load config") {
		t.Errorf("error = %q, want to contain %q", err.Error(), "load config")
	}
}

func TestRootCmd_HasStartSubcommand(t *testing.T) {
	cmd := newRootCmd()
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetArgs([]string{"--help"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("root --help failed: %v", err)
	}

	if !strings.Contains(buf.String(), "start") {
		t.Error("root help should list 'start' subcommand")
	}
}

// --- stop command tests ---

func TestStopCmd_Help(t *testing.T) {
	cmd := newRootCmd()
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SetArgs([]string{"stop", "--help"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("stop --help failed: %v", err)
	}

	out := buf.String()
	if !strings.Contains(out, "shutdown") {
		t.Errorf("expected help to mention 'shutdown', got: %s", out)
	}
}

func TestStopCmd_Flags(t *testing.T) {
	cmd := newStopCmd()
	if cmd.Use != "stop" {
		t.Errorf("Use = %q, want %q", cmd.Use, "stop")
	}
	if cmd.Flags().Lookup("config") == nil {
		t.Error("expected --config flag")
	}
	if cmd.Flags().Lookup("timeout") == nil {
		t.Error("expected --timeout flag")
	}
	timeoutFlag := cmd.Flags().Lookup("timeout")
	if timeoutFlag.DefValue != "1m0s" {
		t.Errorf("--timeout default = %q, want %q", timeoutFlag.DefValue, "1m0s")
	}
}

func TestStopCmd_MissingConfig(t *testing.T) {
	cmd := newRootCmd()
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SetArgs([]string{"stop", "--config", "/nonexistent/railyard.yaml"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error for missing config")
	}
	if !strings.Contains(err.Error(), "load config") {
		t.Errorf("error = %q, want to contain %q", err.Error(), "load config")
	}
}

func TestRootCmd_HasStopSubcommand(t *testing.T) {
	cmd := newRootCmd()
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetArgs([]string{"--help"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("root --help failed: %v", err)
	}

	if !strings.Contains(buf.String(), "stop") {
		t.Error("root help should list 'stop' subcommand")
	}
}

// --- status command tests ---

func TestStatusCmd_Help(t *testing.T) {
	cmd := newRootCmd()
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SetArgs([]string{"status", "--help"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("status --help failed: %v", err)
	}

	out := buf.String()
	if !strings.Contains(out, "dashboard") {
		t.Errorf("expected help to mention 'dashboard', got: %s", out)
	}
}

func TestStatusCmd_Flags(t *testing.T) {
	cmd := newStatusCmd()
	if cmd.Use != "status" {
		t.Errorf("Use = %q, want %q", cmd.Use, "status")
	}
	if cmd.Flags().Lookup("config") == nil {
		t.Error("expected --config flag")
	}
	if cmd.Flags().Lookup("watch") == nil {
		t.Error("expected --watch flag")
	}
	watchFlag := cmd.Flags().Lookup("watch")
	if watchFlag.DefValue != "false" {
		t.Errorf("--watch default = %q, want %q", watchFlag.DefValue, "false")
	}
}

func TestStatusCmd_MissingConfig(t *testing.T) {
	cmd := newRootCmd()
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SetArgs([]string{"status", "--config", "/nonexistent/railyard.yaml"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error for missing config")
	}
	if !strings.Contains(err.Error(), "load config") {
		t.Errorf("error = %q, want to contain %q", err.Error(), "load config")
	}
}

func TestRootCmd_HasStatusSubcommand(t *testing.T) {
	cmd := newRootCmd()
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetArgs([]string{"--help"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("root --help failed: %v", err)
	}

	if !strings.Contains(buf.String(), "status") {
		t.Error("root help should list 'status' subcommand")
	}
}

// --- engine scale command tests ---

func TestEngineScaleCmd_Help(t *testing.T) {
	cmd := newRootCmd()
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SetArgs([]string{"engine", "scale", "--help"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("engine scale --help failed: %v", err)
	}

	out := buf.String()
	if !strings.Contains(out, "Scale") {
		t.Errorf("expected help to mention 'Scale', got: %s", out)
	}
}

func TestEngineScaleCmd_Flags(t *testing.T) {
	cmd := newEngineScaleCmd()
	if cmd.Use != "scale" {
		t.Errorf("Use = %q, want %q", cmd.Use, "scale")
	}
	if cmd.Flags().Lookup("track") == nil {
		t.Error("expected --track flag")
	}
	if cmd.Flags().Lookup("count") == nil {
		t.Error("expected --count flag")
	}
}

func TestEngineListCmd_Help(t *testing.T) {
	cmd := newRootCmd()
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SetArgs([]string{"engine", "list", "--help"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("engine list --help failed: %v", err)
	}

	out := buf.String()
	if !strings.Contains(out, "engines") {
		t.Errorf("expected help to mention 'engines', got: %s", out)
	}
}

func TestEngineListCmd_Flags(t *testing.T) {
	cmd := newEngineListCmd()
	if cmd.Use != "list" {
		t.Errorf("Use = %q, want %q", cmd.Use, "list")
	}
	if cmd.Flags().Lookup("track") == nil {
		t.Error("expected --track flag")
	}
	if cmd.Flags().Lookup("status") == nil {
		t.Error("expected --status flag")
	}
}

func TestEngineRestartCmd_Help(t *testing.T) {
	cmd := newRootCmd()
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SetArgs([]string{"engine", "restart", "--help"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("engine restart --help failed: %v", err)
	}

	out := buf.String()
	if !strings.Contains(out, "Restart") || !strings.Contains(out, "engine") {
		t.Errorf("expected help to mention 'Restart' and 'engine', got: %s", out)
	}
}

func TestEngineRestartCmd_Flags(t *testing.T) {
	cmd := newEngineRestartCmd()
	if cmd.Use != "restart <engine-id>" {
		t.Errorf("Use = %q, want %q", cmd.Use, "restart <engine-id>")
	}
	if cmd.Flags().Lookup("config") == nil {
		t.Error("expected --config flag")
	}
}

func TestEngineRestartCmd_NoArgs(t *testing.T) {
	cmd := newRootCmd()
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SetArgs([]string{"engine", "restart"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error for missing args")
	}
}

func TestEngineCmd_HasSubcommands(t *testing.T) {
	cmd := newEngineCmd()
	subs := make(map[string]bool)
	for _, c := range cmd.Commands() {
		subs[c.Name()] = true
	}
	for _, expected := range []string{"start", "scale", "list", "restart"} {
		if !subs[expected] {
			t.Errorf("expected subcommand %q", expected)
		}
	}
}
