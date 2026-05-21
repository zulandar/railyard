package providers

import (
	"context"
	"os/exec"
	"syscall"
	"time"

	"github.com/zulandar/railyard/internal/engine"
)

// OpenCodeProvider implements AgentProvider for the OpenCode CLI.
//
// OpenCode (github.com/opencode-ai/opencode) is an open-source AI coding agent.
// Non-interactive operation uses --non-interactive flag to suppress TUI.
// System prompts are passed via --system-prompt.
// Initial prompts are passed via --prompt flag.
// Output is plain text (no structured JSON), so token parsing returns empty stats.
// Authentication depends on configured LLM backend (OPENAI_API_KEY, ANTHROPIC_API_KEY, etc.).
//
// Model selection is applied via the `--model` flag (verified 2026-05-21 against
// the opencode docs at opencode.ai/docs/cli/). Per the docs, the value uses
// `provider/model` form (e.g. `anthropic/claude-4.5-sonnet`); callers supplying
// `Model` are expected to use that format. Empty model omits the flag.
type OpenCodeProvider struct {
	Binary string // path to opencode binary; defaults to "opencode"
}

func (p *OpenCodeProvider) Name() string { return "opencode" }

func (p *OpenCodeProvider) BuildCommand(ctx context.Context, opts engine.SpawnOpts) (*exec.Cmd, context.CancelFunc) {
	ctx, cancel := context.WithCancel(ctx)

	binary := p.Binary
	if binary == "" {
		binary = "opencode"
	}

	args := []string{
		"--non-interactive",
		"--system-prompt", opts.ContextPayload,
		"--prompt", "Begin working on your assigned car. Follow the instructions provided.",
	}
	if opts.Model != "" {
		args = append(args, "--model", opts.Model)
	}
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

func (p *OpenCodeProvider) BuildInteractiveCommand(systemPrompt, workDir, model string) *exec.Cmd {
	binary := p.Binary
	if binary == "" {
		binary = "opencode"
	}
	args := []string{"--system-prompt", systemPrompt}
	if model != "" {
		args = append(args, "--model", model)
	}
	cmd := exec.Command(binary, args...)
	if workDir != "" {
		cmd.Dir = workDir
	}
	return cmd
}

func (p *OpenCodeProvider) BuildPromptCommand(ctx context.Context, prompt, model string) (*exec.Cmd, context.CancelFunc) {
	ctx, cancel := context.WithCancel(ctx)
	binary := p.Binary
	if binary == "" {
		binary = "opencode"
	}
	args := []string{"--non-interactive", "--prompt", prompt}
	if model != "" {
		args = append(args, "--model", model)
	}
	cmd := exec.CommandContext(ctx, binary, args...)
	return cmd, cancel
}

// ParseOutput returns empty stats — OpenCode CLI outputs plain text without
// structured token usage information.
func (p *OpenCodeProvider) ParseOutput(content string) engine.UsageStats {
	return engine.UsageStats{}
}

func (p *OpenCodeProvider) ValidateBinary() error {
	binary := p.Binary
	if binary == "" {
		binary = "opencode"
	}
	_, err := exec.LookPath(binary)
	return err
}

func init() {
	engine.RegisterProvider(&OpenCodeProvider{})
}
