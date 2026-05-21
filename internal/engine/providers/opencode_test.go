package providers

import (
	"context"
	"strings"
	"testing"

	"github.com/zulandar/railyard/internal/engine"
)

func TestOpenCodeProvider_Name(t *testing.T) {
	p := &OpenCodeProvider{}
	if p.Name() != "opencode" {
		t.Errorf("Name() = %q, want %q", p.Name(), "opencode")
	}
}

func TestOpenCodeProvider_BuildCommand_DefaultBinary(t *testing.T) {
	p := &OpenCodeProvider{}
	cmd, cancel := p.BuildCommand(context.Background(), engine.SpawnOpts{
		ContextPayload: "test context",
	})
	defer cancel()

	if cmd.Args[0] != "opencode" {
		t.Errorf("binary = %q, want %q", cmd.Args[0], "opencode")
	}
}

func TestOpenCodeProvider_BuildCommand_CustomBinary(t *testing.T) {
	p := &OpenCodeProvider{Binary: "/usr/local/bin/opencode"}
	cmd, cancel := p.BuildCommand(context.Background(), engine.SpawnOpts{
		ContextPayload: "test context",
	})
	defer cancel()

	if cmd.Args[0] != "/usr/local/bin/opencode" {
		t.Errorf("binary = %q, want %q", cmd.Args[0], "/usr/local/bin/opencode")
	}
}

func TestOpenCodeProvider_BuildCommand_RequiredFlags(t *testing.T) {
	p := &OpenCodeProvider{}
	cmd, cancel := p.BuildCommand(context.Background(), engine.SpawnOpts{
		ContextPayload: "my context",
		WorkDir:        "/tmp/work",
	})
	defer cancel()

	args := strings.Join(cmd.Args, " ")
	for _, flag := range []string{
		"--non-interactive",
		"--system-prompt my context",
		"--prompt",
	} {
		if !strings.Contains(args, flag) {
			t.Errorf("missing flag %q in args: %s", flag, args)
		}
	}

	if cmd.Dir != "/tmp/work" {
		t.Errorf("Dir = %q, want %q", cmd.Dir, "/tmp/work")
	}
}

func TestOpenCodeProvider_BuildCommand_Cancel(t *testing.T) {
	p := &OpenCodeProvider{}
	cmd, cancel := p.BuildCommand(context.Background(), engine.SpawnOpts{
		ContextPayload: "test",
	})
	defer cancel()

	if cmd.Cancel == nil {
		t.Error("cmd.Cancel should be set (SIGTERM handler)")
	}
}

func TestOpenCodeProvider_ParseOutput_ReturnsEmpty(t *testing.T) {
	p := &OpenCodeProvider{}
	stats := p.ParseOutput("some plain text output\nmore output")
	if stats.InputTokens != 0 || stats.OutputTokens != 0 {
		t.Errorf("expected zero stats for plain text, got %+v", stats)
	}
	if stats.Model != "" {
		t.Errorf("Model = %q, want empty", stats.Model)
	}
}

func TestOpenCodeProvider_ValidateBinary_Missing(t *testing.T) {
	p := &OpenCodeProvider{Binary: "/nonexistent/path/to/opencode-xyz"}
	err := p.ValidateBinary()
	if err == nil {
		t.Error("expected error for missing binary")
	}
}

func TestOpenCodeProvider_RegisteredViaInit(t *testing.T) {
	got, err := engine.GetProvider("opencode")
	if err != nil {
		t.Fatalf("GetProvider(opencode): %v", err)
	}
	if got.Name() != "opencode" {
		t.Errorf("Name() = %q, want %q", got.Name(), "opencode")
	}
}

func TestOpenCodeProvider_BuildCommand_ModelAddsFlag(t *testing.T) {
	p := &OpenCodeProvider{}
	cmd, cancel := p.BuildCommand(context.Background(), engine.SpawnOpts{
		ContextPayload: "ctx",
		Model:          "anthropic/claude-4.5-sonnet",
	})
	defer cancel()

	if !argsContainSequence(cmd.Args, "--model", "anthropic/claude-4.5-sonnet") {
		t.Errorf("expected --model anthropic/claude-4.5-sonnet in cmd.Args, got: %v", cmd.Args)
	}
}

func TestOpenCodeProvider_BuildCommand_NoModelOmitsFlag(t *testing.T) {
	p := &OpenCodeProvider{}
	cmd, cancel := p.BuildCommand(context.Background(), engine.SpawnOpts{
		ContextPayload: "ctx",
	})
	defer cancel()

	for _, a := range cmd.Args {
		if a == "--model" {
			t.Errorf("expected no --model flag when Model empty, got args: %v", cmd.Args)
		}
	}
}

func TestOpenCodeProvider_BuildInteractiveCommand_ModelAddsFlag(t *testing.T) {
	p := &OpenCodeProvider{}
	cmd := p.BuildInteractiveCommand("sys", "/tmp/work", "openai/gpt-5")

	if !argsContainSequence(cmd.Args, "--model", "openai/gpt-5") {
		t.Errorf("expected --model openai/gpt-5 in cmd.Args, got: %v", cmd.Args)
	}
}

func TestOpenCodeProvider_BuildInteractiveCommand_NoModelOmitsFlag(t *testing.T) {
	p := &OpenCodeProvider{}
	cmd := p.BuildInteractiveCommand("sys", "/tmp/work", "")

	for _, a := range cmd.Args {
		if a == "--model" {
			t.Errorf("expected no --model flag when model empty, got args: %v", cmd.Args)
		}
	}
}

func TestOpenCodeProvider_BuildPromptCommand_ModelAddsFlag(t *testing.T) {
	p := &OpenCodeProvider{}
	cmd, cancel := p.BuildPromptCommand(context.Background(), "do thing", "anthropic/claude-4.5-sonnet")
	defer cancel()

	if !argsContainSequence(cmd.Args, "--model", "anthropic/claude-4.5-sonnet") {
		t.Errorf("expected --model anthropic/claude-4.5-sonnet in cmd.Args, got: %v", cmd.Args)
	}
}

func TestOpenCodeProvider_BuildPromptCommand_NoModelOmitsFlag(t *testing.T) {
	p := &OpenCodeProvider{}
	cmd, cancel := p.BuildPromptCommand(context.Background(), "do thing", "")
	defer cancel()

	for _, a := range cmd.Args {
		if a == "--model" {
			t.Errorf("expected no --model flag when model empty, got args: %v", cmd.Args)
		}
	}
}
