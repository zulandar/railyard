package main

import (
	"bytes"
	"fmt"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

func TestVersionCmd(t *testing.T) {
	cmd := newRootCmd()
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SetArgs([]string{"version"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("version command failed: %v", err)
	}

	out := buf.String()
	if !strings.Contains(out, "ry dev") {
		t.Errorf("expected output to contain 'ry dev', got: %s", out)
	}
	if !strings.Contains(out, "commit: none") {
		t.Errorf("expected output to contain 'commit: none', got: %s", out)
	}
}

func TestVersionCmdWithCustomValues(t *testing.T) {
	origVersion, origCommit, origDate := Version, Commit, Date
	Version, Commit, Date = "1.0.0", "abc123", "2026-01-01"
	defer func() { Version, Commit, Date = origVersion, origCommit, origDate }()

	cmd := newRootCmd()
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetArgs([]string{"version"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("version command failed: %v", err)
	}

	out := buf.String()
	if !strings.Contains(out, "ry 1.0.0") {
		t.Errorf("expected output to contain 'ry 1.0.0', got: %s", out)
	}
	if !strings.Contains(out, "commit: abc123") {
		t.Errorf("expected output to contain 'commit: abc123', got: %s", out)
	}
	if !strings.Contains(out, "built: 2026-01-01") {
		t.Errorf("expected output to contain 'built: 2026-01-01', got: %s", out)
	}
}

func TestRootCmdHelp(t *testing.T) {
	cmd := newRootCmd()
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetArgs([]string{"--help"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("help command failed: %v", err)
	}

	out := buf.String()
	if !strings.Contains(out, "Railyard") {
		t.Errorf("expected help output to contain 'Railyard', got: %s", out)
	}
	if !strings.Contains(out, "version") {
		t.Errorf("expected help output to list 'version' subcommand, got: %s", out)
	}
}

func TestRootCmdNoArgs(t *testing.T) {
	cmd := newRootCmd()
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetArgs([]string{})

	// Root command with no args should print help (not error)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("root command with no args failed: %v", err)
	}
}

func TestExecuteSuccess(t *testing.T) {
	code := execute(newRootCmd())
	if code != 0 {
		t.Errorf("expected exit code 0, got %d", code)
	}
}

func TestExecuteError(t *testing.T) {
	cmd := &cobra.Command{
		Use:           "failing",
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return fmt.Errorf("intentional error")
		},
	}
	code := execute(cmd)
	if code != 1 {
		t.Errorf("expected exit code 1, got %d", code)
	}
}

func TestNewVersionCmdOutput(t *testing.T) {
	cmd := newVersionCmd()
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)

	cmd.Run(cmd, nil)

	out := buf.String()
	expected := "ry dev (commit: none, built: unknown)\n"
	if out != expected {
		t.Errorf("expected %q, got %q", expected, out)
	}
}
