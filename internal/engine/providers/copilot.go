package providers

import (
	"context"
	"log"
	"os"
	"os/exec"
	"strings"
	"sync"
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
//
// Model selection: copilot CLI does not expose a stable model-selection knob
// (the model is tied to the user's GitHub Copilot subscription). When a
// non-empty model is supplied, the provider logs a warning ONCE and proceeds
// without applying anything.
type CopilotProvider struct {
	Binary string // path to copilot binary; defaults to "copilot"
}

// copilotModelWarnOnce ensures we log at most one warning when a model is
// requested but copilot cannot honor it. Package-level so all CopilotProvider
// instances share the same warning state.
var copilotModelWarnOnce sync.Once

// warnUnsupportedModel emits a one-time warning when a non-empty model is
// supplied to a copilot Build* method. No-op when model is empty.
func warnUnsupportedModel(model string) {
	if model == "" {
		return
	}
	copilotModelWarnOnce.Do(func() {
		log.Printf("copilot provider doesn't support model selection — ignoring agent_model=%q", model)
	})
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

	warnUnsupportedModel(opts.Model)

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

func (p *CopilotProvider) BuildInteractiveCommand(systemPrompt, workDir, model string, _ ...string) *exec.Cmd {
	binary := p.Binary
	if binary == "" {
		binary = "copilot"
	}
	warnUnsupportedModel(model)
	cmd := exec.Command(binary,
		"--system-prompt", systemPrompt,
	)
	cmd.Env = copilotEnv()
	if workDir != "" {
		cmd.Dir = workDir
	}
	return cmd
}

func (p *CopilotProvider) BuildPromptCommand(ctx context.Context, prompt, model string) (*exec.Cmd, context.CancelFunc) {
	ctx, cancel := context.WithCancel(ctx)
	binary := p.Binary
	if binary == "" {
		binary = "copilot"
	}
	warnUnsupportedModel(model)
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
