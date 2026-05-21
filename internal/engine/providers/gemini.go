package providers

import (
	"context"
	"os"
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
//
// Model selection is applied via the GEMINI_MODEL environment variable
// (verified 2026-05-21 against gemini-cli docs and discussion #1824 at
// github.com/google-gemini/gemini-cli). The CLI also supports a `--model`
// flag, but the env var mirrors the claude provider's pattern and avoids
// touching arg positions across three Build methods. Empty model leaves the
// env var unset, preserving the CLI's default.
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

	if opts.Model != "" {
		cmd.Env = append(os.Environ(), "GEMINI_MODEL="+opts.Model)
	}

	cmd.Cancel = func() error {
		return cmd.Process.Signal(syscall.SIGTERM)
	}
	cmd.WaitDelay = 10 * time.Second

	return cmd, cancel
}

func (p *GeminiProvider) BuildInteractiveCommand(systemPrompt, workDir, model string) *exec.Cmd {
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
	if model != "" {
		cmd.Env = append(os.Environ(), "GEMINI_MODEL="+model)
	}
	return cmd
}

func (p *GeminiProvider) BuildPromptCommand(ctx context.Context, prompt, model string) (*exec.Cmd, context.CancelFunc) {
	ctx, cancel := context.WithCancel(ctx)
	binary := p.Binary
	if binary == "" {
		binary = "gemini"
	}
	cmd := exec.CommandContext(ctx, binary, "--yes", "--sandbox", "-p", prompt)
	if model != "" {
		cmd.Env = append(os.Environ(), "GEMINI_MODEL="+model)
	}
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
