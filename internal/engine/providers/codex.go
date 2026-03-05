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
// Non-interactive operation requires --quiet and --full-auto flags.
// System prompts are passed via --instructions, initial prompts as positional args.
// Output is plain text (no structured JSON), so token parsing returns empty stats.
// Authentication: OPENAI_API_KEY environment variable.
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

	cmd := exec.CommandContext(ctx, binary,
		"--quiet",
		"--full-auto",
		"--instructions", opts.ContextPayload,
		"Begin working on your assigned car. Follow the instructions provided.",
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
