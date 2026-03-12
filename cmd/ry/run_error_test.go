package main

import (
	"bytes"
	"strings"
	"testing"
)

// --- missing config error tests ---

func TestLogsCmd_MissingConfig(t *testing.T) {
	cmd := newRootCmd()
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SetArgs([]string{"logs", "--config", "/nonexistent/railyard.yaml"})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error for missing config file")
	}
}

func TestCarListCmd_MissingConfig(t *testing.T) {
	cmd := newRootCmd()
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SetArgs([]string{"car", "list", "--config", "/nonexistent/railyard.yaml"})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error for missing config file")
	}
}

func TestCarShowCmd_MissingConfig(t *testing.T) {
	cmd := newRootCmd()
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SetArgs([]string{"car", "show", "CAR-123", "--config", "/nonexistent/railyard.yaml"})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error for missing config file")
	}
}

func TestCarUpdateCmd_MissingConfig(t *testing.T) {
	cmd := newRootCmd()
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SetArgs([]string{"car", "update", "CAR-123", "--status", "done", "--config", "/nonexistent/railyard.yaml"})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error for missing config file")
	}
}

func TestCarChildrenCmd_MissingConfig(t *testing.T) {
	cmd := newRootCmd()
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SetArgs([]string{"car", "children", "CAR-123", "--config", "/nonexistent/railyard.yaml"})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error for missing config file")
	}
}

func TestEngineScaleCmd_MissingConfig(t *testing.T) {
	cmd := newRootCmd()
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SetArgs([]string{"engine", "scale", "--track", "backend", "--count", "2", "--config", "/nonexistent/railyard.yaml"})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error for missing config file")
	}
}

func TestEngineListCmd_MissingConfig(t *testing.T) {
	cmd := newRootCmd()
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SetArgs([]string{"engine", "list", "--config", "/nonexistent/railyard.yaml"})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error for missing config file")
	}
}

func TestEngineRestartCmd_MissingConfig(t *testing.T) {
	cmd := newRootCmd()
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SetArgs([]string{"engine", "restart", "eng-123", "--config", "/nonexistent/railyard.yaml"})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error for missing config file")
	}
}

func TestCompleteCmd_MissingConfig(t *testing.T) {
	cmd := newRootCmd()
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SetArgs([]string{"complete", "CAR-123", "--config", "/nonexistent/railyard.yaml"})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error for missing config file")
	}
}

func TestProgressCmd_MissingConfig(t *testing.T) {
	cmd := newRootCmd()
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SetArgs([]string{"progress", "CAR-123", "--message", "done", "--config", "/nonexistent/railyard.yaml"})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error for missing config file")
	}
}

func TestDoctorCmd_MissingConfig(t *testing.T) {
	cmd := newRootCmd()
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SetArgs([]string{"doctor", "--config", "/nonexistent/railyard.yaml"})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error for missing config file")
	}
}

func TestEngineStartCmd_MissingConfig(t *testing.T) {
	cmd := newRootCmd()
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SetArgs([]string{"engine", "start", "--config", "/nonexistent/railyard.yaml"})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error for missing config file")
	}
}

// --- additional tests ---

func TestDoctorCmd_RunsChecks(t *testing.T) {
	cmd := newRootCmd()
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SetArgs([]string{"doctor", "--config", writeTestConfig(t)})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error from doctor (database not running)")
	}
	out := buf.String()
	if !strings.Contains(out, "PASS") {
		t.Errorf("expected output to contain PASS (config check), got: %s", out)
	}
	if !strings.Contains(out, "FAIL") {
		t.Errorf("expected output to contain FAIL (database checks), got: %s", out)
	}
}

func TestLogsCmd_Help(t *testing.T) {
	cmd := newRootCmd()
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SetArgs([]string{"logs", "--help"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("logs --help failed: %v", err)
	}
	out := buf.String()
	for _, flag := range []string{"--engine", "--car", "--follow", "--raw"} {
		if !strings.Contains(out, flag) {
			t.Errorf("expected %s flag in help, got: %s", flag, out)
		}
	}
}
