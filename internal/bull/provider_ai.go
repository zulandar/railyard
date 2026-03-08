package bull

import (
	"context"
	"fmt"
	"strings"

	"github.com/zulandar/railyard/internal/engine"
)

// ProviderAI adapts an engine.AgentProvider to the TriageAI interface
// by using BuildPromptCommand for one-shot prompt execution.
type ProviderAI struct {
	provider engine.AgentProvider
}

// NewProviderAI creates a TriageAI backed by the named agent provider.
func NewProviderAI(providerName string) (*ProviderAI, error) {
	p, err := engine.GetProvider(providerName)
	if err != nil {
		return nil, fmt.Errorf("bull: %w", err)
	}
	return &ProviderAI{provider: p}, nil
}

func (a *ProviderAI) RunPrompt(ctx context.Context, prompt string) (string, error) {
	cmd, cancel := a.provider.BuildPromptCommand(ctx, prompt)
	defer cancel()

	output, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("bull: run prompt (%s): %w", a.provider.Name(), err)
	}
	return strings.TrimSpace(string(output)), nil
}
