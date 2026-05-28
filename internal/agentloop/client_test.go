package agentloop

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// decodeRequestBody is a test helper that reads the raw OpenAI-compatible
// request body the Client sent.
func decodeRequestBody(t *testing.T, r *http.Request) map[string]any {
	t.Helper()
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		t.Fatalf("read request body: %v", err)
	}
	var body map[string]any
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("unmarshal request body: %v\nraw: %s", err, raw)
	}
	return body
}

func TestClient_Complete_RequestShapeAndParse(t *testing.T) {
	var gotBody map[string]any
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			t.Errorf("path = %q, want /chat/completions", r.URL.Path)
		}
		if r.Method != http.MethodPost {
			t.Errorf("method = %q, want POST", r.Method)
		}
		gotAuth = r.Header.Get("Authorization")
		gotBody = decodeRequestBody(t, r)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{
			"choices": [{"message": {"content": "the answer is 42"}, "finish_reason": "stop"}],
			"usage": {"prompt_tokens": 10, "completion_tokens": 5, "total_tokens": 15}
		}`)
	}))
	defer srv.Close()

	c := NewClient(Credentials{BaseURL: srv.URL, APIKey: "sk-test"})
	resp, err := c.Complete(context.Background(), Request{
		Model: "openrouter/owl-alpha",
		Messages: []Message{
			{Role: "system", Content: "be brief"},
			{Role: "user", Content: "what is the answer"},
		},
		Tools: []ToolDef{
			{Name: "bash", Description: "run a shell command", Parameters: json.RawMessage(`{"type":"object"}`)},
		},
	})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}

	// Response parsing.
	if resp.Content != "the answer is 42" {
		t.Errorf("Content = %q, want %q", resp.Content, "the answer is 42")
	}
	if resp.FinishReason != "stop" {
		t.Errorf("FinishReason = %q, want stop", resp.FinishReason)
	}
	if resp.Usage.PromptTokens != 10 || resp.Usage.CompletionTokens != 5 || resp.Usage.TotalTokens != 15 {
		t.Errorf("Usage = %+v, want {10 5 15}", resp.Usage)
	}

	// Auth header.
	if gotAuth != "Bearer sk-test" {
		t.Errorf("Authorization = %q, want %q", gotAuth, "Bearer sk-test")
	}

	// Request shape.
	if gotBody["model"] != "openrouter/owl-alpha" {
		t.Errorf("request model = %v, want openrouter/owl-alpha", gotBody["model"])
	}
	msgs, ok := gotBody["messages"].([]any)
	if !ok || len(msgs) != 2 {
		t.Fatalf("request messages = %v, want 2 entries", gotBody["messages"])
	}
	first := msgs[0].(map[string]any)
	if first["role"] != "system" || first["content"] != "be brief" {
		t.Errorf("messages[0] = %v, want system/be brief", first)
	}
	tools, ok := gotBody["tools"].([]any)
	if !ok || len(tools) != 1 {
		t.Fatalf("request tools = %v, want 1 entry", gotBody["tools"])
	}
	tool := tools[0].(map[string]any)
	if tool["type"] != "function" {
		t.Errorf("tool type = %v, want function", tool["type"])
	}
	fn := tool["function"].(map[string]any)
	if fn["name"] != "bash" {
		t.Errorf("tool function name = %v, want bash", fn["name"])
	}
}

func TestClient_Complete_ParsesToolCalls(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{
			"choices": [{
				"message": {
					"content": null,
					"tool_calls": [{
						"id": "call_1",
						"type": "function",
						"function": {"name": "bash", "arguments": "{\"command\":\"ls\"}"}
					}]
				},
				"finish_reason": "tool_calls"
			}],
			"usage": {"prompt_tokens": 3, "completion_tokens": 7, "total_tokens": 10}
		}`)
	}))
	defer srv.Close()

	c := NewClient(Credentials{BaseURL: srv.URL, APIKey: "sk-test"})
	resp, err := c.Complete(context.Background(), Request{Model: "m", Messages: []Message{{Role: "user", Content: "list"}}})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if resp.FinishReason != "tool_calls" {
		t.Errorf("FinishReason = %q, want tool_calls", resp.FinishReason)
	}
	if len(resp.ToolCalls) != 1 {
		t.Fatalf("ToolCalls len = %d, want 1", len(resp.ToolCalls))
	}
	tc := resp.ToolCalls[0]
	if tc.ID != "call_1" {
		t.Errorf("ToolCall.ID = %q, want call_1", tc.ID)
	}
	if tc.Name != "bash" {
		t.Errorf("ToolCall.Name = %q, want bash", tc.Name)
	}
	// Arguments are the inner JSON object, not the wire-level quoted string.
	var args struct {
		Command string `json:"command"`
	}
	if err := json.Unmarshal(tc.Arguments, &args); err != nil {
		t.Fatalf("unmarshal tool args %q: %v", tc.Arguments, err)
	}
	if args.Command != "ls" {
		t.Errorf("tool args command = %q, want ls", args.Command)
	}
}

func TestClient_Complete_AssistantToolCallsRoundTrip(t *testing.T) {
	// When an assistant message carrying tool_calls is sent back to the model,
	// the wire arguments must be a JSON string, and tool-result messages must
	// carry their tool_call_id.
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotBody = decodeRequestBody(t, r)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"choices":[{"message":{"content":"done"},"finish_reason":"stop"}]}`)
	}))
	defer srv.Close()

	c := NewClient(Credentials{BaseURL: srv.URL, APIKey: "sk-test"})
	_, err := c.Complete(context.Background(), Request{
		Model: "m",
		Messages: []Message{
			{Role: "user", Content: "list"},
			{Role: "assistant", ToolCalls: []ToolCall{{ID: "call_1", Name: "bash", Arguments: json.RawMessage(`{"command":"ls"}`)}}},
			{Role: "tool", ToolCallID: "call_1", Name: "bash", Content: "file1\nfile2"},
		},
	})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	msgs := gotBody["messages"].([]any)
	asst := msgs[1].(map[string]any)
	tcs := asst["tool_calls"].([]any)
	tc := tcs[0].(map[string]any)
	fn := tc["function"].(map[string]any)
	// arguments must be a JSON-encoded string, not a nested object.
	argStr, ok := fn["arguments"].(string)
	if !ok {
		t.Fatalf("assistant tool_call arguments = %T, want string", fn["arguments"])
	}
	if argStr != `{"command":"ls"}` {
		t.Errorf("assistant tool_call arguments = %q, want %q", argStr, `{"command":"ls"}`)
	}
	toolMsg := msgs[2].(map[string]any)
	if toolMsg["role"] != "tool" || toolMsg["tool_call_id"] != "call_1" {
		t.Errorf("tool message = %v, want role=tool tool_call_id=call_1", toolMsg)
	}
}

func TestClient_Complete_RateLimitError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "12")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = io.WriteString(w, `{"error":{"message":"slow down"}}`)
	}))
	defer srv.Close()

	c := NewClient(Credentials{BaseURL: srv.URL, APIKey: "sk-test"})
	_, err := c.Complete(context.Background(), Request{Model: "m", Messages: []Message{{Role: "user", Content: "hi"}}})
	var rle *RateLimitError
	if !errors.As(err, &rle) {
		t.Fatalf("error = %v (%T), want *RateLimitError", err, err)
	}
	if rle.RetryAfter != 12*time.Second {
		t.Errorf("RetryAfter = %v, want 12s", rle.RetryAfter)
	}
}

func TestClient_Complete_CreditError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusPaymentRequired)
		_, _ = io.WriteString(w, `{"error":{"message":"insufficient credits"}}`)
	}))
	defer srv.Close()

	c := NewClient(Credentials{BaseURL: srv.URL, APIKey: "sk-test"})
	_, err := c.Complete(context.Background(), Request{Model: "m", Messages: []Message{{Role: "user", Content: "hi"}}})
	var ce *CreditError
	if !errors.As(err, &ce) {
		t.Fatalf("error = %v (%T), want *CreditError", err, err)
	}
	if !strings.Contains(ce.Error(), "credits") {
		t.Errorf("CreditError message = %q, want to mention credits", ce.Error())
	}
}

func TestClient_Complete_RetriesOn5xxThenSucceeds(t *testing.T) {
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls < 3 {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = io.WriteString(w, `{"error":{"message":"boom"}}`)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"choices":[{"message":{"content":"ok"},"finish_reason":"stop"}]}`)
	}))
	defer srv.Close()

	c := NewClient(Credentials{BaseURL: srv.URL, APIKey: "sk-test"},
		WithMaxRetries(3), WithRetryBaseDelay(0))
	resp, err := c.Complete(context.Background(), Request{Model: "m", Messages: []Message{{Role: "user", Content: "hi"}}})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if resp.Content != "ok" {
		t.Errorf("Content = %q, want ok", resp.Content)
	}
	if calls != 3 {
		t.Errorf("server calls = %d, want 3", calls)
	}
}

func TestClient_Complete_5xxExhaustsRetries(t *testing.T) {
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(http.StatusBadGateway)
		_, _ = io.WriteString(w, `{"error":{"message":"down"}}`)
	}))
	defer srv.Close()

	c := NewClient(Credentials{BaseURL: srv.URL, APIKey: "sk-test"},
		WithMaxRetries(2), WithRetryBaseDelay(0))
	_, err := c.Complete(context.Background(), Request{Model: "m", Messages: []Message{{Role: "user", Content: "hi"}}})
	if err == nil {
		t.Fatal("expected error after exhausting retries, got nil")
	}
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("error = %v (%T), want *APIError", err, err)
	}
	if apiErr.StatusCode != http.StatusBadGateway {
		t.Errorf("APIError.StatusCode = %d, want 502", apiErr.StatusCode)
	}
	// initial attempt + 2 retries = 3 calls
	if calls != 3 {
		t.Errorf("server calls = %d, want 3", calls)
	}
}

// HTTP 200 with a JSON {"error":...} body is what stealth OpenRouter providers
// like openrouter/owl-alpha return on transient upstream hiccups. The client
// must treat this as retryable so a single bad turn doesn't bubble up to
// `ry dispatch` (forcing a user retype) or to the engine native runner (where
// it throws away the whole car cycle as outcomeClear). See railyard-0se.
func TestClient_Complete_RetriesOn200WithErrorBodyThenSucceeds(t *testing.T) {
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Content-Type", "application/json")
		if calls < 2 {
			_, _ = io.WriteString(w, `{"error":{"message":"Provider returned error"}}`)
			return
		}
		_, _ = io.WriteString(w, `{"choices":[{"message":{"content":"ok"},"finish_reason":"stop"}]}`)
	}))
	defer srv.Close()

	c := NewClient(Credentials{BaseURL: srv.URL, APIKey: "sk-test"},
		WithMaxRetries(3), WithRetryBaseDelay(0))
	resp, err := c.Complete(context.Background(), Request{Model: "m", Messages: []Message{{Role: "user", Content: "hi"}}})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if resp.Content != "ok" {
		t.Errorf("Content = %q, want ok", resp.Content)
	}
	if calls != 2 {
		t.Errorf("server calls = %d, want 2", calls)
	}
}

func TestClient_Complete_200WithErrorBodyExhaustsRetries(t *testing.T) {
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"error":{"message":"Provider returned error"}}`)
	}))
	defer srv.Close()

	c := NewClient(Credentials{BaseURL: srv.URL, APIKey: "sk-test"},
		WithMaxRetries(2), WithRetryBaseDelay(0))
	_, err := c.Complete(context.Background(), Request{Model: "m", Messages: []Message{{Role: "user", Content: "hi"}}})
	if err == nil {
		t.Fatal("expected error after exhausting retries, got nil")
	}
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("error = %v (%T), want *APIError", err, err)
	}
	if apiErr.StatusCode != http.StatusOK {
		t.Errorf("APIError.StatusCode = %d, want 200", apiErr.StatusCode)
	}
	// initial attempt + 2 retries = 3 calls
	if calls != 3 {
		t.Errorf("server calls = %d, want 3", calls)
	}
}
