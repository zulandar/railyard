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

// envHas returns true if env contains an entry exactly equal to want.
func envHas(t *testing.T, env []string, want string) bool {
	t.Helper()
	for _, e := range env {
		if e == want {
			return true
		}
	}
	return false
}

// envHasPrefix returns true if env contains any entry with the given prefix.
func envHasPrefix(t *testing.T, env []string, prefix string) bool {
	t.Helper()
	for _, e := range env {
		if strings.HasPrefix(e, prefix) {
			return true
		}
	}
	return false
}

func TestClaudeProvider_BuildCommand_ModelSetsEnv(t *testing.T) {
	p := &ClaudeProvider{}
	cmd, cancel := p.BuildCommand(context.Background(), engine.SpawnOpts{
		ContextPayload: "ctx",
		Model:          "claude-opus-4-7",
	})
	defer cancel()

	if !envHas(t, cmd.Env, "ANTHROPIC_MODEL=claude-opus-4-7") {
		t.Errorf("expected ANTHROPIC_MODEL=claude-opus-4-7 in cmd.Env, got: %v", cmd.Env)
	}
}

func TestClaudeProvider_BuildCommand_NoModelLeavesEnvUnset(t *testing.T) {
	p := &ClaudeProvider{}
	cmd, cancel := p.BuildCommand(context.Background(), engine.SpawnOpts{
		ContextPayload: "ctx",
	})
	defer cancel()

	if envHasPrefix(t, cmd.Env, "ANTHROPIC_MODEL=") {
		t.Errorf("expected ANTHROPIC_MODEL not present in cmd.Env when Model empty, got: %v", cmd.Env)
	}
}

func TestClaudeProvider_BuildInteractiveCommand_ModelSetsEnv(t *testing.T) {
	p := &ClaudeProvider{}
	cmd := p.BuildInteractiveCommand("sys", "/tmp/work", "claude-sonnet-4-6")

	if !envHas(t, cmd.Env, "ANTHROPIC_MODEL=claude-sonnet-4-6") {
		t.Errorf("expected ANTHROPIC_MODEL in cmd.Env, got: %v", cmd.Env)
	}
}

func TestClaudeProvider_BuildInteractiveCommand_NoModelLeavesEnvUnset(t *testing.T) {
	p := &ClaudeProvider{}
	cmd := p.BuildInteractiveCommand("sys", "/tmp/work", "")

	if envHasPrefix(t, cmd.Env, "ANTHROPIC_MODEL=") {
		t.Errorf("expected no ANTHROPIC_MODEL in cmd.Env, got: %v", cmd.Env)
	}
}

func TestClaudeProvider_BuildPromptCommand_ModelSetsEnv(t *testing.T) {
	p := &ClaudeProvider{}
	cmd, cancel := p.BuildPromptCommand(context.Background(), "do thing", "claude-haiku-4-5")
	defer cancel()

	if !envHas(t, cmd.Env, "ANTHROPIC_MODEL=claude-haiku-4-5") {
		t.Errorf("expected ANTHROPIC_MODEL in cmd.Env, got: %v", cmd.Env)
	}
}

func TestClaudeProvider_BuildPromptCommand_NoModelLeavesEnvUnset(t *testing.T) {
	p := &ClaudeProvider{}
	cmd, cancel := p.BuildPromptCommand(context.Background(), "do thing", "")
	defer cancel()

	if envHasPrefix(t, cmd.Env, "ANTHROPIC_MODEL=") {
		t.Errorf("expected no ANTHROPIC_MODEL in cmd.Env, got: %v", cmd.Env)
	}
}
