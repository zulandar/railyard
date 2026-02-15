package main

import (
	"bytes"
	"strings"
	"testing"
)

// --- complete command tests ---

func TestCompleteCmd_Help(t *testing.T) {
	cmd := newRootCmd()
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SetArgs([]string{"complete", "--help"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("complete --help failed: %v", err)
	}

	out := buf.String()
	if !strings.Contains(out, "done") {
		t.Errorf("expected help to mention 'done', got: %s", out)
	}
}

func TestCompleteCmd_Flags(t *testing.T) {
	cmd := newCompleteCmd()
	if cmd.Use != "complete <bead-id> <summary>" {
		t.Errorf("Use = %q, want %q", cmd.Use, "complete <bead-id> <summary>")
	}
	if cmd.Flags().Lookup("config") == nil {
		t.Error("expected --config flag")
	}
}

func TestCompleteCmd_NoArgs(t *testing.T) {
	cmd := newRootCmd()
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SetArgs([]string{"complete"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error for missing args")
	}
}

func TestCompleteCmd_OneArg(t *testing.T) {
	cmd := newRootCmd()
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	// Only bead-id, no summary
	cmd.SetArgs([]string{"complete", "be-12345"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error for insufficient args (need bead-id and summary)")
	}
}

func TestRootCmd_HasCompleteSubcommand(t *testing.T) {
	cmd := newRootCmd()
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetArgs([]string{"--help"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("root --help failed: %v", err)
	}

	if !strings.Contains(buf.String(), "complete") {
		t.Error("root help should list 'complete' subcommand")
	}
}

// --- progress command tests ---

func TestProgressCmd_Help(t *testing.T) {
	cmd := newRootCmd()
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SetArgs([]string{"progress", "--help"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("progress --help failed: %v", err)
	}

	out := buf.String()
	if !strings.Contains(out, "progress note") {
		t.Errorf("expected help to mention 'progress note', got: %s", out)
	}
}

func TestProgressCmd_Flags(t *testing.T) {
	cmd := newProgressCmd()
	if cmd.Use != "progress <bead-id> <note>" {
		t.Errorf("Use = %q, want %q", cmd.Use, "progress <bead-id> <note>")
	}
	if cmd.Flags().Lookup("config") == nil {
		t.Error("expected --config flag")
	}
}

func TestProgressCmd_NoArgs(t *testing.T) {
	cmd := newRootCmd()
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SetArgs([]string{"progress"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error for missing args")
	}
}

func TestProgressCmd_OneArg(t *testing.T) {
	cmd := newRootCmd()
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	// Only bead-id, no note
	cmd.SetArgs([]string{"progress", "be-12345"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error for insufficient args (need bead-id and note)")
	}
}

func TestRootCmd_HasProgressSubcommand(t *testing.T) {
	cmd := newRootCmd()
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetArgs([]string{"--help"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("root --help failed: %v", err)
	}

	if !strings.Contains(buf.String(), "progress") {
		t.Error("root help should list 'progress' subcommand")
	}
}
