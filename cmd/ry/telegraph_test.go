package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/zulandar/railyard/internal/orchestration"
)

// ---------------------------------------------------------------------------
// telegraph command structure tests
// ---------------------------------------------------------------------------

func TestTelegraphCmd_HasSubcommands(t *testing.T) {
	cmd := newTelegraphCmd()
	subs := make(map[string]bool)
	for _, c := range cmd.Commands() {
		subs[c.Name()] = true
	}
	for _, expected := range []string{"start", "status", "stop"} {
		if !subs[expected] {
			t.Errorf("expected subcommand %q", expected)
		}
	}
}

func TestTelegraphCmd_Alias(t *testing.T) {
	cmd := newTelegraphCmd()
	if len(cmd.Aliases) == 0 {
		t.Fatal("expected at least one alias")
	}
	if cmd.Aliases[0] != "tg" {
		t.Errorf("alias = %q, want %q", cmd.Aliases[0], "tg")
	}
}

func TestTelegraphCmd_Help(t *testing.T) {
	cmd := newRootCmd()
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SetArgs([]string{"telegraph", "--help"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("telegraph --help failed: %v", err)
	}

	out := buf.String()
	if !strings.Contains(out, "Telegraph") {
		t.Errorf("expected help to mention 'Telegraph', got: %s", out)
	}
}

func TestTelegraphCmd_TgAliasHelp(t *testing.T) {
	cmd := newRootCmd()
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SetArgs([]string{"tg", "--help"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("tg --help failed: %v", err)
	}

	out := buf.String()
	if !strings.Contains(out, "Telegraph") {
		t.Errorf("expected tg alias help to mention 'Telegraph', got: %s", out)
	}
}

func TestRootCmd_HasTelegraphSubcommand(t *testing.T) {
	cmd := newRootCmd()
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetArgs([]string{"--help"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("root --help failed: %v", err)
	}

	if !strings.Contains(buf.String(), "telegraph") {
		t.Error("root help should list 'telegraph' subcommand")
	}
}

// ---------------------------------------------------------------------------
// telegraph start tests
// ---------------------------------------------------------------------------

func TestTelegraphStartCmd_Flags(t *testing.T) {
	cmd := newTelegraphStartCmd()
	if cmd.Use != "start" {
		t.Errorf("Use = %q, want %q", cmd.Use, "start")
	}
	cfgFlag := cmd.Flags().Lookup("config")
	if cfgFlag == nil {
		t.Fatal("expected --config flag")
	}
	if cfgFlag.DefValue != "railyard.yaml" {
		t.Errorf("--config default = %q, want %q", cfgFlag.DefValue, "railyard.yaml")
	}
}

func TestTelegraphStartCmd_MissingConfig(t *testing.T) {
	cmd := newRootCmd()
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SetArgs([]string{"telegraph", "start", "--config", "/nonexistent/railyard.yaml"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error for missing config file")
	}
	if !strings.Contains(err.Error(), "load config") {
		t.Errorf("error = %q, want to contain 'load config'", err.Error())
	}
}

func TestTelegraphStartCmd_Help(t *testing.T) {
	cmd := newRootCmd()
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SetArgs([]string{"telegraph", "start", "--help"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("telegraph start --help failed: %v", err)
	}

	out := buf.String()
	if !strings.Contains(out, "chat platform") {
		t.Errorf("expected help to mention 'chat platform', got: %s", out)
	}
}

// ---------------------------------------------------------------------------
// telegraph status tests
// ---------------------------------------------------------------------------

type mockTelegraphTmux struct {
	sessionExists bool
	panes         []string
	signals       []string
}

func (m *mockTelegraphTmux) SessionExists(name string) bool         { return m.sessionExists }
func (m *mockTelegraphTmux) CreateSession(name string) error        { return nil }
func (m *mockTelegraphTmux) NewPane(session string) (string, error) { return "", nil }
func (m *mockTelegraphTmux) SendKeys(paneID, keys string) error     { return nil }
func (m *mockTelegraphTmux) SendSignal(paneID, signal string) error {
	m.signals = append(m.signals, signal)
	return nil
}
func (m *mockTelegraphTmux) KillPane(paneID string) error               { return nil }
func (m *mockTelegraphTmux) KillSession(name string) error              { return nil }
func (m *mockTelegraphTmux) ListPanes(session string) ([]string, error) { return m.panes, nil }
func (m *mockTelegraphTmux) TileLayout(session string) error            { return nil }

func TestTelegraphStatus_Running(t *testing.T) {
	mock := &mockTelegraphTmux{sessionExists: true}
	orig := tmuxForTelegraph
	tmuxForTelegraph = func() orchestration.Tmux { return mock }
	defer func() { tmuxForTelegraph = orig }()

	cmd := newRootCmd()
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SetArgs([]string{"telegraph", "status"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	out := buf.String()
	if !strings.Contains(out, "RUNNING") {
		t.Errorf("expected 'RUNNING', got: %s", out)
	}
}

func TestTelegraphStatus_Stopped(t *testing.T) {
	mock := &mockTelegraphTmux{sessionExists: false}
	orig := tmuxForTelegraph
	tmuxForTelegraph = func() orchestration.Tmux { return mock }
	defer func() { tmuxForTelegraph = orig }()

	cmd := newRootCmd()
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SetArgs([]string{"telegraph", "status"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	out := buf.String()
	if !strings.Contains(out, "STOPPED") {
		t.Errorf("expected 'STOPPED', got: %s", out)
	}
}

// ---------------------------------------------------------------------------
// telegraph stop tests
// ---------------------------------------------------------------------------

func TestTelegraphStop_NoSession(t *testing.T) {
	mock := &mockTelegraphTmux{sessionExists: false}
	orig := tmuxForTelegraph
	tmuxForTelegraph = func() orchestration.Tmux { return mock }
	defer func() { tmuxForTelegraph = orig }()

	cmd := newRootCmd()
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SetArgs([]string{"telegraph", "stop"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error when no session running")
	}
	if !strings.Contains(err.Error(), "no telegraph session running") {
		t.Errorf("error = %q", err)
	}
}

func TestTelegraphStop_SendsSignal(t *testing.T) {
	mock := &mockTelegraphTmux{
		sessionExists: true,
		panes:         []string{"%0"},
	}
	orig := tmuxForTelegraph
	tmuxForTelegraph = func() orchestration.Tmux { return mock }
	defer func() { tmuxForTelegraph = orig }()

	cmd := newRootCmd()
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SetArgs([]string{"telegraph", "stop"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(mock.signals) != 1 {
		t.Errorf("signals sent = %d, want 1", len(mock.signals))
	}
	if mock.signals[0] != "C-c" {
		t.Errorf("signal = %q, want C-c", mock.signals[0])
	}

	out := buf.String()
	if !strings.Contains(out, "shutdown signal sent") {
		t.Errorf("expected shutdown message, got: %s", out)
	}
}

func TestTelegraphStatusCmd_Help(t *testing.T) {
	cmd := newRootCmd()
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SetArgs([]string{"telegraph", "status", "--help"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("telegraph status --help failed: %v", err)
	}

	out := buf.String()
	if !strings.Contains(out, "status") {
		t.Errorf("expected help to mention status, got: %s", out)
	}
}

func TestTelegraphStopCmd_Help(t *testing.T) {
	cmd := newRootCmd()
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SetArgs([]string{"telegraph", "stop", "--help"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("telegraph stop --help failed: %v", err)
	}

	out := buf.String()
	if !strings.Contains(out, "shutdown") {
		t.Errorf("expected help to mention shutdown, got: %s", out)
	}
}
