package main

import (
	"bytes"
	"strings"
	"testing"
)

// --- doctor command tests ---

func TestDoctorCmd_Help(t *testing.T) {
	cmd := newRootCmd()
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SetArgs([]string{"doctor", "--help"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("doctor --help failed: %v", err)
	}

	out := buf.String()
	if !strings.Contains(out, "diagnostic checks") {
		t.Errorf("expected help to mention 'diagnostic checks', got: %s", out)
	}
	if !strings.Contains(out, "--config") {
		t.Errorf("expected --config flag in help, got: %s", out)
	}
}

func TestNewDoctorCmd(t *testing.T) {
	cmd := newDoctorCmd()
	if cmd.Use != "doctor" {
		t.Errorf("Use = %q, want %q", cmd.Use, "doctor")
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

func TestCheckBinary_Go(t *testing.T) {
	result := checkBinary("go")
	if result.status != "PASS" {
		t.Errorf("expected PASS for go binary, got %s: %s", result.status, result.detail)
	}
	if !strings.Contains(result.detail, "go") {
		t.Errorf("expected detail to contain 'go', got: %s", result.detail)
	}
}

func TestCheckBinary_Missing(t *testing.T) {
	result := checkBinary("nonexistent-binary-xyz-12345")
	if result.status != "FAIL" {
		t.Errorf("expected FAIL for missing binary, got %s: %s", result.status, result.detail)
	}
	if !strings.Contains(result.detail, "not found") {
		t.Errorf("expected detail to contain 'not found', got: %s", result.detail)
	}
}

func TestCheckBinary_Claude_Warn(t *testing.T) {
	// Claude CLI may or may not be installed; if missing, it should be WARN not FAIL.
	result := checkBinary("claude")
	if result.status == "FAIL" {
		t.Errorf("claude should be WARN when missing, not FAIL; got: %s: %s", result.status, result.detail)
	}
}

func TestCheckGitRepo(t *testing.T) {
	// We're running inside the railyard git repo, so this should pass.
	result := checkGitRepo()
	if result.status != "PASS" {
		t.Errorf("expected PASS for git repo, got %s: %s", result.status, result.detail)
	}
}

func TestRootCmd_HasDoctorSubcommand(t *testing.T) {
	cmd := newRootCmd()
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetArgs([]string{"--help"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("root --help failed: %v", err)
	}

	if !strings.Contains(buf.String(), "doctor") {
		t.Error("root help should list 'doctor' subcommand")
	}
}
