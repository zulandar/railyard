package agentloop

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"
)

// liveKey resolves the OpenRouter API key for the live smoke test from
// RAILYARD_LIVE_OPENROUTER_KEY, falling back to a local /tmp/or_test_key file.
// Returns "" when neither is present (the test then skips).
func liveKey() string {
	if k := strings.TrimSpace(os.Getenv("RAILYARD_LIVE_OPENROUTER_KEY")); k != "" {
		return k
	}
	if b, err := os.ReadFile("/tmp/or_test_key"); err == nil {
		return strings.TrimSpace(string(b))
	}
	return ""
}

// upstreamUnavailable reports whether err reflects the OpenRouter/model being
// down rather than a defect in our loop: provider 5xx, exhausted credits, or a
// stalled request that burned the context deadline. The smoke test skips on
// these so a flaky free model (owl-alpha is a rotating "stealth" provider)
// can't produce a false failure.
func upstreamUnavailable(err error) bool {
	var apiErr *APIError
	if errors.As(err, &apiErr) && apiErr.StatusCode >= 500 {
		return true
	}
	var creditErr *CreditError
	if errors.As(err, &creditErr) {
		return true
	}
	var rateErr *RateLimitError
	if errors.As(err, &rateErr) {
		return true
	}
	return errors.Is(err, context.DeadlineExceeded)
}

// magicNumberTool is a trivial tool whose only job is to prove end-to-end
// tool-calling: the model must call it and report the number it returns.
type magicNumberTool struct{ called *bool }

func (magicNumberTool) Definition() ToolDef {
	return ToolDef{
		Name:        "get_magic_number",
		Description: "Returns the secret magic number. Call this to learn the magic number.",
		Parameters:  json.RawMessage(`{"type":"object","properties":{}}`),
	}
}

func (m magicNumberTool) Execute(_ context.Context, _ json.RawMessage) (string, error) {
	*m.called = true
	return "the magic number is 1729", nil
}

// TestLive_OpenRouter_ToolCall exercises the real OpenRouter endpoint with a
// trivial tool, mirroring the 2026-05-27 probe: the model must emit a tool call
// and summarize the result under our clean prompt. It is gated on a live key
// (skipped in CI) and skips when the upstream model is unavailable.
//
// Default model is openrouter/owl-alpha (the weak model this work targets);
// override with RAILYARD_LIVE_OPENROUTER_MODEL (e.g. openai/gpt-4o-mini) to
// validate the loop against a reliably-available model.
func TestLive_OpenRouter_ToolCall(t *testing.T) {
	key := liveKey()
	if key == "" {
		t.Skip("no live key: set RAILYARD_LIVE_OPENROUTER_KEY (or provide /tmp/or_test_key) to run")
	}

	model := os.Getenv("RAILYARD_LIVE_OPENROUTER_MODEL")
	if model == "" {
		model = "openrouter/owl-alpha"
	}

	t.Setenv("OPENROUTER_API_KEY", key)
	t.Setenv("OPENROUTER_BASE_URL", "")
	// Bound a stalled request so a hung provider skips promptly instead of
	// burning the whole context budget.
	client, err := NewClientFromEnv("openrouter",
		WithHTTPClient(&http.Client{Timeout: 30 * time.Second}),
		WithMaxRetries(1),
		WithRetryBaseDelay(time.Second),
	)
	if err != nil {
		t.Fatalf("NewClientFromEnv: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 75*time.Second)
	defer cancel()

	called := false
	loop := NewLoop(client, LoopConfig{
		Model:         model,
		SystemPrompt:  "You are a helpful assistant. Use the provided tools when they are relevant, then answer concisely.",
		Tools:         []Tool{magicNumberTool{called: &called}},
		MaxIterations: 6,
	})

	res, err := loop.Run(ctx, "Use the get_magic_number tool to look up the magic number, then tell me what it is.")
	if err != nil {
		if upstreamUnavailable(err) {
			t.Skipf("upstream model %q unavailable, skipping live smoke: %v", model, err)
		}
		t.Fatalf("live loop run: %v", err)
	}
	if !called {
		t.Fatalf("model never called get_magic_number; final text = %q", res.FinalText)
	}
	if !strings.Contains(res.FinalText, "1729") {
		t.Errorf("final text = %q, want it to report the magic number 1729", res.FinalText)
	}
	t.Logf("live smoke OK: model=%s usage=%+v final=%q", model, res.Usage, res.FinalText)
}
