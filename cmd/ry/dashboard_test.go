package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestDashboardCmd_Help(t *testing.T) {
	cmd := newRootCmd()
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SetArgs([]string{"dashboard", "--help"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("dashboard --help failed: %v", err)
	}

	out := buf.String()
	if !strings.Contains(out, "web dashboard") {
		t.Errorf("expected help to mention 'web dashboard', got: %s", out)
	}
	if !strings.Contains(out, "--port") {
		t.Errorf("expected help to mention '--port' flag, got: %s", out)
	}
	if !strings.Contains(out, "--config") {
		t.Errorf("expected help to mention '--config' flag, got: %s", out)
	}
}

func TestDashboardCmd_MissingConfig(t *testing.T) {
	cmd := newRootCmd()
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SetArgs([]string{"dashboard", "--config", "/nonexistent/railyard.yaml"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error for missing config file")
	}
	if !strings.Contains(err.Error(), "load config") {
		t.Errorf("error = %q, want to contain %q", err.Error(), "load config")
	}
}

func TestDashboardCmd_DefaultPort(t *testing.T) {
	cmd := newDashboardCmd()
	flag := cmd.Flags().Lookup("port")
	if flag == nil {
		t.Fatal("--port flag not found")
	}
	if flag.DefValue != "8080" {
		t.Errorf("default port = %q, want %q", flag.DefValue, "8080")
	}
}
