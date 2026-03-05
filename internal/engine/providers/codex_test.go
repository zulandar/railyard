package providers

import (
	"context"
	"strings"
	"testing"

	"github.com/zulandar/railyard/internal/engine"
)

func TestCodexProvider_Name(t *testing.T) {
	p := &CodexProvider{}
	if p.Name() != "codex" {
		t.Errorf("Name() = %q, want %q", p.Name(), "codex")
	}
}

func TestCodexProvider_BuildCommand_DefaultBinary(t *testing.T) {
	p := &CodexProvider{}
	cmd, cancel := p.BuildCommand(context.Background(), engine.SpawnOpts{
		ContextPayload: "test context",
	})
	defer cancel()

	if cmd.Args[0] != "codex" {
		t.Errorf("binary = %q, want %q", cmd.Args[0], "codex")
	}
}

func TestCodexProvider_BuildCommand_CustomBinary(t *testing.T) {
	p := &CodexProvider{Binary: "/usr/local/bin/codex"}
	cmd, cancel := p.BuildCommand(context.Background(), engine.SpawnOpts{
		ContextPayload: "test context",
	})
	defer cancel()

	if cmd.Args[0] != "/usr/local/bin/codex" {
		t.Errorf("binary = %q, want %q", cmd.Args[0], "/usr/local/bin/codex")
	}
}

func TestCodexProvider_BuildCommand_RequiredFlags(t *testing.T) {
	p := &CodexProvider{}
	cmd, cancel := p.BuildCommand(context.Background(), engine.SpawnOpts{
		ContextPayload: "my context",
		WorkDir:        "/tmp/work",
	})
	defer cancel()

	args := strings.Join(cmd.Args, " ")
	for _, flag := range []string{
		"exec",
		"--full-auto",
	} {
		if !strings.Contains(args, flag) {
			t.Errorf("missing flag %q in args: %s", flag, args)
		}
	}
	// Context payload should be included in the prompt (last positional arg)
	lastArg := cmd.Args[len(cmd.Args)-1]
	if !strings.Contains(lastArg, "my context") {
		t.Errorf("last arg should contain context payload, got: %s", lastArg)
	}
	// Should NOT contain --instructions (removed; not a valid codex flag)
	if strings.Contains(args, "--instructions") {
		t.Errorf("should not contain --instructions flag, got: %s", args)
	}

	if cmd.Dir != "/tmp/work" {
		t.Errorf("Dir = %q, want %q", cmd.Dir, "/tmp/work")
	}
}

func TestCodexProvider_BuildCommand_Cancel(t *testing.T) {
	p := &CodexProvider{}
	cmd, cancel := p.BuildCommand(context.Background(), engine.SpawnOpts{
		ContextPayload: "test",
	})
	defer cancel()

	if cmd.Cancel == nil {
		t.Error("cmd.Cancel should be set (SIGTERM handler)")
	}
}

func TestCodexProvider_ParseOutput_ReturnsEmpty(t *testing.T) {
	p := &CodexProvider{}
	stats := p.ParseOutput("some plain text output\nmore output")
	if stats.InputTokens != 0 || stats.OutputTokens != 0 {
		t.Errorf("expected zero stats for plain text, got %+v", stats)
	}
	if stats.Model != "" {
		t.Errorf("Model = %q, want empty", stats.Model)
	}
}

func TestCodexProvider_ValidateBinary_Missing(t *testing.T) {
	p := &CodexProvider{Binary: "/nonexistent/path/to/codex-xyz"}
	err := p.ValidateBinary()
	if err == nil {
		t.Error("expected error for missing binary")
	}
}

func TestCodexProvider_RegisteredViaInit(t *testing.T) {
	got, err := engine.GetProvider("codex")
	if err != nil {
		t.Fatalf("GetProvider(codex): %v", err)
	}
	if got.Name() != "codex" {
		t.Errorf("Name() = %q, want %q", got.Name(), "codex")
	}
}
