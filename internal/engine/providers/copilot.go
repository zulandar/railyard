package providers

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"

	"github.com/zulandar/railyard/internal/engine"
)

// CopilotProvider implements AgentProvider for GitHub's Copilot CLI.
//
// Copilot CLI is a terminal-based AI coding agent from GitHub.
// Non-interactive operation uses --auto-approve (auto-approve all actions).
// System prompts are passed via --system-prompt.
// Initial prompts are passed via -p flag.
// Output is plain text (no structured JSON), so token parsing returns empty stats.
type CopilotProvider struct {
	Binary string // path to copilot binary; defaults to "copilot"
}

func (p *CopilotProvider) Name() string { return "copilot" }

// copilotEnv returns a copy of os.Environ with GH_TOKEN set to
// GITHUB_COPILOT_TOKEN when that variable is present. This keeps the
// pod-level GH_TOKEN (repo-scoped, used by gh CLI for PRs) intact while
// giving the copilot subprocess its own Copilot-scoped credential.
func copilotEnv() []string {
	tok := os.Getenv("GITHUB_COPILOT_TOKEN")
	if tok == "" {
		return nil // inherit process env as-is
	}
	env := os.Environ()
	out := make([]string, 0, len(env))
	for _, e := range env {
		if strings.HasPrefix(e, "GH_TOKEN=") {
			continue // drop; replaced below
		}
		out = append(out, e)
	}
	return append(out, "GH_TOKEN="+tok)
}

func (p *CopilotProvider) BuildCommand(ctx context.Context, opts engine.SpawnOpts) (*exec.Cmd, context.CancelFunc) {
	ctx, cancel := context.WithCancel(ctx)

	binary := p.Binary
	if binary == "" {
		binary = "copilot"
	}

	cmd := exec.CommandContext(ctx, binary,
		"--auto-approve",
		"--system-prompt", opts.ContextPayload,
		"-p", "Begin working on your assigned car. Follow the instructions provided.",
	)

	if opts.WorkDir != "" {
		cmd.Dir = opts.WorkDir
	}

	cmd.Env = copilotEnv()
	cmd.Cancel = func() error {
		return cmd.Process.Signal(syscall.SIGTERM)
	}
	cmd.WaitDelay = 10 * time.Second

	return cmd, cancel
}

func (p *CopilotProvider) BuildInteractiveCommand(systemPrompt, workDir string) *exec.Cmd {
	binary := p.Binary
	if binary == "" {
		binary = "copilot"
	}
	cmd := exec.Command(binary,
		"--system-prompt", systemPrompt,
	)
	cmd.Env = copilotEnv()
	if workDir != "" {
		cmd.Dir = workDir
	}
	return cmd
}

func (p *CopilotProvider) BuildPromptCommand(ctx context.Context, prompt string) (*exec.Cmd, context.CancelFunc) {
	ctx, cancel := context.WithCancel(ctx)
	binary := p.Binary
	if binary == "" {
		binary = "copilot"
	}
	cmd := exec.CommandContext(ctx, binary, "--auto-approve", "-p", prompt)
	cmd.Env = copilotEnv()
	return cmd, cancel
}

// ParseOutput returns empty stats — Copilot CLI outputs plain text without
// structured token usage information.
func (p *CopilotProvider) ParseOutput(content string) engine.UsageStats {
	return engine.UsageStats{}
}

func (p *CopilotProvider) ValidateBinary() error {
	binary := p.Binary
	if binary == "" {
		binary = "copilot"
	}
	_, err := exec.LookPath(binary)
	return err
}

func init() {
	engine.RegisterProvider(&CopilotProvider{})
}
