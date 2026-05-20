package main

import (
	"bytes"
	"testing"

	"github.com/spf13/cobra"
)

func TestNewOverlayCmd(t *testing.T) {
	cmd := newOverlayCmd()
	if cmd.Use != "overlay" {
		t.Errorf("expected Use='overlay', got %q", cmd.Use)
	}
	// Verify all subcommands exist.
	subs := make(map[string]bool)
	for _, sub := range cmd.Commands() {
		subs[sub.Use] = true
	}
	for _, name := range []string{"build", "status", "cleanup", "gc"} {
		if !subs[name] {
			t.Errorf("missing subcommand %q", name)
		}
	}
}

func TestOverlayBuildRequiresFlags(t *testing.T) {
	root := &cobra.Command{Use: "ry"}
	root.AddCommand(newOverlayCmd())

	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs([]string{"overlay", "build"})

	err := root.Execute()
	if err == nil {
		t.Error("expected error when --engine flag is missing")
	}
}

func TestOverlayCleanupRequiresEngine(t *testing.T) {
	root := &cobra.Command{Use: "ry"}
	root.AddCommand(newOverlayCmd())

	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs([]string{"overlay", "cleanup"})

	err := root.Execute()
	if err == nil {
		t.Error("expected error when --engine flag is missing")
	}
}

func TestOverlayGCHasFlags(t *testing.T) {
	cmd := newOverlayGCCmd()
	f := cmd.Flags().Lookup("dry-run")
	if f == nil {
		t.Error("gc command missing --dry-run flag")
	}
	f = cmd.Flags().Lookup("config")
	if f == nil {
		t.Error("gc command missing --config flag")
	}
}

func TestPrintOverlayStatus(t *testing.T) {
	var buf bytes.Buffer
	r := overlayStatusResult{
		EngineID:      "eng-test123",
		Track:         "backend",
		Branch:        "ry/user/backend/eng-test123",
		LastCommit:    "abc123",
		FilesIndexed:  10,
		ChunksIndexed: 50,
		UpdatedAt:     "2026-02-19 12:00:00",
	}

	printOverlayStatus(&buf, r)
	output := buf.String()

	for _, want := range []string{"eng-test123", "backend", "abc123", "10", "50"} {
		if !bytes.Contains([]byte(output), []byte(want)) {
			t.Errorf("output missing %q:\n%s", want, output)
		}
	}
}
