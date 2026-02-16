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

func TestDBResetCmd_Help(t *testing.T) {
	cmd := newRootCmd()
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SetArgs([]string{"db", "reset", "--help"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("db reset --help failed: %v", err)
	}

	out := buf.String()
	for _, want := range []string{"Drops the Railyard database", "--config", "--database", "--yes", "--force"} {
		if !strings.Contains(out, want) {
			t.Errorf("expected help to contain %q, got: %s", want, out)
		}
	}
}

func TestNewDBResetCmd(t *testing.T) {
	cmd := newDBResetCmd()
	if cmd.Use != "reset" {
		t.Errorf("Use = %q, want %q", cmd.Use, "reset")
	}

	tests := []struct {
		name, defValue, shorthand string
	}{
		{"config", "railyard.yaml", "c"},
		{"database", "", ""},
		{"yes", "false", "y"},
		{"force", "false", ""},
	}
	for _, tt := range tests {
		flag := cmd.Flags().Lookup(tt.name)
		if flag == nil {
			t.Fatalf("expected --%s flag", tt.name)
		}
		if flag.DefValue != tt.defValue {
			t.Errorf("--%s default = %q, want %q", tt.name, flag.DefValue, tt.defValue)
		}
		if flag.Shorthand != tt.shorthand {
			t.Errorf("--%s shorthand = %q, want %q", tt.name, flag.Shorthand, tt.shorthand)
		}
	}
}

func TestDBResetCmd_MissingConfig(t *testing.T) {
	cmd := newRootCmd()
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SetArgs([]string{"db", "reset", "--yes", "--config", "/nonexistent/railyard.yaml"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error for missing config file")
	}
	if !strings.Contains(err.Error(), "load config") {
		t.Errorf("error = %q, want to contain %q", err.Error(), "load config")
	}
}

func TestDBResetCmd_RequiresConfirmation(t *testing.T) {
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
	// Simulate typing "no" on stdin.
	cmd.SetIn(strings.NewReader("no\n"))
	cmd.SetArgs([]string{"db", "reset", "--config", cfgPath})

	err := cmd.Execute()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	out := buf.String()
	if !strings.Contains(out, "WARNING") {
		t.Errorf("expected WARNING prompt, got: %s", out)
	}
	if !strings.Contains(out, "Aborted") {
		t.Errorf("expected 'Aborted' message, got: %s", out)
	}
}

func TestDBResetCmd_DatabaseFlag(t *testing.T) {
	// --database flag should skip config loading entirely.
	// Without --yes it will prompt, so supply "no" on stdin.
	cmd := newRootCmd()
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SetIn(strings.NewReader("no\n"))
	cmd.SetArgs([]string{"db", "reset", "--database", "mydb"})

	err := cmd.Execute()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	out := buf.String()
	// Should prompt about "mydb", not try to load config.
	if !strings.Contains(out, "mydb") {
		t.Errorf("expected prompt to mention 'mydb', got: %s", out)
	}
	if !strings.Contains(out, "Aborted") {
		t.Errorf("expected 'Aborted' after typing 'no', got: %s", out)
	}
}

func writeTestFile(path, content string) error {
	return os.WriteFile(path, []byte(content), 0644)
}
