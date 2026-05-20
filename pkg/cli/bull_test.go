package main

import (
	"bytes"
	"strings"
	"testing"
)

// --- bull command tests ---

func TestBullCmd_Help(t *testing.T) {
	cmd := newRootCmd()
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SetArgs([]string{"bull", "--help"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("bull --help failed: %v", err)
	}

	out := buf.String()
	if !strings.Contains(out, "triage") {
		t.Errorf("expected help to mention 'triage', got: %s", out)
	}
}

func TestBullCmd_Flags(t *testing.T) {
	cmd := newBullCmd()
	if cmd.Use != "bull" {
		t.Errorf("Use = %q, want %q", cmd.Use, "bull")
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

func TestBullCmd_MissingConfig(t *testing.T) {
	cmd := newRootCmd()
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SetArgs([]string{"bull", "--config", "/nonexistent/railyard.yaml"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error for missing config")
	}
	if !strings.Contains(err.Error(), "load config") {
		t.Errorf("error = %q, want to contain %q", err.Error(), "load config")
	}
}

func TestBullCmd_LongDescription(t *testing.T) {
	cmd := newBullCmd()
	if !strings.Contains(cmd.Long, "GitHub") {
		t.Errorf("long description should mention 'GitHub'")
	}
	if !strings.Contains(cmd.Long, "triage") {
		t.Errorf("long description should mention 'triage'")
	}
}

func TestRootCmd_HasBullSubcommand(t *testing.T) {
	cmd := newRootCmd()
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetArgs([]string{"--help"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("root --help failed: %v", err)
	}

	if !strings.Contains(buf.String(), "bull") {
		t.Error("root help should list 'bull' subcommand")
	}
}

// --- bull triage subcommand tests ---

func TestBullTriageCmd_Help(t *testing.T) {
	cmd := newRootCmd()
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SetArgs([]string{"bull", "triage", "--help"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("bull triage --help failed: %v", err)
	}

	out := buf.String()
	if !strings.Contains(out, "issue") {
		t.Errorf("expected help to mention 'issue', got: %s", out)
	}
}

func TestBullTriageCmd_NoArgs(t *testing.T) {
	cmd := newRootCmd()
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SetArgs([]string{"bull", "triage"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error for missing issue number argument")
	}
}

func TestBullTriageCmd_MissingConfig(t *testing.T) {
	cmd := newRootCmd()
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SetArgs([]string{"bull", "triage", "42", "--config", "/nonexistent/railyard.yaml"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error for missing config")
	}
	if !strings.Contains(err.Error(), "load config") {
		t.Errorf("error = %q, want to contain %q", err.Error(), "load config")
	}
}
