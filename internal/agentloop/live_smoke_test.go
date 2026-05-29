package agentloop

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
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

// TestLive_OpenRouter_EngineToolsWriteFile validates the engine code-writing
// path (railyard-j89.5.3 spike): the weak model must actually use the engine
// tool profile (write_file + bash) to create a real file in the worktree. This
// is the core capability the native engine runner depends on. Gated on a live
// key and skipped when the upstream model is unavailable.
func TestLive_OpenRouter_EngineToolsWriteFile(t *testing.T) {
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
	client, err := NewClientFromEnv("openrouter",
		WithHTTPClient(&http.Client{Timeout: 45 * time.Second}),
		WithMaxRetries(1),
		WithRetryBaseDelay(time.Second),
	)
	if err != nil {
		t.Fatalf("NewClientFromEnv: %v", err)
	}

	workDir := t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	loop := NewLoop(client, LoopConfig{
		Model:         model,
		SystemPrompt:  "You are a coding engine. Use the provided file and shell tools to complete the task in the working directory. When done, briefly confirm.",
		Tools:         EngineTools(workDir, nil),
		MaxIterations: 12,
	})

	res, err := loop.Run(ctx, "Create a file named greeting.txt in the current directory containing exactly the text: hello from railyard. Use the write_file tool.")
	if err != nil {
		if upstreamUnavailable(err) {
			t.Skipf("upstream model %q unavailable, skipping engine-tools smoke: %v", model, err)
		}
		t.Fatalf("live engine loop run: %v", err)
	}

	got, readErr := os.ReadFile(filepath.Join(workDir, "greeting.txt"))
	if readErr != nil {
		t.Fatalf("model did not create greeting.txt (%v); final text = %q", readErr, res.FinalText)
	}
	if !strings.Contains(string(got), "hello from railyard") {
		t.Errorf("greeting.txt = %q, want it to contain the requested text", got)
	}
	t.Logf("engine-tools smoke OK: model=%s usage=%+v file=%q", model, res.Usage, string(got))
}

// TestLive_OpenRouter_CodeSearch validates the native-loop codesearch tool
// (railyard-agx) end-to-end against a REAL populated CocoIndex/pgvector index: a
// weak tool-capable model must actually CALL codesearch (not bash grep) and the
// tool must return real ranked snippets from the index. This is the mandatory
// pre-ship smoke gate for the native codesearch work.
//
// Gated on a live OpenRouter key AND a live index, supplied via env:
//
//	RAILYARD_LIVE_COCOINDEX_DB     postgres URL of the populated pgvector index
//	RAILYARD_LIVE_COCOINDEX_PYTHON venv python that can run mcp_server.py
//	RAILYARD_LIVE_COCOINDEX_SCRIPT path to cocoindex/mcp_server.py
//	RAILYARD_LIVE_COCOINDEX_TABLE  COCOINDEX_MAIN_TABLE (comma-separated allowed)
//
// It skips cleanly when any of these are absent (so CI stays green) and when the
// upstream model is unavailable.
func TestLive_OpenRouter_CodeSearch(t *testing.T) {
	key := liveKey()
	if key == "" {
		t.Skip("no live key: set RAILYARD_LIVE_OPENROUTER_KEY (or provide /tmp/or_test_key) to run")
	}
	db := strings.TrimSpace(os.Getenv("RAILYARD_LIVE_COCOINDEX_DB"))
	python := strings.TrimSpace(os.Getenv("RAILYARD_LIVE_COCOINDEX_PYTHON"))
	script := strings.TrimSpace(os.Getenv("RAILYARD_LIVE_COCOINDEX_SCRIPT"))
	table := strings.TrimSpace(os.Getenv("RAILYARD_LIVE_COCOINDEX_TABLE"))
	if db == "" || python == "" || script == "" || table == "" {
		t.Skip("no live index: set RAILYARD_LIVE_COCOINDEX_{DB,PYTHON,SCRIPT,TABLE} to run")
	}

	model := os.Getenv("RAILYARD_LIVE_OPENROUTER_MODEL")
	if model == "" {
		model = "openrouter/owl-alpha"
	}

	t.Setenv("OPENROUTER_API_KEY", key)
	t.Setenv("OPENROUTER_BASE_URL", "")
	client, err := NewClientFromEnv("openrouter",
		WithHTTPClient(&http.Client{Timeout: 60 * time.Second}),
		WithMaxRetries(1),
		WithRetryBaseDelay(time.Second),
	)
	if err != nil {
		t.Fatalf("NewClientFromEnv: %v", err)
	}

	cs := &CodeSearchParams{
		PythonPath: python,
		ScriptPath: script,
		Env: map[string]string{
			"COCOINDEX_DATABASE_URL": db,
			"COCOINDEX_MAIN_TABLE":   table,
		},
	}

	// Cover both native-loop toolset shapes that carry codesearch: the
	// dispatch/telegraph/engine profile (railyard-agx) and the read-only
	// triage/review profile bull & inspect use (railyard-tsy). Both must let a
	// real weak model actually call codesearch and get real ranked results.
	profiles := []struct {
		name  string
		role  string
		tools []Tool
	}{
		{"dispatch_profile", "dispatch", DispatchTools(t.TempDir(), cs)},
		{"readonly_profile", "inspect", ReadOnlyTools(t.TempDir(), cs)},
	}
	for _, p := range profiles {
		t.Run(p.name, func(t *testing.T) {
			runCodeSearchSmoke(t, client, model, p.role, p.tools)
		})
	}
}

// runCodeSearchSmoke drives one native-loop turn with the given toolset against a
// real model + index, asserting the model actually invoked codesearch and the
// tool returned real ranked results (not a "no results" miss or an error).
func runCodeSearchSmoke(t *testing.T, client *Client, model, role string, tools []Tool) {
	t.Helper()

	events := make(chan Event, 256)
	var called bool
	var resultText, toolErr string
	drained := make(chan struct{})
	go func() {
		defer close(drained)
		for ev := range events {
			switch ev.Type {
			case EventToolCallStart:
				if ev.ToolName == CodeSearchToolName {
					called = true
				}
			case EventToolCallEnd:
				if ev.ToolName == CodeSearchToolName {
					resultText = ev.ToolResult
					toolErr = ev.ToolError
				}
			}
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()

	loop := NewLoop(client, LoopConfig{
		Model:         model,
		SystemPrompt:  "You are a coding assistant. Use the codesearch tool to look up code in the indexed codebase, then answer concisely.",
		Tools:         tools,
		Role:          role,
		MaxIterations: 6,
		Events:        events,
	})

	res, runErr := loop.Run(ctx, "Use the codesearch tool to search the codebase for \"user authentication\". Report the filename of the top result.")
	close(events)
	<-drained

	if runErr != nil {
		if upstreamUnavailable(runErr) {
			t.Skipf("upstream model %q unavailable, skipping codesearch smoke: %v", model, runErr)
		}
		t.Fatalf("live codesearch loop run: %v", runErr)
	}
	if !called {
		t.Fatalf("model never called codesearch; final text = %q", res.FinalText)
	}
	if toolErr != "" {
		t.Fatalf("codesearch tool errored against the live index: %s", toolErr)
	}
	if strings.TrimSpace(resultText) == "" || strings.Contains(resultText, "No results found") {
		t.Fatalf("codesearch returned no real results from the live index: %q", resultText)
	}
	t.Logf("codesearch smoke OK: model=%s usage=%+v\nresult snippet:\n%s", model, res.Usage, Truncate(resultText, 400))
}
