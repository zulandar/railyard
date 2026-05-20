package main

import (
	"bytes"
	"testing"
)

func TestNewInspectCmd_Exists(t *testing.T) {
	cmd := newInspectCmd()
	if cmd.Use != "inspect" {
		t.Errorf("Use = %q, want %q", cmd.Use, "inspect")
	}
	if cmd.Short == "" {
		t.Error("Short description should not be empty")
	}
}

func TestNewInspectCmd_HasReviewSubcommand(t *testing.T) {
	cmd := newInspectCmd()
	found := false
	for _, sub := range cmd.Commands() {
		if sub.Use == "review <pr-number>" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected 'review' subcommand")
	}
}

func TestNewInspectCmd_Help(t *testing.T) {
	cmd := newRootCmd()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetArgs([]string{"inspect", "--help"})
	err := cmd.Execute()
	if err != nil {
		t.Fatalf("help: %v", err)
	}
	if !bytes.Contains(buf.Bytes(), []byte("inspect")) {
		t.Error("help output should mention inspect")
	}
}
