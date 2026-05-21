package providers

import (
	"context"
	"os/exec"
	"syscall"
	"time"

	"github.com/zulandar/railyard/internal/engine"
)

// CodexProvider implements AgentProvider for OpenAI's Codex CLI.
//
// Codex CLI (github.com/openai/codex) is a terminal-based coding agent.
// Non-interactive execution uses "codex exec --full-auto <prompt>".
// Interactive sessions use "codex --full-auto <prompt>".
// Output is plain text (no structured JSON), so token parsing returns empty stats.
// Authentication: OPENAI_API_KEY environment variable.
//
// Model selection is applied via the `--model` flag (verified 2026-05-21 against
// the Codex CLI reference at developers.openai.com/codex/cli/reference). Per the
// docs, global flags must be placed AFTER the `exec` subcommand for them to
// take effect on the subcommand. Empty model omits the flag entirely.
type CodexProvider struct {
	Binary string // path to codex binary; defaults to "codex"
}

func (p *CodexProvider) Name() string { return "codex" }

func (p *CodexProvider) BuildCommand(ctx context.Context, opts engine.SpawnOpts) (*exec.Cmd, context.CancelFunc) {
	ctx, cancel := context.WithCancel(ctx)

	binary := p.Binary
	if binary == "" {
		binary = "codex"
	}

	prompt := opts.ContextPayload + "\n\nBegin working on your assigned car. Follow the instructions provided."
	args := []string{"exec", "--full-auto"}
	if opts.Model != "" {
		// Per codex docs, global flags belong AFTER the subcommand to apply.
		args = append(args, "--model", opts.Model)
	}
	args = append(args, prompt)
	cmd := exec.CommandContext(ctx, binary, args...)

	if opts.WorkDir != "" {
		cmd.Dir = opts.WorkDir
	}

	cmd.Cancel = func() error {
		return cmd.Process.Signal(syscall.SIGTERM)
	}
	cmd.WaitDelay = 10 * time.Second

	return cmd, cancel
}

func (p *CodexProvider) BuildInteractiveCommand(systemPrompt, workDir, model string) *exec.Cmd {
	binary := p.Binary
	if binary == "" {
		binary = "codex"
	}
	args := []string{"--full-auto"}
	if model != "" {
		args = append(args, "--model", model)
	}
	args = append(args, systemPrompt)
	cmd := exec.Command(binary, args...)
	if workDir != "" {
		cmd.Dir = workDir
	}
	return cmd
}

func (p *CodexProvider) BuildPromptCommand(ctx context.Context, prompt, model string) (*exec.Cmd, context.CancelFunc) {
	ctx, cancel := context.WithCancel(ctx)
	binary := p.Binary
	if binary == "" {
		binary = "codex"
	}
	args := []string{"exec", "--full-auto"}
	if model != "" {
		args = append(args, "--model", model)
	}
	args = append(args, prompt)
	cmd := exec.CommandContext(ctx, binary, args...)
	return cmd, cancel
}

// ParseOutput returns empty stats — Codex CLI outputs plain text without
// structured token usage information.
func (p *CodexProvider) ParseOutput(content string) engine.UsageStats {
	return engine.UsageStats{}
}

func (p *CodexProvider) ValidateBinary() error {
	binary := p.Binary
	if binary == "" {
		binary = "codex"
	}
	_, err := exec.LookPath(binary)
	return err
}

func init() {
	engine.RegisterProvider(&CodexProvider{})
}
