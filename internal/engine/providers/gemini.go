package providers

import (
	"context"
	"os/exec"
	"syscall"
	"time"

	"github.com/zulandar/railyard/internal/engine"
)

// GeminiProvider implements AgentProvider for Google's Gemini CLI.
//
// Gemini CLI is a terminal-based AI coding agent from Google.
// Non-interactive operation uses --yes (auto-approve all actions).
// System prompts are passed via --system-instruction.
// Initial prompts are passed via -p flag.
// Output is plain text (no structured JSON), so token parsing returns empty stats.
// Authentication: GEMINI_API_KEY or GOOGLE_API_KEY environment variable.
type GeminiProvider struct {
	Binary string // path to gemini binary; defaults to "gemini"
}

func (p *GeminiProvider) Name() string { return "gemini" }

func (p *GeminiProvider) BuildCommand(ctx context.Context, opts engine.SpawnOpts) (*exec.Cmd, context.CancelFunc) {
	ctx, cancel := context.WithCancel(ctx)

	binary := p.Binary
	if binary == "" {
		binary = "gemini"
	}

	cmd := exec.CommandContext(ctx, binary,
		"--yes",
		"--sandbox",
		"--system-instruction", opts.ContextPayload,
		"-p", "Begin working on your assigned car. Follow the instructions provided.",
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

func (p *GeminiProvider) BuildInteractiveCommand(systemPrompt, workDir string) *exec.Cmd {
	binary := p.Binary
	if binary == "" {
		binary = "gemini"
	}
	cmd := exec.Command(binary,
		"--yes",
		"--sandbox",
		"--system-instruction", systemPrompt,
	)
	if workDir != "" {
		cmd.Dir = workDir
	}
	return cmd
}

func (p *GeminiProvider) BuildPromptCommand(ctx context.Context, prompt string) (*exec.Cmd, context.CancelFunc) {
	ctx, cancel := context.WithCancel(ctx)
	binary := p.Binary
	if binary == "" {
		binary = "gemini"
	}
	cmd := exec.CommandContext(ctx, binary, "--yes", "--sandbox", "-p", prompt)
	return cmd, cancel
}

// ParseOutput returns empty stats — Gemini CLI outputs plain text without
// structured token usage information.
func (p *GeminiProvider) ParseOutput(content string) engine.UsageStats {
	return engine.UsageStats{}
}

func (p *GeminiProvider) ValidateBinary() error {
	binary := p.Binary
	if binary == "" {
		binary = "gemini"
	}
	_, err := exec.LookPath(binary)
	return err
}

func init() {
	engine.RegisterProvider(&GeminiProvider{})
}
