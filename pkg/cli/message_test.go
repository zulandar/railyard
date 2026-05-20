package main

import (
	"bytes"
	"strings"
	"testing"
)

// --- message command tests ---

func TestMessageCmd_Help(t *testing.T) {
	cmd := newRootCmd()
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SetArgs([]string{"message", "--help"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("message --help failed: %v", err)
	}

	out := buf.String()
	if !strings.Contains(out, "Messaging commands") {
		t.Errorf("expected help to mention 'Messaging commands', got: %s", out)
	}
	for _, sub := range []string{"send", "ack", "thread"} {
		if !strings.Contains(out, sub) {
			t.Errorf("expected help to list %q subcommand, got: %s", sub, out)
		}
	}
}

func TestNewMessageCmd(t *testing.T) {
	cmd := newMessageCmd()
	if cmd.Use != "message" {
		t.Errorf("Use = %q, want %q", cmd.Use, "message")
	}
	if !cmd.HasSubCommands() {
		t.Error("message command should have subcommands")
	}
}

// --- message send tests ---

func TestMessageSendCmd_Help(t *testing.T) {
	cmd := newRootCmd()
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SetArgs([]string{"message", "send", "--help"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("message send --help failed: %v", err)
	}

	out := buf.String()
	for _, flag := range []string{"--from", "--to", "--subject", "--body", "--car-id", "--thread-id", "--priority", "--config"} {
		if !strings.Contains(out, flag) {
			t.Errorf("expected %s flag, got: %s", flag, out)
		}
	}
}

func TestNewMessageSendCmd(t *testing.T) {
	cmd := newMessageSendCmd()
	if cmd.Use != "send" {
		t.Errorf("Use = %q, want %q", cmd.Use, "send")
	}

	for _, name := range []string{"from", "to", "subject", "body", "car-id", "thread-id", "priority", "config"} {
		if cmd.Flags().Lookup(name) == nil {
			t.Errorf("expected --%s flag", name)
		}
	}

	// Check defaults
	priFlag := cmd.Flags().Lookup("priority")
	if priFlag.DefValue != "normal" {
		t.Errorf("--priority default = %q, want %q", priFlag.DefValue, "normal")
	}

	cfgFlag := cmd.Flags().Lookup("config")
	if cfgFlag.DefValue != "railyard.yaml" {
		t.Errorf("--config default = %q, want %q", cfgFlag.DefValue, "railyard.yaml")
	}
	if cfgFlag.Shorthand != "c" {
		t.Errorf("--config shorthand = %q, want %q", cfgFlag.Shorthand, "c")
	}
}

func TestMessageSendCmd_MissingRequiredFlags(t *testing.T) {
	cmd := newRootCmd()
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SetArgs([]string{"message", "send"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error for missing required flags")
	}
}

func TestMessageSendCmd_MissingConfig(t *testing.T) {
	cmd := newRootCmd()
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SetArgs([]string{"message", "send",
		"--from", "engine-1",
		"--to", "engine-2",
		"--subject", "test",
		"--body", "hello",
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

// --- inbox command tests ---

func TestInboxCmd_Help(t *testing.T) {
	cmd := newRootCmd()
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SetArgs([]string{"inbox", "--help"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("inbox --help failed: %v", err)
	}

	out := buf.String()
	if !strings.Contains(out, "inbox") {
		t.Errorf("expected help to mention 'inbox', got: %s", out)
	}
	if !strings.Contains(out, "--agent") {
		t.Errorf("expected --agent flag, got: %s", out)
	}
}

func TestNewInboxCmd(t *testing.T) {
	cmd := newInboxCmd()
	if cmd.Use != "inbox" {
		t.Errorf("Use = %q, want %q", cmd.Use, "inbox")
	}

	for _, name := range []string{"agent", "config"} {
		if cmd.Flags().Lookup(name) == nil {
			t.Errorf("expected --%s flag", name)
		}
	}

	cfgFlag := cmd.Flags().Lookup("config")
	if cfgFlag.DefValue != "railyard.yaml" {
		t.Errorf("--config default = %q, want %q", cfgFlag.DefValue, "railyard.yaml")
	}
	if cfgFlag.Shorthand != "c" {
		t.Errorf("--config shorthand = %q, want %q", cfgFlag.Shorthand, "c")
	}
}

func TestInboxCmd_MissingRequiredFlags(t *testing.T) {
	cmd := newRootCmd()
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SetArgs([]string{"inbox"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error for missing --agent flag")
	}
}

func TestInboxCmd_MissingConfig(t *testing.T) {
	cmd := newRootCmd()
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SetArgs([]string{"inbox",
		"--agent", "engine-1",
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

// --- message ack tests ---

func TestMessageAckCmd_Help(t *testing.T) {
	cmd := newRootCmd()
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SetArgs([]string{"message", "ack", "--help"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("message ack --help failed: %v", err)
	}

	out := buf.String()
	if !strings.Contains(out, "acknowledged") {
		t.Errorf("expected help to mention 'acknowledged', got: %s", out)
	}
	for _, flag := range []string{"--config", "--broadcast", "--agent"} {
		if !strings.Contains(out, flag) {
			t.Errorf("expected %s flag, got: %s", flag, out)
		}
	}
}

func TestNewMessageAckCmd(t *testing.T) {
	cmd := newMessageAckCmd()
	if cmd.Use != "ack <message-id>" {
		t.Errorf("Use = %q, want %q", cmd.Use, "ack <message-id>")
	}

	for _, name := range []string{"config", "broadcast", "agent"} {
		if cmd.Flags().Lookup(name) == nil {
			t.Errorf("expected --%s flag", name)
		}
	}

	broadcastFlag := cmd.Flags().Lookup("broadcast")
	if broadcastFlag.DefValue != "false" {
		t.Errorf("--broadcast default = %q, want %q", broadcastFlag.DefValue, "false")
	}
}

func TestMessageAckCmd_NoArgs(t *testing.T) {
	cmd := newRootCmd()
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SetArgs([]string{"message", "ack"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error for missing args")
	}
}

func TestMessageAckCmd_MissingConfig(t *testing.T) {
	cmd := newRootCmd()
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SetArgs([]string{"message", "ack", "42",
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

// --- message thread tests ---

func TestMessageThreadCmd_Help(t *testing.T) {
	cmd := newRootCmd()
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SetArgs([]string{"message", "thread", "--help"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("message thread --help failed: %v", err)
	}

	out := buf.String()
	if !strings.Contains(out, "thread") {
		t.Errorf("expected help to mention 'thread', got: %s", out)
	}
}

func TestNewMessageThreadCmd(t *testing.T) {
	cmd := newMessageThreadCmd()
	if cmd.Use != "thread <thread-id>" {
		t.Errorf("Use = %q, want %q", cmd.Use, "thread <thread-id>")
	}

	cfgFlag := cmd.Flags().Lookup("config")
	if cfgFlag == nil {
		t.Fatal("expected --config flag")
	}
	if cfgFlag.DefValue != "railyard.yaml" {
		t.Errorf("--config default = %q, want %q", cfgFlag.DefValue, "railyard.yaml")
	}
}

func TestMessageThreadCmd_NoArgs(t *testing.T) {
	cmd := newRootCmd()
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SetArgs([]string{"message", "thread"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error for missing args")
	}
}

func TestMessageThreadCmd_MissingConfig(t *testing.T) {
	cmd := newRootCmd()
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SetArgs([]string{"message", "thread", "7",
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

// --- root command integration tests ---

func TestRootCmd_HasMessageSubcommand(t *testing.T) {
	cmd := newRootCmd()
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetArgs([]string{"--help"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("root --help failed: %v", err)
	}

	if !strings.Contains(buf.String(), "message") {
		t.Error("root help should list 'message' subcommand")
	}
}

func TestRootCmd_HasInboxSubcommand(t *testing.T) {
	cmd := newRootCmd()
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetArgs([]string{"--help"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("root --help failed: %v", err)
	}

	if !strings.Contains(buf.String(), "inbox") {
		t.Error("root help should list 'inbox' subcommand")
	}
}
