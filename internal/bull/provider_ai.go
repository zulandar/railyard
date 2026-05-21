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
	model    string
}

// NewProviderAI creates a TriageAI backed by the named agent provider.
// The model argument is threaded through to the provider's BuildPromptCommand;
// pass "" to use the provider's default model.
func NewProviderAI(providerName, model string) (*ProviderAI, error) {
	p, err := engine.GetProvider(providerName)
	if err != nil {
		return nil, fmt.Errorf("bull: %w", err)
	}
	return &ProviderAI{provider: p, model: model}, nil
}

func (a *ProviderAI) RunPrompt(ctx context.Context, prompt string) (string, error) {
	cmd, cancel := a.provider.BuildPromptCommand(ctx, prompt, a.model)
	defer cancel()

	output, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("bull: run prompt (%s): %w", a.provider.Name(), err)
	}
	return strings.TrimSpace(string(output)), nil
}
