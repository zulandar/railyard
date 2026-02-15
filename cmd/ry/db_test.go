package main

import (
	"bytes"
	"os"
	"strings"
	"testing"
)

func TestDBCmd_Help(t *testing.T) {
	cmd := newRootCmd()
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SetArgs([]string{"db", "--help"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("db --help failed: %v", err)
	}

	out := buf.String()
	if !strings.Contains(out, "Database management") {
		t.Errorf("expected help to mention 'Database management', got: %s", out)
	}
	if !strings.Contains(out, "init") {
		t.Errorf("expected help to list 'init' subcommand, got: %s", out)
	}
}

func TestDBInitCmd_Help(t *testing.T) {
	cmd := newRootCmd()
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SetArgs([]string{"db", "init", "--help"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("db init --help failed: %v", err)
	}

	out := buf.String()
	if !strings.Contains(out, "Dolt database") {
		t.Errorf("expected help to mention 'Dolt database', got: %s", out)
	}
	if !strings.Contains(out, "--config") {
		t.Errorf("expected help to mention '--config' flag, got: %s", out)
	}
	if !strings.Contains(out, "railyard.yaml") {
		t.Errorf("expected default config path 'railyard.yaml', got: %s", out)
	}
}

func TestDBInitCmd_MissingConfig(t *testing.T) {
	cmd := newRootCmd()
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SetArgs([]string{"db", "init", "--config", "/nonexistent/railyard.yaml"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error for missing config file")
	}
	if !strings.Contains(err.Error(), "load config") {
		t.Errorf("error = %q, want to contain %q", err.Error(), "load config")
	}
}

func TestDBInitCmd_InvalidConfig(t *testing.T) {
	// Write an invalid config (missing required fields)
	dir := t.TempDir()
	cfgPath := dir + "/railyard.yaml"
	if err := writeTestFile(cfgPath, "invalid: true\n"); err != nil {
		t.Fatal(err)
	}

	cmd := newRootCmd()
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SetArgs([]string{"db", "init", "--config", cfgPath})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error for invalid config")
	}
	if !strings.Contains(err.Error(), "load config") {
		t.Errorf("error = %q, want to contain %q", err.Error(), "load config")
	}
}

func TestDBInitCmd_NoDolt(t *testing.T) {
	dir := t.TempDir()
	cfgPath := dir + "/railyard.yaml"
	cfg := `
owner: testuser
repo: git@github.com:org/app.git
dolt:
  host: 127.0.0.1
  port: 19876
tracks:
  - name: backend
    language: go
`
	if err := writeTestFile(cfgPath, cfg); err != nil {
		t.Fatal(err)
	}

	cmd := newRootCmd()
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SetArgs([]string{"db", "init", "--config", cfgPath})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error when Dolt is not running")
	}
	// Should load config successfully but fail on connection
	output := buf.String()
	if !strings.Contains(output, "Loaded config") {
		t.Errorf("expected 'Loaded config' in output, got: %s", output)
	}
}

func TestNewDBCmd(t *testing.T) {
	cmd := newDBCmd()
	if cmd.Use != "db" {
		t.Errorf("Use = %q, want %q", cmd.Use, "db")
	}
	if !cmd.HasSubCommands() {
		t.Error("db command should have subcommands")
	}
}

func TestNewDBInitCmd(t *testing.T) {
	cmd := newDBInitCmd()
	if cmd.Use != "init" {
		t.Errorf("Use = %q, want %q", cmd.Use, "init")
	}
	flag := cmd.Flags().Lookup("config")
	if flag == nil {
		t.Fatal("expected --config flag")
	}
	if flag.DefValue != "railyard.yaml" {
		t.Errorf("--config default = %q, want %q", flag.DefValue, "railyard.yaml")
	}
	if flag.Shorthand != "c" {
		t.Errorf("--config shorthand = %q, want %q", flag.Shorthand, "c")
	}
}

func writeTestFile(path, content string) error {
	return os.WriteFile(path, []byte(content), 0644)
}
