package bull

import (
	"context"
	"fmt"
	"strings"

	"github.com/zulandar/railyard/internal/agentbackend"
	"github.com/zulandar/railyard/internal/agentloop"
	"github.com/zulandar/railyard/internal/config"
)

// NativeAI implements TriageAI using the Railyard-owned agent loop's client for
// a single one-shot completion (no tools). It is selected when auth_method
// routes to the native loop (openrouter/openai_compat); triage is a pure
// text-in/text-out decision, so no tool dispatch is needed.
type NativeAI struct {
	client agentloop.Completer
	model  string
}

// NewNativeAI creates a TriageAI backed by an OpenAI-compatible client.
func NewNativeAI(client agentloop.Completer, model string) *NativeAI {
	return &NativeAI{client: client, model: model}
}

// RunPrompt sends the triage prompt as a single user message and returns the
// model's trimmed text response — matching ProviderAI.RunPrompt's contract.
func (a *NativeAI) RunPrompt(ctx context.Context, prompt string) (string, error) {
	resp, err := a.client.Complete(ctx, agentloop.Request{
		Model:    a.model,
		Messages: []agentloop.Message{{Role: "user", Content: prompt}},
	})
	if err != nil {
		return "", fmt.Errorf("bull: native run prompt: %w", err)
	}
	return strings.TrimSpace(resp.Content), nil
}

// newTriageAI selects the triage AI backend: the native agent loop when
// auth_method routes to it (credentials from the environment), otherwise the
// CLI agent provider (unchanged behavior).
func newTriageAI(cfg *config.Config) (TriageAI, error) {
	client, useNative, err := agentbackend.Resolve(cfg)
	if err != nil {
		return nil, fmt.Errorf("bull: native loop: %w", err)
	}
	if useNative {
		return NewNativeAI(client, cfg.Bull.AgentModel), nil
	}
	return NewProviderAI(cfg.Bull.AgentProvider, cfg.Bull.AgentModel)
}
