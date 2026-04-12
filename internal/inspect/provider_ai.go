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
}

// NewProviderAI creates a ReviewAI backed by the named agent provider.
func NewProviderAI(providerName string) (*ProviderAI, error) {
	p, err := engine.GetProvider(providerName)
	if err != nil {
		return nil, fmt.Errorf("inspect: %w", err)
	}
	return &ProviderAI{provider: p}, nil
}

// RunPrompt executes a one-shot prompt via the agent provider.
func (a *ProviderAI) RunPrompt(ctx context.Context, prompt string) (string, error) {
	cmd, cancel := a.provider.BuildPromptCommand(ctx, prompt)
	defer cancel()

	output, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("inspect: run prompt (%s): %w", a.provider.Name(), err)
	}
	return strings.TrimSpace(string(output)), nil
}
