package inspect

import (
	"context"
	"fmt"
	"strings"

	"github.com/zulandar/railyard/internal/engine"
)

// ReviewAI is the interface for running review prompts.
type ReviewAI interface {
	RunPrompt(ctx context.Context, prompt string) (string, error)
}

// ProviderAI adapts an engine.AgentProvider to the ReviewAI interface.
type ProviderAI struct {
	provider engine.AgentProvider
	model    string
}

// NewProviderAI creates a ReviewAI backed by the named agent provider.
// The model argument is threaded through to the provider's BuildPromptCommand;
// pass "" to use the provider's default model.
func NewProviderAI(providerName, model string) (*ProviderAI, error) {
	p, err := engine.GetProvider(providerName)
	if err != nil {
		return nil, fmt.Errorf("inspect: %w", err)
	}
	return &ProviderAI{provider: p, model: model}, nil
}

// RunPrompt executes a one-shot prompt via the agent provider.
func (a *ProviderAI) RunPrompt(ctx context.Context, prompt string) (string, error) {
	cmd, cancel := a.provider.BuildPromptCommand(ctx, prompt, a.model)
	defer cancel()

	output, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("inspect: run prompt (%s): %w", a.provider.Name(), err)
	}
	return strings.TrimSpace(string(output)), nil
}
