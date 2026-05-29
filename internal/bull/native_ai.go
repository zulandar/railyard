package bull

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

// nativeTriageMaxIterations bounds the read-only triage loop. Triage is a
// look-up-then-decide task (a few codesearch/read_file calls before answering),
// so the cap sits well below the engine's coding budget. Hitting it means the
// model never converged, and RunPrompt surfaces that as an error rather than a
// placeholder the triage parser would silently reject.
const nativeTriageMaxIterations = 16

// NativeAI implements TriageAI using the Railyard-owned agent loop. When
// CocoIndex is configured it runs a tool-capable loop with a READ-ONLY toolset
// (read_file + codesearch — no bash/write/edit) so the model can look up related
// code before deciding; otherwise it falls back to a single tool-less completion
// (triage is a text-in/text-out decision). It is selected when auth_method
// routes to the native loop (openrouter/openai_compat).
type NativeAI struct {
	client agentloop.Completer
	model  string
	// workDir roots the read_file tool (the repo the triage runs against);
	// unused on the tool-less path.
	workDir string
	// codeSearch, when non-nil, switches RunPrompt to the tool-capable read-only
	// loop. nil preserves the original tool-less one-shot behavior.
	codeSearch *agentloop.CodeSearchParams
}

// NewNativeAI creates a tool-less one-shot TriageAI backed by an
// OpenAI-compatible client.
func NewNativeAI(client agentloop.Completer, model string) *NativeAI {
	return &NativeAI{client: client, model: model}
}

// NewNativeAIWithCodeSearch creates a tool-capable TriageAI: when cs is non-nil
// the model can call read-only codesearch/read_file (rooted at workDir) before
// deciding. A nil cs degrades to the tool-less one-shot path.
func NewNativeAIWithCodeSearch(client agentloop.Completer, model, workDir string, cs *agentloop.CodeSearchParams) *NativeAI {
	return &NativeAI{client: client, model: model, workDir: workDir, codeSearch: cs}
}

// RunPrompt sends the triage prompt and returns the model's trimmed text
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
			return "", fmt.Errorf("bull: native run prompt: %w", err)
		}
		return strings.TrimSpace(resp.Content), nil
	}

	loop := agentloop.NewLoop(a.client, agentloop.LoopConfig{
		Model:         a.model,
		Tools:         agentloop.ReadOnlyTools(a.workDir, a.codeSearch),
		MaxIterations: nativeTriageMaxIterations,
		Role:          "bull",
	})
	res, err := loop.Run(ctx, prompt)
	if err != nil {
		return "", fmt.Errorf("bull: native run prompt: %w", err)
	}
	// A max-iterations stop means the model never produced a decision; its
	// FinalText is a synthesized placeholder, not a triage result. Surface it as
	// an error so the caller doesn't try to parse a non-decision.
	if res.StopReason == agentloop.StopMaxIterations {
		return "", fmt.Errorf("bull: native run prompt: agent did not finish within %d iterations", res.Iterations)
	}
	return strings.TrimSpace(res.FinalText), nil
}

// newTriageAI selects the triage AI backend: the native agent loop when
// auth_method routes to it (credentials from the environment), otherwise the
// CLI agent provider (unchanged behavior). On the native path, when CocoIndex is
// configured the triage agent gets read-only semantic codesearch.
func newTriageAI(cfg *config.Config) (TriageAI, error) {
	client, useNative, err := agentbackend.Resolve(cfg)
	if err != nil {
		return nil, fmt.Errorf("bull: native loop: %w", err)
	}
	if useNative {
		// Triage runs against the repo it's launched in; the main index (all
		// tracks, no overlay) is the right search target. nil when CocoIndex is
		// unconfigured, which keeps the tool-less one-shot behavior.
		workDir, _ := os.Getwd()
		return NewNativeAIWithCodeSearch(client, cfg.Bull.AgentModel, workDir, engine.MainIndexCodeSearchParams(cfg)), nil
	}
	return NewProviderAI(cfg.Bull.AgentProvider, cfg.Bull.AgentModel)
}
