package providers

import (
	"context"
	"strings"
	"testing"

	"github.com/zulandar/railyard/internal/engine"
)

func TestCopilotProvider_Name(t *testing.T) {
	p := &CopilotProvider{}
	if p.Name() != "copilot" {
		t.Errorf("Name() = %q, want %q", p.Name(), "copilot")
	}
}

func TestCopilotProvider_BuildCommand_DefaultBinary(t *testing.T) {
	p := &CopilotProvider{}
	cmd, cancel := p.BuildCommand(context.Background(), engine.SpawnOpts{
		ContextPayload: "test context",
	})
	defer cancel()

	if cmd.Args[0] != "copilot" {
		t.Errorf("binary = %q, want %q", cmd.Args[0], "copilot")
	}
}

func TestCopilotProvider_BuildCommand_CustomBinary(t *testing.T) {
	p := &CopilotProvider{Binary: "/usr/local/bin/copilot"}
	cmd, cancel := p.BuildCommand(context.Background(), engine.SpawnOpts{
		ContextPayload: "test context",
	})
	defer cancel()

	if cmd.Args[0] != "/usr/local/bin/copilot" {
		t.Errorf("binary = %q, want %q", cmd.Args[0], "/usr/local/bin/copilot")
	}
}

func TestCopilotProvider_BuildCommand_RequiredFlags(t *testing.T) {
	p := &CopilotProvider{}
	cmd, cancel := p.BuildCommand(context.Background(), engine.SpawnOpts{
		ContextPayload: "my context",
		WorkDir:        "/tmp/work",
	})
	defer cancel()

	args := strings.Join(cmd.Args, " ")
	for _, flag := range []string{
		"--auto-approve",
		"--system-prompt my context",
		"-p",
	} {
		if !strings.Contains(args, flag) {
			t.Errorf("missing flag %q in args: %s", flag, args)
		}
	}

	if cmd.Dir != "/tmp/work" {
		t.Errorf("Dir = %q, want %q", cmd.Dir, "/tmp/work")
	}
}

func TestCopilotProvider_BuildCommand_Cancel(t *testing.T) {
	p := &CopilotProvider{}
	cmd, cancel := p.BuildCommand(context.Background(), engine.SpawnOpts{
		ContextPayload: "test",
	})
	defer cancel()

	if cmd.Cancel == nil {
		t.Error("cmd.Cancel should be set (SIGTERM handler)")
	}
}

func TestCopilotProvider_BuildInteractiveCommand(t *testing.T) {
	p := &CopilotProvider{}
	cmd := p.BuildInteractiveCommand("my system prompt", "/tmp/work")

	if cmd.Args[0] != "copilot" {
		t.Errorf("binary = %q, want %q", cmd.Args[0], "copilot")
	}

	args := strings.Join(cmd.Args, " ")
	if !strings.Contains(args, "--system-prompt my system prompt") {
		t.Errorf("missing --system-prompt flag in args: %s", args)
	}

	if strings.Contains(args, "--auto-approve") {
		t.Errorf("interactive command should NOT contain --auto-approve, got: %s", args)
	}

	if cmd.Dir != "/tmp/work" {
		t.Errorf("Dir = %q, want %q", cmd.Dir, "/tmp/work")
	}
}

func TestCopilotProvider_BuildPromptCommand(t *testing.T) {
	p := &CopilotProvider{}
	cmd, cancel := p.BuildPromptCommand(context.Background(), "do something")
	defer cancel()

	if cmd.Args[0] != "copilot" {
		t.Errorf("binary = %q, want %q", cmd.Args[0], "copilot")
	}

	args := strings.Join(cmd.Args, " ")
	if !strings.Contains(args, "--auto-approve") {
		t.Errorf("missing --auto-approve flag in args: %s", args)
	}
	if !strings.Contains(args, "-p") {
		t.Errorf("missing -p flag in args: %s", args)
	}
}

func TestCopilotProvider_ParseOutput_ReturnsEmpty(t *testing.T) {
	p := &CopilotProvider{}
	stats := p.ParseOutput("some plain text output\nmore output")
	if stats.InputTokens != 0 || stats.OutputTokens != 0 {
		t.Errorf("expected zero stats for plain text, got %+v", stats)
	}
	if stats.Model != "" {
		t.Errorf("Model = %q, want empty", stats.Model)
	}
}

func TestCopilotProvider_ValidateBinary_Missing(t *testing.T) {
	p := &CopilotProvider{Binary: "/nonexistent/path/to/copilot-xyz"}
	err := p.ValidateBinary()
	if err == nil {
		t.Error("expected error for missing binary")
	}
}

func TestCopilotProvider_RegisteredViaInit(t *testing.T) {
	got, err := engine.GetProvider("copilot")
	if err != nil {
		t.Fatalf("GetProvider(copilot): %v", err)
	}
	if got.Name() != "copilot" {
		t.Errorf("Name() = %q, want %q", got.Name(), "copilot")
	}
}
