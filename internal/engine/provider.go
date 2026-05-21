package engine

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"sync"
)

// AgentProvider defines the interface for AI CLI tool integrations.
//
// Model selection is plumbed per-call (via `SpawnOpts.Model` for engine mode and
// the `model string` argument on interactive/prompt builders) rather than stored
// on provider struct fields. Providers are registered as singletons at init();
// mutating shared state would race between concurrent engines selecting
// different models. See ADR 5 in the do-inference design doc.
type AgentProvider interface {
	// Name returns the provider identifier (e.g., "claude", "opencode").
	Name() string
	// BuildCommand constructs the exec.Cmd for the provider's CLI tool (engine mode).
	// Honors opts.Model via a provider-appropriate mechanism (env var or flag).
	BuildCommand(ctx context.Context, opts SpawnOpts) (*exec.Cmd, context.CancelFunc)
	// BuildInteractiveCommand constructs an interactive CLI session (dispatch mode).
	// The system prompt is appended to the agent's default behavior. When `model`
	// is non-empty, the provider applies it via its native mechanism; empty
	// preserves the CLI's default model selection.
	BuildInteractiveCommand(systemPrompt, workDir, model string) *exec.Cmd
	// BuildPromptCommand constructs a one-shot CLI invocation (escalation mode).
	// The prompt is sent as a single message and the agent exits after responding.
	// When `model` is non-empty, the provider applies it; empty preserves default.
	BuildPromptCommand(ctx context.Context, prompt, model string) (*exec.Cmd, context.CancelFunc)
	// ParseOutput extracts token usage statistics from the provider's output.
	ParseOutput(content string) UsageStats
	// ValidateBinary checks that the provider's CLI binary is available.
	ValidateBinary() error
}

// registry holds registered providers.
var (
	registryMu sync.RWMutex
	registry   = make(map[string]AgentProvider)
)

// defaultClaudeProvider is a built-in provider that uses the legacy buildCommand
// and ParseUsageFromContent functions already in the engine package. It is
// registered at init time so that SpawnAgent's default "claude" provider works
// without requiring callers to add a side-effect import of the providers
// sub-package. The providers/claude.go init() overwrites this with an identical
// (but explicitly constructed) implementation when imported.
type defaultClaudeProvider struct{}

func (defaultClaudeProvider) Name() string { return "claude" }

func (defaultClaudeProvider) BuildCommand(ctx context.Context, opts SpawnOpts) (*exec.Cmd, context.CancelFunc) {
	return buildCommand(ctx, opts)
}

func (defaultClaudeProvider) BuildInteractiveCommand(systemPrompt, workDir, model string) *exec.Cmd {
	cmd := exec.Command("claude",
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

func (defaultClaudeProvider) BuildPromptCommand(ctx context.Context, prompt, model string) (*exec.Cmd, context.CancelFunc) {
	ctx, cancel := context.WithCancel(ctx)
	cmd := exec.CommandContext(ctx, "claude", "-p", prompt)
	if model != "" {
		cmd.Env = append(os.Environ(), "ANTHROPIC_MODEL="+model)
	}
	return cmd, cancel
}

func (defaultClaudeProvider) ParseOutput(content string) UsageStats {
	return ParseUsageFromContent(content)
}

func (defaultClaudeProvider) ValidateBinary() error {
	_, err := exec.LookPath("claude")
	return err
}

func init() {
	RegisterProvider(defaultClaudeProvider{})
}

// RegisterProvider adds a provider to the global registry.
func RegisterProvider(p AgentProvider) {
	registryMu.Lock()
	defer registryMu.Unlock()
	registry[p.Name()] = p
}

// GetProvider retrieves a provider by name from the registry.
func GetProvider(name string) (AgentProvider, error) {
	registryMu.RLock()
	defer registryMu.RUnlock()
	p, ok := registry[name]
	if !ok {
		return nil, fmt.Errorf("engine: unknown provider %q", name)
	}
	return p, nil
}

// ListProviders returns the names of all registered providers.
func ListProviders() []string {
	registryMu.RLock()
	defer registryMu.RUnlock()
	names := make([]string, 0, len(registry))
	for name := range registry {
		names = append(names, name)
	}
	return names
}

// ResetRegistry clears all registered providers (for testing).
func ResetRegistry() {
	registryMu.Lock()
	defer registryMu.Unlock()
	registry = make(map[string]AgentProvider)
}
