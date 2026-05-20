package main

import (
	"bytes"
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// overlay commands (overlay.go)
// ---------------------------------------------------------------------------

func TestOverlayCmd_Help(t *testing.T) {
	cmd := newRootCmd()
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SetArgs([]string{"overlay", "--help"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("overlay --help failed: %v", err)
	}

	out := buf.String()
	for _, sub := range []string{"build", "status", "cleanup", "gc"} {
		if !strings.Contains(out, sub) {
			t.Errorf("expected help to list %q subcommand, got: %s", sub, out)
		}
	}
}

func TestOverlayBuildCmd_Help(t *testing.T) {
	cmd := newRootCmd()
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SetArgs([]string{"overlay", "build", "--help"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("overlay build --help failed: %v", err)
	}

	out := buf.String()
	for _, flag := range []string{"--engine", "--track", "--workdir"} {
		if !strings.Contains(out, flag) {
			t.Errorf("expected help to mention %q, got: %s", flag, out)
		}
	}
}

func TestOverlayBuildCmd_RequiredFlags(t *testing.T) {
	cmd := newRootCmd()
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SetArgs([]string{"overlay", "build"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error when --engine is missing")
	}
}

func TestOverlayStatusCmd_Help(t *testing.T) {
	cmd := newRootCmd()
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SetArgs([]string{"overlay", "status", "--help"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("overlay status --help failed: %v", err)
	}

	out := buf.String()
	if !strings.Contains(out, "--engine") {
		t.Errorf("expected help to mention '--engine', got: %s", out)
	}
}

func TestOverlayCleanupCmd_Help(t *testing.T) {
	cmd := newRootCmd()
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SetArgs([]string{"overlay", "cleanup", "--help"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("overlay cleanup --help failed: %v", err)
	}

	out := buf.String()
	if !strings.Contains(out, "--engine") {
		t.Errorf("expected help to mention '--engine', got: %s", out)
	}
}

func TestOverlayCleanupCmd_RequiredFlag(t *testing.T) {
	cmd := newRootCmd()
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SetArgs([]string{"overlay", "cleanup"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error when --engine is missing")
	}
}

func TestOverlayGCCmd_Help(t *testing.T) {
	cmd := newRootCmd()
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SetArgs([]string{"overlay", "gc", "--help"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("overlay gc --help failed: %v", err)
	}

	out := buf.String()
	if !strings.Contains(out, "--dry-run") {
		t.Errorf("expected help to mention '--dry-run', got: %s", out)
	}
}

func TestOverlayBuildCmd_MissingConfig(t *testing.T) {
	cmd := newRootCmd()
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SetArgs([]string{"overlay", "build", "--config", "/nonexistent/railyard.yaml", "--engine", "eng-1", "--track", "backend"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error for missing config file")
	}
}

func TestOverlayStatusCmd_MissingConfig(t *testing.T) {
	cmd := newRootCmd()
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SetArgs([]string{"overlay", "status", "--config", "/nonexistent/railyard.yaml"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error for missing config file")
	}
}

func TestOverlayCleanupCmd_MissingConfig(t *testing.T) {
	cmd := newRootCmd()
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SetArgs([]string{"overlay", "cleanup", "--config", "/nonexistent/railyard.yaml", "--engine", "eng-1"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error for missing config file")
	}
}

func TestOverlayGCCmd_MissingConfig(t *testing.T) {
	cmd := newRootCmd()
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SetArgs([]string{"overlay", "gc", "--config", "/nonexistent/railyard.yaml"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error for missing config file")
	}
}

// ---------------------------------------------------------------------------
// migrate command (migrate.go)
// ---------------------------------------------------------------------------

func TestNewMigrateCmd_Flags(t *testing.T) {
	cmd := newMigrateCmd()
	cfgFlag := cmd.Flags().Lookup("config")
	if cfgFlag == nil {
		t.Fatal("expected --config flag")
	}
	if cfgFlag.DefValue != "railyard.yaml" {
		t.Errorf("--config default = %q, want %q", cfgFlag.DefValue, "railyard.yaml")
	}
}

// ---------------------------------------------------------------------------
// car publish (car.go)
// ---------------------------------------------------------------------------

func TestCarPublishCmd_Help(t *testing.T) {
	cmd := newRootCmd()
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SetArgs([]string{"car", "publish", "--help"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("car publish --help failed: %v", err)
	}

	out := buf.String()
	if !strings.Contains(out, "--recursive") {
		t.Errorf("expected help to mention '--recursive', got: %s", out)
	}
}

func TestCarPublishCmd_NoArgs(t *testing.T) {
	cmd := newRootCmd()
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SetArgs([]string{"car", "publish"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error for missing args")
	}
}

func TestCarPublishCmd_MissingConfig(t *testing.T) {
	cmd := newRootCmd()
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SetArgs([]string{"car", "publish", "--config", "/nonexistent/railyard.yaml", "car-123"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error for missing config file")
	}
}

// ---------------------------------------------------------------------------
// car ready (car.go)
// ---------------------------------------------------------------------------

func TestCarReadyCmd_MissingConfig(t *testing.T) {
	cmd := newRootCmd()
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SetArgs([]string{"car", "ready", "--config", "/nonexistent/railyard.yaml"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error for missing config file")
	}
}

// ---------------------------------------------------------------------------
// car dep add/list/remove missing config (car.go)
// ---------------------------------------------------------------------------

func TestCarDepAddCmd_MissingConfig(t *testing.T) {
	cmd := newRootCmd()
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SetArgs([]string{"car", "dep", "add", "car-123", "--blocked-by", "car-456", "--config", "/nonexistent/railyard.yaml"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error for missing config file")
	}
}

func TestCarDepListCmd_MissingConfig(t *testing.T) {
	cmd := newRootCmd()
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SetArgs([]string{"car", "dep", "list", "car-123", "--config", "/nonexistent/railyard.yaml"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error for missing config file")
	}
}

func TestCarDepRemoveCmd_MissingConfig(t *testing.T) {
	cmd := newRootCmd()
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SetArgs([]string{"car", "dep", "remove", "car-123", "--blocked-by", "car-456", "--config", "/nonexistent/railyard.yaml"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error for missing config file")
	}
}

// ---------------------------------------------------------------------------
// telegraph sessions (telegraph.go)
// ---------------------------------------------------------------------------

func TestTelegraphSessionsCmd_Help(t *testing.T) {
	cmd := newRootCmd()
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SetArgs([]string{"telegraph", "sessions", "--help"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("telegraph sessions --help failed: %v", err)
	}

	out := buf.String()
	if !strings.Contains(out, "--clear") {
		t.Errorf("expected help to mention '--clear', got: %s", out)
	}
}

func TestTelegraphSessionsCmd_MissingConfig(t *testing.T) {
	cmd := newRootCmd()
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SetArgs([]string{"telegraph", "sessions", "--config", "/nonexistent/railyard.yaml"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error for missing config file")
	}
}

// ---------------------------------------------------------------------------
// version command (main.go)
// ---------------------------------------------------------------------------

func TestVersionCmd_Output(t *testing.T) {
	cmd := newRootCmd()
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SetArgs([]string{"version"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("version command failed: %v", err)
	}

	out := buf.String()
	if len(out) == 0 {
		t.Error("expected version output, got empty string")
	}
}
