package providers

import (
	"context"
	"os"
	"os/exec"
	"syscall"
	"time"

	"github.com/zulandar/railyard/internal/engine"
)

// ClaudeProvider implements AgentProvider for Claude Code CLI.
//
// Model selection is applied via the ANTHROPIC_MODEL environment variable,
// which the Claude Code CLI honors natively. Empty model leaves the env var
// unset, preserving the CLI's default model selection.
type ClaudeProvider struct {
	Binary string // path to claude binary; defaults to "claude"
}

func (p *ClaudeProvider) Name() string { return "claude" }

func (p *ClaudeProvider) BuildCommand(ctx context.Context, opts engine.SpawnOpts) (*exec.Cmd, context.CancelFunc) {
	ctx, cancel := context.WithCancel(ctx)

	binary := p.Binary
	if binary == "" {
		binary = opts.ClaudeBinary
	}
	if binary == "" {
		binary = "claude"
	}

	cmd := exec.CommandContext(ctx, binary,
		"--dangerously-skip-permissions",
		"--verbose",
		"--output-format", "stream-json",
		"--system-prompt", opts.ContextPayload,
		"-p", "Begin working on your assigned car. Follow the instructions in the system prompt.",
	)

	if opts.WorkDir != "" {
		cmd.Dir = opts.WorkDir
	}

	if opts.Model != "" {
		cmd.Env = append(os.Environ(), "ANTHROPIC_MODEL="+opts.Model)
	}

	cmd.Cancel = func() error {
		return cmd.Process.Signal(syscall.SIGTERM)
	}
	cmd.WaitDelay = 10 * time.Second

	return cmd, cancel
}

func (p *ClaudeProvider) BuildInteractiveCommand(systemPrompt, workDir, model string) *exec.Cmd {
	binary := p.Binary
	if binary == "" {
		binary = "claude"
	}
	cmd := exec.Command(binary,
		"--dangerously-skip-permissions",
		"--append-system-prompt", systemPrompt,
	)
	if workDir != "" {
		cmd.Dir = workDir
	}
	if model != "" {
		cmd.Env = append(os.Environ(), "ANTHROPIC_MODEL="+model)
	}
	return cmd
}

func (p *ClaudeProvider) BuildPromptCommand(ctx context.Context, prompt, model string) (*exec.Cmd, context.CancelFunc) {
	ctx, cancel := context.WithCancel(ctx)
	binary := p.Binary
	if binary == "" {
		binary = "claude"
	}
	cmd := exec.CommandContext(ctx, binary, "-p", prompt)
	if model != "" {
		cmd.Env = append(os.Environ(), "ANTHROPIC_MODEL="+model)
	}
	return cmd, cancel
}

func (p *ClaudeProvider) ParseOutput(content string) engine.UsageStats {
	return engine.ParseUsageFromContent(content)
}

func (p *ClaudeProvider) ValidateBinary() error {
	binary := p.Binary
	if binary == "" {
		binary = "claude"
	}
	_, err := exec.LookPath(binary)
	return err
}

func init() {
	engine.RegisterProvider(&ClaudeProvider{})
}
