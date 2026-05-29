package inspect

import (
	"context"
	"fmt"
	"strings"

	"github.com/zulandar/railyard/internal/agentloop"
	"github.com/zulandar/railyard/internal/config"
)

// NativeAI implements ReviewAI using the Railyard-owned agent loop's client for
// a single one-shot completion (no tools). It is selected when auth_method
// routes to the native loop (openrouter/openai_compat); review is a pure
// text-in/text-out decision, so no tool dispatch is needed.
type NativeAI struct {
	client agentloop.Completer
	model  string
}

// NewNativeAI creates a ReviewAI backed by an OpenAI-compatible client.
func NewNativeAI(client agentloop.Completer, model string) *NativeAI {
	return &NativeAI{client: client, model: model}
}

// RunPrompt sends the review prompt as a single user message and returns the
// model's trimmed text response — matching ProviderAI.RunPrompt's contract.
func (a *NativeAI) RunPrompt(ctx context.Context, prompt string) (string, error) {
	resp, err := a.client.Complete(ctx, agentloop.Request{
		Model:    a.model,
		Messages: []agentloop.Message{{Role: "user", Content: prompt}},
	})
	if err != nil {
		return "", fmt.Errorf("inspect: native run prompt: %w", err)
	}
	return strings.TrimSpace(resp.Content), nil
}

// newReviewAI selects the review AI backend: the native agent loop when
// auth_method routes to it (credentials from the environment), otherwise the
// CLI agent provider (unchanged behavior). Mirrors bull.newTriageAI so the
// inspect role follows the same native-vs-CLI routing as every other role.
func newReviewAI(cfg *config.Config) (ReviewAI, error) {
	if agentloop.IsNativeLoopMethod(cfg.AuthMethod) {
		client, err := agentloop.NewClientFromEnv(cfg.AuthMethod)
		if err != nil {
			return nil, fmt.Errorf("inspect: native loop: %w", err)
		}
		return NewNativeAI(client, cfg.Inspect.AgentModel), nil
	}
	return NewProviderAI(cfg.Inspect.AgentProvider, cfg.Inspect.AgentModel)
}
