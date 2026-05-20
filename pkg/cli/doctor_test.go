package main

import (
	"bytes"
	"io"
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

func TestCheckCredentials_DefaultPassword(t *testing.T) {
	result := checkCredentials("root", "", io.Discard)
	if result.status != "WARN" {
		t.Errorf("expected WARN for default root/empty-password, got %s: %s", result.status, result.detail)
	}
	if !strings.Contains(result.detail, "default") {
		t.Errorf("expected detail to mention 'default', got: %s", result.detail)
	}
}

func TestCheckCredentials_ConfiguredPassword(t *testing.T) {
	result := checkCredentials("admin", "s3cret", io.Discard)
	if result.status != "PASS" {
		t.Errorf("expected PASS for configured credentials, got %s: %s", result.status, result.detail)
	}
}

func TestCheckCredentials_RootWithPassword(t *testing.T) {
	result := checkCredentials("root", "s3cret", io.Discard)
	if result.status != "PASS" {
		t.Errorf("expected PASS for root with password, got %s: %s", result.status, result.detail)
	}
}

func TestBinaryLabel_Gh(t *testing.T) {
	got := binaryLabel("gh")
	if got != "GitHub CLI" {
		t.Errorf("binaryLabel(\"gh\") = %q, want %q", got, "GitHub CLI")
	}
}

func TestCheckGhAuth_ReturnsCheckResult(t *testing.T) {
	result := checkGhAuth()
	if result.name != "GitHub CLI auth" {
		t.Errorf("name = %q, want %q", result.name, "GitHub CLI auth")
	}
	// gh may or may not be installed/authenticated; just verify status is PASS or WARN
	if result.status != "PASS" && result.status != "WARN" {
		t.Errorf("status = %q, want PASS or WARN", result.status)
	}
}

func TestCheckGhAuth_DetailContent(t *testing.T) {
	result := checkGhAuth()
	if result.status == "PASS" {
		if !strings.Contains(result.detail, "authenticated") {
			t.Errorf("PASS detail should contain 'authenticated', got: %s", result.detail)
		}
	} else if result.status == "WARN" {
		if !strings.Contains(result.detail, "gh auth login") {
			t.Errorf("WARN detail should contain 'gh auth login', got: %s", result.detail)
		}
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
