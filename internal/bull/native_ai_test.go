package bull

import (
	"context"
	"errors"
	"testing"

	"github.com/zulandar/railyard/internal/agentloop"
	"github.com/zulandar/railyard/internal/config"
)

// fakeCompleter is a fake agentloop.Completer for bull tests.
type fakeCompleter struct {
	resp    agentloop.Response
	err     error
	gotReq  agentloop.Request
	gotCall bool
}

func (c *fakeCompleter) Complete(_ context.Context, req agentloop.Request) (agentloop.Response, error) {
	c.gotCall = true
	c.gotReq = req
	if c.err != nil {
		return agentloop.Response{}, c.err
	}
	return c.resp, nil
}

func TestNativeAI_RunPrompt(t *testing.T) {
	c := &fakeCompleter{resp: agentloop.Response{Content: "DECISION: approve", FinishReason: "stop"}}
	ai := NewNativeAI(c, "openrouter/owl-alpha")

	out, err := ai.RunPrompt(context.Background(), "triage this issue")
	if err != nil {
		t.Fatalf("RunPrompt: %v", err)
	}
	if out != "DECISION: approve" {
		t.Errorf("RunPrompt = %q, want %q", out, "DECISION: approve")
	}

	// One-shot: a single user message carrying the prompt, no tools.
	if c.gotReq.Model != "openrouter/owl-alpha" {
		t.Errorf("request model = %q, want owl-alpha", c.gotReq.Model)
	}
	if len(c.gotReq.Tools) != 0 {
		t.Errorf("request tools = %v, want none (one-shot completion)", c.gotReq.Tools)
	}
	if len(c.gotReq.Messages) != 1 || c.gotReq.Messages[0].Role != "user" || c.gotReq.Messages[0].Content != "triage this issue" {
		t.Errorf("request messages = %+v, want a single user message with the prompt", c.gotReq.Messages)
	}
}

func TestNativeAI_RunPrompt_TrimsWhitespace(t *testing.T) {
	c := &fakeCompleter{resp: agentloop.Response{Content: "  hello\n\n", FinishReason: "stop"}}
	ai := NewNativeAI(c, "m")

	out, err := ai.RunPrompt(context.Background(), "p")
	if err != nil {
		t.Fatalf("RunPrompt: %v", err)
	}
	if out != "hello" {
		t.Errorf("RunPrompt = %q, want trimmed %q", out, "hello")
	}
}

func TestNativeAI_RunPrompt_Error(t *testing.T) {
	c := &fakeCompleter{err: &agentloop.RateLimitError{Message: "slow down"}}
	ai := NewNativeAI(c, "m")

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
	c := &fakeCompleter{resp: agentloop.Response{Content: "DECISION: approve", FinishReason: "stop"}}
	cs := &agentloop.CodeSearchParams{PythonPath: "/x/python", ScriptPath: "/x/mcp_server.py"}
	ai := NewNativeAIWithCodeSearch(c, "m", t.TempDir(), cs)

	out, err := ai.RunPrompt(context.Background(), "triage this issue")
	if err != nil {
		t.Fatalf("RunPrompt: %v", err)
	}
	if out != "DECISION: approve" {
		t.Errorf("RunPrompt = %q, want %q", out, "DECISION: approve")
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
	// Triage may look up code (read-only) but must never get write/edit/bash.
	if !has("codesearch") || !has("read_file") {
		t.Errorf("tools = %v, want read_file + codesearch", names)
	}
	for _, forbidden := range []string{"bash", "write_file", "edit_file"} {
		if has(forbidden) {
			t.Errorf("tools = %v, must NOT expose %q to triage", names, forbidden)
		}
	}
}

func TestNewTriageAI_NativeWiresCodeSearchWhenConfigured(t *testing.T) {
	t.Setenv("OPENROUTER_API_KEY", "sk-or-test")
	t.Setenv("OPENROUTER_BASE_URL", "")
	cfg := &config.Config{
		AuthMethod: "openrouter",
		CocoIndex:  config.CocoIndexConfig{DatabaseURL: "postgresql://x", VenvPath: "cocoindex/.venv", ScriptsPath: "cocoindex"},
		Tracks:     []config.TrackConfig{{Name: "backend"}},
	}
	cfg.Bull.AgentModel = "openrouter/owl-alpha"

	ai, err := newTriageAI(cfg)
	if err != nil {
		t.Fatalf("newTriageAI: %v", err)
	}
	native, ok := ai.(*NativeAI)
	if !ok {
		t.Fatalf("newTriageAI returned %T, want *NativeAI", ai)
	}
	if native.codeSearch == nil {
		t.Error("native triage AI should be wired with codesearch when CocoIndex is configured")
	}
}

func TestNewTriageAI_DefaultsToCLIProvider(t *testing.T) {
	cfg := &config.Config{AgentProvider: "claude"}
	cfg.Bull.AgentProvider = "claude"

	ai, err := newTriageAI(cfg)
	if err != nil {
		t.Fatalf("newTriageAI: %v", err)
	}
	if _, ok := ai.(*ProviderAI); !ok {
		t.Errorf("newTriageAI returned %T, want *ProviderAI for default auth method", ai)
	}
}

func TestNewTriageAI_SelectsNativeByAuthMethod(t *testing.T) {
	t.Setenv("OPENROUTER_API_KEY", "sk-or-test")
	t.Setenv("OPENROUTER_BASE_URL", "")
	cfg := &config.Config{AuthMethod: "openrouter"}
	cfg.Bull.AgentModel = "openrouter/owl-alpha"

	ai, err := newTriageAI(cfg)
	if err != nil {
		t.Fatalf("newTriageAI: %v", err)
	}
	if _, ok := ai.(*NativeAI); !ok {
		t.Errorf("newTriageAI returned %T, want *NativeAI for auth_method=openrouter", ai)
	}
}

func TestNewTriageAI_NativeMissingKey(t *testing.T) {
	t.Setenv("OPENROUTER_API_KEY", "")
	cfg := &config.Config{AuthMethod: "openrouter"}

	if _, err := newTriageAI(cfg); err == nil {
		t.Fatal("expected error when native loop selected without API key, got nil")
	}
}
