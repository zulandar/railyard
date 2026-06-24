package inspect

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/zulandar/railyard/internal/agentloop"
	"github.com/zulandar/railyard/internal/config"
)

// fakeCompleter is a fake agentloop.Completer for inspect tests.
type fakeCompleter struct {
	resp   agentloop.Response
	err    error
	gotReq agentloop.Request
}

func (c *fakeCompleter) Complete(_ context.Context, req agentloop.Request) (agentloop.Response, error) {
	c.gotReq = req
	if c.err != nil {
		return agentloop.Response{}, c.err
	}
	return c.resp, nil
}

func TestNativeAI_RunPrompt(t *testing.T) {
	c := &fakeCompleter{resp: agentloop.Response{Content: "  APPROVE\n", FinishReason: "stop"}}
	ai := NewNativeAI(c, "openrouter/owl-alpha")

	out, err := ai.RunPrompt(context.Background(), "review this PR")
	if err != nil {
		t.Fatalf("RunPrompt: %v", err)
	}
	if out != "APPROVE" {
		t.Errorf("RunPrompt = %q, want trimmed %q", out, "APPROVE")
	}
	// One-shot: a single user message carrying the prompt, no tools.
	if c.gotReq.Model != "openrouter/owl-alpha" {
		t.Errorf("request model = %q, want owl-alpha", c.gotReq.Model)
	}
	if len(c.gotReq.Tools) != 0 {
		t.Errorf("request tools = %v, want none (one-shot completion)", c.gotReq.Tools)
	}
	if len(c.gotReq.Messages) != 1 || c.gotReq.Messages[0].Role != "user" || c.gotReq.Messages[0].Content != "review this PR" {
		t.Errorf("request messages = %+v, want a single user message with the prompt", c.gotReq.Messages)
	}
}

func TestNativeAI_RunPrompt_Error(t *testing.T) {
	ai := NewNativeAI(&fakeCompleter{err: &agentloop.RateLimitError{Message: "slow down"}}, "m")
	_, err := ai.RunPrompt(context.Background(), "p")
	if err == nil {
		t.Fatal("expected error from RunPrompt, got nil")
	}
	var rle *agentloop.RateLimitError
	if !errors.As(err, &rle) {
		t.Errorf("error = %v, want to wrap *RateLimitError", err)
	}
}

func TestNativeAI_RunPrompt_WithCodeSearch_ExposesReadOnlyToolsOnly(t *testing.T) {
	c := &fakeCompleter{resp: agentloop.Response{Content: "APPROVE", FinishReason: "stop"}}
	cs := &agentloop.CodeSearchParams{PythonPath: "/x/python", ScriptPath: "/x/mcp_server.py"}
	ai := NewNativeAIWithCodeSearch(c, "m", t.TempDir(), cs)

	out, err := ai.RunPrompt(context.Background(), "review this PR")
	if err != nil {
		t.Fatalf("RunPrompt: %v", err)
	}
	if out != "APPROVE" {
		t.Errorf("RunPrompt = %q, want %q", out, "APPROVE")
	}

	var names []string
	for _, td := range c.gotReq.Tools {
		names = append(names, td.Name)
	}
	has := func(n string) bool {
		for _, x := range names {
			if x == n {
				return true
			}
		}
		return false
	}
	// Review may look up code (read-only) but must never get write/edit/bash.
	if !has("codesearch") || !has("read_file") {
		t.Errorf("tools = %v, want read_file + codesearch", names)
	}
	for _, forbidden := range []string{"bash", "write_file", "edit_file"} {
		if has(forbidden) {
			t.Errorf("tools = %v, must NOT expose %q to review", names, forbidden)
		}
	}
}

func TestNativeAI_RunPrompt_WithCodeSearch_MaxIterationsReturnsError(t *testing.T) {
	// A model that only ever calls a tool (never producing a final answer) drives
	// the read-only loop to its iteration cap. RunPrompt must surface that as an
	// error rather than returning the loop's placeholder text as if it were a
	// review decision (which would fail to parse and silently drop the result).
	c := &fakeCompleter{resp: agentloop.Response{
		ToolCalls: []agentloop.ToolCall{{ID: "1", Name: "read_file", Arguments: json.RawMessage(`{"path":"missing.txt"}`)}},
	}}
	cs := &agentloop.CodeSearchParams{PythonPath: "/x/python", ScriptPath: "/x/mcp_server.py"}
	ai := NewNativeAIWithCodeSearch(c, "m", t.TempDir(), cs)

	out, err := ai.RunPrompt(context.Background(), "review this change")
	if err == nil {
		t.Fatalf("RunPrompt should error when the loop hits the iteration cap; got %q", out)
	}
}

func TestNewReviewAI_NativeWiresCodeSearchWhenConfigured(t *testing.T) {
	t.Setenv("OPENROUTER_API_KEY", "sk-or-test")
	t.Setenv("OPENROUTER_BASE_URL", "")
	cfg := &config.Config{
		AuthMethod: "openrouter",
		CocoIndex:  config.CocoIndexConfig{DatabaseURL: "postgresql://x", VenvPath: "cocoindex/.venv", ScriptsPath: "cocoindex"},
		Tracks:     []config.TrackConfig{{Name: "backend"}},
	}
	cfg.Inspect.AgentModel = "openrouter/owl-alpha"

	ai, err := newReviewAI(cfg)
	if err != nil {
		t.Fatalf("newReviewAI: %v", err)
	}
	native, ok := ai.(*NativeAI)
	if !ok {
		t.Fatalf("newReviewAI returned %T, want *NativeAI", ai)
	}
	if native.codeSearch == nil {
		t.Error("native review AI should be wired with codesearch when CocoIndex is configured")
	}
}

func TestNewReviewAI_DefaultsToCLIProvider(t *testing.T) {
	cfg := &config.Config{}
	cfg.Inspect.AgentProvider = "claude"

	ai, err := newReviewAI(cfg)
	if err != nil {
		t.Fatalf("newReviewAI: %v", err)
	}
	if _, ok := ai.(*ProviderAI); !ok {
		t.Errorf("newReviewAI returned %T, want *ProviderAI for default auth method", ai)
	}
}

func TestNewReviewAI_SelectsNativeByAuthMethod(t *testing.T) {
	t.Setenv("OPENROUTER_API_KEY", "sk-or-test")
	t.Setenv("OPENROUTER_BASE_URL", "")
	cfg := &config.Config{AuthMethod: "openrouter"}
	cfg.Inspect.AgentModel = "openrouter/owl-alpha"

	ai, err := newReviewAI(cfg)
	if err != nil {
		t.Fatalf("newReviewAI: %v", err)
	}
	if _, ok := ai.(*NativeAI); !ok {
		t.Errorf("newReviewAI returned %T, want *NativeAI for auth_method=openrouter", ai)
	}
}

func TestNewReviewAI_NativeMissingKey(t *testing.T) {
	t.Setenv("OPENROUTER_API_KEY", "")
	cfg := &config.Config{AuthMethod: "openrouter"}

	if _, err := newReviewAI(cfg); err == nil {
		t.Fatal("expected error when native loop selected without API key, got nil")
	}
}

// TestNativeAI_MaxIterations_WiredFromConfig verifies that the config
// MaxReviewIterations value is written onto the NativeAI struct by newReviewAI.
func TestInspectNativeAI_MaxIterations_WiredFromConfig(t *testing.T) {
	t.Setenv("OPENROUTER_API_KEY", "sk-or-test")
	t.Setenv("OPENROUTER_BASE_URL", "")
	cfg := &config.Config{
		AuthMethod: "openrouter",
		CocoIndex:  config.CocoIndexConfig{DatabaseURL: "postgresql://x", VenvPath: "cocoindex/.venv", ScriptsPath: "cocoindex"},
		Tracks:     []config.TrackConfig{{Name: "backend"}},
	}
	cfg.Inspect.AgentModel = "openrouter/owl-alpha"
	cfg.Inspect.MaxReviewIterations = 42

	ai, err := newReviewAI(cfg)
	if err != nil {
		t.Fatalf("newReviewAI: %v", err)
	}
	native, ok := ai.(*NativeAI)
	if !ok {
		t.Fatalf("newReviewAI returned %T, want *NativeAI", ai)
	}
	if native.maxIterations != 42 {
		t.Errorf("maxIterations = %d, want 42 from config", native.maxIterations)
	}
}

// TestInspectNativeAI_MaxIterations_CodeSearchDefault verifies that when the config
// field is unset (0), the codesearch/deep_review path defaults to 30.
func TestInspectNativeAI_MaxIterations_CodeSearchDefault(t *testing.T) {
	c := &fakeCompleter{resp: agentloop.Response{Content: "APPROVE", FinishReason: "stop"}}
	cs := &agentloop.CodeSearchParams{PythonPath: "/x/python", ScriptPath: "/x/mcp_server.py"}
	ai := NewNativeAIWithCodeSearch(c, "m", t.TempDir(), cs)
	ai.maxIterations = 0 // simulate unset config

	out, err := ai.RunPrompt(context.Background(), "test")
	if err != nil {
		t.Fatalf("RunPrompt: %v", err)
	}
	if out != "APPROVE" {
		t.Errorf("RunPrompt = %q, want %q", out, "APPROVE")
	}
}

// TestInspectNativeAI_MaxIterations_OverrideCodeSearchDefault verifies that a
// configured value overrides the path-specific default on the codesearch path.
func TestInspectNativeAI_MaxIterations_OverrideCodeSearchDefault(t *testing.T) {
	c := &fakeCompleter{resp: agentloop.Response{Content: "APPROVE", FinishReason: "stop"}}
	cs := &agentloop.CodeSearchParams{PythonPath: "/x/python", ScriptPath: "/x/mcp_server.py"}
	ai := NewNativeAIWithCodeSearch(c, "m", t.TempDir(), cs)
	ai.maxIterations = 7 // explicit config override

	out, err := ai.RunPrompt(context.Background(), "test")
	if err != nil {
		t.Fatalf("RunPrompt: %v", err)
	}
	if out != "APPROVE" {
		t.Errorf("RunPrompt = %q, want %q", out, "APPROVE")
	}
}

// TestInspectNativeAI_MaxIterations_ToolLessDefault verifies the tool-less path works
// when maxIterations is 0 (unset config).
func TestInspectNativeAI_MaxIterations_ToolLessDefault(t *testing.T) {
	c := &fakeCompleter{resp: agentloop.Response{Content: "decision", FinishReason: "stop"}}
	ai := NewNativeAI(c, "m")
	ai.maxIterations = 0 // unset — should be irrelevant for tool-less

	out, err := ai.RunPrompt(context.Background(), "test")
	if err != nil {
		t.Fatalf("RunPrompt: %v", err)
	}
	if out != "decision" {
		t.Errorf("RunPrompt = %q, want %q", out, "decision")
	}
}

// TestNewReviewAI_MaxIterationsZeroDefaultsCorrectly verifies that when
// MaxReviewIterations is 0 (unset) in config, newReviewAI propagates the zero
// so the NativeAI can apply its path-specific default at runtime.
func TestNewReviewAI_MaxIterationsZeroDefaultsCorrectly(t *testing.T) {
	t.Setenv("OPENROUTER_API_KEY", "sk-or-test")
	t.Setenv("OPENROUTER_BASE_URL", "")
	cfg := &config.Config{
		AuthMethod: "openrouter",
		CocoIndex:  config.CocoIndexConfig{DatabaseURL: "postgresql://x", VenvPath: "cocoindex/.venv", ScriptsPath: "cocoindex"},
		Tracks:     []config.TrackConfig{{Name: "backend"}},
	}
	cfg.Inspect.AgentModel = "openrouter/owl-alpha"
	// MaxReviewIterations is 0 (default)

	ai, err := newReviewAI(cfg)
	if err != nil {
		t.Fatalf("newReviewAI: %v", err)
	}
	native, ok := ai.(*NativeAI)
	if !ok {
		t.Fatalf("newReviewAI returned %T, want *NativeAI", ai)
	}
	if native.maxIterations != 0 {
		t.Errorf("maxIterations = %d, want 0 (unset config, defer to path default)", native.maxIterations)
	}
}
