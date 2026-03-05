package providers

import (
	"context"
	"strings"
	"testing"

	"github.com/zulandar/railyard/internal/engine"
)

func TestClaudeProvider_Name(t *testing.T) {
	p := &ClaudeProvider{}
	if p.Name() != "claude" {
		t.Errorf("Name() = %q, want %q", p.Name(), "claude")
	}
}

func TestClaudeProvider_BuildCommand_DefaultBinary(t *testing.T) {
	p := &ClaudeProvider{}
	cmd, cancel := p.BuildCommand(context.Background(), engine.SpawnOpts{
		ContextPayload: "test context",
	})
	defer cancel()

	if cmd.Args[0] != "claude" {
		t.Errorf("binary = %q, want %q", cmd.Args[0], "claude")
	}
}

func TestClaudeProvider_BuildCommand_CustomBinary(t *testing.T) {
	p := &ClaudeProvider{Binary: "/usr/local/bin/claude"}
	cmd, cancel := p.BuildCommand(context.Background(), engine.SpawnOpts{
		ContextPayload: "test context",
	})
	defer cancel()

	if cmd.Args[0] != "/usr/local/bin/claude" {
		t.Errorf("binary = %q, want %q", cmd.Args[0], "/usr/local/bin/claude")
	}
}

func TestClaudeProvider_BuildCommand_FallbackToOptsBinary(t *testing.T) {
	p := &ClaudeProvider{} // no Binary set
	cmd, cancel := p.BuildCommand(context.Background(), engine.SpawnOpts{
		ClaudeBinary:   "/opt/claude",
		ContextPayload: "test",
	})
	defer cancel()

	if cmd.Args[0] != "/opt/claude" {
		t.Errorf("binary = %q, want %q", cmd.Args[0], "/opt/claude")
	}
}

func TestClaudeProvider_BuildCommand_RequiredFlags(t *testing.T) {
	p := &ClaudeProvider{}
	cmd, cancel := p.BuildCommand(context.Background(), engine.SpawnOpts{
		ContextPayload: "my context",
		WorkDir:        "/tmp/work",
	})
	defer cancel()

	args := strings.Join(cmd.Args, " ")
	for _, flag := range []string{
		"--dangerously-skip-permissions",
		"--verbose",
		"--output-format stream-json",
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

func TestClaudeProvider_BuildCommand_Cancel(t *testing.T) {
	p := &ClaudeProvider{}
	cmd, cancel := p.BuildCommand(context.Background(), engine.SpawnOpts{
		ContextPayload: "test",
	})
	defer cancel()

	if cmd.Cancel == nil {
		t.Error("cmd.Cancel should be set (SIGTERM handler)")
	}
}

func TestClaudeProvider_ParseOutput(t *testing.T) {
	p := &ClaudeProvider{}
	content := `{"type":"result","subtype":"success","usage":{"input_tokens":500,"output_tokens":100}}`
	stats := p.ParseOutput(content)
	if stats.InputTokens != 500 {
		t.Errorf("InputTokens = %d, want 500", stats.InputTokens)
	}
	if stats.OutputTokens != 100 {
		t.Errorf("OutputTokens = %d, want 100", stats.OutputTokens)
	}
}

func TestClaudeProvider_ParseOutput_Empty(t *testing.T) {
	p := &ClaudeProvider{}
	stats := p.ParseOutput("")
	if stats.InputTokens != 0 || stats.OutputTokens != 0 {
		t.Errorf("expected zero stats for empty content, got %+v", stats)
	}
}

func TestClaudeProvider_ValidateBinary_Missing(t *testing.T) {
	p := &ClaudeProvider{Binary: "/nonexistent/path/to/claude-xyz"}
	err := p.ValidateBinary()
	if err == nil {
		t.Error("expected error for missing binary")
	}
}

func TestClaudeProvider_RegisteredViaInit(t *testing.T) {
	// The init() function should have registered the claude provider
	got, err := engine.GetProvider("claude")
	if err != nil {
		t.Fatalf("GetProvider(claude): %v", err)
	}
	if got.Name() != "claude" {
		t.Errorf("Name() = %q, want %q", got.Name(), "claude")
	}
}
