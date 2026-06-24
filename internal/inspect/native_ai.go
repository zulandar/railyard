package inspect

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/zulandar/railyard/internal/agentbackend"
	"github.com/zulandar/railyard/internal/agentloop"
	"github.com/zulandar/railyard/internal/config"
	"github.com/zulandar/railyard/internal/engine"
)

// nativeReviewMaxIterationsCodeSearch is the fallback iteration cap for the
// codesearch/deep_review path. Codesearch needs more rounds for
// look-up-then-decide; matching agentloop.defaultMaxIterations (30).
const nativeReviewMaxIterationsCodeSearch = 30

// NativeAI implements ReviewAI using the Railyard-owned agent loop. When
// CocoIndex is configured it runs a tool-capable loop with a READ-ONLY toolset
// (read_file + codesearch — no bash/write/edit) so the model can look up related
// code before deciding; otherwise it falls back to a single tool-less completion
// (review is a text-in/text-out decision). It is selected when auth_method
// routes to the native loop (openrouter/openai_compat).
type NativeAI struct {
	client agentloop.Completer
	model  string
	// workDir roots the read_file tool (the repo the review runs against);
	// unused on the tool-less path.
	workDir string
	// codeSearch, when non-nil, switches RunPrompt to the tool-capable read-only
	// loop. nil preserves the original tool-less one-shot behavior.
	codeSearch *agentloop.CodeSearchParams
	// maxIterations is the agent-loop iteration cap (0 means use the
	// path-appropriate default). Drawn from config.Inspect.MaxReviewIterations.
	maxIterations int
}

// NewNativeAI creates a tool-less one-shot ReviewAI backed by an
// OpenAI-compatible client.
func NewNativeAI(client agentloop.Completer, model string) *NativeAI {
	return &NativeAI{client: client, model: model}
}

// NewNativeAIWithCodeSearch creates a tool-capable ReviewAI: when cs is non-nil
// the model can call read-only codesearch/read_file (rooted at workDir) before
// deciding. A nil cs degrades to the tool-less one-shot path.
func NewNativeAIWithCodeSearch(client agentloop.Completer, model, workDir string, cs *agentloop.CodeSearchParams) *NativeAI {
	return &NativeAI{client: client, model: model, workDir: workDir, codeSearch: cs}
}

// RunPrompt sends the review prompt and returns the model's trimmed text
// response — matching ProviderAI.RunPrompt's contract. With codesearch
// configured it drives a read-only agent loop (the model may look up code first);
// otherwise it is a single completion.
func (a *NativeAI) RunPrompt(ctx context.Context, prompt string) (string, error) {
	if a.codeSearch == nil {
		resp, err := a.client.Complete(ctx, agentloop.Request{
			Model:    a.model,
			Messages: []agentloop.Message{{Role: "user", Content: prompt}},
		})
		if err != nil {
			return "", fmt.Errorf("inspect: native run prompt: %w", err)
		}
		return strings.TrimSpace(resp.Content), nil
	}

	maxIter := a.maxIterations
	if maxIter <= 0 {
		maxIter = nativeReviewMaxIterationsCodeSearch
	}

	loop := agentloop.NewLoop(a.client, agentloop.LoopConfig{
		Model:         a.model,
		Tools:         agentloop.ReadOnlyTools(a.workDir, a.codeSearch),
		MaxIterations: maxIter,
		Role:          "inspect",
	})
	res, err := loop.Run(ctx, prompt)
	if err != nil {
		return "", fmt.Errorf("inspect: native run prompt: %w", err)
	}
	// A max-iterations stop means the model never produced a decision; its
	// FinalText is a synthesized placeholder, not a review result. Surface it as
	// an error so the caller doesn't try to parse a non-decision.
	if res.StopReason == agentloop.StopMaxIterations {
		return "", fmt.Errorf("inspect: native run prompt: agent did not finish within %d iterations", res.Iterations)
	}
	return strings.TrimSpace(res.FinalText), nil
}

// newReviewAI selects the review AI backend: the native agent loop when
// auth_method routes to it (credentials from the environment), otherwise the
// CLI agent provider (unchanged behavior). Mirrors bull.newTriageAI so the
// inspect role follows the same native-vs-CLI routing as every other role. On
// the native path, when CocoIndex is configured the review agent gets read-only
// semantic codesearch.
func newReviewAI(cfg *config.Config) (ReviewAI, error) {
	client, useNative, err := agentbackend.Resolve(cfg)
	if err != nil {
		return nil, fmt.Errorf("inspect: native loop: %w", err)
	}
	if useNative {
		// Review runs against the repo it's launched in; the main index (all
		// tracks, no overlay) is the right search target. nil when CocoIndex is
		// unconfigured, which keeps the tool-less one-shot behavior.
		workDir, _ := os.Getwd()
		ai := NewNativeAIWithCodeSearch(client, cfg.Inspect.AgentModel, workDir, engine.MainIndexCodeSearchParams(cfg))
		ai.maxIterations = cfg.Inspect.MaxReviewIterations
		return ai, nil
	}
	return NewProviderAI(cfg.Inspect.AgentProvider, cfg.Inspect.AgentModel)
}
