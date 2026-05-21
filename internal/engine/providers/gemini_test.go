package providers

import (
	"context"
	"strings"
	"testing"

	"github.com/zulandar/railyard/internal/engine"
)

func TestGeminiProvider_Name(t *testing.T) {
	p := &GeminiProvider{}
	if p.Name() != "gemini" {
		t.Errorf("Name() = %q, want %q", p.Name(), "gemini")
	}
}

func TestGeminiProvider_BuildCommand_DefaultBinary(t *testing.T) {
	p := &GeminiProvider{}
	cmd, cancel := p.BuildCommand(context.Background(), engine.SpawnOpts{
		ContextPayload: "test context",
	})
	defer cancel()

	if cmd.Args[0] != "gemini" {
		t.Errorf("binary = %q, want %q", cmd.Args[0], "gemini")
	}
}

func TestGeminiProvider_BuildCommand_CustomBinary(t *testing.T) {
	p := &GeminiProvider{Binary: "/usr/local/bin/gemini"}
	cmd, cancel := p.BuildCommand(context.Background(), engine.SpawnOpts{
		ContextPayload: "test context",
	})
	defer cancel()

	if cmd.Args[0] != "/usr/local/bin/gemini" {
		t.Errorf("binary = %q, want %q", cmd.Args[0], "/usr/local/bin/gemini")
	}
}

func TestGeminiProvider_BuildCommand_RequiredFlags(t *testing.T) {
	p := &GeminiProvider{}
	cmd, cancel := p.BuildCommand(context.Background(), engine.SpawnOpts{
		ContextPayload: "my context",
		WorkDir:        "/tmp/work",
	})
	defer cancel()

	args := strings.Join(cmd.Args, " ")
	for _, flag := range []string{
		"--yes",
		"--sandbox",
		"--system-instruction my context",
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

func TestGeminiProvider_BuildCommand_Cancel(t *testing.T) {
	p := &GeminiProvider{}
	cmd, cancel := p.BuildCommand(context.Background(), engine.SpawnOpts{
		ContextPayload: "test",
	})
	defer cancel()

	if cmd.Cancel == nil {
		t.Error("cmd.Cancel should be set (SIGTERM handler)")
	}
}

func TestGeminiProvider_ParseOutput_ReturnsEmpty(t *testing.T) {
	p := &GeminiProvider{}
	stats := p.ParseOutput("some plain text output\nmore output")
	if stats.InputTokens != 0 || stats.OutputTokens != 0 {
		t.Errorf("expected zero stats for plain text, got %+v", stats)
	}
	if stats.Model != "" {
		t.Errorf("Model = %q, want empty", stats.Model)
	}
}

func TestGeminiProvider_ValidateBinary_Missing(t *testing.T) {
	p := &GeminiProvider{Binary: "/nonexistent/path/to/gemini-xyz"}
	err := p.ValidateBinary()
	if err == nil {
		t.Error("expected error for missing binary")
	}
}

func TestGeminiProvider_RegisteredViaInit(t *testing.T) {
	got, err := engine.GetProvider("gemini")
	if err != nil {
		t.Fatalf("GetProvider(gemini): %v", err)
	}
	if got.Name() != "gemini" {
		t.Errorf("Name() = %q, want %q", got.Name(), "gemini")
	}
}

func TestGeminiProvider_BuildCommand_ModelSetsEnv(t *testing.T) {
	p := &GeminiProvider{}
	cmd, cancel := p.BuildCommand(context.Background(), engine.SpawnOpts{
		ContextPayload: "ctx",
		Model:          "gemini-2.5-pro",
	})
	defer cancel()

	if !envHas(t, cmd.Env, "GEMINI_MODEL=gemini-2.5-pro") {
		t.Errorf("expected GEMINI_MODEL=gemini-2.5-pro in cmd.Env, got: %v", cmd.Env)
	}
}

func TestGeminiProvider_BuildCommand_NoModelLeavesEnvUnset(t *testing.T) {
	p := &GeminiProvider{}
	cmd, cancel := p.BuildCommand(context.Background(), engine.SpawnOpts{
		ContextPayload: "ctx",
	})
	defer cancel()

	if envHasPrefix(t, cmd.Env, "GEMINI_MODEL=") {
		t.Errorf("expected no GEMINI_MODEL in cmd.Env when Model empty, got: %v", cmd.Env)
	}
}

func TestGeminiProvider_BuildInteractiveCommand_ModelSetsEnv(t *testing.T) {
	p := &GeminiProvider{}
	cmd := p.BuildInteractiveCommand("sys", "/tmp/work", "gemini-2.5-flash")

	if !envHas(t, cmd.Env, "GEMINI_MODEL=gemini-2.5-flash") {
		t.Errorf("expected GEMINI_MODEL in cmd.Env, got: %v", cmd.Env)
	}
}

func TestGeminiProvider_BuildInteractiveCommand_NoModelLeavesEnvUnset(t *testing.T) {
	p := &GeminiProvider{}
	cmd := p.BuildInteractiveCommand("sys", "/tmp/work", "")

	if envHasPrefix(t, cmd.Env, "GEMINI_MODEL=") {
		t.Errorf("expected no GEMINI_MODEL in cmd.Env, got: %v", cmd.Env)
	}
}

func TestGeminiProvider_BuildPromptCommand_ModelSetsEnv(t *testing.T) {
	p := &GeminiProvider{}
	cmd, cancel := p.BuildPromptCommand(context.Background(), "do thing", "gemini-2.5-pro")
	defer cancel()

	if !envHas(t, cmd.Env, "GEMINI_MODEL=gemini-2.5-pro") {
		t.Errorf("expected GEMINI_MODEL in cmd.Env, got: %v", cmd.Env)
	}
}

func TestGeminiProvider_BuildPromptCommand_NoModelLeavesEnvUnset(t *testing.T) {
	p := &GeminiProvider{}
	cmd, cancel := p.BuildPromptCommand(context.Background(), "do thing", "")
	defer cancel()

	if envHasPrefix(t, cmd.Env, "GEMINI_MODEL=") {
		t.Errorf("expected no GEMINI_MODEL in cmd.Env, got: %v", cmd.Env)
	}
}
