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

	cmd := exec.CommandContext(ctx, binary,
		"--non-interactive",
		"--system-prompt", opts.ContextPayload,
		"--prompt", "Begin working on your assigned car. Follow the instructions provided.",
	)

	if opts.WorkDir != "" {
		cmd.Dir = opts.WorkDir
	}

	cmd.Cancel = func() error {
		return cmd.Process.Signal(syscall.SIGTERM)
	}
	cmd.WaitDelay = 10 * time.Second

	return cmd, cancel
}

func (p *OpenCodeProvider) BuildInteractiveCommand(systemPrompt, workDir string) *exec.Cmd {
	binary := p.Binary
	if binary == "" {
		binary = "opencode"
	}
	cmd := exec.Command(binary,
		"--system-prompt", systemPrompt,
	)
	if workDir != "" {
		cmd.Dir = workDir
	}
	return cmd
}

func (p *OpenCodeProvider) BuildPromptCommand(ctx context.Context, prompt string) (*exec.Cmd, context.CancelFunc) {
	ctx, cancel := context.WithCancel(ctx)
	binary := p.Binary
	if binary == "" {
		binary = "opencode"
	}
	cmd := exec.CommandContext(ctx, binary, "--non-interactive", "--prompt", prompt)
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
